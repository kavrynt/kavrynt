package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/auth"
	"github.com/kavrynt/kavrynt/internal/gateway/model"
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

var sessionKey = []byte(strings.Repeat("k", 32))

// boundGateway is a Gateway with client authentication and session binding
// on, routing team-a.demo and team-a.other to one fake MCP server that starts
// a session as "server-session" on initialize and records every request it
// receives.
type boundGateway struct {
	*Handler
	mint func(map[string]any) string
	seen *[]*http.Request
}

func newBoundGateway(t *testing.T) *boundGateway {
	t.Helper()
	h, _, mint := authHandler(t, true)
	var seen []*http.Request
	h.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seen = append(seen, req)
		header := http.Header{"Content-Type": []string{"application/json"}}
		if body, _ := io.ReadAll(req.Body); strings.Contains(string(body), `"method":"initialize"`) {
			header.Set("Mcp-Session-Id", "server-session")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{}}`))}, nil
	})}
	h.table.Replace([]model.Route{
		{Name: "team-a.demo", Version: "0.1.0", Transport: "http", Endpoint: "http://demo.test/mcp"},
		{Name: "team-a.other", Version: "0.1.0", Transport: "http", Endpoint: "http://other.test/mcp"},
	}, time.Now())
	h.EnableSessionBinding(sessionKey)
	return &boundGateway{Handler: h, mint: mint, seen: &seen}
}

// token is a token for route whose caller is user and client.
func (g *boundGateway) token(route, user, client string) string {
	return g.mint(map[string]any{"aud": authResource + "/mcp/" + route, "sub": user, "client_id": client})
}

func (g *boundGateway) do(route, token, session, body string) *httptest.ResponseRecorder {
	return callRoute(g.Handler, "/mcp/"+route, token, func(r *http.Request) {
		r.Body = io.NopCloser(strings.NewReader(body))
		r.ContentLength = int64(len(body))
		if session != "" {
			r.Header.Set("Mcp-Session-Id", session)
		}
	})
}

// start opens a session on route for the caller and returns the session ID
// the client gets.
func (g *boundGateway) start(t *testing.T, route, token string) string {
	t.Helper()
	rr := g.do(route, token, "", initializeBody)
	session := rr.Header().Get("Mcp-Session-Id")
	if rr.Code != http.StatusOK || session == "" {
		t.Fatalf("initialize: %d, session %q, %s", rr.Code, session, rr.Body)
	}
	return session
}

// Verifies SPEC-0042-R03: the session ID a client gets is bound, and does not
// show the server's ID as it is.
func TestSessionIDIsBound(t *testing.T) {
	g := newBoundGateway(t)
	session := g.start(t, "team-a.demo", g.token("team-a.demo", "user-42", "agent-a"))
	if !strings.HasPrefix(session, "s1.") || !strings.HasSuffix(session, ".server-session") || session == "server-session" {
		t.Fatalf("session ID = %q, want s1.<tag>.server-session", session)
	}
}

// Verifies SPEC-0042-R04 and SPEC-0042-R05: another caller's session ID, with a
// valid token for the route, gets 404 and nothing reaches the server; the
// caller's own works, also on a new token, and the server gets its own ID
// without the tag.
func TestAnotherCallersSessionIsNotFound(t *testing.T) {
	g := newBoundGateway(t)
	owner := g.token("team-a.demo", "user-42", "agent-a")
	session := g.start(t, "team-a.demo", owner)
	forwarded := len(*g.seen)

	for name, token := range map[string]string{
		"another user":                 g.token("team-a.demo", "user-43", "agent-a"),
		"the same user's other client": g.token("team-a.demo", "user-42", "agent-b"),
	} {
		rr := g.do("team-a.demo", token, session, callBody)
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", name, rr.Code)
		}
	}
	// Neither an unbound ID nor the server's own ID is a session of anyone.
	for _, id := range []string{"server-session", "s1." + strings.Repeat("0", 32) + ".server-session", "s2.x.server-session"} {
		if rr := g.do("team-a.demo", owner, id, callBody); rr.Code != http.StatusNotFound {
			t.Errorf("session ID %q: status %d, want 404", id, rr.Code)
		}
	}
	if len(*g.seen) != forwarded {
		t.Fatalf("%d requests reached the server for sessions that are not the caller's", len(*g.seen)-forwarded)
	}
	requireSeries(t, scrape(t, g.Handler), `outcome="session_not_found"`)

	// The owner's token may change; the caller does not.
	renewed := g.token("team-a.demo", "user-42", "agent-a")
	if rr := g.do("team-a.demo", renewed, session, callBody); rr.Code != http.StatusOK {
		t.Fatalf("the caller's session on a new token: status %d", rr.Code)
	}
	last := (*g.seen)[len(*g.seen)-1]
	if got := last.Header.Get("Mcp-Session-Id"); got != "server-session" {
		t.Fatalf("the server got session ID %q, want its own without the tag", got)
	}
}

// Verifies SPEC-0042-R03 and SPEC-0042-R04: a session of one route is no session
// of another, though the same token audience rules let the caller reach both.
func TestSessionIDIsBoundToItsRoute(t *testing.T) {
	g := newBoundGateway(t)
	session := g.start(t, "team-a.demo", g.token("team-a.demo", "user-42", "agent-a"))
	forwarded := len(*g.seen)
	if rr := g.do("team-a.other", g.token("team-a.other", "user-42", "agent-a"), session, callBody); rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
	if len(*g.seen) != forwarded {
		t.Fatal("a request with another route's session reached the server")
	}
}

// Verifies SPEC-0042-R07: the key, and what the tag covers beyond what the
// audit event already records, appear in no response, metric, or audit event
// other than the tag itself.
func TestSessionKeyInputsAreNotExposed(t *testing.T) {
	g := newBoundGateway(t)
	log, out := enableAudit(t, g.Handler)
	token := g.token("team-a.demo", "user-42", "agent-a")
	session := g.start(t, "team-a.demo", token)
	g.do("team-a.demo", g.token("team-a.demo", "user-43", "agent-a"), session, callBody)

	rr := g.do("team-a.demo", token, session, callBody)
	events(t, log, out)
	everything := rr.Body.String() + fmt.Sprint(rr.Header()) + scrape(t, g.Handler) + out.String()
	if strings.Contains(everything, string(sessionKey)) {
		t.Fatal("the session key appears in a response, metric, or audit event")
	}
	if strings.Contains(out.String(), session) {
		t.Fatal("an audit event holds a session ID")
	}
}

// Verifies SPEC-0042-R11 at the route: a token without a caller never reaches
// a session or the server.
func TestTokenWithoutCallerGetsNoSession(t *testing.T) {
	g := newBoundGateway(t)
	token := g.mint(map[string]any{"aud": authResource + "/mcp/team-a.demo", "sub": "", "client_id": ""})
	if rr := g.do("team-a.demo", token, "", initializeBody); rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rr.Code)
	}
	if len(*g.seen) != 0 {
		t.Fatal("a request without a caller reached the server")
	}
}
