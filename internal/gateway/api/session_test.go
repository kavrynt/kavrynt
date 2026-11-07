package api

import (
	"io"
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
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"enabled":false`) {
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

// expiringStream returns a Gateway with client authentication on whose
// upstream answers a GET with an event stream that stays open until the test
// closes writer. The token it returns expires in one to two seconds, and the
// Gateway gives no skew past that.
func expiringStream(t *testing.T) (h *Handler, token string, writer *io.PipeWriter) {
	t.Helper()
	h, _, mint := authHandler(t, true)
	reader, writer := io.Pipe()
	h.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       reader,
		}, nil
	})}
	h.tokenSkew = 0
	t.Cleanup(func() { _ = writer.Close() })
	return h, mint(map[string]any{"exp": time.Now().Add(2 * time.Second).Unix()}), writer
}

// openStream sends a GET for the route's event stream and reports when the
// Gateway ends it.
func openStream(h *Handler, token string) (*flushRecorder, <-chan struct{}) {
	req := httptest.NewRequest(http.MethodGet, "/mcp/team-a.demo", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	rr := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		h.ServeHTTP(rr, req)
	}()
	return rr, ended
}

// Verifies SPEC-0042-R01: a GET event stream ends, by the Gateway closing the
// response, when the token that opened it expires, though the server and the
// client keep it open. The client sees a clean end, not a broken connection.
func TestStreamEndsWhenItsTokenExpires(t *testing.T) {
	t.Parallel()
	h, token, writer := expiringStream(t)
	server := httptest.NewServer(h)
	defer server.Close()
	req, err := http.NewRequest(http.MethodGet, server.URL+"/mcp/team-a.demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	// The Gateway sends the headers with the first event.
	go func() { _, _ = io.WriteString(writer, "data: one\n\n") }()
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	first := make([]byte, len("data: one\n\n"))
	if _, err := io.ReadFull(resp.Body, first); err != nil || string(first) != "data: one\n\n" {
		t.Fatalf("an event sent before expiry did not arrive: %q, %v", first, err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, resp.Body)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the stream ended with %v, want a clean end", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived its token")
	}
}

// Verifies SPEC-0042-R02: a stream ended at expiry is counted and audited as
// token_expired.
func TestExpiredStreamOutcome(t *testing.T) {
	t.Parallel()
	h, token, _ := expiringStream(t)
	log, out := enableAudit(t, h)
	_, ended := openStream(h, token)
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived its token")
	}
	requireSeries(t, scrape(t, h), `kavrynt_gateway_mcp_requests_total{method="http_get",outcome="token_expired",route="team-a.demo",tool="none"} 1`)
	list := events(t, log, out)
	if len(list) != 1 || list[0].Outcome != "token_expired" {
		t.Fatalf("audit events = %+v, want one with outcome token_expired", list)
	}
}

// Verifies SPEC-0042-R02: a server that drops the stream before the token
// expires is still incomplete, not token_expired.
func TestUpstreamDisconnectStaysIncomplete(t *testing.T) {
	t.Parallel()
	h, token, writer := expiringStream(t)
	_, ended := openStream(h, token)
	_ = writer.CloseWithError(io.ErrUnexpectedEOF)
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end with its server")
	}
	metrics := scrape(t, h)
	requireSeries(t, metrics, `outcome="incomplete"`)
	if strings.Contains(metrics, "token_expired") {
		t.Fatalf("a dropped stream was counted as token_expired:\n%s", metrics)
	}
}
