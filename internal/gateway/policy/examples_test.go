package policy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// examplePolicy loads one policy from examples/policy.
func examplePolicy(t *testing.T, name string) *Engine {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "policy", name))
	if err != nil {
		t.Fatal(err)
	}
	engine, _, _ := newEngine(t, map[string]string{name: string(data)})
	return engine
}

func authenticated(input Input, clientID string) Input {
	input.Subject, input.ClientID, input.Issuer = "user-42", clientID, "https://login.example.com"
	return input
}

// hinted sets the tool fields the Gateway takes from the route's last
// discovery.
func hinted(input Input, discovered bool, annotations map[string]bool) Input {
	input.ToolDiscovered, input.ToolAnnotations = discovered, annotations
	return input
}

// untrusted moves input to a server the example does not trust.
func untrusted(input Input) Input {
	input.Route = Route{Namespace: "team-b", Name: "scraper"}
	return input
}

// Verifies SPEC-0012-R11.
func TestExamplePolicies(t *testing.T) {
	ctx := context.Background()
	route := Route{Namespace: "team-a", Name: "search"}
	list := Input{Route: route, HTTPMethod: "POST", MCPMethod: "tools/list"}
	search := Input{Route: route, HTTPMethod: "POST", MCPMethod: "tools/call", Tool: "search"}
	deleteAll := Input{Route: route, HTTPMethod: "POST", MCPMethod: "tools/call", Tool: "delete_all"}
	stream := Input{Route: route, HTTPMethod: "GET"}

	cases := []struct {
		file  string
		input Input
		allow bool
	}{
		{"allow-authenticated.rego", authenticated(search, "any"), true},
		{"allow-authenticated.rego", search, false},

		{"client-allowlist.rego", authenticated(search, "support-agent"), true},
		{"client-allowlist.rego", authenticated(deleteAll, "support-agent"), false},
		{"client-allowlist.rego", authenticated(search, "etl"), false},
		{"client-allowlist.rego", authenticated(list, "etl"), true},
		{"client-allowlist.rego", list, false},
		{"client-allowlist.rego", search, false},

		{"read-only.rego", list, true},
		{"read-only.rego", Input{Route: route, HTTPMethod: "POST", MCPMethod: "notifications/initialized"}, true},
		{"read-only.rego", stream, true},
		{"read-only.rego", authenticated(search, "support-agent"), false},
		{"read-only.rego", Input{Route: route, HTTPMethod: "POST"}, false},

		// Verifies SPEC-0028-R09.
		{"read-only-tools.rego", list, true},
		{"read-only-tools.rego", stream, true},
		{"read-only-tools.rego", hinted(search, true, map[string]bool{"readOnlyHint": true}), true},
		{"read-only-tools.rego", hinted(deleteAll, true, map[string]bool{"destructiveHint": true}), false},
		{"read-only-tools.rego", hinted(deleteAll, true, map[string]bool{"readOnlyHint": false}), false},
		{"read-only-tools.rego", hinted(search, false, map[string]bool{}), false},
		{"read-only-tools.rego", hinted(search, true, map[string]bool{}), false},
		{"read-only-tools.rego", untrusted(hinted(search, true, map[string]bool{"readOnlyHint": true})), false},
		{"read-only-tools.rego", untrusted(list), true},
	}
	engines := map[string]*Engine{}
	for _, tc := range cases {
		engine := engines[tc.file]
		if engine == nil {
			engine = examplePolicy(t, tc.file)
			engines[tc.file] = engine
		}
		if d := engine.Decide(ctx, tc.input); d.Allow != tc.allow {
			t.Errorf("%s with %+v: allow = %v (%s), want %v", tc.file, tc.input, d.Allow, d.Reason, tc.allow)
		}
	}
}
