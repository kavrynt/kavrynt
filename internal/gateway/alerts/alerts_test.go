package alerts

import (
	"math"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/model"
)

var (
	t0      = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}
)

func testConfig() Config {
	cfg := DefaultConfig("1.2.3")
	cfg.RunbookBaseURL = "https://runbooks.test"
	return cfg
}

func newTestEvaluator() (*Evaluator, *Window) {
	window := NewWindow(buckets)
	return NewEvaluator(window, testConfig), window
}

func server(ready, discovered *model.Condition) model.Server {
	s := model.Server{Namespace: "team-a", Name: "search", Route: "team-a.search", CreatedAt: t0.Add(-time.Hour)}
	if ready != nil {
		s.Conditions = append(s.Conditions, *ready)
	}
	if discovered != nil {
		s.Conditions = append(s.Conditions, *discovered)
	}
	return s
}

func cond(conditionType, status, reason string, since time.Time) *model.Condition {
	return &model.Condition{Type: conditionType, Status: status, Reason: reason, LastTransitionTime: since}
}

func readyServer() model.Server {
	return server(cond("Ready", "True", "Accepted", t0.Add(-time.Hour)), cond("ToolsDiscovered", "True", "Discovered", t0.Add(-time.Hour)))
}

func names(alerts []Alert) []string {
	out := []string{}
	for _, a := range alerts {
		state := "active"
		if a.ResolvedAt != nil {
			state = "resolved"
		}
		out = append(out, a.Name+"/"+a.Scope.Route+"/"+a.Scope.Tool+"/"+state)
	}
	return out
}

func assertAlerts(t *testing.T, e *Evaluator, want ...string) []Alert {
	t.Helper()
	got, _ := e.List()
	if g, w := names(got), want; len(g) != len(w) || (len(w) > 0 && !equal(g, w)) {
		t.Fatalf("alerts = %q, want %q", g, w)
	}
	return got
}

func equal(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Verifies SPEC-0020-R01, SPEC-0020-R02, and SPEC-0020-R08: ServerNotReady is
// pending until the server has not been Ready for notReadyFor, then active
// since its Ready condition changed, and resolves when it is Ready again.
func TestServerNotReady(t *testing.T) {
	e, _ := newTestEvaluator()
	notReady := server(cond("Ready", "False", "EndpointRejected", t0), nil)

	e.Evaluate(t0.Add(9*time.Minute), []model.Server{notReady})
	assertAlerts(t, e)

	e.Evaluate(t0.Add(10*time.Minute), []model.Server{notReady})
	alerts := assertAlerts(t, e, "ServerNotReady/team-a.search//active")
	a := alerts[0]
	if !a.Since.Equal(t0) || a.Severity != SeverityWarning || a.RunbookURL != "https://runbooks.test/KavryntMCPServerNotReady.md" {
		t.Errorf("alert = %+v", a)
	}
	if a.Summary != "MCPServer team-a/search is not Ready (EndpointRejected); the Gateway does not route it." {
		t.Errorf("summary = %q", a.Summary)
	}

	e.Evaluate(t0.Add(12*time.Minute), []model.Server{readyServer()})
	alerts = assertAlerts(t, e, "ServerNotReady/team-a.search//resolved")
	if !alerts[0].ResolvedAt.Equal(t0.Add(12 * time.Minute)) {
		t.Errorf("resolvedAt = %v", alerts[0].ResolvedAt)
	}
}

// Verifies SPEC-0020-R02: a condition that stops holding while pending
// leaves nothing behind.
func TestPendingConditionThatStopsIsDropped(t *testing.T) {
	e, _ := newTestEvaluator()
	e.Evaluate(t0.Add(time.Minute), []model.Server{server(cond("Ready", "False", "EndpointRejected", t0), nil)})
	e.Evaluate(t0.Add(2*time.Minute), []model.Server{readyServer()})
	assertAlerts(t, e)
}

// Verifies SPEC-0020-R01, SPEC-0020-R02, and SPEC-0020-R08: DiscoveryFailing
// fires for a Ready server whose discovery failed for discoveryFailingFor,
// and resolves when its tools are listed.
func TestDiscoveryFailing(t *testing.T) {
	e, _ := newTestEvaluator()
	ready := cond("Ready", "True", "Accepted", t0.Add(-time.Hour))
	failing := server(ready, cond("ToolsDiscovered", "False", "DiscoveryFailed", t0))

	e.Evaluate(t0.Add(14*time.Minute), []model.Server{failing})
	assertAlerts(t, e)
	e.Evaluate(t0.Add(15*time.Minute), []model.Server{failing})
	alerts := assertAlerts(t, e, "DiscoveryFailing/team-a.search//active")
	if alerts[0].RunbookURL != "https://runbooks.test/KavryntMCPToolDiscoveryFailing.md" {
		t.Errorf("runbook = %q", alerts[0].RunbookURL)
	}
	e.Evaluate(t0.Add(16*time.Minute), []model.Server{readyServer()})
	assertAlerts(t, e, "DiscoveryFailing/team-a.search//resolved")
}

// Verifies SPEC-0020-R01: only failed discoveries of Ready servers count,
// like KavryntMCPToolDiscoveryFailing.
func TestDiscoveryNotListedByDesignIsNoAlert(t *testing.T) {
	e, _ := newTestEvaluator()
	ready := cond("Ready", "True", "Accepted", t0.Add(-time.Hour))
	e.Evaluate(t0.Add(time.Hour), []model.Server{
		server(ready, cond("ToolsDiscovered", "False", "CallerTokenRequired", t0)),
	})
	assertAlerts(t, e)

	credential := server(ready, cond("ToolsDiscovered", "False", "CredentialUnavailable", t0))
	e.Evaluate(t0.Add(time.Hour), []model.Server{credential})
	assertAlerts(t, e, "DiscoveryFailing/team-a.search//active")
}

// Verifies SPEC-0020-R04: status conditions start at the condition's last
// transition, so two replicas that started at different times agree.
func TestStatusAlertsAgreeAcrossReplicas(t *testing.T) {
	early, _ := newTestEvaluator()
	late, _ := newTestEvaluator()
	notReady := []model.Server{server(cond("Ready", "False", "EndpointRejected", t0), nil)}
	early.Evaluate(t0.Add(time.Minute), notReady)
	early.Evaluate(t0.Add(20*time.Minute), notReady)
	late.Evaluate(t0.Add(20*time.Minute), notReady)
	a, _ := early.List()
	b, _ := late.List()
	if len(a) != 1 || len(b) != 1 || !a[0].Since.Equal(b[0].Since) {
		t.Fatalf("replicas disagree: %+v and %+v", a, b)
	}
}

// calls is one minute of traffic to team-a.search.
type calls struct {
	method, tool, outcome string
	total, failing        int
	elapsed               time.Duration
}

// minutes records each minute's traffic from minute from to minute to
// (exclusive) and evaluates 30 seconds into the minute, the way traffic
// arrives; it returns the alerts after each evaluation.
func minutes(e *Evaluator, w *Window, from, to int, traffic ...calls) [][]string {
	var seen [][]string
	for m := from; m < to; m++ {
		at := t0.Add(time.Duration(m) * time.Minute)
		for _, c := range traffic {
			for i := 0; i < c.total; i++ {
				outcome := "ok"
				if i < c.failing {
					outcome = c.outcome
				}
				w.Record(at, "team-a.search", c.method, c.tool, outcome, c.elapsed)
			}
		}
		e.Evaluate(at.Add(30*time.Second), []model.Server{readyServer()})
		list, _ := e.List()
		seen = append(seen, names(list))
	}
	return seen
}

// firstActive is the minute at which an alert was first listed, or -1.
func firstActive(seen [][]string) int {
	for i, list := range seen {
		if len(list) > 0 {
			return i
		}
	}
	return -1
}

// Verifies SPEC-0020-R01, SPEC-0020-R02, and SPEC-0020-R08: UpstreamErrors
// is pending until the route has had minRequestsPerSecond and errorRatio
// for 5 minutes, then active, and resolves when the failures stop.
func TestUpstreamErrors(t *testing.T) {
	e, w := newTestEvaluator()
	// 4 calls a minute, 1 failing: 25%. 30 calls take 8 minutes.
	failing := calls{"tools/call", "lookup", "upstream_unreachable", 4, 1, 10 * time.Millisecond}
	seen := minutes(e, w, 0, 14, failing)
	if got := firstActive(seen); got != 12 {
		t.Fatalf("UpstreamErrors first listed at minute %d, want 12 (30 calls at minute 7, then 5 minutes): %q", got, seen)
	}
	alerts := assertAlerts(t, e, "UpstreamErrors/team-a.search//active")
	if !alerts[0].Since.Equal(t0.Add(7*time.Minute + 30*time.Second)) {
		t.Errorf("since = %v, want the first evaluation that saw it", alerts[0].Since)
	}
	if alerts[0].Summary != "25% of 40 calls to team-a.search failed upstream (upstream_error, upstream_unreachable, or timeout) in the last 10 minutes." {
		t.Errorf("summary = %q", alerts[0].Summary)
	}
	if alerts[0].RunbookURL != "https://runbooks.test/KavryntMCPUpstreamErrorsHigh.md" {
		t.Errorf("runbook = %q", alerts[0].RunbookURL)
	}
	// Healthy calls dilute the failures below 5% within a few minutes.
	minutes(e, w, 14, 30, calls{"tools/call", "lookup", "ok", 40, 0, 10 * time.Millisecond})
	assertAlerts(t, e, "UpstreamErrors/team-a.search//resolved")
}

// Verifies SPEC-0020-R01: routes with fewer calls than minRequestsPerSecond
// over 10 minutes (30) never raise UpstreamErrors.
func TestUpstreamErrorsNeedTraffic(t *testing.T) {
	e, w := newTestEvaluator()
	minutes(e, w, 0, 30, calls{"tools/call", "lookup", "upstream_error", 2, 2, time.Millisecond})
	assertAlerts(t, e)
}

// Verifies SPEC-0020-R01, SPEC-0020-R02, and SPEC-0020-R08: a tool that
// fails 30% of its calls raises ToolErrors after 10 minutes, which resolves
// when the failures stop.
func TestToolErrors(t *testing.T) {
	e, w := newTestEvaluator()
	healthy := calls{"tools/call", "healthy", "ok", 5, 0, time.Millisecond}
	list := calls{"tools/list", "none", "ok", 5, 0, time.Millisecond}
	// 10 calls a minute, 3 failing: 30%. The tool's 12 calls take 2 minutes.
	failing := calls{"tools/call", "lookup", "tool_error", 10, 3, time.Millisecond}
	seen := minutes(e, w, 0, 13, healthy, list, failing)
	if got := firstActive(seen); got != 11 {
		t.Fatalf("ToolErrors first listed at minute %d, want 11 (12 calls at minute 1, then 10 minutes): %q", got, seen)
	}
	alerts := assertAlerts(t, e, "ToolErrors/team-a.search/lookup/active")
	if alerts[0].Summary != "30% of 100 calls to tool lookup on team-a.search ended in tool_error or rpc_error in the last 10 minutes." {
		t.Errorf("summary = %q", alerts[0].Summary)
	}
	if alerts[0].RunbookURL != "https://runbooks.test/KavryntMCPToolErrorsHigh.md" {
		t.Errorf("runbook = %q", alerts[0].RunbookURL)
	}
	minutes(e, w, 13, 30, healthy, calls{"tools/call", "lookup", "ok", 10, 0, time.Millisecond})
	assertAlerts(t, e, "ToolErrors/team-a.search/lookup/resolved")
}

// Verifies SPEC-0020-R01, SPEC-0020-R02, and SPEC-0020-R08: SlowRoute fires
// after the route's p95 has been over latencyP95Seconds for 15 minutes, and
// resolves when calls are fast again.
func TestSlowRoute(t *testing.T) {
	e, w := newTestEvaluator()
	slow := calls{"tools/call", "lookup", "ok", 30, 0, 8 * time.Second}
	seen := minutes(e, w, 0, 17, slow)
	if got := firstActive(seen); got != 15 {
		t.Fatalf("SlowRoute first listed at minute %d, want 15: %q", got, seen)
	}
	alerts := assertAlerts(t, e, "SlowRoute/team-a.search//active")
	if alerts[0].RunbookURL != "https://runbooks.test/KavryntMCPLatencyHigh.md" {
		t.Errorf("runbook = %q", alerts[0].RunbookURL)
	}
	minutes(e, w, 17, 40, calls{"tools/call", "lookup", "ok", 30, 0, 20 * time.Millisecond})
	assertAlerts(t, e, "SlowRoute/team-a.search//resolved")
}

// Verifies SPEC-0020-R05: alerts about a deleted MCPServer disappear, and
// resolved alerts are kept for an hour.
func TestDeletedServersAndOldResolvedAlertsDisappear(t *testing.T) {
	e, _ := newTestEvaluator()
	notReady := []model.Server{server(cond("Ready", "False", "EndpointRejected", t0), nil)}
	e.Evaluate(t0.Add(time.Hour), notReady)
	assertAlerts(t, e, "ServerNotReady/team-a.search//active")
	e.Evaluate(t0.Add(time.Hour+time.Minute), nil)
	assertAlerts(t, e)

	e.Evaluate(t0.Add(2*time.Hour), notReady)
	e.Evaluate(t0.Add(2*time.Hour+time.Minute), []model.Server{readyServer()})
	assertAlerts(t, e, "ServerNotReady/team-a.search//resolved")
	e.Evaluate(t0.Add(3*time.Hour+2*time.Minute), []model.Server{readyServer()})
	assertAlerts(t, e)
}

// Verifies SPEC-0020-R05 and SPEC-0020-R06: active alerts come first,
// oldest first, then resolved ones, newest first.
func TestListOrder(t *testing.T) {
	e, _ := newTestEvaluator()
	ready := cond("Ready", "True", "Accepted", t0.Add(-2*time.Hour))
	a := model.Server{Namespace: "team-a", Name: "a", Route: "team-a.a", Conditions: []model.Condition{*cond("Ready", "False", "EndpointRejected", t0.Add(-time.Hour))}}
	b := model.Server{Namespace: "team-a", Name: "b", Route: "team-a.b", Conditions: []model.Condition{*ready, *cond("ToolsDiscovered", "False", "DiscoveryFailed", t0.Add(-90*time.Minute))}}
	c := model.Server{Namespace: "team-a", Name: "c", Route: "team-a.c", Conditions: []model.Condition{*cond("Ready", "False", "EndpointRejected", t0.Add(-time.Hour))}}
	d := model.Server{Namespace: "team-a", Name: "d", Route: "team-a.d", Conditions: []model.Condition{*cond("Ready", "False", "EndpointRejected", t0.Add(-time.Hour))}}
	e.Evaluate(t0, []model.Server{a, b, c, d})
	c.Conditions = []model.Condition{*ready}
	e.Evaluate(t0.Add(time.Minute), []model.Server{a, b, c, d})
	d.Conditions = []model.Condition{*ready}
	e.Evaluate(t0.Add(2*time.Minute), []model.Server{a, b, c, d})
	assertAlerts(t, e,
		"DiscoveryFailing/team-a.b//active",
		"ServerNotReady/team-a.a//active",
		"ServerNotReady/team-a.d//resolved",
		"ServerNotReady/team-a.c//resolved")
}

// Verifies SPEC-0020-R01: p95 follows histogram_quantile.
func TestQuantile(t *testing.T) {
	bounds := []float64{1, 2, 5}
	if got := quantile(0.95, bounds, []uint64{0, 0, 0, 0}); !math.IsNaN(got) {
		t.Errorf("no calls: %v", got)
	}
	// 10 calls in (1, 2]: rank 9.5 of 10 is 95% into the bucket.
	if got := quantile(0.95, bounds, []uint64{0, 10, 0, 0}); math.Abs(got-1.95) > 1e-9 {
		t.Errorf("p95 = %v, want 1.95", got)
	}
	// Calls over the last bound report it, as Prometheus does.
	if got := quantile(0.95, bounds, []uint64{0, 0, 0, 10}); got != 5 {
		t.Errorf("p95 = %v, want 5", got)
	}
}

// Verifies SPEC-0020-R01: the window forgets calls older than 10 minutes.
func TestWindowForgetsOldCalls(t *testing.T) {
	w := NewWindow(buckets)
	w.Record(t0, "team-a.search", "tools/call", "lookup", "tool_error", time.Millisecond)
	w.Record(t0.Add(9*time.Minute), "team-a.search", "tools/call", "lookup", "ok", time.Millisecond)
	routes, tools := w.Snapshot(t0.Add(9*time.Minute + 59*time.Second))
	if routes["team-a.search"].Calls != 2 || tools[[2]string{"team-a.search", "lookup"}].Errors != 1 {
		t.Fatalf("routes = %+v, tools = %+v", routes, tools)
	}
	routes, tools = w.Snapshot(t0.Add(10 * time.Minute))
	if routes["team-a.search"].Calls != 1 || tools[[2]string{"team-a.search", "lookup"}].Errors != 0 {
		t.Fatalf("after 10 minutes: routes = %+v, tools = %+v", routes, tools)
	}
}
