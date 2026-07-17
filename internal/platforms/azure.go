package platforms

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/andreatchori/prism/internal/reviewer"
)

type AzureClient struct {
	pat    string
	org    string
	client *http.Client
}

type azureIterationList struct {
	Value []struct {
		ID              int `json:"id"`
		SourceRefCommit struct {
			CommitID string `json:"commitId"`
		} `json:"sourceRefCommit"`
	} `json:"value"`
}

type azureChanges struct {
	ChangeEntries []struct {
		ChangeType string `json:"changeType"`
		Item       struct {
			Path     string `json:"path"`
			IsFolder bool   `json:"isFolder"`
			ObjectID string `json:"objectId"`
		} `json:"item"`
	} `json:"changeEntries"`
}

// azureMaxFiles caps how many changed files we fetch content for, to keep the
// review payload and API usage bounded.
const azureMaxFiles = 50

// azureMaxFileBytes skips files larger than this when building the diff.
const azureMaxFileBytes = 200 * 1024

func NewAzureClient() *AzureClient {
	return &AzureClient{
		pat:    os.Getenv("AZURE_DEVOPS_PAT"),
		org:    os.Getenv("AZURE_DEVOPS_ORG"),
		client: newHTTPClient(45 * time.Second),
	}
}

func (a *AzureClient) authHeader() string {
	token := base64.StdEncoding.EncodeToString([]byte(":" + a.pat))
	return "Basic " + token
}

// FetchDiff builds a unified-diff-like view of the latest PR iteration.
//
// Azure does not expose a raw unified diff cheaply, so for added/edited files we
// fetch the new file content at the source commit and emit it as an all-added
// hunk. This gives Ollama real code and lets the Rust engine (which scans added
// lines) run. Deleted files and folders are listed only.
func (a *AzureClient) FetchDiff(org, project, repoID string, prID int) (string, error) {
	if org == "" {
		org = a.org
	}

	iterationID, sourceCommit, err := a.latestIteration(org, project, repoID, prID)
	if err != nil {
		return "", err
	}

	changes, err := a.iterationChanges(org, project, repoID, prID, iterationID)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fetched := 0
	for _, entry := range changes.ChangeEntries {
		path := entry.Item.Path
		if path == "" || entry.Item.IsFolder {
			continue
		}

		changeType := strings.ToLower(entry.ChangeType)
		if strings.Contains(changeType, "delete") {
			b.WriteString(fmt.Sprintf("diff --git a%s b%s\n", path, path))
			b.WriteString(fmt.Sprintf("--- a%s\n+++ /dev/null\n", path))
			continue
		}

		if fetched >= azureMaxFiles {
			b.WriteString(fmt.Sprintf("# skipped %s (file limit reached)\n", path))
			continue
		}

		content, err := a.fileContent(org, project, repoID, path, sourceCommit)
		if err != nil {
			log.Printf("Azure: could not fetch %s: %v", path, err)
			b.WriteString(fmt.Sprintf("diff --git a%s b%s\n# [%s] content unavailable\n", path, path, entry.ChangeType))
			continue
		}
		fetched++

		writeSyntheticDiff(&b, path, content, strings.Contains(changeType, "add"))
	}

	if b.Len() == 0 {
		return fmt.Sprintf("# Azure DevOps PR #%d: no reviewable file changes\n", prID), nil
	}
	return b.String(), nil
}

// writeSyntheticDiff emits an all-added unified diff block for a file's content.
func writeSyntheticDiff(b *strings.Builder, path, content string, isNew bool) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	// Drop a trailing empty element from a final newline
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	b.WriteString(fmt.Sprintf("diff --git a%s b%s\n", path, path))
	if isNew {
		b.WriteString("--- /dev/null\n")
	} else {
		b.WriteString(fmt.Sprintf("--- a%s\n", path))
	}
	b.WriteString(fmt.Sprintf("+++ b%s\n", path))
	b.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", len(lines)))
	for _, l := range lines {
		b.WriteString("+")
		b.WriteString(l)
		b.WriteString("\n")
	}
}

func (a *AzureClient) latestIteration(org, project, repoID string, prID int) (int, string, error) {
	iterationsURL := fmt.Sprintf(
		"https://dev.azure.com/%s/%s/_apis/git/repositories/%s/pullRequests/%d/iterations?api-version=7.1",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repoID), prID,
	)

	req, err := http.NewRequest("GET", iterationsURL, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", a.authHeader())

	resp, err := a.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("failed to list iterations: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, "", fmt.Errorf("Azure DevOps iterations status %d: %s", resp.StatusCode, string(body))
	}

	var iters azureIterationList
	if err := json.NewDecoder(resp.Body).Decode(&iters); err != nil {
		return 0, "", fmt.Errorf("failed to decode iterations: %w", err)
	}
	if len(iters.Value) == 0 {
		return 0, "", fmt.Errorf("no iterations found for PR %d", prID)
	}
	last := iters.Value[len(iters.Value)-1]
	return last.ID, last.SourceRefCommit.CommitID, nil
}

func (a *AzureClient) iterationChanges(org, project, repoID string, prID, iterationID int) (*azureChanges, error) {
	changesURL := fmt.Sprintf(
		"https://dev.azure.com/%s/%s/_apis/git/repositories/%s/pullRequests/%d/iterations/%d/changes?api-version=7.1",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repoID), prID, iterationID,
	)

	req, err := http.NewRequest("GET", changesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", a.authHeader())

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch changes: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Azure DevOps changes status %d: %s", resp.StatusCode, string(body))
	}

	var changes azureChanges
	if err := json.NewDecoder(resp.Body).Decode(&changes); err != nil {
		return nil, fmt.Errorf("failed to decode changes: %w", err)
	}
	return &changes, nil
}

// fileContent fetches a file's raw text at a specific commit.
func (a *AzureClient) fileContent(org, project, repoID, path, commit string) (string, error) {
	endpoint := fmt.Sprintf(
		"https://dev.azure.com/%s/%s/_apis/git/repositories/%s/items?path=%s&api-version=7.1&$format=text",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repoID), url.QueryEscape(path),
	)
	if commit != "" {
		endpoint += "&versionDescriptor.version=" + url.QueryEscape(commit) + "&versionDescriptor.versionType=commit"
	}

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", a.authHeader())
	req.Header.Set("Accept", "text/plain")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("items status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, azureMaxFileBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// PostComment upserts a PR thread comment: updates the existing Prism comment
// when present, otherwise creates a new thread.
func (a *AzureClient) PostComment(org, project, repoID string, prID int, comment string) error {
	if org == "" {
		org = a.org
	}

	content := formatAzureComment(comment)

	threadID, commentID, err := a.findPrismThread(org, project, repoID, prID)
	if err != nil {
		log.Printf("Could not look up existing Azure thread (will create new): %v", err)
	}

	if threadID != 0 && commentID != 0 {
		return a.updateThreadComment(org, project, repoID, prID, threadID, commentID, content)
	}

	endpoint := fmt.Sprintf(
		"https://dev.azure.com/%s/%s/_apis/git/repositories/%s/pullRequests/%d/threads?api-version=7.1",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repoID), prID,
	)

	payload := fmt.Sprintf(`{
		"comments": [{"parentCommentId": 0, "content": %q, "commentType": 1}],
		"status": 1
	}`, content)

	req, err := http.NewRequest("POST", endpoint, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", a.authHeader())
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to post comment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Azure DevOps comment status %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("Comment posted on Azure DevOps PR #%d", prID)
	return nil
}

func (a *AzureClient) updateThreadComment(org, project, repoID string, prID, threadID, commentID int, content string) error {
	endpoint := fmt.Sprintf(
		"https://dev.azure.com/%s/%s/_apis/git/repositories/%s/pullRequests/%d/threads/%d/comments/%d?api-version=7.1",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repoID), prID, threadID, commentID,
	)

	payload := fmt.Sprintf(`{"content": %q}`, content)
	req, err := http.NewRequest("PATCH", endpoint, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", a.authHeader())
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to update comment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Azure DevOps update status %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("Comment updated on Azure DevOps PR #%d (thread %d)", prID, threadID)
	return nil
}

// findPrismThread returns the thread ID and comment ID of an existing Prism
// comment, or zeros if none.
func (a *AzureClient) findPrismThread(org, project, repoID string, prID int) (int, int, error) {
	endpoint := fmt.Sprintf(
		"https://dev.azure.com/%s/%s/_apis/git/repositories/%s/pullRequests/%d/threads?api-version=7.1",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repoID), prID,
	)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", a.authHeader())

	resp, err := a.client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, fmt.Errorf("list threads status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Value []struct {
			ID       int `json:"id"`
			Comments []struct {
				ID      int    `json:"id"`
				Content string `json:"content"`
			} `json:"comments"`
		} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, 0, err
	}

	for _, thread := range result.Value {
		for _, c := range thread.Comments {
			if strings.Contains(c.Content, prismCommentMarker) {
				return thread.ID, c.ID, nil
			}
		}
	}
	return 0, 0, nil
}

// PostSuggestions posts rule-based suggestions as inline PR threads anchored to a
// file/line. Azure DevOps has no one-click apply, so the code is shown as a
// copyable block. Existing Prism suggestion threads at the same file/line are
// skipped to avoid duplicates on subsequent pushes.
func (a *AzureClient) PostSuggestions(org, project, repoID string, prID int, suggestions []reviewer.Suggestion) error {
	if org == "" {
		org = a.org
	}
	if len(suggestions) == 0 {
		return nil
	}

	existing, err := a.listPrismSuggestionThreads(org, project, repoID, prID)
	if err != nil {
		log.Printf("Could not list existing Azure suggestion threads (will create new): %v", err)
		existing = map[string]bool{}
	}

	const maxInline = 30
	posted := 0
	for _, s := range suggestions {
		if posted >= maxInline {
			break
		}
		if s.File == "" || s.Line == 0 || strings.TrimSpace(s.Code) == "" {
			continue
		}
		filePath := ensureLeadingSlash(s.File)
		key := fmt.Sprintf("%s:%d", filePath, s.Line)
		if existing[key] {
			continue
		}

		if err := a.createSuggestionThread(org, project, repoID, prID, filePath, s.Line, degradedSuggestionBody(s)); err != nil {
			log.Printf("Failed to post Azure suggestion on PR #%d (%s): %v", prID, key, err)
			continue
		}
		posted++
	}

	if posted > 0 {
		log.Printf("Posted %d inline suggestion(s) on Azure DevOps PR #%d", posted, prID)
	}
	return nil
}

func (a *AzureClient) createSuggestionThread(org, project, repoID string, prID int, filePath string, line uint, content string) error {
	endpoint := fmt.Sprintf(
		"https://dev.azure.com/%s/%s/_apis/git/repositories/%s/pullRequests/%d/threads?api-version=7.1",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repoID), prID,
	)

	payload := struct {
		Comments []map[string]interface{} `json:"comments"`
		Status   int                      `json:"status"`
		Context  map[string]interface{}   `json:"threadContext"`
	}{
		Comments: []map[string]interface{}{
			{"parentCommentId": 0, "content": content, "commentType": 1},
		},
		Status: 1,
		Context: map[string]interface{}{
			"filePath":       filePath,
			"rightFileStart": map[string]int{"line": int(line), "offset": 1},
			"rightFileEnd":   map[string]int{"line": int(line), "offset": 1},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", endpoint, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", a.authHeader())
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create suggestion thread status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// listPrismSuggestionThreads returns the set of "file:line" keys already covered
// by a Prism suggestion thread.
func (a *AzureClient) listPrismSuggestionThreads(org, project, repoID string, prID int) (map[string]bool, error) {
	endpoint := fmt.Sprintf(
		"https://dev.azure.com/%s/%s/_apis/git/repositories/%s/pullRequests/%d/threads?api-version=7.1",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repoID), prID,
	)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", a.authHeader())

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list threads status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Value []struct {
			Comments []struct {
				Content string `json:"content"`
			} `json:"comments"`
			ThreadContext *struct {
				FilePath       string `json:"filePath"`
				RightFileStart *struct {
					Line int `json:"line"`
				} `json:"rightFileStart"`
			} `json:"threadContext"`
		} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	out := make(map[string]bool)
	for _, thread := range result.Value {
		if thread.ThreadContext == nil || thread.ThreadContext.RightFileStart == nil {
			continue
		}
		isPrism := false
		for _, c := range thread.Comments {
			if strings.Contains(c.Content, prismInlineMarker) {
				isPrism = true
				break
			}
		}
		if !isPrism {
			continue
		}
		key := fmt.Sprintf("%s:%d", thread.ThreadContext.FilePath, thread.ThreadContext.RightFileStart.Line)
		out[key] = true
	}
	return out, nil
}

func ensureLeadingSlash(p string) string {
	if p == "" || strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}

// SetPRStatus sets a pull request status
func (a *AzureClient) SetPRStatus(org, project, repoID string, prID int, success bool) error {
	if org == "" {
		org = a.org
	}
	endpoint := fmt.Sprintf(
		"https://dev.azure.com/%s/%s/_apis/git/repositories/%s/pullRequests/%d/statuses?api-version=7.1",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repoID), prID,
	)

	state := "succeeded"
	description := "Prism review passed"
	if !success {
		state = "failed"
		description = "Prism review failed - critical issues found"
	}

	payload := fmt.Sprintf(`{
		"state": %q,
		"description": %q,
		"context": {"name": "prism/review", "genre": "prism"}
	}`, state, description)

	req, err := http.NewRequest("POST", endpoint, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", a.authHeader())
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to set PR status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Azure DevOps status %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("Azure DevOps PR status set to: %s", state)
	return nil
}

// ExtractAzurePRInfo extracts org, project, repo id and PR id from a service hook payload
func ExtractAzurePRInfo(payload map[string]interface{}) (org, project, repoID string, prID int, err error) {
	resource, ok := payload["resource"].(map[string]interface{})
	if !ok {
		return "", "", "", 0, fmt.Errorf("missing resource in payload")
	}

	prIDFloat, ok := resource["pullRequestId"].(float64)
	if !ok {
		return "", "", "", 0, fmt.Errorf("missing pullRequestId")
	}
	prID = int(prIDFloat)

	repo, ok := resource["repository"].(map[string]interface{})
	if !ok {
		return "", "", "", 0, fmt.Errorf("missing repository")
	}

	repoID, _ = repo["id"].(string)
	if repoID == "" {
		repoID, _ = repo["name"].(string)
	}
	if repoID == "" {
		return "", "", "", 0, fmt.Errorf("missing repository id")
	}

	if proj, ok := repo["project"].(map[string]interface{}); ok {
		project, _ = proj["name"].(string)
	}
	if project == "" {
		return "", "", "", 0, fmt.Errorf("missing project name")
	}

	org = os.Getenv("AZURE_DEVOPS_ORG")
	if containers, ok := payload["resourceContainers"].(map[string]interface{}); ok {
		if account, ok := containers["account"].(map[string]interface{}); ok {
			if baseURL, ok := account["baseUrl"].(string); ok {
				org = parseAzureOrg(baseURL, org)
			}
		}
	}
	if org == "" {
		return "", "", "", 0, fmt.Errorf("missing organization (set AZURE_DEVOPS_ORG)")
	}

	return org, project, repoID, prID, nil
}

func parseAzureOrg(baseURL, fallback string) string {
	baseURL = strings.TrimSuffix(baseURL, "/")
	// https://dev.azure.com/{org} or https://{org}.visualstudio.com
	if strings.Contains(baseURL, "dev.azure.com/") {
		parts := strings.Split(baseURL, "dev.azure.com/")
		if len(parts) == 2 {
			org := strings.Split(parts[1], "/")[0]
			if org != "" {
				return org
			}
		}
	}
	if strings.Contains(baseURL, ".visualstudio.com") {
		host := strings.TrimPrefix(baseURL, "https://")
		host = strings.TrimPrefix(host, "http://")
		return strings.Split(host, ".")[0]
	}
	return fallback
}

func formatAzureComment(review string) string {
	return fmt.Sprintf("%s\n## Prism Code Review\n\n%s\n\n---\n*Reviewed by [Prism](https://github.com/andreatchori/prism) - self-hosted AI code review agent*", prismCommentMarker, review)
}

// verifyAzureBasicAuth checks optional Basic auth when AZURE_WEBHOOK_SECRET is set
// (username ignored; password must match the secret).
func verifyAzureBasicAuth(header string) bool {
	secret := os.Getenv("AZURE_WEBHOOK_SECRET")
	if secret == "" {
		return true
	}
	if !strings.HasPrefix(header, "Basic ") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Basic "))
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return false
	}
	return hmacEqualString(secret, parts[1])
}
