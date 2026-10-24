package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/kavrynt/kavrynt/internal/gateway/clientauth"
	"github.com/kavrynt/kavrynt/internal/gateway/model"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
)

const (
	authIssuer   = "https://as.example.com"
	authResource = "https://mcp.example.com"
)

// authHandler returns a Gateway with client authentication on, routing
// team-a.demo to a fake upstream that records the requests it receives, and a
// function that mints tokens. keysUp controls whether the issuer's key set
// can be fetched.
func authHandler(t *testing.T, keysUp bool) (*Handler, *[]*http.Request, func(claims map[string]any) string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	set, _ := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "ES256", Use: "sig"}}})
	keyTransport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if !keysUp || req.URL.String() != authIssuer+"/jwks" {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody, Header: http.Header{}}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(set))}, nil
	})
	auth, err := clientauth.New(clientauth.Config{
		ResourceURL:    authResource,
		Issuers:        []clientauth.Issuer{{Issuer: authIssuer, JWKSURI: authIssuer + "/jwks"}},
		RequiredScopes: []string{"mcp:tools"},
	}, keyTransport, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	var received []*http.Request
	table := routing.NewTable()
	table.Replace([]model.Route{{Name: "team-a.demo", Version: "0.1.0", Transport: "http", Endpoint: "http://demo.test/mcp?key=secret"}}, time.Now())
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		received = append(received, req)
		return okResponse(req)
	})}
	handler := NewHandler(table, client, Metadata{})
	handler.EnableClientAuth(auth)

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	mint := func(overrides map[string]any) string {
		claims := map[string]any{
			"iss": authIssuer, "sub": "user-42", "client_id": "support-agent",
			"aud": authResource + "/mcp/team-a.demo", "exp": time.Now().Add(time.Minute).Unix(), "scope": "mcp:tools",
		}
		for k, v := range overrides {
			claims[k] = v
		}
		token, err := jwt.Signed(signer).Claims(claims).Serialize()
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	return handler, &received, mint
}

func callRoute(h *Handler, path, token string, edit ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"message":"secret-argument"}}}`))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for _, e := range edit {
		e(req)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// Verifies SPEC-0006-R01.
func TestRoutesStayOpenWithoutClientAuth(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	if rr := callRoute(handler, "/mcp/team-a.demo", ""); rr.Code != http.StatusOK || *calls != 1 {
		t.Fatalf("call without a token and without auth = %d, want it proxied", rr.Code)
	}
	if rr := get(handler, metadataPrefix+"team-a.demo"); rr.Code != http.StatusNotFound {
		t.Fatalf("metadata without auth = %d, want 404", rr.Code)
	}
}

// Verifies SPEC-0006-R03 and SPEC-0006-R05.
func TestRefusedCallsNeverReachTheServer(t *testing.T) {
	handler, received, mint := authHandler(t, true)
	for name, token := range map[string]string{
		"no token":       "",
		"invalid token":  "not-a-jwt",
		"other audience": mint(map[string]any{"aud": authResource + "/mcp/team-b.billing"}),
		"expired":        mint(map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}),
	} {
		rr := callRoute(handler, "/mcp/team-a.demo", token)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", name, rr.Code)
		}
		challenge := rr.Header().Get("WWW-Authenticate")
		if !strings.HasPrefix(challenge, "Bearer ") || !strings.Contains(challenge, `resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/mcp/team-a.demo"`) || !strings.Contains(challenge, `scope="mcp:tools"`) {
			t.Fatalf("%s: WWW-Authenticate = %q", name, challenge)
		}
		if token != "" && (!strings.Contains(challenge, `error="invalid_token"`) || !strings.Contains(rr.Body.String(), `"invalid token"`)) {
			t.Fatalf("%s: %q %s, want invalid_token without details", name, challenge, rr.Body.String())
		}
	}
	if len(*received) != 0 {
		t.Fatalf("%d refused calls reached the MCP server", len(*received))
	}
}

// Verifies SPEC-0006-R04.
func TestTokensOutsideTheHeaderAreRejected(t *testing.T) {
	handler, received, mint := authHandler(t, true)
	token := mint(nil)
	rr := callRoute(handler, "/mcp/team-a.demo?access_token="+token, "")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Header().Get("WWW-Authenticate"), `error="invalid_request"`) {
		t.Fatalf("query token: %d %q, want 400 invalid_request", rr.Code, rr.Header().Get("WWW-Authenticate"))
	}
	form := func(body string) func(*http.Request) {
		return func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if rr := callRoute(handler, "/mcp/team-a.demo", "", form("access_token="+token)); rr.Code != http.StatusBadRequest {
		t.Fatalf("form token: %d, want 400", rr.Code)
	}
	if len(*received) != 0 {
		t.Fatal("a call with a token outside the header reached the MCP server")
	}
	// A form body without a token is still forwarded whole.
	if rr := callRoute(handler, "/mcp/team-a.demo", token, form("a=1&b=2")); rr.Code != http.StatusOK {
		t.Fatalf("form without a token: %d", rr.Code)
	}
	body, _ := io.ReadAll((*received)[0].Body)
	if string(body) != "a=1&b=2" {
		t.Fatalf("forwarded body = %q", body)
	}
}

// Verifies SPEC-0006-R06.
func TestMissingScopeIsForbidden(t *testing.T) {
	handler, received, mint := authHandler(t, true)
	rr := callRoute(handler, "/mcp/team-a.demo", mint(map[string]any{"scope": "openid"}))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Header().Get("WWW-Authenticate"), `error="insufficient_scope", scope="mcp:tools"`) {
		t.Fatalf("status %d, WWW-Authenticate %q", rr.Code, rr.Header().Get("WWW-Authenticate"))
	}
	if len(*received) != 0 {
		t.Fatal("a forbidden call reached the MCP server")
	}
}

// Verifies SPEC-0006-R07.
func TestProtectedResourceMetadataEndpoint(t *testing.T) {
	handler, _, _ := authHandler(t, true)
	rr := get(handler, metadataPrefix+"team-a.demo")
	if rr.Code != http.StatusOK {
		t.Fatalf("metadata = %d", rr.Code)
	}
	var metadata clientauth.ProtectedResourceMetadata
	if err := json.Unmarshal(rr.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Resource != authResource+"/mcp/team-a.demo" || len(metadata.AuthorizationServers) != 1 ||
		metadata.AuthorizationServers[0] != authIssuer || metadata.BearerMethodsSupported[0] != "header" || metadata.ScopesSupported[0] != "mcp:tools" {
		t.Fatalf("metadata = %+v", metadata)
	}
	for _, path := range []string{metadataPrefix + "team-b.unknown", metadataPrefix + "team-a.demo/x"} {
		if rr := get(handler, path); rr.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", path, rr.Code)
		}
	}
}

// Verifies SPEC-0006-R08 and SPEC-0006-R13: a valid token is accepted and the
// MCP server never sees it.
func TestValidTokenIsProxiedWithoutPassthrough(t *testing.T) {
	handler, received, mint := authHandler(t, true)
	rr := callRoute(handler, "/mcp/team-a.demo", mint(nil))
	if rr.Code != http.StatusOK || len(*received) != 1 {
		t.Fatalf("valid token: %d after %d upstream calls", rr.Code, len(*received))
	}
	if got := (*received)[0].Header.Get("Authorization"); got != "" {
		t.Fatalf("MCP server received Authorization %q", got)
	}
}

// Verifies SPEC-0006-R08: callers without a valid token cannot tell
// registered routes from others; with one, an unknown route is 404.
func TestUnknownRoutesNeedATokenToo(t *testing.T) {
	handler, _, mint := authHandler(t, true)
	if rr := callRoute(handler, "/mcp/team-z.unknown", ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unknown route without a token = %d, want 401", rr.Code)
	}
	if rr := callRoute(handler, "/mcp/team-z.unknown", mint(map[string]any{"aud": authResource + "/mcp/team-z.unknown"})); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown route with a valid token = %d, want 404", rr.Code)
	}
}

// Verifies SPEC-0006-R12.
func TestUnavailableKeysAnswer503(t *testing.T) {
	handler, received, mint := authHandler(t, false)
	rr := callRoute(handler, "/mcp/team-a.demo", mint(nil))
	if rr.Code != http.StatusServiceUnavailable || rr.Header().Get("Retry-After") == "" || len(*received) != 0 {
		t.Fatalf("keys unavailable: %d, Retry-After %q, %d upstream calls", rr.Code, rr.Header().Get("Retry-After"), len(*received))
	}
	if rr := get(handler, "/readyz"); rr.Code != http.StatusOK {
		t.Fatalf("readyz = %d while an issuer is down, want 200", rr.Code)
	}
}

// Verifies SPEC-0006-R14.
func TestRefusalsAreRecordedWithoutTokens(t *testing.T) {
	handler, _, mint := authHandler(t, true)
	bad := mint(map[string]any{"aud": "https://elsewhere.example.com"})
	callRoute(handler, "/mcp/team-a.demo", "")
	callRoute(handler, "/mcp/team-a.demo", bad)
	callRoute(handler, "/mcp/team-a.demo", mint(map[string]any{"scope": "openid"}))
	callRoute(handler, "/mcp/team-z.unknown", "")

	metrics := get(handler, "/metrics").Body.String()
	requireSeries(t, metrics, `kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="unauthenticated",route="team-a.demo",tool="echo"} 2`)
	requireSeries(t, metrics, `kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="forbidden",route="team-a.demo",tool="echo"} 1`)
	if strings.Contains(metrics, "team-z.unknown") {
		t.Fatal("a refused call to an unregistered route created metric series")
	}
	traffic := get(handler, "/v1/traffic").Body.String()
	for _, secret := range append(strings.Split(bad, "."), "user-42", "support-agent", "secret-argument") {
		if strings.Contains(metrics, secret) || strings.Contains(traffic, secret) {
			t.Fatalf("metrics or traffic contain %q", secret)
		}
	}
}

// Verifies SPEC-0006-R15.
func TestSessionReportsMCPAuth(t *testing.T) {
	handler, _, _ := authHandler(t, true)
	if rr := get(handler, "/v1/session"); !strings.Contains(rr.Body.String(), `"mcpAuth":true`) {
		t.Fatalf("/v1/session = %s, want mcpAuth true", rr.Body.String())
	}
	open, _ := demoHandler(t, okResponse)
	if rr := get(open, "/v1/session"); !strings.Contains(rr.Body.String(), `"mcpAuth":false`) {
		t.Fatalf("/v1/session = %s, want mcpAuth false", rr.Body.String())
	}
}

// Verifies SPEC-0006-R18 and SPEC-0006-R19.
func TestRouteListingHidesEndpoints(t *testing.T) {
	open, _ := demoHandler(t, okResponse)
	open.table.Replace([]model.Route{{Name: "team-a.demo", Version: "0.1.0", Transport: "http", Endpoint: "http://user:pw@demo.test/mcp?key=secret#frag"}}, time.Now())
	body := get(open, "/v1/routes").Body.String()
	if !strings.Contains(body, `"endpoint":"http://demo.test/mcp"`) || strings.Contains(body, "secret") || strings.Contains(body, "pw") || strings.Contains(body, "frag") {
		t.Fatalf("/v1/routes without auth = %s, want the endpoint without credentials, query, or fragment", body)
	}

	handler, _, _ := authHandler(t, true)
	body = get(handler, "/v1/routes").Body.String()
	if strings.Contains(body, "endpoint") || !strings.Contains(body, `"name":"team-a.demo"`) {
		t.Fatalf("/v1/routes with auth = %s, want routes without endpoints", body)
	}
}

// Verifies SPEC-0006-R14: the outcomes clientauth reports are the ones the
// Gateway records and documents.
func TestRefusalOutcomesMatch(t *testing.T) {
	for _, outcome := range []string{clientauth.OutcomeUnauthenticated, clientauth.OutcomeForbidden, clientauth.OutcomeAuthUnavailable} {
		if refusalOutcome(outcome) != outcome {
			t.Fatalf("outcome %q is not a Gateway outcome", outcome)
		}
	}
	if refusalOutcome("made_up") != outcomeGatewayError {
		t.Fatal("an unknown outcome became a metric label")
	}
}
