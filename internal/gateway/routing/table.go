package routing

import (
	"sort"
	"sync"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/model"
)

type Table struct {
	mu        sync.RWMutex
	routes    map[string]model.Route
	lastSync  time.Time
	lastError string
	ready     bool
	syncs     uint64
	failures  uint64
}

func NewTable() *Table {
	return &Table{routes: map[string]model.Route{}}
}

// Replace swaps the full route set and marks the table ready.
func (t *Table) Replace(routes []model.Route, syncedAt time.Time) {
	next := make(map[string]model.Route, len(routes))
	for _, route := range routes {
		if route.Name == "" {
			continue
		}
		next[route.Name] = route
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.routes = next
	t.lastSync = syncedAt
	t.lastError = ""
	t.ready = true
	t.syncs++
}

// MarkSyncError records a failed sync. Existing routes are kept.
func (t *Table) MarkSyncError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures++
	if err != nil {
		t.lastError = err.Error()
	}
}

func (t *Table) Get(name string) (model.Route, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	route, ok := t.routes[name]
	return route, ok
}

func (t *Table) List() []model.Route {
	t.mu.RLock()
	defer t.mu.RUnlock()
	routes := make([]model.Route, 0, len(t.routes))
	for _, route := range t.routes {
		routes = append(routes, route)
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Name < routes[j].Name
	})
	return routes
}

func (t *Table) Status() (bool, time.Time, string, int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.ready, t.lastSync, t.lastError, len(t.routes)
}

// SyncCounts returns the number of successful and failed syncs.
func (t *Table) SyncCounts() (uint64, uint64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.syncs, t.failures
}
