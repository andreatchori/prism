package platforms

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// prismCommentMarker is an HTML comment embedded in every Prism comment so we
// can find and update it on subsequent pushes instead of creating duplicates.
const prismCommentMarker = "<!-- prism-review-comment -->"

// prismInlineMarker identifies Prism inline review comments so they can be
// updated or removed on subsequent pushes instead of duplicated.
const prismInlineMarker = "<!-- prism-inline -->"

type GitHubClient struct {
	token   string
	baseURL string
	client  *http.Client
}

type GitHubPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func NewGitHubClient() *GitHubClient {
	base := os.Getenv("GITHUB_API_URL")
	if base == "" {
		base = "https://api.github.com"
	}
	return &GitHubClient{
		token:   os.Getenv("GITHUB_TOKEN"),
		baseURL: strings.TrimRight(base, "/"),
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// FetchDiff fetches the raw diff of a Pull Request
func (g *GitHubClient) FetchDiff(owner, repo string, prNumber int) (string, error) {
	url := fmt.Sprintf(g.baseURL+"/repos/%s/%s/pulls/%d", owner, repo, prNumber)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github.v3.diff")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch diff: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read diff body: %w", err)
	}

	return string(body), nil
}

// PostComment upserts the review comment on the Pull Request: it updates the
// existing Prism comment when present, otherwise creates a new one.
func (g *GitHubClient) PostComment(owner, repo string, prNumber int, comment string) error {
	formatted := formatComment(comment)

	existingID, err := g.findPrismComment(owner, repo, prNumber)
	if err != nil {
		log.Printf("Could not look up existing Prism comment (will create new): %v", err)
	}

	if existingID != 0 {
		return g.updateComment(owner, repo, existingID, formatted, prNumber)
	}
	return g.createComment(owner, repo, prNumber, formatted)
}

func (g *GitHubClient) createComment(owner, repo string, prNumber int, formatted string) error {
	url := fmt.Sprintf(
		g.baseURL+"/repos/%s/%s/issues/%d/comments",
		owner, repo, prNumber,
	)

	body := fmt.Sprintf(`{"body": %q}`, formatted)

	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	g.setJSONHeaders(req)

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to post comment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("Comment posted on PR #%d", prNumber)
	return nil
}

func (g *GitHubClient) updateComment(owner, repo string, commentID int64, formatted string, prNumber int) error {
	url := fmt.Sprintf(
		g.baseURL+"/repos/%s/%s/issues/comments/%d",
		owner, repo, commentID,
	)

	body := fmt.Sprintf(`{"body": %q}`, formatted)

	req, err := http.NewRequest("PATCH", url, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	g.setJSONHeaders(req)

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to update comment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("Comment updated on PR #%d (id %d)", prNumber, commentID)
	return nil
}

// findPrismComment returns the ID of an existing Prism comment, or 0 if none.
func (g *GitHubClient) findPrismComment(owner, repo string, prNumber int) (int64, error) {
	url := fmt.Sprintf(
		g.baseURL+"/repos/%s/%s/issues/%d/comments?per_page=100",
		owner, repo, prNumber,
	)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := g.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("list comments status %d: %s", resp.StatusCode, string(respBody))
	}

	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		return 0, err
	}

	for _, c := range comments {
		if strings.Contains(c.Body, prismCommentMarker) {
			return c.ID, nil
		}
	}
	return 0, nil
}

func (g *GitHubClient) setJSONHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

// InlineComment is a single review comment tied to a file and line.
// StartLine, when > 0 and different from Line, produces a multi-line comment.
type InlineComment struct {
	Path      string
	Line      uint
	StartLine uint
	Body      string
}

// PostReview upserts Prism inline comments on the PR: it creates new comments,
// updates changed ones in place, and deletes stale Prism comments that are no
// longer applicable - all keyed by file path and line so pushes don't duplicate.
func (g *GitHubClient) PostReview(owner, repo string, prNumber int, sha string, comments []InlineComment) error {
	const maxInline = 30
	if len(comments) > maxInline {
		comments = comments[:maxInline]
	}

	existing, err := g.listPrismInlineComments(owner, repo, prNumber)
	if err != nil {
		log.Printf("Could not list existing GitHub inline comments (will create new): %v", err)
		existing = map[string]githubExistingComment{}
	}

	created, updated := 0, 0
	currentKeys := make(map[string]bool)
	for _, c := range comments {
		if c.Path == "" || c.Line == 0 || strings.TrimSpace(c.Body) == "" {
			continue
		}
		key := inlineKey(c.Path, c.Line)
		currentKeys[key] = true

		if prev, ok := existing[key]; ok {
			if strings.TrimSpace(prev.body) == strings.TrimSpace(c.Body) {
				continue
			}
			if err := g.updateReviewComment(owner, repo, prev.id, c.Body); err != nil {
				log.Printf("Failed to update inline comment on PR #%d (%s:%d): %v", prNumber, c.Path, c.Line, err)
				continue
			}
			updated++
			continue
		}

		if err := g.createReviewComment(owner, repo, prNumber, sha, c); err != nil {
			log.Printf("Failed to create inline comment on PR #%d (%s:%d): %v", prNumber, c.Path, c.Line, err)
			continue
		}
		created++
	}

	deleted := 0
	for key, prev := range existing {
		if currentKeys[key] {
			continue
		}
		if err := g.deleteReviewComment(owner, repo, prev.id); err != nil {
			log.Printf("Failed to delete stale inline comment on PR #%d (%s): %v", prNumber, key, err)
			continue
		}
		deleted++
	}

	if created > 0 || updated > 0 || deleted > 0 {
		log.Printf("Inline comments on PR #%d: %d created, %d updated, %d deleted", prNumber, created, updated, deleted)
	}
	return nil
}

type githubExistingComment struct {
	id   int64
	body string
}

func inlineKey(path string, line uint) string {
	return fmt.Sprintf("%s:%d", path, line)
}

// listPrismInlineComments returns existing Prism review comments keyed by
// "path:line" so they can be updated or removed instead of duplicated.
func (g *GitHubClient) listPrismInlineComments(owner, repo string, prNumber int) (map[string]githubExistingComment, error) {
	url := fmt.Sprintf(
		g.baseURL+"/repos/%s/%s/pulls/%d/comments?per_page=100",
		owner, repo, prNumber,
	)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	g.setJSONHeaders(req)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list PR comments status %d: %s", resp.StatusCode, string(respBody))
	}

	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		Path string `json:"path"`
		Line *uint  `json:"line"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		return nil, err
	}

	out := make(map[string]githubExistingComment)
	for _, c := range comments {
		if !strings.Contains(c.Body, prismInlineMarker) || c.Path == "" || c.Line == nil {
			continue
		}
		out[inlineKey(c.Path, *c.Line)] = githubExistingComment{id: c.ID, body: c.Body}
	}
	return out, nil
}

func (g *GitHubClient) createReviewComment(owner, repo string, prNumber int, sha string, c InlineComment) error {
	payload := struct {
		Body      string `json:"body"`
		CommitID  string `json:"commit_id"`
		Path      string `json:"path"`
		Line      uint   `json:"line"`
		StartLine uint   `json:"start_line,omitempty"`
		Side      string `json:"side"`
		StartSide string `json:"start_side,omitempty"`
	}{
		Body:     c.Body,
		CommitID: sha,
		Path:     c.Path,
		Line:     c.Line,
		Side:     "RIGHT",
	}
	if c.StartLine > 0 && c.StartLine < c.Line {
		payload.StartLine = c.StartLine
		payload.StartSide = "RIGHT"
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf(
		g.baseURL+"/repos/%s/%s/pulls/%d/comments",
		owner, repo, prNumber,
	)
	req, err := http.NewRequest("POST", url, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	g.setJSONHeaders(req)

	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create comment status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (g *GitHubClient) updateReviewComment(owner, repo string, commentID int64, body string) error {
	payload := fmt.Sprintf(`{"body": %q}`, body)
	url := fmt.Sprintf(
		g.baseURL+"/repos/%s/%s/pulls/comments/%d",
		owner, repo, commentID,
	)
	req, err := http.NewRequest("PATCH", url, strings.NewReader(payload))
	if err != nil {
		return err
	}
	g.setJSONHeaders(req)

	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update comment status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (g *GitHubClient) deleteReviewComment(owner, repo string, commentID int64) error {
	url := fmt.Sprintf(
		g.baseURL+"/repos/%s/%s/pulls/comments/%d",
		owner, repo, commentID,
	)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}
	g.setJSONHeaders(req)

	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete comment status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// SetCommitStatus sets the commit status (success or failure) on the PR
func (g *GitHubClient) SetCommitStatus(owner, repo, sha string, success bool) error {
	url := fmt.Sprintf(
		g.baseURL+"/repos/%s/%s/statuses/%s",
		owner, repo, sha,
	)

	state := "success"
	description := "Prism review passed"
	if !success {
		state = "failure"
		description = "Prism review failed - critical issues found"
	}

	payload := fmt.Sprintf(`{
		"state": %q,
		"description": %q,
		"context": "prism/review"
	}`, state, description)

	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to set commit status: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("Commit status set to: %s", state)
	return nil
}

// ExtractPRInfo extracts owner, repo and PR number from the webhook payload
func ExtractPRInfo(payload map[string]interface{}) (owner, repo string, prNumber int, sha string, err error) {
	repository, ok := payload["repository"].(map[string]interface{})
	if !ok {
		return "", "", 0, "", fmt.Errorf("missing repository in payload")
	}

	fullName, ok := repository["full_name"].(string)
	if !ok {
		return "", "", 0, "", fmt.Errorf("missing repository full_name")
	}

	parts := strings.Split(fullName, "/")
	if len(parts) != 2 {
		return "", "", 0, "", fmt.Errorf("invalid repository full_name: %s", fullName)
	}

	owner = parts[0]
	repo = parts[1]

	pr, ok := payload["pull_request"].(map[string]interface{})
	if !ok {
		return "", "", 0, "", fmt.Errorf("missing pull_request in payload")
	}

	number, ok := pr["number"].(float64)
	if !ok {
		return "", "", 0, "", fmt.Errorf("missing PR number")
	}
	prNumber = int(number)

	head, ok := pr["head"].(map[string]interface{})
	if !ok {
		return "", "", 0, "", fmt.Errorf("missing PR head")
	}

	sha, ok = head["sha"].(string)
	if !ok {
		return "", "", 0, "", fmt.Errorf("missing PR head sha")
	}

	return owner, repo, prNumber, sha, nil
}

// formatComment wraps the review in a nice markdown format, embedding a hidden
// marker so the comment can be found and updated on later pushes.
func formatComment(review string) string {
	return fmt.Sprintf("%s\n## Prism Code Review\n\n%s\n\n---\n*Reviewed by [Prism](https://github.com/andreatchori/prism) - self-hosted AI code review agent*", prismCommentMarker, review)
}
