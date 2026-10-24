package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kavrynt/kavrynt/internal/gateway/policy"
)

// Allows tools/list and event streams for anyone, and tools/call of echo on
// team-a.demo for client support-agent or, without client authentication,
// for callers with no client ID.
const testPolicy = `package kavrynt.authz

default decision := {"allow": false, "reason": "tool not allowed for client"}

decision := {"allow": true} if input.mcp_method == "tools/list"

decision := {"allow": true} if input.http_method == "GET"

decision := {"allow": true} if {
	input.mcp_method == "tools/call"
	input.tool == "echo"
	input.client_id in {"support-agent", ""}
	input.route == {"namespace": "team-a", "name": "demo"}
}
`

func enablePolicy(t *testing.T, h *Handler, rego string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "authz.rego"), []byte(rego), 0o600); err != nil {
		t.Fatal(err)
	}
	engine, err := policy.New(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h.EnablePolicy(engine)
}

func rpc(h *Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	return post(h, body, headers)
}

// Verifies SPEC-0012-R07 and SPEC-0012-R08.
func TestPolicyDeniesWithAJSONRPCError(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	enablePolicy(t, handler, testPolicy)

	if rr := rpc(handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{}}}`, nil); rr.Code != http.StatusOK || *calls != 1 {
		t.Fatalf("allowed call: %d after %d upstream calls", rr.Code, *calls)
	}
	rr := rpc(handler, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"delete_all","arguments":{"x":"secret-argument"}}}`, nil)
	if rr.Code != http.StatusForbidden || *calls != 1 {
		t.Fatalf("denied call: %d after %d upstream calls, want 403 and no call", rr.Code, *calls)
	}
	var answer struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if answer.JSONRPC != "2.0" || string(answer.ID) != "7" || answer.Error.Code != policyDeniedCode || answer.Error.Message != "denied by policy: tool not allowed for client" {
		t.Fatalf("answer = %s", rr.Body.String())
	}
	metrics := get(handler, "/metrics").Body.String()
	requireSeries(t, metrics, `kavrynt_gateway_mcp_requests_total{method="tools/call",outcome="denied",route="team-a.demo",tool="delete_all"} 1`)
	requireSeries(t, metrics, `kavrynt_gateway_policy_decisions_total{result="deny"} 1`)
	requireSeries(t, metrics, `kavrynt_gateway_policy_info{revision="`)
	if strings.Contains(metrics, "secret-argument") {
		t.Fatal("metrics contain tool arguments")
	}
}

// Verifies SPEC-0012-R09.
func TestPolicyRefusesBodiesItCannotRead(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	enablePolicy(t, handler, testPolicy)
	for name, body := range map[string]string{
		"not JSON":               `tools/call echo`,
		"batch":                  `[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]`,
		"too large":              `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"pad":"` + strings.Repeat("x", maxInspectBytes) + `"}}`,
		"tool name not a string": `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":7}}`,
		"tool name missing":      `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`,
	} {
		if rr := rpc(handler, body, nil); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, rr.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp/team-a.demo", http.NoBody)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("POST without a body: %d, want 400", rr.Code)
	}
	if *calls != 0 {
		t.Fatalf("%d unreadable requests reached the MCP server", *calls)
	}
}

// Verifies SPEC-0012-R10.
func TestPolicyRefusesHeadersThatDisagreeWithTheBody(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	enablePolicy(t, handler, testPolicy)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_all"}}`
	if rr := rpc(handler, body, map[string]string{"Mcp-Method": "tools/call", "Mcp-Name": "echo"}); rr.Code != http.StatusBadRequest {
		t.Fatalf("Mcp-Name echo with body delete_all: %d, want 400", rr.Code)
	}
	if rr := rpc(handler, body, map[string]string{"Mcp-Method": "tools/list"}); rr.Code != http.StatusBadRequest {
		t.Fatalf("Mcp-Method tools/list with body tools/call: %d, want 400", rr.Code)
	}
	ok := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo"}}`
	if rr := rpc(handler, ok, map[string]string{"Mcp-Method": "tools/call", "Mcp-Name": "echo"}); rr.Code != http.StatusOK {
		t.Fatalf("matching headers: %d", rr.Code)
	}
	if *calls != 1 {
		t.Fatalf("%d calls reached the MCP server, want only the matching one", *calls)
	}
}

// Verifies SPEC-0012-R01: without a policy, calls behave as before.
func TestNoPolicyChangesNothing(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	if rr := rpc(handler, `not json`, nil); rr.Code != http.StatusOK || *calls != 1 {
		t.Fatalf("without policy: %d after %d upstream calls", rr.Code, *calls)
	}
}

// Verifies SPEC-0012-R06: the policy sees the verified caller.
func TestPolicyDecidesOnTheVerifiedCaller(t *testing.T) {
	handler, received, mint := authHandler(t, true)
	enablePolicy(t, handler, testPolicy)
	if rr := callRoute(handler, "/mcp/team-a.demo", mint(nil)); rr.Code != http.StatusOK || len(*received) != 1 {
		t.Fatalf("support-agent: %d", rr.Code)
	}
	if rr := callRoute(handler, "/mcp/team-a.demo", mint(map[string]any{"client_id": "etl"})); rr.Code != http.StatusForbidden || len(*received) != 1 {
		t.Fatalf("etl: %d, want 403", rr.Code)
	}
}
