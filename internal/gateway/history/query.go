package history

import (
	"math"
	"sort"
	"time"
)

// Point is the calls of one step (SPEC-0024-R04). Rates and latencies are
// null without calls; bytes are left out for a tool, which has none.
type Point struct {
	Time          time.Time `json:"time"`
	OK            uint64    `json:"ok"`
	ToolError     uint64    `json:"toolError"`
	UpstreamError uint64    `json:"upstreamError"`
	Other         uint64    `json:"other"`
	ErrorRate     *float64  `json:"errorRate"`
	P50Ms         *float64  `json:"p50Ms"`
	P95Ms         *float64  `json:"p95Ms"`
	BytesIn       *uint64   `json:"bytesIn,omitempty"`
	BytesOut      *uint64   `json:"bytesOut,omitempty"`
}

// Total is a route's or tool's calls over the whole range.
type Total struct {
	Route     string   `json:"route"`
	Tool      string   `json:"tool,omitempty"`
	Calls     uint64   `json:"calls"`
	Errors    uint64   `json:"errors"`
	ErrorRate *float64 `json:"errorRate"`
	P95Ms     *float64 `json:"p95Ms"`
}

// View is a range of history for all routes, one route, or one tool.
type View struct {
	Range       string    `json:"range"`
	StepSeconds int64     `json:"stepSeconds"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	// Since is when the points begin: the range's start, or the replica's
	// start when that is later.
	Since  time.Time `json:"since"`
	Points []Point   `json:"points"`
	// Routes holds each route's totals for all routes; Tools each tool's
	// for all routes and one route. Both are empty for one tool.
	Routes []Total `json:"routes"`
	Tools  []Total `json:"tools"`
}

// sums accumulates slots of one or more series.
type sums struct {
	counts []uint64
	bytes  []uint64
}

func newSums(width int, withBytes bool) *sums {
	s := &sums{counts: make([]uint64, width)}
	if withBytes {
		s.bytes = make([]uint64, 2)
	}
	return s
}

func (s *sums) calls() uint64 {
	var n uint64
	for _, c := range s.counts[:classCount] {
		n += c
	}
	return n
}

// Query returns the range named name, ending with the step that holds now,
// for all routes (route ""), one route, or one of its tools.
func (h *History) Query(now time.Time, name string, route, tool string) View {
	rng := Ranges[name]
	index := 0
	for i, spec := range rings {
		if spec.step == rng.Step {
			index = i
		}
	}
	step := int64(rng.Step / time.Second)
	last := now.Unix() / step
	first := last - int64(rng.Length/rng.Step) + 1
	start := time.Unix(first*step, 0).UTC()
	view := View{
		Range: name, StepSeconds: step, Start: start, End: time.Unix((last+1)*step, 0).UTC(), Since: start,
		Points: []Point{}, Routes: []Total{}, Tools: []Total{},
	}
	if started := h.started.UTC(); started.After(start) {
		view.Since = started
		if n := started.Unix() / step; n > first {
			first = n
		}
	}
	if first > last {
		return view
	}

	var pointSeries []*series
	var routeTotals, toolTotals []seriesRef
	h.mu.RLock()
	switch {
	case route == "":
		for r, s := range h.routes {
			pointSeries = append(pointSeries, s)
			routeTotals = append(routeTotals, seriesRef{toolKey{route: r}, s})
		}
		for key, s := range h.tools {
			toolTotals = append(toolTotals, seriesRef{key, s})
		}
	case tool == "":
		if s := h.routes[route]; s != nil {
			pointSeries = append(pointSeries, s)
		}
		for key, s := range h.tools {
			if key.route == route {
				toolTotals = append(toolTotals, seriesRef{key, s})
			}
		}
	default:
		if s := h.tools[toolKey{route, tool}]; s != nil {
			pointSeries = append(pointSeries, s)
		}
	}
	h.mu.RUnlock()

	withBytes := tool == ""
	points := make([]*sums, last-first+1)
	for i := range points {
		points[i] = newSums(h.width, withBytes)
	}
	for _, s := range pointSeries {
		s.read(index, first, last, h.width, func(n int64, counts []uint32, bytes []uint64) {
			p := points[n-first]
			for i, c := range counts {
				p.counts[i] += uint64(c)
			}
			if p.bytes != nil && bytes != nil {
				p.bytes[0] += bytes[0]
				p.bytes[1] += bytes[1]
			}
		})
	}
	for i, p := range points {
		view.Points = append(view.Points, h.point(time.Unix((first+int64(i))*step, 0).UTC(), p))
	}
	view.Routes = h.totals(routeTotals, index, first, last)
	view.Tools = h.totals(toolTotals, index, first, last)
	return view
}

// seriesRef is a series and the route and tool it counts.
type seriesRef struct {
	key toolKey
	s   *series
}

func (h *History) totals(list []seriesRef, index int, first, last int64) []Total {
	totals := []Total{}
	for _, entry := range list {
		sum := newSums(h.width, false)
		entry.s.read(index, first, last, h.width, func(_ int64, counts []uint32, _ []uint64) {
			for i, c := range counts {
				sum.counts[i] += uint64(c)
			}
		})
		calls := sum.calls()
		if calls == 0 {
			continue
		}
		errors := calls - sum.counts[ClassOK]
		totals = append(totals, Total{
			Route: entry.key.route, Tool: entry.key.tool, Calls: calls, Errors: errors,
			ErrorRate: ratio(errors, calls), P95Ms: h.quantileMs(0.95, sum),
		})
	}
	sort.Slice(totals, func(i, j int) bool {
		if totals[i].Route != totals[j].Route {
			return totals[i].Route < totals[j].Route
		}
		return totals[i].Tool < totals[j].Tool
	})
	return totals
}

func (h *History) point(at time.Time, s *sums) Point {
	calls := s.calls()
	p := Point{
		Time: at, OK: s.counts[ClassOK], ToolError: s.counts[ClassToolError],
		UpstreamError: s.counts[ClassUpstreamError], Other: s.counts[ClassOther],
		ErrorRate: ratio(calls-s.counts[ClassOK], calls),
		P50Ms:     h.quantileMs(0.5, s), P95Ms: h.quantileMs(0.95, s),
	}
	if s.bytes != nil {
		in, out := s.bytes[0], s.bytes[1]
		p.BytesIn, p.BytesOut = &in, &out
	}
	return p
}

func (h *History) quantileMs(q float64, s *sums) *float64 {
	v := Quantile(q, h.buckets, s.counts[classCount:])
	if math.IsNaN(v) {
		return nil
	}
	v *= 1000
	return &v
}

func ratio(n, of uint64) *float64 {
	if of == 0 {
		return nil
	}
	r := float64(n) / float64(of)
	return &r
}

// read calls fn with the counts and bytes of each slot from first to last
// that the ring still holds, under the series' lock.
func (s *series) read(index int, first, last int64, width int, fn func(n int64, counts []uint32, bytes []uint64)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := &s.rings[index]
	if r.newest == math.MinInt64 {
		return
	}
	for n := max(first, r.newest-r.slots+1); n <= min(last, r.newest); n++ {
		at := int(n % r.slots)
		var bytes []uint64
		if r.bytes != nil {
			bytes = r.bytes[at*2 : at*2+2]
		}
		fn(n, r.counts[at*width:(at+1)*width], bytes)
	}
}
