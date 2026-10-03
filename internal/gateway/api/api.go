package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

type Handler struct {
	table    *routing.Table
	client   *http.Client
	metadata Metadata

	// stripRequest holds canonical header names removed from proxied requests.
	stripRequest map[string]struct{}

	requests         atomic.Uint64
	proxied          atomic.Uint64
	strippedRequests atomic.Uint64

	traffic        *trafficMetrics
	metricsHandler http.Handler
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewHandler(table *routing.Table, client *http.Client, metadata Metadata) *Handler {
	if client == nil {
		client = http.DefaultClient
	}
	h := &Handler{
		table:        table,
		client:       client,
		metadata:     metadata,
		stripRequest: map[string]struct{}{},
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

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.requests.Add(1)

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
	case strings.HasPrefix(r.URL.Path, "/mcp/"):
		h.proxy(w, r)
	default:
		writeError(w, http.StatusNotFound, errors.New("not found"))
	}
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) ready(w http.ResponseWriter, _ *http.Request) {
	ready, lastSync, lastError, routes := h.table.Status()
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

func (h *Handler) proxy(w http.ResponseWriter, r *http.Request) {
	name, suffix, ok := parseMCPPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("not found"))
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
		h.traffic.observe(route.Name, call, outcomeClientError, time.Since(start), 0, 0)
		return
	}
	upstreamBody := &countingReader{r: body}
	gatewayError := func(status int, err error) {
		writeError(w, status, err)
		h.traffic.observe(route.Name, call, outcomeGatewayError, time.Since(start), upstreamBody.n, 0)
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

	var forward io.Reader = upstreamBody
	if body == http.NoBody {
		forward = http.NoBody
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, forward)
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
	req.Header.Set("X-Kavrynt-Route", route.Name)
	req.Header.Set("X-Forwarded-Host", r.Host)

	// #nosec G704 -- buildTargetURL restricts MCPServer-provided targets to validated HTTP(S) URLs.
	resp, err := h.client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		h.traffic.observe(route.Name, call, classifyTransportError(err), time.Since(start), upstreamBody.n, 0)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header, responseStrip)
	w.WriteHeader(resp.StatusCode)

	// JSON responses are also captured (bounded) to detect JSON-RPC and tool
	// errors; streamed responses (text/event-stream) are not inspected.
	var captured *captureBuffer
	var dst io.Writer = w
	if isJSON(resp.Header.Get("Content-Type")) {
		captured = &captureBuffer{limit: maxInspectBytes}
		dst = io.MultiWriter(w, captured)
	}
	written, copyErr := io.Copy(dst, resp.Body)
	outcome := classifyResponse(resp.StatusCode, captured)
	if copyErr != nil {
		outcome = outcomeIncomplete
	}
	h.traffic.observe(route.Name, call, outcome, time.Since(start), upstreamBody.n, written)
	if copyErr == nil {
		h.proxied.Add(1)
	}
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
	}
	if rawQuery != "" {
		parsed.RawQuery = rawQuery
	}
	return parsed.String(), nil
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
