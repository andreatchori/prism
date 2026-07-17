package platforms

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestGitHubFetchDiffIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r/pulls/1" && r.Header.Get("Accept") == "application/vnd.github.v3.diff" {
			_, _ = w.Write([]byte("DIFF-CONTENT"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "test-token")

	gh := NewGitHubClient()
	diff, err := gh.FetchDiff("o", "r", 1)
	if err != nil {
		t.Fatalf("FetchDiff error: %v", err)
	}
	if diff != "DIFF-CONTENT" {
		t.Errorf("diff = %q, want DIFF-CONTENT", diff)
	}
}

func TestGitHubPostReviewUpsert(t *testing.T) {
	var mu sync.Mutex
	var patched, posted, deleted int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/o/r/pulls/1/comments":
			_, _ = fmt.Fprintf(w, `[
				{"id":100,"path":"main.go","line":5,"body":%q},
				{"id":200,"path":"util.go","line":9,"body":%q},
				{"id":300,"path":"other.go","line":1,"body":"not a prism comment"}
			]`, prismInlineMarker+"\nold body", prismInlineMarker+"\nstale")

		case r.Method == "PATCH" && r.URL.Path == "/repos/o/r/pulls/comments/100":
			patched++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":100}`))

		case r.Method == "POST" && r.URL.Path == "/repos/o/r/pulls/1/comments":
			posted++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":400}`))

		case r.Method == "DELETE" && r.URL.Path == "/repos/o/r/pulls/comments/200":
			deleted++
			w.WriteHeader(http.StatusNoContent)

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "test-token")

	gh := NewGitHubClient()
	comments := []InlineComment{
		{Path: "main.go", Line: 5, Body: prismInlineMarker + "\nnew body"}, // update id 100
		{Path: "foo.go", Line: 3, Body: prismInlineMarker + "\nbrand new"}, // create
	}
	if err := gh.PostReview("o", "r", 1, "sha123", comments); err != nil {
		t.Fatalf("PostReview error: %v", err)
	}

	if patched != 1 {
		t.Errorf("patched = %d, want 1", patched)
	}
	if posted != 1 {
		t.Errorf("posted = %d, want 1", posted)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (stale util.go:9)", deleted)
	}
}
