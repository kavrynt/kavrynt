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
	"github.com/kavrynt/kavrynt/internal/gateway/auth"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
)

// Verifies SPEC-0005-R11.
func TestConsoleLoginNeedsCredentialsDirectory(t *testing.T) {
	if _, err := newConsoleAuth(true, true, "", time.Hour, ""); err == nil || !strings.Contains(err.Error(), "--console-auth-dir") {
		t.Fatalf("login without a directory: err = %v, want a refusal naming --console-auth-dir", err)
	}
	if _, err := newConsoleAuth(true, true, t.TempDir(), 0, ""); err == nil {
		t.Fatal("zero session lifetime accepted")
	}
	if a, err := newConsoleAuth(true, true, t.TempDir(), time.Hour, ""); err != nil || a == nil {
		t.Fatalf("login with a directory: %v, %v", a, err)
	}
	for _, c := range []struct{ console, auth bool }{{false, true}, {true, false}, {false, false}} {
		if a, err := newConsoleAuth(c.console, c.auth, "", time.Hour, ""); err != nil || a != nil {
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
	if a, err := newClientAuth(false, "", "", "", "", ""); a != nil || err != nil {
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
		if _, err := newClientAuth(true, c.url, c.issuers, "", "", ""); err == nil {
			t.Errorf("%s: authentication configured, want a refusal to start", name)
		}
	}
	if a, err := newClientAuth(true, "https://mcp.example.com", issuers, "mcp:tools", "", ""); err != nil || a == nil {
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

// Verifies SPEC-0030-R01: the Gateway refuses to start with a trusted proxy
// it cannot parse.
func TestConsoleTrustedProxiesMustBeCIDRs(t *testing.T) {
	if _, err := newConsoleAuth(true, true, t.TempDir(), time.Hour, "10.0.0.0/8, fd00::/8"); err != nil {
		t.Fatalf("valid CIDRs refused: %v", err)
	}
	if _, err := newConsoleAuth(true, true, t.TempDir(), time.Hour, "10.0.0.1"); err == nil || !strings.Contains(err.Error(), "--console-trusted-proxies") {
		t.Fatalf("an address without a prefix length: err = %v", err)
	}
}

// Verifies SPEC-0031-R01 and SPEC-0031-R06: the Gateway audience needs client
// authentication, refuses "*", and is warned about at start with its routes.
func TestGatewayAudienceConfiguration(t *testing.T) {
	issuers := `[{"issuer":"https://login.example.com"}]`
	if _, err := newClientAuth(false, "", "", "", "", "team-a.*"); err == nil {
		t.Error("Gateway audience without client authentication configured")
	}
	if _, err := newClientAuth(true, "https://mcp.example.com", issuers, "", "", "*"); err == nil {
		t.Error(`Gateway audience for "*" configured`)
	}
	a, err := newClientAuth(true, "https://mcp.example.com", issuers, "", "", "team-a.*, team-c.reports")
	if err != nil || strings.Join(a.GatewayAudienceRoutes(), ",") != "team-a.*,team-c.reports" {
		t.Fatalf("valid configuration: %v", err)
	}

	var logs bytes.Buffer
	warnGatewayAudience(slog.New(slog.NewTextHandler(&logs, nil)), "https://mcp.example.com/", a.GatewayAudienceRoutes())
	if out := logs.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "routes=team-a.*,team-c.reports") || !strings.Contains(out, "audience=https://mcp.example.com ") {
		t.Fatalf("start log = %q, want a warning naming the audience and routes", out)
	}
	logs.Reset()
	warnGatewayAudience(slog.New(slog.NewTextHandler(&logs, nil)), "https://mcp.example.com", nil)
	if logs.Len() != 0 {
		t.Fatalf("Gateway audience off: logged %q", logs.String())
	}
}

// Verifies SPEC-0032-R01 and SPEC-0032-R10: console sign-in needs the
// console login, the https callback URL, and an allow list, and keeping the
// admin login is warned about at start.
func TestConsoleOIDCConfiguration(t *testing.T) {
	dir := t.TempDir()
	consoleAuth := auth.New(dir, time.Hour)
	ok := `{"issuer":"https://login.example.com/realms/agents","clientID":"kavrynt-console","redirectURL":"https://mcp.example.com/v1/session/oidc/callback","allowedGroups":["platform"]}`
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	for name, tc := range map[string]struct {
		auth *auth.Authenticator
		raw  string
	}{
		"without the console login": {nil, ok},
		"http callback":             {consoleAuth, strings.Replace(ok, "https://mcp", "http://mcp", 1)},
		"another callback path":     {consoleAuth, strings.Replace(ok, "/v1/session/oidc/callback", "/callback", 1)},
		"no allow list":             {consoleAuth, strings.Replace(ok, `,"allowedGroups":["platform"]`, "", 1)},
		"http issuer":               {consoleAuth, strings.Replace(ok, "https://login", "http://login", 1)},
		// The secret comes from the mounted Secret only (SPEC-0032-R02).
		"secret in the flag": {consoleAuth, strings.Replace(ok, `"clientID"`, `"clientSecret":"x","clientID"`, 1)},
	} {
		if err := enableConsoleOIDC(tc.auth, tc.raw, filepath.Join(dir, "secret"), logger); err == nil {
			t.Errorf("%s: configured", name)
		}
	}
	if err := enableConsoleOIDC(consoleAuth, ok, filepath.Join(dir, "secret"), logger); err != nil || logs.Len() != 0 {
		t.Fatalf("valid configuration: %v %s", err, logs.String())
	}
	kept := strings.Replace(ok, `"allowedGroups"`, `"keepAdminLogin":true,"allowedGroups"`, 1)
	if err := enableConsoleOIDC(auth.New(dir, time.Hour), kept, filepath.Join(dir, "secret"), logger); err != nil ||
		!strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "shared admin password still opens the console") {
		t.Fatalf("keepAdminLogin: %v %s", err, logs.String())
	}
	if err := enableConsoleOIDC(nil, "", "", logger); err != nil {
		t.Fatalf("off: %v", err)
	}
}

// Verifies SPEC-0042-R06: with client authentication on, the Gateway does not
// start without the session key, or with one under 32 bytes; with it off, it
// needs none.
func TestSessionKeyIsRequiredWithClientAuthentication(t *testing.T) {
	dir := t.TempDir()
	write := func(name, value string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	good := strings.Repeat("k", 64)

	if key, err := loadSessionKey(false, ""); key != nil || err != nil {
		t.Fatalf("client authentication off: %v, %v; want no key and no error", key, err)
	}
	for name, path := range map[string]string{
		"no file configured": "",
		"missing file":       filepath.Join(dir, "absent"),
		"empty file":         write("empty", ""),
		"31 bytes":           write("short", strings.Repeat("k", 31)),
	} {
		key, err := loadSessionKey(true, path)
		if err == nil || key != nil {
			t.Errorf("%s: loaded %d bytes, want a refusal to start", name, len(key))
		}
		if err != nil && strings.Contains(err.Error(), good) {
			t.Errorf("%s: the error holds the key", name)
		}
	}
	key, err := loadSessionKey(true, write("good", good))
	if err != nil || string(key) != good {
		t.Fatalf("a 64-byte key: %v, %v; want its bytes as they are", string(key), err)
	}
	if key, err := loadSessionKey(true, write("min", strings.Repeat("k", 32))); err != nil || len(key) != 32 {
		t.Fatalf("a 32-byte key: %d bytes, %v", len(key), err)
	}
}

// Verifies SPEC-0039-R09: the Gateway reads the Operator's ranges and
// namespace pairs, and does not start with a malformed one.
func TestClusterEgressFlags(t *testing.T) {
	for _, c := range []struct{ cidrs, pairs, want string }{
		{"10.96.0.1/33", "", "--cluster-cidrs"},
		{"10.96.0.0/12,cluster", "", "--cluster-cidrs"},
		{"10.96.0.0/12", "team-a", "--endpoint-cross-namespace"},
	} {
		if _, err := clusterEgress(c.cidrs, c.pairs, "cluster.local"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q, %q: err = %v, want a refusal naming %s", c.cidrs, c.pairs, err, c.want)
		}
	}
	cluster, err := clusterEgress("10.96.0.0/12, fd00:10:96::/112", "team-a:shared", "k8s.example")
	if err != nil || len(cluster.CIDRs) != 2 || cluster.Domain != "k8s.example" ||
		!cluster.CrossNamespace("team-a", "shared") || !cluster.CrossNamespace("team-a", "team-a") || cluster.CrossNamespace("shared", "team-a") {
		t.Fatalf("clusterEgress = %+v, %v", cluster, err)
	}
	if cluster, err := clusterEgress("", "", ""); err != nil || len(cluster.CIDRs) != 0 {
		t.Fatalf("no ranges: %+v, %v", cluster, err)
	}
}
