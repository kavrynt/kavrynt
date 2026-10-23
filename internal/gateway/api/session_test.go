package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/auth"
)

func consoleAuthHandler(t *testing.T, upstream *[]*http.Request) *Handler {
	t.Helper()
	h := trafficHandler(t, func(r *http.Request) (*http.Response, error) {
		*upstream = append(*upstream, r)
		return jsonResponse(http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`), nil
	})
	dir := t.TempDir()
	for name, value := range map[string]string{auth.PasswordFile: "s3cret-admin-password", auth.SessionKeyFile: strings.Repeat("k", 64)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h.EnableConsoleAuth(auth.New(dir, time.Hour))
	return h
}

func get(h *Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr
}

// Verifies SPEC-0005-R01.
func TestConsoleDataNeedsASession(t *testing.T) {
	var upstream []*http.Request
	h := consoleAuthHandler(t, &upstream)
	for _, path := range []string{"/v1/servers", "/v1/traffic"} {
		rr := get(h, path)
		if rr.Code != http.StatusUnauthorized || !strings.Contains(rr.Body.String(), `"error":"login required"`) {
			t.Fatalf("GET %s without session = %d %s, want 401 JSON", path, rr.Code, rr.Body)
		}
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/session", strings.NewReader(`{"username":"admin","password":"s3cret-admin-password"}`))
	r.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("login = %d %s", rr.Code, rr.Body)
	}
	cookies := rr.Result().Cookies()
	for _, path := range []string{"/v1/servers", "/v1/traffic"} {
		if rr := get(h, path, cookies...); rr.Code != http.StatusOK {
			t.Fatalf("GET %s with session = %d, want 200", path, rr.Code)
		}
	}
}

// Verifies SPEC-0005-R02.
func TestConsoleLoginLeavesOtherEndpointsAlone(t *testing.T) {
	var upstream []*http.Request
	h := consoleAuthHandler(t, &upstream)
	for _, path := range []string{"/v1/routes", "/healthz", "/readyz", "/version", "/metrics"} {
		if rr := get(h, path); rr.Code != http.StatusOK {
			t.Fatalf("GET %s without session = %d, want 200", path, rr.Code)
		}
	}
	rr := post(h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"Content-Type": "application/json"})
	if rr.Code != http.StatusOK || len(upstream) != 1 {
		t.Fatalf("MCP call without session = %d (%d upstream requests), want it proxied", rr.Code, len(upstream))
	}
}

// Verifies SPEC-0005-R03.
func TestWithoutConsoleLoginEndpointsAreOpen(t *testing.T) {
	h := trafficHandler(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, `{}`), nil })
	for _, path := range []string{"/v1/servers", "/v1/traffic"} {
		if rr := get(h, path); rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, rr.Code)
		}
	}
	rr := get(h, "/v1/session")
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `{"enabled":false}` {
		t.Fatalf("GET /v1/session = %d %s, want login reported off", rr.Code, rr.Body)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/session", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("POST /v1/session with login off = %d, want 404", rr.Code)
	}
}

// Verifies SPEC-0005-R08.
func TestLoginKeepsTheOriginCheck(t *testing.T) {
	var upstream []*http.Request
	h := consoleAuthHandler(t, &upstream)
	r := httptest.NewRequest(http.MethodPost, "/v1/session", strings.NewReader(`{"username":"admin","password":"s3cret-admin-password"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://attacker.example")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusForbidden || len(rr.Result().Cookies()) != 0 {
		t.Fatalf("cross-origin login = %d, want 403 without a session", rr.Code)
	}
}
