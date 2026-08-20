package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kavrynt/gateway/internal/model"
	"github.com/kavrynt/gateway/internal/registry"
	"github.com/kavrynt/gateway/internal/routing"
)

type Metadata struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
}

type Handler struct {
	table    *routing.Table
	registry *registry.Client
	client   *http.Client
	metadata Metadata

	requests     atomic.Uint64
	proxied      atomic.Uint64
	syncSuccess  atomic.Uint64
	syncFailures atomic.Uint64
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewHandler(table *routing.Table, registry *registry.Client, client *http.Client, metadata Metadata) *Handler {
	if client == nil {
		client = http.DefaultClient
	}
	return &Handler{
		table:    table,
		registry: registry,
		client:   client,
		metadata: metadata,
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

func (h *Handler) SyncOnce(ctx context.Context) error {
	records, err := h.registry.ListServers(ctx)
	if err != nil {
		h.syncFailures.Add(1)
		h.table.MarkSyncError(err)
		return err
	}
	h.table.Replace(records, time.Now())
	h.syncSuccess.Add(1)
	return nil
}

func (h *Handler) StartSync(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Second
	}

	_ = h.SyncOnce(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = h.SyncOnce(ctx)
		}
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

func (h *Handler) metrics(w http.ResponseWriter, _ *http.Request) {
	_, _, _, routes := h.table.Status()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "kavrynt_gateway_requests_total %d\n", h.requests.Load())
	_, _ = fmt.Fprintf(w, "kavrynt_gateway_proxied_requests_total %d\n", h.proxied.Load())
	_, _ = fmt.Fprintf(w, "kavrynt_gateway_registry_sync_success_total %d\n", h.syncSuccess.Load())
	_, _ = fmt.Fprintf(w, "kavrynt_gateway_registry_sync_failure_total %d\n", h.syncFailures.Load())
	_, _ = fmt.Fprintf(w, "kavrynt_gateway_routes %d\n", routes)
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
	if route.Transport != "http" {
		writeError(w, http.StatusBadGateway, fmt.Errorf("transport %q is not supported by gateway MVP", route.Transport))
		return
	}

	target, err := buildTargetURL(route.Endpoint, suffix, r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	copyHeaders(req.Header, r.Header)
	req.Host = req.URL.Host
	req.Header.Set("X-Kavrynt-Route", route.Name)
	req.Header.Set("X-Forwarded-Host", r.Host)

	// #nosec G704 -- buildTargetURL restricts Registry-provided targets to validated HTTP(S) URLs.
	resp, err := h.client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		return
	}
	h.proxied.Add(1)
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

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
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
