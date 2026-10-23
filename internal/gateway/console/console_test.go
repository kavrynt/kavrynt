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

// storageAPIs are the browser storage interfaces the console may touch only
// for its theme preference.
var storageAPIs = regexp.MustCompile(`localStorage|sessionStorage|indexedDB|document\.cookie`)

// Verifies SPEC-0002-R13.
func TestStaticFilesStoreOnlyTheTheme(t *testing.T) {
	uses := 0
	err := fs.WalkDir(static, "static", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !(strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".html")) {
			return err
		}
		body, err := fs.ReadFile(static, path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			if !storageAPIs.MatchString(line) {
				continue
			}
			uses++
			if !strings.Contains(line, "THEME_KEY") {
				t.Errorf("%s:%d uses browser storage for something other than the theme: %s", path, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if uses == 0 {
		t.Fatal("found no theme storage at all; the scan is not looking at the right files")
	}
}

// Verifies SPEC-0002-R18.
func TestStaticFilesAreSelfContainedAndSmall(t *testing.T) {
	const budget = 256 << 10
	external := regexp.MustCompile(`(?i)(src|href)\s*=\s*["']?(https?:)?//|@import|url\(\s*["']?(https?:)?//|from\s+["']https?:|import\(\s*["']https?:`)
	total := 0
	err := fs.WalkDir(static, "static", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := fs.ReadFile(static, path)
		if err != nil {
			return err
		}
		total += len(body)
		switch {
		case strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".css"), strings.HasSuffix(path, ".html"), strings.HasSuffix(path, ".svg"):
		default:
			t.Errorf("%s: only HTML, CSS, JavaScript, and SVG are served; anything else implies a build step", path)
		}
		if loc := external.FindIndex(body); loc != nil {
			t.Errorf("%s references an external asset: %q", path, body[loc[0]:loc[1]])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total > budget {
		t.Fatalf("console static files total %d bytes, budget is %d", total, budget)
	}
}

// Browsers run scripts served with nosniff only with a JavaScript media type.
func TestHandlerServesScriptsAsJavaScript(t *testing.T) {
	handler := Handler()
	for _, path := range []string{"/app.js", "/dom.js", "/format.js", "/playground.js", "/state.js", "/stats.js"} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK || !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/javascript") {
			t.Fatalf("GET %s = %d %q, want 200 text/javascript", path, rr.Code, rr.Header().Get("Content-Type"))
		}
	}
}

// Module scripts are fetched with an Origin header even from the same
// origin, and the Gateway rejects origins outside --allowed-origins: behind
// an Ingress the console would load blank. The console uses classic scripts,
// each loaded after the scripts it reads from.
func TestIndexLoadsClassicScriptsInDependencyOrder(t *testing.T) {
	index, err := fs.ReadFile(static, "static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(index), `type="module"`) {
		t.Fatal(`index.html loads a module script; it would be refused behind an Ingress without --allowed-origins`)
	}
	var order []string
	for _, m := range regexp.MustCompile(`<script src="([a-z]+\.js)" defer></script>`).FindAllStringSubmatch(string(index), -1) {
		order = append(order, m[1])
	}
	want := "format.js stats.js state.js dom.js playground.js app.js"
	if strings.Join(order, " ") != want {
		t.Fatalf("index.html loads %v, want %s", order, want)
	}
}
