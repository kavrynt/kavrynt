package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/console"
	"github.com/kavrynt/kavrynt/internal/gateway/model"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
)

func demoHandler(t *testing.T, respond func(*http.Request) (*http.Response, error)) (*Handler, *int) {
	t.Helper()
	table := routing.NewTable()
	table.Replace([]model.Route{{Name: "team-a.demo", Version: "0.1.0", Transport: "http", Endpoint: "http://demo.test/mcp"}}, time.Now())
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return respond(req)
	})}
	return NewHandler(table, client, Metadata{}), &calls
}

func okResponse(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{}}`)),
	}, nil
}

func TestProxyPassesRedirectsToClient(t *testing.T) {
	handler, calls := demoHandler(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://169.254.169.254/latest/meta-data/"}},
			Body:       http.NoBody,
		}, nil
	})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/mcp/team-a.demo", nil))

	if rr.Code != http.StatusFound || *calls != 1 {
		t.Fatalf("status = %d after %d upstream calls; want the 302 passed through after 1 call", rr.Code, *calls)
	}
}

func TestProxyRejectsDotSegments(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	for _, path := range []string{"/mcp/team-a.demo/../admin", "/mcp/team-a.demo/./x", "/mcp/team-a.demo/a/.."} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, rr.Code)
		}
	}
	if *calls != 0 {
		t.Fatalf("upstream called %d times", *calls)
	}
}

func TestOriginValidation(t *testing.T) {
	handler, _ := demoHandler(t, okResponse)
	handler.AllowOrigins("https://console.example.com/")

	tests := []struct {
		origin string
		want   int
	}{
		{origin: "", want: http.StatusOK},
		{origin: "http://localhost:18080", want: http.StatusOK},
		{origin: "http://127.0.0.1:18080", want: http.StatusOK},
		{origin: "http://[::1]:8080", want: http.StatusOK},
		{origin: "https://console.example.com", want: http.StatusOK},
		{origin: "https://evil.example", want: http.StatusForbidden},
		{origin: "http://localhost.evil.example", want: http.StatusForbidden},
		{origin: "null", want: http.StatusForbidden},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodPost, "/mcp/team-a.demo", strings.NewReader(`{}`))
		if tt.origin != "" {
			req.Header.Set("Origin", tt.origin)
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != tt.want {
			t.Errorf("Origin %q: status = %d, want %d", tt.origin, rr.Code, tt.want)
		}
	}
}

func TestProxyDetectsGatewayLoops(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	req := httptest.NewRequest(http.MethodPost, "/mcp/team-a.demo", strings.NewReader(`{}`))
	req.Header.Set("X-Kavrynt-Route", "team-a.demo")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusLoopDetected || *calls != 0 {
		t.Fatalf("status = %d after %d upstream calls; want 508 and none", rr.Code, *calls)
	}
}

func TestProxyHidesUpstreamErrorDetails(t *testing.T) {
	handler, _ := demoHandler(t, func(req *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Post", URL: req.URL.String(), Err: errors.New("dial tcp 10.0.0.7:8080: connection refused")}
	})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp/team-a.demo", strings.NewReader(`{}`)))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
	if body := rr.Body.String(); strings.Contains(body, "demo.test") || strings.Contains(body, "10.0.0.7") {
		t.Fatalf("error response reveals upstream details: %s", body)
	}
}

func TestProxyTimesOutSlowUpstreams(t *testing.T) {
	handler, _ := demoHandler(t, func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	handler.SetRequestTimeout(20 * time.Millisecond)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp/team-a.demo", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", rr.Code)
	}
	requireSeries(t, scrape(t, handler), `kavrynt_gateway_mcp_requests_total{method="tools/list",outcome="timeout",route="team-a.demo",tool="none"} 1`)
}

// flushRecorder records the body as it was at each flush.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed []string
}

func (f *flushRecorder) Flush() {
	f.ResponseRecorder.Flush()
	f.flushed = append(f.flushed, f.Body.String())
}

func TestProxyStreamsEventsPastTheTimeout(t *testing.T) {
	reader, writer := io.Pipe()
	handler, _ := demoHandler(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       reader,
		}, nil
	})
	handler.SetRequestTimeout(20 * time.Millisecond)
	go func() {
		_, _ = io.WriteString(writer, "data: one\n\n")
		time.Sleep(60 * time.Millisecond)
		_, _ = io.WriteString(writer, "data: two\n\n")
		_ = writer.Close()
	}()

	rr := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/mcp/team-a.demo", nil))

	if rr.Code != http.StatusOK || rr.Body.String() != "data: one\n\ndata: two\n\n" {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if len(rr.flushed) == 0 || rr.flushed[0] != "data: one\n\n" {
		t.Fatalf("first event was not flushed on its own: %q", rr.flushed)
	}
}

func TestProxiedResponsesAreSandboxed(t *testing.T) {
	handler, _ := demoHandler(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}, "Content-Security-Policy": []string{"default-src *"}},
			Body:       io.NopCloser(strings.NewReader(`<script>fetch("/v1/servers")</script>`)),
		}, nil
	})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/mcp/team-a.demo", nil))
	if got := rr.Header().Values("Content-Security-Policy"); len(got) != 1 || got[0] != "sandbox" {
		t.Fatalf("Content-Security-Policy = %q, want only sandbox", got)
	}
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing X-Content-Type-Options: nosniff")
	}
}

// Verifies SPEC-0001-R06, SPEC-0001-R07, and SPEC-0001-R08.
func TestServersAndTrafficEndpoints(t *testing.T) {
	handler, _ := demoHandler(t, okResponse)
	handler.table.ReplaceServers([]model.Server{{Namespace: "team-a", Name: "demo", Route: "team-a.demo", Routed: true,
		Tools: []model.Tool{{Name: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}}}})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/servers", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"tools":[{"name":"echo","inputSchema":{"type":"object"}}]`) {
		t.Fatalf("GET /v1/servers = %d: %s", rr.Code, rr.Body.String())
	}

	post(handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"message":"secret-argument"}}}`, nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/traffic", nil))
	var traffic struct {
		Requests []TrafficRecord `json:"requests"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &traffic); err != nil {
		t.Fatal(err)
	}
	if len(traffic.Requests) != 1 {
		t.Fatalf("traffic = %+v", traffic.Requests)
	}
	if r := traffic.Requests[0]; r.Route != "team-a.demo" || r.Method != "tools/call" || r.Tool != "echo" || r.Outcome != "ok" || r.Status != http.StatusOK {
		t.Fatalf("record = %+v", r)
	}
	if strings.Contains(rr.Body.String(), "secret-argument") {
		t.Fatalf("traffic contains tool arguments: %s", rr.Body.String())
	}
}

// Verifies SPEC-0001-R07.
func TestRecentTrafficKeepsNewestFirst(t *testing.T) {
	recent := newRecentTraffic(3)
	for i := 1; i <= 5; i++ {
		recent.add("r", mcpCall{method: "tools/list"}, outcomeOK, http.StatusOK, time.Millisecond, 0, 0)
	}
	records := recent.list()
	if len(records) != 3 || records[0].ID != 5 || records[1].ID != 4 || records[2].ID != 3 {
		t.Fatalf("records = %+v, want IDs 5, 4, 3", records)
	}
}

// Verifies SPEC-0001-R01, SPEC-0001-R02, and SPEC-0001-R04.
func TestConsole(t *testing.T) {
	handler, _ := demoHandler(t, okResponse)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ui/", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("console disabled: GET /ui/ = %d, want 404", rr.Code)
	}

	handler.EnableConsole(console.Handler())
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/ui/" {
		t.Fatalf("GET / = %d to %q, want redirect to /ui/", rr.Code, rr.Header().Get("Location"))
	}
	for _, path := range []string{"/ui/", "/ui/app.js", "/ui/app.css", "/ui/logo.svg"} {
		rr = httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rr.Code)
		}
		if csp := rr.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Fatalf("GET %s: Content-Security-Policy = %q", path, csp)
		}
	}
}
