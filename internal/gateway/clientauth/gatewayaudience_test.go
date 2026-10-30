package clientauth

import (
	"net/http"
	"testing"
)

// Verifies SPEC-0031-R01: without listed routes, a token for the Gateway
// audience is refused as before (SPEC-0006-R09).
func TestGatewayAudienceIsOffByDefault(t *testing.T) {
	env := newEnv(t)
	if routes := env.auth.GatewayAudienceRoutes(); len(routes) != 0 {
		t.Fatalf("GatewayAudienceRoutes() = %v, want none", routes)
	}
	token := env.key.sign(t, env.claims(map[string]any{"aud": testResource}))
	_, refusal := env.auth.Authenticate(request(token), testRoute)
	wantRefusal(t, refusal, http.StatusUnauthorized, "invalid_token")
}

// Verifies SPEC-0031-R02, SPEC-0031-R03, and SPEC-0031-R04: the Gateway
// audience reaches listed routes only, the route's own URI takes precedence
// and ignores the list, and the identity says which audience was used.
func TestGatewayAudienceReachesListedRoutes(t *testing.T) {
	env := newEnv(t)
	env.auth.gatewayRoutes = []string{"team-a.*", "team-c.reports"}
	gateway := env.key.sign(t, env.claims(map[string]any{"aud": testResource}))
	billing := testResource + "/mcp/team-b.billing"
	both := env.key.sign(t, env.claims(map[string]any{"aud": []string{testResource, billing}}))

	cases := []struct {
		name     string
		token    string
		route    string
		audience string // empty: refused
	}{
		{"gateway audience, namespace pattern", gateway, "team-a.search", AudienceGateway},
		{"gateway audience, route name", gateway, "team-c.reports", AudienceGateway},
		{"gateway audience, unlisted namespace", gateway, "team-b.billing", ""},
		{"gateway audience, unlisted route of a listed one's namespace", gateway, "team-c.other", ""},
		{"gateway audience, namespace prefix only", gateway, "team-ab.search", ""},
		{"route audience, unlisted route", env.key.sign(t, env.claims(map[string]any{"aud": billing})), "team-b.billing", AudienceRoute},
		{"route audience, listed route", env.key.sign(t, env.claims(nil)), testRoute, AudienceRoute},
		{"both audiences, unlisted route", both, "team-b.billing", AudienceRoute},
		{"both audiences, listed route", env.key.sign(t, env.claims(map[string]any{"aud": []string{testResource, testResource + "/mcp/" + testRoute}})), testRoute, AudienceRoute},
		{"both audiences, route neither covers", both, "team-b.other", ""},
		{"Gateway audience with a trailing slash", env.key.sign(t, env.claims(map[string]any{"aud": testResource + "/"})), testRoute, ""},
		{"Gateway audience with a path", env.key.sign(t, env.claims(map[string]any{"aud": testResource + "/mcp"})), testRoute, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			identity, refusal := env.auth.Authenticate(request(tc.token), tc.route)
			if tc.audience == "" {
				if refusal == nil || refusal.Status != http.StatusUnauthorized {
					t.Fatalf("refusal = %+v, want 401", refusal)
				}
				return
			}
			if refusal != nil {
				t.Fatalf("refused: %+v", refusal)
			}
			if identity.Audience != tc.audience || identity.Subject != "user-42" {
				t.Fatalf("identity = %+v, want audience %q", identity, tc.audience)
			}
		})
	}
}

// Verifies SPEC-0031-R02: the Gateway audience passes every other check,
// such as the required scopes.
func TestGatewayAudienceKeepsOtherChecks(t *testing.T) {
	env := newEnv(t)
	env.auth.gatewayRoutes = []string{"team-a.*"}
	token := env.key.sign(t, env.claims(map[string]any{"aud": testResource, "scope": "other"}))
	_, refusal := env.auth.Authenticate(request(token), testRoute)
	wantRefusal(t, refusal, http.StatusForbidden, "insufficient_scope")

	expired := env.key.sign(t, env.claims(map[string]any{"aud": testResource, "exp": env.clock.Unix() - 600}))
	_, refusal = env.auth.Authenticate(request(expired), testRoute)
	wantRefusal(t, refusal, http.StatusUnauthorized, "invalid_token")
}

// Verifies SPEC-0031-R02: the Gateway refuses to start with "*" or anything
// but route names and namespace patterns in the list.
func TestGatewayAudienceRouteList(t *testing.T) {
	base := Config{ResourceURL: testResource, Issuers: []Issuer{{Issuer: testIssuer}}}
	for entry, ok := range map[string]bool{
		"team-a.search":    true,
		"team-a.*":         true,
		"team-a.v1.search": true,
		"*":                false,
		"*.search":         false,
		"team-a":           false,
		"team-a.":          false,
		"Team-A.search":    false,
		"team-a.sea*":      false,
		"":                 false,
	} {
		cfg := base
		cfg.GatewayAudienceRoutes = []string{entry}
		if err := cfg.Validate(); (err == nil) != ok {
			t.Errorf("route %q: Validate() = %v, want ok=%v", entry, err, ok)
		}
	}
}
