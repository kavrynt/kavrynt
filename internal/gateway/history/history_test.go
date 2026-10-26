package history

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/model"
)

var (
	t0      = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}
)

func newTestHistory() *History {
	return New(buckets, DefaultMaxTools, t0.Add(-48*time.Hour))
}

func calls(p Point) uint64 { return p.OK + p.ToolError + p.UpstreamError + p.Other }

func total(points []Point) uint64 {
	var n uint64
	for _, p := range points {
		n += calls(p)
	}
	return n
}

// Verifies SPEC-0024-R01: outcomes fall into the four classes.
func TestClassOf(t *testing.T) {
	for outcome, want := range map[string]int{
		"ok": ClassOK, "tool_error": ClassToolError, "rpc_error": ClassToolError,
		"upstream_error": ClassUpstreamError, "upstream_unreachable": ClassUpstreamError, "timeout": ClassUpstreamError,
		"denied": ClassOther, "unauthenticated": ClassOther, "client_error": ClassOther, "incomplete": ClassOther, "anything": ClassOther,
	} {
		if got := ClassOf(outcome); got != want {
			t.Errorf("ClassOf(%q) = %s, want %s", outcome, ClassNames[got], ClassNames[want])
		}
	}
}

// Verifies SPEC-0024-R01: one-minute points for the last hour, five-minute
// points for 6 and 24 hours, with classes, latency, and bytes.
func TestQueryPoints(t *testing.T) {
	h := newTestHistory()
	now := t0.Add(30 * time.Second)
	h.Record(t0.Add(-10*time.Minute), "a.one", "tools/call", "search", "ok", 20*time.Millisecond, 100, 200)
	h.Record(t0.Add(-10*time.Minute), "a.one", "tools/call", "search", "tool_error", 20*time.Millisecond, 100, 200)
	h.Record(t0, "a.one", "tools/list", "none", "upstream_error", 3*time.Second, 10, 0)
	h.Record(t0, "b.two", "initialize", "none", "denied", time.Millisecond, 5, 7)

	hour := h.Query(now, "1h", "", "")
	if len(hour.Points) != 60 || hour.StepSeconds != 60 {
		t.Fatalf("1h: %d points of %ds, want 60 of 60s", len(hour.Points), hour.StepSeconds)
	}
	if !hour.Start.Equal(t0.Add(-59*time.Minute)) || !hour.End.Equal(t0.Add(time.Minute)) {
		t.Fatalf("1h spans %s to %s", hour.Start, hour.End)
	}
	tenAgo := hour.Points[49]
	if !tenAgo.Time.Equal(t0.Add(-10*time.Minute)) || tenAgo.OK != 1 || tenAgo.ToolError != 1 {
		t.Fatalf("point 10 minutes ago = %+v", tenAgo)
	}
	if *tenAgo.ErrorRate != 0.5 || *tenAgo.BytesIn != 200 || *tenAgo.BytesOut != 400 {
		t.Fatalf("point 10 minutes ago: rate %v, bytes %d/%d", *tenAgo.ErrorRate, *tenAgo.BytesIn, *tenAgo.BytesOut)
	}
	// Both calls fall in the 0.01-0.025 bucket: p50 by interpolation.
	if math.Abs(*tenAgo.P50Ms-17.5) > 1e-9 {
		t.Fatalf("p50 = %v ms, want 17.5", *tenAgo.P50Ms)
	}
	latest := hour.Points[59]
	if latest.UpstreamError != 1 || latest.Other != 1 || *latest.ErrorRate != 1 || *latest.BytesIn != 15 {
		t.Fatalf("latest point = %+v", latest)
	}
	empty := hour.Points[0]
	if empty.ErrorRate != nil || empty.P50Ms != nil || empty.P95Ms != nil || *empty.BytesIn != 0 {
		t.Fatalf("a point without calls must have null rates and latencies: %+v", empty)
	}

	for name, points := range map[string]int{"6h": 72, "24h": 288} {
		view := h.Query(now, name, "", "")
		if len(view.Points) != points || view.StepSeconds != 300 {
			t.Fatalf("%s: %d points of %ds, want %d of 300s", name, len(view.Points), view.StepSeconds, points)
		}
		if total(view.Points) != 4 {
			t.Fatalf("%s counts %d calls, want 4", name, total(view.Points))
		}
		if p := view.Points[len(view.Points)-3]; !p.Time.Equal(t0.Add(-10*time.Minute)) || calls(p) != 2 {
			t.Fatalf("%s: five-minute point = %+v", name, p)
		}
	}
}

// Verifies SPEC-0024-R01 and SPEC-0024-R04: all routes, one route, and one
// tool, with totals.
func TestQueryScopes(t *testing.T) {
	h := newTestHistory()
	for i := 0; i < 30; i++ {
		outcome := "ok"
		if i%3 == 0 {
			outcome = "tool_error"
		}
		h.Record(t0, "a.one", "tools/call", "search", outcome, 40*time.Millisecond, 1, 1)
	}
	h.Record(t0, "a.one", "tools/call", "fetch", "ok", time.Millisecond, 1, 1)
	h.Record(t0, "a.one", "tools/list", "none", "ok", time.Millisecond, 1, 1)
	h.Record(t0, "b.two", "tools/call", "search", "timeout", 31*time.Second, 1, 1)

	all := h.Query(t0, "1h", "", "")
	if total(all.Points) != 33 || len(all.Routes) != 2 || len(all.Tools) != 3 {
		t.Fatalf("all: %d calls, routes %+v, tools %+v", total(all.Points), all.Routes, all.Tools)
	}
	if r := all.Routes[0]; r.Route != "a.one" || r.Calls != 32 || r.Errors != 10 || r.P95Ms == nil {
		t.Fatalf("route total = %+v", r)
	}
	if r := all.Routes[1]; r.Route != "b.two" || *r.ErrorRate != 1 || *r.P95Ms != 30000 {
		t.Fatalf("route total = %+v (the +Inf bucket reports the highest bound)", r)
	}

	one := h.Query(t0, "1h", "a.one", "")
	if total(one.Points) != 32 || len(one.Routes) != 0 || len(one.Tools) != 2 {
		t.Fatalf("route: %d calls, routes %+v, tools %+v", total(one.Points), one.Routes, one.Tools)
	}
	if tool := one.Tools[1]; tool.Tool != "search" || tool.Calls != 30 || tool.Errors != 10 {
		t.Fatalf("tool total = %+v", tool)
	}

	tool := h.Query(t0, "1h", "a.one", "search")
	if total(tool.Points) != 30 || len(tool.Tools) != 0 || len(tool.Routes) != 0 {
		t.Fatalf("tool: %d calls, %+v %+v", total(tool.Points), tool.Routes, tool.Tools)
	}
	if p := tool.Points[59]; p.BytesIn != nil || p.BytesOut != nil {
		t.Fatalf("tool points must not carry bytes: %+v", p)
	}

	missing := h.Query(t0, "1h", "c.three", "")
	if len(missing.Points) != 60 || total(missing.Points) != 0 {
		t.Fatalf("a route without calls has empty points, got %d calls", total(missing.Points))
	}
}

// Verifies SPEC-0024-R01: the hour keeps minutes, the day keeps five-minute
// steps, and both forget what is older.
func TestRingsForgetOldSlots(t *testing.T) {
	h := newTestHistory()
	h.Record(t0, "a.one", "tools/list", "none", "ok", time.Millisecond, 0, 0)
	later := t0.Add(2 * time.Hour)
	h.Record(later, "a.one", "tools/list", "none", "ok", time.Millisecond, 0, 0)
	if got := total(h.Query(later, "1h", "", "").Points); got != 1 {
		t.Fatalf("1h two hours later counts %d calls, want 1", got)
	}
	if got := total(h.Query(later, "24h", "", "").Points); got != 2 {
		t.Fatalf("24h counts %d calls, want 2", got)
	}
	muchLater := t0.Add(25 * time.Hour)
	if got := total(h.Query(muchLater, "24h", "", "").Points); got != 1 {
		t.Fatalf("24h a day later counts %d calls, want 1", got)
	}
	// A slot reused after a full turn of the ring starts from zero.
	h.Record(t0.Add(24*time.Hour), "a.one", "tools/list", "none", "ok", time.Millisecond, 0, 0)
	if got := total(h.Query(t0.Add(24*time.Hour), "24h", "", "").Points); got != 2 {
		t.Fatalf("24h after a full turn counts %d calls, want 2", got)
	}
}

// Verifies SPEC-0024-R01: a call at an earlier time still in the ring
// counts there; one older than the ring is dropped.
func TestRecordAtAnEarlierTime(t *testing.T) {
	h := newTestHistory()
	h.Record(t0, "a.one", "tools/list", "none", "ok", time.Millisecond, 0, 0)
	h.Record(t0.Add(-5*time.Minute), "a.one", "tools/list", "none", "ok", time.Millisecond, 0, 0)
	h.Record(t0.Add(-2*time.Hour), "a.one", "tools/list", "none", "ok", time.Millisecond, 0, 0)
	hour := h.Query(t0, "1h", "", "")
	if hour.Points[54].OK != 1 || total(hour.Points) != 2 {
		t.Fatalf("1h: %d calls, 5 minutes ago %+v", total(hour.Points), hour.Points[54])
	}
	if got := total(h.Query(t0, "24h", "", "").Points); got != 3 {
		t.Fatalf("24h counts %d calls, want 3", got)
	}
}

// Verifies SPEC-0024-R04: points begin at the replica's start.
func TestSinceIsTheReplicaStart(t *testing.T) {
	h := New(buckets, DefaultMaxTools, t0.Add(-2*time.Hour+90*time.Second))
	h.Record(t0, "a.one", "tools/list", "none", "ok", time.Millisecond, 0, 0)
	day := h.Query(t0, "24h", "", "")
	if !day.Since.Equal(t0.Add(-2*time.Hour + 90*time.Second)) {
		t.Fatalf("since = %s", day.Since)
	}
	if len(day.Points) != 25 || !day.Points[0].Time.Equal(t0.Add(-2*time.Hour)) {
		t.Fatalf("24h after a 2-hour-old start: %d points from %s, want 25 from the start's step", len(day.Points), day.Points[0].Time)
	}
	if !day.Start.Equal(t0.Add(-24*time.Hour + 5*time.Minute)) {
		t.Fatalf("the range still starts a day ago: %s", day.Start)
	}
	hour := h.Query(t0, "1h", "", "")
	if !hour.Since.Equal(hour.Start) || len(hour.Points) != 60 {
		t.Fatalf("1h: since %s, start %s, %d points", hour.Since, hour.Start, len(hour.Points))
	}
}

// Verifies SPEC-0024-R02: named tools are capped across routes and further
// tools are counted as their route's other tool.
func TestToolCap(t *testing.T) {
	h := New(buckets, 3, t0.Add(-time.Hour))
	for i := 0; i < 3; i++ {
		h.Record(t0, "a.one", "tools/call", fmt.Sprintf("t%d", i), "ok", time.Millisecond, 0, 0)
	}
	h.Record(t0, "a.one", "tools/call", "t3", "ok", time.Millisecond, 0, 0)
	h.Record(t0, "b.two", "tools/call", "t4", "tool_error", time.Millisecond, 0, 0)
	h.Record(t0, "b.two", "tools/call", "t5", "ok", time.Millisecond, 0, 0)
	h.Record(t0, "a.one", "tools/call", "t0", "ok", time.Millisecond, 0, 0)

	tools := map[string]uint64{}
	for _, tool := range h.Query(t0, "1h", "", "").Tools {
		tools[tool.Route+"/"+tool.Tool] = tool.Calls
	}
	want := map[string]uint64{"a.one/t0": 2, "a.one/t1": 1, "a.one/t2": 1, "a.one/other": 1, "b.two/other": 2}
	if fmt.Sprint(tools) != fmt.Sprint(want) {
		t.Fatalf("tools = %v, want %v", tools, want)
	}
	if got := total(h.Query(t0, "1h", "b.two", "other").Points); got != 2 {
		t.Fatalf("b.two's other tool has %d calls, want 2", got)
	}
}

// Verifies SPEC-0024-R02: the series of deleted routes are dropped and free
// their place under the cap.
func TestRetainDropsDeletedRoutes(t *testing.T) {
	h := New(buckets, 1, t0.Add(-time.Hour))
	h.Record(t0, "a.one", "tools/call", "search", "ok", time.Millisecond, 0, 0)
	h.Record(t0, "b.two", "tools/call", "fetch", "ok", time.Millisecond, 0, 0)
	h.Retain(map[string]bool{"b.two": true})
	view := h.Query(t0, "1h", "", "")
	if len(view.Routes) != 1 || view.Routes[0].Route != "b.two" || len(view.Tools) != 1 || view.Tools[0].Tool != "other" {
		t.Fatalf("after retain: routes %+v tools %+v", view.Routes, view.Tools)
	}
	h.Record(t0, "b.two", "tools/call", "fetch", "ok", time.Millisecond, 0, 0)
	if tools := h.Query(t0, "1h", "b.two", "").Tools; len(tools) != 2 || tools[0].Tool != "fetch" {
		t.Fatalf("a freed place goes to the next tool: %+v", tools)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.Run(ctx, time.Millisecond, func() []model.Server { return nil })
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.Query(t0, "1h", "", "").Routes) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("Run did not drop the routes the table no longer has")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
}

// Verifies SPEC-0024-R02: 300 routes with 200 tools each, every slot
// written, stay under 32 MiB with the default cap.
func TestMemoryStaysUnderBudget(t *testing.T) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	h := New(buckets, DefaultMaxTools, t0.Add(-25*time.Hour))
	for route := 0; route < 300; route++ {
		for tool := 0; tool < 200; tool++ {
			h.Record(t0, fmt.Sprintf("ns.server-%d", route), "tools/call", fmt.Sprintf("tool-%d", tool), "ok", time.Millisecond, 1, 1)
		}
	}
	// Writing the newest slot of every ring allocates nothing more; the
	// rings are allocated whole.
	runtime.GC()
	runtime.ReadMemStats(&after)
	used := after.HeapAlloc - before.HeapAlloc
	t.Logf("history of 300 routes and %d tool series: %.1f MiB", len(h.tools), float64(used)/(1<<20))
	if used >= 32<<20 {
		t.Fatalf("history uses %.1f MiB, want under 32 MiB", float64(used)/(1<<20))
	}
	if h.named != DefaultMaxTools {
		t.Fatalf("%d named tools, want the cap %d", h.named, DefaultMaxTools)
	}
	runtime.KeepAlive(h)
}

// Verifies SPEC-0024-R03: recording into existing series allocates nothing
// and its work does not grow with the history.
func TestRecordIsConstantWork(t *testing.T) {
	h := newTestHistory()
	for i := 0; i < 1000; i++ {
		h.Record(t0, fmt.Sprintf("ns.s-%d", i), "tools/call", "search", "ok", time.Millisecond, 1, 1)
	}
	at := t0
	allocs := testing.AllocsPerRun(1000, func() {
		at = at.Add(time.Second)
		h.Record(at, "ns.s-500", "tools/call", "search", "tool_error", 30*time.Millisecond, 10, 20)
	})
	if allocs != 0 {
		t.Fatalf("Record allocates %.1f times per call, want 0", allocs)
	}

	// The zeroing a long gap needs is bounded by the ring, not the gap.
	small := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			h.Record(t0.Add(time.Duration(i)*48*time.Hour), "ns.s-1", "tools/list", "none", "ok", time.Millisecond, 0, 0)
		}
	})
	if perCall := time.Duration(small.NsPerOp()); perCall > time.Millisecond {
		t.Fatalf("a call after a long gap takes %s", perCall)
	}
}

// Verifies SPEC-0024-R03: a query copies one series at a time, so a call
// to another series is recorded while a query runs.
func TestRecordDoesNotWaitForAQuery(t *testing.T) {
	h := newTestHistory()
	slow := h.newSeries(true)
	h.routes["a.slow"] = slow
	slow.mu.Lock() // a query holding one series
	done := make(chan struct{})
	go func() {
		h.Record(t0, "b.fast", "tools/call", "search", "ok", time.Millisecond, 0, 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("recording another series waited for the locked one")
	}
	slow.mu.Unlock()
}

// Verifies SPEC-0024-R04: p50 and p95 are computed like histogram_quantile.
func TestQuantile(t *testing.T) {
	bounds := []float64{1, 2, 5}
	if got := Quantile(0.95, bounds, []uint32{0, 0, 0, 0}); !math.IsNaN(got) {
		t.Fatalf("no calls: %v, want NaN", got)
	}
	if got := Quantile(0.95, bounds, []uint32{0, 10, 0, 0}); math.Abs(got-1.95) > 1e-9 {
		t.Fatalf("interpolated: %v, want 1.95", got)
	}
	if got := Quantile(0.5, bounds, []uint64{0, 0, 0, 10}); got != 5 {
		t.Fatalf("+Inf bucket: %v, want 5", got)
	}
}
