package history

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Verifies SPEC-0024-R11: the console's outcome classes are the Gateway's,
// in the same order, and every outcome the console knows is in the class
// the Gateway counts it in.
func TestConsoleClassesMatchTheGateway(t *testing.T) {
	charts, err := os.ReadFile("../console/static/charts.js")
	if err != nil {
		t.Fatal(err)
	}
	console := map[string]string{}
	var keys []string
	for _, m := range regexp.MustCompile(`\{ key: "(\w+)", label: "[^"]+", cls: "\w+", outcomes: \[([^\]]*)\] \}`).FindAllSubmatch(charts, -1) {
		keys = append(keys, string(m[1]))
		for _, outcome := range regexp.MustCompile(`"(\w+)"`).FindAllSubmatch(m[2], -1) {
			console[string(outcome[1])] = string(m[1])
		}
	}
	if got, want := strings.Join(keys, ","), strings.Join(ClassNames[:], ","); got != want {
		t.Fatalf("charts.js CLASSES are %s, the Gateway's are %s", got, want)
	}

	stats, err := os.ReadFile("../console/static/stats.js")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)const OUTCOMES = \{(.*?)\n  \};`).FindSubmatch(stats)
	if block == nil {
		t.Fatal("stats.js has no OUTCOMES table")
	}
	outcomes := regexp.MustCompile(`(?m)^    (\w+): \{`).FindAllSubmatch(block[1], -1)
	if len(outcomes) < 10 {
		t.Fatalf("found only %d outcomes in stats.js", len(outcomes))
	}
	for _, m := range outcomes {
		outcome := string(m[1])
		class, listed := console[outcome]
		if !listed {
			class = "other"
		}
		if want := ClassNames[ClassOf(outcome)]; class != want {
			t.Errorf("the console puts %s in %s, the Gateway in %s", outcome, class, want)
		}
	}
}
