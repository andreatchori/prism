package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/andreatchori/prism/internal/config"
	"github.com/andreatchori/prism/internal/platforms"
)

func main() {
	// Load config
	cfgPath := os.Getenv("PRISM_CONFIG")
	if cfgPath == "" {
		cfgPath = "config/examples/rules.toml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("❌ Failed to load config: %v", err)
	}

	fmt.Printf("Prism started - reviewer: %s\n", cfg.Reviewer.Name)

	// Routes
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", platforms.WebhookHandler(cfg))
	mux.HandleFunc("/health", healthHandler)

	port := os.Getenv("PRISM_PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("🚀 Listening on port %s\n", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("❌ Server error: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","service":"prism"}`))
}
