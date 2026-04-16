package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/moduleforge/users-module/api/internal/config"
	"github.com/moduleforge/users-module/api/internal/observability"
)

func main() {
	// Load and validate configuration before anything else so we fail
	// fast with a complete list of what is missing.
	cfg, err := config.Load()
	if err != nil {
		// Use a minimal text logger here because slog may not be
		// initialised yet; we want the error visible regardless.
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	// Structured JSON logging; level can be tuned via LOG_LEVEL env var.
	logLevel := resolveLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	// Root context cancelled on SIGTERM or SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Initialise OpenTelemetry. The returned shutdown function must be
	// called before the process exits to flush any pending spans/metrics.
	otelShutdown, err := observability.Init(ctx, cfg)
	if err != nil {
		slog.ErrorContext(ctx, "otel init failed", "error", err)
		os.Exit(1)
	}

	slog.InfoContext(ctx, "users-api up", "addr", cfg.Server.Addr)

	// Block until a signal is received.
	<-ctx.Done()
	stop() // release signal resources promptly

	slog.InfoContext(context.Background(), "shutdown signal received, beginning graceful shutdown")

	// Build a fresh context for shutdown with the configured deadline.
	// The parent ctx is already cancelled; we need an independent one.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	// Phase 3 (Task 3.1) will add: httpServer.Shutdown(shutdownCtx), pool.Close()
	// For now, only OTel needs flushing.

	slog.InfoContext(shutdownCtx, "flushing otel telemetry")
	if err := otelShutdown(shutdownCtx); err != nil {
		// OTel flush errors (e.g. collector unreachable) are not fatal — the
		// process has already drained in-flight requests. Log at ERROR so the
		// issue is visible, but exit 0 so the orchestrator does not restart
		// the pod unnecessarily.
		slog.ErrorContext(shutdownCtx, "otel shutdown error", "error", err)
	}

	slog.InfoContext(shutdownCtx, "shutdown complete")
}

// resolveLogLevel maps a LOG_LEVEL string to the corresponding slog.Level.
// Matching is case-insensitive so both "debug" and "DEBUG" work.
// It defaults to INFO for empty or unrecognised values.
func resolveLogLevel(level string) slog.Level {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
