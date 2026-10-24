package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/kavrynt/kavrynt/internal/gateway/model"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
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
	// console serves the web console under /ui/; nil disables it.
	console http.Handler
	// auth is the console admin login; nil leaves /v1/servers and
	// /v1/traffic open (SPEC-0005-R03).
	auth ConsoleAuth

	// instance names this replica (its pod name) in /v1/traffic.
	instance string
	// draining is set on shutdown so /readyz fails while requests are still
	// served (SPEC-0008-R01).
	draining atomic.Bool

	requests         atomic.Uint64
	proxied          atomic.Uint64
	strippedRequests atomic.Uint64

	traffic        *trafficMetrics
	recent         *recentTraffic
	metricsHandler http.Handler
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
		recent:         newRecentTraffic(recentTrafficSize),
	}
	h.StripRequestHeaders(credentialHeaders...)
	registry := h.newRegistry()
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
// requests, while every other request is still served (SPEC-0008-R01).
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

// EnableConsole serves console under /ui/ and redirects / to it. The console
// receives paths without the /ui prefix.
func (h *Handler) EnableConsole(console http.Handler) {
	h.console = http.StripPrefix("/ui", console)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.requests.Add(1)

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
	case r.URL.Path == "/v1/session":
		h.session(w, r)
	case strings.HasPrefix(r.URL.Path, "/mcp/"):
		h.proxy(w, r)
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

func (h *Handler) listRoutes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]model.Route{"routes": h.table.List()})
}

func (h *Handler) listServers(w http.ResponseWriter, _ *http.Request) {
	servers := h.table.Servers()
	if servers == nil {
		servers = []model.Server{}
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
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": false})
}

func (h *Handler) listTraffic(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Instance string          `json:"instance"`
		Requests []TrafficRecord `json:"requests"`
	}{h.instance, h.recent.list()})
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

	route, exists := h.table.Get(name)
	if !exists {
		writeError(w, http.StatusNotFound, fmt.Errorf("MCP server %q is not registered", name))
		return
	}

	start := time.Now()
	call, body, err := inspectRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("read request body"))
		h.observe(route.Name, call, outcomeClientError, http.StatusBadRequest, time.Since(start), 0, 0)
		return
	}
	upstreamBody := &countingReader{r: body}
	gatewayError := func(status int, err error) {
		writeError(w, status, err)
		h.observe(route.Name, call, outcomeGatewayError, status, time.Since(start), upstreamBody.n, 0)
	}

	if r.Header.Get(routeHeader) != "" {
		gatewayError(http.StatusLoopDetected, fmt.Errorf("request already passed through a Kavrynt Gateway; the endpoint of %q must not point at a Gateway", route.Name))
		return
	}
	if route.Transport != "http" {
		gatewayError(http.StatusBadGateway, fmt.Errorf("transport %q is not supported by gateway MVP", route.Transport))
		return
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
	req.Host = req.URL.Host
	req.Header.Set(routeHeader, route.Name)
	req.Header.Set("X-Forwarded-Host", r.Host)

	// #nosec G704 -- buildTargetURL restricts MCPServer-provided targets to validated HTTP(S) URLs.
	resp, err := h.client.Do(req)
	if err != nil {
		// The error names the upstream URL; callers get a fixed message.
		outcome, status, message := classifyTransportError(err), http.StatusBadGateway, "upstream request failed"
		if errors.Is(context.Cause(ctx), errRequestTimeout) {
			outcome, status, message = outcomeTimeout, http.StatusGatewayTimeout, errRequestTimeout.Error()
		}
		writeError(w, status, errors.New(message))
		h.observe(route.Name, call, outcome, status, time.Since(start), upstreamBody.n, 0)
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
	w.WriteHeader(resp.StatusCode)

	// JSON responses are also captured (bounded) to detect JSON-RPC and tool
	// errors; streamed responses (text/event-stream) are not inspected but
	// flushed as they arrive.
	var captured *captureBuffer
	var dst io.Writer = w
	switch {
	case isJSON(resp.Header.Get("Content-Type")):
		captured = &captureBuffer{limit: maxInspectBytes}
		dst = io.MultiWriter(w, captured)
	case stream:
		dst = &flushWriter{w: w, controller: http.NewResponseController(w)}
	}
	written, copyErr := io.Copy(dst, resp.Body)
	outcome := classifyResponse(resp.StatusCode, captured)
	if copyErr != nil {
		outcome = outcomeIncomplete
		if errors.Is(context.Cause(ctx), errRequestTimeout) {
			outcome = outcomeTimeout
		}
	}
	h.observe(route.Name, call, outcome, resp.StatusCode, time.Since(start), upstreamBody.n, written)
	if copyErr == nil {
		h.proxied.Add(1)
	}
}

// observe records a proxied request in the metrics and the recent traffic.
func (h *Handler) observe(route string, call mcpCall, outcome string, status int, elapsed time.Duration, bytesIn, bytesOut int64) {
	h.traffic.observe(route, call, outcome, elapsed, bytesIn, bytesOut)
	h.recent.add(route, call, outcome, status, elapsed, bytesIn, bytesOut)
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
// which is shared by every route.
var responseStrip = map[string]struct{}{"Set-Cookie": {}}

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
