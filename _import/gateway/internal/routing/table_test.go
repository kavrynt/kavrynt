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
