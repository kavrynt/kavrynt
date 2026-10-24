package main

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/api"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
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

// Verifies SPEC-0008-R01.
func TestShutdownDrainsBeforeClosing(t *testing.T) {
	handler := api.NewHandler(routing.NewTable(), nil, api.Metadata{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	base := "http://" + listener.Addr().String()

	status := func(path string) int {
		res, err := http.Get(base + path)
		if err != nil {
			return 0
		}
		_ = res.Body.Close()
		return res.StatusCode
	}

	var slept time.Duration
	err = shutdown(handler, server, 5*time.Second, time.Second, func(d time.Duration) {
		slept = d
		// During the delay readiness fails and the server still answers.
		if got := status("/readyz"); got != http.StatusServiceUnavailable {
			t.Errorf("readyz during the delay = %d, want 503", got)
		}
		res, err := http.Get(base + "/healthz")
		if err != nil {
			t.Errorf("healthz during the delay: %v", err)
			return
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK || !res.Close {
			t.Errorf("healthz during the delay = %d (close=%v), want 200 closing the connection", res.StatusCode, res.Close)
		}
	})
	if err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if slept != 5*time.Second {
		t.Fatalf("slept %s, want the 5s delay", slept)
	}
	if got := status("/healthz"); got != 0 {
		t.Fatalf("server still answers after shutdown: %d", got)
	}
}
