package routing

import (
	"testing"
	"time"

	"github.com/kavrynt/gateway/internal/model"
)

func TestReplaceBuildsRoutes(t *testing.T) {
	table := NewTable()
	table.Replace([]model.ServerRecord{
		{
			Manifest: model.Manifest{
				Metadata: model.Metadata{Name: "b-server"},
				Spec: model.Spec{
					Version:   "0.1.0",
					Transport: "http",
					Endpoint:  "http://b.test/mcp",
				},
			},
		},
		{
			Manifest: model.Manifest{
				Metadata: model.Metadata{Name: "a-server"},
				Spec: model.Spec{
					Version:   "0.1.0",
					Transport: "stdio",
					Command:   "demo",
				},
			},
		},
	}, time.Now())

	ready, _, lastError, count := table.Status()
	if !ready {
		t.Fatal("table should be ready after successful replace")
	}
	if lastError != "" {
		t.Fatalf("lastError = %q, want empty", lastError)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}

	routes := table.List()
	if routes[0].Name != "a-server" || routes[1].Name != "b-server" {
		t.Fatalf("routes not sorted by name: %#v", routes)
	}
}

func TestReplaceBuildsNamespaceQualifiedRoutes(t *testing.T) {
	table := NewTable()
	table.Replace([]model.ServerRecord{
		{
			Manifest: model.Manifest{
				Metadata: model.Metadata{Name: "payments", Namespace: "development"},
				Spec:     model.Spec{Version: "0.1.0", Transport: "http", Endpoint: "http://development.test/mcp"},
			},
		},
		{
			Manifest: model.Manifest{
				Metadata: model.Metadata{Name: "payments", Namespace: "production"},
				Spec:     model.Spec{Version: "0.1.0", Transport: "http", Endpoint: "http://production.test/mcp"},
			},
		},
	}, time.Now())

	development, ok := table.Get("development.payments")
	if !ok || development.Endpoint != "http://development.test/mcp" {
		t.Fatalf("development route = %#v, exists = %v", development, ok)
	}
	production, ok := table.Get("production.payments")
	if !ok || production.Endpoint != "http://production.test/mcp" {
		t.Fatalf("production route = %#v, exists = %v", production, ok)
	}
}
