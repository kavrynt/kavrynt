package api

import (
	"io"
	"net/http"
	"net/http/httptest"
)

// newRequest is httptest.NewRequest for a request as MCP clients send it:
// a POST carries its JSON-RPC message as application/json
// (SPEC-0044-R02). Tests of other content types set their own, or use
// httptest.NewRequest.
func newRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}
