package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	auditv1 "github.com/kavrynt/kavrynt/api/audit/v1"
)

// denyDeleteAll allows everything but the tool delete_all, the kind of
// rule a policy that lists what to refuse is made of.
const denyDeleteAll = `package kavrynt.authz

default decision := {"allow": true}

decision := {"allow": false, "reason": "delete_all is not allowed"} if input.tool == "delete_all"
`

const deleteAllCall = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_all","arguments":{}}}`

// recordingHandler is demoHandler that keeps the requests the MCP server
// received.
func recordingHandler(t *testing.T, respond func(*http.Request) (*http.Response, error)) (*Handler, *[]*http.Request) {
	t.Helper()
	var received []*http.Request
	handler, _ := demoHandler(t, func(req *http.Request) (*http.Response, error) {
		received = append(received, req)
		return respond(req)
	})
	return handler, &received
}

// Verifies SPEC-0044-R01: a body on GET or DELETE is refused without
// reading it, whatever the request says about its method.
func TestBodiesOnGetAndDeleteAreRefused(t *testing.T) {
	handler, received := recordingHandler(t, okResponse)
	enablePolicy(t, handler, denyDeleteAll)
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		for name, body := range map[string]io.Reader{
			"with Content-Length":    strings.NewReader(deleteAllCall),
			"without Content-Length": io.MultiReader(strings.NewReader(deleteAllCall)),
		} {
			req := httptest.NewRequest(method, "/mcp/team-a.demo", body)
			if name == "without Content-Length" {
				req.ContentLength = -1
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-HTTP-Method-Override", http.MethodPost)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest || rr.Header().Get("Connection") != "close" {
				t.Errorf("%s %s: %d, Connection %q; want 400 and a closed connection", method, name, rr.Code, rr.Header().Get("Connection"))
			}
		}
		// Without a body it is an event stream or a session's end.
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(method, "/mcp/team-a.demo", nil))
		if rr.Code != http.StatusOK {
			t.Errorf("%s without a body: %d, want 200", method, rr.Code)
		}
	}
	if len(*received) != 2 {
		t.Fatalf("the MCP server received %d requests, want only the 2 without a body", len(*received))
	}
}

// Verifies SPEC-0044-R02: a POST that is not JSON never reaches the server,
// and charset parameters are fine.
func TestPostsMustBeJSON(t *testing.T) {
	handler, received := recordingHandler(t, okResponse)
	for name, contentTypes := range map[string][]string{
		"none":       nil,
		"form":       {"application/x-www-form-urlencoded"},
		"multipart":  {"multipart/form-data; boundary=x"},
		"text":       {"text/plain"},
		"json-ish":   {"application/json-patch+json"},
		"twice":      {"application/json", "text/plain"},
		"unreadable": {"application/json; charset"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/mcp/team-a.demo", strings.NewReader(deleteAllCall))
		for _, value := range contentTypes {
			req.Header.Add("Content-Type", value)
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %s: %d, want 415", name, rr.Code)
		}
	}
	if len(*received) != 0 {
		t.Fatalf("the MCP server received %d requests that are not JSON", len(*received))
	}
	for _, contentType := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON"} {
		req := httptest.NewRequest(http.MethodPost, "/mcp/team-a.demo", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", contentType)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("Content-Type %q: %d, want 200", contentType, rr.Code)
		}
	}
}

// Verifies SPEC-0044-R03: before authentication, the Gateway reads none of
// a body it refuses, and answers registered and unregistered routes alike.
func TestShapeIsCheckedBeforeAuthenticationWithoutReading(t *testing.T) {
	handler, received, _ := authHandler(t, true)
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, route := range []string{"team-a.demo", "team-z.unknown"} {
		answers := map[string]string{}
		for name, request := range map[string]string{
			// The body never comes: an answer proves nothing waited for it.
			"form":        "POST /mcp/%s HTTP/1.1\r\nHost: gateway.test\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 1048576\r\n\r\naccess_token=",
			"GET body":    "GET /mcp/%s HTTP/1.1\r\nHost: gateway.test\r\nContent-Length: 1048576\r\n\r\n{",
			"chunked GET": "GET /mcp/%s HTTP/1.1\r\nHost: gateway.test\r\nTransfer-Encoding: chunked\r\n\r\n10\r\n{",
		} {
			conn, err := net.Dial("tcp", server.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprintf(conn, request, route); err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("%s %s: no answer without the body: %v", route, name, err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			_ = conn.Close()
			if resp.StatusCode == http.StatusUnauthorized || !resp.Close {
				t.Errorf("%s %s: %d, close %t; want refused before authentication, closing", route, name, resp.StatusCode, resp.Close)
			}
			answers[name] = fmt.Sprintf("%d %s", resp.StatusCode, body)
		}
		if answers["form"][:3] != "415" || answers["GET body"][:3] != "400" || answers["chunked GET"][:3] != "400" {
			t.Errorf("%s: %v", route, answers)
		}
	}
	if len(*received) != 0 {
		t.Fatal("a refused request reached the MCP server")
	}
}

// Verifies SPEC-0044-R04: method override headers never reach the server.
func TestMethodOverrideHeadersAreNotForwarded(t *testing.T) {
	handler, received := recordingHandler(t, okResponse)
	req := newRequest(http.MethodGet, "/mcp/team-a.demo", nil)
	for _, name := range methodOverrideHeaders {
		req.Header.Set(name, http.MethodPost)
	}
	req.Header.Add("x-http-method-override", http.MethodDelete)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || len(*received) != 1 {
		t.Fatalf("GET: %d after %d upstream requests", rr.Code, len(*received))
	}
	for _, name := range methodOverrideHeaders {
		if value := (*received)[0].Header.Values(name); len(value) != 0 {
			t.Errorf("the MCP server received %s: %q", name, value)
		}
	}
}

// Verifies SPEC-0044-R05 and SPEC-0044-R06: with a policy, a method or tool
// name a rule cannot match is refused, with the protocol checks on or off;
// without a policy, it is refused unless they are off.
func TestNamesARuleCannotMatchAreRefused(t *testing.T) {
	bad := map[string]string{
		"tool with a space":    `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_all ","arguments":{}}}`,
		"tool with a NUL":      `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_all\u0000","arguments":{}}}`,
		"tool with a newline":  `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_all\n","arguments":{}}}`,
		"empty tool":           `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"","arguments":{}}}`,
		"long tool":            `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + strings.Repeat("a", 129) + `","arguments":{}}}`,
		"method with a space":  `{"jsonrpc":"2.0","id":1,"method":"tools/call ","params":{"name":"delete_all","arguments":{}}}`,
		"method with unicode":  `{"jsonrpc":"2.0","id":1,"method":"tools/cаll","params":{"name":"delete_all","arguments":{}}}`,
		"method with a slash ": `{"jsonrpc":"2.0","id":1,"method":"tools\\/call\t","params":{"name":"delete_all","arguments":{}}}`,
		"empty method":         `{"jsonrpc":"2.0","id":1,"method":"","params":{}}`,
		"null method":          `{"jsonrpc":"2.0","id":1,"method":null,"params":{}}`,
	}
	for _, withPolicy := range []bool{true, false} {
		for _, strict := range []bool{true, false} {
			handler, received := recordingHandler(t, okResponse)
			if withPolicy {
				enablePolicy(t, handler, denyDeleteAll)
			}
			handler.SetStrictProtocol(strict)
			refused := withPolicy || strict
			for name, body := range bad {
				rr := post(handler, body, nil)
				if !refused {
					if rr.Code != http.StatusOK {
						t.Errorf("policy %t, strict %t, %s: %d, want forwarded", withPolicy, strict, name, rr.Code)
					}
					continue
				}
				var answer struct {
					ID    json.RawMessage `json:"id"`
					Error struct {
						Code    int    `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if rr.Code != http.StatusBadRequest || json.Unmarshal(rr.Body.Bytes(), &answer) != nil || answer.Error.Code != jsonRPCInvalidRequest || string(answer.ID) != "1" {
					t.Errorf("policy %t, strict %t, %s: %d %s, want 400 with a JSON-RPC error", withPolicy, strict, name, rr.Code, rr.Body.String())
				}
				if strings.Contains(answer.Error.Message, "delete_all") {
					t.Errorf("%s: the refusal quotes the name: %s", name, answer.Error.Message)
				}
			}
			if refused && len(*received) != 0 {
				t.Errorf("policy %t, strict %t: %d refused calls reached the MCP server", withPolicy, strict, len(*received))
			}
			// Names MCP uses pass, and a client's JSON-RPC response has none.
			for _, body := range []string{
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"files.read_v2/latest-1","arguments":{}}}`,
				`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
				`{"jsonrpc":"2.0","id":3,"result":{}}`,
			} {
				if rr := post(handler, body, nil); rr.Code == http.StatusBadRequest {
					t.Errorf("policy %t, strict %t: %s refused: %s", withPolicy, strict, body, rr.Body.String())
				}
			}
		}
	}
}

// Verifies SPEC-0044-R07 and SPEC-0044-R10: every redirect status becomes
// 502 without the server's Location or body, counted as an upstream error.
func TestRedirectsAreNotPassedOn(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusNotModified, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		h := trafficHandler(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: status,
				Header:     http.Header{"Location": {"/mcp/team-b.payments"}, "Content-Type": {"text/html"}},
				Body:       io.NopCloser(strings.NewReader("<a href=/mcp/team-b.payments>moved</a>")),
			}, nil
		})
		rr := post(h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
		if rr.Code != http.StatusBadGateway || rr.Header().Get("Location") != "" || strings.Contains(rr.Body.String(), "team-b") {
			t.Errorf("%d: %d, Location %q, body %s; want 502 without them", status, rr.Code, rr.Header().Get("Location"), rr.Body.String())
		}
		requireSeries(t, scrape(t, h), `kavrynt_gateway_mcp_requests_total{method="tools/list",outcome="upstream_error",route="team-a.demo",tool="none"} 1`)
	}
}

// Verifies SPEC-0044-R08: of a server's response headers, clients get only
// those MCP needs.
func TestOnlyMCPResponseHeadersReachClients(t *testing.T) {
	refused := map[string]string{
		"WWW-Authenticate":                 `Bearer resource_metadata="https://evil.example/.well-known/oauth-protected-resource"`,
		"Strict-Transport-Security":        "max-age=0",
		"Clear-Site-Data":                  `"*"`,
		"Refresh":                          "0; url=https://evil.example/",
		"Set-Cookie":                       "session=x",
		"Access-Control-Allow-Origin":      "*",
		"Access-Control-Allow-Credentials": "true",
		"Content-Security-Policy":          "default-src *",
		"X-Request-Id":                     "forged",
		"Link":                             "<https://evil.example/>; rel=preload",
		"X-Custom":                         "1",
		"Server":                           "upstream/1.0",
	}
	allowed := map[string]string{
		"Content-Type":         "application/json",
		"Mcp-Session-Id":       "session-7",
		"MCP-Protocol-Version": "2025-06-18",
		"Cache-Control":        "no-store",
		"Retry-After":          "5",
		"Vary":                 "Accept",
		"Allow":                "GET, POST, DELETE",
	}
	h := trafficHandler(t, func(*http.Request) (*http.Response, error) {
		header := http.Header{}
		for name, value := range refused {
			header.Set(name, value)
		}
		for name, value := range allowed {
			header.Set(name, value)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{}}`))}, nil
	})
	rr := post(h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	for name := range refused {
		if values := rr.Header().Values(name); len(values) != 0 && !(name == "X-Request-Id" && values[0] != "forged") && !(name == "Content-Security-Policy" && values[0] == "sandbox") {
			t.Errorf("client received upstream %s: %q", name, values)
		}
	}
	for name, value := range allowed {
		if got := rr.Header().Get(name); got != value {
			t.Errorf("client got %s %q, want %q", name, got, value)
		}
	}
	// The Gateway's own headers stay its own.
	if rr.Header().Get("Content-Security-Policy") != "sandbox" || len(rr.Header().Values("Content-Security-Policy")) != 1 {
		t.Errorf("Content-Security-Policy = %q, want only the Gateway's sandbox", rr.Header().Values("Content-Security-Policy"))
	}
}

// Verifies SPEC-0044-R09: configured names are added; names that act on
// the Gateway's origin or the client's sign-in are refused, in any case.
func TestAllowedResponseHeaders(t *testing.T) {
	h := trafficHandler(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{
			"X-Custom": {"1"}, "X-Other": {"2"}, "X-Scoped": {"3"}, "Content-Type": {"application/json"},
			"Connection": {"close, X-Scoped"}, "Keep-Alive": {"timeout=1"}, "Upgrade": {"h2c"},
		}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	if err := h.AllowResponseHeaders("x-custom", " ", "Connection", "Keep-Alive", "Upgrade", "X-Scoped"); err != nil {
		t.Fatal(err)
	}
	rr := post(h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	if rr.Header().Get("X-Custom") != "1" || rr.Header().Get("X-Other") != "" {
		t.Fatalf("X-Custom %q, X-Other %q; want only the allowed one", rr.Header().Get("X-Custom"), rr.Header().Get("X-Other"))
	}
	// Allowing them changes nothing for headers of the connection.
	for _, name := range []string{"Connection", "Keep-Alive", "Upgrade", "X-Scoped"} {
		if value := rr.Header().Get(name); value != "" {
			t.Errorf("client received upstream %s: %q", name, value)
		}
	}
	for _, name := range []string{
		"Set-Cookie", "www-authenticate", "Proxy-Authenticate", "location", "Refresh",
		"Strict-Transport-Security", "CLEAR-SITE-DATA", "Content-Security-Policy",
		"Content-Security-Policy-Report-Only", "X-Content-Type-Options", "Alt-Svc", "Server",
		"X-Powered-By", "X-Request-Id", "Access-Control-Allow-Origin", "access-control-expose-headers",
		"Bad Name", "X-Fine,Set-Cookie",
	} {
		before := len(h.allowResponse)
		if err := h.AllowResponseHeaders("X-Fine", name); err == nil || len(h.allowResponse) != before {
			t.Errorf("%q: allowed (%v), want refused and nothing changed", name, err)
		}
	}
}

// Verifies SPEC-0044-R10: refusals of R01, R02, and R05 are protocol errors
// with an invalid audit decision that names the check.
func TestRefusalsAreRecordedAsProtocolErrors(t *testing.T) {
	h := trafficHandler(t, okResponse)
	w, out := enableAudit(t, h)
	get := httptest.NewRequest(http.MethodGet, "/mcp/team-a.demo", strings.NewReader("{}"))
	h.ServeHTTP(httptest.NewRecorder(), get)
	form := httptest.NewRequest(http.MethodPost, "/mcp/team-a.demo", strings.NewReader("a=1"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(httptest.NewRecorder(), form)
	post(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_all ","arguments":{}}}`, nil)

	metrics := scrape(t, h)
	for _, series := range []string{
		`kavrynt_gateway_mcp_requests_total{method="http_get",outcome="protocol_error",route="team-a.demo",tool="none"} 1`,
		`kavrynt_gateway_mcp_requests_total{method="unknown",outcome="protocol_error",route="team-a.demo",tool="none"} 1`,
		`kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="protocol_error",route="team-a.demo",tool="invalid"} 1`,
	} {
		requireSeries(t, metrics, series)
	}
	list := events(t, w, out)
	if len(list) != 3 {
		t.Fatalf("%d audit events, want 3", len(list))
	}
	for i, want := range []string{"must not have a body", "Content-Type: application/json", "tool name"} {
		if e := list[i]; e.Decision != auditv1.DecisionInvalid || !strings.Contains(e.Reason, want) || e.Outcome != outcomeProtocolError {
			t.Errorf("event %d: decision %q, reason %q, outcome %q; want invalid, naming %q", i, e.Decision, e.Reason, e.Outcome, want)
		}
	}
}
