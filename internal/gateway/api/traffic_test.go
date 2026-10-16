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
			name:     "headers take precedence",
			body:     `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"from_body"}}`,
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
