package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// gatewayOutcomes returns the values of the outcome* constants in
// traffic.go, so a new outcome cannot be added without a definition.
func gatewayOutcomes(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "traffic.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var outcomes []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value := spec.(*ast.ValueSpec)
			for i, name := range value.Names {
				if !strings.HasPrefix(name.Name, "outcome") || i >= len(value.Values) {
					continue
				}
				lit, ok := value.Values[i].(*ast.BasicLit)
				if !ok {
					t.Fatalf("%s is not a string literal", name.Name)
				}
				outcome, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				outcomes = append(outcomes, outcome)
			}
		}
	}
	if len(outcomes) == 0 {
		t.Fatal("found no outcome constants in traffic.go")
	}
	sort.Strings(outcomes)
	return outcomes
}

// Verifies SPEC-0002-R08.
func TestEveryOutcomeIsDefinedInConsoleAndDocs(t *testing.T) {
	stats, err := os.ReadFile("../console/static/stats.js")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)const OUTCOMES = \{(.*?)\n  \};`).FindSubmatch(stats)
	if block == nil {
		t.Fatal("stats.js has no OUTCOMES table")
	}
	var defined []string
	for _, m := range regexp.MustCompile(`(?m)^    ([a-z_]+): \{\n      cls: "[a-z]+",\n      definition: "[^"]{20,}",\n    \},`).FindAllSubmatch(block[1], -1) {
		defined = append(defined, string(m[1]))
	}
	sort.Strings(defined)

	outcomes := gatewayOutcomes(t)
	if strings.Join(defined, ",") != strings.Join(outcomes, ",") {
		t.Fatalf("console defines outcomes %v, the Gateway records %v", defined, outcomes)
	}

	doc, err := os.ReadFile("../../../docs/components/gateway/README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range outcomes {
		if !strings.Contains(string(doc), "`"+outcome+"`") {
			t.Errorf("docs/components/gateway/README.md does not define outcome %q", outcome)
		}
	}
}
