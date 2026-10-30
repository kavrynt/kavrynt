// Package alerts evaluates the MCP health conditions the console shows
// (SPEC-0020): servers that are not Ready, tool discovery that fails, and
// routes and tools whose recent calls fail or are slow. The conditions and
// thresholds match the chart's Prometheus alerts (SPEC-0010, SPEC-0021), so
// the two agree on what is a problem; teams without Prometheus still see
// them.
//
// Status conditions come from the MCPServer conditions every replica
// watches, so every replica reports them alike. Traffic conditions come from
// this replica's own calls.
package alerts

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/model"
)

// Condition names.
const (
	ServerNotReady   = "ServerNotReady"
	DiscoveryFailing = "DiscoveryFailing"
	UpstreamErrors   = "UpstreamErrors"
	ToolErrors       = "ToolErrors"
	SlowRoute        = "SlowRoute"
)

// How long the traffic conditions must hold, as the `for` of their
// Prometheus alerts (SPEC-0020-R02).
const (
	upstreamErrorsFor = 5 * time.Minute
	toolErrorsFor     = 10 * time.Minute
	slowRouteFor      = 15 * time.Minute
)

// EvaluationInterval is how often Run evaluates (SPEC-0020-R01).
const EvaluationInterval = 15 * time.Second

// ResolvedRetention is how long List shows resolved alerts. The evaluator
// keeps them for HistoryRetention, at most maxResolved, so the console
// dashboards can mark them over their longest range (SPEC-0024-R09).
const (
	ResolvedRetention = time.Hour
	HistoryRetention  = 24 * time.Hour
	maxResolved       = 500
)

// runbooks names the Prometheus alert, and runbook, of each condition.
var runbooks = map[string]string{
	ServerNotReady:   "KavryntMCPServerNotReady",
	DiscoveryFailing: "KavryntMCPToolDiscoveryFailing",
	UpstreamErrors:   "KavryntMCPUpstreamErrorsHigh",
	ToolErrors:       "KavryntMCPToolErrorsHigh",
	SlowRoute:        "KavryntMCPLatencyHigh",
}

// Names lists every condition.
func Names() []string {
	names := make([]string, 0, len(runbooks))
	for name := range runbooks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Severities, ordered by severityRank.
const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
)

var severityRank = map[string]int{SeverityCritical: 0, SeverityWarning: 1}

// Scope is what an alert is about: a route and, for tool alerts, a tool.
type Scope struct {
	Route string `json:"route"`
	Tool  string `json:"tool,omitempty"`
}

// Alert is an active or recently resolved condition. It carries route and
// tool names and counts only, never arguments, results, or credentials
// (SPEC-0020-R05).
type Alert struct {
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Scope    Scope  `json:"scope"`
	Summary  string `json:"summary"`
	// Since is when the condition began to hold; it is listed once it has
	// held for its duration.
	Since      time.Time  `json:"since"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
	RunbookURL string     `json:"runbookURL"`
}

type key struct {
	name  string
	scope Scope
}

// candidate is a condition that holds now.
type candidate struct {
	since   time.Time
	hold    time.Duration
	summary string
}

// Evaluator tracks the conditions over time. It is safe for concurrent use.
type Evaluator struct {
	window *Window
	config func() Config

	mu        sync.Mutex
	pending   map[key]time.Time
	active    map[key]*Alert
	resolved  []Alert
	evaluated time.Time
}

// NewEvaluator evaluates the conditions over window with the thresholds
// config returns at each evaluation.
func NewEvaluator(window *Window, config func() Config) *Evaluator {
	return &Evaluator{window: window, config: config, pending: map[key]time.Time{}, active: map[key]*Alert{}}
}

// Run evaluates every interval until ctx ends.
func (e *Evaluator) Run(ctx context.Context, interval time.Duration, servers func() []model.Server) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	e.Evaluate(time.Now(), servers())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			e.Evaluate(now, servers())
		}
	}
}

// Evaluate checks every condition at now against servers and the window.
func (e *Evaluator) Evaluate(now time.Time, servers []model.Server) {
	cfg := e.config()
	holding := map[key]candidate{}
	routes := map[string]bool{}
	for _, server := range servers {
		routes[server.Route] = true
		statusConditions(server, cfg, holding)
	}
	routeStats, toolStats := e.window.Snapshot(now)
	for route, stats := range routeStats {
		if !routes[route] {
			continue
		}
		if stats.Calls >= minCalls(cfg.MinRequestsPerSecond) && stats.Calls > 0 {
			if ratio := float64(stats.Failures) / float64(stats.Calls); ratio > cfg.ErrorRatio {
				holding[key{UpstreamErrors, Scope{Route: route}}] = candidate{hold: upstreamErrorsFor,
					summary: fmt.Sprintf("%s of %d calls to %s failed upstream (upstream_error, upstream_unreachable, or timeout) in the last 10 minutes.", percent(ratio), stats.Calls, route)}
			}
			if !math.IsNaN(stats.P95) && stats.P95 > cfg.LatencyP95Seconds {
				holding[key{SlowRoute, Scope{Route: route}}] = candidate{hold: slowRouteFor,
					summary: fmt.Sprintf("The p95 latency of calls to %s was %s in the last 10 minutes, over %s.", route, seconds(stats.P95), seconds(cfg.LatencyP95Seconds))}
			}
		}
	}
	for id, stats := range toolStats {
		route, tool := id[0], id[1]
		if !routes[route] || stats.Calls == 0 || stats.Calls < minCalls(cfg.MinToolRequestsPerSecond) {
			continue
		}
		if ratio := float64(stats.Errors) / float64(stats.Calls); ratio > cfg.ToolErrorRatio {
			holding[key{ToolErrors, Scope{Route: route, Tool: tool}}] = candidate{hold: toolErrorsFor,
				summary: fmt.Sprintf("%s of %d calls to tool %s on %s ended in tool_error or rpc_error in the last 10 minutes.", percent(ratio), stats.Calls, tool, route)}
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.evaluated = now
	for k, c := range holding {
		since := c.since
		if since.IsZero() {
			// A traffic condition starts when this replica first sees it.
			if started, ok := e.pending[k]; ok {
				since = started
			} else if alert, ok := e.active[k]; ok {
				since = alert.Since
			} else {
				since = now
			}
		}
		if alert, ok := e.active[k]; ok {
			alert.Summary = c.summary
			continue
		}
		if now.Sub(since) < c.hold {
			e.pending[k] = since
			continue
		}
		delete(e.pending, k)
		e.active[k] = &Alert{
			Name: k.name, Severity: SeverityWarning, Scope: k.scope, Summary: c.summary, Since: since,
			RunbookURL: cfg.RunbookBaseURL + "/" + runbooks[k.name] + ".md",
		}
	}
	for k := range e.pending {
		if _, ok := holding[k]; !ok {
			delete(e.pending, k)
		}
	}
	for k, alert := range e.active {
		if _, ok := holding[k]; ok {
			continue
		}
		delete(e.active, k)
		// Alerts about a deleted MCPServer disappear (SPEC-0020-R09).
		if !routes[k.scope.Route] {
			continue
		}
		resolvedAt := now
		alert.ResolvedAt = &resolvedAt
		e.resolved = append(e.resolved, *alert)
	}
	kept := e.resolved[:0]
	for _, alert := range e.resolved {
		if now.Sub(*alert.ResolvedAt) <= HistoryRetention && routes[alert.Scope.Route] {
			kept = append(kept, alert)
		}
	}
	if len(kept) > maxResolved {
		kept = kept[len(kept)-maxResolved:]
	}
	e.resolved = kept
}

// statusConditions adds the conditions of one MCPServer's status.
func statusConditions(server model.Server, cfg Config, holding map[key]candidate) {
	scope := Scope{Route: server.Route}
	ready := findCondition(server, "Ready")
	if ready == nil || ready.Status != "True" {
		since, reason := server.CreatedAt, "no Ready condition yet"
		if ready != nil {
			since, reason = ready.LastTransitionTime, ready.Reason
		}
		holding[key{ServerNotReady, scope}] = candidate{since: since, hold: time.Duration(cfg.NotReadyFor),
			summary: fmt.Sprintf("MCPServer %s/%s is not Ready (%s); the Gateway does not route it.", server.Namespace, server.Name, reason)}
		return
	}
	discovered := findCondition(server, "ToolsDiscovered")
	if discovered != nil && discovered.Status == "False" &&
		(discovered.Reason == "DiscoveryFailed" || discovered.Reason == "CredentialUnavailable") {
		holding[key{DiscoveryFailing, scope}] = candidate{since: discovered.LastTransitionTime, hold: time.Duration(cfg.DiscoveryFailingFor),
			summary: fmt.Sprintf("The Operator cannot list the tools of MCPServer %s/%s (%s); its tool list is stale or empty.", server.Namespace, server.Name, discovered.Reason)}
	}
}

func findCondition(server model.Server, conditionType string) *model.Condition {
	for i := range server.Conditions {
		if server.Conditions[i].Type == conditionType {
			return &server.Conditions[i]
		}
	}
	return nil
}

// List returns the active alerts, critical before warning and then oldest
// first, followed by those resolved in the ResolvedRetention before the last
// evaluation, newest first, and when they were last evaluated.
func (e *Evaluator) List() ([]Alert, time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	active := make([]Alert, 0, len(e.active))
	for _, alert := range e.active {
		active = append(active, *alert)
	}
	sort.Slice(active, func(i, j int) bool {
		a, b := active[i], active[j]
		if severityRank[a.Severity] != severityRank[b.Severity] {
			return severityRank[a.Severity] < severityRank[b.Severity]
		}
		if !a.Since.Equal(b.Since) {
			return a.Since.Before(b.Since)
		}
		return a.Name+a.Scope.Route+a.Scope.Tool < b.Name+b.Scope.Route+b.Scope.Tool
	})
	for i := len(e.resolved) - 1; i >= 0; i-- {
		if e.evaluated.Sub(*e.resolved[i].ResolvedAt) <= ResolvedRetention {
			active = append(active, e.resolved[i])
		}
	}
	return active, e.evaluated
}

// Within returns the alerts active at some time from from to to, oldest
// first, about a scope: every alert for route "", the route's alerts for a
// route, and the route's alerts without a tool or for tool for a tool
// (SPEC-0024-R09).
func (e *Evaluator) Within(from, to time.Time, route, tool string) []Alert {
	e.mu.Lock()
	defer e.mu.Unlock()
	matches := func(alert Alert) bool {
		if route != "" && alert.Scope.Route != route {
			return false
		}
		if tool != "" && alert.Scope.Tool != "" && alert.Scope.Tool != tool {
			return false
		}
		return !alert.Since.After(to) && (alert.ResolvedAt == nil || !alert.ResolvedAt.Before(from))
	}
	list := []Alert{}
	for _, alert := range e.active {
		if matches(*alert) {
			list = append(list, *alert)
		}
	}
	for _, alert := range e.resolved {
		if matches(alert) {
			list = append(list, alert)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if !list[i].Since.Equal(list[j].Since) {
			return list[i].Since.Before(list[j].Since)
		}
		return list[i].Name+list[i].Scope.Route+list[i].Scope.Tool < list[j].Name+list[j].Scope.Route+list[j].Scope.Tool
	})
	return list
}

func percent(ratio float64) string {
	return fmt.Sprintf("%.0f%%", ratio*100)
}

func seconds(s float64) string {
	return (time.Duration(s * float64(time.Second))).Round(time.Millisecond).String()
}
