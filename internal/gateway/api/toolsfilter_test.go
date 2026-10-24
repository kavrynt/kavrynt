package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kavrynt/kavrynt/internal/gateway/policy"
)

// Lists tools for anyone; calls only search.
const searchOnlyPolicy = `package kavrynt.authz

default decision := {"allow": false}

decision := {"allow": true} if input.mcp_method == "tools/list"

decision := {"allow": true} if {
	input.mcp_method == "tools/call"
	input.tool == "search"
}
`

const listTools = `{"jsonrpc":"2.0","id":4,"method":"tools/list","params":{}}`

const twoTools = `{"jsonrpc":"2.0","id":4,"result":{"tools":[` +
	`{"name":"search","description":"Find tickets","inputSchema":{"type":"object"}},` +
	`{"name":"delete_all","description":"Delete every ticket"}],` +
	`"nextCursor":"page-2","_meta":{"x":1}}}`

func toolsHandler(t *testing.T, contentType, body string) (*Handler, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	handler, _ := demoHandler(t, func(req *http.Request) (*http.Response, error) {
		seen = append(seen, req)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{contentType}, "Content-Length": []string{"9999"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	enablePolicy(t, handler, searchOnlyPolicy)
	handler.EnableToolsListFilter()
	return handler, &seen
}

type listAnswer struct {
	ID     json.RawMessage `json:"id"`
	Result struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
		NextCursor string          `json:"nextCursor"`
		Meta       json.RawMessage `json:"_meta"`
	} `json:"result"`
}

func toolNames(t *testing.T, body []byte) []string {
	t.Helper()
	var answer listAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("not a JSON-RPC answer: %s", body)
	}
	names := []string{}
	for _, tool := range answer.Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// Verifies SPEC-0016-R01 and SPEC-0016-R03.
func TestToolsListShowsOnlyCallableTools(t *testing.T) {
	handler, seen := toolsHandler(t, "application/json", twoTools)
	rr := rpc(handler, listTools, map[string]string{"Accept-Encoding": "gzip"})
	if rr.Code != http.StatusOK {
		t.Fatalf("tools/list: %d %s", rr.Code, rr.Body.String())
	}
	var answer listAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Result.Tools) != 1 || answer.Result.Tools[0].Name != "search" {
		t.Fatalf("tools = %s, want only search", rr.Body.String())
	}
	tool := answer.Result.Tools[0]
	if string(answer.ID) != "4" || answer.Result.NextCursor != "page-2" || string(answer.Result.Meta) != `{"x":1}` ||
		tool.Description != "Find tickets" || string(tool.InputSchema) != `{"type":"object"}` {
		t.Fatalf("fields were not kept: %s", rr.Body.String())
	}
	if rr.Header().Get("Content-Length") == "9999" {
		t.Fatal("the upstream Content-Length was kept for a changed body")
	}
	if got := (*seen)[0].Header.Get("Accept-Encoding"); got != "" {
		t.Fatalf("the upstream request asked for %q; the filter needs an answer it can read", got)
	}
}

// Verifies SPEC-0016-R01: without the switch, or for other methods, answers
// pass unchanged.
func TestToolsListFilterIsOptIn(t *testing.T) {
	handler, _ := demoHandler(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, twoTools), nil
	})
	enablePolicy(t, handler, searchOnlyPolicy)
	if names := toolNames(t, rpc(handler, listTools, nil).Body.Bytes()); len(names) != 2 {
		t.Fatalf("filter off: tools = %v, want both", names)
	}

	errorAnswer := `{"jsonrpc":"2.0","id":4,"error":{"code":-32601,"message":"no tools"}}`
	filtered, _ := toolsHandler(t, "application/json", errorAnswer)
	if rr := rpc(filtered, listTools, nil); strings.TrimSpace(rr.Body.String()) != errorAnswer {
		t.Fatalf("a JSON-RPC error answer changed: %s", rr.Body.String())
	}
}

// Verifies SPEC-0016-R02: in an event stream, only the response event is
// filtered; a notification before it passes unchanged.
func TestToolsListFilterInEventStreams(t *testing.T) {
	notification := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"delete_all\":true}}\n\n"
	response := "id: 7\nevent: message\ndata: " + twoTools + "\n\n"
	handler, _ := toolsHandler(t, "text/event-stream", notification+response)
	rr := rpc(handler, listTools, nil)
	body := rr.Body.String()
	if !strings.HasPrefix(body, notification) {
		t.Fatalf("the notification changed: %q", body)
	}
	rest := strings.TrimPrefix(body, notification)
	if !strings.HasPrefix(rest, "id: 7\nevent: message\ndata: ") || !strings.HasSuffix(rest, "\n\n") {
		t.Fatalf("the response event lost its fields: %q", rest)
	}
	data := strings.TrimSuffix(strings.TrimPrefix(rest, "id: 7\nevent: message\ndata: "), "\n\n")
	if names := toolNames(t, []byte(data)); len(names) != 1 || names[0] != "search" {
		t.Fatalf("stream tools = %v, want only search", names)
	}
}

// Verifies SPEC-0016-R04: answers the filter cannot handle list no tools.
func TestToolsListFilterFailsClosed(t *testing.T) {
	for name, c := range map[string]struct{ contentType, body string }{
		"too large":       {"application/json", `{"jsonrpc":"2.0","id":4,"result":{"tools":[],"pad":"` + strings.Repeat("x", maxInspectBytes) + `"}}`},
		"not a result":    {"application/json", `{"jsonrpc":"2.0","id":4,"result":{"items":[{"name":"delete_all"}]}}`},
		"not JSON":        {"application/json", `delete_all`},
		"other type":      {"text/plain", `delete_all`},
		"event too large": {"text/event-stream", "data: {\"jsonrpc\":\"2.0\",\"id\":4,\"result\":{\"tools\":[],\"pad\":\"" + strings.Repeat("x", maxInspectBytes) + "\"}}\n\n"},
		"bad event":       {"text/event-stream", "data: {\"jsonrpc\":\"2.0\",\"id\":4,\"result\":{\"tools\":{\"name\":\"delete_all\"}}}\n\n"},
	} {
		handler, _ := toolsHandler(t, c.contentType, c.body)
		rr := rpc(handler, listTools, nil)
		body := strings.TrimPrefix(strings.TrimSpace(rr.Body.String()), "data: ")
		if strings.Contains(body, "delete_all") || len(toolNames(t, []byte(body))) != 0 || !strings.Contains(body, `"id":4`) {
			t.Errorf("%s: answer %.120q, want no tools and the request id", name, body)
		}
	}
}

// Verifies SPEC-0016-R04: evaluations that do not finish in time list no
// tools.
func TestToolsListFilterHasADeadline(t *testing.T) {
	f := &toolsFilter{
		decide: func(ctx context.Context, _ policy.Input) policy.Decision {
			<-ctx.Done()
			return policy.Decision{Allow: true}
		},
		id: json.RawMessage("4"),
	}
	answer, _, reason := f.filterJSON(context.Background(), strings.NewReader(twoTools))
	if reason == nil || len(toolNames(t, answer)) != 0 {
		t.Fatalf("slow policy: %s (%v), want no tools", answer, reason)
	}
}

// Verifies SPEC-0016-R05.
func TestFilteredToolsAreCountedAndAudited(t *testing.T) {
	handler, _ := toolsHandler(t, "application/json", twoTools)
	w, out := enableAudit(t, handler)
	rpc(handler, listTools, nil)
	requireSeries(t, get(handler, "/metrics").Body.String(), `kavrynt_gateway_policy_filtered_tools_total{route="team-a.demo"} 1`)
	list := events(t, w, out)
	if len(list) != 1 || list[0].FilteredTools != 1 || list[0].MCPMethod != "tools/list" {
		t.Fatalf("audit events = %+v", list)
	}
	if strings.Contains(out.String(), "delete_all") {
		t.Fatal("the audit event names a tool")
	}
}
