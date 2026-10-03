package routing

import (
	"errors"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/model"
)

func TestReplaceBuildsRoutes(t *testing.T) {
	table := NewTable()
	if ready, _, _, _ := table.Status(); ready {
		t.Fatal("new table must not be ready")
	}

	table.Replace([]model.Route{
		{Name: "team-b.search", Version: "1.0.0", Transport: "http", Endpoint: "http://search:8080"},
		{Name: "team-a.payments", Version: "0.1.0", Transport: "http", Endpoint: "http://payments:8080"},
		{Name: ""},
	}, time.Unix(100, 0))

	routes := table.List()
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(routes))
	}
	if routes[0].Name != "team-a.payments" || routes[1].Name != "team-b.search" {
		t.Fatalf("routes not sorted: %+v", routes)
	}
	if ready, synced, _, count := table.Status(); !ready || !synced.Equal(time.Unix(100, 0)) || count != 2 {
		t.Fatalf("status = %v %v %d", ready, synced, count)
	}
	if route, ok := table.Get("team-a.payments"); !ok || route.Endpoint != "http://payments:8080" {
		t.Fatalf("Get = %+v %v", route, ok)
	}
}

func TestReplaceRemovesDeletedRoutes(t *testing.T) {
	table := NewTable()
	table.Replace([]model.Route{{Name: "a", Transport: "http"}}, time.Now())
	table.Replace(nil, time.Now())
	if _, ok := table.Get("a"); ok {
		t.Fatal("route a should be removed")
	}
}

func TestMarkSyncErrorKeepsRoutes(t *testing.T) {
	table := NewTable()
	table.Replace([]model.Route{{Name: "a", Transport: "http"}}, time.Now())
	table.MarkSyncError(errors.New("watch failed"))

	if _, ok := table.Get("a"); !ok {
		t.Fatal("route a should be kept after a sync error")
	}
	if _, _, lastError, _ := table.Status(); lastError != "watch failed" {
		t.Fatalf("lastError = %q", lastError)
	}
	if syncs, failures := table.SyncCounts(); syncs != 1 || failures != 1 {
		t.Fatalf("counts = %d/%d, want 1/1", syncs, failures)
	}
}
