package main

import (
	"context"
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
	"github.com/kavrynt/kavrynt/internal/gateway/build"
	"github.com/kavrynt/kavrynt/internal/gateway/console"
	"github.com/kavrynt/kavrynt/internal/gateway/kube"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
	ctrl "sigs.k8s.io/controller-runtime"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	watchNamespaces := flag.String("watch-namespaces", getenv("KAVRYNT_WATCH_NAMESPACES", ""), "Comma-separated namespaces to watch for MCPServer resources (empty watches all)")
	stripHeaders := flag.String("strip-request-headers", getenv("KAVRYNT_STRIP_REQUEST_HEADERS", ""), "Comma-separated extra request headers never forwarded upstream (Authorization, Cookie, and Proxy-Authorization are always removed)")
	requestTimeout := flag.Duration("request-timeout", api.DefaultRequestTimeout, "Upstream request timeout (event streams: until response headers)")
	allowedOrigins := flag.String("allowed-origins", getenv("KAVRYNT_ALLOWED_ORIGINS", ""), "Comma-separated browser origins (scheme://host[:port]) allowed besides loopback; requests from other origins are rejected")
	consoleEnabled := flag.Bool("console", true, "Serve the read-only web console at /ui/")
	allowLoopback := flag.Bool("allow-loopback-endpoints", false, "Allow MCP endpoints that resolve to loopback addresses (local development only)")
	shutdownTimeout := flag.Duration("shutdown-timeout", 10*time.Second, "Graceful shutdown timeout")
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n  gateway [--watch-namespaces ns1,ns2]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

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
	if *consoleEnabled {
		handler.EnableConsole(console.Handler())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
		slog.Info("starting kavrynt gateway", "addr", *addr, "watchNamespaces", namespaces, "console", *consoleEnabled, "version", build.Version)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	exitCode := 0
	select {
	case <-ctx.Done():
	case err := <-errCh:
		slog.Error("gateway failed", "error", err)
		exitCode = 1
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("gateway shutdown failed", "error", err)
		exitCode = 1
	}
	slog.Info("gateway stopped")
	os.Exit(exitCode)
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

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
