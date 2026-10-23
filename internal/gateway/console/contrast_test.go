package console

import (
	"fmt"
	"io/fs"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// themes returns the console's colour tokens per theme, read from app.css:
// light is the :root block; dark overrides it with the [data-theme="dark"]
// block, which the prefers-color-scheme block must repeat exactly.
func themes(t *testing.T) map[string]map[string]string {
	t.Helper()
	css, err := fs.ReadFile(static, "static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	block := func(selector string) map[string]string {
		start := strings.Index(string(css), selector+" {")
		if start < 0 {
			t.Fatalf("app.css has no %s block", selector)
		}
		body := string(css[start+len(selector)+2:])
		body = body[:strings.Index(body, "}")]
		tokens := map[string]string{}
		for _, m := range regexp.MustCompile(`(--[a-z0-9-]+):\s*([^;]+);`).FindAllStringSubmatch(body, -1) {
			tokens[m[1]] = strings.TrimSpace(m[2])
		}
		return tokens
	}
	light := block(":root")
	darkOverrides := block(`:root[data-theme="dark"]`)
	if prefers := block(`:root:not([data-theme="light"])`); fmt.Sprint(prefers) != fmt.Sprint(darkOverrides) {
		t.Fatalf("the prefers-color-scheme dark tokens differ from [data-theme=\"dark\"]:\n%v\n%v", prefers, darkOverrides)
	}
	dark := map[string]string{}
	for k, v := range light {
		dark[k] = v
	}
	for k, v := range darkOverrides {
		dark[k] = v
	}
	// Colours the stylesheet sets outside the tokens.
	light["primary-text"], dark["primary-text"] = "#ffffff", "#042f2e"
	light["white"], dark["white"] = "#ffffff", "#ffffff"
	return map[string]map[string]string{"light": light, "dark": dark}
}

func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	if !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(hex) {
		t.Fatalf("colour %q is not #rrggbb; the contrast test reads only hex tokens", hex)
	}
	channel := func(i int) float64 {
		v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(0) + 0.7152*channel(1) + 0.0722*channel(2)
}

func contrast(t *testing.T, a, b string) float64 {
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Verifies SPEC-0002-R16.
func TestThemeContrastMeetsWCAGAA(t *testing.T) {
	// Text pairs need 4.5:1 (WCAG 2.2 1.4.3); focus indicators and state
	// borders need 3:1 against what they sit on (1.4.11).
	pairs := []struct {
		fg, bg string
		min    float64
		use    string
	}{
		{"--text", "--bg", 4.5, "page text"},
		{"--text", "--surface", 4.5, "panel text"},
		{"--text", "--surface-2", 4.5, "table headers, inputs"},
		{"--muted", "--bg", 4.5, "page descriptions, scope notes"},
		{"--muted", "--surface", 4.5, "secondary text in panels"},
		{"--muted", "--surface-2", 4.5, "neutral badges, disabled buttons"},
		{"--accent", "--bg", 4.5, "links on the page"},
		{"--accent", "--surface", 4.5, "links and active tabs in panels"},
		{"primary-text", "--accent", 4.5, "primary buttons"},
		{"--ok", "--ok-soft", 4.5, "ok badges"},
		{"--warn", "--warn-soft", 4.5, "warning badges and notice"},
		{"--bad", "--bad-soft", 4.5, "error badges"},
		{"--info", "--info-soft", 4.5, "info badges"},
		{"--bad", "--surface", 4.5, "error text"},
		{"--text", "--warn-soft", 4.5, "notice and attention text"},
		{"--text", "--bad-soft", 4.5, "failing-server text"},
		{"--text", "--accent-soft", 4.5, "selected playground tool"},
		{"--code-text", "--code-bg", 4.5, "code blocks"},
		{"--sidebar-text", "--sidebar", 4.5, "navigation"},
		{"white", "--sidebar-active", 4.5, "active navigation item"},
		{"--sidebar-muted", "--sidebar", 4.5, "Gateway version in the sidebar"},
		{"--accent", "--bg", 3, "focus outline"},
		{"--warn", "--surface", 3, "attention borders"},
		{"--bad", "--surface", 3, "failing borders"},
	}
	for name, tokens := range themes(t) {
		for _, p := range pairs {
			fg, bg := tokens[p.fg], tokens[p.bg]
			if fg == "" || bg == "" {
				t.Fatalf("%s theme: missing token %s or %s", name, p.fg, p.bg)
			}
			if ratio := contrast(t, fg, bg); ratio < p.min {
				t.Errorf("%s theme, %s: %s %s on %s %s is %.2f:1, want at least %.1f:1", name, p.use, p.fg, fg, p.bg, bg, ratio, p.min)
			}
		}
	}
}
