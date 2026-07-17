package platforms

import (
	"net/http"
	"sync"
	"time"
)

const (
	deliveryTTL      = 10 * time.Minute
	maxDeliveryCache = 10000
)

// ttlCache is a small in-memory set of keys with per-entry expiry, used to
// de-duplicate webhook deliveries that providers may retry.
type ttlCache struct {
	mu    sync.Mutex
	items map[string]time.Time
	ttl   time.Duration
	max   int
}

func newTTLCache(ttl time.Duration, max int) *ttlCache {
	return &ttlCache{items: make(map[string]time.Time), ttl: ttl, max: max}
}

// seen reports whether key was already recorded (and still valid); otherwise it
// records the key and returns false.
func (c *ttlCache) seen(key string) bool {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	if exp, ok := c.items[key]; ok && now.Before(exp) {
		return true
	}

	c.purgeExpiredLocked(now)
	if len(c.items) >= c.max {
		// Cache full of live entries: drop everything rather than grow unbounded.
		c.items = make(map[string]time.Time)
	}
	c.items[key] = now.Add(c.ttl)
	return false
}

func (c *ttlCache) purgeExpiredLocked(now time.Time) {
	for k, exp := range c.items {
		if now.After(exp) {
			delete(c.items, k)
		}
	}
}

var seenDeliveries = newTTLCache(deliveryTTL, maxDeliveryCache)

// deliveryID extracts the provider's per-delivery identifier from the headers.
// Returns "" when the platform has no such header (dedup then disabled).
func deliveryID(platform string, h http.Header) string {
	switch platform {
	case "github":
		return h.Get("X-GitHub-Delivery")
	case "gitlab":
		return h.Get("X-Gitlab-Event-UUID")
	case "azure":
		return h.Get("X-VSS-ActivityId")
	case "bitbucket":
		return h.Get("X-Request-UUID")
	default:
		return ""
	}
}

// isDuplicateDelivery reports whether this exact webhook delivery was already
// processed recently. Deliveries without an identifier are never treated as
// duplicates.
func isDuplicateDelivery(platform string, h http.Header) bool {
	id := deliveryID(platform, h)
	if id == "" {
		return false
	}
	return seenDeliveries.seen(platform + ":" + id)
}
