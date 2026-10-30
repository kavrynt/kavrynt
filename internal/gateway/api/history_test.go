package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/alerts"
	"github.com/kavrynt/kavrynt/internal/gateway/history"
	"github.com/kavrynt/kavrynt/internal/gateway/model"
)

type historyAnswer struct {
	Instance string `json:"instance"`
	Scope    struct {
		Route string `json:"route"`
		Tool  string `json:"tool"`
	} `json:"scope"`
	history.View
	Alerts     []alerts.Alert `json:"alerts"`
	GrafanaURL string         `json:"grafanaURL"`
}

func getHistory(t *testing.T, h *Handler, query string) historyAnswer {
	t.Helper()
	rr := get(h, "/v1/history"+query)
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("GET /v1/history%s = %d %s", query, rr.Code, rr.Body)
	}
	var view historyAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func pointCalls(points []history.Point) uint64 {
	var n uint64
	for _, p := range points {
		n += p.OK + p.ToolError + p.UpstreamError + p.Other
	}
	return n
}

// Verifies SPEC-0024-R04: /v1/history needs the console login like
// /v1/servers.
func TestHistoryNeedsASession(t *testing.T) {
	var upstream []*http.Request
	h := consoleAuthHandler(t, &upstream)
	if rr := get(h, "/v1/history"); rr.Code != http.StatusUnauthorized || !strings.Contains(rr.Body.String(), `"error":"login required"`) {
		t.Fatalf("GET /v1/history without session = %d %s, want 401", rr.Code, rr.Body)
	}
}

// Verifies SPEC-0024-R01, SPEC-0024-R02, and SPEC-0024-R04: proxied calls
// appear in /v1/history for all routes, their route, and their tool, with
// the bounded metric labels, totals, replica, start, and alerts, and
// without arguments or results.
func TestHistoryEndpoint(t *testing.T) {
	h := trafficHandler(t, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[{"type":"text","text":"secret-result"}]}}`), nil
	})
	h.SetInstance("kavrynt-gateway-abc")
	h.SetGrafanaURL("https://grafana.example.com")
	for i := 0; i < 3; i++ {
		post(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lookup","arguments":{"q":"secret-argument"}}}`,
			map[string]string{"Content-Type": "application/json", "Authorization": "Bearer secret-token"})
	}
	post(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"bad name!","arguments":{}}}`,
		map[string]string{"Content-Type": "application/json"})
	evaluator := h.EnableAlerts(alertConfig)
	evaluator.Evaluate(time.Now(), []model.Server{{Namespace: "team-a", Name: "demo", Route: "team-a.demo",
		Conditions: []model.Condition{{Type: "Ready", Status: "False", Reason: "EndpointRejected", LastTransitionTime: time.Now().Add(-time.Hour)}}}})

	all := getHistory(t, h, "")
	if all.Instance != "kavrynt-gateway-abc" || all.Range != "1h" || all.StepSeconds != 60 || all.End.Sub(all.Start) != time.Hour || all.GrafanaURL != "https://grafana.example.com" {
		t.Fatalf("all routes: instance %q range %q step %d span %s grafana %q", all.Instance, all.Range, all.StepSeconds, all.End.Sub(all.Start), all.GrafanaURL)
	}
	// The handler started just now: its points begin with its start.
	if !all.Since.After(all.Start) || all.Since.After(time.Now()) || len(all.Points) == 0 || all.Points[0].Time.After(all.Since) {
		t.Fatalf("since %s must be the handler's start, inside %s..%s, and begin the points", all.Since, all.Start, all.End)
	}
	latest := all.Points[len(all.Points)-1]
	if latest.ToolError != 4 || *latest.ErrorRate != 1 || latest.BytesIn == nil || latest.BytesOut == nil || *latest.BytesOut == 0 {
		t.Fatalf("latest point = %+v", latest)
	}
	if len(all.Routes) != 1 || all.Routes[0].Route != "team-a.demo" || all.Routes[0].Calls != 4 {
		t.Fatalf("routes = %+v", all.Routes)
	}
	// The tool label is the bounded metric label, so a malformed name is
	// "invalid" here as in the metrics.
	if len(all.Tools) != 2 || all.Tools[0].Tool != "invalid" || all.Tools[1].Tool != "lookup" || all.Tools[1].Calls != 3 {
		t.Fatalf("tools = %+v", all.Tools)
	}
	if len(all.Alerts) != 1 || all.Alerts[0].Name != alerts.ServerNotReady {
		t.Fatalf("alerts = %+v", all.Alerts)
	}

	route := getHistory(t, h, "?range=6h&route=team-a.demo")
	if route.Scope.Route != "team-a.demo" || route.StepSeconds != 300 || route.End.Sub(route.Start) != 6*time.Hour || pointCalls(route.Points) != 4 || len(route.Tools) != 2 || len(route.Routes) != 0 {
		t.Fatalf("route: %+v", route.View)
	}
	tool := getHistory(t, h, "?range=24h&route=team-a.demo&tool=lookup")
	if tool.Scope.Tool != "lookup" || tool.End.Sub(tool.Start) != 24*time.Hour || pointCalls(tool.Points) != 3 || len(tool.Tools) != 0 || len(tool.Alerts) != 1 {
		t.Fatalf("tool: %+v alerts %+v", tool.View, tool.Alerts)
	}
	if p := tool.Points[len(tool.Points)-1]; p.BytesIn != nil {
		t.Fatalf("tool points carry bytes: %+v", p)
	}

	body := get(h, "/v1/history?route=team-a.demo&tool=lookup").Body.String() + get(h, "/v1/history").Body.String()
	for _, secret := range []string{"secret-argument", "secret-result", "secret-token", "Bearer"} {
		if strings.Contains(body, secret) {
			t.Fatalf("/v1/history contains %s: %s", secret, body)
		}
	}
}

// Verifies SPEC-0024-R02: the history of a route the table no longer has
// is dropped.
func TestHistoryDropsDeletedRoutes(t *testing.T) {
	h := trafficHandler(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, `{}`), nil })
	hist := h.EnableHistory(10)
	post(h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"Content-Type": "application/json"})
	if got := len(getHistory(t, h, "").Routes); got != 1 {
		t.Fatalf("%d routes before the server is deleted", got)
	}
	hist.Retain(map[string]bool{})
	if got := getHistory(t, h, ""); len(got.Routes) != 0 || pointCalls(got.Points) != 0 {
		t.Fatalf("after deletion: routes %+v, %d calls", got.Routes, pointCalls(got.Points))
	}
}

// Verifies SPEC-0024-R05: unknown ranges, routes that are not routed, a
// tool without a route, and malformed tools are refused with 400.
func TestHistoryRefusesBadQueries(t *testing.T) {
	h := trafficHandler(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, `{}`), nil })
	for query, message := range map[string]string{
		"?range=7d":                          "range must be 1h, 6h, or 24h",
		"?range=1H":                          "range must be 1h, 6h, or 24h",
		"?route=team-b.missing":              "route is not routed",
		"?tool=lookup":                       "tool needs a route",
		"?route=team-a.demo&tool=bad%20name": "tool is not a valid tool name",
		"?route=team-a.demo&tool=" + strings.Repeat("x", 200): "tool is not a valid tool name",
	} {
		rr := get(h, "/v1/history"+query)
		if rr.Code != http.StatusBadRequest || strings.TrimSpace(rr.Body.String()) != `{"error":"`+message+`"}` {
			t.Errorf("GET /v1/history%s = %d %s, want 400 %q", query, rr.Code, rr.Body, message)
		}
	}
}
