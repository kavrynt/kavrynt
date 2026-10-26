// Package history keeps the Gateway's recorded calls of the last 24 hours in
// memory for the console dashboards (SPEC-0024): per route and per tool,
// calls by outcome class and latency, and per route bytes, at one-minute
// points for the last hour and five-minute points for the last day.
//
// Its labels are the bounded metric labels, and named tools are capped
// across routes, so its size is fixed by the number of routes. It is per
// replica and starts again when the Gateway restarts.
package history

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/model"
)

// Outcome classes (SPEC-0024-R01).
const (
	ClassOK = iota
	ClassToolError
	ClassUpstreamError
	ClassOther
	classCount
)

// ClassNames are the JSON names of the classes, by class.
var ClassNames = [classCount]string{"ok", "toolError", "upstreamError", "other"}

var classes = map[string]int{
	"ok":                   ClassOK,
	"tool_error":           ClassToolError,
	"rpc_error":            ClassToolError,
	"upstream_error":       ClassUpstreamError,
	"upstream_unreachable": ClassUpstreamError,
	"timeout":              ClassUpstreamError,
}

// ClassOf returns the class of an outcome; every outcome not named above,
// such as a call the Gateway refused, is ClassOther.
func ClassOf(outcome string) int {
	if class, ok := classes[outcome]; ok {
		return class
	}
	return ClassOther
}

// DefaultMaxTools caps the named tool series across routes
// (gateway.console.history.maxTools).
const DefaultMaxTools = 500

// OtherTool is the tool a route's calls to tools beyond the cap are counted
// as, like the metrics' label for tools beyond their limit.
const OtherTool = "other"

// PruneInterval is how often Run drops the series of deleted routes.
const PruneInterval = time.Minute

// Range is a span the dashboards show and the distance of its points.
type Range struct {
	Length time.Duration
	Step   time.Duration
}

// Ranges are the spans /v1/history serves (SPEC-0024-R04).
var Ranges = map[string]Range{
	"1h":  {Length: time.Hour, Step: time.Minute},
	"6h":  {Length: 6 * time.Hour, Step: 5 * time.Minute},
	"24h": {Length: 24 * time.Hour, Step: 5 * time.Minute},
}

// rings are the resolutions every series keeps: an hour of minutes and a
// day of five minutes.
var rings = [2]struct {
	step  time.Duration
	slots int
}{{time.Minute, 60}, {5 * time.Minute, 288}}

// ring is one resolution of a series. counts holds width uint32 per slot:
// the classes, then the latency buckets; bytes holds in and out per slot
// for route series and is nil for tool series. Slot n (the time divided by
// step) is at n modulo the slot count. newest is the newest slot written;
// writing a later one zeroes the slots in between, so a slot is valid while
// it is within the slot count of newest and needs no timestamp.
type ring struct {
	step   int64
	slots  int64
	newest int64
	counts []uint32
	bytes  []uint64
}

type series struct {
	mu    sync.Mutex
	rings [len(rings)]ring
}

type toolKey struct{ route, tool string }

// History is safe for concurrent use. Recording locks one series; queries
// copy one series at a time (SPEC-0024-R03).
type History struct {
	buckets  []float64
	width    int
	maxTools int
	started  time.Time

	mu     sync.RWMutex
	routes map[string]*series
	tools  map[toolKey]*series
	// named counts the tool series other than the routes' OtherTool.
	named int
}

// New returns a history whose latency buckets are the upper bounds of the
// Gateway's duration histogram, in seconds, holding at most maxTools named
// tools. started is when the replica's history begins.
func New(buckets []float64, maxTools int, started time.Time) *History {
	if maxTools < 1 {
		maxTools = DefaultMaxTools
	}
	return &History{
		buckets:  append([]float64(nil), buckets...),
		width:    classCount + len(buckets) + 1,
		maxTools: maxTools,
		started:  started,
		routes:   map[string]*series{},
		tools:    map[toolKey]*series{},
	}
}

// Started is when this history began.
func (h *History) Started() time.Time { return h.started }

func (h *History) newSeries(withBytes bool) *series {
	s := &series{}
	for i, spec := range rings {
		r := ring{step: int64(spec.step / time.Second), slots: int64(spec.slots), newest: math.MinInt64,
			counts: make([]uint32, spec.slots*h.width)}
		if withBytes {
			r.bytes = make([]uint64, spec.slots*2)
		}
		s.rings[i] = r
	}
	return s
}

// Record counts one call. method and tool are the bounded metric labels.
func (h *History) Record(at time.Time, route, method, tool, outcome string, elapsed time.Duration, bytesIn, bytesOut int64) {
	class := ClassOf(outcome)
	bucket := classCount + sort.SearchFloat64s(h.buckets, elapsed.Seconds())
	unix := at.Unix()
	routeSeries, toolSeries := h.lookup(route, method == "tools/call", tool)
	routeSeries.add(unix, h.width, class, bucket, nonNegative(bytesIn), nonNegative(bytesOut))
	if toolSeries != nil {
		toolSeries.add(unix, h.width, class, bucket, 0, 0)
	}
}

// lookup finds or creates the series of a call. Existing series need only
// the read lock.
func (h *History) lookup(route string, isTool bool, tool string) (*series, *series) {
	h.mu.RLock()
	routeSeries := h.routes[route]
	var toolSeries *series
	if isTool {
		toolSeries = h.tools[toolKey{route, tool}]
		if toolSeries == nil && h.named >= h.maxTools {
			toolSeries = h.tools[toolKey{route, OtherTool}]
		}
	}
	h.mu.RUnlock()
	if routeSeries != nil && (!isTool || toolSeries != nil) {
		return routeSeries, toolSeries
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if routeSeries = h.routes[route]; routeSeries == nil {
		routeSeries = h.newSeries(true)
		h.routes[route] = routeSeries
	}
	if !isTool {
		return routeSeries, nil
	}
	key := toolKey{route, tool}
	if toolSeries = h.tools[key]; toolSeries != nil {
		return routeSeries, toolSeries
	}
	if tool != OtherTool && h.named >= h.maxTools {
		key.tool = OtherTool
		if toolSeries = h.tools[key]; toolSeries != nil {
			return routeSeries, toolSeries
		}
	}
	toolSeries = h.newSeries(false)
	h.tools[key] = toolSeries
	if key.tool != OtherTool {
		h.named++
	}
	return routeSeries, toolSeries
}

func (s *series) add(unix int64, width, class, bucket int, bytesIn, bytesOut uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rings {
		r := &s.rings[i]
		n := unix / r.step
		if n > r.newest {
			// Zero the slots skipped since the newest write, at most the
			// whole ring: constant work per call.
			from := r.newest + 1
			if r.newest == math.MinInt64 || n-r.newest > r.slots {
				from = n - r.slots + 1
			}
			for m := from; m <= n; m++ {
				r.clear(m, width)
			}
			r.newest = n
		} else if n <= r.newest-r.slots {
			continue
		}
		at := int(n % r.slots)
		r.counts[at*width+class]++
		r.counts[at*width+bucket]++
		if r.bytes != nil {
			r.bytes[at*2] += bytesIn
			r.bytes[at*2+1] += bytesOut
		}
	}
}

func (r *ring) clear(n int64, width int) {
	at := int(n % r.slots)
	clear(r.counts[at*width : (at+1)*width])
	if r.bytes != nil {
		r.bytes[at*2], r.bytes[at*2+1] = 0, 0
	}
}

func nonNegative(n int64) uint64 {
	if n < 0 {
		return 0
	}
	return uint64(n)
}

// Retain drops the series of routes that are not in routes (SPEC-0024-R02).
func (h *History) Retain(routes map[string]bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for route := range h.routes {
		if !routes[route] {
			delete(h.routes, route)
		}
	}
	for key := range h.tools {
		if !routes[key.route] {
			delete(h.tools, key)
			if key.tool != OtherTool {
				h.named--
			}
		}
	}
}

// Run drops the series of deleted MCPServers every interval until ctx ends.
func (h *History) Run(ctx context.Context, interval time.Duration, servers func() []model.Server) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			routes := map[string]bool{}
			for _, server := range servers() {
				routes[server.Route] = true
			}
			h.Retain(routes)
		}
	}
}

// Quantile estimates a quantile from bucket counts the way Prometheus'
// histogram_quantile does: linear interpolation inside the bucket that
// holds the rank, the highest finite bound for the +Inf bucket. It returns
// NaN without counts.
func Quantile[T uint32 | uint64](q float64, bounds []float64, counts []T) float64 {
	var total uint64
	for _, n := range counts {
		total += uint64(n)
	}
	if total == 0 {
		return math.NaN()
	}
	rank := q * float64(total)
	var cumulative uint64
	for i, n := range counts {
		previous := cumulative
		cumulative += uint64(n)
		if float64(cumulative) < rank {
			continue
		}
		if i == len(bounds) {
			return bounds[len(bounds)-1]
		}
		lower := 0.0
		if i > 0 {
			lower = bounds[i-1]
		}
		if n == 0 {
			return bounds[i]
		}
		return lower + (bounds[i]-lower)*(rank-float64(previous))/float64(n)
	}
	return bounds[len(bounds)-1]
}
