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
)

type AzureClient struct {
	pat    string
	org    string
	client *http.Client
}

type azureIterationList struct {
	Value []struct {
		ID int `json:"id"`
	} `json:"value"`
}

type azureChanges struct {
	ChangeEntries []struct {
		ChangeType string `json:"changeType"`
		Item       struct {
			Path string `json:"path"`
		} `json:"item"`
	} `json:"changeEntries"`
}

func NewAzureClient() *AzureClient {
	return &AzureClient{
		pat:    os.Getenv("AZURE_DEVOPS_PAT"),
		org:    os.Getenv("AZURE_DEVOPS_ORG"),
		client: &http.Client{},
	}
}

func (a *AzureClient) authHeader() string {
	token := base64.StdEncoding.EncodeToString([]byte(":" + a.pat))
	return "Basic " + token
}

// FetchDiff builds a text summary of changed files in the latest PR iteration.
func (a *AzureClient) FetchDiff(org, project, repoID string, prID int) (string, error) {
	if org == "" {
		org = a.org
	}
	iterationsURL := fmt.Sprintf(
		"https://dev.azure.com/%s/%s/_apis/git/repositories/%s/pullRequests/%d/iterations?api-version=7.1",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repoID), prID,
	)

	req, err := http.NewRequest("GET", iterationsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", a.authHeader())

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to list iterations: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Azure DevOps iterations status %d: %s", resp.StatusCode, string(body))
	}

	var iters azureIterationList
	if err := json.NewDecoder(resp.Body).Decode(&iters); err != nil {
		return "", fmt.Errorf("failed to decode iterations: %w", err)
	}
	if len(iters.Value) == 0 {
		return "", fmt.Errorf("no iterations found for PR %d", prID)
	}
	iterationID := iters.Value[len(iters.Value)-1].ID

	changesURL := fmt.Sprintf(
		"https://dev.azure.com/%s/%s/_apis/git/repositories/%s/pullRequests/%d/iterations/%d/changes?api-version=7.1",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repoID), prID, iterationID,
	)

	req, err = http.NewRequest("GET", changesURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", a.authHeader())

	resp, err = a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch changes: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Azure DevOps changes status %d: %s", resp.StatusCode, string(body))
	}

	var changes azureChanges
	if err := json.NewDecoder(resp.Body).Decode(&changes); err != nil {
		return "", fmt.Errorf("failed to decode changes: %w", err)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Azure DevOps PR #%d changes (iteration %d)\n\n", prID, iterationID))
	for _, entry := range changes.ChangeEntries {
		path := entry.Item.Path
		if path == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("- [%s] %s\n", entry.ChangeType, path))
	}
	return b.String(), nil
}

// PostComment creates a PR thread comment
func (a *AzureClient) PostComment(org, project, repoID string, prID int, comment string) error {
	if org == "" {
		org = a.org
	}
	endpoint := fmt.Sprintf(
		"https://dev.azure.com/%s/%s/_apis/git/repositories/%s/pullRequests/%d/threads?api-version=7.1",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repoID), prID,
	)

	payload := fmt.Sprintf(`{
		"comments": [{"parentCommentId": 0, "content": %q, "commentType": 1}],
		"status": 1
	}`, formatAzureComment(comment))

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
	return fmt.Sprintf("## Prism Code Review\n\n%s\n\n---\n*Reviewed by [Prism](https://github.com/andreatchori/prism) - self-hosted AI code review agent*", review)
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
