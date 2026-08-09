package routing

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kavrynt/gateway/internal/model"
)

type Table struct {
	mu        sync.RWMutex
	routes    map[string]model.Route
	lastSync  time.Time
	lastError string
	ready     bool
}

func NewTable() *Table {
	return &Table{routes: map[string]model.Route{}}
}

func (t *Table) Replace(records []model.ServerRecord, syncedAt time.Time) {
	routes := map[string]model.Route{}
	for _, record := range records {
		manifest := record.Manifest
		name := strings.TrimSpace(manifest.Metadata.Name)
		if name == "" {
			continue
		}
		routes[name] = model.Route{
			Name:      name,
			Version:   manifest.Spec.Version,
			Transport: manifest.Spec.Transport,
			Endpoint:  manifest.Spec.Endpoint,
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.routes = routes
	t.lastSync = syncedAt
	t.lastError = ""
	t.ready = true
}

func (t *Table) MarkSyncError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
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
