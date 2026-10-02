package remote

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kavrynt/kavrynt/internal/kavryctl/manifest"
)

func TestNewClientValidatesURL(t *testing.T) {
	if _, err := NewClient("http://localhost:8080"); err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	if _, err := NewClient("localhost:8080"); err == nil {
		t.Fatal("NewClient accepted URL without scheme")
	}

	if _, err := NewClient("ftp://localhost:8080"); err == nil {
		t.Fatal("NewClient accepted unsupported scheme")
	}
}

func TestClientRegisterListInspect(t *testing.T) {
	transport := &fakeTransport{}
	client := &Client{
		baseURL: "http://registry.example",
		httpClient: &http.Client{
			Transport: transport,
		},
	}

	m := manifest.Manifest{
		APIVersion: manifest.APIVersion,
		Kind:       manifest.Kind,
		Metadata: manifest.Metadata{
			Name: "example",
		},
		Spec: manifest.Spec{
			Version:   "0.1.0",
			Transport: "stdio",
			Command:   "python3",
		},
	}

	record, created, err := client.Register(m)
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if !created {
		t.Fatal("Register reported update")
	}
	if record.Manifest.Metadata.Name != "example" {
		t.Fatalf("registered name = %q", record.Manifest.Metadata.Name)
	}

	servers, err := client.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(servers) != 1 || servers[0].Manifest.Metadata.Name != "example" {
		t.Fatalf("servers = %#v", servers)
	}

	record, err = client.Inspect("example")
	if err != nil {
		t.Fatalf("Inspect returned error: %v", err)
	}
	if record.Manifest.Metadata.Name != "example" {
		t.Fatalf("inspect name = %q", record.Manifest.Metadata.Name)
	}

	if err := client.Unregister("example"); err != nil {
		t.Fatalf("Unregister returned error: %v", err)
	}

	wantRequests := []string{
		"POST /v1/servers",
		"GET /v1/servers",
		"GET /v1/servers/example",
		"DELETE /v1/servers/example",
	}
	if strings.Join(transport.requests, ",") != strings.Join(wantRequests, ",") {
		t.Fatalf("requests = %#v", transport.requests)
	}
}

type fakeTransport struct {
	requests []string
}

func (t *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, req.Method+" "+req.URL.Path)
	body := `{"manifest":{"apiVersion":"kavrynt.io/v1alpha1","kind":"MCPServer","metadata":{"name":"example"},"spec":{"version":"0.1.0","transport":"stdio","command":"python3"}}}`
	status := http.StatusOK

	if req.Method == http.MethodPost && req.URL.Path == "/v1/servers" {
		status = http.StatusCreated
	} else if req.Method == http.MethodGet && req.URL.Path == "/v1/servers" {
		body = `{"servers":[` + body + `]}`
	} else if !(req.Method == http.MethodGet && req.URL.Path == "/v1/servers/example") {
		if req.Method == http.MethodDelete && req.URL.Path == "/v1/servers/example" {
			status = http.StatusNoContent
			body = ""
		} else {
			status = http.StatusNotFound
			body = `{"error":"not found"}`
		}
	}

	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Request:    req,
	}, nil
}
