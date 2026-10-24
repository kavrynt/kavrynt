package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kavrynt/kavrynt/internal/egress"
	"github.com/kavrynt/kavrynt/internal/gateway/api"
	"github.com/kavrynt/kavrynt/internal/gateway/audit"
	"github.com/kavrynt/kavrynt/internal/gateway/auth"
	"github.com/kavrynt/kavrynt/internal/gateway/build"
	"github.com/kavrynt/kavrynt/internal/gateway/clientauth"
	"github.com/kavrynt/kavrynt/internal/gateway/console"
	"github.com/kavrynt/kavrynt/internal/gateway/kube"
	"github.com/kavrynt/kavrynt/internal/gateway/policy"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
	"github.com/kavrynt/kavrynt/internal/upstreamcred"
	ctrl "sigs.k8s.io/controller-runtime"
)

// policyReloadInterval is how often the mounted policy is checked for changes.
const policyReloadInterval = 10 * time.Second

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	watchNamespaces := flag.String("watch-namespaces", getenv("KAVRYNT_WATCH_NAMESPACES", ""), "Comma-separated namespaces to watch for MCPServer resources (empty watches all)")
	stripHeaders := flag.String("strip-request-headers", getenv("KAVRYNT_STRIP_REQUEST_HEADERS", ""), "Comma-separated extra request headers never forwarded upstream (Authorization, Cookie, and Proxy-Authorization are always removed)")
	requestTimeout := flag.Duration("request-timeout", api.DefaultRequestTimeout, "Upstream request timeout (event streams: until response headers)")
	allowedOrigins := flag.String("allowed-origins", getenv("KAVRYNT_ALLOWED_ORIGINS", ""), "Comma-separated browser origins (scheme://host[:port]) allowed besides loopback; requests from other origins are rejected")
	consoleEnabled := flag.Bool("console", true, "Serve the read-only web console at /ui/")
	consoleAuthEnabled := flag.Bool("console-auth", true, "Require the console admin login for the console and /v1/servers, /v1/traffic (with --console); MCP routes are not affected")
	consoleAuthDir := flag.String("console-auth-dir", getenv("KAVRYNT_CONSOLE_AUTH_DIR", ""), "Directory with the console admin password and sessionKey files, normally a mounted Secret")
	consoleSessionTTL := flag.Duration("console-session-ttl", auth.DefaultSessionTTL, "Console session lifetime")
	authEnabled := flag.Bool("auth", false, "Require a bearer access token for the route on every /mcp/ request (MCP client authentication)")
	authResourceURL := flag.String("auth-resource-url", getenv("KAVRYNT_AUTH_RESOURCE_URL", ""), "External https origin of the Gateway; a route's token audience is <url>/mcp/<route>")
	authIssuers := flag.String("auth-issuers", getenv("KAVRYNT_AUTH_ISSUERS", ""), `Trusted authorization servers as JSON: [{"issuer":"https://...","jwksURI":"(optional)","allowHTTP":false}]`)
	authScopes := flag.String("auth-required-scopes", getenv("KAVRYNT_AUTH_REQUIRED_SCOPES", ""), "Comma-separated scopes every token must carry")
	auditEnabled := flag.Bool("audit", false, "Write one audit event per MCP request as a JSON line on standard output")
	auditQueue := flag.Int("audit-queue-size", audit.DefaultQueueSize, "Audit events that may wait to be written")
	auditOverflow := flag.String("audit-on-overflow", audit.Reject, "With a full audit queue: reject (refuse new MCP requests with 503) or drop (serve them, count the lost events)")
	policyDir := flag.String("policy-dir", getenv("KAVRYNT_POLICY_DIR", ""), "Directory of the mounted tool authorization policy (.rego files defining data.kavrynt.authz.decision); every MCP request is then allowed or denied by it")
	upstreamCredentialsDir := flag.String("upstream-credentials-dir", getenv("KAVRYNT_UPSTREAM_CREDENTIALS_DIR", ""), "Directory of the mounted upstream credentials Secret (<namespace>.<name> and <namespace>.<name>.origin keys) for MCP servers that declare spec.credential")
	allowLoopback := flag.Bool("allow-loopback-endpoints", false, "Allow MCP endpoints that resolve to loopback addresses (local development only)")
	shutdownDelay := flag.Duration("shutdown-delay", 5*time.Second, "On shutdown, how long to keep serving after /readyz starts failing, so the Service stops sending new requests first")
	shutdownTimeout := flag.Duration("shutdown-timeout", 10*time.Second, "Graceful shutdown timeout")
	strictProtocol := flag.Bool("strict-protocol", true, "Refuse MCP requests that are not well-formed MCP Streamable HTTP: unsupported MCP-Protocol-Version, JSON-RPC batches, Mcp-Method or Mcp-Name headers that disagree with the body")
	instance := flag.String("instance", getenv("KAVRYNT_INSTANCE", hostname()), "Name of this replica in /v1/traffic, normally the pod name")
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n  gateway [--watch-namespaces ns1,ns2]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	consoleAuth, err := newConsoleAuth(*consoleEnabled, *consoleAuthEnabled, *consoleAuthDir, *consoleSessionTTL)
	if err != nil {
		slog.Error("configure console login", "error", err)
		os.Exit(1)
	}

	clientAuth, err := newClientAuth(*authEnabled, *authResourceURL, *authIssuers, *authScopes)
	if err != nil {
		slog.Error("configure MCP client authentication", "error", err)
		os.Exit(1)
	}
	warnUnauthenticated(slog.Default(), clientAuth != nil)

	var policyEngine *policy.Engine
	if *policyDir != "" {
		// A policy that does not compile stops the Gateway (SPEC-0012-R03).
		policyEngine, err = policy.New(*policyDir, slog.Default())
		if err != nil {
			slog.Error("load tool authorization policy", "error", err)
			os.Exit(1)
		}
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		slog.Error("load Kubernetes configuration", "error", err)
		os.Exit(1)
	}
	namespaces := splitList(*watchNamespaces)
	routeCache, err := kube.NewCache(cfg, namespaces)
	if err != nil {
		slog.Error("create MCPServer cache", "error", err)
		os.Exit(1)
	}

	table := routing.NewTable()
	// The request timeout is applied per request by the handler, so event
	// streams can outlive it.
	handler := api.NewHandler(table, &http.Client{Transport: egress.Transport(*allowLoopback)}, api.Metadata{
		Version:   build.Version,
		Commit:    build.Commit,
		BuildDate: build.BuildDate,
	})
	handler.StripRequestHeaders(splitList(*stripHeaders)...)
	handler.SetRequestTimeout(*requestTimeout)
	handler.AllowOrigins(splitList(*allowedOrigins)...)
	handler.SetInstance(*instance)
	handler.SetStrictProtocol(*strictProtocol)
	var auditWriter *audit.Writer
	if *auditEnabled {
		// A random suffix keeps chains apart when a pod's container restarts.
		auditWriter, err = audit.New(os.Stdout, *instance+"/"+randomSuffix(), *auditOverflow, *auditQueue)
		if err != nil {
			slog.Error("configure audit", "error", err)
			os.Exit(1)
		}
		handler.EnableAudit(auditWriter)
	}
	handler.EnableUpstreamCredentials(upstreamcred.Store{Dir: *upstreamCredentialsDir})
	if *consoleEnabled {
		handler.EnableConsole(console.Handler())
	}
	if consoleAuth != nil {
		handler.EnableConsoleAuth(consoleAuth)
	}
	if policyEngine != nil {
		handler.EnablePolicy(policyEngine)
	}
	if clientAuth != nil {
		handler.EnableClientAuth(clientAuth)
		if consoleAuth != nil {
			consoleAuth.ReportMCPAuth(true)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if policyEngine != nil {
		go policyEngine.Watch(ctx, policyReloadInterval)
	}

	errCh := make(chan error, 2)
	go func() {
		watcher := &kube.Watcher{Cache: routeCache, Table: table}
		if err := watcher.Run(ctx); err != nil {
			errCh <- fmt.Errorf("MCPServer watch: %w", err)
		}
	}()

	server := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		slog.Info("starting kavrynt gateway", "addr", *addr, "watchNamespaces", namespaces, "console", *consoleEnabled, "consoleAuth", consoleAuth != nil, "mcpAuth", clientAuth != nil, "policy", policyEngine != nil, "version", build.Version)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	exitCode := 0
	delay := *shutdownDelay
	select {
	case <-ctx.Done():
		slog.Info("draining gateway", "delay", delay)
	case err := <-errCh:
		slog.Error("gateway failed", "error", err)
		exitCode = 1
		delay = 0
	}

	if err := shutdown(handler, server, delay, *shutdownTimeout, time.Sleep); err != nil {
		slog.Error("gateway shutdown failed", "error", err)
		exitCode = 1
	}
	if auditWriter != nil {
		// Queued events are written before the Gateway exits (SPEC-0013-R09).
		flushCtx, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
		if err := auditWriter.Close(flushCtx); err != nil {
			slog.Error("audit events lost at shutdown", "error", err)
			exitCode = 1
		}
		cancelFlush()
	}
	slog.Info("gateway stopped")
	os.Exit(exitCode)
}

// shutdown drains the Gateway (SPEC-0008-R01): /readyz fails first so the
// Service stops routing new requests here, every request is still served for
// delay, then the server stops accepting connections and waits up to timeout
// for in-flight requests to finish. While draining, every response closes its
// connection, so clients open their next one to a replica that is staying.
func shutdown(handler *api.Handler, server *http.Server, delay, timeout time.Duration, sleep func(time.Duration)) error {
	handler.SetDraining()
	if delay > 0 {
		sleep(delay)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return server.Shutdown(ctx)
}

// newConsoleAuth returns the console admin login, or nil when the console or
// its login is off. Login without a credentials directory is refused rather
// than leaving the console open by accident (SPEC-0005-R11).
func newConsoleAuth(consoleEnabled, authEnabled bool, dir string, ttl time.Duration) (*auth.Authenticator, error) {
	if !consoleEnabled || !authEnabled {
		return nil, nil
	}
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("console login needs --console-auth-dir (a mounted Secret with password and sessionKey files); use --console-auth=false to run without it")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("--console-session-ttl must be positive, got %s", ttl)
	}
	return auth.New(dir, ttl), nil
}

// warnUnauthenticated says at start that MCP routes are open when client
// authentication is off (SPEC-0014-R03).
func warnUnauthenticated(logger *slog.Logger, authenticated bool) {
	if !authenticated {
		logger.Warn("MCP client authentication is off: every /mcp/ route accepts unauthenticated calls; configure --auth for production")
	}
}

// newClientAuth returns the MCP client authenticator, or nil when --auth is
// off. Authentication without a resource URL or a trusted issuer is refused
// (SPEC-0006-R02). Key and metadata requests use the egress guard without the
// loopback exception (SPEC-0006-R11).
func newClientAuth(enabled bool, resourceURL, issuersJSON, scopes string) (*clientauth.Authenticator, error) {
	if !enabled {
		return nil, nil
	}
	var issuers []clientauth.Issuer
	if strings.TrimSpace(issuersJSON) != "" {
		decoder := json.NewDecoder(strings.NewReader(issuersJSON))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&issuers); err != nil {
			return nil, fmt.Errorf("--auth-issuers must be a JSON array of issuers: %w", err)
		}
	}
	return clientauth.New(clientauth.Config{
		ResourceURL:    resourceURL,
		Issuers:        issuers,
		RequiredScopes: splitList(scopes),
	}, egress.Transport(false), slog.Default())
}

func splitList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hostname() string {
	name, _ := os.Hostname()
	return name
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
