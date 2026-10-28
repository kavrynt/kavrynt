package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/model"
)

// Verifies SPEC-0029-R01.
func TestOnlyMCPMethodsAreForwarded(t *testing.T) {
	handler, calls := demoHandler(t, okResponse)
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodHead, http.MethodOptions, "PROPFIND"} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(method, "/mcp/team-a.demo", strings.NewReader("{}")))
		if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != "GET, POST, DELETE" {
			t.Errorf("%s: %d Allow %q, want 405 and GET, POST, DELETE", method, rr.Code, rr.Header().Get("Allow"))
		}
	}
	if *calls != 0 {
		t.Fatalf("%d requests reached the server", *calls)
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(method, "/mcp/team-a.demo", nil))
		if rr.Code != http.StatusOK {
			t.Errorf("%s: %d, want it forwarded", method, rr.Code)
		}
	}
	if rr := post(handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil); rr.Code != http.StatusOK {
		t.Fatalf("POST: %d", rr.Code)
	}
}

// Verifies SPEC-0029-R02 and SPEC-0029-R03.
func TestSubpathsAreForwardedOnlyWhenAllowed(t *testing.T) {
	var targets []string
	handler, calls := demoHandler(t, func(req *http.Request) (*http.Response, error) {
		targets = append(targets, req.URL.Path)
		return okResponse(req)
	})
	for _, path := range []string{"/mcp/team-a.demo/other", "/mcp/team-a.demo/", "/mcp/team-a.demo/admin/x"} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, rr.Code)
		}
	}
	if *calls != 0 {
		t.Fatalf("%d requests with a path suffix reached the server", *calls)
	}

	handler.table.Replace([]model.Route{{Name: "team-a.demo", Version: "0.1.0", Transport: "http", Endpoint: "http://demo.test/mcp", ForwardSubpaths: true}}, time.Now())
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/mcp/team-a.demo/other", nil))
	if rr.Code != http.StatusOK || len(targets) != 1 || targets[0] != "/mcp/other" {
		t.Fatalf("with forwardSubpaths: %d to %v, want the suffix forwarded", rr.Code, targets)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/mcp/team-a.demo/a/../../admin", nil))
	if rr.Code != http.StatusBadRequest || len(targets) != 1 {
		t.Fatalf("dot segments with forwardSubpaths: %d", rr.Code)
	}
}
