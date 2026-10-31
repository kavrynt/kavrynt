package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	auditv1 "github.com/kavrynt/kavrynt/api/audit/v1"
)

// audiencePolicy allows a call only when the policy input names the
// audience the token was accepted for.
const audiencePolicy = `package kavrynt.authz

default decision := {"allow": false, "reason": "audience not allowed"}

decision := {"allow": true} if input.audience == input.subject
`

// Verifies SPEC-0031-R02 and SPEC-0031-R04: a token for the Gateway audience
// reaches a listed route with audience "gateway" in the policy input, one
// for the route's own URI has audience "route", and unlisted routes refuse
// the Gateway audience before the server is reached.
func TestPolicySeesTheTokenAudience(t *testing.T) {
	handler, received, mint := gatewayAudienceHandler(t, true, []string{"team-a.*"})
	enablePolicy(t, handler, audiencePolicy)

	// The policy compares input.audience with the subject the token names.
	if rr := callRoute(handler, "/mcp/team-a.demo", mint(map[string]any{"aud": authResource, "sub": "gateway"})); rr.Code != http.StatusOK || len(*received) != 1 {
		t.Fatalf("gateway audience: %d %s, want it allowed with audience gateway", rr.Code, rr.Body.String())
	}
	if rr := callRoute(handler, "/mcp/team-a.demo", mint(map[string]any{"sub": "route"})); rr.Code != http.StatusOK || len(*received) != 2 {
		t.Fatalf("route audience: %d %s, want it allowed with audience route", rr.Code, rr.Body.String())
	}
	both := []string{authResource, authResource + "/mcp/team-a.demo"}
	if rr := callRoute(handler, "/mcp/team-a.demo", mint(map[string]any{"aud": both, "sub": "route"})); rr.Code != http.StatusOK || len(*received) != 3 {
		t.Fatalf("both audiences: %d %s, want audience route", rr.Code, rr.Body.String())
	}

	unlisted, unlistedReceived, mintUnlisted := gatewayAudienceHandler(t, true, []string{"team-b.*"})
	if rr := callRoute(unlisted, "/mcp/team-a.demo", mintUnlisted(map[string]any{"aud": authResource})); rr.Code != http.StatusUnauthorized || len(*unlistedReceived) != 0 {
		t.Fatalf("unlisted route: %d, want 401 before the server", rr.Code)
	}
}

// Verifies SPEC-0031-R05: audit events record the audience while the
// Gateway audience is enabled, and stay unchanged when it is off.
func TestAuditRecordsTheTokenAudience(t *testing.T) {
	handler, _, mint := gatewayAudienceHandler(t, true, []string{"team-a.*"})
	w, out := enableAudit(t, handler)
	callRoute(handler, "/mcp/team-a.demo", mint(map[string]any{"aud": authResource}))
	callRoute(handler, "/mcp/team-a.demo", mint(nil))
	callRoute(handler, "/mcp/team-a.demo", "")
	got := events(t, w, out)
	if len(got) != 3 {
		t.Fatalf("%d events, want 3", len(got))
	}
	for i, want := range []string{"gateway", "route", ""} {
		if got[i].Audience != want {
			t.Errorf("event %d: audience %q, want %q", i, got[i].Audience, want)
		}
	}
	if got[0].Decision != auditv1.DecisionAllow || got[2].Decision != auditv1.DecisionUnauthenticated {
		t.Fatalf("decisions = %q, %q", got[0].Decision, got[2].Decision)
	}
	if summary, err := auditv1.Verify(strings.NewReader(out.String())); err != nil || len(summary.Breaks) != 0 {
		t.Fatalf("chain with audiences: %+v, %v", summary, err)
	}

	off, _, mintOff := authHandler(t, true)
	w, out = enableAudit(t, off)
	callRoute(off, "/mcp/team-a.demo", mintOff(nil))
	got = events(t, w, out)
	if len(got) != 1 || strings.Contains(out.String(), `"audience"`) {
		t.Fatalf("Gateway audience off: %s, want no audience field", out.String())
	}
}

// Verifies SPEC-0031-R06: /v1/session reports the routes that accept the
// Gateway audience, for the console, and none when it is off.
func TestSessionReportsGatewayAudience(t *testing.T) {
	for _, tc := range []struct {
		routes []string
		want   []string
	}{
		{nil, []string{}},
		{[]string{"team-a.*", "team-c.reports"}, []string{"team-a.*", "team-c.reports"}},
	} {
		handler, _, _ := gatewayAudienceHandler(t, true, tc.routes)
		var body struct {
			GatewayAudience []string `json:"gatewayAudience"`
		}
		rr := get(handler, "/v1/session")
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body.GatewayAudience == nil || strings.Join(body.GatewayAudience, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("routes %v: /v1/session = %s", tc.routes, rr.Body.String())
		}
	}
}
