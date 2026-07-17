package platforms

import (
	"net/http"
	"testing"
	"time"
)

func TestTTLCacheSeen(t *testing.T) {
	c := newTTLCache(time.Minute, 100)
	if c.seen("a") {
		t.Error("first occurrence should not be seen")
	}
	if !c.seen("a") {
		t.Error("second occurrence should be seen")
	}
	if c.seen("b") {
		t.Error("different key should not be seen")
	}
}

func TestTTLCacheExpiry(t *testing.T) {
	c := newTTLCache(10*time.Millisecond, 100)
	if c.seen("a") {
		t.Error("first should not be seen")
	}
	time.Sleep(20 * time.Millisecond)
	if c.seen("a") {
		t.Error("expired entry should not be seen")
	}
}

func TestDeliveryID(t *testing.T) {
	h := http.Header{}
	h.Set("X-GitHub-Delivery", "gh-123")
	h.Set("X-Gitlab-Event-UUID", "gl-456")
	h.Set("X-VSS-ActivityId", "az-789")
	h.Set("X-Request-UUID", "bb-abc")

	cases := map[string]string{
		"github":    "gh-123",
		"gitlab":    "gl-456",
		"azure":     "az-789",
		"bitbucket": "bb-abc",
		"unknown":   "",
	}
	for platform, want := range cases {
		if got := deliveryID(platform, h); got != want {
			t.Errorf("deliveryID(%s) = %q, want %q", platform, got, want)
		}
	}
}

func TestIsDuplicateDeliveryNoHeader(t *testing.T) {
	// No delivery header -> never a duplicate.
	if isDuplicateDelivery("github", http.Header{}) {
		t.Error("missing delivery id should not be treated as duplicate")
	}
}
