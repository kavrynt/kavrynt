package upstreamcred

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const secretValue = "s3cr3t-token-value"

func store(t *testing.T, files map[string]string) Store {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Store{Dir: dir}
}

// Verifies SPEC-0011-R03.
func TestLookupNeedsTheOriginPin(t *testing.T) {
	s := store(t, map[string]string{
		"team-a.search":        secretValue + "\n",
		"team-a.search.origin": "http://search.team-a.svc:8080\n",
	})
	value, err := s.Lookup("team-a.search", "http://search.team-a.svc:8080/mcp?x=1")
	if err != nil || value != secretValue {
		t.Fatalf("Lookup = %q, %v", value, err)
	}
	for _, endpoint := range []string{
		"http://evil.example:8080/mcp",
		"https://search.team-a.svc:8080/mcp", // scheme is part of the pin
		"http://search.team-a.svc:9090/mcp",
		"http://search.team-a.svc/mcp", // port 80, not 8080
	} {
		if _, err := s.Lookup("team-a.search", endpoint); !errors.Is(err, ErrUnavailable) {
			t.Errorf("endpoint %s: err = %v, want unavailable", endpoint, err)
		}
	}
	if _, err := s.Lookup("team-b.search", "http://search.team-a.svc:8080"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("another server's credential was returned")
	}
}

// Verifies SPEC-0011-R03: default ports and host case are normalized.
func TestOriginNormalizesPortsAndCase(t *testing.T) {
	for raw, want := range map[string]string{
		"https://API.example.com/mcp":  "https://api.example.com:443",
		"https://api.example.com:443/": "https://api.example.com:443",
		"http://api.example.com":       "http://api.example.com:80",
		"http://[fd00::1]:8080/x":      "http://[fd00::1]:8080",
	} {
		if got, err := Origin(raw); err != nil || got != want {
			t.Errorf("Origin(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"ftp://a", "http://", "http://u:p@a", "not a url"} {
		if _, err := Origin(raw); err == nil {
			t.Errorf("Origin(%q) accepted", raw)
		}
	}
}

// Verifies SPEC-0011-R04: a rotated Secret applies without a restart.
func TestRotationAppliesAtOnce(t *testing.T) {
	s := store(t, map[string]string{"team-a.search": "old", "team-a.search.origin": "http://search:8080"})
	if value, _ := s.Lookup("team-a.search", "http://search:8080"); value != "old" {
		t.Fatalf("value = %q", value)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "team-a.search"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if value, _ := s.Lookup("team-a.search", "http://search:8080"); value != "new" {
		t.Fatalf("value after rotation = %q", value)
	}
}

// Verifies SPEC-0011-R06 and SPEC-0011-R09: missing, empty, oversized, and
// multi-line credentials are unavailable, and errors never contain values.
func TestUnavailableCredentialsAndErrors(t *testing.T) {
	cases := map[string]Store{
		"no Secret mounted": {},
		"no key":            store(t, map[string]string{"team-a.search.origin": "http://search:8080"}),
		"no pin":            store(t, map[string]string{"team-a.search": secretValue}),
		"empty value":       store(t, map[string]string{"team-a.search": "\n", "team-a.search.origin": "http://search:8080"}),
		"bad pin":           store(t, map[string]string{"team-a.search": secretValue, "team-a.search.origin": "search:8080 " + secretValue}),
		"two lines":         store(t, map[string]string{"team-a.search": secretValue + "\nInjected: yes", "team-a.search.origin": "http://search:8080"}),
		"too large":         store(t, map[string]string{"team-a.search": strings.Repeat("x", maxValueBytes+1), "team-a.search.origin": "http://search:8080"}),
		"wrong endpoint":    store(t, map[string]string{"team-a.search": secretValue, "team-a.search.origin": "http://other:8080"}),
	}
	for name, s := range cases {
		value, err := s.Lookup("team-a.search", "http://search:8080")
		if !errors.Is(err, ErrUnavailable) || value != "" {
			t.Errorf("%s: %q, %v; want unavailable", name, value, err)
			continue
		}
		if strings.Contains(err.Error(), secretValue) {
			t.Errorf("%s: error contains the credential: %v", name, err)
		}
		if s.Available("team-a.search", "http://search:8080") {
			t.Errorf("%s: reported available", name)
		}
	}
	s := store(t, map[string]string{"x": "y"})
	for _, route := range []string{"", "../etc", "team-a/search", ".hidden"} {
		if _, err := s.Lookup(route, "http://search:8080"); !errors.Is(err, ErrUnavailable) {
			t.Errorf("route %q: err = %v", route, err)
		}
	}
}
