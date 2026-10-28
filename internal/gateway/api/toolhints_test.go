package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/model"
)

// Denies everything with the tool fields of the input as the reason.
const echoToolFieldsPolicy = `package kavrynt.authz

decision := {"allow": false, "reason": sprintf("%s %v", [json.marshal(input.tool_annotations), input.tool_discovered])}
`

// withDiscoveredTools gives the demo route the hints of its last discovery.
func withDiscoveredTools(handler *Handler, tools map[string]map[string]bool) {
	handler.table.Replace([]model.Route{{Name: "team-a.demo", Version: "0.1.0", Transport: "http", Endpoint: "http://demo.test/mcp", Tools: tools}}, time.Now())
}

// Verifies SPEC-0028-R03, SPEC-0028-R04, SPEC-0028-R05, and SPEC-0028-R07.
func TestPolicyInputHasTheRecordedToolHints(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	enablePolicy(t, handler, echoToolFieldsPolicy)
	withDiscoveredTools(handler, map[string]map[string]bool{"search": {"readOnlyHint": true}, "bare": {}})

	cases := []struct {
		name, body, want string
	}{
		{"discovered tool", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{}}}`, `{"readOnlyHint":true} true`},
		{"discovered tool without hints", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"bare"}}`, `{} true`},
		{"tool added since discovery", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"new_tool"}}`, `{} false`},
		{"hints sent by the caller", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"bare","annotations":{"readOnlyHint":true},"_meta":{"annotations":{"readOnlyHint":true}}}}`, `{} true`},
		{"another method", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, `{} false`},
	}
	for _, tc := range cases {
		rr := rpc(handler, tc.body, nil)
		var answer struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &answer)
		if rr.Code != http.StatusForbidden || answer.Error.Message != "denied by policy: "+tc.want {
			t.Errorf("%s: %d %s, want the input fields %s", tc.name, rr.Code, rr.Body.String(), tc.want)
		}
	}
	if *calls != 0 {
		t.Fatalf("%d upstream calls, want none", *calls)
	}
}

// Lists tools for anyone; calls only discovered tools that report
// readOnlyHint.
const readOnlyHintPolicy = `package kavrynt.authz

default decision := {"allow": false}

decision := {"allow": true} if input.mcp_method == "tools/list"

decision := {"allow": true} if {
	input.mcp_method == "tools/call"
	input.tool_discovered
	input.tool_annotations.readOnlyHint == true
}
`

// Verifies SPEC-0028-R06: the filter decides on the recorded hints, not on
// the ones in the tools/list answer.
func TestToolsListFilterUsesRecordedHints(t *testing.T) {
	answer := `{"jsonrpc":"2.0","id":4,"result":{"tools":[` +
		`{"name":"search","annotations":{"readOnlyHint":true}},` +
		`{"name":"delete_all","annotations":{"readOnlyHint":true}},` +
		`{"name":"new_tool","annotations":{"readOnlyHint":true}}]}}`
	handler, _ := demoHandler(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(answer))}, nil
	})
	enablePolicy(t, handler, readOnlyHintPolicy)
	handler.EnableToolsListFilter()
	withDiscoveredTools(handler, map[string]map[string]bool{"search": {"readOnlyHint": true}, "delete_all": {"destructiveHint": true}})

	rr := rpc(handler, listTools, nil)
	if names := toolNames(t, rr.Body.Bytes()); len(names) != 1 || names[0] != "search" {
		t.Fatalf("tools = %v, want only search", names)
	}
}
