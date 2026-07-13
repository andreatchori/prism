package platforms

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

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

// PostComment posts the review comment on the Pull Request
func (g *GitHubClient) PostComment(owner, repo string, prNumber int, comment string) error {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/issues/%d/comments",
		owner, repo, prNumber,
	)

	body := fmt.Sprintf(`{"body": %q}`, formatComment(comment))

	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

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

// formatComment wraps the review in a nice markdown format
func formatComment(review string) string {
	return fmt.Sprintf("## Prism Code Review\n\n%s\n\n---\n*Reviewed by [Prism](https://github.com/andreatchori/prism) - self-hosted AI code review agent*", review)
}
