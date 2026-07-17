package platforms

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRetryTransportRetriesOn429(t *testing.T) {
	var mu sync.Mutex
	var attempts int
	var bodies []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))

		if attempts < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := newHTTPClient(5 * time.Second)
	req, err := http.NewRequest("POST", srv.URL, strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	for i, b := range bodies {
		if b != "payload" {
			t.Errorf("attempt %d body = %q, want payload (body should be rewound)", i, b)
		}
	}
}

func TestRetryTransportNoRetryOn200(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := newHTTPClient(5 * time.Second)
	resp, err := client.Do(mustReq(t, "GET", srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on success)", attempts)
	}
}

func TestRetryTransportGivesUpAfterMax(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := newHTTPClient(5 * time.Second)
	resp, err := client.Do(mustReq(t, "GET", srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	// 1 initial + defaultMaxHTTPRetries retries
	if attempts != defaultMaxHTTPRetries+1 {
		t.Errorf("attempts = %d, want %d", attempts, defaultMaxHTTPRetries+1)
	}
}

func mustReq(t *testing.T, method, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
