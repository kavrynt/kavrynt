package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	auditv1 "github.com/kavrynt/kavrynt/api/audit/v1"
	"github.com/kavrynt/kavrynt/internal/mcpmeta"
)

const callEcho = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{}}}`

// Verifies SPEC-0018-R01: headers that name another method or tool than the
// body are refused without a policy.
func TestProtocolRefusesHeadersThatDisagreeWithTheBody(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	deleteAll := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_all"}}`
	for name, c := range map[string]struct {
		body    string
		headers map[string]string
	}{
		"Mcp-Name of another tool":     {deleteAll, map[string]string{"Mcp-Method": "tools/call", "Mcp-Name": "echo"}},
		"Mcp-Method of another method": {deleteAll, map[string]string{"Mcp-Method": "tools/list"}},
		"Mcp-Name without params.name": {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`, map[string]string{"Mcp-Name": "echo"}},
	} {
		rr := rpc(handler, c.body, c.headers)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "does not match the request body") {
			t.Errorf("%s: %d %s, want 400", name, rr.Code, rr.Body.String())
		}
	}
	if *calls != 0 {
		t.Fatalf("%d mismatched requests reached the MCP server", *calls)
	}
	if rr := rpc(handler, callEcho, map[string]string{"Mcp-Method": "tools/call", "Mcp-Name": "echo"}); rr.Code != http.StatusOK || *calls != 1 {
		t.Fatalf("matching headers: %d after %d calls", rr.Code, *calls)
	}
}

// Verifies SPEC-0018-R02.
func TestProtocolRequiresMcpMethodFromItsRevision(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	for _, version := range []string{"", "2025-03-26", "2025-06-18", "2025-11-25"} {
		if rr := rpc(handler, callEcho, map[string]string{"MCP-Protocol-Version": version}); rr.Code != http.StatusOK {
			t.Errorf("version %q without Mcp-Method: %d, want it forwarded", version, rr.Code)
		}
	}
	forwarded := *calls
	rr := rpc(handler, callEcho, map[string]string{"MCP-Protocol-Version": mcpmeta.HeaderProtocolVersion})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "requires the Mcp-Method header") || *calls != forwarded {
		t.Fatalf("version %s without Mcp-Method: %d %s", mcpmeta.HeaderProtocolVersion, rr.Code, rr.Body.String())
	}
	headers := map[string]string{"MCP-Protocol-Version": mcpmeta.HeaderProtocolVersion, "Mcp-Method": "tools/call", "Mcp-Name": "echo"}
	if rr := rpc(handler, callEcho, headers); rr.Code != http.StatusOK {
		t.Fatalf("version %s with headers: %d", mcpmeta.HeaderProtocolVersion, rr.Code)
	}
	// A JSON-RPC response from the client has no method to name.
	response := `{"jsonrpc":"2.0","id":"server-1","result":{}}`
	if rr := rpc(handler, response, map[string]string{"MCP-Protocol-Version": mcpmeta.HeaderProtocolVersion}); rr.Code != http.StatusOK {
		t.Fatalf("client response without Mcp-Method: %d", rr.Code)
	}
}

// Verifies SPEC-0018-R03.
func TestProtocolRefusesBatches(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	for _, body := range []string{
		`[{"jsonrpc":"2.0","id":1,"method":"tools/list"},{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`,
		"  \n[" + strings.Repeat(" ", maxInspectBytes) + "]",
	} {
		if rr := rpc(handler, body, nil); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "batches are not supported") {
			t.Errorf("batch: %d %.80s", rr.Code, rr.Body.String())
		}
	}
	if *calls != 0 {
		t.Fatalf("%d batches reached the MCP server", *calls)
	}
}

// Verifies SPEC-0018-R04.
func TestProtocolRefusesUnknownVersions(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	for _, version := range []string{"2099-01-01", "1.0", strings.Repeat("9", 500)} {
		req := httptest.NewRequest(http.MethodGet, "/mcp/team-a.demo", nil)
		req.Header.Set("MCP-Protocol-Version", version)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		var body errorResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rr.Code != http.StatusBadRequest || !strings.Contains(body.Error, strings.Join(mcpmeta.ProtocolVersions, ", ")) || len(body.Error) > 300 {
			t.Errorf("version %.20s: %d %q, want 400 listing the supported versions", version, rr.Code, body.Error)
		}
	}
	if *calls != 0 {
		t.Fatalf("%d requests with unknown versions reached the MCP server", *calls)
	}
	for _, version := range mcpmeta.ProtocolVersions {
		if rr := rpc(handler, callEcho, map[string]string{"MCP-Protocol-Version": version, "Mcp-Method": "tools/call"}); rr.Code != http.StatusOK {
			t.Errorf("supported version %s: %d", version, rr.Code)
		}
	}
}

// Verifies SPEC-0018-R05: event streams and session ends are proxied
// without a body check, with resumption and session headers unchanged.
func TestProtocolPassesStreamsAndSessionHeaders(t *testing.T) {
	var seen []*http.Request
	handler, _ := demoHandler(t, func(req *http.Request) (*http.Response, error) {
		seen = append(seen, req)
		return okResponse(req)
	})
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(method, "/mcp/team-a.demo", strings.NewReader("[not checked]"))
		req.Header.Set("Last-Event-ID", "event-41")
		req.Header.Set("Mcp-Session-Id", "session-7")
		req.Header.Set("MCP-Protocol-Version", mcpmeta.HeaderProtocolVersion)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", method, rr.Code, rr.Body.String())
		}
	}
	if len(seen) != 2 {
		t.Fatalf("%d requests forwarded, want 2", len(seen))
	}
	for _, req := range seen {
		if req.Header.Get("Last-Event-ID") != "event-41" || req.Header.Get("Mcp-Session-Id") != "session-7" {
			t.Errorf("%s forwarded with headers %v", req.Method, req.Header)
		}
	}
}

// Verifies SPEC-0018-R06: refusals are recorded as protocol errors and
// audited as invalid; with strict checks off they are forwarded.
func TestProtocolRefusalsAreRecorded(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	w, out := enableAudit(t, handler)
	if rr := rpc(handler, callEcho, map[string]string{"Mcp-Name": "other"}); rr.Code != http.StatusBadRequest {
		t.Fatalf("mismatch: %d", rr.Code)
	}
	metrics := get(handler, "/metrics").Body.String()
	requireSeries(t, metrics, `kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="protocol_error",route="team-a.demo",tool="other"} 1`)
	list := events(t, w, out)
	if len(list) != 1 || list[0].Decision != auditv1.DecisionInvalid || list[0].Outcome != outcomeProtocolError || list[0].Status != http.StatusBadRequest {
		t.Fatalf("audit events = %+v", list)
	}

	handler.SetStrictProtocol(false)
	if rr := rpc(handler, callEcho, map[string]string{"Mcp-Name": "other"}); rr.Code != http.StatusOK || *calls != 1 {
		t.Fatalf("strict checks off: %d after %d calls, want it forwarded", rr.Code, *calls)
	}
}
