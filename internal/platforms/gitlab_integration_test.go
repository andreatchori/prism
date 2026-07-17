package platforms

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/andreatchori/prism/internal/reviewer"
)

func TestGitLabGetDiffRefs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/projects/42/merge_requests/3" {
			_, _ = w.Write([]byte(`{"diff_refs":{"base_sha":"base","head_sha":"head","start_sha":"start"}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	t.Setenv("GITLAB_URL", srv.URL)
	t.Setenv("GITLAB_TOKEN", "test-token")

	gl := NewGitLabClient()
	refs, err := gl.GetDiffRefs("42", 3)
	if err != nil {
		t.Fatalf("GetDiffRefs error: %v", err)
	}
	if refs.BaseSHA != "base" || refs.HeadSHA != "head" || refs.StartSHA != "start" {
		t.Errorf("unexpected refs: %+v", refs)
	}
}

func TestGitLabPostSuggestionsUpsertAndResolve(t *testing.T) {
	var mu sync.Mutex
	var created, updated, resolved int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		base := "/api/v4/projects/42/merge_requests/3"
		switch {
		case r.Method == "GET" && r.URL.Path == base+"/discussions":
			// Existing Prism suggestion at main.go:5 (will be updated) and a stale
			// one at util.go:9 (will be resolved).
			_, _ = fmt.Fprintf(w, `[
				{"id":"disc-1","notes":[{"id":11,"body":%q,"position":{"new_path":"main.go","new_line":5}}]},
				{"id":"disc-2","notes":[{"id":22,"body":%q,"position":{"new_path":"util.go","new_line":9}}]}
			]`, prismSuggestionMarker+"\nold", prismSuggestionMarker+"\nstale")

		case r.Method == "POST" && r.URL.Path == base+"/discussions":
			created++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"disc-new"}`))

		case r.Method == "PUT" && r.URL.Path == base+"/discussions/disc-1/notes/11":
			updated++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":11}`))

		case r.Method == "PUT" && r.URL.Path == base+"/discussions/disc-2":
			if r.URL.Query().Get("resolved") != "true" {
				t.Errorf("expected resolved=true, got %q", r.URL.RawQuery)
			}
			resolved++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"disc-2"}`))

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("GITLAB_URL", srv.URL)
	t.Setenv("GITLAB_TOKEN", "test-token")

	gl := NewGitLabClient()
	refs := GitLabDiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}
	suggestions := []reviewer.Suggestion{
		{File: "main.go", Line: 5, EndLine: 5, Code: "fixed", Rationale: "r"}, // update disc-1
		{File: "new.go", Line: 2, EndLine: 2, Code: "brand new"},              // create
	}
	if err := gl.PostSuggestions("42", 3, refs, suggestions); err != nil {
		t.Fatalf("PostSuggestions error: %v", err)
	}

	if created != 1 {
		t.Errorf("created = %d, want 1", created)
	}
	if updated != 1 {
		t.Errorf("updated = %d, want 1", updated)
	}
	if resolved != 1 {
		t.Errorf("resolved = %d, want 1 (stale util.go:9)", resolved)
	}
}
