package registry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestUpsertPostsManifest(t *testing.T) {
	client, err := NewClient("http://registry.test", &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", req.Method)
			}
			if req.URL.String() != "http://registry.test/v1/servers" {
				t.Fatalf("url = %s", req.URL.String())
			}
			if req.Header.Get("Accept") != "application/json" {
				t.Fatalf("Accept = %q, want application/json", req.Header.Get("Accept"))
			}
			if req.Header.Get("User-Agent") != UserAgent {
				t.Fatalf("User-Agent = %q, want %s", req.Header.Get("User-Agent"), UserAgent)
			}
			var manifest Manifest
			if err := json.NewDecoder(req.Body).Decode(&manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.Metadata.Name != "demo-mcp" {
				t.Fatalf("name = %s, want demo-mcp", manifest.Metadata.Name)
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{}`)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := client.Upsert(context.Background(), Manifest{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   Metadata{Name: "demo-mcp"},
		Spec: Spec{
			Version:   "0.1.0",
			Transport: "http",
			Endpoint:  "http://demo.test/mcp",
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteTreatsNotFoundAsSuccess(t *testing.T) {
	client, err := NewClient("http://registry.test", &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodDelete {
				t.Fatalf("method = %s, want DELETE", req.Method)
			}
			if req.URL.String() != "http://registry.test/v1/servers/demo-mcp" {
				t.Fatalf("url = %s", req.URL.String())
			}
			if req.Header.Get("Accept") != "application/json" {
				t.Fatalf("Accept = %q, want application/json", req.Header.Get("Accept"))
			}
			if req.Header.Get("User-Agent") != UserAgent {
				t.Fatalf("User-Agent = %q, want %s", req.Header.Get("User-Agent"), UserAgent)
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := client.Delete(context.Background(), "demo-mcp"); err != nil {
		t.Fatal(err)
	}
}

func TestNewClientRequiresAbsoluteURL(t *testing.T) {
	if _, err := NewClient("registry:8080", nil); err == nil {
		t.Fatal("expected error for non-absolute URL")
	}
}
