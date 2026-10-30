package api

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// firstExact returns the first member of the JSON object raw named exactly
// name, as MCP servers that match names exactly and keep the first of two
// read it.
func firstExact(raw []byte, name string) (json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, false
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		if token == name {
			return value, true
		}
	}
	return nil, false
}

// A message the Gateway classifies is read the same way by encoding/json
// (names matched case-insensitively, the last of two kept) and by a server
// that matches names exactly and keeps the first: the policy, audit, and
// metrics decide on the message the server runs (SPEC-0012-R09).
func FuzzInspectBodyReadsOneMessage(f *testing.F) {
	for _, seed := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_all"},"Method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_all","name":"echo"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_all"},"paramſ":{"name":"echo"}}`,
		`[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		info := inspectBody(body)
		if !info.classified {
			return
		}
		var lenient struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &lenient); err != nil {
			t.Fatalf("classified a message encoding/json rejects: %v", err)
		}
		var exact string
		if raw, ok := firstExact(body, "method"); ok {
			_ = json.Unmarshal(raw, &exact)
		}
		if lenient.Method != info.method || exact != info.method {
			t.Fatalf("method: Gateway %q, encoding/json %q, exact first %q", info.method, lenient.Method, exact)
		}
		if !info.toolOK {
			return
		}
		var lenientParams struct {
			Name *string `json:"name"`
		}
		if json.Unmarshal(lenient.Params, &lenientParams) != nil || lenientParams.Name == nil || *lenientParams.Name != info.tool {
			t.Fatalf("tool: Gateway %q, encoding/json read params %s differently", info.tool, lenient.Params)
		}
		params, _ := firstExact(body, "params")
		raw, ok := firstExact(params, "name")
		var name string
		if !ok || json.Unmarshal(raw, &name) != nil || name != info.tool {
			t.Fatalf("tool: Gateway %q, exact first read params %s differently", info.tool, params)
		}
	})
}

// A path the Gateway forwards has no . or .. segment however the upstream
// splits it: on / only, also on \, or with ;parameters dropped (Tomcat,
// Jetty, Spring).
func FuzzHasDotSegmentCoversServerReadings(f *testing.F) {
	for _, seed := range []string{"/a/b", "/../admin", "/..;/admin", "/.;x=1/x", `/a\..\admin`, "/a/..", "/..."} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		if hasDotSegment(path) {
			return
		}
		readings := map[string]func(rune) bool{
			"/":       func(r rune) bool { return r == '/' },
			"/ or \\": func(r rune) bool { return r == '/' || r == '\\' },
		}
		for name, split := range readings {
			for _, segment := range strings.FieldsFunc(path, split) {
				withoutParams, _, _ := strings.Cut(segment, ";")
				for _, read := range []string{segment, withoutParams} {
					if read == "." || read == ".." {
						t.Fatalf("%q passed, but a server splitting on %s reads the segment %q", path, name, read)
					}
				}
			}
		}
	})
}
