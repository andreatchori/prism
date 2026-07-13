package platforms

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type GitLabClient struct {
	token   string
	baseURL string
	client  *http.Client
}

type gitlabChange struct {
	Diff string `json:"diff"`
}

type gitlabChangesResponse struct {
	Changes []gitlabChange `json:"changes"`
}

func NewGitLabClient() *GitLabClient {
	base := os.Getenv("GITLAB_URL")
	if base == "" {
		base = "https://gitlab.com"
	}
	return &GitLabClient{
		token:   os.Getenv("GITLAB_TOKEN"),
		baseURL: strings.TrimRight(base, "/"),
		client:  &http.Client{},
	}
}

// FetchDiff fetches the unified diff of a Merge Request
func (g *GitLabClient) FetchDiff(projectID string, mrIID int) (string, error) {
	endpoint := fmt.Sprintf(
		"%s/api/v4/projects/%s/merge_requests/%d/changes",
		g.baseURL,
		url.PathEscape(projectID),
		mrIID,
	)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	g.setHeaders(req)

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch MR changes: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GitLab API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result gitlabChangesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode changes: %w", err)
	}

	var b strings.Builder
	for _, change := range result.Changes {
		b.WriteString(change.Diff)
		if !strings.HasSuffix(change.Diff, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

// PostComment posts a note on the Merge Request
func (g *GitLabClient) PostComment(projectID string, mrIID int, comment string) error {
	endpoint := fmt.Sprintf(
		"%s/api/v4/projects/%s/merge_requests/%d/notes",
		g.baseURL,
		url.PathEscape(projectID),
		mrIID,
	)

	payload := fmt.Sprintf(`{"body": %q}`, formatGitLabComment(comment))
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	g.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to post comment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitLab API returned status %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("Comment posted on GitLab MR !%d", mrIID)
	return nil
}

// SetCommitStatus sets a commit status on the MR head SHA
func (g *GitLabClient) SetCommitStatus(projectID, sha string, success bool) error {
	endpoint := fmt.Sprintf(
		"%s/api/v4/projects/%s/statuses/%s",
		g.baseURL,
		url.PathEscape(projectID),
		url.PathEscape(sha),
	)

	state := "success"
	description := "Prism review passed"
	if !success {
		state = "failed"
		description = "Prism review failed - critical issues found"
	}

	payload := fmt.Sprintf(`{
		"state": %q,
		"description": %q,
		"name": "prism/review",
		"context": "prism/review"
	}`, state, description)

	req, err := http.NewRequest("POST", endpoint, strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	g.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to set commit status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitLab API returned status %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("GitLab commit status set to: %s", state)
	return nil
}

func (g *GitLabClient) setHeaders(req *http.Request) {
	req.Header.Set("PRIVATE-TOKEN", g.token)
	req.Header.Set("Accept", "application/json")
}

// ExtractMRInfo extracts project id/path, MR IID and head SHA from a GitLab webhook payload
func ExtractMRInfo(payload map[string]interface{}) (projectID string, mrIID int, sha string, err error) {
	project, ok := payload["project"].(map[string]interface{})
	if !ok {
		return "", 0, "", fmt.Errorf("missing project in payload")
	}

	if path, ok := project["path_with_namespace"].(string); ok && path != "" {
		projectID = path
	} else if id, ok := project["id"].(float64); ok {
		projectID = fmt.Sprintf("%.0f", id)
	} else {
		return "", 0, "", fmt.Errorf("missing project id or path_with_namespace")
	}

	attrs, ok := payload["object_attributes"].(map[string]interface{})
	if !ok {
		return "", 0, "", fmt.Errorf("missing object_attributes in payload")
	}

	iid, ok := attrs["iid"].(float64)
	if !ok {
		return "", 0, "", fmt.Errorf("missing merge request iid")
	}
	mrIID = int(iid)

	if lastCommit, ok := attrs["last_commit"].(map[string]interface{}); ok {
		if id, ok := lastCommit["id"].(string); ok && id != "" {
			return projectID, mrIID, id, nil
		}
	}

	return "", 0, "", fmt.Errorf("missing last_commit.id")
}

func formatGitLabComment(review string) string {
	return fmt.Sprintf("## Prism Code Review\n\n%s\n\n---\n*Reviewed by [Prism](https://github.com/andreatchori/prism) - self-hosted AI code review agent*", review)
}

// verifyGitLabToken checks X-Gitlab-Token when GITLAB_WEBHOOK_SECRET is set.
func verifyGitLabToken(tokenHeader string) bool {
	secret := os.Getenv("GITLAB_WEBHOOK_SECRET")
	if secret == "" {
		return true
	}
	return hmacEqualString(secret, tokenHeader)
}

func hmacEqualString(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
