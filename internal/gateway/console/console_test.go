package console

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// Verifies SPEC-0001-R03.
func TestHandlerAllowsOnlyReads(t *testing.T) {
	handler := Handler()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(method, "/", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s / = %d, want 200", method, rr.Code)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(method, "/", nil))
		if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("%s / = %d with Allow %q, want 405 with GET, HEAD", method, rr.Code, rr.Header().Get("Allow"))
		}
	}
}

// Verifies SPEC-0001-R04.
func TestHandlerSetsSecurityHeaders(t *testing.T) {
	handler := Handler()
	for _, path := range []string{"/", "/app.js", "/app.css", "/logo.svg", "/missing"} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		csp := rr.Header().Get("Content-Security-Policy")
		for _, directive := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "connect-src 'self'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, directive) {
				t.Fatalf("GET %s: Content-Security-Policy %q lacks %q", path, csp, directive)
			}
		}
		if rr.Header().Get("X-Content-Type-Options") != "nosniff" || rr.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("GET %s: headers = %v", path, rr.Header())
		}
	}
}

// Verifies SPEC-0001-R05.
func TestStaticFilesUseNoHTMLSinks(t *testing.T) {
	sinks := regexp.MustCompile(`\.innerHTML\b|\.outerHTML\b|insertAdjacentHTML|document\.write|\beval\(|new Function\b`)
	err := fs.WalkDir(static, "static", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !(strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".html")) {
			return err
		}
		body, err := fs.ReadFile(static, path)
		if err != nil {
			return err
		}
		if sink := sinks.Find(body); sink != nil {
			t.Errorf("%s uses %q; insert untrusted data with textContent only", path, sink)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
