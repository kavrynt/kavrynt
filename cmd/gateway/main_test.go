package main

import (
	"strings"
	"testing"
	"time"
)

// Verifies SPEC-0005-R11.
func TestConsoleLoginNeedsCredentialsDirectory(t *testing.T) {
	if _, err := newConsoleAuth(true, true, "", time.Hour); err == nil || !strings.Contains(err.Error(), "--console-auth-dir") {
		t.Fatalf("login without a directory: err = %v, want a refusal naming --console-auth-dir", err)
	}
	if _, err := newConsoleAuth(true, true, t.TempDir(), 0); err == nil {
		t.Fatal("zero session lifetime accepted")
	}
	if a, err := newConsoleAuth(true, true, t.TempDir(), time.Hour); err != nil || a == nil {
		t.Fatalf("login with a directory: %v, %v", a, err)
	}
	for _, c := range []struct{ console, auth bool }{{false, true}, {true, false}, {false, false}} {
		if a, err := newConsoleAuth(c.console, c.auth, "", time.Hour); err != nil || a != nil {
			t.Fatalf("console=%v auth=%v: got %v, %v; want no login and no error", c.console, c.auth, a, err)
		}
	}
}
