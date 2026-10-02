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

	"github.com/kavrynt/registry/internal/api"
	"github.com/kavrynt/registry/internal/build"
	"github.com/kavrynt/registry/internal/store"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("registry", flag.ContinueOnError)
	addr := fs.String("addr", envOrDefault("KAVRYNT_REGISTRY_ADDR", ":8080"), "HTTP listen address")
	dataPath := fs.String("data", envOrDefault("KAVRYNT_REGISTRY_DATA", "data/registry.json"), "registry data file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	fileStore, err := store.NewFileStore(*dataPath)
	if err != nil {
		logger.Error("failed to initialize store", "error", err)
		return 1
	}

	handler := api.NewHandler(fileStore, api.Metadata{
		Version:   build.Version,
		Commit:    build.Commit,
		BuildDate: build.BuildDate,
	})

	server := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting registry", "addr", *addr, "data", *dataPath, "version", build.Version)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("registry shutdown failed", "error", err)
			return 1
		}
		logger.Info("registry stopped")
		return 0
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("registry failed", "error", err)
			return 1
		}
		return 0
	}
}

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func init() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: registry [--addr :8080] [--data data/registry.json]")
	}
}
