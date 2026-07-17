package platforms

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/andreatchori/prism/internal/config"
	"github.com/andreatchori/prism/internal/engine"
	"github.com/andreatchori/prism/internal/reviewer"
)

// maxWebhookBodyBytes caps the webhook payload size (25 MB) to avoid unbounded
// memory use from oversized or malicious requests.
const maxWebhookBodyBytes = 25 << 20

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
		r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body (too large?)", http.StatusRequestEntityTooLarge)
			return
		}
		defer r.Body.Close()

		platform := detectPlatform(r.Header)
		slog.Info("webhook received", "platform", platform)

		if platform != "unknown" && isDuplicateDelivery(platform, r.Header) {
			slog.Info("duplicate webhook delivery ignored", "platform", platform)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"duplicate"}`))
			return
		}

		switch platform {
		case "github":
			if !verifyGitHubSignature(body, r.Header.Get("X-Hub-Signature-256")) {
				log.Printf("Invalid GitHub webhook signature")
				http.Error(w, "invalid signature", http.StatusUnauthorized)
				return
			}

			var payload map[string]interface{}
			if err := json.Unmarshal(body, &payload); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			handleGitHub(w, payload, cfg)

		case "gitlab":
			if !verifyGitLabToken(r.Header.Get("X-Gitlab-Token")) {
				log.Printf("Invalid GitLab webhook token")
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}

			var payload map[string]interface{}
			if err := json.Unmarshal(body, &payload); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			handleGitLab(w, payload, cfg)

		case "azure":
			if !verifyAzureBasicAuth(r.Header.Get("Authorization")) {
				log.Printf("Invalid Azure DevOps webhook auth")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			var payload map[string]interface{}
			if err := json.Unmarshal(body, &payload); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			handleAzure(w, payload, cfg)

		case "bitbucket":
			if !verifyBitbucketSignature(body, r.Header.Get("X-Hub-Signature")) {
				log.Printf("Invalid Bitbucket webhook signature")
				http.Error(w, "invalid signature", http.StatusUnauthorized)
				return
			}

			var payload map[string]interface{}
			if err := json.Unmarshal(body, &payload); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			handleBitbucket(w, r.Header.Get("X-Event-Key"), payload, cfg)

		default:
			log.Printf("Unknown platform: %s", platform)
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

// verifyGitHubSignature checks X-Hub-Signature-256 when GITHUB_WEBHOOK_SECRET is set.
// If the secret is empty, verification is skipped (local/dev mode).
func verifyGitHubSignature(body []byte, signatureHeader string) bool {
	secret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	if secret == "" {
		return true
	}
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}
	expected, err := hex.DecodeString(strings.TrimPrefix(signatureHeader, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), expected)
}

func handleGitHub(w http.ResponseWriter, payload map[string]interface{}, cfg *config.Config) {
	action, _ := payload["action"].(string)
	if action != "opened" && action != "synchronize" {
		w.WriteHeader(http.StatusOK)
		return
	}

	owner, repo, prNumber, sha, err := ExtractPRInfo(payload)
	if err != nil {
		log.Printf("Failed to extract PR info: %v", err)
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	log.Printf("Accepted PR #%d on %s/%s (sha:%s) - reviewing in background", prNumber, owner, repo, sha)

	// Acknowledge quickly so GitHub does not time out while Ollama runs
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"accepted"}`))

	key := fmt.Sprintf("github:%s/%s#%d", owner, repo, prNumber)
	runReview(key, func() { processGitHubReview(owner, repo, prNumber, sha, cfg) })
}

func processGitHubReview(owner, repo string, prNumber int, sha string, cfg *config.Config) {
	gh := NewGitHubClient()

	diff, err := gh.FetchDiff(owner, repo, prNumber)
	if err != nil {
		log.Printf("Failed to fetch diff for PR #%d: %v", prNumber, err)
		return
	}

	diff = truncateDiff(diff, cfg.Behavior.MaxDiffLines)

	result, err := reviewer.Review(diff, cfg)
	if err != nil {
		log.Printf("Review failed for PR #%d: %v", prNumber, err)
		return
	}

	if err := gh.PostComment(owner, repo, prNumber, result.Body); err != nil {
		log.Printf("Failed to post comment on PR #%d: %v", prNumber, err)
	}

	inline := inlineCommentsFromFindings(result.Findings)
	inline = append(inline, inlineCommentsFromSuggestions(result.Suggestions)...)
	if len(inline) > 0 {
		if err := gh.PostReview(owner, repo, prNumber, sha, inline); err != nil {
			log.Printf("Failed to post inline review on PR #%d: %v", prNumber, err)
		}
	}

	passed := true
	if cfg.Behavior.BlockOnCritical && result.HasCritical {
		passed = false
	}
	if err := gh.SetCommitStatus(owner, repo, sha, passed); err != nil {
		log.Printf("Failed to set commit status for PR #%d: %v", prNumber, err)
	}

	log.Printf("Review finished for PR #%d (critical=%v, passed=%v)", prNumber, result.HasCritical, passed)
}

// inlineCommentsFromFindings converts engine findings that have a line number
// into GitHub inline review comments.
func inlineCommentsFromFindings(findings []engine.Finding) []InlineComment {
	var out []InlineComment
	for _, f := range findings {
		if f.Line == nil || f.File == "" {
			continue
		}
		severity := strings.ToUpper(f.Severity)
		body := fmt.Sprintf("%s\n**Prism [%s]** (%s): %s", prismInlineMarker, severity, f.Category, f.Rule)
		out = append(out, InlineComment{
			Path: f.File,
			Line: *f.Line,
			Body: body,
		})
	}
	return out
}

// inlineCommentsFromSuggestions converts rule-based suggestions into GitHub
// inline comments carrying a one-click applicable ```suggestion block.
func inlineCommentsFromSuggestions(suggestions []reviewer.Suggestion) []InlineComment {
	var out []InlineComment
	for _, s := range suggestions {
		if s.File == "" || s.Line == 0 || strings.TrimSpace(s.Code) == "" {
			continue
		}
		code := strings.TrimRight(s.Code, "\n")
		body := prismInlineMarker + "\n**Prism suggestion** (rule-based)"
		if r := strings.TrimSpace(s.Rationale); r != "" {
			body += ": " + r
		}
		body += fmt.Sprintf("\n\n```suggestion\n%s\n```", code)

		ic := InlineComment{Path: s.File, Line: s.Line, Body: body}
		if s.EndLine > s.Line {
			ic.Line = s.EndLine
			ic.StartLine = s.Line
		}
		out = append(out, ic)
	}
	return out
}

// degradedSuggestionBody renders a suggestion as an inline comment with a plain
// (non-applicable) code block, for platforms without native one-click suggestions.
func degradedSuggestionBody(s reviewer.Suggestion) string {
	code := strings.TrimRight(s.Code, "\n")
	body := prismInlineMarker + "\n**Prism suggestion** (rule-based)"
	if r := strings.TrimSpace(s.Rationale); r != "" {
		body += ": " + r
	}
	body += fmt.Sprintf("\n\n```\n%s\n```", code)
	return body
}

// truncateDiff limits the diff to maxLines (approximate). If maxLines <= 0, no truncation.
func truncateDiff(diff string, maxLines int) string {
	if maxLines <= 0 {
		return diff
	}
	lines := strings.Split(diff, "\n")
	if len(lines) <= maxLines {
		return diff
	}
	log.Printf("Diff truncated from %d to %d lines", len(lines), maxLines)
	return strings.Join(lines[:maxLines], "\n")
}

func handleGitLab(w http.ResponseWriter, payload map[string]interface{}, cfg *config.Config) {
	kind, _ := payload["object_kind"].(string)
	if kind != "" && kind != "merge_request" {
		w.WriteHeader(http.StatusOK)
		return
	}

	attrs, _ := payload["object_attributes"].(map[string]interface{})
	action, _ := attrs["action"].(string)
	if action != "open" && action != "update" && action != "reopen" {
		w.WriteHeader(http.StatusOK)
		return
	}

	projectID, mrIID, sha, err := ExtractMRInfo(payload)
	if err != nil {
		log.Printf("Failed to extract MR info: %v", err)
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	log.Printf("Accepted GitLab MR !%d on %s (sha:%s) - reviewing in background", mrIID, projectID, sha)

	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"accepted"}`))

	key := fmt.Sprintf("gitlab:%s!%d", projectID, mrIID)
	runReview(key, func() { processGitLabReview(projectID, mrIID, sha, cfg) })
}

func processGitLabReview(projectID string, mrIID int, sha string, cfg *config.Config) {
	gl := NewGitLabClient()

	diff, err := gl.FetchDiff(projectID, mrIID)
	if err != nil {
		log.Printf("Failed to fetch diff for MR !%d: %v", mrIID, err)
		return
	}

	diff = truncateDiff(diff, cfg.Behavior.MaxDiffLines)

	result, err := reviewer.Review(diff, cfg)
	if err != nil {
		log.Printf("Review failed for MR !%d: %v", mrIID, err)
		return
	}

	if err := gl.PostComment(projectID, mrIID, result.Body); err != nil {
		log.Printf("Failed to post comment on MR !%d: %v", mrIID, err)
	}

	if cfg.Behavior.ProposeChanges && len(result.Suggestions) > 0 {
		refs, err := gl.GetDiffRefs(projectID, mrIID)
		if err != nil {
			log.Printf("Failed to fetch diff refs for MR !%d: %v", mrIID, err)
		} else if err := gl.PostSuggestions(projectID, mrIID, refs, result.Suggestions); err != nil {
			log.Printf("Failed to post inline suggestions on MR !%d: %v", mrIID, err)
		}
	}

	passed := true
	if cfg.Behavior.BlockOnCritical && result.HasCritical {
		passed = false
	}
	if err := gl.SetCommitStatus(projectID, sha, passed); err != nil {
		log.Printf("Failed to set commit status for MR !%d: %v", mrIID, err)
	}

	log.Printf("Review finished for MR !%d (critical=%v, passed=%v)", mrIID, result.HasCritical, passed)
}

func handleAzure(w http.ResponseWriter, payload map[string]interface{}, cfg *config.Config) {
	eventType, _ := payload["eventType"].(string)
	if eventType != "" &&
		eventType != "git.pullrequest.created" &&
		eventType != "git.pullrequest.updated" {
		w.WriteHeader(http.StatusOK)
		return
	}

	org, project, repoID, prID, err := ExtractAzurePRInfo(payload)
	if err != nil {
		log.Printf("Failed to extract Azure PR info: %v", err)
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	log.Printf("Accepted Azure DevOps PR #%d on %s/%s (repo:%s) - reviewing in background", prID, org, project, repoID)

	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"accepted"}`))

	key := fmt.Sprintf("azure:%s/%s/%s#%d", org, project, repoID, prID)
	runReview(key, func() { processAzureReview(org, project, repoID, prID, cfg) })
}

func processAzureReview(org, project, repoID string, prID int, cfg *config.Config) {
	az := NewAzureClient()

	diff, err := az.FetchDiff(org, project, repoID, prID)
	if err != nil {
		log.Printf("Failed to fetch Azure diff for PR #%d: %v", prID, err)
		return
	}

	diff = truncateDiff(diff, cfg.Behavior.MaxDiffLines)

	result, err := reviewer.Review(diff, cfg)
	if err != nil {
		log.Printf("Review failed for Azure PR #%d: %v", prID, err)
		return
	}

	if err := az.PostComment(org, project, repoID, prID, result.Body); err != nil {
		log.Printf("Failed to post comment on Azure PR #%d: %v", prID, err)
	}

	if cfg.Behavior.ProposeChanges && len(result.Suggestions) > 0 {
		if err := az.PostSuggestions(org, project, repoID, prID, result.Suggestions); err != nil {
			log.Printf("Failed to post inline suggestions on Azure PR #%d: %v", prID, err)
		}
	}

	passed := true
	if cfg.Behavior.BlockOnCritical && result.HasCritical {
		passed = false
	}
	if err := az.SetPRStatus(org, project, repoID, prID, passed); err != nil {
		log.Printf("Failed to set Azure PR status for #%d: %v", prID, err)
	}

	log.Printf("Review finished for Azure PR #%d (critical=%v, passed=%v)", prID, result.HasCritical, passed)
}

func handleBitbucket(w http.ResponseWriter, eventKey string, payload map[string]interface{}, cfg *config.Config) {
	if eventKey != "" &&
		eventKey != "pullrequest:created" &&
		eventKey != "pullrequest:updated" {
		w.WriteHeader(http.StatusOK)
		return
	}

	workspace, repoSlug, prID, sha, err := ExtractBitbucketPRInfo(payload)
	if err != nil {
		log.Printf("Failed to extract Bitbucket PR info: %v", err)
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	log.Printf("Accepted Bitbucket PR #%d on %s/%s (sha:%s) - reviewing in background", prID, workspace, repoSlug, sha)

	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"accepted"}`))

	key := fmt.Sprintf("bitbucket:%s/%s#%d", workspace, repoSlug, prID)
	runReview(key, func() { processBitbucketReview(workspace, repoSlug, prID, sha, cfg) })
}

func processBitbucketReview(workspace, repoSlug string, prID int, sha string, cfg *config.Config) {
	bb := NewBitbucketClient()

	diff, err := bb.FetchDiff(workspace, repoSlug, prID)
	if err != nil {
		log.Printf("Failed to fetch Bitbucket diff for PR #%d: %v", prID, err)
		return
	}

	diff = truncateDiff(diff, cfg.Behavior.MaxDiffLines)

	result, err := reviewer.Review(diff, cfg)
	if err != nil {
		log.Printf("Review failed for Bitbucket PR #%d: %v", prID, err)
		return
	}

	if err := bb.PostComment(workspace, repoSlug, prID, result.Body); err != nil {
		log.Printf("Failed to post comment on Bitbucket PR #%d: %v", prID, err)
	}

	if cfg.Behavior.ProposeChanges && len(result.Suggestions) > 0 {
		if err := bb.PostSuggestions(workspace, repoSlug, prID, result.Suggestions); err != nil {
			log.Printf("Failed to post inline suggestions on Bitbucket PR #%d: %v", prID, err)
		}
	}

	passed := true
	if cfg.Behavior.BlockOnCritical && result.HasCritical {
		passed = false
	}
	if err := bb.SetCommitStatus(workspace, repoSlug, sha, passed); err != nil {
		log.Printf("Failed to set Bitbucket status for PR #%d: %v", prID, err)
	}

	log.Printf("Review finished for Bitbucket PR #%d (critical=%v, passed=%v)", prID, result.HasCritical, passed)
}
