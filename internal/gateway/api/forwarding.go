package api

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"github.com/kavrynt/kavrynt/internal/mcpmeta"
)

// methodOverrideHeaders ask a server to run another HTTP method than the
// request's. They are never forwarded (SPEC-0044-R04).
var methodOverrideHeaders = []string{"X-HTTP-Method-Override", "X-HTTP-Method", "X-Method-Override"}

// responseHeaders are the MCP server response headers clients always get
// (SPEC-0044-R08).
var responseHeaders = []string{
	"Allow", "Cache-Control", "Content-Encoding", "Content-Language", "Content-Length",
	"Content-Type", "Date", "MCP-Protocol-Version", "Mcp-Session-Id", "Retry-After", "Vary",
}

// refusedResponseHeaders act on the Gateway's origin, on the client's
// sign-in, or on what the Gateway itself sets, so no configuration lets an
// MCP server send them (SPEC-0044-R09).
var refusedResponseHeaders = map[string]struct{}{
	"Set-Cookie": {}, "Www-Authenticate": {}, "Proxy-Authenticate": {}, "Location": {},
	"Refresh": {}, "Strict-Transport-Security": {}, "Clear-Site-Data": {},
	"Content-Security-Policy": {}, "Content-Security-Policy-Report-Only": {},
	"X-Content-Type-Options": {}, "Alt-Svc": {}, "Server": {}, "X-Powered-By": {},
	"X-Request-Id": {},
}

// headerName is an HTTP header field name (RFC 9110 token).
var headerName = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")

// AllowResponseHeaders adds MCP server response headers that clients get,
// besides the fixed list (SPEC-0044-R09). It refuses names that are not
// header names and names no MCP server may send, and changes nothing then.
func (h *Handler) AllowResponseHeaders(names ...string) error {
	allowed := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !headerName.MatchString(name) {
			return fmt.Errorf("%q is not a header name", name)
		}
		if refusedResponseHeader(name) {
			return fmt.Errorf("MCP servers may never send %s to clients; remove it from the allowed response headers", http.CanonicalHeaderKey(name))
		}
		allowed = append(allowed, http.CanonicalHeaderKey(name))
	}
	for _, name := range allowed {
		h.allowResponse[name] = struct{}{}
	}
	return nil
}

func refusedResponseHeader(name string) bool {
	canonical := http.CanonicalHeaderKey(name)
	if _, refused := refusedResponseHeaders[canonical]; refused {
		return true
	}
	return strings.HasPrefix(canonical, "Access-Control-")
}

// checkRequestShape refuses, from the method and headers alone, a request
// whose body the Gateway would not read as one MCP message: a body on GET
// or DELETE, and a POST that is not JSON. It returns the status to answer
// with (SPEC-0044-R01, R02). A Content-Length above limit is left to the
// request size limit, which answers it with 413 (SPEC-0038-R01).
func checkRequestShape(r *http.Request, limit int64) (int, error) {
	switch r.Method {
	case http.MethodGet, http.MethodDelete:
		// 0 is no body; -1 is a body without Content-Length.
		if r.ContentLength != 0 && r.ContentLength <= limit {
			return http.StatusBadRequest, fmt.Errorf("%s requests to MCP routes must not have a body", r.Method)
		}
	case http.MethodPost:
		// A repeated header could be read either way by the server.
		values := r.Header.Values("Content-Type")
		mediaType, err := "", error(nil)
		if len(values) == 1 {
			// The type comes back with an error for a malformed parameter,
			// which a server could read another way.
			mediaType, _, err = mime.ParseMediaType(values[0])
		}
		if err != nil || mediaType != "application/json" {
			return http.StatusUnsupportedMediaType, errors.New("MCP requests must be POSTed with Content-Type: application/json")
		}
	}
	return 0, nil
}

// shapeCall labels a request refused by checkRequestShape, whose body was
// never read.
func shapeCall(r *http.Request) mcpCall {
	if r.Method == http.MethodPost {
		return mcpCall{method: labelUnknown}
	}
	return mcpCall{method: "http_" + strings.ToLower(r.Method)}
}

// checkNames refuses a JSON-RPC method or tools/call tool name outside the
// characters MCP names use, which the Operator also requires of the tools it
// records: a policy rule cannot match every way a server might read
// "delete_all " or "delete_all\u0000" (SPEC-0044-R05). The error never
// quotes the name.
func checkNames(body bodyInfo) error {
	if !body.classified || body.method == "" {
		return nil
	}
	if !mcpmeta.ValidName(body.method, mcpmeta.MaxToolNameLength) {
		return errors.New("the JSON-RPC method must be 1 to 128 characters of A-Z, a-z, 0-9, _, ., /, and -")
	}
	if body.method == "tools/call" && body.toolOK && !mcpmeta.ValidName(body.tool, mcpmeta.MaxToolNameLength) {
		return errors.New("the tool name must be 1 to 128 characters of A-Z, a-z, 0-9, _, ., /, and -")
	}
	return nil
}
