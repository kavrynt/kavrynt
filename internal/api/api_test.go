package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kavrynt/registry/internal/store"
)

func TestListServersReturnsEmptyArray(t *testing.T) {
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}
	handler := NewHandler(fileStore, Metadata{})

	req := httptest.NewRequest(http.MethodGet, "/v1/servers", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET list status = %d", rec.Code)
	}

	var body struct {
		Servers []any `json:"servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET list JSON = %v, body = %s", err, rec.Body.String())
	}
	if body.Servers == nil {
		t.Fatalf("GET list returned nil servers: %s", rec.Body.String())
	}
	if len(body.Servers) != 0 {
		t.Fatalf("GET list server count = %d", len(body.Servers))
	}
}

func TestServerLifecycleAPI(t *testing.T) {
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}
	handler := NewHandler(fileStore, Metadata{Version: "test", Commit: "abc", BuildDate: "now"})

	body := `{
  "apiVersion": "kavrynt.io/v1alpha1",
  "kind": "MCPServer",
  "metadata": {"name": "example"},
  "spec": {"version": "0.1.0", "transport": "http", "endpoint": "http://localhost:8080"}
}`

	req := httptest.NewRequest(http.MethodPost, "/v1/servers", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/servers", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET list status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"name":"example"`) {
		t.Fatalf("GET list body = %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/servers/example", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET inspect status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/servers/example", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", rec.Code)
	}
}

func TestRejectsUnknownJSONFields(t *testing.T) {
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}
	handler := NewHandler(fileStore, Metadata{})

	body := `{
  "apiVersion": "kavrynt.io/v1alpha1",
  "kind": "MCPServer",
  "metadata": {"name": "example"},
  "spec": {"version": "0.1.0", "transport": "http", "endpoint": "http://localhost:8080"},
  "unexpected": true
}`

	req := httptest.NewRequest(http.MethodPost, "/v1/servers", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
