package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

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
	requests  []*http.Request
	methods   []string
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
		result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": capabilities}
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
	return New("test", server).ListTools(context.Background(), endpoint)
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
			want: "initialize returned HTTP 401",
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
			want: "initialize: unsupported response content type",
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
