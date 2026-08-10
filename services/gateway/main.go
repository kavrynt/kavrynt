package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kavrynt/gateway/internal/api"
	"github.com/kavrynt/gateway/internal/build"
	"github.com/kavrynt/gateway/internal/registry"
	"github.com/kavrynt/gateway/internal/routing"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	registryURL := flag.String("registry-url", getenv("KAVRYNT_REGISTRY_URL", ""), "Kavrynt Registry base URL")
	syncInterval := flag.Duration("sync-interval", 10*time.Second, "Registry sync interval")
	requestTimeout := flag.Duration("request-timeout", 30*time.Second, "Outbound request timeout")
	shutdownTimeout := flag.Duration("shutdown-timeout", 10*time.Second, "Graceful shutdown timeout")
	flag.Parse()

	registryClient, err := registry.NewClient(*registryURL, &http.Client{Timeout: *requestTimeout})
	if err != nil {
		slog.Error("invalid registry configuration", "error", err)
		os.Exit(1)
	}

	table := routing.NewTable()
	handler := api.NewHandler(table, registryClient, &http.Client{Timeout: *requestTimeout}, api.Metadata{
		Version:   build.Version,
		Commit:    build.Commit,
		BuildDate: build.BuildDate,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go handler.StartSync(ctx, *syncInterval)

	server := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("starting kavrynt gateway", "addr", *addr, "registryURL", *registryURL)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			slog.Error("gateway failed", "error", err)
			os.Exit(1)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("gateway shutdown failed", "error", err)
		os.Exit(1)
	}
	slog.Info("gateway stopped")
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func init() {
	flag.CommandLine.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "Kavrynt Gateway\n\n")
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n  gateway --registry-url http://localhost:8081\n\n")
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "Flags:\n")
		flag.PrintDefaults()
	}
}
