package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/model"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestListRoutesAndReadiness(t *testing.T) {
	table := routing.NewTable()
	handler := NewHandler(table, nil, Metadata{})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz before first sync = %d, want 503", rr.Code)
	}

	table.Replace([]model.Route{{Name: "team-a.demo", Version: "0.1.0", Transport: "http", Endpoint: "http://demo.test/mcp"}}, time.Now())

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("readyz after sync = %d, want 200", rr.Code)
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/routes", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"name":"team-a.demo"`) {
		t.Fatalf("response missing route: %s", rr.Body.String())
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "kavrynt_gateway_route_sync_success_total 1") {
		t.Fatalf("metrics missing sync count: %s", rr.Body.String())
	}
}

func TestProxyHTTPRoute(t *testing.T) {
	table := routing.NewTable()
	table.Replace([]model.Route{{Name: "demo", Version: "0.1.0", Transport: "http", Endpoint: "http://demo.test/mcp"}}, time.Now())

	var gotURL string
	var gotBody string
	proxyClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotURL = req.URL.String()
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			gotBody = string(body)
			if req.Header.Get("X-Kavrynt-Route") != "demo" {
				t.Fatalf("missing X-Kavrynt-Route header")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}, nil
		}),
	}

	handler := NewHandler(table, proxyClient, Metadata{})

	req := httptest.NewRequest(http.MethodPost, "/mcp/demo/tools/list?trace=1", strings.NewReader(`{"jsonrpc":"2.0"}`))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if gotURL != "http://demo.test/mcp/tools/list?trace=1" {
		t.Fatalf("gotURL = %s", gotURL)
	}
	if gotBody != `{"jsonrpc":"2.0"}` {
		t.Fatalf("gotBody = %s", gotBody)
	}
}

func TestProxyRejectsUnsupportedTransport(t *testing.T) {
	table := routing.NewTable()
	table.Replace([]model.Route{{Name: "stdio-demo", Version: "0.1.0", Transport: "stdio"}}, time.Now())

	handler := NewHandler(table, nil, Metadata{})

	req := httptest.NewRequest(http.MethodPost, "/mcp/stdio-demo", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
}

func TestBuildTargetURLRejectsUnsafeEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{name: "relative", endpoint: "/mcp"},
		{name: "unsupported scheme", endpoint: "file:///etc/passwd"},
		{name: "embedded credentials", endpoint: "http://user:secret@example.test/mcp"},
		{name: "fragment", endpoint: "http://example.test/mcp#section"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := buildTargetURL(tt.endpoint, "", ""); err == nil {
				t.Fatalf("buildTargetURL(%q) returned nil error", tt.endpoint)
			}
		})
	}
}
