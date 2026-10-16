package api

// MCP traffic metrics (kavrynt-cloud LLD-0002). The Gateway records metadata
// only: route, JSON-RPC method, tool name, outcome, latency, and sizes. It never
// records arguments, results, or bodies.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

const (
	// maxInspectBytes bounds how much of a request or JSON response body the
	// Gateway reads to classify a call. Larger bodies are forwarded unchanged
	// and classified as "unknown".
	maxInspectBytes = 1 << 20

	maxToolsPerRoute   = 200
	maxMethodsPerRoute = 50
	maxMethodLength    = 64
	maxToolLength      = 128

	labelNone    = "none"
	labelUnknown = "unknown"
	labelInvalid = "invalid"
	labelOther   = "other"
)

// Outcomes of a proxied MCP request.
const (
	outcomeOK                  = "ok"
	outcomeToolError           = "tool_error"
	outcomeRPCError            = "rpc_error"
	outcomeClientError         = "client_error"
	outcomeUpstreamError       = "upstream_error"
	outcomeUpstreamUnreachable = "upstream_unreachable"
	outcomeTimeout             = "timeout"
	outcomeIncomplete          = "incomplete"
	outcomeGatewayError        = "gateway_error"
)

var labelPattern = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

// mcpCall identifies what a client asked an MCP server to do.
type mcpCall struct {
	method string
	tool   string
}

// inspectRequest classifies a request from the MCP headers or, for POST, from
// the JSON-RPC body. It returns a body that replays everything it read.
func inspectRequest(r *http.Request) (mcpCall, io.Reader, error) {
	call := mcpCall{method: r.Header.Get("Mcp-Method"), tool: r.Header.Get("Mcp-Name")}

	if r.Body == nil || r.Body == http.NoBody {
		if call.method == "" {
			call.method = "http_" + strings.ToLower(r.Method)
		}
		return call, http.NoBody, nil
	}
	if r.Method != http.MethodPost {
		if call.method == "" {
			call.method = "http_" + strings.ToLower(r.Method)
		}
		return call, r.Body, nil
	}

	prefix, err := io.ReadAll(io.LimitReader(r.Body, maxInspectBytes+1))
	if err != nil {
		return call, nil, err
	}
	body := io.MultiReader(bytes.NewReader(prefix), r.Body)
	if call.method != "" {
		return call, body, nil
	}

	call.method = labelUnknown
	if len(prefix) > maxInspectBytes {
		return call, body, nil
	}
	var message struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(prefix, &message) != nil || message.Method == "" {
		return call, body, nil
	}
	call.method = message.Method
	if message.Method == "tools/call" && call.tool == "" {
		var params struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(message.Params, &params) == nil {
			call.tool = params.Name
		}
	}
	return call, body, nil
}

// classifyResponse maps an upstream response to an outcome. body holds at
// most maxInspectBytes of a JSON response; nil when not captured.
func classifyResponse(status int, body *captureBuffer) string {
	switch {
	case status >= 500:
		return outcomeUpstreamError
	case status >= 400:
		return outcomeClientError
	}
	if body == nil || body.truncated {
		return outcomeOK
	}
	var message struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if json.Unmarshal(body.Bytes(), &message) != nil {
		return outcomeOK
	}
	if len(message.Error) > 0 && string(message.Error) != "null" {
		return outcomeRPCError
	}
	if message.Result.IsError {
		return outcomeToolError
	}
	return outcomeOK
}

func classifyTransportError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return outcomeTimeout
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return outcomeTimeout
	}
	return outcomeUpstreamUnreachable
}

func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}

// captureBuffer keeps the first limit bytes written to it and reports whether
// more were discarded. Writes never fail, so it cannot interrupt a response.
type captureBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (c *captureBuffer) Write(p []byte) (int, error) {
	if room := c.limit - c.Len(); room > 0 {
		if len(p) > room {
			c.truncated = true
			c.Buffer.Write(p[:room])
		} else {
			c.Buffer.Write(p)
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	return len(p), nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// labelLimiter caps the distinct values of a label per route so untrusted
// tool and method names cannot grow metric cardinality without bound.
type labelLimiter struct {
	mu   sync.Mutex
	max  int
	seen map[string]map[string]struct{}
}

func newLabelLimiter(max int) *labelLimiter {
	return &labelLimiter{max: max, seen: map[string]map[string]struct{}{}}
}

func (l *labelLimiter) admit(route, value string) string {
	switch value {
	case labelNone, labelUnknown, labelInvalid:
		return value
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	values := l.seen[route]
	if values == nil {
		values = map[string]struct{}{}
		l.seen[route] = values
	}
	if _, ok := values[value]; ok {
		return value
	}
	if len(values) >= l.max {
		return labelOther
	}
	values[value] = struct{}{}
	return value
}

func sanitizeLabel(value string, maxLength int) string {
	switch {
	case value == "":
		return labelNone
	case len(value) > maxLength || !labelPattern.MatchString(value):
		return labelInvalid
	default:
		return value
	}
}

// trafficMetrics records MCP requests per route, method, tool, and outcome.
type trafficMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	bytes    *prometheus.CounterVec
	methods  *labelLimiter
	tools    *labelLimiter
}

func newTrafficMetrics(registry prometheus.Registerer) *trafficMetrics {
	labels := []string{"route", "method", "tool", "outcome"}
	m := &trafficMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kavrynt_gateway_mcp_requests_total",
			Help: "MCP requests proxied by the Gateway.",
		}, labels),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kavrynt_gateway_mcp_request_duration_seconds",
			Help:    "Time from receiving an MCP request to the end of the upstream response.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, labels),
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kavrynt_gateway_mcp_bytes_total",
			Help: "Request and response body bytes proxied by the Gateway.",
		}, []string{"route", "direction"}),
		methods: newLabelLimiter(maxMethodsPerRoute),
		tools:   newLabelLimiter(maxToolsPerRoute),
	}
	registry.MustRegister(m.requests, m.duration, m.bytes)
	return m
}

func (m *trafficMetrics) observe(route string, call mcpCall, outcome string, elapsed time.Duration, bytesIn, bytesOut int64) {
	method := m.methods.admit(route, sanitizeLabel(call.method, maxMethodLength))
	tool := m.tools.admit(route, sanitizeLabel(call.tool, maxToolLength))
	m.requests.WithLabelValues(route, method, tool, outcome).Inc()
	m.duration.WithLabelValues(route, method, tool, outcome).Observe(elapsed.Seconds())
	m.bytes.WithLabelValues(route, "in").Add(float64(bytesIn))
	m.bytes.WithLabelValues(route, "out").Add(float64(bytesOut))
}

// newRegistry returns a registry with the Gateway's operational metrics, Go
// runtime metrics, and process metrics.
func (h *Handler) newRegistry() *prometheus.Registry {
	registry := prometheus.NewRegistry()
	counter := func(name, help string, value func() float64) prometheus.Collector {
		return prometheus.NewCounterFunc(prometheus.CounterOpts{Name: name, Help: help}, value)
	}
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		counter("kavrynt_gateway_requests_total", "HTTP requests received by the Gateway.",
			func() float64 { return float64(h.requests.Load()) }),
		counter("kavrynt_gateway_proxied_requests_total", "Requests proxied to MCP servers to completion.",
			func() float64 { return float64(h.proxied.Load()) }),
		counter("kavrynt_gateway_stripped_credential_requests_total", "Proxied requests from which credential headers were removed.",
			func() float64 { return float64(h.strippedRequests.Load()) }),
		counter("kavrynt_gateway_route_sync_success_total", "Successful route table syncs.",
			func() float64 { syncs, _ := h.table.SyncCounts(); return float64(syncs) }),
		counter("kavrynt_gateway_route_sync_failure_total", "Failed route table syncs.",
			func() float64 { _, failures := h.table.SyncCounts(); return float64(failures) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "kavrynt_gateway_routes", Help: "Routes currently served."},
			func() float64 { _, _, _, routes := h.table.Status(); return float64(routes) }),
	)
	return registry
}
