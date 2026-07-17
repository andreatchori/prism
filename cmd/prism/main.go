package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/andreatchori/prism/internal/config"
	"github.com/andreatchori/prism/internal/platforms"
)

func main() {
	setupLogging()

	cfgPath := os.Getenv("PRISM_CONFIG")
	if cfgPath == "" {
		cfgPath = "config/examples/rules.toml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	slog.Info("prism started", "reviewer", cfg.Reviewer.Name)

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", platforms.WebhookHandler(cfg))
	mux.HandleFunc("/health", healthHandler)

	port := os.Getenv("PRISM_PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Run the server in the background so we can wait for shutdown signals.
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		slog.Error("server error", "error", err)
		os.Exit(1)
	case sig := <-stop:
		slog.Info("shutting down gracefully", "signal", sig.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Warn("graceful shutdown failed", "error", err)
		if err := srv.Close(); err != nil {
			slog.Error("forced close failed", "error", err)
		}
	}

	// Give in-flight background reviews a moment to finish.
	platforms.WaitForReviews(15 * time.Second)
	slog.Info("prism stopped")
}

// setupLogging configures slog as the default logger (also routing the standard
// log package through it). Format is controlled by PRISM_LOG_FORMAT (text|json)
// and level by PRISM_LOG_LEVEL (debug|info|warn|error).
func setupLogging() {
	level := parseLogLevel(os.Getenv("PRISM_LOG_LEVEL"))
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if strings.EqualFold(os.Getenv("PRISM_LOG_FORMAT"), "json") {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	// Ensure log.* calls (used across packages) flow through slog without
	// duplicating timestamps.
	log.SetFlags(0)
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","service":"prism"}`))
}
