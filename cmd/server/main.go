package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/chat"
	"github.com/Alex3k/grafana-demo-compiler/internal/httpapi"
	"github.com/Alex3k/grafana-demo-compiler/internal/observability"
	"github.com/Alex3k/grafana-demo-compiler/internal/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dataDir := envOrDefault("DEMO_COMPILER_DATA_DIR", "./data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	dataStore, err := store.Open(ctx, filepath.Join(dataDir, "compiler.db"))
	if err != nil {
		return err
	}
	defer dataStore.Close()

	o11y := observability.New(ctx)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := o11y.Shutdown(shutdownCtx); err != nil {
			logger.Error("observability shutdown failed", "error", err)
		}
	}()

	chatService, err := chat.New(ctx, o11y)
	if err != nil {
		return err
	}

	var webFiles fs.FS
	if _, err := os.Stat("web/dist/index.html"); err == nil {
		webFiles = os.DirFS("web/dist")
	}
	server := &http.Server{
		Addr:              envOrDefault("DEMO_COMPILER_ADDR", ":8080"),
		Handler:           httpapi.New(dataStore, chatService, o11y, logger, webFiles),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("Grafana Demo Compiler listening", "address", server.Addr, "bedrockModel", chatService.ModelID())
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
