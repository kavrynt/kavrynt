package mcpsession

import (
	"strings"
	"testing"
)

var testKey = []byte(strings.Repeat("k", 32))

var alice = Caller{Issuer: "https://as.example.com", Subject: "user-42", ClientID: "agent-a"}

// Verifies SPEC-0042-R03: the tag covers the caller's issuer, subject, and
// client ID, the route, and the session ID, so changing any of them does not
// open it.
func TestBindCoversCallerRouteAndSession(t *testing.T) {
	bound := Bind(testKey, alice, "team-a.demo", "abc123")
	if id, ok := Open(testKey, alice, "team-a.demo", bound); !ok || id != "abc123" {
		t.Fatalf("Open = %q, %v; want the session ID back", id, ok)
	}
	others := map[string]struct {
		key    []byte
		caller Caller
		route  string
		bound  string
	}{
		"another issuer":    {testKey, Caller{"https://other.example.com", alice.Subject, alice.ClientID}, "team-a.demo", bound},
		"another subject":   {testKey, Caller{alice.Issuer, "user-43", alice.ClientID}, "team-a.demo", bound},
		"another client ID": {testKey, Caller{alice.Issuer, alice.Subject, "agent-b"}, "team-a.demo", bound},
		"another route":     {testKey, alice, "team-a.other", bound},
		"another key":       {[]byte(strings.Repeat("j", 32)), alice, "team-a.demo", bound},
		"another session":   {testKey, alice, "team-a.demo", Bind(testKey, alice, "team-a.demo", "abc124")[:len("s1.")+32+1] + "abc123"},
	}
	for name, c := range others {
		if id, ok := Open(c.key, c.caller, c.route, c.bound); ok {
			t.Errorf("%s: opened as %q", name, id)
		}
	}
}

// Verifies SPEC-0042-R03: fields that move text between them do not give two
// callers the same tag.
func TestBindInputsAreLengthPrefixed(t *testing.T) {
	a := Bind(testKey, Caller{Issuer: "i", Subject: "ab", ClientID: "c"}, "r", "id")
	b := Bind(testKey, Caller{Issuer: "i", Subject: "a", ClientID: "bc"}, "r", "id")
	if a == b {
		t.Fatal("two different callers got the same bound ID")
	}
}

// Verifies SPEC-0042-R10: a fixed key and inputs give a fixed string, of the
// version, 32 lowercase hex characters, and the ID as received.
func TestBoundIDFormat(t *testing.T) {
	// The ID may itself contain dots, as the pod-tagged ID of session
	// affinity does.
	bound := Bind(testKey, alice, "team-a.demo", "0123456789abcdef.server-id")
	const want = "s1.fb1e706caaec731955d728fe746562f4.0123456789abcdef.server-id"
	if bound != want {
		t.Fatalf("Bind = %q, want %q", bound, want)
	}
	if id, ok := Open(testKey, alice, "team-a.demo", want); !ok || id != "0123456789abcdef.server-id" {
		t.Fatalf("Open = %q, %v", id, ok)
	}
}

// Verifies SPEC-0042-R10: a version the Gateway does not know, and anything
// that is not a bound ID, does not open.
func TestUnknownVersionDoesNotOpen(t *testing.T) {
	bound := Bind(testKey, alice, "team-a.demo", "abc123")
	for name, id := range map[string]string{
		"unknown version": "s2." + strings.TrimPrefix(bound, "s1."),
		"no version":      strings.TrimPrefix(bound, "s1."),
		"no tag":          "abc123",
		"empty":           "",
		"short MAC":       "s1.abcd.abc123",
		"upper-case MAC":  "s1." + strings.ToUpper(bound[3:35]) + ".abc123",
		"non-hex MAC":     "s1." + strings.Repeat("z", 32) + ".abc123",
		"empty ID":        bound[:len(bound)-len("abc123")],
	} {
		if opened, ok := Open(testKey, alice, "team-a.demo", id); ok {
			t.Errorf("%s: opened as %q", name, opened)
		}
	}
}
