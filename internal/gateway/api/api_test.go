package api

import (
	"encoding/json"
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
	table.Replace([]model.Route{{Name: "demo", Version: "0.1.0", Transport: "http", Endpoint: "http://demo.test/mcp", ForwardSubpaths: true}}, time.Now())

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

func TestProxyNeverForwardsCredentials(t *testing.T) {
	table := routing.NewTable()
	table.Replace([]model.Route{{Name: "demo", Version: "0.1.0", Transport: "http", Endpoint: "http://demo.test/mcp"}}, time.Now())

	var upstream http.Header
	proxyClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		upstream = req.Header.Clone()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"Set-Cookie":   []string{"session=upstream"},
				"Server":       []string{"uvicorn Python/3.12"},
				"X-Powered-By": []string{"Express"},
				"X-Upstream":   []string{"1"},
			},
			Body: io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	})}
	handler := NewHandler(table, proxyClient, Metadata{})
	handler.StripRequestHeaders("X-Api-Key")

	req := httptest.NewRequest(http.MethodPost, "/mcp/demo", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer client-token")
	req.Header.Set("Cookie", "session=client")
	req.Header.Set("Proxy-Authorization", "Basic abc")
	req.Header.Set("X-Api-Key", "secret")
	req.Header.Set("Connection", "X-Hop")
	req.Header.Set("X-Hop", "1")
	req.Header.Set("Mcp-Method", "tools/list")
	req.Header.Set("X-Keep", "1")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "X-Api-Key", "X-Hop", "Connection"} {
		if value := upstream.Get(name); value != "" {
			t.Errorf("upstream received %s: %q", name, value)
		}
	}
	for _, name := range []string{"Mcp-Method", "X-Keep", "X-Kavrynt-Route"} {
		if upstream.Get(name) == "" {
			t.Errorf("upstream missing %s", name)
		}
	}
	// Set-Cookie would apply to the shared Gateway origin; Server and
	// X-Powered-By disclose the upstream's software (issue #80).
	for _, name := range []string{"Set-Cookie", "Server", "X-Powered-By"} {
		if value := rr.Header().Get(name); value != "" {
			t.Errorf("client received upstream %s: %q", name, value)
		}
	}
	if rr.Header().Get("X-Upstream") != "1" {
		t.Error("client missing X-Upstream response header")
	}

	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), "kavrynt_gateway_stripped_credential_requests_total 1") {
		t.Fatalf("metrics missing stripped count:\n%s", metrics.Body.String())
	}
}

// A registered server the Gateway does not route says why, instead of
// claiming it is not registered (issue #87).
func TestProxyExplainsUnroutedServers(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	handler.table.ReplaceServers([]model.Server{
		{Namespace: "team-a", Name: "files", Route: "team-a.files", Transport: "stdio"},
		{Namespace: "team-a", Name: "pending", Route: "team-a.pending", Transport: "http"},
	})
	for route, want := range map[string]string{
		"team-a.files":   `MCP server "team-a.files" uses the stdio transport, which the Gateway does not proxy; run it behind a Streamable HTTP bridge and register the bridge's http endpoint`,
		"team-a.pending": `MCP server "team-a.pending" is registered but not routed; check its Ready condition`,
		"team-a.missing": `MCP server "team-a.missing" is not registered`,
	} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp/"+route, strings.NewReader(`{}`)))
		var body errorResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rr.Code != http.StatusNotFound || body.Error != want {
			t.Errorf("%s: %d %q, want 404 %q", route, rr.Code, body.Error, want)
		}
	}
	if *calls != 0 {
		t.Fatalf("%d requests reached an MCP server", *calls)
	}
}
