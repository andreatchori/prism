package platforms

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

	"github.com/andreatchori/prism/internal/config"
	"github.com/andreatchori/prism/internal/reviewer"
)

type PullRequest struct {
	ID       string
	Title    string
	Diff     string
	Author   string
	Platform string
}

// WebhookHandler detects the platform and routes accordingly
func WebhookHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		// Detect platform from headers
		platform := detectPlatform(r.Header)
		log.Printf("📥 Webhook received from: %s", platform)

		switch platform {
		case "github":
			handleGitHub(w, r, payload, cfg)
		case "gitlab":
			handleGitLab(w, r, payload, cfg)
		default:
			log.Printf("⚠️  Unknown platform: %s", platform)
			http.Error(w, "unsupported platform", http.StatusBadRequest)
		}
	}
}

func detectPlatform(headers http.Header) string {
	if headers.Get("X-GitHub-Event") != "" {
		return "github"
	}
	if headers.Get("X-Gitlab-Event") != "" {
		return "gitlab"
	}
	if headers.Get("X-Vss-Activityid") != "" {
		return "azure"
	}
	if headers.Get("X-Event-Key") != "" {
		return "bitbucket"
	}
	return "unknown"
}

func handleGitHub(w http.ResponseWriter, r *http.Request, payload map[string]interface{}, cfg *config.Config) {
	// Extract basic PR info from payload
	pr := PullRequest{Platform: "github"}

	if action, ok := payload["action"].(string); ok {
		if action != "opened" && action != "synchronize" {
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	// TODO: fetch actual diff via GitHub API
	pr.Diff = "[diff will be fetched here]"
	pr.Title = "PR Title"

	review, err := reviewer.Review(pr.Diff, cfg)
	if err != nil {
		log.Printf("❌ Review failed: %v", err)
		http.Error(w, "review failed", http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Review completed for GitHub PR:\n%s", review)
	w.WriteHeader(http.StatusOK)
}

func handleGitLab(w http.ResponseWriter, r *http.Request, payload map[string]interface{}, cfg *config.Config) {
	// TODO: implement GitLab handler
	log.Println("GitLab webhook received — coming soon")
	w.WriteHeader(http.StatusOK)
}
