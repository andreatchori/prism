package platforms

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestFormatCommentHasMarker(t *testing.T) {
	out := formatComment("hello")
	if !strings.Contains(out, prismCommentMarker) {
		t.Error("expected formatted comment to contain the hidden marker")
	}
	if !strings.Contains(out, "hello") {
		t.Error("expected formatted comment to contain the review body")
	}
}

func TestExtractPRInfo(t *testing.T) {
	payload := map[string]interface{}{
		"repository": map[string]interface{}{
			"full_name": "andreatchori/prism-sandbox",
		},
		"pull_request": map[string]interface{}{
			"number": float64(7),
			"head": map[string]interface{}{
				"sha": "abc123def456",
			},
		},
	}

	owner, repo, n, sha, err := ExtractPRInfo(payload)
	if err != nil {
		t.Fatalf("ExtractPRInfo() error: %v", err)
	}
	if owner != "andreatchori" || repo != "prism-sandbox" {
		t.Errorf("got %s/%s, want andreatchori/prism-sandbox", owner, repo)
	}
	if n != 7 {
		t.Errorf("number = %d, want 7", n)
	}
	if sha != "abc123def456" {
		t.Errorf("sha = %q, want abc123def456", sha)
	}
}

func TestExtractPRInfoMissingRepo(t *testing.T) {
	_, _, _, _, err := ExtractPRInfo(map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVerifyGitHubSignature(t *testing.T) {
	t.Setenv("GITHUB_WEBHOOK_SECRET", "test-secret")
	body := []byte(`{"action":"opened"}`)

	mac := hmac.New(sha256.New, []byte("test-secret"))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !verifyGitHubSignature(body, sig) {
		t.Error("expected valid signature to pass")
	}
	if verifyGitHubSignature(body, "sha256=deadbeef") {
		t.Error("expected invalid signature to fail")
	}
	if verifyGitHubSignature(body, "") {
		t.Error("expected empty signature to fail when secret is set")
	}
}

func TestVerifyGitHubSignatureSkippedWithoutSecret(t *testing.T) {
	os.Unsetenv("GITHUB_WEBHOOK_SECRET")
	if !verifyGitHubSignature([]byte("x"), "") {
		t.Error("expected skip when secret is unset")
	}
}

func TestTruncateDiff(t *testing.T) {
	diff := "a\nb\nc\nd\ne"
	got := truncateDiff(diff, 3)
	if got != "a\nb\nc" {
		t.Errorf("got %q, want a\\nb\\nc", got)
	}
	if truncateDiff(diff, 0) != diff {
		t.Error("maxLines=0 should not truncate")
	}
}

func TestExtractMRInfo(t *testing.T) {
	payload := map[string]interface{}{
		"object_kind": "merge_request",
		"project": map[string]interface{}{
			"id":                  float64(42),
			"path_with_namespace": "group/prism-sandbox",
		},
		"object_attributes": map[string]interface{}{
			"iid":    float64(3),
			"action": "open",
			"last_commit": map[string]interface{}{
				"id": "deadbeef",
			},
		},
	}

	projectID, iid, sha, err := ExtractMRInfo(payload)
	if err != nil {
		t.Fatalf("ExtractMRInfo() error: %v", err)
	}
	if projectID != "group/prism-sandbox" {
		t.Errorf("projectID = %q", projectID)
	}
	if iid != 3 {
		t.Errorf("iid = %d, want 3", iid)
	}
	if sha != "deadbeef" {
		t.Errorf("sha = %q", sha)
	}
}

func TestVerifyGitLabToken(t *testing.T) {
	t.Setenv("GITLAB_WEBHOOK_SECRET", "gitlab-secret")
	if !verifyGitLabToken("gitlab-secret") {
		t.Error("expected matching token to pass")
	}
	if verifyGitLabToken("wrong") {
		t.Error("expected wrong token to fail")
	}
}

func TestVerifyGitLabTokenSkippedWithoutSecret(t *testing.T) {
	os.Unsetenv("GITLAB_WEBHOOK_SECRET")
	if !verifyGitLabToken("") {
		t.Error("expected skip when secret is unset")
	}
}

func TestExtractAzurePRInfo(t *testing.T) {
	t.Setenv("AZURE_DEVOPS_ORG", "fallback-org")
	payload := map[string]interface{}{
		"eventType": "git.pullrequest.created",
		"resource": map[string]interface{}{
			"pullRequestId": float64(12),
			"repository": map[string]interface{}{
				"id":   "repo-guid",
				"name": "my-repo",
				"project": map[string]interface{}{
					"name": "MyProject",
				},
			},
		},
		"resourceContainers": map[string]interface{}{
			"account": map[string]interface{}{
				"baseUrl": "https://dev.azure.com/acme/",
			},
		},
	}

	org, project, repoID, prID, err := ExtractAzurePRInfo(payload)
	if err != nil {
		t.Fatalf("ExtractAzurePRInfo() error: %v", err)
	}
	if org != "acme" {
		t.Errorf("org = %q, want acme", org)
	}
	if project != "MyProject" {
		t.Errorf("project = %q", project)
	}
	if repoID != "repo-guid" {
		t.Errorf("repoID = %q", repoID)
	}
	if prID != 12 {
		t.Errorf("prID = %d", prID)
	}
}

func TestParseAzureOrg(t *testing.T) {
	if got := parseAzureOrg("https://dev.azure.com/fabrikam/", ""); got != "fabrikam" {
		t.Errorf("got %q", got)
	}
	if got := parseAzureOrg("https://contoso.visualstudio.com", ""); got != "contoso" {
		t.Errorf("got %q", got)
	}
}

func TestExtractBitbucketPRInfo(t *testing.T) {
	payload := map[string]interface{}{
		"repository": map[string]interface{}{
			"full_name": "team/sandbox",
		},
		"pullrequest": map[string]interface{}{
			"id": float64(9),
			"source": map[string]interface{}{
				"commit": map[string]interface{}{
					"hash": "abcdef123456",
				},
			},
		},
	}

	ws, slug, id, sha, err := ExtractBitbucketPRInfo(payload)
	if err != nil {
		t.Fatalf("ExtractBitbucketPRInfo() error: %v", err)
	}
	if ws != "team" || slug != "sandbox" {
		t.Errorf("got %s/%s", ws, slug)
	}
	if id != 9 || sha != "abcdef123456" {
		t.Errorf("id=%d sha=%s", id, sha)
	}
}

func TestVerifyAzureBasicAuth(t *testing.T) {
	t.Setenv("AZURE_WEBHOOK_SECRET", "azure-secret")
	encoded := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:azure-secret"))
	if !verifyAzureBasicAuth(encoded) {
		t.Error("expected valid basic auth to pass")
	}
	if verifyAzureBasicAuth("Basic " + base64.StdEncoding.EncodeToString([]byte("user:wrong"))) {
		t.Error("expected wrong password to fail")
	}
}

func TestVerifyBitbucketSignature(t *testing.T) {
	t.Setenv("BITBUCKET_WEBHOOK_SECRET", "bb-secret")
	body := []byte(`{"pullrequest":{}}`)
	mac := hmac.New(sha256.New, []byte("bb-secret"))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	if !verifyBitbucketSignature(body, sig) {
		t.Error("expected valid signature to pass")
	}
	if !verifyBitbucketSignature(body, "sha256="+sig) {
		t.Error("expected sha256= prefix to pass")
	}
}
