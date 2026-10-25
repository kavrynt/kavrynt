package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/alerts"
	"github.com/kavrynt/kavrynt/internal/gateway/model"
)

func alertConfig() alerts.Config {
	cfg := alerts.DefaultConfig("1.2.3")
	cfg.NotReadyFor = alerts.Duration(time.Minute)
	return cfg
}

// Verifies SPEC-0020-R05: /v1/alerts needs the console login like
// /v1/servers.
func TestAlertsNeedASession(t *testing.T) {
	var upstream []*http.Request
	h := consoleAuthHandler(t, &upstream)
	h.EnableAlerts(alertConfig)
	if rr := get(h, "/v1/alerts"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/alerts without session = %d, want 401", rr.Code)
	}
}

// Verifies SPEC-0020-R04 and SPEC-0020-R05: /v1/alerts lists the active
// alerts with their scope, summary, start, and runbook, and names the
// replica.
func TestAlertsEndpoint(t *testing.T) {
	h := trafficHandler(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, `{}`), nil })
	h.SetInstance("kavrynt-gateway-abc")

	rr := get(h, "/v1/alerts")
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `{"instance":"kavrynt-gateway-abc","alerts":[]}` {
		t.Fatalf("without the evaluator: %d %s", rr.Code, rr.Body)
	}

	evaluator := h.EnableAlerts(alertConfig)
	since := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	evaluator.Evaluate(time.Now(), []model.Server{{
		Namespace: "team-a", Name: "demo", Route: "team-a.demo",
		Conditions: []model.Condition{{Type: "Ready", Status: "False", Reason: "EndpointRejected", LastTransitionTime: since}},
	}})
	rr = get(h, "/v1/alerts")
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("GET /v1/alerts = %d %s", rr.Code, rr.Header())
	}
	var view struct {
		Instance    string         `json:"instance"`
		EvaluatedAt *time.Time     `json:"evaluatedAt"`
		Alerts      []alerts.Alert `json:"alerts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Instance != "kavrynt-gateway-abc" || view.EvaluatedAt == nil || len(view.Alerts) != 1 {
		t.Fatalf("view = %+v", view)
	}
	a := view.Alerts[0]
	if a.Name != alerts.ServerNotReady || a.Severity != "warning" || a.Scope.Route != "team-a.demo" || a.Scope.Tool != "" ||
		!a.Since.Equal(since) || a.ResolvedAt != nil ||
		a.RunbookURL != "https://github.com/kavrynt/kavrynt/blob/v1.2.3/docs/runbooks/KavryntMCPServerNotReady.md" ||
		!strings.Contains(a.Summary, "team-a/demo is not Ready") {
		t.Errorf("alert = %+v", a)
	}
}

// Verifies SPEC-0020-R04 and SPEC-0020-R05: the console alerts count this
// replica's proxied calls by their metric labels, and nothing of their
// arguments or results reaches /v1/alerts.
func TestProxiedCallsFeedTheAlertWindow(t *testing.T) {
	h := trafficHandler(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[{"type":"text","text":"secret-result"}]}}`), nil
	})
	for i := 0; i < 3; i++ {
		post(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lookup","arguments":{"q":"secret-argument"}}}`,
			map[string]string{"Content-Type": "application/json"})
	}
	routes, tools := h.alertWindow.Snapshot(time.Now())
	if routes["team-a.demo"].Calls != 3 || tools[[2]string{"team-a.demo", "lookup"}] != (alerts.ToolStats{Calls: 3, Errors: 3}) {
		t.Fatalf("routes = %+v, tools = %+v", routes, tools)
	}
	h.EnableAlerts(alertConfig).Evaluate(time.Now(), []model.Server{{Namespace: "team-a", Name: "demo", Route: "team-a.demo",
		Conditions: []model.Condition{{Type: "Ready", Status: "True"}}}})
	body := get(h, "/v1/alerts").Body.String()
	for _, secret := range []string{"secret-argument", "secret-result"} {
		if strings.Contains(body, secret) {
			t.Fatalf("/v1/alerts contains %s: %s", secret, body)
		}
	}
}
