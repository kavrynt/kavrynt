package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kavrynt/kavrynt/internal/gateway/clientauth"
)

// fakeProvider is an identity provider that remembers what it was asked.
type fakeProvider struct {
	endpoint    string
	endpointErr error
	claims      clientauth.ConsoleClaims
	verifyErr   error
	gotCode     string
	gotVerifier string
	gotRedirect string
	gotNonce    string
}

func (f *fakeProvider) AuthorizationEndpoint(context.Context) (string, error) {
	return f.endpoint, f.endpointErr
}

func (f *fakeProvider) ExchangeCode(_ context.Context, code, verifier, redirectURI string) (string, error) {
	f.gotCode, f.gotVerifier, f.gotRedirect = code, verifier, redirectURI
	return "id-token-value-xyz", nil
}

func (f *fakeProvider) VerifyIDToken(_ context.Context, raw, nonce string) (clientauth.ConsoleClaims, error) {
	f.gotNonce = nonce
	if raw != "id-token-value-xyz" {
		return clientauth.ConsoleClaims{}, errors.New("unexpected token")
	}
	return f.claims, f.verifyErr
}

const testRedirect = "https://mcp.example.com/v1/session/oidc/callback"

func newSSOAuth(t *testing.T, keepAdmin bool) (*Authenticator, *fakeProvider) {
	t.Helper()
	a, _, _, _ := newTestAuth(t)
	provider := &fakeProvider{
		endpoint: "https://login.example.com/authorize?tenant=acme",
		claims: clientauth.ConsoleClaims{Subject: "user-42", Claims: map[string]any{
			"sub": "user-42", "preferred_username": "priya", "groups": []any{"platform", "ops"},
		}},
	}
	a.EnableOIDC(OIDC{Provider: provider, ProviderName: "Keycloak", ClientID: "kavrynt-console", RedirectURL: testRedirect,
		Scopes: []string{"profile"}, AllowedGroups: []string{"platform"}, KeepAdminLogin: keepAdmin})
	return a, provider
}

func serve(a *Authenticator, r *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	if strings.HasPrefix(r.URL.Path, "/v1/session/oidc/") {
		a.ServeOIDC(rr, r)
	} else {
		a.ServeHTTP(rr, r)
	}
	return rr
}

func cookieNamed(rr *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rr.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// startSignIn begins a sign-in and returns the redirect's query and the
// state cookie.
func startSignIn(t *testing.T, a *Authenticator) (url.Values, *http.Cookie) {
	t.Helper()
	rr := serve(a, httptest.NewRequest(http.MethodGet, StartPath, nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("start: %d %s", rr.Code, rr.Body.String())
	}
	target, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	cookie := cookieNamed(rr, stateCookieName)
	if cookie == nil {
		t.Fatal("no state cookie")
	}
	return target.Query(), cookie
}

func callback(a *Authenticator, query string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, CallbackPath+"?"+query, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return serve(a, r)
}

// Verifies SPEC-0032-R03 and SPEC-0032-R04: the start redirects to the
// authorization endpoint with the client, redirect URI, scopes, state, nonce,
// and a PKCE S256 challenge of the verifier kept in a signed, HttpOnly,
// SameSite=Lax cookie scoped to the callback for 10 minutes.
func TestSignInStart(t *testing.T) {
	a, _ := newSSOAuth(t, false)
	q, cookie := startSignIn(t, a)
	for key, want := range map[string]string{
		"tenant": "acme", "response_type": "code", "client_id": "kavrynt-console",
		"redirect_uri": testRedirect, "scope": "openid profile", "code_challenge_method": "S256",
	} {
		if q.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, q.Get(key), want)
		}
	}
	if q.Get("state") == "" || q.Get("nonce") == "" || q.Get("state") == q.Get("nonce") {
		t.Fatalf("state and nonce: %v", q)
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != CallbackPath || cookie.MaxAge != 600 {
		t.Fatalf("state cookie: %+v", cookie)
	}
	creds, _ := a.credentials()
	r := httptest.NewRequest(http.MethodGet, CallbackPath, nil)
	r.AddCookie(cookie)
	st, ok := readSignInState(r, creds, a.now())
	sum := sha256.Sum256([]byte(st.Verifier))
	if !ok || st.State != q.Get("state") || st.Nonce != q.Get("nonce") || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatalf("cookie state %+v does not match the redirect %v", st, q)
	}

	// The cookie is signed: a forged one is refused.
	forged := *cookie
	forged.Value = strings.Replace(cookie.Value, ".", "x.", 1)
	if rr := callback(a, "state="+q.Get("state")+"&code=c", &forged); rr.Code != http.StatusBadRequest {
		t.Fatalf("forged state cookie: %d", rr.Code)
	}
}

// Verifies SPEC-0032-R03: an unreachable provider does not start a sign-in.
func TestSignInStartNeedsTheProvider(t *testing.T) {
	a, provider := newSSOAuth(t, false)
	provider.endpointErr = errors.New("the authorization endpoint must use https")
	if rr := serve(a, httptest.NewRequest(http.MethodGet, StartPath, nil)); rr.Code != http.StatusServiceUnavailable || cookieNamed(rr, stateCookieName) != nil {
		t.Fatalf("unavailable provider: %d", rr.Code)
	}
}

// Verifies SPEC-0032-R04 and SPEC-0032-R05: a missing or mismatched state, a
// provider error, and a missing code are refused with 400 and no session,
// and the state cookie is cleared every time.
func TestSignInCallbackRefusals(t *testing.T) {
	a, provider := newSSOAuth(t, false)
	q, cookie := startSignIn(t, a)
	state := q.Get("state")
	for name, rr := range map[string]*httptest.ResponseRecorder{
		"no cookie":      callback(a, "state="+state+"&code=c", nil),
		"other state":    callback(a, "state=someone-else&code=c", cookie),
		"provider error": callback(a, "error=access_denied&state="+state, cookie),
		"no code":        callback(a, "state="+state, cookie),
	} {
		if rr.Code != http.StatusBadRequest || cookieNamed(rr, CookieName) != nil {
			t.Errorf("%s: %d, want 400 without a session", name, rr.Code)
		}
		if cleared := cookieNamed(rr, stateCookieName); cleared == nil || cleared.MaxAge >= 0 {
			t.Errorf("%s: state cookie not cleared", name)
		}
	}
	if provider.gotCode != "" {
		t.Fatal("a refused callback exchanged its code")
	}
}

// Verifies SPEC-0032-R05, SPEC-0032-R07, SPEC-0032-R08, SPEC-0032-R09, and
// SPEC-0032-R11: the code is exchanged with the cookie's verifier, the ID
// token is verified with its nonce, an allowed group gets a session named
// by preferred_username, the session reports method oidc, and no code or
// token is logged.
func TestSignInSucceedsForAllowedGroups(t *testing.T) {
	a, provider := newSSOAuth(t, false)
	q, cookie := startSignIn(t, a)
	r := httptest.NewRequest(http.MethodGet, CallbackPath+"?state="+q.Get("state")+"&code=secret-code-123", nil)
	r.AddCookie(cookie)
	creds, _ := a.credentials()
	st, _ := readSignInState(r, creds, a.now())
	rr := serve(a, r)
	session := cookieNamed(rr, CookieName)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ui/" || session == nil {
		t.Fatalf("callback: %d %v", rr.Code, rr.Header())
	}
	if provider.gotCode != "secret-code-123" || provider.gotVerifier != st.Verifier || provider.gotRedirect != testRedirect || provider.gotNonce != q.Get("nonce") {
		t.Fatalf("exchange or verification inputs: %+v", provider)
	}
	if !session.HttpOnly || session.SameSite != http.SameSiteStrictMode || session.Path != cookiePath || session.MaxAge != 3600 {
		t.Fatalf("session cookie: %+v", session)
	}
	get := httptest.NewRequest(http.MethodGet, "/v1/session", nil)
	get.AddCookie(session)
	var body map[string]any
	if rr := serve(a, get); rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &body) != nil || body["method"] != "oidc" || body["username"] != "priya" {
		t.Fatalf("session: %d %s", rr.Code, rr.Body.String())
	}
	if !a.Authorized(withCookie(session)) {
		t.Fatal("the SSO session does not open the console")
	}
}

// Verifies SPEC-0032-R07 and SPEC-0032-R08: a user in no listed group gets
// 403 and no session; a listed subject is enough, and the display name falls
// back to sub.
func TestSignInChecksTheAllowLists(t *testing.T) {
	a, provider := newSSOAuth(t, false)
	provider.claims = clientauth.ConsoleClaims{Subject: "user-7", Claims: map[string]any{"sub": "user-7", "groups": []any{"finance"}}}
	q, cookie := startSignIn(t, a)
	if rr := callback(a, "state="+q.Get("state")+"&code=c", cookie); rr.Code != http.StatusForbidden || cookieNamed(rr, CookieName) != nil {
		t.Fatalf("not allowed: %d", rr.Code)
	}
	a.oidc.AllowedSubjects = []string{"user-7"}
	q, cookie = startSignIn(t, a)
	rr := callback(a, "state="+q.Get("state")+"&code=c", cookie)
	session := cookieNamed(rr, CookieName)
	get := httptest.NewRequest(http.MethodGet, "/v1/session", nil)
	if session != nil {
		get.AddCookie(session)
	}
	if rr := serve(a, get); !strings.Contains(rr.Body.String(), `"username":"user-7"`) {
		t.Fatalf("allowed subject: %s", rr.Body.String())
	}
	provider.verifyErr = errors.New("nonce does not match")
	q, cookie = startSignIn(t, a)
	if rr := callback(a, "state="+q.Get("state")+"&code=c", cookie); rr.Code != http.StatusBadRequest || cookieNamed(rr, CookieName) != nil {
		t.Fatalf("unverified ID token: %d", rr.Code)
	}
}

// Verifies SPEC-0032-R09: refused and successful sign-ins log reasons, never
// the code or a token.
func TestSignInLogsNoSecrets(t *testing.T) {
	a, _, logs, _ := newTestAuth(t)
	provider := &fakeProvider{endpoint: "https://login.example.com/authorize",
		claims: clientauth.ConsoleClaims{Subject: "user-42", Claims: map[string]any{"groups": []any{"platform"}}}}
	a.EnableOIDC(OIDC{Provider: provider, ClientID: "c", RedirectURL: testRedirect, AllowedGroups: []string{"platform"}})
	q, cookie := startSignIn(t, a)
	callback(a, "state="+q.Get("state")+"&code=secret-code-123", cookie)
	callback(a, "state=wrong&code=secret-code-456", cookie)
	for _, secret := range []string{"secret-code-123", "secret-code-456", "id-token-value-xyz", q.Get("state"), q.Get("nonce")} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("logs contain %q:\n%s", secret, logs.String())
		}
	}
	if !strings.Contains(logs.String(), "console sign-in refused") || !strings.Contains(logs.String(), "console sign-in succeeded") {
		t.Fatalf("logs lack the outcomes:\n%s", logs.String())
	}
}

// Verifies SPEC-0032-R10 and SPEC-0032-R11: with SSO on, the admin login
// answers 404 and an admin session no longer counts, unless keepAdminLogin;
// the login page learns that SSO is offered and whether the password is.
func TestAdminLoginWithSSO(t *testing.T) {
	a, _, _, _ := newTestAuth(t)
	adminSession := sessionCookie(t, login(t, a, Username, testPassword))
	a.EnableOIDC(OIDC{Provider: &fakeProvider{}, ProviderName: "Keycloak", ClientID: "c", RedirectURL: testRedirect, AllowedGroups: []string{"platform"}})
	if rr := login(t, a, Username, testPassword); rr.Code != http.StatusNotFound {
		t.Fatalf("admin login with SSO: %d, want 404", rr.Code)
	}
	if a.Authorized(withCookie(adminSession)) {
		t.Fatal("an admin session still opens the console once SSO replaced the admin login")
	}
	var body map[string]any
	rr := serve(a, httptest.NewRequest(http.MethodGet, "/v1/session", nil))
	if json.Unmarshal(rr.Body.Bytes(), &body) != nil || body["adminLogin"] != false || body["oidc"].(map[string]any)["providerName"] != "Keycloak" {
		t.Fatalf("login page info: %s", rr.Body.String())
	}

	kept, _ := newSSOAuth(t, true)
	session := sessionCookie(t, login(t, kept, Username, testPassword))
	get := httptest.NewRequest(http.MethodGet, "/v1/session", nil)
	get.AddCookie(session)
	if rr := serve(kept, get); !strings.Contains(rr.Body.String(), `"method":"admin"`) {
		t.Fatalf("kept admin login: %s", rr.Body.String())
	}
}
