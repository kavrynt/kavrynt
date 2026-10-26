package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
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

// Verifies SPEC-0006-R01 and SPEC-0006-R02.
func TestClientAuthConfiguration(t *testing.T) {
	if a, err := newClientAuth(false, "", "", "", ""); a != nil || err != nil {
		t.Fatalf("auth off: %v, %v; want nothing", a, err)
	}
	issuers := `[{"issuer":"https://as.example.com"}]`
	for name, c := range map[string]struct{ url, issuers string }{
		"no resource URL":      {"", issuers},
		"no issuer":            {"https://mcp.example.com", ""},
		"empty issuer list":    {"https://mcp.example.com", "[]"},
		"issuers not JSON":     {"https://mcp.example.com", "https://as.example.com"},
		"unknown issuer field": {"https://mcp.example.com", `[{"issuer":"https://as.example.com","jwks":"x"}]`},
		"http issuer":          {"https://mcp.example.com", `[{"issuer":"http://as.example.com"}]`},
		"http resource URL":    {"http://mcp.example.com", issuers},
	} {
		if _, err := newClientAuth(true, c.url, c.issuers, "", ""); err == nil {
			t.Errorf("%s: authentication configured, want a refusal to start", name)
		}
	}
	if a, err := newClientAuth(true, "https://mcp.example.com", issuers, "mcp:tools", ""); err != nil || a == nil {
		t.Fatalf("valid configuration: %v, %v", a, err)
	}
}

// Verifies SPEC-0014-R03.
func TestWarnsWhenMCPRoutesAreUnauthenticated(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		var logs bytes.Buffer
		warnUnauthenticated(slog.New(slog.NewTextHandler(&logs, nil)), authenticated)
		warned := strings.Contains(logs.String(), "level=WARN") && strings.Contains(logs.String(), "unauthenticated")
		if warned == authenticated {
			t.Errorf("authenticated=%v: logged %q", authenticated, logs.String())
		}
	}
}

// Verifies SPEC-0017-R01 and SPEC-0017-R06: delivery needs audit, an https
// Cloud URL, and a positive buffer, and its requests follow the egress
// guard: a cloud metadata address is refused before any connection.
func TestAuditShipperConfiguration(t *testing.T) {
	if s, err := newAuditShipper(true, "", "", 10, false); s != nil || err != nil {
		t.Fatalf("no URL: %v, %v; want nothing", s, err)
	}
	if _, err := newAuditShipper(false, "https://cloud.example.com", "/token", 10, false); err == nil {
		t.Error("delivery without audit configured")
	}
	if _, err := newAuditShipper(true, "https://cloud.example.com", "/token", 0, false); err == nil {
		t.Error("a zero buffer accepted")
	}
	if _, err := newAuditShipper(true, "http://cloud.example.com", "/token", 10, false); err == nil {
		t.Error("an http Cloud URL accepted")
	}
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("cluster-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := newAuditShipper(true, "https://169.254.169.254", token, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	s.Add([]byte(`{"seq":1}`))
	err = s.Flush(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("delivery to a metadata address = %v, want refused by the egress guard", err)
	}
}

// Verifies SPEC-0024-R10: the Grafana URL the dashboards link is a plain
// http or https URL, and the tool cap is bounded.
func TestHistoryFlags(t *testing.T) {
	for _, ok := range []string{"", "https://grafana.example.com", "http://grafana.monitoring.svc:3000/grafana/"} {
		if err := checkHistoryFlags(500, ok); err != nil {
			t.Errorf("--grafana-url %q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"javascript:alert(1)", "grafana.example.com", "https://user:pw@grafana.example.com", "https://grafana.example.com/?orgId=1", "https://grafana.example.com/#x", "ftp://grafana.example.com"} {
		if err := checkHistoryFlags(500, bad); err == nil {
			t.Errorf("--grafana-url %q accepted", bad)
		}
	}
	for _, n := range []int{0, -1, 10001} {
		if err := checkHistoryFlags(n, ""); err == nil {
			t.Errorf("--history-max-tools %d accepted", n)
		}
	}
}
