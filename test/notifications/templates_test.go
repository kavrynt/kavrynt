// Package notifications_test checks the chart's Alertmanager message
// templates (charts/kavrynt/notifications) against alerts shaped like the
// chart's. scripts/test-notifications.sh parses the same templates with
// amtool, so they also use only functions Alertmanager has.
package notifications_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"text/template"
)

// kv, alert, alerts, and data mirror the parts of Alertmanager's
// template.Data that the templates use.
type kv map[string]string

func (m kv) String() string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+m[key])
	}
	return strings.Join(pairs, ", ")
}

type alert struct {
	Status      string
	Labels      kv
	Annotations kv
}

type alerts []alert

func (as alerts) Firing() alerts {
	var firing alerts
	for _, a := range as {
		if a.Status == "firing" {
			firing = append(firing, a)
		}
	}
	return firing
}

type data struct {
	Status      string
	Alerts      alerts
	GroupLabels kv
}

func render(t *testing.T, name string, d data) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "charts", "kavrynt", "notifications", name))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := template.New(name).Funcs(template.FuncMap{"toUpper": strings.ToUpper}).Parse(string(source))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := tmpl.Execute(&out, d); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func serverNotReady(status string) alert {
	return alert{
		Status: status,
		Labels: kv{
			"alertname": "KavryntMCPServerNotReady", "severity": "warning", "component": "mcp-server",
			"kavrynt_release": "kavrynt", "namespace": "team-a", "name": "search",
		},
		Annotations: kv{
			"summary":     "MCPServer team-a/search is not Ready.",
			"description": "The Operator has reported team-a/search as not Ready for 10m; the Gateway does not route it.",
			"runbook_url": "https://runbooks.test/KavryntMCPServerNotReady.md",
		},
	}
}

// Verifies SPEC-0022-R04: a firing server alert names the alert, its
// severity and state, the server, the summary and description, and the
// runbook.
func TestMessageNamesServerAndRunbook(t *testing.T) {
	d := data{
		Status:      "firing",
		Alerts:      alerts{serverNotReady("firing")},
		GroupLabels: kv{"alertname": "KavryntMCPServerNotReady", "namespace": "team-a", "name": "search"},
	}
	if got, want := render(t, "title.tmpl", d), "[FIRING:1] KavryntMCPServerNotReady on team-a/search"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
	text := render(t, "text.tmpl", d)
	for _, want := range []string{
		"KavryntMCPServerNotReady (warning, firing)",
		"Server: team-a/search",
		"MCPServer team-a/search is not Ready.",
		"for 10m; the Gateway does not route it.",
		"Runbook: https://runbooks.test/KavryntMCPServerNotReady.md",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Tool:") {
		t.Errorf("text names a tool for a server alert:\n%s", text)
	}
}

// Verifies SPEC-0022-R04: a tool alert names the route and the tool.
func TestMessageNamesTool(t *testing.T) {
	toolErrors := alert{
		Status: "firing",
		Labels: kv{
			"alertname": "KavryntMCPToolErrorsHigh", "severity": "warning", "component": "mcp-server",
			"kavrynt_release": "kavrynt", "route": "team-a.search", "tool": "lookup",
		},
		Annotations: kv{
			"summary":     "Tool lookup on team-a.search keeps failing.",
			"description": "30% of calls to tool lookup on team-a.search ended in tool_error or rpc_error over 10 minutes.",
			"runbook_url": "https://runbooks.test/KavryntMCPToolErrorsHigh.md",
		},
	}
	d := data{
		Status:      "firing",
		Alerts:      alerts{toolErrors},
		GroupLabels: kv{"alertname": "KavryntMCPToolErrorsHigh", "route": "team-a.search"},
	}
	if got, want := render(t, "title.tmpl", d), "[FIRING:1] KavryntMCPToolErrorsHigh on team-a.search"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
	text := render(t, "text.tmpl", d)
	for _, want := range []string{"Server: team-a.search", "Tool: lookup", "Runbook: https://runbooks.test/KavryntMCPToolErrorsHigh.md"} {
		if !strings.Contains(text, want) {
			t.Errorf("text does not contain %q:\n%s", want, text)
		}
	}
}

// Verifies SPEC-0022-R04: a resolved alert says so in the title and in the
// alert's line.
func TestMessageSaysResolved(t *testing.T) {
	d := data{
		Status:      "resolved",
		Alerts:      alerts{serverNotReady("resolved")},
		GroupLabels: kv{"alertname": "KavryntMCPServerNotReady", "namespace": "team-a", "name": "search"},
	}
	if got, want := render(t, "title.tmpl", d), "[RESOLVED] KavryntMCPServerNotReady on team-a/search"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
	if text := render(t, "text.tmpl", d); !strings.Contains(text, "KavryntMCPServerNotReady (warning, resolved)") {
		t.Errorf("text does not say resolved:\n%s", text)
	}
}
