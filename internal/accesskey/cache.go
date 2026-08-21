package accesskey

import (
	"sync"
	"time"
)

const (
	defaultCacheTTL     = 30 * time.Second
	defaultCacheEntries = 4096
)

// cache memoizes successful access-key digest lookups. Every /v1 request
// authenticated with a DB round trip, which serialized behind the single
// writer connection; caching removes that from the hot path.
//
// Only successful lookups are stored. Caching misses would let an unauthenticated
// caller fill the map with arbitrary digests, and a miss already costs one
// indexed read rather than a write-lock wait.
type cache struct {
	ttl        time.Duration
	maxEntries int

	mu      sync.RWMutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	identity  AccessKeyIdentity
	expiresAt time.Time
}

func newCache(ttl time.Duration, maxEntries int) *cache {
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}
	if maxEntries <= 0 {
		maxEntries = defaultCacheEntries
	}
	return &cache{ttl: ttl, maxEntries: maxEntries, entries: make(map[string]cacheEntry)}
}

func (c *cache) lookup(digest []byte, now time.Time) (AccessKeyIdentity, bool) {
	if c == nil {
		return AccessKeyIdentity{}, false
	}
	c.mu.RLock()
	entry, ok := c.entries[string(digest)]
	c.mu.RUnlock()
	if !ok || !now.Before(entry.expiresAt) {
		return AccessKeyIdentity{}, false
	}
	return entry.identity, true
}

func (c *cache) store(digest []byte, identity AccessKeyIdentity, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxEntries {
		c.evictLocked(now)
	}
	c.entries[string(digest)] = cacheEntry{identity: identity, expiresAt: now.Add(c.ttl)}
}

// invalidate drops every entry. Revocation only carries a key ID while entries
// are addressed by digest, and admin mutations are rare enough that a full
// clear costs less than maintaining a reverse index.
func (c *cache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cacheEntry)
}

// drop removes a single entry. Used where the digest is already in hand — one
// client looping with an expired key must not flush every other key's identity
// and push the whole instance back onto the writer-contended database read that
// this cache exists to avoid.
func (c *cache) drop(digest []byte) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, string(digest))
}

func (c *cache) size() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// evictLocked first drops expired entries, and only if that frees nothing does
// it drop arbitrary live ones, so a full cache still admits new keys.
func (c *cache) evictLocked(now time.Time) {
	for digest, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			delete(c.entries, digest)
		}
	}
	if len(c.entries) < c.maxEntries {
		return
	}
	drop := len(c.entries) - c.maxEntries + 1
	for digest := range c.entries {
		if drop == 0 {
			break
		}
		delete(c.entries, digest)
		drop--
	}
}
