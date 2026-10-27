package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	auditv1 "github.com/kavrynt/kavrynt/api/audit/v1"
	"github.com/kavrynt/kavrynt/internal/gateway/alerts"
	"github.com/kavrynt/kavrynt/internal/gateway/audit"
	"github.com/kavrynt/kavrynt/internal/gateway/clientauth"
	"github.com/kavrynt/kavrynt/internal/gateway/history"
	"github.com/kavrynt/kavrynt/internal/gateway/model"
	"github.com/kavrynt/kavrynt/internal/gateway/policy"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
	"github.com/kavrynt/kavrynt/internal/upstreamcred"
)

type Metadata struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
}

// credentialHeaders are never forwarded upstream. The MCP authorization
// specification forbids token passthrough: a token issued for the Gateway must
// not reach another resource. See docs/ADR-0003-Runtime-Authorization.md.
var credentialHeaders = []string{"Authorization", "Cookie", "Proxy-Authorization"}

// routeHeader names the route on every forwarded request. A request that
// already carries it has passed through a Gateway, so an endpoint pointing
// back at a Gateway is reported instead of looping.
const routeHeader = "X-Kavrynt-Route"

// DefaultRequestTimeout bounds a proxied request until its response is
// complete. Event-stream responses are bounded only until their headers
// arrive, so they stay open as long as the client does.
const DefaultRequestTimeout = 30 * time.Second

var errRequestTimeout = errors.New("upstream request timed out")

type Handler struct {
	table    *routing.Table
	client   *http.Client
	metadata Metadata

	// stripRequest holds canonical header names removed from proxied requests.
	stripRequest map[string]struct{}
	// allowedOrigins holds browser origins accepted besides loopback ones.
	allowedOrigins map[string]struct{}
	requestTimeout time.Duration
	// strictProtocol refuses requests that fail the MCP Streamable HTTP
	// checks; false only logs them (SPEC-0018).
	strictProtocol bool
	// console serves the web console under /ui/; nil disables it.
	console http.Handler
	// auth is the console admin login; nil leaves /v1/servers and
	// /v1/traffic open (SPEC-0005-R03).
	auth ConsoleAuth
	// clientAuth authenticates MCP clients; nil leaves /mcp/ routes open
	// (SPEC-0006-R01).
	clientAuth *clientauth.Authenticator
	// credentials holds the upstream credentials of servers that need one;
	// the zero value has none (SPEC-0011).
	credentials upstreamcred.Store

	// instance names this replica (its pod name) in /v1/traffic.
	instance string
	// draining is set on shutdown so /readyz fails while requests are still
	// served (SPEC-0008-R01).
	draining atomic.Bool

	requests         atomic.Uint64
	proxied          atomic.Uint64
	strippedRequests atomic.Uint64

	traffic *trafficMetrics
	recent  *recentTraffic
	// alertWindow holds the last ten minutes of calls for the console
	// alerts, which alerts evaluates (SPEC-0020).
	alertWindow *alerts.Window
	alerts      *alerts.Evaluator
	// history keeps 24 hours of calls for the console dashboards
	// (SPEC-0024); grafanaURL is linked there for cluster-wide history.
	history        *history.History
	grafanaURL     string
	registry       *prometheus.Registry
	metricsHandler http.Handler

	// policy decides every MCP request when set (SPEC-0012).
	policy *policy.Engine
	// filterToolsList removes the tools the policy denies from tools/list
	// answers (SPEC-0016).
	filterToolsList bool
	filteredTools   *prometheus.CounterVec
	// audit records every MCP request when set (SPEC-0013).
	audit *audit.Writer
	// exchangeWithoutAuth remembers routes logged as needing token exchange
	// without client authentication (SPEC-0015-R07).
	exchangeWithoutAuth sync.Map
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewHandler(table *routing.Table, client *http.Client, metadata Metadata) *Handler {
	if client == nil {
		client = http.DefaultClient
	}
	// A proxy passes redirects to the client. Following them would let an
	// upstream send the Gateway to any address, bypassing endpoint validation.
	proxyClient := *client
	proxyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	h := &Handler{
		table:          table,
		client:         &proxyClient,
		metadata:       metadata,
		stripRequest:   map[string]struct{}{},
		allowedOrigins: map[string]struct{}{},
		requestTimeout: DefaultRequestTimeout,
		strictProtocol: true,
		recent:         newRecentTraffic(recentTrafficSize),
		alertWindow:    alerts.NewWindow(durationBuckets),
		history:        history.New(durationBuckets, history.DefaultMaxTools, time.Now()),
	}
	h.StripRequestHeaders(credentialHeaders...)
	registry := h.newRegistry()
	h.registry = registry
	h.traffic = newTrafficMetrics(registry)
	h.metricsHandler = promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	return h
}

// StripRequestHeaders adds headers that must not be forwarded upstream, in
// addition to the credential headers that are always removed.
func (h *Handler) StripRequestHeaders(names ...string) {
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			h.stripRequest[http.CanonicalHeaderKey(name)] = struct{}{}
		}
	}
}

// SetRequestTimeout bounds proxied requests; zero or less disables the bound.
func (h *Handler) SetRequestTimeout(timeout time.Duration) {
	h.requestTimeout = timeout
}

// SetStrictProtocol sets whether requests that are not well-formed MCP
// Streamable HTTP requests are refused (true, the default) or forwarded
// with a debug log entry, for clients that are being migrated (SPEC-0018).
func (h *Handler) SetStrictProtocol(strict bool) {
	h.strictProtocol = strict
}

// AllowOrigins adds browser origins (scheme://host[:port]) whose requests are
// accepted. Loopback origins are always accepted.
func (h *Handler) AllowOrigins(origins ...string) {
	for _, origin := range origins {
		if origin = strings.TrimRight(strings.TrimSpace(origin), "/"); origin != "" {
			h.allowedOrigins[strings.ToLower(origin)] = struct{}{}
		}
	}
}

// SetInstance names this replica in /v1/traffic, whose recent traffic is per
// replica (SPEC-0008-R06).
func (h *Handler) SetInstance(name string) {
	h.instance = name
}

// SetDraining makes /readyz answer 503 so the Service stops sending new
// requests, while every other request is still served, each closing its
// connection (SPEC-0008-R01).
func (h *Handler) SetDraining() {
	h.draining.Store(true)
}

// ConsoleAuth is the console admin login (internal/gateway/auth).
type ConsoleAuth interface {
	http.Handler
	Authorized(*http.Request) bool
}

// EnableConsoleAuth requires a console session for the endpoints the console
// reads and serves /v1/session. MCP routes, /v1/routes, health, version, and
// metrics are unaffected: the login is not MCP client authentication.
func (h *Handler) EnableConsoleAuth(auth ConsoleAuth) {
	h.auth = auth
}

// EnableClientAuth requires a valid bearer token for the route on every
// /mcp/ request and serves Protected Resource Metadata (SPEC-0006).
func (h *Handler) EnableClientAuth(a *clientauth.Authenticator) {
	h.clientAuth = a
}

// EnableUpstreamCredentials sends each server's stored credential, from the
// mounted upstream credentials Secret, to servers that declare one
// (SPEC-0011).
func (h *Handler) EnableUpstreamCredentials(store upstreamcred.Store) {
	h.credentials = store
}

// EnablePolicy has engine decide every MCP request before it is forwarded,
// and exports its metrics (SPEC-0012).
func (h *Handler) EnablePolicy(engine *policy.Engine) {
	h.policy = engine
	h.filteredTools = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kavrynt_gateway_policy_filtered_tools_total",
		Help: "Tools removed from tools/list answers because the policy denies calling them.",
	}, []string{"route"})
	h.registry.MustRegister(engine.Collectors()...)
	h.registry.MustRegister(h.filteredTools)
}

// EnableToolsListFilter makes tools/list answers list only the tools the
// policy lets the caller call (SPEC-0016). It needs EnablePolicy.
func (h *Handler) EnableToolsListFilter() {
	h.filterToolsList = true
}

// EnableAudit writes one audit event per MCP request (SPEC-0013).
func (h *Handler) EnableAudit(w *audit.Writer) {
	h.audit = w
	h.registry.MustRegister(w.Collectors()...)
}

// EnableAlerts evaluates the console alerts over this replica's calls with
// the thresholds config returns, and serves them at /v1/alerts (SPEC-0020).
// The caller runs the returned evaluator.
func (h *Handler) EnableAlerts(config func() alerts.Config) *alerts.Evaluator {
	h.alerts = alerts.NewEvaluator(h.alertWindow, config)
	return h.alerts
}

// EnableAuditDelivery exports the metrics of the audit delivery to Kavrynt
// Cloud (SPEC-0017-R05).
func (h *Handler) EnableAuditDelivery(s *audit.Shipper) {
	h.registry.MustRegister(s.Collectors()...)
}

// EnableConsole serves console under /ui/ and redirects / to it. The console
// receives paths without the /ui prefix.
func (h *Handler) EnableConsole(console http.Handler) {
	h.console = http.StripPrefix("/ui", console)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.requests.Add(1)
	if h.draining.Load() {
		// Close each connection after its response, so clients move their
		// next request to a replica that stays. Closing idle connections
		// instead would race with requests already being sent on them.
		w.Header().Set("Connection", "close")
	}

	// Browsers send Origin on cross-origin and state-changing requests. The
	// Gateway does not authenticate callers yet, so a web page must not be
	// able to drive MCP calls through it (MCP requires servers to validate
	// Origin against DNS rebinding). Clients that are not browsers send none.
	if !h.originAllowed(r.Header.Get("Origin")) {
		writeError(w, http.StatusForbidden, errors.New("origin not allowed; add it with --allowed-origins"))
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		h.health(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/readyz":
		h.ready(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/version":
		h.version(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/metrics":
		h.metrics(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/routes":
		h.listRoutes(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/servers":
		if h.authorized(w, r) {
			h.listServers(w, r)
		}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/traffic":
		if h.authorized(w, r) {
			h.listTraffic(w, r)
		}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/alerts":
		if h.authorized(w, r) {
			h.listAlerts(w, r)
		}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/history":
		if h.authorized(w, r) {
			h.listHistory(w, r)
		}
	case r.URL.Path == "/v1/session":
		h.session(w, r)
	case h.clientAuth != nil && r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, metadataPrefix):
		h.protectedResource(w, r)
	case strings.HasPrefix(r.URL.Path, "/mcp/"):
		h.serveMCP(w, r)
	case h.console != nil && r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/ui"):
		http.Redirect(w, r, "/ui/", http.StatusFound)
	case h.console != nil && strings.HasPrefix(r.URL.Path, "/ui/"):
		h.console.ServeHTTP(w, r)
	default:
		writeError(w, http.StatusNotFound, errors.New("not found"))
	}
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) ready(w http.ResponseWriter, _ *http.Request) {
	ready, lastSync, lastError, routes := h.table.Status()
	if h.draining.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "draining", "routes": routes})
		return
	}
	if !ready {
		status := map[string]any{
			"status":    "not ready",
			"routes":    routes,
			"lastError": lastError,
		}
		writeJSON(w, http.StatusServiceUnavailable, status)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ready",
		"routes":   routes,
		"lastSync": lastSync.Format(time.RFC3339),
	})
}

func (h *Handler) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.metadata)
}

func (h *Handler) metrics(w http.ResponseWriter, r *http.Request) {
	h.metricsHandler.ServeHTTP(w, r)
}

// routeView is a route in /v1/routes. Endpoints never include credentials,
// a query, or a fragment (SPEC-0006-R18), and are left out when MCP client
// authentication is on, so unauthenticated callers do not learn internal
// addresses (SPEC-0006-R19).
type routeView struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Transport string `json:"transport"`
	Endpoint  string `json:"endpoint,omitempty"`
}

func (h *Handler) listRoutes(w http.ResponseWriter, _ *http.Request) {
	routes := []routeView{}
	for _, route := range h.table.List() {
		view := routeView{Name: route.Name, Version: route.Version, Transport: route.Transport}
		if h.clientAuth == nil {
			view.Endpoint = redactEndpoint(route.Endpoint)
		}
		routes = append(routes, view)
	}
	writeJSON(w, http.StatusOK, map[string][]routeView{"routes": routes})
}

// redactEndpoint drops userinfo, query, and fragment from an endpoint.
func redactEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
	return u.String()
}

const metadataPrefix = "/.well-known/oauth-protected-resource/mcp/"

// protectedResource serves the RFC 9728 metadata of a routed server, without
// authentication (SPEC-0006-R07).
func (h *Handler) protectedResource(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, metadataPrefix)
	if _, exists := h.table.Get(name); !exists || strings.Contains(name, "/") {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	writeJSON(w, http.StatusOK, h.clientAuth.Metadata(name))
}

func (h *Handler) listServers(w http.ResponseWriter, _ *http.Request) {
	// A copy: the table's slice is shared with other requests.
	servers := append([]model.Server{}, h.table.Servers()...)
	// Availability depends on the mounted Secret, which changes on its own,
	// so it is checked per request; the value is never read into the view.
	for i := range servers {
		if servers[i].Credential != nil {
			servers[i].Credential = &model.ServerCredential{
				Required:  true,
				Available: h.credentials.Available(servers[i].Route, servers[i].Endpoint),
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string][]model.Server{"servers": servers})
}

// authorized reports whether r may read console data, answering 401 when
// it may not.
func (h *Handler) authorized(w http.ResponseWriter, r *http.Request) bool {
	if h.auth == nil || h.auth.Authorized(r) {
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	writeError(w, http.StatusUnauthorized, errors.New("login required"))
	return false
}

// session serves /v1/session. Without console login it only reports that
// login is off.
func (h *Handler) session(w http.ResponseWriter, r *http.Request) {
	if h.auth != nil {
		h.auth.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusNotFound, errors.New("console login is disabled"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": false, "mcpAuth": h.clientAuth != nil})
}

func (h *Handler) listTraffic(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Instance string          `json:"instance"`
		Requests []TrafficRecord `json:"requests"`
	}{h.instance, h.recent.list()})
}

// alertsView is the answer of /v1/alerts (SPEC-0020-R05).
type alertsView struct {
	// Instance is the replica whose calls the traffic alerts count.
	Instance    string         `json:"instance"`
	EvaluatedAt *time.Time     `json:"evaluatedAt,omitempty"`
	Alerts      []alerts.Alert `json:"alerts"`
}

func (h *Handler) listAlerts(w http.ResponseWriter, _ *http.Request) {
	view := alertsView{Instance: h.instance, Alerts: []alerts.Alert{}}
	if h.alerts != nil {
		list, evaluated := h.alerts.List()
		view.Alerts = list
		if !evaluated.IsZero() {
			view.EvaluatedAt = &evaluated
		}
	}
	writeJSON(w, http.StatusOK, view)
}

// originAllowed accepts requests without an Origin header, loopback origins
// (a port-forwarded Gateway or console), and configured origins.
func (h *Handler) originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return false
	}
	if host := parsed.Hostname(); host == "localhost" || isLoopbackIP(host) {
		return true
	}
	_, ok := h.allowedOrigins[strings.ToLower(parsed.Scheme+"://"+parsed.Host)]
	return ok
}

func isLoopbackIP(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (h *Handler) proxy(w http.ResponseWriter, r *http.Request) {
	name, suffix, ok := parseMCPPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	// The suffix is appended to the endpoint path; dot segments would let a
	// caller leave it for other paths on the same host.
	if hasDotSegment(suffix) {
		writeError(w, http.StatusBadRequest, errors.New("path must not contain . or .. segments"))
		return
	}

	start := time.Now()
	route, exists := h.table.Get(name)
	if h.clientAuth != nil {
		// Authentication runs before the route is looked up, so callers
		// without a valid token cannot tell registered routes from others.
		identity, refusal := h.authenticate(r, name)
		if refusal != nil {
			if rec := auditRecordFrom(r); rec != nil {
				rec.decision, rec.reason = refusalDecision(refusal.Outcome, refusal.Status), refusal.Message
			}
			writeRefusal(w, refusal)
			if exists {
				call, _, _ := inspectRequest(r)
				h.finish(r, route.Name, call, refusalOutcome(refusal.Outcome), refusal.Status, time.Since(start), 0, 0)
			}
			return
		}
		r = r.WithContext(clientauth.WithIdentity(r.Context(), identity))
		if rec := auditRecordFrom(r); rec != nil {
			rec.identity = identity
		}
	}
	if !exists {
		writeError(w, http.StatusNotFound, fmt.Errorf("MCP server %q is not registered", name))
		return
	}

	call, body, err := inspectRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("read request body"))
		h.finish(r, route.Name, call, outcomeClientError, http.StatusBadRequest, time.Since(start), 0, 0)
		return
	}
	upstreamBody := &countingReader{r: body}
	gatewayError := func(status int, err error) {
		writeError(w, status, err)
		h.finish(r, route.Name, call, outcomeGatewayError, status, time.Since(start), upstreamBody.n, 0)
	}
	if err := checkProtocol(r, call.body); err != nil {
		if h.strictProtocol {
			if rec := auditRecordFrom(r); rec != nil {
				rec.decision, rec.reason = auditv1.DecisionInvalid, err.Error()
			}
			if r.Method == http.MethodPost {
				writeRPCError(w, http.StatusBadRequest, call.body.id, jsonRPCInvalidRequest, err.Error())
			} else {
				writeError(w, http.StatusBadRequest, err)
			}
			h.finish(r, route.Name, call, outcomeProtocolError, http.StatusBadRequest, time.Since(start), 0, 0)
			return
		}
		slog.Debug("forwarding a request that fails the MCP protocol checks", "route", route.Name, "error", err)
	}

	if r.Header.Get(routeHeader) != "" {
		gatewayError(http.StatusLoopDetected, fmt.Errorf("request already passed through a Kavrynt Gateway; the endpoint of %q must not point at a Gateway", route.Name))
		return
	}
	if route.Transport != "http" {
		gatewayError(http.StatusBadGateway, fmt.Errorf("transport %q is not supported by gateway MVP", route.Transport))
		return
	}
	if h.policy != nil && !h.authorize(w, r, route.Name, call, start, upstreamBody) {
		return
	}
	var filter *toolsFilter
	if h.policy != nil && h.filterToolsList && r.Method == http.MethodPost && call.body.classified && call.body.method == "tools/list" {
		filter = &toolsFilter{decide: h.policy.Decide, input: policyInput(r, route.Name), id: call.body.id}
	}

	target, err := buildTargetURL(route.Endpoint, suffix, r.URL.RawQuery)
	if err != nil {
		gatewayError(http.StatusBadGateway, err)
		return
	}

	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	var timer *time.Timer
	if h.requestTimeout > 0 {
		timer = time.AfterFunc(h.requestTimeout, func() { cancel(errRequestTimeout) })
		defer timer.Stop()
	}

	var forward io.Reader = upstreamBody
	if body == http.NoBody {
		forward = http.NoBody
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, forward)
	if err != nil {
		gatewayError(http.StatusBadGateway, err)
		return
	}
	if forward != http.NoBody {
		req.ContentLength = r.ContentLength
	}
	if copyHeaders(req.Header, r.Header, h.stripRequest) {
		h.strippedRequests.Add(1)
	}
	if filter != nil {
		// The transport then asks for gzip and decodes it, so the filter
		// reads the answer uncompressed.
		req.Header.Del("Accept-Encoding")
	}
	if route.Credential != nil {
		// The stored credential replaces any header of that name the client
		// sent; without it the request is not forwarded (SPEC-0011-R05, R06).
		value, err := h.credentials.Lookup(route.Name, route.Endpoint)
		if err != nil {
			if rec := auditRecordFrom(r); rec != nil {
				rec.decision, rec.reason = auditv1.DecisionUnavailable, "upstream credential unavailable"
			}
			writeError(w, http.StatusServiceUnavailable, errors.New("upstream credential unavailable"))
			h.finish(r, route.Name, call, outcomeCredentialUnavailable, http.StatusServiceUnavailable, time.Since(start), upstreamBody.n, 0)
			return
		}
		req.Header.Set(route.Credential.HeaderName(), route.Credential.HeaderValue(value))
	}
	if route.TokenExchange != nil {
		// Only the exchanged token reaches the server; the caller's was
		// stripped with the other credential headers (SPEC-0015-R03).
		token, status, err := h.exchangeToken(r, route)
		if err != nil {
			if rec := auditRecordFrom(r); rec != nil {
				rec.decision, rec.reason = auditv1.DecisionUnavailable, err.Error()
			}
			writeError(w, status, err)
			h.finish(r, route.Name, call, outcomeExchangeFailed, status, time.Since(start), upstreamBody.n, 0)
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Host = req.URL.Host
	req.Header.Set(routeHeader, route.Name)
	req.Header.Set("X-Forwarded-Host", r.Host)

	// #nosec G704 -- buildTargetURL restricts MCPServer-provided targets to validated HTTP(S) URLs.
	if rec := auditRecordFrom(r); rec != nil {
		rec.decision = auditv1.DecisionAllow
	}
	resp, err := h.client.Do(req)
	if err != nil {
		// The error names the upstream URL; callers get a fixed message.
		outcome, status, message := classifyTransportError(err), http.StatusBadGateway, "upstream request failed"
		if errors.Is(context.Cause(ctx), errRequestTimeout) {
			outcome, status, message = outcomeTimeout, http.StatusGatewayTimeout, errRequestTimeout.Error()
		}
		writeError(w, status, errors.New(message))
		h.finish(r, route.Name, call, outcome, status, time.Since(start), upstreamBody.n, 0)
		return
	}
	defer resp.Body.Close()

	stream := isEventStream(resp.Header.Get("Content-Type"))
	if stream && timer != nil {
		timer.Stop()
	}

	copyHeaders(w.Header(), resp.Header, responseStrip)
	// Every route shares the Gateway origin, which also serves the console.
	// Responses render in a sandbox (an opaque origin, no scripts) so an
	// upstream cannot run code as the Gateway origin.
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if filter != nil {
		written, removed, copyErr := h.serveFilteredToolsList(w, r, route.Name, filter, resp)
		if rec := auditRecordFrom(r); rec != nil {
			rec.filteredTools = removed
		}
		outcome := classifyResponse(r, call, resp.StatusCode, nil)
		if copyErr != nil {
			outcome = outcomeIncomplete
		}
		h.finish(r, route.Name, call, outcome, resp.StatusCode, time.Since(start), upstreamBody.n, written)
		if copyErr == nil {
			h.proxied.Add(1)
		}
		return
	}
	w.WriteHeader(resp.StatusCode)

	// JSON responses are also captured (bounded) to detect JSON-RPC and tool
	// errors. Streamed responses (text/event-stream) are flushed as they
	// arrive; those to a POST are read event by event for the same.
	var captured *captureBuffer
	var observer *streamObserver
	var dst io.Writer = w
	switch {
	case isJSON(resp.Header.Get("Content-Type")):
		captured = &captureBuffer{limit: maxInspectBytes}
		dst = io.MultiWriter(w, captured)
	case stream:
		dst = &flushWriter{w: w, controller: http.NewResponseController(w)}
		if r.Method == http.MethodPost {
			observer = &streamObserver{id: call.body.id}
			dst = io.MultiWriter(dst, observer)
		}
	}
	written, copyErr := io.Copy(dst, resp.Body)
	var message []byte
	switch {
	case captured != nil && !captured.truncated:
		message = captured.Bytes()
	case observer != nil:
		observer.finish()
		message = observer.response
	}
	outcome := classifyResponse(r, call, resp.StatusCode, message)
	if copyErr != nil {
		outcome = outcomeIncomplete
		if errors.Is(context.Cause(ctx), errRequestTimeout) {
			outcome = outcomeTimeout
		}
	}
	h.finish(r, route.Name, call, outcome, resp.StatusCode, time.Since(start), upstreamBody.n, written)
	if copyErr == nil {
		h.proxied.Add(1)
	}
}

// authorize asks the policy whether the request may reach the MCP server,
// and answers it when not (SPEC-0012). It reports whether to forward.
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, routeName string, call mcpCall, start time.Time, body *countingReader) bool {
	rec := auditRecordFrom(r)
	if rec != nil {
		rec.policyRevision = h.policy.Revision()
	}
	refuse := func(err error) bool {
		if rec != nil {
			rec.decision, rec.reason = auditv1.DecisionInvalid, err.Error()
		}
		writeError(w, http.StatusBadRequest, err)
		h.finish(r, routeName, call, outcomeClientError, http.StatusBadRequest, time.Since(start), body.n, 0)
		return false
	}
	input := policyInput(r, routeName)
	if r.Method == http.MethodPost {
		// The policy decides on the body's JSON-RPC message, so a body it
		// cannot read is refused (SPEC-0012-R09), and headers that name
		// another method or tool are refused (SPEC-0012-R10).
		b := call.body
		switch {
		case !b.read || !b.classified:
			return refuse(errors.New("the request is not a single JSON-RPC message the Gateway can read (at most 1 MiB); the policy cannot decide on it"))
		case b.method == "tools/call" && !b.toolOK:
			return refuse(errors.New("tools/call needs a string params.name"))
		case r.Header.Get("Mcp-Method") != "" && r.Header.Get("Mcp-Method") != b.method:
			return refuse(errors.New("the Mcp-Method header does not match the request body"))
		case r.Header.Get("Mcp-Name") != "" && b.method == "tools/call" && r.Header.Get("Mcp-Name") != b.tool:
			return refuse(errors.New("the Mcp-Name header does not match the request body"))
		}
		input.MCPMethod = b.method
		if b.method == "tools/call" {
			input.Tool = b.tool
		}
	}

	decision := h.policy.Decide(r.Context(), input)
	if decision.Allow {
		return true
	}
	message := "denied by policy"
	if decision.Reason != "" {
		message += ": " + decision.Reason
	}
	if rec != nil {
		rec.decision, rec.reason = auditv1.DecisionDeny, decision.Reason
	}
	w.Header().Set("Cache-Control", "no-store")
	writeRPCError(w, http.StatusForbidden, call.body.id, policyDeniedCode, message)
	h.finish(r, routeName, call, outcomeDenied, http.StatusForbidden, time.Since(start), body.n, 0)
	return false
}

// exchangeToken returns the token for route in exchange for the caller's,
// or the status and fixed message to answer with (SPEC-0015-R06, R07).
func (h *Handler) exchangeToken(r *http.Request, route model.Route) (string, int, error) {
	if h.clientAuth == nil {
		if _, logged := h.exchangeWithoutAuth.LoadOrStore(route.Name, true); !logged {
			slog.Error("MCP server needs token exchange, which needs MCP client authentication; its calls are refused", "route", route.Name)
		}
		return "", http.StatusServiceUnavailable, errors.New("token exchange needs MCP client authentication")
	}
	identity, _ := clientauth.IdentityFrom(r.Context())
	token, err := h.clientAuth.Exchange(r, identity, route.TokenExchange.Audience, route.TokenExchange.Scopes)
	switch {
	case errors.Is(err, clientauth.ErrExchangeUnreachable):
		return "", http.StatusServiceUnavailable, errors.New("token exchange unavailable")
	case err != nil:
		return "", http.StatusBadGateway, errors.New("token exchange failed")
	}
	return token, 0, nil
}

// policyInput is the policy input of a request without its MCP method and
// tool: the verified caller, the route, the HTTP method, and the time.
func policyInput(r *http.Request, routeName string) policy.Input {
	input := policy.Input{HTTPMethod: r.Method, Time: time.Now().UTC().Format(time.RFC3339)}
	input.Route.Namespace, input.Route.Name, _ = strings.Cut(routeName, ".")
	if identity, ok := clientauth.IdentityFrom(r.Context()); ok {
		input.Subject, input.ClientID, input.Issuer, input.Scopes = identity.Subject, identity.ClientID, identity.Issuer, identity.Scopes
	}
	return input
}

// policyDeniedCode is the JSON-RPC error code of a policy denial, in the
// range JSON-RPC reserves for server errors.
const policyDeniedCode = -32001

// jsonRPCInvalidRequest is the JSON-RPC error code of a request that is not
// a valid JSON-RPC request.
const jsonRPCInvalidRequest = -32600

// writeRPCError answers a JSON-RPC request with an error object; id is the
// request's id, null when unknown.
func writeRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	writeJSON(w, status, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
}

// authenticate refuses tokens sent outside the Authorization header
// (SPEC-0006-R04), then verifies the bearer token for the route.
func (h *Handler) authenticate(r *http.Request, route string) (clientauth.Identity, *clientauth.Refusal) {
	if tokenOutsideHeader(r) {
		return clientauth.Identity{}, h.clientAuth.InvalidRequest(route)
	}
	return h.clientAuth.Authenticate(r, route)
}

// tokenOutsideHeader reports an access_token query parameter or form field.
// A form body is read (bounded) and put back for the request.
func tokenOutsideHeader(r *http.Request) bool {
	if r.URL.Query().Has("access_token") {
		return true
	}
	if r.Body == nil || r.Body == http.NoBody {
		return false
	}
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/x-www-form-urlencoded" {
		return false
	}
	prefix, err := io.ReadAll(io.LimitReader(r.Body, maxInspectBytes))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(prefix), r.Body), r.Body}
	if err != nil {
		return false
	}
	form, err := url.ParseQuery(string(prefix))
	return err == nil && form.Has("access_token")
}

// refusalOutcome keeps metric labels to the known outcomes.
func refusalOutcome(outcome string) string {
	switch outcome {
	case outcomeUnauthenticated, outcomeForbidden, outcomeAuthUnavailable:
		return outcome
	default:
		return outcomeGatewayError
	}
}

func writeRefusal(w http.ResponseWriter, refusal *clientauth.Refusal) {
	w.Header().Set("WWW-Authenticate", refusal.WWWAuthenticate)
	w.Header().Set("Cache-Control", "no-store")
	if refusal.RetryAfter != "" {
		w.Header().Set("Retry-After", refusal.RetryAfter)
	}
	writeError(w, refusal.Status, errors.New(refusal.Message))
}

// observe records a proxied request in the metrics, the recent traffic,
// the console alerts' window, and the dashboards' history.
func (h *Handler) observe(route string, call mcpCall, outcome string, status int, elapsed time.Duration, bytesIn, bytesOut int64) {
	method, tool := h.traffic.observe(route, call, outcome, elapsed, bytesIn, bytesOut)
	h.recent.add(route, call, outcome, status, elapsed, bytesIn, bytesOut)
	now := time.Now()
	h.alertWindow.Record(now, route, method, tool, outcome, elapsed)
	h.history.Record(now, route, method, tool, outcome, elapsed, bytesIn, bytesOut)
}

func parseMCPPath(path string) (string, string, bool) {
	trimmed := strings.TrimPrefix(path, "/mcp/")
	if trimmed == "" || trimmed == path {
		return "", "", false
	}
	name, suffix, found := strings.Cut(trimmed, "/")
	if strings.TrimSpace(name) == "" {
		return "", "", false
	}
	if !found {
		return name, "", true
	}
	return name, "/" + suffix, true
}

func hasDotSegment(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func buildTargetURL(endpoint, suffix, rawQuery string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("route endpoint must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("route endpoint scheme must be http or https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("route endpoint must not contain user information")
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("route endpoint must not contain a fragment")
	}
	if suffix != "" {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + suffix
		parsed.RawPath = ""
	}
	if rawQuery != "" {
		parsed.RawQuery = rawQuery
	}
	return parsed.String(), nil
}

// flushWriter flushes after every write so server-sent events reach the
// client as the upstream sends them.
type flushWriter struct {
	w          io.Writer
	controller *http.ResponseController
}

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err == nil {
		err = f.controller.Flush()
	}
	return n, err
}

// responseStrip keeps upstream cookies from being set on the Gateway origin,
// which is shared by every route, and hides the upstream's software.
var responseStrip = map[string]struct{}{"Set-Cookie": {}, "Server": {}, "X-Powered-By": {}}

// copyHeaders copies end-to-end headers from src to dst. It drops hop-by-hop
// headers, headers named in Connection, and every header in strip. It reports
// whether any header in strip was present.
func copyHeaders(dst, src http.Header, strip map[string]struct{}) bool {
	connectionScoped := map[string]struct{}{}
	for _, value := range src.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if token = strings.TrimSpace(token); token != "" {
				connectionScoped[http.CanonicalHeaderKey(token)] = struct{}{}
			}
		}
	}

	stripped := false
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		if _, drop := strip[canonical]; drop {
			stripped = true
			continue
		}
		if _, drop := connectionScoped[canonical]; drop || isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	return stripped
}

func isHopByHopHeader(name string) bool {
	switch strings.ToLower(name) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorResponse{Error: err.Error()})
}
