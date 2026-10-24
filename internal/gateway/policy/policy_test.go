package policy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

const allowSearch = `package kavrynt.authz

default decision := {"allow": false, "reason": "not allowed"}

decision := {"allow": true} if input.tool == "search"
`

func writePolicy(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func newEngine(t *testing.T, files map[string]string) (*Engine, string, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	writePolicy(t, dir, files)
	logs := &bytes.Buffer{}
	engine, err := New(dir, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return engine, dir, logs
}

func call(tool string) Input {
	return Input{Route: Route{Namespace: "team-a", Name: "search"}, HTTPMethod: "POST", MCPMethod: "tools/call", Tool: tool, Time: "2026-10-24T12:00:00Z"}
}

// Verifies SPEC-0012-R02.
func TestPolicyDecidesFromTheMountedFiles(t *testing.T) {
	engine, _, _ := newEngine(t, map[string]string{"authz.rego": allowSearch, "README.md": "not a policy"})
	if d := engine.Decide(context.Background(), call("search")); !d.Allow {
		t.Fatalf("search denied: %+v", d)
	}
	if d := engine.Decide(context.Background(), call("delete_all")); d.Allow || d.Reason != "not allowed" {
		t.Fatalf("delete_all = %+v, want denied with the policy's reason", d)
	}
}

// Verifies SPEC-0012-R03.
func TestStartupRefusesAPolicyThatDoesNotCompile(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, map[string]string{"bad.rego": "package kavrynt.authz\n\ndecision := {\"allow\": true} if {\n  input.tool ==\n}\n"})
	_, err := New(dir, nil)
	if err == nil || !strings.Contains(err.Error(), "bad.rego:") {
		t.Fatalf("New = %v, want an error naming the file and line", err)
	}
	if _, err := New(t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "no .rego files") {
		t.Fatalf("empty directory: %v", err)
	}
}

// Verifies SPEC-0012-R04 and SPEC-0012-R05.
func TestReloadKeepsTheLastGoodPolicy(t *testing.T) {
	engine, dir, logs := newEngine(t, map[string]string{"authz.rego": allowSearch})
	registry := prometheus.NewRegistry()
	registry.MustRegister(engine.Collectors()...)
	first := engine.Revision()
	sum := sha256.New()
	sum.Write([]byte("authz.rego\x00" + strconv.Itoa(len(allowSearch)) + "\x00" + allowSearch))
	if first != hex.EncodeToString(sum.Sum(nil)) {
		t.Fatalf("revision %s is not the SHA-256 of the policy files", first)
	}
	if got := testutil.ToFloat64(engine.info.WithLabelValues(first)); got != 1 {
		t.Fatalf("policy_info{revision=first} = %v", got)
	}

	writePolicy(t, dir, map[string]string{"authz.rego": "package kavrynt.authz\n\ndecision := {"})
	if err := engine.Reload(context.Background()); err == nil {
		t.Fatal("an invalid policy was accepted")
	}
	if engine.Revision() != first || !engine.Decide(context.Background(), call("search")).Allow {
		t.Fatal("an invalid update replaced the policy in force")
	}
	if got := testutil.ToFloat64(engine.loadFailures); got != 1 {
		t.Fatalf("load failures = %v, want 1", got)
	}
	if !strings.Contains(logs.String(), "previous policy stays in force") {
		t.Fatalf("log = %s", logs.String())
	}

	writePolicy(t, dir, map[string]string{"authz.rego": strings.Replace(allowSearch, `"search"`, `"delete_all"`, 1)})
	if err := engine.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if engine.Revision() == first || !engine.Decide(context.Background(), call("delete_all")).Allow || engine.Decide(context.Background(), call("search")).Allow {
		t.Fatal("a valid update did not take effect")
	}
	if n := testutil.CollectAndCount(engine.info); n != 1 {
		t.Fatalf("policy_info has %d series, want only the active revision", n)
	}
}

// Verifies SPEC-0012-R07: everything but {"allow": true} denies.
func TestEverythingButAllowDenies(t *testing.T) {
	for name, policy := range map[string]string{
		"undefined":        "package kavrynt.authz\n\ndecision := {\"allow\": true} if input.tool == \"never\"\n",
		"not an object":    "package kavrynt.authz\n\ndecision := true\n",
		"allow not true":   "package kavrynt.authz\n\ndecision := {\"allow\": \"yes\"}\n",
		"evaluation error": "package kavrynt.authz\n\ndecision := {\"allow\": to_number(input.tool) > 0}\n",
		"conflict":         "package kavrynt.authz\n\ndecision := {\"allow\": true} if input.tool != \"\"\n\ndecision := {\"allow\": false} if input.tool != \"\"\n",
	} {
		engine, _, _ := newEngine(t, map[string]string{"authz.rego": policy})
		if d := engine.Decide(context.Background(), call("search")); d.Allow {
			t.Errorf("%s: allowed", name)
		}
	}
}

// Verifies SPEC-0012-R07: a slow policy is cut off and denies.
func TestSlowPolicyTimesOut(t *testing.T) {
	engine, _, _ := newEngine(t, map[string]string{"authz.rego": `package kavrynt.authz

decision := {"allow": true} if {
	r := numbers.range(1, 20000)
	some i, j
	r[i] > 0
	r[j] > 0
	i + j < 0
}
`})
	start := time.Now()
	d := engine.Decide(context.Background(), call("search"))
	if d.Allow || d.Reason != "policy evaluation timed out" {
		t.Fatalf("decision = %+v", d)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("evaluation took %s, want it cut off near %s", elapsed, EvalTimeout)
	}
}

// Verifies SPEC-0012-R08: the reason returned to clients is bounded and has
// no control characters.
func TestReasonIsSanitized(t *testing.T) {
	engine, _, _ := newEngine(t, map[string]string{"authz.rego": "package kavrynt.authz\n\ndecision := {\"allow\": false, \"reason\": \"bad\\r\\nInjected: header" + strings.Repeat("x", 500) + "\"}\n"})
	d := engine.Decide(context.Background(), call("search"))
	if strings.ContainsAny(d.Reason, "\r\n") || len([]rune(d.Reason)) > maxReasonLength || !strings.HasPrefix(d.Reason, "badInjected") {
		t.Fatalf("reason = %q", d.Reason)
	}
}

// Policies cannot reach the network or the Gateway's environment.
func TestNetworkBuiltinsAreUnavailable(t *testing.T) {
	for _, body := range []string{
		`http.send({"method": "GET", "url": "http://169.254.169.254/"}).status_code == 200`,
		`count(net.lookup_ip_addr("example.com")) > 0`,
		`opa.runtime().env.HOME != ""`,
	} {
		dir := t.TempDir()
		writePolicy(t, dir, map[string]string{"authz.rego": "package kavrynt.authz\n\ndecision := {\"allow\": true} if " + body + "\n"})
		if _, err := New(dir, nil); err == nil {
			t.Errorf("policy using %q compiled", body)
		}
	}
}
