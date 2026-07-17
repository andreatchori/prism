package platforms

import (
	"net/http"
	"strconv"
	"time"
)

const (
	defaultMaxHTTPRetries = 3
	maxRetryAfterWait     = 60 * time.Second
)

// newHTTPClient returns an *http.Client with the given timeout and a transport
// that transparently retries transient failures (HTTP 429 and 5xx), honoring
// the Retry-After header when present. Only requests with a rewindable body
// (created via http.NewRequest with a bytes/strings reader) are retried after a
// response; others fall back to a single attempt.
func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &retryTransport{
			base:       http.DefaultTransport,
			maxRetries: defaultMaxHTTPRetries,
		},
	}
}

type retryTransport struct {
	base       http.RoundTripper
	maxRetries int
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error

	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		if attempt > 0 {
			if req.GetBody != nil {
				body, berr := req.GetBody()
				if berr != nil {
					return resp, err
				}
				req.Body = body
			} else if req.Body != nil {
				// Body already consumed and not rewindable: don't risk a partial retry.
				return resp, err
			}
		}

		resp, err = t.base.RoundTrip(req)
		if err != nil {
			// Transport-level error (timeout, connection reset): retry.
			if attempt < t.maxRetries {
				select {
				case <-time.After(backoffDuration(attempt)):
					continue
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
			}
			return resp, err
		}

		if !shouldRetryStatus(resp.StatusCode) || attempt == t.maxRetries {
			return resp, nil
		}

		wait := retryAfter(resp, attempt)
		resp.Body.Close()

		select {
		case <-time.After(wait):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}

	return resp, err
}

func shouldRetryStatus(code int) bool {
	return code == http.StatusTooManyRequests ||
		code == http.StatusBadGateway ||
		code == http.StatusServiceUnavailable ||
		code == http.StatusGatewayTimeout
}

// retryAfter returns how long to wait before the next attempt, preferring the
// server-provided Retry-After header (seconds) and falling back to backoff.
func retryAfter(resp *http.Response, attempt int) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			d := time.Duration(secs) * time.Second
			if d > maxRetryAfterWait {
				return maxRetryAfterWait
			}
			return d
		}
	}
	return backoffDuration(attempt)
}

func backoffDuration(attempt int) time.Duration {
	// Exponential backoff: 1s, 2s, 4s, ... capped.
	d := time.Duration(1<<attempt) * time.Second
	if d > maxRetryAfterWait {
		return maxRetryAfterWait
	}
	return d
}
