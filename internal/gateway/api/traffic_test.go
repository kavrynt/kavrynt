package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/model"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
)

func trafficHandler(t *testing.T, respond func(*http.Request) (*http.Response, error)) *Handler {
	t.Helper()
	table := routing.NewTable()
	table.Replace([]model.Route{{Name: "team-a.demo", Version: "0.1.0", Transport: "http", Endpoint: "http://demo.test/mcp"}}, time.Now())
	return NewHandler(table, &http.Client{Transport: roundTripFunc(respond)}, Metadata{})
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func scrape(t *testing.T, h *Handler) string {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", rr.Code)
	}
	return rr.Body.String()
}

func post(h *Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp/team-a.demo", strings.NewReader(body))
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func requireSeries(t *testing.T, metrics, series string) {
	t.Helper()
	if !strings.Contains(metrics, series) {
		t.Fatalf("metrics missing %s\n%s", series, metrics)
	}
}

func TestTrafficMetricsClassifyCalls(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		headers  map[string]string
		response *http.Response
		err      error
		series   string
	}{
		{
			name:     "tool call from body",
			body:     `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_payments","arguments":{"order":"42"}}}`,
			response: jsonResponse(http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`),
			series:   `kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="ok",route="team-a.demo",tool="search_payments"} 1`,
		},
		{
			// A body the Gateway cannot read is labelled from the headers.
			name:     "headers take precedence",
			body:     `tools/call from_body`,
			headers:  map[string]string{"Mcp-Method": "tools/call", "Mcp-Name": "from_header"},
			response: jsonResponse(http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":{}}`),
			series:   `kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="ok",route="team-a.demo",tool="from_header"} 1`,
		},
		{
			name:     "list has no tool",
			body:     `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
			response: jsonResponse(http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`),
			series:   `kavrynt_gateway_mcp_requests_total{method="tools/list",outcome="ok",route="team-a.demo",tool="none"} 1`,
		},
		{
			name:     "tool error result",
			body:     `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"refund"}}`,
			response: jsonResponse(http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[]}}`),
			series:   `kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="tool_error",route="team-a.demo",tool="refund"} 1`,
		},
		{
			name:     "json-rpc error",
			body:     `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"refund"}}`,
			response: jsonResponse(http.StatusOK, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"bad params"}}`),
			series:   `kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="rpc_error",route="team-a.demo",tool="refund"} 1`,
		},
		{
			name:     "upstream 5xx",
			body:     `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
			response: jsonResponse(http.StatusServiceUnavailable, `{}`),
			series:   `kavrynt_gateway_mcp_requests_total{method="tools/list",outcome="upstream_error",route="team-a.demo",tool="none"} 1`,
		},
		{
			name:   "upstream unreachable",
			body:   `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
			err:    errors.New("connection refused"),
			series: `kavrynt_gateway_mcp_requests_total{method="tools/list",outcome="upstream_unreachable",route="team-a.demo",tool="none"} 1`,
		},
		{
			name:     "unsafe tool name",
			body:     `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x\"} 1\nfake_metric{a=\"b"}}`,
			response: jsonResponse(http.StatusOK, `{"result":{}}`),
			series:   `kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="ok",route="team-a.demo",tool="invalid"} 1`,
		},
		{
			name:     "not json-rpc",
			body:     `not json`,
			response: jsonResponse(http.StatusOK, `{}`),
			series:   `kavrynt_gateway_mcp_requests_total{method="unknown",outcome="ok",route="team-a.demo",tool="none"} 1`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := trafficHandler(t, func(*http.Request) (*http.Response, error) { return tt.response, tt.err })
			post(h, tt.body, tt.headers)
			metrics := scrape(t, h)
			requireSeries(t, metrics, tt.series)
			if strings.Contains(metrics, "fake_metric") || strings.Contains(metrics, "order") {
				t.Fatalf("request content leaked into metrics:\n%s", metrics)
			}
		})
	}
}

func eventStreamResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// The official MCP SDKs answer POST requests with an event stream, so its
// response message is classified like a JSON answer.
func TestTrafficMetricsClassifyEventStreams(t *testing.T) {
	call := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"refund"}}`
	progress := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":1}}\n\n"
	tests := []struct {
		name    string
		body    string
		stream  string
		outcome string
	}{
		{
			name:    "tool error after a notification",
			body:    call,
			stream:  progress + "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"isError\":true,\"content\":[]}}\n\n",
			outcome: "tool_error",
		},
		{
			name:    "json-rpc error",
			body:    call,
			stream:  "data: {\"jsonrpc\":\"2.0\",\"id\":7,\"error\":{\"code\":-32602,\"message\":\"bad params\"}}\n\n",
			outcome: "rpc_error",
		},
		{
			name:    "result",
			body:    call,
			stream:  progress + "data: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"content\":[]}}\n\n",
			outcome: "ok",
		},
		{
			name:    "message split over data lines with CRLF",
			body:    call,
			stream:  "id: 1\r\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\r\ndata: \"result\":{\"isError\":true}}\r\n\r\n",
			outcome: "tool_error",
		},
		{
			name:    "last event without a blank line",
			body:    call,
			stream:  "data: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"isError\":true}}",
			outcome: "tool_error",
		},
		{
			// A server request on the stream, such as sampling, carries its
			// own id and is not the answer.
			name:    "answer to another request is ignored",
			body:    call,
			stream:  "data: {\"jsonrpc\":\"2.0\",\"id\":99,\"error\":{\"code\":-1,\"message\":\"x\"}}\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{}}\n\n",
			outcome: "ok",
		},
		{
			name:    "event larger than the inspection limit",
			body:    call,
			stream:  "data: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"isError\":true,\"content\":\"" + strings.Repeat("a", maxInspectBytes) + "\"}}\n\n",
			outcome: "ok",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := trafficHandler(t, func(*http.Request) (*http.Response, error) {
				return eventStreamResponse(http.StatusOK, tt.stream), nil
			})
			rr := post(h, tt.body, nil)
			if rr.Body.String() != tt.stream {
				t.Fatalf("client received %d bytes, want the %d upstream sent unchanged", rr.Body.Len(), len(tt.stream))
			}
			requireSeries(t, scrape(t, h), `kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="`+tt.outcome+`",route="team-a.demo",tool="refund"} 1`)
		})
	}
}

func TestStreamObserverReadsEventsSplitAcrossWrites(t *testing.T) {
	answer := `{"jsonrpc":"2.0","id":"a","result":{"isError":true}}`
	stream := ": keep-alive\n\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\ndata: " + answer + "\n\n"
	s := &streamObserver{id: []byte(`"a"`)}
	for i := range len(stream) {
		if n, err := s.Write([]byte{stream[i]}); n != 1 || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	s.finish()
	if string(s.response) != answer {
		t.Fatalf("response = %q, want %q", s.response, answer)
	}
}

// The rest of an event too large to inspect is not read as a new event.
func TestStreamObserverSkipsOversizedEventToItsEnd(t *testing.T) {
	stream := "data: " + strings.Repeat("a", maxInspectBytes) + "\n" +
		`data: {"jsonrpc":"2.0","id":1,"result":{"isError":true}}` + "\n\n"
	s := &streamObserver{id: []byte(`1`)}
	if _, err := io.Copy(s, strings.NewReader(stream)); err != nil {
		t.Fatal(err)
	}
	s.finish()
	if s.response != nil {
		t.Fatalf("read %q from the tail of an oversized event", s.response)
	}
}

// Answers the MCP specification defines for a feature the server does not
// offer are normal client behavior, not errors.
func TestTrafficMetricsCountNormalProbesAsOK(t *testing.T) {
	notFound := `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`
	tests := []struct {
		name     string
		method   string
		body     string
		response *http.Response
		series   string
	}{
		{
			name:     "optional GET stream not offered",
			method:   http.MethodGet,
			response: jsonResponse(http.StatusMethodNotAllowed, `{}`),
			series:   `kavrynt_gateway_mcp_requests_total{method="http_get",outcome="ok",route="team-a.demo",tool="none"} 1`,
		},
		{
			name:     "session termination not offered",
			method:   http.MethodDelete,
			response: jsonResponse(http.StatusMethodNotAllowed, `{}`),
			series:   `kavrynt_gateway_mcp_requests_total{method="http_delete",outcome="ok",route="team-a.demo",tool="none"} 1`,
		},
		{
			name:     "server/discover not offered",
			method:   http.MethodPost,
			body:     `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`,
			response: jsonResponse(http.StatusOK, notFound),
			series:   `kavrynt_gateway_mcp_requests_total{method="server/discover",outcome="ok",route="team-a.demo",tool="none"} 1`,
		},
		{
			name:     "server/discover not offered on a stream",
			method:   http.MethodPost,
			body:     `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`,
			response: eventStreamResponse(http.StatusOK, "data: "+notFound+"\n\n"),
			series:   `kavrynt_gateway_mcp_requests_total{method="server/discover",outcome="ok",route="team-a.demo",tool="none"} 1`,
		},
		{
			name:     "other methods not found are still errors",
			method:   http.MethodPost,
			body:     `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
			response: jsonResponse(http.StatusOK, notFound),
			series:   `kavrynt_gateway_mcp_requests_total{method="tools/list",outcome="rpc_error",route="team-a.demo",tool="none"} 1`,
		},
		{
			name:     "server/discover failing otherwise is still an error",
			method:   http.MethodPost,
			body:     `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`,
			response: jsonResponse(http.StatusOK, `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"internal"}}`),
			series:   `kavrynt_gateway_mcp_requests_total{method="server/discover",outcome="rpc_error",route="team-a.demo",tool="none"} 1`,
		},
		{
			name:     "405 to a POST is still a client error",
			method:   http.MethodPost,
			body:     `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
			response: jsonResponse(http.StatusMethodNotAllowed, `{}`),
			series:   `kavrynt_gateway_mcp_requests_total{method="tools/list",outcome="client_error",route="team-a.demo",tool="none"} 1`,
		},
		{
			name:     "other 4xx to a GET is still a client error",
			method:   http.MethodGet,
			response: jsonResponse(http.StatusNotFound, `{}`),
			series:   `kavrynt_gateway_mcp_requests_total{method="http_get",outcome="client_error",route="team-a.demo",tool="none"} 1`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := trafficHandler(t, func(*http.Request) (*http.Response, error) { return tt.response, nil })
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tt.method, "/mcp/team-a.demo", body))
			requireSeries(t, scrape(t, h), tt.series)
		})
	}
}

func TestTrafficMetricsRecordLatencyAndBytes(t *testing.T) {
	h := trafficHandler(t, func(req *http.Request) (*http.Response, error) {
		// Bytes in are counted as the transport sends them upstream.
		_, err := io.Copy(io.Discard, req.Body)
		return jsonResponse(http.StatusOK, `{"result":{}}`), err
	})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	post(h, body, nil)

	metrics := scrape(t, h)
	requireSeries(t, metrics, `kavrynt_gateway_mcp_request_duration_seconds_count{method="tools/list",outcome="ok",route="team-a.demo",tool="none"} 1`)
	requireSeries(t, metrics, fmt.Sprintf(`kavrynt_gateway_mcp_bytes_total{direction="in",route="team-a.demo"} %d`, len(body)))
	requireSeries(t, metrics, `kavrynt_gateway_mcp_bytes_total{direction="out",route="team-a.demo"} 13`)
	requireSeries(t, metrics, `kavrynt_gateway_proxied_requests_total 1`)
}

func TestTrafficMetricsCapToolCardinality(t *testing.T) {
	h := trafficHandler(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"result":{}}`), nil
	})
	for i := 0; i <= maxToolsPerRoute; i++ {
		post(h, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tool_%d"}}`, i), nil)
	}
	metrics := scrape(t, h)
	requireSeries(t, metrics, fmt.Sprintf(`tool="tool_%d"} 1`, maxToolsPerRoute-1))
	requireSeries(t, metrics, `kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="ok",route="team-a.demo",tool="other"} 1`)
	if strings.Contains(metrics, fmt.Sprintf(`tool="tool_%d"`, maxToolsPerRoute)) {
		t.Fatal("tool label exceeded the per-route cap")
	}
}

func TestProxyForwardsInspectedBodyUnchanged(t *testing.T) {
	large := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"upload","arguments":{"blob":"` +
		strings.Repeat("a", maxInspectBytes) + `"}}}`

	for name, body := range map[string]string{"small": `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "larger than inspect limit": large} {
		t.Run(name, func(t *testing.T) {
			var got []byte
			var gotLength int64
			h := trafficHandler(t, func(req *http.Request) (*http.Response, error) {
				gotLength = req.ContentLength
				var err error
				got, err = io.ReadAll(req.Body)
				return jsonResponse(http.StatusOK, `{"result":{}}`), err
			})
			rr := post(h, body, nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d", rr.Code)
			}
			if !bytes.Equal(got, []byte(body)) {
				t.Fatalf("upstream body differs: got %d bytes, want %d", len(got), len(body))
			}
			if gotLength != int64(len(body)) {
				t.Fatalf("upstream ContentLength = %d, want %d", gotLength, len(body))
			}
		})
	}
}

func TestProxyGetForwardsNoBody(t *testing.T) {
	var sawBody bool
	h := trafficHandler(t, func(req *http.Request) (*http.Response, error) {
		sawBody = req.Body != nil && req.Body != http.NoBody
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/mcp/team-a.demo", nil))
	if sawBody {
		t.Fatal("GET was forwarded with a body")
	}
	requireSeries(t, scrape(t, h), `kavrynt_gateway_mcp_requests_total{method="http_get",outcome="ok",route="team-a.demo",tool="none"} 1`)
}

func TestCaptureBufferNeverFailsWrites(t *testing.T) {
	c := &captureBuffer{limit: 4}
	for _, chunk := range []string{"ab", "cdef", "gh"} {
		if n, err := c.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if c.String() != "abcd" || !c.truncated {
		t.Fatalf("captured %q truncated=%v", c.String(), c.truncated)
	}
}
