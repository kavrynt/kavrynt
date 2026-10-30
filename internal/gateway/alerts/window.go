package alerts

import (
	"sort"
	"sync"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/history"
)

// Window10m is how far back the traffic conditions look, like the [10m]
// ranges of the Prometheus rules.
const Window10m = 10 * time.Minute

const (
	slotLength = time.Minute
	slotCount  = int(Window10m / slotLength)
)

// Outcomes the traffic conditions count, as recorded by the Gateway.
var (
	upstreamFailures = map[string]bool{"upstream_error": true, "upstream_unreachable": true, "timeout": true}
	toolFailures     = map[string]bool{"tool_error": true, "rpc_error": true}
)

type routeCounts struct {
	calls    uint64
	failures uint64
	// latency counts calls per bucket; the last one is +Inf.
	latency []uint64
}

type toolKey struct{ route, tool string }

type toolCounts struct {
	calls  uint64
	errors uint64
}

type slot struct {
	start  time.Time
	routes map[string]*routeCounts
	tools  map[toolKey]*toolCounts
}

// Window keeps the Gateway's recorded calls of the last ten minutes in
// one-minute slots. Its labels are the bounded metric labels, so it holds
// at most one entry per routed server and per tool the metrics admit.
type Window struct {
	mu      sync.Mutex
	buckets []float64
	slots   [slotCount]slot
}

// NewWindow returns a window whose latency buckets are the upper bounds of
// the Gateway's duration histogram, in seconds.
func NewWindow(buckets []float64) *Window {
	return &Window{buckets: append([]float64(nil), buckets...)}
}

// Record counts one call. method and tool are the metric labels.
func (w *Window) Record(at time.Time, route, method, tool, outcome string, elapsed time.Duration) {
	start := at.Truncate(slotLength)
	w.mu.Lock()
	defer w.mu.Unlock()
	s := &w.slots[int(start.Unix()/int64(slotLength.Seconds()))%slotCount]
	if !s.start.Equal(start) {
		*s = slot{start: start, routes: map[string]*routeCounts{}, tools: map[toolKey]*toolCounts{}}
	}
	rc := s.routes[route]
	if rc == nil {
		rc = &routeCounts{latency: make([]uint64, len(w.buckets)+1)}
		s.routes[route] = rc
	}
	rc.calls++
	if upstreamFailures[outcome] {
		rc.failures++
	}
	rc.latency[sort.SearchFloat64s(w.buckets, elapsed.Seconds())]++
	if method == "tools/call" {
		key := toolKey{route, tool}
		tc := s.tools[key]
		if tc == nil {
			tc = &toolCounts{}
			s.tools[key] = tc
		}
		tc.calls++
		if toolFailures[outcome] {
			tc.errors++
		}
	}
}

// RouteStats are a route's calls in the window.
type RouteStats struct {
	Calls    uint64
	Failures uint64
	// P95 is the 95th percentile latency in seconds, NaN without calls.
	P95 float64
}

// ToolStats are a tool's tools/call requests in the window.
type ToolStats struct {
	Calls  uint64
	Errors uint64
}

// Snapshot sums the slots of the ten minutes before now.
func (w *Window) Snapshot(now time.Time) (map[string]RouteStats, map[[2]string]ToolStats) {
	w.mu.Lock()
	defer w.mu.Unlock()
	oldest := now.Truncate(slotLength).Add(-time.Duration(slotCount-1) * slotLength)
	latency := map[string][]uint64{}
	routes := map[string]RouteStats{}
	tools := map[[2]string]ToolStats{}
	for i := range w.slots {
		s := &w.slots[i]
		if s.start.Before(oldest) || s.start.After(now) {
			continue
		}
		for route, rc := range s.routes {
			stats := routes[route]
			stats.Calls += rc.calls
			stats.Failures += rc.failures
			routes[route] = stats
			counts := latency[route]
			if counts == nil {
				counts = make([]uint64, len(w.buckets)+1)
				latency[route] = counts
			}
			for b, n := range rc.latency {
				counts[b] += n
			}
		}
		for key, tc := range s.tools {
			stats := tools[[2]string{key.route, key.tool}]
			stats.Calls += tc.calls
			stats.Errors += tc.errors
			tools[[2]string{key.route, key.tool}] = stats
		}
	}
	for route, stats := range routes {
		stats.P95 = quantile(0.95, w.buckets, latency[route])
		routes[route] = stats
	}
	return routes, tools
}

// quantile is histogram_quantile over the window's latency buckets; the
// console dashboards compute their latencies the same way.
var quantile = history.Quantile[uint64]
