package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/mcpmeta"
)

const endpoint = "http://demo.default.svc:8080/mcp"

// fakeServer is an in-memory MCP server reached through a RoundTripper, so
// tests need no network.
type fakeServer struct {
	mu        sync.Mutex
	sessionID string
	sse       bool
	toolsCap  bool
	pages     [][]any
	handle    func(method string, w http.ResponseWriter) bool
	// serverInfo is sent in the initialize result when set.
	serverInfo any
	requests   []*http.Request
	methods    []string
}

func newFakeServer(tools ...any) *fakeServer {
	return &fakeServer{toolsCap: true, pages: [][]any{tools}}
}

func (f *fakeServer) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	f.ServeHTTP(rec, r)
	resp := rec.Result()
	resp.Request = r
	return resp, nil
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r)
	if r.Method == http.MethodDelete {
		f.methods = append(f.methods, "DELETE")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Cursor string `json:"cursor"`
		} `json:"params"`
	}
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &req)
	f.methods = append(f.methods, req.Method)
	if f.handle != nil && f.handle(req.Method, w) {
		return
	}

	var result any
	switch req.Method {
	case "initialize":
		capabilities := map[string]any{}
		if f.toolsCap {
			capabilities["tools"] = map[string]any{}
		}
		initialized := map[string]any{"protocolVersion": "2025-03-26", "capabilities": capabilities}
		if f.serverInfo != nil {
			initialized["serverInfo"] = f.serverInfo
		}
		result = initialized
		if f.sessionID != "" {
			w.Header().Set("Mcp-Session-Id", f.sessionID)
		}
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
		return
	case "tools/list":
		page := 0
		if req.Params.Cursor != "" {
			_, _ = fmt.Sscanf(req.Params.Cursor, "page-%d", &page)
		}
		listed := map[string]any{"tools": f.pages[page]}
		if page+1 < len(f.pages) {
			listed["nextCursor"] = fmt.Sprintf("page-%d", page+1)
		}
		result = listed
	}
	message, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	if f.sse {
		w.Header().Set("Content-Type", "text/event-stream")
		// A server notification before the response must be skipped.
		fmt.Fprint(w, ": keep-alive\n\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\"}\n\n")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", message)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(message)
}

func tool(name string) map[string]any {
	return map[string]any{
		"name":        name,
		"description": "Echo a message.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}}},
	}
}

func listTools(t *testing.T, server *fakeServer) (Result, error) {
	t.Helper()
	return New("test", server).ListTools(context.Background(), endpoint, netip.AddrPort{}, nil)
}

func TestListToolsOverJSON(t *testing.T) {
	server := newFakeServer(tool("echo"), tool("search"))
	server.sessionID = "session-1"

	result, err := listTools(t, server)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 2 || result.Tools[0].Name != "echo" || result.Tools[1].Name != "search" {
		t.Fatalf("tools = %+v", result.Tools)
	}
	if got := string(result.Tools[0].InputSchema.Raw); got != `{"properties":{"message":{"type":"string"}},"type":"object"}` {
		t.Fatalf("schema = %s", got)
	}
	if result.Summary() != "Discovered 2 tools" {
		t.Fatalf("summary = %q", result.Summary())
	}

	want := []string{"initialize", "notifications/initialized", "tools/list", "DELETE"}
	if strings.Join(server.methods, ",") != strings.Join(want, ",") {
		t.Fatalf("methods = %v, want %v", server.methods, want)
	}
	if got := server.requests[0].Header.Get("Mcp-Session-Id"); got != "" {
		t.Fatalf("initialize sent session ID %q", got)
	}
	for _, r := range server.requests[1:] {
		if r.Header.Get("Mcp-Session-Id") != "session-1" {
			t.Fatalf("%s %s missing session ID", r.Method, r.URL)
		}
		if r.Header.Get("MCP-Protocol-Version") != "2025-03-26" {
			t.Fatalf("protocol version = %q, want the negotiated version", r.Header.Get("MCP-Protocol-Version"))
		}
		if r.Header.Get("Authorization") != "" {
			t.Fatal("discovery must not send credentials")
		}
	}
}

func TestListToolsOverEventStream(t *testing.T) {
	server := newFakeServer(tool("echo"))
	server.sse = true

	result, err := listTools(t, server)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", result.Tools)
	}
	for _, method := range server.methods {
		if method == "DELETE" {
			t.Fatal("DELETE sent without a session ID")
		}
	}
}

func TestListToolsFollowsCursorsUpToMaxPages(t *testing.T) {
	server := newFakeServer()
	server.pages = nil
	for i := 0; i < MaxPages+2; i++ {
		server.pages = append(server.pages, []any{tool(fmt.Sprintf("tool-%d", i))})
	}

	result, err := listTools(t, server)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != MaxPages || !result.Truncated {
		t.Fatalf("got %d tools, truncated %v; want %d, true", len(result.Tools), result.Truncated, MaxPages)
	}
}

func TestListToolsWithoutToolsCapability(t *testing.T) {
	server := newFakeServer()
	server.toolsCap = false

	result, err := listTools(t, server)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoToolsCapability || len(result.Tools) != 0 {
		t.Fatalf("result = %+v", result)
	}
	for _, method := range server.methods {
		if method == "tools/list" {
			t.Fatal("tools/list called without the tools capability")
		}
	}
}

func TestListToolsBoundsUntrustedMetadata(t *testing.T) {
	longDescription := strings.Repeat("é", MaxDescriptionLength+10)
	bigSchema := map[string]any{"type": "object", "description": strings.Repeat("x", MaxSchemaBytes)}
	server := newFakeServer(
		map[string]any{"name": "ok", "description": longDescription, "inputSchema": bigSchema},
		map[string]any{"name": "array-schema", "inputSchema": []any{"not", "an", "object"}},
		map[string]any{"name": "bad name!"},
		map[string]any{"name": strings.Repeat("a", mcpmeta.MaxToolNameLength+1)},
		map[string]any{"name": 42},
		map[string]any{"name": "ok"},
	)

	result, err := listTools(t, server)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 2 || result.Skipped != 4 {
		t.Fatalf("tools = %d, skipped = %d; want 2, 4", len(result.Tools), result.Skipped)
	}
	first := result.Tools[0]
	if utf8.RuneCountInString(first.Description) != MaxDescriptionLength || !utf8.ValidString(first.Description) {
		t.Fatalf("description has %d characters, want %d", utf8.RuneCountInString(first.Description), MaxDescriptionLength)
	}
	if first.InputSchema != nil {
		t.Fatal("schema over MaxSchemaBytes was kept")
	}
	if result.Tools[1].InputSchema != nil {
		t.Fatal("non-object schema was kept")
	}
}

// Text a server reports reaches terminals through kubectl describe, so
// control characters other than newline and tab are dropped from it.
func TestListToolsDropsControlCharacters(t *testing.T) {
	escape := "\x1b[2J\x1b]0;owned\x07\r"
	clean := map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string", "description": "query\n\tterm"}}}
	server := newFakeServer(
		map[string]any{"name": "evil", "description": "safe" + escape + "\nnext\ttab\u0085",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"a" + escape + "b": map[string]any{"description": escape + "ok"}}}},
		map[string]any{"name": "clean", "description": "plain", "inputSchema": clean},
	)
	result, err := listTools(t, server)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(result.Tools))
	}
	evil := result.Tools[0]
	if want := "safe[2J]0;owned\nnext\ttab"; evil.Description != want {
		t.Fatalf("description = %q, want %q", evil.Description, want)
	}
	if evil.InputSchema == nil || string(evil.InputSchema.Raw) != `{"properties":{"a[2J]0;ownedb":{"description":"[2J]0;ownedok"}},"type":"object"}` {
		t.Fatalf("schema = %v", evil.InputSchema)
	}
	want, err := json.Marshal(clean)
	if err != nil {
		t.Fatal(err)
	}
	if canonical, err := CanonicalJSON(want); err != nil || string(result.Tools[1].InputSchema.Raw) != string(canonical) {
		t.Fatalf("a clean schema changed: %s, want %s", result.Tools[1].InputSchema.Raw, canonical)
	}
}

func TestListToolsCapsToolCountAndStatusSize(t *testing.T) {
	var tools []any
	for i := 0; i < mcpmeta.MaxToolsPerServer+5; i++ {
		tools = append(tools, tool(fmt.Sprintf("tool-%d", i)))
	}
	result, err := listTools(t, newFakeServer(tools...))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != mcpmeta.MaxToolsPerServer || !result.Truncated {
		t.Fatalf("got %d tools, truncated %v", len(result.Tools), result.Truncated)
	}

	schema := map[string]any{"type": "object", "description": strings.Repeat("x", 4<<10)}
	tools = nil
	for i := 0; i < 100; i++ {
		tools = append(tools, map[string]any{"name": fmt.Sprintf("tool-%d", i), "inputSchema": schema})
	}
	result, err = listTools(t, newFakeServer(tools...))
	if err != nil {
		t.Fatal(err)
	}
	if !result.SchemasDropped || len(result.Tools) != 100 || statusSize(result.Tools) > MaxStatusBytes {
		t.Fatalf("schemasDropped %v, tools %d, size %d", result.SchemasDropped, len(result.Tools), statusSize(result.Tools))
	}
}

func TestListToolsErrorsNeverEchoServerText(t *testing.T) {
	tests := []struct {
		name   string
		handle func(method string, w http.ResponseWriter) bool
		want   string
	}{
		{
			name: "HTTP error",
			handle: func(method string, w http.ResponseWriter) bool {
				http.Error(w, "upstream-secret", http.StatusUnauthorized)
				return true
			},
			want: "initialize returned HTTP 401; the server needs a credential: set spec.credential for a stored key, or spec.tokenExchange for the caller's own token",
		},
		{
			name: "redirect is not followed",
			handle: func(method string, w http.ResponseWriter) bool {
				w.Header().Set("Location", "http://169.254.169.254/upstream-secret")
				w.WriteHeader(http.StatusFound)
				return true
			},
			want: "initialize returned HTTP 302",
		},
		{
			name: "JSON-RPC error",
			handle: func(method string, w http.ResponseWriter) bool {
				if method != "tools/list" {
					return false
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"error":{"code":-32001,"message":"upstream-secret"}}`)
				return true
			},
			want: "tools/list returned JSON-RPC error -32001",
		},
		{
			name: "oversized response",
			handle: func(method string, w http.ResponseWriter) bool {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"pad":"%s"}}`, strings.Repeat("x", MaxResponseBytes))
				return true
			},
			want: "initialize: response exceeds 1 MiB",
		},
		{
			name: "unsupported content type",
			handle: func(method string, w http.ResponseWriter) bool {
				w.Header().Set("Content-Type", "text/html; upstream-secret")
				fmt.Fprint(w, "<html>")
				return true
			},
			want: "initialize: the answer is neither application/json nor text/event-stream; the endpoint does not look like an MCP Streamable HTTP endpoint",
		},
		{
			name: "invalid session ID",
			handle: func(method string, w http.ResponseWriter) bool {
				w.Header().Set("Mcp-Session-Id", "bad id\x01")
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
				return true
			},
			want: "initialize returned an invalid session ID",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newFakeServer(tool("echo"))
			server.handle = tt.handle
			_, err := listTools(t, server)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "upstream-secret") {
				t.Fatalf("error echoes server text: %v", err)
			}
			if len(server.requests) > 1 && tt.want == "initialize returned HTTP 302" {
				t.Fatal("redirect was followed")
			}
		})
	}
}

func TestCanonicalJSON(t *testing.T) {
	got, err := CanonicalJSON([]byte(` { "type" : "object", "maximum": 10000000000000000000001, "a": [1, 2.50] } `))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":[1,2.50],"maximum":10000000000000000000001,"type":"object"}`; string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if _, err := CanonicalJSON([]byte(`{} {}`)); err == nil {
		t.Fatal("trailing data accepted")
	}
}

// Verifies SPEC-0004-R01.
func TestListToolsRecordsBoundedServerInfo(t *testing.T) {
	long := strings.Repeat("v", MaxServerInfoLength+1)
	exact := strings.Repeat("é", MaxServerInfoLength)
	cases := []struct {
		name       string
		serverInfo any
		toolsCap   bool
		want       *kavryntv1alpha1.MCPServerInfo
	}{
		{"name and version", map[string]any{"name": "kavrynt-demo-incidents", "version": "0.2.0"}, true, &kavryntv1alpha1.MCPServerInfo{Name: "kavrynt-demo-incidents", Version: "0.2.0"}},
		{"without the tools capability", map[string]any{"name": "notes", "version": "1.0.0"}, false, &kavryntv1alpha1.MCPServerInfo{Name: "notes", Version: "1.0.0"}},
		{"128 characters", map[string]any{"name": "x", "version": exact}, true, &kavryntv1alpha1.MCPServerInfo{Name: "x", Version: exact}},
		{"too long", map[string]any{"name": "x", "version": long}, true, &kavryntv1alpha1.MCPServerInfo{Name: "x"}},
		{"control characters", map[string]any{"name": "evil\x1b[2Jname", "version": "1.0\n"}, true, nil},
		{"blank", map[string]any{"name": "  ", "version": ""}, true, nil},
		{"not strings", map[string]any{"name": 42, "version": map[string]any{"major": 1}}, true, nil},
		{"missing", nil, true, nil},
		{"wrong type", "server", true, nil},
	}
	for _, c := range cases {
		server := newFakeServer(tool("echo"))
		server.toolsCap = c.toolsCap
		server.serverInfo = c.serverInfo
		result, err := listTools(t, server)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if (result.ServerInfo == nil) != (c.want == nil) || (c.want != nil && *result.ServerInfo != *c.want) {
			t.Fatalf("%s: serverInfo = %+v, want %+v", c.name, result.ServerInfo, c.want)
		}
	}
}

// Verifies SPEC-0011-R08: the stored credential goes with every discovery
// request, including the session close.
func TestListToolsSendsTheCredentialHeader(t *testing.T) {
	server := newFakeServer(tool("echo"))
	server.sessionID = "session-1"
	header := http.Header{}
	header.Set("X-API-Key", "stored-secret")
	if _, err := New("test", server).ListTools(context.Background(), endpoint, netip.AddrPort{}, header); err != nil {
		t.Fatal(err)
	}
	if len(server.requests) < 3 {
		t.Fatalf("only %d requests", len(server.requests))
	}
	for i, req := range server.requests {
		if got := req.Header.Values("X-API-Key"); len(got) != 1 || got[0] != "stored-secret" {
			t.Fatalf("request %d (%s) X-API-Key = %q", i, server.methods[i], got)
		}
	}
}

// Verifies SPEC-0028-R01: only the four hints, only as JSON booleans, and
// only under their exact names, are recorded; a malformed annotations value
// keeps the tool.
func TestListToolsRecordsBehaviorHints(t *testing.T) {
	server := newFakeServer(
		map[string]any{"name": "search", "annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false, "title": "Search"}},
		map[string]any{"name": "delete_all", "annotations": map[string]any{"destructiveHint": true, "idempotentHint": true}},
		map[string]any{"name": "odd", "annotations": map[string]any{"readOnlyHint": "yes", "ReadOnlyHint": true, "destructiveHint": nil, "dangerous": true}},
		map[string]any{"name": "bare"},
		map[string]any{"name": "array", "annotations": []any{true}},
	)
	result, err := listTools(t, server)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]bool{
		"search":     {"readOnlyHint": true, "openWorldHint": false},
		"delete_all": {"destructiveHint": true, "idempotentHint": true},
		"odd":        {},
		"bare":       {},
		"array":      {},
	}
	if len(result.Tools) != len(want) {
		t.Fatalf("tools = %+v, want %d", result.Tools, len(want))
	}
	for _, tool := range result.Tools {
		if got := tool.Annotations.Hints(); !reflect.DeepEqual(got, want[tool.Name]) {
			t.Errorf("%s hints = %v, want %v", tool.Name, got, want[tool.Name])
		}
		if len(want[tool.Name]) == 0 && tool.Annotations != nil {
			t.Errorf("%s records empty annotations %+v; absent hints must be absent", tool.Name, tool.Annotations)
		}
	}
}

// Verifies SPEC-0028-R02: tools that fit MaxStatusBytes without their hints
// are truncated once the hints count.
func TestListToolsCountsAnnotationsTowardStatusSize(t *testing.T) {
	description := strings.Repeat("é", 630) // 1260 bytes
	hints := map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
	var tools []any
	for i := 0; i < mcpmeta.MaxToolsPerServer; i++ {
		tools = append(tools, map[string]any{"name": fmt.Sprintf("tool-%03d", i), "description": description, "annotations": hints})
	}
	if bare := mcpmeta.MaxToolsPerServer * (len("tool-000") + len(description)); bare > MaxStatusBytes {
		t.Fatalf("the tools take %d bytes without hints; the test needs them to fit %d", bare, MaxStatusBytes)
	}
	result, err := listTools(t, newFakeServer(tools...))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || len(result.Tools) == mcpmeta.MaxToolsPerServer || statusSize(result.Tools) > MaxStatusBytes {
		t.Fatalf("truncated %v, tools %d, size %d; the hints must count toward %d", result.Truncated, len(result.Tools), statusSize(result.Tools), MaxStatusBytes)
	}
	if result.Tools[0].Annotations == nil {
		t.Fatal("the kept tools lost their hints")
	}
}

// Verifies SPEC-0028-R02.
func TestStatusSizeCountsAnnotations(t *testing.T) {
	bare := []kavryntv1alpha1.MCPServerTool{{Name: "search"}}
	hinted := []kavryntv1alpha1.MCPServerTool{{Name: "search", Annotations: &kavryntv1alpha1.MCPServerToolAnnotations{ReadOnlyHint: new(true), DestructiveHint: new(false)}}}
	if statusSize(hinted) <= statusSize(bare) {
		t.Fatalf("status size with hints = %d, without = %d; hints must count toward MaxStatusBytes", statusSize(hinted), statusSize(bare))
	}
}

// Verifies SPEC-0026-R09: given a pod, every request of the session goes to
// it, with the endpoint's host in Host.
func TestListToolsAtOneAddress(t *testing.T) {
	server := newFakeServer(tool("echo"))
	server.sessionID = "s-1"
	pod := netip.MustParseAddrPort("10.0.0.7:8080")
	if _, err := New("test", server).ListTools(context.Background(), endpoint, pod, nil); err != nil {
		t.Fatal(err)
	}
	if len(server.requests) < 4 {
		t.Fatalf("%d requests, want initialize, initialized, tools/list, and DELETE", len(server.requests))
	}
	for _, r := range server.requests {
		if r.URL.Host != "10.0.0.7:8080" || r.Host != "demo.default.svc:8080" || r.URL.Path != "/mcp" {
			t.Fatalf("%s %s went to %s with Host %q", r.Method, r.URL.Path, r.URL.Host, r.Host)
		}
	}
}
