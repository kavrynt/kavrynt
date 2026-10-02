package registry

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestListServers(t *testing.T) {
	client, err := NewClient("http://registry.test", &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", req.Method)
			}
			if req.URL.String() != "http://registry.test/v1/servers" {
				t.Fatalf("url = %s", req.URL.String())
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
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
				}`)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	records, err := client.ListServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0].Manifest.Metadata.Name != "demo" {
		t.Fatalf("name = %s, want demo", records[0].Manifest.Metadata.Name)
	}
}

func TestNewClientRequiresAbsoluteURL(t *testing.T) {
	if _, err := NewClient("localhost:8080", nil); err == nil {
		t.Fatal("expected error for non-absolute URL")
	}
}
