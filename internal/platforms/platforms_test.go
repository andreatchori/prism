package platforms

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/andreatchori/prism/internal/engine"
	"github.com/andreatchori/prism/internal/reviewer"
)

func TestInlineCommentsFromSuggestions(t *testing.T) {
	suggestions := []reviewer.Suggestion{
		{File: "main.go", Line: 12, EndLine: 12, Code: "fmt.Println(x)", Rationale: "no debug print"},
		{File: "util.go", Line: 3, EndLine: 5, Code: "a\nb\nc", Rationale: "multi-line"},
		{File: "", Line: 1, Code: "x"},     // dropped (no file)
		{File: "z.go", Line: 0, Code: "x"}, // dropped (no line)
		{File: "z.go", Line: 4, Code: " "}, // dropped (empty code)
	}

	out := inlineCommentsFromSuggestions(suggestions)
	if len(out) != 2 {
		t.Fatalf("expected 2 inline suggestions, got %d", len(out))
	}
	if !strings.Contains(out[0].Body, "```suggestion\nfmt.Println(x)\n```") {
		t.Errorf("expected suggestion block, got %q", out[0].Body)
	}
	if !strings.Contains(out[0].Body, "no debug print") {
		t.Errorf("expected rationale in body, got %q", out[0].Body)
	}
	if out[1].StartLine != 3 || out[1].Line != 5 {
		t.Errorf("expected multi-line span 3..5, got start=%d line=%d", out[1].StartLine, out[1].Line)
	}
}

func TestWriteSyntheticDiff(t *testing.T) {
	var b strings.Builder
	writeSyntheticDiff(&b, "/src/main.go", "package main\nfunc main() {}\n", true)
	out := b.String()

	for _, want := range []string{
		"diff --git a/src/main.go b/src/main.go",
		"--- /dev/null",
		"+++ b/src/main.go",
		"@@ -0,0 +1,2 @@",
		"+package main",
		"+func main() {}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("synthetic diff missing %q:\n%s", want, out)
		}
	}
}

func TestInlineCommentsFromFindings(t *testing.T) {
	line := uint(7)
	findings := []engine.Finding{
		{Severity: "critical", Category: "security", Rule: "No secrets", File: "main.go", Line: &line},
		{Severity: "warning", Category: "perf", Rule: "loop alloc", File: "util.go", Line: nil}, // dropped (no line)
		{Severity: "critical", Category: "forbidden", Rule: "no unwrap", File: "", Line: &line}, // dropped (no file)
	}

	out := inlineCommentsFromFindings(findings)
	if len(out) != 1 {
		t.Fatalf("expected 1 inline comment, got %d", len(out))
	}
	if out[0].Path != "main.go" || out[0].Line != 7 {
		t.Errorf("unexpected inline target: %+v", out[0])
	}
	if !strings.Contains(out[0].Body, "CRITICAL") || !strings.Contains(out[0].Body, "No secrets") {
		t.Errorf("unexpected body: %q", out[0].Body)
	}
}

func TestGitlabSuggestionBody(t *testing.T) {
	line, body := gitlabSuggestionBody(reviewer.Suggestion{
		File: "main.go", Line: 12, EndLine: 12, Code: "fmt.Println(x)\n", Rationale: "no debug print",
	})
	if line != 12 {
		t.Errorf("anchor line = %d, want 12", line)
	}
	if !strings.Contains(body, "```suggestion:-0+0\nfmt.Println(x)\n```") {
		t.Errorf("expected single-line suggestion block, got %q", body)
	}
	if !strings.Contains(body, "no debug print") {
		t.Errorf("expected rationale, got %q", body)
	}
	if !strings.Contains(body, prismSuggestionMarker) {
		t.Errorf("expected hidden suggestion marker, got %q", body)
	}

	line, body = gitlabSuggestionBody(reviewer.Suggestion{
		File: "util.go", Line: 3, EndLine: 5, Code: "a\nb\nc",
	})
	if line != 3 {
		t.Errorf("multi-line anchor = %d, want 3", line)
	}
	if !strings.Contains(body, "```suggestion:-0+2\na\nb\nc\n```") {
		t.Errorf("expected multi-line span -0+2, got %q", body)
	}
}

func TestDegradedSuggestionBody(t *testing.T) {
	body := degradedSuggestionBody(reviewer.Suggestion{
		File: "main.go", Line: 5, EndLine: 5, Code: "log.Println(x)\n", Rationale: "use logger",
	})
	if !strings.Contains(body, prismInlineMarker) {
		t.Errorf("expected inline marker, got %q", body)
	}
	if !strings.Contains(body, "```\nlog.Println(x)\n```") {
		t.Errorf("expected plain code block, got %q", body)
	}
	if strings.Contains(body, "```suggestion") {
		t.Errorf("degraded body must not use a one-click suggestion block: %q", body)
	}
	if !strings.Contains(body, "use logger") {
		t.Errorf("expected rationale, got %q", body)
	}
}

func TestEnsureLeadingSlash(t *testing.T) {
	if got := ensureLeadingSlash("main.go"); got != "/main.go" {
		t.Errorf("got %q, want /main.go", got)
	}
	if got := ensureLeadingSlash("/src/main.go"); got != "/src/main.go" {
		t.Errorf("got %q, want unchanged", got)
	}
	if got := ensureLeadingSlash(""); got != "" {
		t.Errorf("empty should stay empty, got %q", got)
	}
}

func TestSuggestionKey(t *testing.T) {
	if got := suggestionKey("main.go", 12); got != "main.go:12" {
		t.Errorf("suggestionKey = %q, want main.go:12", got)
	}
	if suggestionKey("a.go", 1) == suggestionKey("a.go", 2) {
		t.Error("different lines should produce different keys")
	}
}

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
