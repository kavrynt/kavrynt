// Package dashboards_test checks the chart's Grafana dashboards and the
// metrics reference in the component docs against the metrics the runtime
// exports, found by parsing the Go sources under internal/.
package dashboards_test

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	root          = "../.."
	dashboardsDir = root + "/charts/kavrynt/charts/gateway/dashboards"
)

var (
	exportedName = regexp.MustCompile(`^kavrynt_(gateway|operator)_[a-z0-9_]+$`)
	queriedName  = regexp.MustCompile(`kavrynt_[a-z0-9_]+`)
	referenceRow = regexp.MustCompile("(?m)^\\| `(kavrynt_[a-z0-9_]+)` \\|")
)

// exportedMetrics returns every kavrynt_gateway_* and kavrynt_operator_*
// string literal in the non-test Go files under internal/.
func exportedMetrics(t *testing.T) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if value, err := strconv.Unquote(lit.Value); err == nil && exportedName.MatchString(value) {
					names[value] = true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("found no exported metrics")
	}
	return names
}

// queries returns the PromQL of every panel target and variable.
func queries(node any) []string {
	var out []string
	switch v := node.(type) {
	case map[string]any:
		for key, value := range v {
			if s, ok := value.(string); ok && (key == "expr" || key == "query" || key == "definition") {
				out = append(out, s)
				continue
			}
			out = append(out, queries(value)...)
		}
	case []any:
		for _, value := range v {
			out = append(out, queries(value)...)
		}
	}
	return out
}

// usesDatasource reports whether a datasource reference is the dashboard's
// datasource variable.
func usesDatasource(ref any) bool {
	m, ok := ref.(map[string]any)
	return ok && m["uid"] == "${datasource}"
}

// checkDashboard lists what is wrong with a dashboard: metrics the runtime
// does not export, and panels or targets that do not use the datasource
// variable.
func checkDashboard(dashboard map[string]any, exported map[string]bool) []string {
	var problems []string
	unknown := map[string]bool{}
	for _, query := range queries(dashboard) {
		for _, name := range queriedName.FindAllString(query, -1) {
			base := name
			for _, suffix := range []string{"_bucket", "_sum", "_count"} {
				if trimmed, ok := strings.CutSuffix(name, suffix); ok && exported[trimmed] {
					base = trimmed
				}
			}
			if !exported[base] {
				unknown[name] = true
			}
		}
	}
	for name := range unknown {
		problems = append(problems, "queries "+name+", which the runtime does not export")
	}
	hasVariable := false
	templating, _ := dashboard["templating"].(map[string]any)
	variables, _ := templating["list"].([]any)
	for _, v := range variables {
		if m, ok := v.(map[string]any); ok && m["name"] == "datasource" && m["type"] == "datasource" {
			hasVariable = true
		}
	}
	if !hasVariable {
		problems = append(problems, "has no datasource variable")
	}
	panels, _ := dashboard["panels"].([]any)
	for _, p := range panels {
		panel, _ := p.(map[string]any)
		if !usesDatasource(panel["datasource"]) {
			problems = append(problems, fmt.Sprintf("panel %q does not use ${datasource}", panel["title"]))
		}
		targets, _ := panel["targets"].([]any)
		for _, target := range targets {
			if m, _ := target.(map[string]any); !usesDatasource(m["datasource"]) {
				problems = append(problems, fmt.Sprintf("a target of panel %q does not use ${datasource}", panel["title"]))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

func readDashboard(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var dashboard map[string]any
	if err := json.Unmarshal(data, &dashboard); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return dashboard
}

// Verifies SPEC-0023-R03: every shipped dashboard queries only exported
// metrics through its datasource variable.
func TestShippedDashboardsUseExportedMetrics(t *testing.T) {
	exported := exportedMetrics(t)
	paths, err := filepath.Glob(filepath.Join(dashboardsDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 2 {
		t.Fatalf("found %d dashboards, want the traffic and server dashboards", len(paths))
	}
	for _, path := range paths {
		for _, problem := range checkDashboard(readDashboard(t, path), exported) {
			t.Errorf("%s %s", filepath.Base(path), problem)
		}
	}
}

// Verifies SPEC-0023-R03: a query for a metric that does not exist fails
// the check.
func TestCheckRejectsUnknownMetric(t *testing.T) {
	dashboard := map[string]any{
		"templating": map[string]any{"list": []any{map[string]any{"name": "datasource", "type": "datasource"}}},
		"panels": []any{map[string]any{
			"title":      "Broken",
			"datasource": map[string]any{"uid": "${datasource}"},
			"targets": []any{map[string]any{
				"datasource": map[string]any{"uid": "${datasource}"},
				"expr":       `sum(rate(kavrynt_gateway_unknown_metric[5m])) / sum(rate(kavrynt_gateway_mcp_request_duration_seconds_count[5m]))`,
			}},
		}},
	}
	problems := checkDashboard(dashboard, exportedMetrics(t))
	want := []string{"queries kavrynt_gateway_unknown_metric, which the runtime does not export"}
	if strings.Join(problems, "\n") != strings.Join(want, "\n") {
		t.Errorf("problems = %q, want %q", problems, want)
	}
}

// Verifies SPEC-0023-R03: a dashboard without the datasource variable fails
// the check.
func TestCheckRequiresDatasourceVariable(t *testing.T) {
	dashboard := map[string]any{"panels": []any{map[string]any{
		"title":      "Fixed",
		"datasource": map[string]any{"uid": "prometheus"},
	}}}
	problems := checkDashboard(dashboard, exportedMetrics(t))
	if len(problems) != 2 {
		t.Errorf("problems = %q, want a missing variable and a fixed datasource", problems)
	}
}

// Verifies SPEC-0023-R01: the server dashboard picks one MCPServer and shows
// its readiness, discovery, traffic, errors, latency per tool, failing
// tools, and bytes.
func TestServerDashboardPanels(t *testing.T) {
	dashboard := readDashboard(t, filepath.Join(dashboardsDir, "mcp-server.json"))
	if dashboard["title"] != "Kavrynt MCP Server" {
		t.Errorf("title = %v", dashboard["title"])
	}
	variables := map[string]bool{}
	for _, v := range dashboard["templating"].(map[string]any)["list"].([]any) {
		variables[v.(map[string]any)["name"].(string)] = true
	}
	for _, name := range []string{"datasource", "namespace", "name"} {
		if !variables[name] {
			t.Errorf("no %s variable", name)
		}
	}
	want := map[string][]string{
		"Ready":                   {"kavrynt_operator_mcpserver_ready{namespace=\"$namespace\",name=\"$name\"}"},
		"Tools discovered":        {"kavrynt_operator_mcpserver_tools_discovered{namespace=\"$namespace\",name=\"$name\"}"},
		"Requests / s":            {"kavrynt_gateway_mcp_requests_total{route=\"$namespace.$name\"}"},
		"Error rate":              {"outcome!=\"ok\""},
		"Requests / s by outcome": {"sum by (outcome)"},
		"Latency by tool":         {"histogram_quantile(0.5, sum by (tool, le)", "histogram_quantile(0.95, sum by (tool, le)"},
		"Top failing tools":       {"topk(10, sum by (tool)", "outcome=~\"tool_error|rpc_error\""},
		"Bytes / s":               {"kavrynt_gateway_mcp_bytes_total{route=\"$namespace.$name\"}"},
	}
	for _, p := range dashboard["panels"].([]any) {
		panel := p.(map[string]any)
		title, _ := panel["title"].(string)
		expected, ok := want[title]
		if !ok {
			continue
		}
		delete(want, title)
		all := strings.Join(queries(panel), "\n")
		for _, fragment := range expected {
			if !strings.Contains(all, fragment) {
				t.Errorf("panel %q does not query %s", title, fragment)
			}
		}
		if strings.Contains(all, "kavrynt_gateway_") && !strings.Contains(all, `route="$namespace.$name"`) {
			t.Errorf("panel %q is not limited to the selected server", title)
		}
	}
	for title := range want {
		t.Errorf("no %q panel", title)
	}
}

// referenceSection returns the "## Metrics reference" section of a doc.
func referenceSection(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(data), "\n## Metrics reference\n")
	if !ok {
		t.Fatalf("%s has no Metrics reference section", path)
	}
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// Verifies SPEC-0023-R04: the Gateway and Operator docs list every exported
// metric, and only those, with example PromQL.
func TestMetricsReference(t *testing.T) {
	exported := exportedMetrics(t)
	listed := map[string]bool{}
	for _, component := range []string{"gateway", "operator"} {
		section := referenceSection(t, filepath.Join(root, "docs", "components", component, "README.md"))
		if !strings.Contains(section, "| Metric | Type | Labels and bounds |") {
			t.Errorf("%s reference has no Metric, Type, Labels table", component)
		}
		if !strings.Contains(section, "```promql") {
			t.Errorf("%s reference has no example PromQL", component)
		}
		for _, match := range referenceRow.FindAllStringSubmatch(section, -1) {
			name := match[1]
			if !strings.HasPrefix(name, "kavrynt_"+component+"_") {
				t.Errorf("%s reference lists %s", component, name)
			}
			listed[name] = true
		}
	}
	for name := range exported {
		if !listed[name] {
			t.Errorf("%s is exported but not in the metrics reference", name)
		}
	}
	for name := range listed {
		if !exported[name] {
			t.Errorf("the metrics reference lists %s, which is not exported", name)
		}
	}
}

// Verifies SPEC-0023-R05: the chart README shows how to add a dashboard
// exported from Grafana.
func TestChartREADMEShowsExtraDashboards(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(root, "charts", "kavrynt", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gateway.metrics.dashboard.extra", "--set-file", "Export"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("chart README does not mention %s", want)
		}
	}
}
