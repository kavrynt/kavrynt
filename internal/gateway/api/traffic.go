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
	"strings"
	"sync"
	"time"

	"github.com/kavrynt/kavrynt/internal/mcpmeta"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

const (
	// maxInspectBytes bounds how much of a request or JSON response body the
	// Gateway reads to classify a call. Larger bodies are forwarded unchanged
	// and classified as "unknown".
	maxInspectBytes = 1 << 20

	maxToolsPerRoute   = mcpmeta.MaxToolsPerServer
	maxMethodsPerRoute = 50
	maxMethodLength    = 64
	maxToolLength      = mcpmeta.MaxToolNameLength

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

	// Requests refused by MCP client authentication (SPEC-0006-R14); the
	// values are clientauth's Outcome* constants.
	outcomeUnauthenticated = "unauthenticated"
	outcomeForbidden       = "forbidden"
	outcomeAuthUnavailable = "auth_unavailable"

	// The server needs a stored credential the Gateway does not have for its
	// endpoint (SPEC-0011-R06).
	outcomeCredentialUnavailable = "credential_unavailable" // #nosec G101 -- an outcome label, not a credential.

	// The tool authorization policy denied the request (SPEC-0012-R08).
	outcomeDenied = "denied"

	// The request is not a well-formed MCP Streamable HTTP request
	// (SPEC-0018-R06).
	outcomeProtocolError = "protocol_error"

	// The caller's token could not be exchanged for one for the server
	// (SPEC-0015-R06).
	outcomeExchangeFailed = "exchange_failed"
)

// mcpCall identifies what a client asked an MCP server to do.
type mcpCall struct {
	method string
	tool   string
	// body describes a POST body, for the policy step (SPEC-0012).
	body bodyInfo
}

// bodyInfo is what the Gateway read from a POST body.
type bodyInfo struct {
	// read: a POST body was inspected.
	read bool
	// batch: the body is a JSON array, a JSON-RPC batch.
	batch bool
	// classified: the body is a single JSON object within the inspection
	// limit, so its JSON-RPC method (and tool) are known.
	classified bool
	// method is the JSON-RPC method; empty for a JSON-RPC response.
	method string
	// tool is params.name of tools/call; toolOK reports it was a string.
	tool   string
	toolOK bool
	// id is the JSON-RPC id, for an error answer.
	id json.RawMessage
}

// inspectRequest classifies a request from the MCP headers or, for POST, from
// the JSON-RPC body. It returns a body that replays everything it read.
// Metric labels prefer the headers; call.body always describes the body.
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
	call.body = inspectBody(prefix)
	if call.method != "" {
		return call, body, nil
	}
	call.method = labelUnknown
	if call.body.classified && call.body.method != "" {
		call.method = call.body.method
		if call.body.method == "tools/call" && call.tool == "" {
			call.tool = call.body.tool
		}
	}
	return call, body, nil
}

// inspectBody reads the JSON-RPC message of a POST body prefix.
func inspectBody(prefix []byte) bodyInfo {
	info := bodyInfo{read: true}
	trimmed := bytes.TrimLeft(prefix, " \t\r\n")
	info.batch = len(trimmed) > 0 && trimmed[0] == '['
	if len(prefix) > maxInspectBytes {
		return info
	}
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	// A JSON-RPC batch is an array and does not decode into the struct.
	if json.Unmarshal(prefix, &message) != nil {
		return info
	}
	info.classified, info.method, info.id = true, message.Method, message.ID
	if message.Method == "tools/call" {
		var params struct {
			Name *string `json:"name"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.Name != nil {
			info.tool, info.toolOK = *params.Name, true
		}
	}
	return info
}

// probeMethods are methods clients call to learn whether a server offers
// them, so "method not found" is a normal answer and not an error.
var probeMethods = map[string]bool{"server/discover": true}

// jsonRPCMethodNotFound is the JSON-RPC error code of an unknown method.
const jsonRPCMethodNotFound = -32601

// classifyResponse maps an upstream response to an outcome. message is the
// JSON-RPC response read from a JSON answer or event stream; nil when none
// was read.
func classifyResponse(r *http.Request, call mcpCall, status int, message []byte) string {
	switch {
	case status == http.StatusMethodNotAllowed && (r.Method == http.MethodGet || r.Method == http.MethodDelete):
		// The MCP answer of a server that does not offer the optional GET
		// stream or client-terminated sessions.
		return outcomeOK
	case status >= 500:
		return outcomeUpstreamError
	case status >= 400:
		return outcomeClientError
	}
	if message == nil {
		return outcomeOK
	}
	var response struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if json.Unmarshal(message, &response) != nil {
		return outcomeOK
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		var rpcErr struct {
			Code int `json:"code"`
		}
		if probeMethods[call.method] && json.Unmarshal(response.Error, &rpcErr) == nil && rpcErr.Code == jsonRPCMethodNotFound {
			return outcomeOK
		}
		return outcomeRPCError
	}
	if response.Result.IsError {
		return outcomeToolError
	}
	return outcomeOK
}

// isResponseTo reports whether message is a JSON-RPC response to the request
// with id; any response when id is unknown.
func isResponseTo(message []byte, id json.RawMessage) bool {
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(message, &envelope) != nil || (envelope.Result == nil && envelope.Error == nil) {
		return false
	}
	if len(id) == 0 {
		return true
	}
	var got, want bytes.Buffer
	return json.Compact(&got, envelope.ID) == nil && json.Compact(&want, id) == nil && got.String() == want.String()
}

// streamObserver reads a text/event-stream answer as it is copied to the
// client and keeps the first event that carries the JSON-RPC response to the
// request. It holds one event at a time; an event larger than
// maxInspectBytes is skipped. Writes never fail, so it cannot interrupt a
// response.
type streamObserver struct {
	id json.RawMessage
	// line is the current line; lineLen counts its bytes even when skipped.
	line    bytes.Buffer
	lineLen int
	// data is the current event's data, its lines joined by "\n".
	data    bytes.Buffer
	hasData bool
	skip    bool
	// response is the JSON-RPC response, once seen.
	response []byte
}

func (s *streamObserver) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 && s.response == nil {
		chunk, rest, complete := bytes.Cut(p, []byte("\n"))
		s.lineLen += len(chunk)
		if !s.skip {
			if s.line.Len()+s.data.Len()+len(chunk) > maxInspectBytes {
				s.skip = true
				s.line.Reset()
				s.data.Reset()
			} else {
				s.line.Write(chunk)
			}
		}
		if !complete {
			break
		}
		s.endLine()
		p = rest
	}
	return n, nil
}

// finish ends a stream whose last event has no closing blank line.
func (s *streamObserver) finish() {
	if s.response == nil && s.lineLen > 0 {
		s.endLine()
	}
	s.dispatch()
}

func (s *streamObserver) endLine() {
	line := bytes.TrimSuffix(s.line.Bytes(), []byte("\r"))
	switch {
	case len(line) == 0 && s.lineLen <= 1:
		s.dispatch()
	case !s.skip:
		if field, value, ok := bytes.Cut(line, []byte(":")); ok && string(field) == "data" {
			if s.hasData {
				s.data.WriteByte('\n')
			}
			s.data.Write(bytes.TrimPrefix(value, []byte(" ")))
			s.hasData = true
		}
	}
	s.line.Reset()
	s.lineLen = 0
}

func (s *streamObserver) dispatch() {
	if s.response == nil && !s.skip && s.hasData && isResponseTo(s.data.Bytes(), s.id) {
		s.response = bytes.Clone(s.data.Bytes())
	}
	s.data.Reset()
	s.hasData, s.skip = false, false
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

func isEventStream(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "text/event-stream"
}

// recentTrafficSize is how many proxied requests /v1/traffic keeps.
const recentTrafficSize = 500

// TrafficRecord is one proxied request at /v1/traffic, with the same
// metadata as the traffic metrics: never arguments, results, or bodies.
type TrafficRecord struct {
	ID         uint64    `json:"id"`
	Time       time.Time `json:"time"`
	Route      string    `json:"route"`
	Method     string    `json:"method"`
	Tool       string    `json:"tool"`
	Outcome    string    `json:"outcome"`
	Status     int       `json:"status"`
	DurationMS float64   `json:"durationMs"`
	BytesIn    int64     `json:"bytesIn"`
	BytesOut   int64     `json:"bytesOut"`
}

// recentTraffic is a ring buffer of the latest proxied requests, for the
// console's live view. It lives in memory and is per Gateway replica.
type recentTraffic struct {
	mu      sync.Mutex
	records []TrafficRecord
	next    int
	seq     uint64
}

func newRecentTraffic(size int) *recentTraffic {
	return &recentTraffic{records: make([]TrafficRecord, 0, size)}
}

func (t *recentTraffic) add(route string, call mcpCall, outcome string, status int, elapsed time.Duration, bytesIn, bytesOut int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	record := TrafficRecord{
		ID:         t.seq,
		Time:       time.Now().UTC(),
		Route:      route,
		Method:     sanitizeLabel(call.method, maxMethodLength),
		Tool:       sanitizeLabel(call.tool, maxToolLength),
		Outcome:    outcome,
		Status:     status,
		DurationMS: float64(elapsed.Microseconds()) / 1000,
		BytesIn:    bytesIn,
		BytesOut:   bytesOut,
	}
	if len(t.records) < cap(t.records) {
		t.records = append(t.records, record)
		return
	}
	t.records[t.next] = record
	t.next = (t.next + 1) % len(t.records)
}

// list returns the records, newest first.
func (t *recentTraffic) list() []TrafficRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]TrafficRecord, 0, len(t.records))
	for i := len(t.records) - 1; i >= 0; i-- {
		out = append(out, t.records[(t.next+i)%len(t.records)])
	}
	return out
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
	case !mcpmeta.ValidName(value, maxLength):
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

// durationBuckets are the latency buckets of the duration histogram, and of
// the console alerts' latency (SPEC-0020-R01).
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

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
			Buckets: durationBuckets,
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

// observe records a request and returns its bounded method and tool labels.
func (m *trafficMetrics) observe(route string, call mcpCall, outcome string, elapsed time.Duration, bytesIn, bytesOut int64) (string, string) {
	method := m.methods.admit(route, sanitizeLabel(call.method, maxMethodLength))
	tool := m.tools.admit(route, sanitizeLabel(call.tool, maxToolLength))
	m.requests.WithLabelValues(route, method, tool, outcome).Inc()
	m.duration.WithLabelValues(route, method, tool, outcome).Observe(elapsed.Seconds())
	m.bytes.WithLabelValues(route, "in").Add(float64(bytesIn))
	m.bytes.WithLabelValues(route, "out").Add(float64(bytesOut))
	return method, tool
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
