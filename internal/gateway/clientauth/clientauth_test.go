package clientauth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	testIssuer   = "https://as.example.com/tenant"
	otherIssuer  = "https://other.example.com"
	testResource = "https://mcp.example.com"
	testRoute    = "team-a.search"
)

// fakeAS is an in-memory authorization server behind a RoundTripper: it
// serves metadata documents and key sets and counts requests per URL.
type fakeAS struct {
	mu        sync.Mutex
	documents map[string]any
	status    map[string]int
	requests  map[string]int
}

func newFakeAS() *fakeAS {
	return &fakeAS{documents: map[string]any{}, status: map[string]int{}, requests: map[string]int{}}
}

func (f *fakeAS) set(url string, document any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.documents[url] = document
	delete(f.status, url)
}

func (f *fakeAS) fail(url string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[url] = status
}

func (f *fakeAS) count(url string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[url]
}

func (f *fakeAS) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	url := req.URL.String()
	f.requests[url]++
	status, failing := f.status[url]
	document, found := f.documents[url]
	if !failing && !found {
		status = http.StatusNotFound
	}
	if status == 0 {
		status = http.StatusOK
	}
	var body []byte
	if status == http.StatusOK {
		if raw, ok := document.([]byte); ok {
			body = raw
		} else {
			body, _ = json.Marshal(document)
		}
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

type signingKey struct {
	kid    string
	alg    jose.SignatureAlgorithm
	secret any
	public jose.JSONWebKey
}

func newECKey(t *testing.T, kid string) signingKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signingKey{kid: kid, alg: jose.ES256, secret: key, public: jose.JSONWebKey{Key: &key.PublicKey, KeyID: kid, Algorithm: "ES256", Use: "sig"}}
}

func newRSAKey(t *testing.T, kid string) signingKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return signingKey{kid: kid, alg: jose.RS256, secret: key, public: jose.JSONWebKey{Key: &key.PublicKey, KeyID: kid, Algorithm: "RS256", Use: "sig"}}
}

func jwks(keys ...signingKey) jose.JSONWebKeySet {
	set := jose.JSONWebKeySet{}
	for _, k := range keys {
		set.Keys = append(set.Keys, k.public)
	}
	return set
}

func (k signingKey) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: k.alg, Key: k.secret},
		(&jose.SignerOptions{}).WithType("at+jwt").WithHeader("kid", k.kid))
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

type testEnv struct {
	auth  *Authenticator
	as    *fakeAS
	key   signingKey
	logs  *bytes.Buffer
	clock *time.Time
}

func (e *testEnv) claims(overrides map[string]any) map[string]any {
	claims := map[string]any{
		"iss":       testIssuer,
		"sub":       "user-42",
		"client_id": "support-agent",
		"aud":       testResource + "/mcp/" + testRoute,
		"exp":       e.clock.Add(5 * time.Minute).Unix(),
		"iat":       e.clock.Add(-time.Minute).Unix(),
		"scope":     "mcp:tools openid",
	}
	for k, v := range overrides {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}
	return claims
}

// newEnv trusts testIssuer with an explicit JWKS URI and otherIssuer through
// discovery, and requires scope mcp:tools.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	as := newFakeAS()
	key := newECKey(t, "key-1")
	as.set(testIssuer+"/jwks", jwks(key))
	clock := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	logs := &bytes.Buffer{}
	auth, err := New(Config{
		ResourceURL:    testResource,
		Issuers:        []Issuer{{Issuer: testIssuer, JWKSURI: testIssuer + "/jwks"}, {Issuer: otherIssuer}},
		RequiredScopes: []string{"mcp:tools"},
	}, as, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	env := &testEnv{auth: auth, as: as, key: key, logs: logs, clock: &clock}
	auth.now = func() time.Time { return *env.clock }
	return env
}

func request(token string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/mcp/"+testRoute, strings.NewReader(`{}`))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func wantRefusal(t *testing.T, refusal *Refusal, status int, code string) {
	t.Helper()
	if refusal == nil {
		t.Fatalf("token accepted, want HTTP %d %s", status, code)
	}
	if refusal.Status != status {
		t.Fatalf("status = %d (%s), want %d", refusal.Status, refusal.WWWAuthenticate, status)
	}
	if code != "" && !strings.Contains(refusal.WWWAuthenticate, `error="`+code+`"`) {
		t.Fatalf("WWW-Authenticate = %q, want error=%q", refusal.WWWAuthenticate, code)
	}
	if !strings.Contains(refusal.WWWAuthenticate, `resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/mcp/team-a.search"`) {
		t.Fatalf("WWW-Authenticate = %q, want the route's resource_metadata", refusal.WWWAuthenticate)
	}
}

// Verifies SPEC-0006-R02 and SPEC-0006-R20.
func TestConfigValidation(t *testing.T) {
	good := Issuer{Issuer: "https://as.example.com"}
	cases := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"valid", Config{ResourceURL: "https://mcp.example.com", Issuers: []Issuer{good}}, true},
		{"trailing slash", Config{ResourceURL: "https://mcp.example.com/", Issuers: []Issuer{good}}, true},
		{"no resource URL", Config{Issuers: []Issuer{good}}, false},
		{"http resource URL", Config{ResourceURL: "http://mcp.example.com", Issuers: []Issuer{good}}, false},
		{"resource URL with path", Config{ResourceURL: "https://mcp.example.com/kavrynt", Issuers: []Issuer{good}}, false},
		{"resource URL with query", Config{ResourceURL: "https://mcp.example.com?x=1", Issuers: []Issuer{good}}, false},
		{"no issuer", Config{ResourceURL: "https://mcp.example.com"}, false},
		{"duplicate issuer", Config{ResourceURL: "https://mcp.example.com", Issuers: []Issuer{good, good}}, false},
		{"http issuer", Config{ResourceURL: "https://mcp.example.com", Issuers: []Issuer{{Issuer: "http://keycloak.auth.svc/realms/a"}}}, false},
		{"http issuer allowed", Config{ResourceURL: "https://mcp.example.com", Issuers: []Issuer{{Issuer: "http://keycloak.auth.svc/realms/a", AllowHTTP: true}}}, true},
		{"http jwks", Config{ResourceURL: "https://mcp.example.com", Issuers: []Issuer{{Issuer: "https://as.example.com", JWKSURI: "http://as.example.com/jwks"}}}, false},
		{"issuer with credentials", Config{ResourceURL: "https://mcp.example.com", Issuers: []Issuer{{Issuer: "https://u:p@as.example.com"}}}, false},
		{"bad scope", Config{ResourceURL: "https://mcp.example.com", Issuers: []Issuer{good}, RequiredScopes: []string{"two words"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// Verifies SPEC-0006-R20: trusting a plain-http issuer is logged.
func TestHTTPIssuerIsLogged(t *testing.T) {
	logs := &bytes.Buffer{}
	_, err := New(Config{ResourceURL: testResource, Issuers: []Issuer{{Issuer: "http://keycloak.auth.svc/realms/a", AllowHTTP: true}}},
		newFakeAS(), slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "http://keycloak.auth.svc/realms/a") {
		t.Fatalf("log = %q, want a warning naming the issuer", logs.String())
	}
}

// Verifies SPEC-0006-R03.
func TestMissingTokenIsChallenged(t *testing.T) {
	env := newEnv(t)
	for _, header := range []string{"", "Basic dXNlcjpwYXNz", "Bearer", "Bearer a b"} {
		r := request("")
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		_, refusal := env.auth.Authenticate(r, testRoute)
		wantRefusal(t, refusal, http.StatusUnauthorized, "")
		if strings.Contains(refusal.WWWAuthenticate, "error=") {
			t.Fatalf("challenge without a token = %q, want no error code (RFC 6750)", refusal.WWWAuthenticate)
		}
		if !strings.Contains(refusal.WWWAuthenticate, `scope="mcp:tools"`) {
			t.Fatalf("challenge = %q, want the required scope", refusal.WWWAuthenticate)
		}
		if refusal.Outcome != OutcomeUnauthenticated {
			t.Fatalf("outcome = %q", refusal.Outcome)
		}
	}
}

// Verifies SPEC-0006-R05, SPEC-0006-R08, and SPEC-0006-R09.
func TestTokenValidation(t *testing.T) {
	env := newEnv(t)
	stranger := newECKey(t, "key-1") // same kid, different key
	rsaKey := newRSAKey(t, "key-rsa")
	env.as.set(testIssuer+"/jwks", jwks(env.key, rsaKey))

	hmacSigner, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte(strings.Repeat("s", 32))}, nil)
	if err != nil {
		t.Fatal(err)
	}
	hmacToken, err := jwt.Signed(hmacSigner).Claims(env.claims(nil)).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(env.claims(nil))
	noneToken := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload) + "."

	cases := []struct {
		name  string
		token string
		ok    bool
	}{
		{"valid ES256", env.key.sign(t, env.claims(nil)), true},
		{"valid RS256", rsaKey.sign(t, env.claims(nil)), true},
		{"audience in an array", env.key.sign(t, env.claims(map[string]any{"aud": []string{"https://other.example.com", testResource + "/mcp/" + testRoute}})), true},
		{"expired within skew", env.key.sign(t, env.claims(map[string]any{"exp": env.clock.Add(-30 * time.Second).Unix()})), true},
		{"HS256", hmacToken, false},
		{"alg none", noneToken, false},
		{"garbage", "not.a.token", false},
		{"signed by another key", stranger.sign(t, env.claims(nil)), false},
		{"untrusted issuer", env.key.sign(t, env.claims(map[string]any{"iss": "https://evil.example.com"})), false},
		{"issuer differs by a slash", env.key.sign(t, env.claims(map[string]any{"iss": testIssuer + "/"})), false},
		{"expired", env.key.sign(t, env.claims(map[string]any{"exp": env.clock.Add(-2 * time.Minute).Unix()})), false},
		{"no exp", env.key.sign(t, env.claims(map[string]any{"exp": nil})), false},
		{"not yet valid", env.key.sign(t, env.claims(map[string]any{"nbf": env.clock.Add(2 * time.Minute).Unix()})), false},
		{"issued in the future", env.key.sign(t, env.claims(map[string]any{"iat": env.clock.Add(10 * time.Minute).Unix()})), false},
		{"audience of another route", env.key.sign(t, env.claims(map[string]any{"aud": testResource + "/mcp/team-b.billing"})), false},
		{"audience of the Gateway base URL", env.key.sign(t, env.claims(map[string]any{"aud": testResource})), false},
		{"audience of another resource", env.key.sign(t, env.claims(map[string]any{"aud": "https://api.example.com"})), false},
		{"no audience", env.key.sign(t, env.claims(map[string]any{"aud": nil})), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			identity, refusal := env.auth.Authenticate(request(tc.token), testRoute)
			if tc.ok {
				if refusal != nil {
					t.Fatalf("refused: %+v", refusal)
				}
				if identity.Subject != "user-42" || identity.ClientID != "support-agent" || identity.Issuer != testIssuer {
					t.Fatalf("identity = %+v", identity)
				}
				return
			}
			wantRefusal(t, refusal, http.StatusUnauthorized, "invalid_token")
			if refusal.Message != "invalid token" {
				t.Fatalf("message = %q, want one that does not say which check failed", refusal.Message)
			}
		})
	}
}

// Verifies SPEC-0006-R06.
func TestRequiredScopes(t *testing.T) {
	env := newEnv(t)
	_, refusal := env.auth.Authenticate(request(env.key.sign(t, env.claims(map[string]any{"scope": "openid"}))), testRoute)
	wantRefusal(t, refusal, http.StatusForbidden, "insufficient_scope")
	if !strings.Contains(refusal.WWWAuthenticate, `scope="mcp:tools"`) || refusal.Outcome != OutcomeForbidden {
		t.Fatalf("refusal = %+v", refusal)
	}

	identity, refusal := env.auth.Authenticate(request(env.key.sign(t, env.claims(map[string]any{"scope": nil, "scp": []string{"mcp:tools"}}))), testRoute)
	if refusal != nil || len(identity.Scopes) != 1 {
		t.Fatalf("scp array: identity %+v, refusal %+v", identity, refusal)
	}
	identity, refusal = env.auth.Authenticate(request(env.key.sign(t, env.claims(map[string]any{"client_id": nil, "azp": "etl"}))), testRoute)
	if refusal != nil || identity.ClientID != "etl" {
		t.Fatalf("azp: identity %+v, refusal %+v", identity, refusal)
	}
}

// Verifies SPEC-0006-R07.
func TestProtectedResourceMetadata(t *testing.T) {
	env := newEnv(t)
	got, err := json.Marshal(env.auth.Metadata(testRoute))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"resource":"https://mcp.example.com/mcp/team-a.search","authorization_servers":["https://as.example.com/tenant","https://other.example.com"],"bearer_methods_supported":["header"],"scopes_supported":["mcp:tools"]}`
	if string(got) != want {
		t.Fatalf("metadata = %s\nwant       %s", got, want)
	}
	if env.auth.MetadataURL(testRoute) != "https://mcp.example.com/.well-known/oauth-protected-resource/mcp/team-a.search" {
		t.Fatalf("metadata URL = %s", env.auth.MetadataURL(testRoute))
	}
}

// Verifies SPEC-0006-R10: keys come from the authorization server metadata,
// then OpenID Connect discovery, are cached, refreshed, and fetched again
// for an unknown kid at most once a minute.
func TestKeyDiscoveryAndRefresh(t *testing.T) {
	env := newEnv(t)
	other := newECKey(t, "other-1")
	oauthMetadata := "https://other.example.com/.well-known/oauth-authorization-server"
	oidcMetadata := "https://other.example.com/.well-known/openid-configuration"
	env.as.set(oidcMetadata, map[string]string{"issuer": otherIssuer, "jwks_uri": "https://other.example.com/keys"})
	env.as.set("https://other.example.com/keys", jwks(other))

	sign := func(k signingKey) string {
		return k.sign(t, env.claims(map[string]any{"iss": otherIssuer}))
	}
	if _, refusal := env.auth.Authenticate(request(sign(other)), testRoute); refusal != nil {
		t.Fatalf("token of a discovered issuer refused: %+v", refusal)
	}
	if env.as.count(oauthMetadata) != 1 || env.as.count(oidcMetadata) != 1 {
		t.Fatalf("discovery requests: RFC 8414 %d, OIDC %d, want one each", env.as.count(oauthMetadata), env.as.count(oidcMetadata))
	}
	if _, refusal := env.auth.Authenticate(request(sign(other)), testRoute); refusal != nil || env.as.count("https://other.example.com/keys") != 1 {
		t.Fatalf("second token: refusal %+v, key fetches %d, want cached keys", refusal, env.as.count("https://other.example.com/keys"))
	}

	// A rotated key is fetched on its first use, but unknown kids cannot
	// force more than one fetch a minute.
	rotated := newECKey(t, "other-2")
	if _, refusal := env.auth.Authenticate(request(sign(rotated)), testRoute); refusal == nil {
		t.Fatal("token of an unpublished key accepted")
	}
	*env.clock = env.clock.Add(30 * time.Second)
	env.as.set("https://other.example.com/keys", jwks(other, rotated))
	if _, refusal := env.auth.Authenticate(request(sign(rotated)), testRoute); refusal == nil {
		t.Fatal("refetched within a minute of the last fetch")
	}
	*env.clock = env.clock.Add(time.Minute)
	if _, refusal := env.auth.Authenticate(request(sign(rotated)), testRoute); refusal != nil {
		t.Fatalf("rotated key not picked up after a minute: %+v", refusal)
	}
	fetches := env.as.count("https://other.example.com/keys")

	// Keys are refreshed after 15 minutes.
	*env.clock = env.clock.Add(16 * time.Minute)
	env.auth.Authenticate(request(sign(other)), testRoute)
	if env.as.count("https://other.example.com/keys") != fetches+1 {
		t.Fatalf("keys not refreshed after 15 minutes")
	}
}

// Verifies SPEC-0006-R10: metadata naming another issuer is not trusted.
func TestDiscoveryChecksTheIssuer(t *testing.T) {
	env := newEnv(t)
	env.as.set("https://other.example.com/.well-known/oauth-authorization-server", map[string]string{"issuer": "https://evil.example.com", "jwks_uri": "https://evil.example.com/keys"})
	_, refusal := env.auth.Authenticate(request(env.key.sign(t, env.claims(map[string]any{"iss": otherIssuer}))), testRoute)
	if refusal == nil || refusal.Status != http.StatusServiceUnavailable {
		t.Fatalf("refusal = %+v, want 503", refusal)
	}
	if env.as.count("https://evil.example.com/keys") != 0 {
		t.Fatal("fetched keys from a URL named by metadata of another issuer")
	}
}

// Verifies SPEC-0006-R11: http and oversized documents are refused, and
// every request goes through the given transport (the Gateway passes its
// egress-guarded one).
func TestKeyFetchBounds(t *testing.T) {
	as := newFakeAS()
	auth, err := New(Config{ResourceURL: testResource, Issuers: []Issuer{{Issuer: testIssuer, JWKSURI: testIssuer + "/jwks"}}}, as, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return clock }
	key := newECKey(t, "key-1")
	as.set(testIssuer+"/jwks", append([]byte(`{"keys":[],"pad":"`), append(bytes.Repeat([]byte("x"), maxDocumentBytes), []byte(`"}`)...)...))
	token := key.sign(t, map[string]any{"iss": testIssuer, "aud": testResource + "/mcp/" + testRoute, "exp": clock.Add(time.Minute).Unix()})
	if _, refusal := auth.Authenticate(request(token), testRoute); refusal == nil || refusal.Status != http.StatusServiceUnavailable {
		t.Fatalf("oversized key set: refusal = %+v, want 503", refusal)
	}
	if as.count(testIssuer+"/jwks") != 1 {
		t.Fatal("the key set was not fetched through the given transport")
	}

	set := newKeySet(Issuer{Issuer: testIssuer}, &http.Client{Transport: as}, func() time.Time { return clock })
	if err := set.getJSON(t.Context(), "http://as.example.com/jwks", &struct{}{}); err == nil {
		t.Fatal("fetched an http URL without allowHTTP")
	}
	if fetchTimeout > 5*time.Second {
		t.Fatalf("fetch timeout %s, want at most 5s", fetchTimeout)
	}
}

// Verifies SPEC-0006-R12: an issuer whose keys cannot be loaded fails
// closed with 503; other issuers keep working.
func TestUnavailableKeysFailClosedPerIssuer(t *testing.T) {
	env := newEnv(t)
	env.as.fail("https://other.example.com/.well-known/oauth-authorization-server", http.StatusInternalServerError)
	env.as.fail("https://other.example.com/.well-known/openid-configuration", http.StatusInternalServerError)

	_, refusal := env.auth.Authenticate(request(env.key.sign(t, env.claims(map[string]any{"iss": otherIssuer}))), testRoute)
	if refusal == nil || refusal.Status != http.StatusServiceUnavailable || refusal.RetryAfter == "" || refusal.Outcome != OutcomeAuthUnavailable {
		t.Fatalf("refusal = %+v, want 503 with Retry-After and auth_unavailable", refusal)
	}
	if _, refusal := env.auth.Authenticate(request(env.key.sign(t, env.claims(nil))), testRoute); refusal != nil {
		t.Fatalf("a working issuer was affected: %+v", refusal)
	}
}

// Verifies SPEC-0006-R14: tokens and claims never reach the logs.
func TestLogsNeverContainTokens(t *testing.T) {
	env := newEnv(t)
	tokens := []string{
		env.key.sign(t, env.claims(nil)),
		env.key.sign(t, env.claims(map[string]any{"exp": env.clock.Add(-time.Hour).Unix()})),
		env.key.sign(t, env.claims(map[string]any{"iss": "https://evil.example.com"})),
		newECKey(t, "key-1").sign(t, env.claims(nil)),
	}
	for _, token := range tokens {
		env.auth.Authenticate(request(token), testRoute)
		for _, part := range strings.Split(token, ".") {
			if strings.Contains(env.logs.String(), part) {
				t.Fatalf("log contains part of a token: %s", env.logs.String())
			}
		}
	}
	if strings.Contains(env.logs.String(), "user-42") || strings.Contains(env.logs.String(), "support-agent") {
		t.Fatalf("log contains token claims: %s", env.logs.String())
	}
}
