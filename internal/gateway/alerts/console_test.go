package alerts

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Verifies SPEC-0020-R06: the console describes every condition the Gateway
// evaluates, and the Gateway docs name each one.
func TestConsoleAndDocsDescribeEveryCondition(t *testing.T) {
	stats, err := os.ReadFile("../console/static/stats.js")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)const ALERTS = \{(.*?)\n  \};`).FindSubmatch(stats)
	if block == nil {
		t.Fatal("stats.js has no ALERTS table")
	}
	var described []string
	for _, m := range regexp.MustCompile(`(?m)^    ([A-Za-z]+): \{\n      title: "[^"]+",\n      traffic: (true|false),\n      definition: "[^"]{20,}",\n    \},`).FindAllSubmatch(block[1], -1) {
		described = append(described, string(m[1]))
	}
	sort.Strings(described)
	if got, want := strings.Join(described, ","), strings.Join(Names(), ","); got != want {
		t.Fatalf("console describes %s, the Gateway evaluates %s", got, want)
	}
	doc, err := os.ReadFile("../../../docs/components/gateway/README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range Names() {
		if !strings.Contains(string(doc), "`"+name+"`") {
			t.Errorf("docs/components/gateway/README.md does not describe %s", name)
		}
	}
}
