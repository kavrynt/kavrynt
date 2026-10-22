package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, name, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	p, err := newProfile(name)
	if err != nil {
		t.Fatal(err)
	}
	return serveProfile(p, method, body)
}

func serveProfile(p *profile, method, body string) *httptest.ResponseRecorder {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	recorder := httptest.NewRecorder()
	newHandler(p, logger).ServeHTTP(recorder, httptest.NewRequest(method, "/", strings.NewReader(body)))
	return recorder
}

func decode(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", recorder.Body.String(), err)
	}
	return out
}

func toolText(t *testing.T, out map[string]any) (string, bool) {
	t.Helper()
	result, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", out)
	}
	content := result["content"].([]any)[0].(map[string]any)
	isError, _ := result["isError"].(bool)
	return content["text"].(string), isError
}

func TestNewProfileRejectsUnknownProfile(t *testing.T) {
	if _, err := newProfile(""); err == nil {
		t.Fatal("empty profile accepted")
	}
}

func TestNotificationIsAccepted(t *testing.T) {
	recorder := serve(t, "incidents", http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if recorder.Code != http.StatusAccepted || recorder.Body.Len() != 0 {
		t.Fatalf("got %d %q, want 202 with no body", recorder.Code, recorder.Body.String())
	}
}

func TestGetStreamIsNotSupported(t *testing.T) {
	recorder := serve(t, "incidents", http.MethodGet, "")
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", recorder.Code)
	}
}

func TestInvalidRequest(t *testing.T) {
	recorder := serve(t, "incidents", http.MethodPost, `{"id":1}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", recorder.Code)
	}
}

func TestInitializeAndToolsList(t *testing.T) {
	for name, want := range map[string][]string{
		"incidents": {"list_incidents", "get_incident", "acknowledge_incident"},
		"runbooks":  {"search_runbooks", "get_runbook"},
	} {
		out := decode(t, serve(t, name, http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
		info := out["result"].(map[string]any)["serverInfo"].(map[string]any)
		if info["name"] != "kavrynt-demo-"+name {
			t.Fatalf("serverInfo.name = %v", info["name"])
		}

		out = decode(t, serve(t, name, http.MethodPost, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
		tools := out["result"].(map[string]any)["tools"].([]any)
		var got []string
		for _, tool := range tools {
			entry := tool.(map[string]any)
			if entry["inputSchema"].(map[string]any)["type"] != "object" {
				t.Fatalf("%s: inputSchema is not an object", entry["name"])
			}
			got = append(got, entry["name"].(string))
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s tools = %v, want %v", name, got, want)
		}
	}
}

func TestUnknownMethod(t *testing.T) {
	out := decode(t, serve(t, "runbooks", http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`))
	if out["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("got %v, want method not found", out)
	}
}

func TestAcknowledgeIncidentChangesState(t *testing.T) {
	p := incidentsProfile()
	call := func(body string) (string, bool) {
		return toolText(t, decode(t, serveProfile(p, http.MethodPost, body)))
	}

	text, isError := call(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"acknowledge_incident","arguments":{"id":"inc-1043","note":"on it"}}}`)
	if isError || text != "INC-1043 acknowledged" {
		t.Fatalf("acknowledge = %q, isError %v", text, isError)
	}
	text, _ = call(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_incidents","arguments":{"status":"open"}}}`)
	if strings.Contains(text, "INC-1043") || !strings.Contains(text, "INC-1042") {
		t.Fatalf("open incidents after acknowledge = %s", text)
	}
	if _, isError = call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"acknowledge_incident","arguments":{"id":"INC-1043","note":"again"}}}`); !isError {
		t.Fatal("acknowledging twice succeeded")
	}
}

func TestToolErrors(t *testing.T) {
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"missing_tool"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_runbook","arguments":{"id":"RB-99"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_runbook","arguments":"not-an-object"}}`,
	} {
		if _, isError := toolText(t, decode(t, serve(t, "runbooks", http.MethodPost, body))); !isError {
			t.Fatalf("%s: want a tool error", body)
		}
	}
}

func TestSearchRunbooks(t *testing.T) {
	out := decode(t, serve(t, "runbooks", http.MethodPost,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_runbooks","arguments":{"query":"Secrets"}}}`))
	text, isError := toolText(t, out)
	if isError || !strings.Contains(text, "RB-3") || strings.Contains(text, "RB-7") {
		t.Fatalf("search = %s", text)
	}
}
