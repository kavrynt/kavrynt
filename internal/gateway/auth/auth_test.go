package auth

import (
	"bytes"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testPassword = "correct-horse-battery-staple"

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func writeCredentials(t *testing.T, dir, password, key string) {
	t.Helper()
	for name, value := range map[string]string{PasswordFile: password, SessionKeyFile: key} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func newTestAuth(t *testing.T) (*Authenticator, *clock, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	writeCredentials(t, dir, testPassword+"\n", strings.Repeat("k", 64))
	a := New(dir, time.Hour)
	c := &clock{t: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
	a.now = c.now
	logs := &bytes.Buffer{}
	a.logger = slog.New(slog.NewTextHandler(logs, nil))
	return a, c, logs, dir
}

func login(t *testing.T, a *Authenticator, username, password string, edit ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"username":` + quote(username) + `,"password":` + quote(password) + `}`
	r := httptest.NewRequest(http.MethodPost, "/v1/session", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "10.0.0.7:51000"
	for _, e := range edit {
		e(r)
	}
	rr := httptest.NewRecorder()
	a.ServeHTTP(rr, r)
	return rr
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func sessionCookie(t *testing.T, rr *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rr.Result().Cookies() {
		if c.Name == CookieName {
			return c
		}
	}
	t.Fatalf("no %s cookie in %v", CookieName, rr.Header())
	return nil
}

func withCookie(c *http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/servers", nil)
	r.AddCookie(c)
	return r
}

// Verifies SPEC-0005-R04.
func TestLoginAcceptsOnlyAdminAndThePassword(t *testing.T) {
	a, _, _, _ := newTestAuth(t)
	rr := login(t, a, "admin", testPassword)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("correct login = %d %s, want 204", rr.Code, rr.Body)
	}
	if !a.Authorized(withCookie(sessionCookie(t, rr))) {
		t.Fatal("session from a successful login is not authorized")
	}

	var bodies []string
	for _, c := range []struct{ user, password string }{
		{"admin", "wrong"}, {"root", testPassword}, {"Admin", testPassword}, {"admin", testPassword + " "}, {"admin", ""},
	} {
		a.reset("10.0.0.7")
		rr := login(t, a, c.user, c.password)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("login %q/%q = %d, want 401", c.user, c.password, rr.Code)
		}
		if len(rr.Result().Cookies()) != 0 {
			t.Fatalf("failed login %q set a cookie", c.user)
		}
		bodies = append(bodies, rr.Body.String())
	}
	for _, b := range bodies {
		if b != bodies[0] {
			t.Fatalf("failed logins answer differently (%q vs %q); a wrong username must look like a wrong password", b, bodies[0])
		}
	}
}

// Verifies SPEC-0005-R05.
func TestSessionCookieAttributesAndExpiry(t *testing.T) {
	a, c, _, _ := newTestAuth(t)
	cookie := sessionCookie(t, login(t, a, "admin", testPassword))
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/v1/" || cookie.MaxAge != 3600 {
		t.Fatalf("cookie = %+v, want HttpOnly, SameSite=Strict, Path=/v1/, Max-Age=3600", cookie)
	}
	if cookie.Secure {
		t.Fatal("cookie is Secure on a plain HTTP request; browsers would drop it")
	}
	for name, edit := range map[string]func(*http.Request){
		"TLS":               func(r *http.Request) { r.TLS = &tls.ConnectionState{} },
		"X-Forwarded-Proto": func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") },
	} {
		if !sessionCookie(t, login(t, a, "admin", testPassword, edit)).Secure {
			t.Fatalf("cookie is not Secure for HTTPS via %s", name)
		}
	}

	c.advance(time.Hour - time.Second)
	if !a.Authorized(withCookie(cookie)) {
		t.Fatal("session expired early")
	}
	c.advance(time.Second)
	if a.Authorized(withCookie(cookie)) {
		t.Fatal("session still valid after its lifetime")
	}

	// A token signed with another key, or edited, is not a session.
	forged := *cookie
	forged.Value = strings.Replace(cookie.Value, ".", "x.", 1)
	c.advance(-time.Hour)
	if a.Authorized(withCookie(&forged)) {
		t.Fatal("tampered session accepted")
	}
	other, _, _, _ := newTestAuth(t)
	writeCredentials(t, other.dir, testPassword, strings.Repeat("z", 64))
	other.now = a.now
	if other.Authorized(withCookie(cookie)) {
		t.Fatal("session signed with another key accepted")
	}
}

// Verifies SPEC-0005-R06.
func TestPasswordChangeEndsSessions(t *testing.T) {
	a, _, _, dir := newTestAuth(t)
	cookie := sessionCookie(t, login(t, a, "admin", testPassword))
	writeCredentials(t, dir, "a-new-password", strings.Repeat("k", 64))
	if a.Authorized(withCookie(cookie)) {
		t.Fatal("session survived a password change")
	}
	if rr := login(t, a, "admin", testPassword); rr.Code != http.StatusUnauthorized {
		t.Fatalf("old password after rotation = %d, want 401", rr.Code)
	}
	if rr := login(t, a, "admin", "a-new-password"); rr.Code != http.StatusNoContent {
		t.Fatalf("new password after rotation = %d, want 204", rr.Code)
	}
}

// Verifies SPEC-0005-R07.
func TestSessionEndpointReportsAndEndsSessions(t *testing.T) {
	a, _, _, _ := newTestAuth(t)
	rr := httptest.NewRecorder()
	a.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/session", nil))
	if rr.Code != http.StatusUnauthorized || !strings.Contains(rr.Body.String(), `"enabled":true`) {
		t.Fatalf("GET without session = %d %s, want 401 with enabled", rr.Code, rr.Body)
	}

	cookie := sessionCookie(t, login(t, a, "admin", testPassword))
	r := httptest.NewRequest(http.MethodGet, "/v1/session", nil)
	r.AddCookie(cookie)
	rr = httptest.NewRecorder()
	a.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"username":"admin"`) || !strings.Contains(rr.Body.String(), `"expiresAt":"2026-10-23T13:00:00Z"`) {
		t.Fatalf("GET with session = %d %s", rr.Code, rr.Body)
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("session responses must not be cached")
	}

	rr = httptest.NewRecorder()
	a.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/v1/session", nil))
	cleared := sessionCookie(t, rr)
	if rr.Code != http.StatusNoContent || cleared.MaxAge >= 0 || cleared.Value != "" || cleared.Path != "/v1/" {
		t.Fatalf("DELETE = %d cookie %+v, want 204 and an expired cookie", rr.Code, cleared)
	}

	rr = httptest.NewRecorder()
	a.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/v1/session", nil))
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != "GET, POST, DELETE" {
		t.Fatalf("PUT = %d, want 405", rr.Code)
	}
}

// Verifies SPEC-0005-R08.
func TestLoginAcceptsOnlyJSON(t *testing.T) {
	a, _, _, _ := newTestAuth(t)
	for _, contentType := range []string{"application/x-www-form-urlencoded", "text/plain", "multipart/form-data; boundary=x", ""} {
		rr := login(t, a, "admin", testPassword, func(r *http.Request) { r.Header.Set("Content-Type", contentType) })
		if rr.Code != http.StatusUnsupportedMediaType || len(rr.Result().Cookies()) != 0 {
			t.Fatalf("login as %q = %d, want 415 without a session", contentType, rr.Code)
		}
	}
	if rr := login(t, a, "admin", testPassword, func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=utf-8") }); rr.Code != http.StatusNoContent {
		t.Fatalf("login as JSON with charset = %d, want 204", rr.Code)
	}
}

// Verifies SPEC-0005-R09.
func TestFailedLoginsAreRateLimitedPerClient(t *testing.T) {
	a, c, _, _ := newTestAuth(t)
	for i := 0; i < maxFailures; i++ {
		if rr := login(t, a, "admin", "guess"); rr.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d, want 401", i+1, rr.Code)
		}
	}
	rr := login(t, a, "admin", testPassword)
	if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") != "60" {
		t.Fatalf("correct password after %d failures = %d Retry-After %q, want 429 and 60", maxFailures, rr.Code, rr.Header().Get("Retry-After"))
	}
	other := login(t, a, "admin", testPassword, func(r *http.Request) { r.RemoteAddr = "10.0.0.8:4000" })
	if other.Code != http.StatusNoContent {
		t.Fatalf("another client = %d, want 204", other.Code)
	}

	c.advance(30 * time.Second)
	if rr := login(t, a, "admin", testPassword); rr.Header().Get("Retry-After") != "30" {
		t.Fatalf("Retry-After after 30 s = %q, want 30", rr.Header().Get("Retry-After"))
	}
	c.advance(30 * time.Second)
	if rr := login(t, a, "admin", testPassword); rr.Code != http.StatusNoContent {
		t.Fatalf("login after the window = %d, want 204", rr.Code)
	}

	// A success clears earlier failures.
	for i := 0; i < maxFailures-1; i++ {
		login(t, a, "admin", "guess")
	}
	login(t, a, "admin", testPassword)
	if rr := login(t, a, "admin", "guess"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("failure after a success = %d, want 401", rr.Code)
	}

	// The table of clients is bounded.
	for i := 0; i < maxTrackedClients+10; i++ {
		a.fail("192.0.2." + time.Duration(i).String())
	}
	if len(a.failures) > maxTrackedClients {
		t.Fatalf("tracking %d clients, limit %d", len(a.failures), maxTrackedClients)
	}
}

// Verifies SPEC-0005-R10.
func TestLoginLogsNeverContainSecrets(t *testing.T) {
	a, _, logs, _ := newTestAuth(t)
	login(t, a, "admin", "wrong-guess-value")
	rr := login(t, a, "admin", testPassword)
	token := sessionCookie(t, rr).Value
	out := logs.String()
	for _, secret := range []string{testPassword, "wrong-guess-value", token, strings.Repeat("k", 64)} {
		if strings.Contains(out, secret) {
			t.Fatalf("logs contain a secret: %s", out)
		}
	}
	for _, want := range []string{"console login failed", "console login succeeded", "client=10.0.0.7"} {
		if !strings.Contains(out, want) {
			t.Fatalf("logs lack %q: %s", want, out)
		}
	}
	if strings.Contains(rr.Body.String(), testPassword) {
		t.Fatal("login response contains the password")
	}
}

// Verifies SPEC-0005-R11.
func TestMissingCredentialsFailClosed(t *testing.T) {
	for name, setup := range map[string]func(dir string){
		"no files":          func(string) {},
		"empty password":    func(dir string) { writeCredentials(t, dir, "\n", "key") },
		"empty session key": func(dir string) { writeCredentials(t, dir, testPassword, "") },
	} {
		dir := t.TempDir()
		setup(dir)
		a := New(dir, time.Hour)
		a.logger = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
		rr := login(t, a, "admin", testPassword)
		if rr.Code != http.StatusServiceUnavailable || len(rr.Result().Cookies()) != 0 {
			t.Fatalf("%s: login = %d, want 503 without a session", name, rr.Code)
		}
		if a.Authorized(httptest.NewRequest(http.MethodGet, "/v1/servers", nil)) {
			t.Fatalf("%s: request without a session authorized", name)
		}
	}
}

// Verifies SPEC-0008-R07: every Gateway replica mounts the same Secret, so a
// session signed by one replica is accepted by the others.
func TestSessionsWorkAcrossReplicas(t *testing.T) {
	first, c, _, dir := newTestAuth(t)
	second := New(dir, time.Hour)
	second.now = c.now

	cookie := sessionCookie(t, login(t, first, "admin", testPassword))
	if !second.Authorized(withCookie(cookie)) {
		t.Fatal("a session from one replica is rejected by another replica with the same Secret")
	}
}

// Verifies SPEC-0006-R15: the session reports MCP client authentication.
func TestSessionReportsMCPAuth(t *testing.T) {
	a, _, _, _ := newTestAuth(t)
	a.ReportMCPAuth(true)
	rr := httptest.NewRecorder()
	a.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/session", nil))
	if rr.Code != http.StatusUnauthorized || !strings.Contains(rr.Body.String(), `"mcpAuth":true`) {
		t.Fatalf("GET /v1/session = %d %s, want 401 reporting mcpAuth", rr.Code, rr.Body.String())
	}
	cookie := sessionCookie(t, login(t, a, "admin", testPassword))
	rr = httptest.NewRecorder()
	a.ServeHTTP(rr, withCookie(cookie))
	if !strings.Contains(rr.Body.String(), `"mcpAuth":true`) {
		t.Fatalf("signed-in session = %s, want mcpAuth", rr.Body.String())
	}
}
