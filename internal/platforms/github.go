package platforms

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

// prismCommentMarker is an HTML comment embedded in every Prism comment so we
// can find and update it on subsequent pushes instead of creating duplicates.
const prismCommentMarker = "<!-- prism-review-comment -->"

type GitHubClient struct {
	token  string
	client *http.Client
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
	return &GitHubClient{
		token:  os.Getenv("GITHUB_TOKEN"),
		client: &http.Client{},
	}
}

// FetchDiff fetches the raw diff of a Pull Request
func (g *GitHubClient) FetchDiff(owner, repo string, prNumber int) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, prNumber)

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
		"https://api.github.com/repos/%s/%s/issues/%d/comments",
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
		"https://api.github.com/repos/%s/%s/issues/comments/%d",
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
		"https://api.github.com/repos/%s/%s/issues/%d/comments?per_page=100",
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

// SetCommitStatus sets the commit status (success or failure) on the PR
func (g *GitHubClient) SetCommitStatus(owner, repo, sha string, success bool) error {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/statuses/%s",
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
