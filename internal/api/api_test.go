package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kavrynt/gateway/internal/model"
	"github.com/kavrynt/gateway/internal/registry"
	"github.com/kavrynt/gateway/internal/routing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestSyncOnceAndListRoutes(t *testing.T) {
	handler := newTestHandler(t, registryResponse(t, `{
		"servers": [
			{
				"manifest": {
					"apiVersion": "kavrynt.io/v1alpha1",
					"kind": "MCPServer",
					"metadata": {"name": "demo"},
					"spec": {
						"version": "0.1.0",
						"transport": "http",
						"endpoint": "http://demo.test/mcp"
					}
				},
				"registeredAt": "2026-08-09T00:00:00Z",
				"updatedAt": "2026-08-09T00:00:00Z"
			}
		]
	}`), nil)

	if err := handler.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/routes", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"name":"demo"`) {
		t.Fatalf("response missing route: %s", rr.Body.String())
	}
}

func TestProxyHTTPRoute(t *testing.T) {
	table := routing.NewTable()
	table.Replace([]model.ServerRecord{
		{
			Manifest: model.Manifest{
				Metadata: model.Metadata{Name: "demo"},
				Spec: model.Spec{
					Version:   "0.1.0",
					Transport: "http",
					Endpoint:  "http://demo.test/mcp",
				},
			},
		},
	}, time.Now())

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

	registryClient, err := registry.NewClient("http://registry.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(table, registryClient, proxyClient, Metadata{})

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
	table.Replace([]model.ServerRecord{
		{
			Manifest: model.Manifest{
				Metadata: model.Metadata{Name: "stdio-demo"},
				Spec: model.Spec{
					Version:   "0.1.0",
					Transport: "stdio",
					Command:   "demo",
				},
			},
		},
	}, time.Now())

	registryClient, err := registry.NewClient("http://registry.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(table, registryClient, nil, Metadata{})

	req := httptest.NewRequest(http.MethodPost, "/mcp/stdio-demo", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
}

func newTestHandler(t *testing.T, registryTransport http.RoundTripper, proxyTransport http.RoundTripper) *Handler {
	t.Helper()
	registryClient, err := registry.NewClient("http://registry.test", &http.Client{Transport: registryTransport})
	if err != nil {
		t.Fatal(err)
	}
	var proxyClient *http.Client
	if proxyTransport != nil {
		proxyClient = &http.Client{Transport: proxyTransport}
	}
	return NewHandler(routing.NewTable(), registryClient, proxyClient, Metadata{})
}

func registryResponse(t *testing.T, body string) http.RoundTripper {
	t.Helper()
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
}
