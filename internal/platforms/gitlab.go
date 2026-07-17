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
	"time"

	"github.com/andreatchori/prism/internal/reviewer"
)

// prismSuggestionMarker identifies inline suggestion discussions created by Prism
// so they can be updated in place instead of duplicated on every push.
const prismSuggestionMarker = "<!-- prism-suggestion -->"

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
		client:  &http.Client{Timeout: 30 * time.Second},
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

// PostComment upserts a note on the Merge Request: updates the existing Prism
// note when present, otherwise creates a new one.
func (g *GitLabClient) PostComment(projectID string, mrIID int, comment string) error {
	body := formatGitLabComment(comment)

	existingID, err := g.findPrismNote(projectID, mrIID)
	if err != nil {
		log.Printf("Could not look up existing GitLab note (will create new): %v", err)
	}

	base := fmt.Sprintf(
		"%s/api/v4/projects/%s/merge_requests/%d/notes",
		g.baseURL,
		url.PathEscape(projectID),
		mrIID,
	)

	method := "POST"
	endpoint := base
	wantStatus := http.StatusCreated
	if existingID != 0 {
		method = "PUT"
		endpoint = fmt.Sprintf("%s/%d", base, existingID)
		wantStatus = http.StatusOK
	}

	payload := fmt.Sprintf(`{"body": %q}`, body)
	req, err := http.NewRequest(method, endpoint, strings.NewReader(payload))
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

	if resp.StatusCode != wantStatus {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitLab API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	if existingID != 0 {
		log.Printf("Comment updated on GitLab MR !%d (note %d)", mrIID, existingID)
	} else {
		log.Printf("Comment posted on GitLab MR !%d", mrIID)
	}
	return nil
}

// findPrismNote returns the ID of an existing Prism note, or 0 if none.
func (g *GitLabClient) findPrismNote(projectID string, mrIID int) (int64, error) {
	endpoint := fmt.Sprintf(
		"%s/api/v4/projects/%s/merge_requests/%d/notes?per_page=100",
		g.baseURL,
		url.PathEscape(projectID),
		mrIID,
	)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return 0, err
	}
	g.setHeaders(req)

	resp, err := g.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("list notes status %d: %s", resp.StatusCode, string(respBody))
	}

	var notes []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&notes); err != nil {
		return 0, err
	}

	for _, n := range notes {
		if strings.Contains(n.Body, prismCommentMarker) {
			return n.ID, nil
		}
	}
	return 0, nil
}

// GitLabDiffRefs holds the SHAs required to position inline discussions.
type GitLabDiffRefs struct {
	BaseSHA  string `json:"base_sha"`
	HeadSHA  string `json:"head_sha"`
	StartSHA string `json:"start_sha"`
}

// GetDiffRefs fetches the MR's diff refs (base/head/start SHAs) needed to anchor
// inline suggestions to the diff.
func (g *GitLabClient) GetDiffRefs(projectID string, mrIID int) (GitLabDiffRefs, error) {
	endpoint := fmt.Sprintf(
		"%s/api/v4/projects/%s/merge_requests/%d",
		g.baseURL,
		url.PathEscape(projectID),
		mrIID,
	)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return GitLabDiffRefs{}, err
	}
	g.setHeaders(req)

	resp, err := g.client.Do(req)
	if err != nil {
		return GitLabDiffRefs{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return GitLabDiffRefs{}, fmt.Errorf("get MR status %d: %s", resp.StatusCode, string(body))
	}

	var mr struct {
		DiffRefs GitLabDiffRefs `json:"diff_refs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&mr); err != nil {
		return GitLabDiffRefs{}, err
	}
	return mr.DiffRefs, nil
}

// PostSuggestions posts rule-based, one-click applicable suggestions as inline
// discussions on the MR, using GitLab's `suggestion:-0+N` code-block syntax.
func (g *GitLabClient) PostSuggestions(projectID string, mrIID int, refs GitLabDiffRefs, suggestions []reviewer.Suggestion) error {
	if len(suggestions) == 0 {
		return nil
	}
	if refs.HeadSHA == "" || refs.BaseSHA == "" {
		return fmt.Errorf("missing diff refs; cannot post inline suggestions")
	}

	existing, err := g.listPrismSuggestions(projectID, mrIID)
	if err != nil {
		log.Printf("Could not list existing GitLab suggestions (will create new): %v", err)
		existing = map[string]gitlabExistingSuggestion{}
	}

	const maxInline = 30
	posted, updated := 0, 0
	handled := 0
	currentKeys := make(map[string]bool)
	for _, s := range suggestions {
		if handled >= maxInline {
			break
		}
		if s.File == "" || s.Line == 0 || strings.TrimSpace(s.Code) == "" {
			continue
		}

		line, body := gitlabSuggestionBody(s)
		key := suggestionKey(s.File, line)
		currentKeys[key] = true

		if prev, ok := existing[key]; ok {
			handled++
			if strings.TrimSpace(prev.body) == strings.TrimSpace(body) {
				continue
			}
			if err := g.updateDiscussionNote(projectID, mrIID, prev.discussionID, prev.noteID, body); err != nil {
				log.Printf("Failed to update inline suggestion on MR !%d (%s:%d): %v", mrIID, s.File, line, err)
				continue
			}
			updated++
			continue
		}

		if err := g.postDiscussion(projectID, mrIID, refs, s.File, line, body); err != nil {
			log.Printf("Failed to post inline suggestion on MR !%d (%s:%d): %v", mrIID, s.File, line, err)
			continue
		}
		handled++
		posted++
	}

	// Resolve stale Prism suggestions that no longer match any current proposal.
	resolved := 0
	for key, prev := range existing {
		if currentKeys[key] {
			continue
		}
		if err := g.resolveDiscussion(projectID, mrIID, prev.discussionID); err != nil {
			log.Printf("Failed to resolve stale suggestion on MR !%d (%s): %v", mrIID, key, err)
			continue
		}
		resolved++
	}

	if posted > 0 || updated > 0 || resolved > 0 {
		log.Printf("Inline suggestions on GitLab MR !%d: %d created, %d updated, %d resolved", mrIID, posted, updated, resolved)
	}
	return nil
}

type gitlabExistingSuggestion struct {
	discussionID string
	noteID       int64
	body         string
}

func suggestionKey(path string, line uint) string {
	return fmt.Sprintf("%s:%d", path, line)
}

// listPrismSuggestions returns existing Prism suggestion notes keyed by
// "path:new_line", so we can update them in place instead of duplicating.
func (g *GitLabClient) listPrismSuggestions(projectID string, mrIID int) (map[string]gitlabExistingSuggestion, error) {
	endpoint := fmt.Sprintf(
		"%s/api/v4/projects/%s/merge_requests/%d/discussions?per_page=100",
		g.baseURL,
		url.PathEscape(projectID),
		mrIID,
	)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	g.setHeaders(req)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list discussions status %d: %s", resp.StatusCode, string(respBody))
	}

	var discussions []struct {
		ID    string `json:"id"`
		Notes []struct {
			ID       int64  `json:"id"`
			Body     string `json:"body"`
			Position *struct {
				NewPath string `json:"new_path"`
				NewLine *int   `json:"new_line"`
			} `json:"position"`
		} `json:"notes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&discussions); err != nil {
		return nil, err
	}

	out := make(map[string]gitlabExistingSuggestion)
	for _, d := range discussions {
		for _, n := range d.Notes {
			if !strings.Contains(n.Body, prismSuggestionMarker) {
				continue
			}
			if n.Position == nil || n.Position.NewLine == nil || n.Position.NewPath == "" {
				continue
			}
			key := suggestionKey(n.Position.NewPath, uint(*n.Position.NewLine))
			out[key] = gitlabExistingSuggestion{
				discussionID: d.ID,
				noteID:       n.ID,
				body:         n.Body,
			}
		}
	}
	return out, nil
}

func (g *GitLabClient) updateDiscussionNote(projectID string, mrIID int, discussionID string, noteID int64, body string) error {
	endpoint := fmt.Sprintf(
		"%s/api/v4/projects/%s/merge_requests/%d/discussions/%s/notes/%d",
		g.baseURL,
		url.PathEscape(projectID),
		mrIID,
		discussionID,
		noteID,
	)

	payload := fmt.Sprintf(`{"body": %q}`, body)
	req, err := http.NewRequest("PUT", endpoint, strings.NewReader(payload))
	if err != nil {
		return err
	}
	g.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update discussion note status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// resolveDiscussion marks a whole discussion thread as resolved.
func (g *GitLabClient) resolveDiscussion(projectID string, mrIID int, discussionID string) error {
	endpoint := fmt.Sprintf(
		"%s/api/v4/projects/%s/merge_requests/%d/discussions/%s?resolved=true",
		g.baseURL,
		url.PathEscape(projectID),
		mrIID,
		discussionID,
	)

	req, err := http.NewRequest("PUT", endpoint, nil)
	if err != nil {
		return err
	}
	g.setHeaders(req)

	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("resolve discussion status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// gitlabSuggestionBody returns the new-file line the comment anchors to and the
// note body carrying a GitLab suggestion block spanning the suggestion's range.
func gitlabSuggestionBody(s reviewer.Suggestion) (uint, string) {
	startLine := s.Line
	endLine := s.EndLine
	if endLine < startLine {
		endLine = startLine
	}
	below := endLine - startLine

	code := strings.TrimRight(s.Code, "\n")
	body := prismSuggestionMarker + "\n**Prism suggestion** (rule-based)"
	if r := strings.TrimSpace(s.Rationale); r != "" {
		body += ": " + r
	}
	body += fmt.Sprintf("\n\n```suggestion:-0+%d\n%s\n```", below, code)
	return startLine, body
}

func (g *GitLabClient) postDiscussion(projectID string, mrIID int, refs GitLabDiffRefs, path string, newLine uint, body string) error {
	type position struct {
		BaseSHA      string `json:"base_sha"`
		StartSHA     string `json:"start_sha"`
		HeadSHA      string `json:"head_sha"`
		PositionType string `json:"position_type"`
		NewPath      string `json:"new_path"`
		OldPath      string `json:"old_path"`
		NewLine      uint   `json:"new_line"`
	}
	payload := struct {
		Body     string   `json:"body"`
		Position position `json:"position"`
	}{
		Body: body,
		Position: position{
			BaseSHA:      refs.BaseSHA,
			StartSHA:     refs.StartSHA,
			HeadSHA:      refs.HeadSHA,
			PositionType: "text",
			NewPath:      path,
			OldPath:      path,
			NewLine:      newLine,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf(
		"%s/api/v4/projects/%s/merge_requests/%d/discussions",
		g.baseURL,
		url.PathEscape(projectID),
		mrIID,
	)
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	g.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create discussion status %d: %s", resp.StatusCode, string(respBody))
	}
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
	return fmt.Sprintf("%s\n## Prism Code Review\n\n%s\n\n---\n*Reviewed by [Prism](https://github.com/andreatchori/prism) - self-hosted AI code review agent*", prismCommentMarker, review)
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
