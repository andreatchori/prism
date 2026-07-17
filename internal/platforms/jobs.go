package platforms

import (
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"
)

const defaultMaxConcurrentReviews = 4

var (
	reviewSem     chan struct{}
	reviewSemOnce sync.Once

	reviewWG sync.WaitGroup

	inFlightMu sync.Mutex
	inFlight   = make(map[string]bool)
)

func semaphore() chan struct{} {
	reviewSemOnce.Do(func() {
		limit := defaultMaxConcurrentReviews
		if v := os.Getenv("PRISM_MAX_CONCURRENT_REVIEWS"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		reviewSem = make(chan struct{}, limit)
	})
	return reviewSem
}

// runReview executes fn on a background goroutine with panic recovery, a global
// concurrency limit, and per-PR de-duplication: if a review keyed by the same
// string is already running, the new one is skipped (the newer webhook typically
// carries the same head, and the running review will post the latest state).
func runReview(key string, fn func()) {
	inFlightMu.Lock()
	if inFlight[key] {
		inFlightMu.Unlock()
		slog.Info("review already in progress, skipping duplicate webhook", "pr", key)
		return
	}
	inFlight[key] = true
	inFlightMu.Unlock()

	reviewWG.Add(1)
	go func() {
		defer reviewWG.Done()
		defer func() {
			inFlightMu.Lock()
			delete(inFlight, key)
			inFlightMu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				slog.Error("recovered from panic during review", "pr", key, "panic", r)
			}
		}()

		sem := semaphore()
		sem <- struct{}{}
		defer func() { <-sem }()

		fn()
	}()
}

// WaitForReviews blocks until all in-flight reviews finish or the timeout elapses.
func WaitForReviews(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		reviewWG.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		slog.Warn("timed out waiting for in-flight reviews to finish")
	}
}
