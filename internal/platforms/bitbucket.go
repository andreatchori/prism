package platforms

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type BitbucketClient struct {
	username string
	appPass  string
	token    string
	client   *http.Client
}

func NewBitbucketClient() *BitbucketClient {
	return &BitbucketClient{
		username: os.Getenv("BITBUCKET_USERNAME"),
		appPass:  os.Getenv("BITBUCKET_APP_PASSWORD"),
		token:    os.Getenv("BITBUCKET_TOKEN"),
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (b *BitbucketClient) setAuth(req *http.Request) {
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
		return
	}
	req.SetBasicAuth(b.username, b.appPass)
}

// FetchDiff fetches the raw unified diff of a Pull Request
func (b *BitbucketClient) FetchDiff(workspace, repoSlug string, prID int) (string, error) {
	endpoint := fmt.Sprintf(
		"https://api.bitbucket.org/2.0/repositories/%s/%s/pullrequests/%d/diff",
		url.PathEscape(workspace), url.PathEscape(repoSlug), prID,
	)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return "", err
	}
	b.setAuth(req)

	resp, err := b.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch diff: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Bitbucket API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read diff: %w", err)
	}
	return string(body), nil
}

// PostComment upserts a comment on the Pull Request: updates the existing Prism
// comment when present, otherwise creates a new one.
func (b *BitbucketClient) PostComment(workspace, repoSlug string, prID int, comment string) error {
	base := fmt.Sprintf(
		"https://api.bitbucket.org/2.0/repositories/%s/%s/pullrequests/%d/comments",
		url.PathEscape(workspace), url.PathEscape(repoSlug), prID,
	)

	existingID, err := b.findPrismComment(base)
	if err != nil {
		log.Printf("Could not look up existing Bitbucket comment (will create new): %v", err)
	}

	method := "POST"
	endpoint := base
	if existingID != 0 {
		method = "PUT"
		endpoint = fmt.Sprintf("%s/%d", base, existingID)
	}

	payload := fmt.Sprintf(`{"content": {"raw": %q}}`, formatBitbucketComment(comment))
	req, err := http.NewRequest(method, endpoint, strings.NewReader(payload))
	if err != nil {
		return err
	}
	b.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to post comment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Bitbucket API returned status %d: %s", resp.StatusCode, string(body))
	}

	if existingID != 0 {
		log.Printf("Comment updated on Bitbucket PR #%d (id %d)", prID, existingID)
	} else {
		log.Printf("Comment posted on Bitbucket PR #%d", prID)
	}
	return nil
}

// findPrismComment returns the ID of an existing Prism comment, or 0 if none.
func (b *BitbucketClient) findPrismComment(base string) (int64, error) {
	req, err := http.NewRequest("GET", base+"?pagelen=100", nil)
	if err != nil {
		return 0, err
	}
	b.setAuth(req)

	resp, err := b.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("list comments status %d: %s", resp.StatusCode, string(respBody))
	}

	var page struct {
		Values []struct {
			ID      int64 `json:"id"`
			Content struct {
				Raw string `json:"raw"`
			} `json:"content"`
			Deleted bool `json:"deleted"`
		} `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return 0, err
	}

	for _, c := range page.Values {
		if !c.Deleted && strings.Contains(c.Content.Raw, prismCommentMarker) {
			return c.ID, nil
		}
	}
	return 0, nil
}

// SetCommitStatus sets a build status on the head commit
func (b *BitbucketClient) SetCommitStatus(workspace, repoSlug, sha string, success bool) error {
	endpoint := fmt.Sprintf(
		"https://api.bitbucket.org/2.0/repositories/%s/%s/commit/%s/statuses/build",
		url.PathEscape(workspace), url.PathEscape(repoSlug), url.PathEscape(sha),
	)

	state := "SUCCESSFUL"
	description := "Prism review passed"
	if !success {
		state = "FAILED"
		description = "Prism review failed - critical issues found"
	}

	payload := fmt.Sprintf(`{
		"state": %q,
		"key": "prism/review",
		"name": "Prism Code Review",
		"description": %q
	}`, state, description)

	req, err := http.NewRequest("POST", endpoint, strings.NewReader(payload))
	if err != nil {
		return err
	}
	b.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to set commit status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Bitbucket API returned status %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("Bitbucket commit status set to: %s", state)
	return nil
}

// ExtractBitbucketPRInfo extracts workspace, repo, PR id and SHA from a webhook payload
func ExtractBitbucketPRInfo(payload map[string]interface{}) (workspace, repoSlug string, prID int, sha string, err error) {
	repo, ok := payload["repository"].(map[string]interface{})
	if !ok {
		return "", "", 0, "", fmt.Errorf("missing repository in payload")
	}

	fullName, _ := repo["full_name"].(string)
	if fullName == "" {
		return "", "", 0, "", fmt.Errorf("missing repository full_name")
	}
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return "", "", 0, "", fmt.Errorf("invalid repository full_name: %s", fullName)
	}
	workspace, repoSlug = parts[0], parts[1]

	pr, ok := payload["pullrequest"].(map[string]interface{})
	if !ok {
		return "", "", 0, "", fmt.Errorf("missing pullrequest in payload")
	}

	id, ok := pr["id"].(float64)
	if !ok {
		return "", "", 0, "", fmt.Errorf("missing pullrequest id")
	}
	prID = int(id)

	source, ok := pr["source"].(map[string]interface{})
	if !ok {
		return "", "", 0, "", fmt.Errorf("missing pullrequest source")
	}
	commit, ok := source["commit"].(map[string]interface{})
	if !ok {
		return "", "", 0, "", fmt.Errorf("missing source commit")
	}
	sha, ok = commit["hash"].(string)
	if !ok || sha == "" {
		return "", "", 0, "", fmt.Errorf("missing source commit hash")
	}

	return workspace, repoSlug, prID, sha, nil
}

func formatBitbucketComment(review string) string {
	return fmt.Sprintf("%s\n## Prism Code Review\n\n%s\n\n---\n*Reviewed by [Prism](https://github.com/andreatchori/prism) - self-hosted AI code review agent*", prismCommentMarker, review)
}

// verifyBitbucketSignature checks X-Hub-Signature when BITBUCKET_WEBHOOK_SECRET is set.
func verifyBitbucketSignature(body []byte, signatureHeader string) bool {
	secret := os.Getenv("BITBUCKET_WEBHOOK_SECRET")
	if secret == "" {
		return true
	}
	sig := strings.TrimSpace(signatureHeader)
	sig = strings.TrimPrefix(sig, "sha256=")
	expected, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), expected)
}
