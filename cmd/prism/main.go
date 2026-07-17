package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/andreatchori/prism/internal/config"
	"github.com/andreatchori/prism/internal/platforms"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.SetPrefix("prism ")

	cfgPath := os.Getenv("PRISM_CONFIG")
	if cfgPath == "" {
		cfgPath = "config/examples/rules.toml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("Prism started - reviewer: %s\n", cfg.Reviewer.Name)

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
		fmt.Printf("Listening on port %s\n", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		log.Fatalf("Server error: %v", err)
	case sig := <-stop:
		log.Printf("Received %s, shutting down gracefully...", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Graceful shutdown failed: %v", err)
		if err := srv.Close(); err != nil {
			log.Printf("Forced close failed: %v", err)
		}
	}

	// Give in-flight background reviews a moment to finish.
	platforms.WaitForReviews(15 * time.Second)
	log.Println("Prism stopped")
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","service":"prism"}`))
}
