package clientauth

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tokenRecorder answers the token endpoint with an ID token and records the
// request; everything else goes to the fake authorization server.
type tokenRecorder struct {
	as       *fakeAS
	endpoint string
	answer   string
	form     url.Values
	user     string
	password string
}

func (t *tokenRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.String() != t.endpoint {
		return t.as.RoundTrip(req)
	}
	body, _ := io.ReadAll(req.Body)
	t.form, _ = url.ParseQuery(string(body))
	t.user, t.password, _ = req.BasicAuth()
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(t.answer)), Request: req}, nil
}

func newConsoleEnv(t *testing.T, authorizationEndpoint string, allowHTTP bool) (*ConsoleProvider, *tokenRecorder, signingKey, time.Time) {
	t.Helper()
	issuer := "https://login.example.com/realms/agents"
	as := newFakeAS()
	key := newECKey(t, "id-key")
	as.set(issuer+"/.well-known/openid-configuration", map[string]any{
		"issuer": issuer, "jwks_uri": issuer + "/jwks", "token_endpoint": issuer + "/token",
		"authorization_endpoint": authorizationEndpoint,
	})
	as.set(issuer+"/jwks", jwks(key))
	recorder := &tokenRecorder{as: as, endpoint: issuer + "/token", answer: `{"access_token":"a","id_token":"the-id-token","refresh_token":"r"}`}
	secret := filepath.Join(t.TempDir(), "client-secret")
	if err := os.WriteFile(secret, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := NewConsoleProvider(ConsoleProviderConfig{Issuer: issuer, AllowHTTP: allowHTTP, ClientID: "kavrynt-console", SecretFile: secret}, recorder)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }
	return p, recorder, key, now
}

// Verifies SPEC-0032-R03: the authorization endpoint comes from the
// issuer's metadata and must be https, even when the issuer allows http.
func TestConsoleAuthorizationEndpoint(t *testing.T) {
	p, _, _, _ := newConsoleEnv(t, "https://login.example.com/realms/agents/auth", false)
	if got, err := p.AuthorizationEndpoint(context.Background()); err != nil || got != "https://login.example.com/realms/agents/auth" {
		t.Fatalf("endpoint: %q %v", got, err)
	}
	p, _, _, _ = newConsoleEnv(t, "http://keycloak.idp.svc:8080/realms/agents/auth", true)
	if _, err := p.AuthorizationEndpoint(context.Background()); err == nil {
		t.Fatal("an http authorization endpoint was accepted with allowHTTP")
	}
}

// Verifies SPEC-0032-R05 and SPEC-0032-R09: the code goes to the token
// endpoint with client_secret_basic, the redirect URI, and the PKCE
// verifier; only the ID token is kept.
func TestConsoleExchangeCode(t *testing.T) {
	p, recorder, _, _ := newConsoleEnv(t, "https://login.example.com/auth", false)
	token, err := p.ExchangeCode(context.Background(), "the-code", "the-verifier", "https://mcp.example.com/v1/session/oidc/callback")
	if err != nil || token != "the-id-token" {
		t.Fatalf("exchange: %q %v", token, err)
	}
	want := url.Values{"grant_type": {"authorization_code"}, "code": {"the-code"}, "code_verifier": {"the-verifier"},
		"redirect_uri": {"https://mcp.example.com/v1/session/oidc/callback"}}
	for key := range want {
		if recorder.form.Get(key) != want.Get(key) {
			t.Errorf("form %s = %q, want %q", key, recorder.form.Get(key), want.Get(key))
		}
	}
	if recorder.user != "kavrynt-console" || recorder.password != "s3cret" || recorder.form.Has("client_secret") {
		t.Fatalf("client authentication: %q %q %v", recorder.user, recorder.password, recorder.form)
	}
	recorder.answer = `{"access_token":"a"}`
	if _, err := p.ExchangeCode(context.Background(), "c", "v", "r"); err == nil {
		t.Fatal("an answer without id_token was accepted")
	}
}

// Verifies SPEC-0032-R06: the ID token is verified for signature, issuer,
// times, audience (the client ID, and azp with other audiences), and nonce.
func TestConsoleVerifyIDToken(t *testing.T) {
	p, _, key, now := newConsoleEnv(t, "https://login.example.com/auth", false)
	stranger := newECKey(t, "id-key")
	claims := func(overrides map[string]any) map[string]any {
		c := map[string]any{"iss": p.issuer, "sub": "user-42", "aud": "kavrynt-console", "nonce": "n-1",
			"exp": now.Add(5 * time.Minute).Unix(), "iat": now.Add(-time.Minute).Unix(), "groups": []string{"platform"}}
		for k, v := range overrides {
			if v == nil {
				delete(c, k)
			} else {
				c[k] = v
			}
		}
		return c
	}
	cases := []struct {
		name  string
		token string
		ok    bool
	}{
		{"valid", key.sign(t, claims(nil)), true},
		{"several audiences with azp", key.sign(t, claims(map[string]any{"aud": []string{"kavrynt-console", "other"}, "azp": "kavrynt-console"})), true},
		{"several audiences without azp", key.sign(t, claims(map[string]any{"aud": []string{"kavrynt-console", "other"}})), false},
		{"another audience", key.sign(t, claims(map[string]any{"aud": "other-client"})), false},
		{"wrong nonce", key.sign(t, claims(map[string]any{"nonce": "n-2"})), false},
		{"no nonce", key.sign(t, claims(map[string]any{"nonce": nil})), false},
		{"another issuer", key.sign(t, claims(map[string]any{"iss": "https://evil.example.com"})), false},
		{"expired", key.sign(t, claims(map[string]any{"exp": now.Add(-5 * time.Minute).Unix()})), false},
		{"no exp", key.sign(t, claims(map[string]any{"exp": nil})), false},
		{"signed by another key", stranger.sign(t, claims(nil)), false},
		{"not a JWT", "not.a.token", false},
	}
	for _, tc := range cases {
		got, err := p.VerifyIDToken(context.Background(), tc.token, "n-1")
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
		if tc.ok && (got.Subject != "user-42" || strings.Join(got.StringClaims("groups"), ",") != "platform") {
			t.Errorf("%s: claims %+v", tc.name, got)
		}
	}
}
