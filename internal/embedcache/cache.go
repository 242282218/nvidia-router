// Package embedcache provides a bounded, in-memory exact-match cache for
// embedding requests. It is deliberately deterministic (cache key = SHA-256 of
// the normalized upstream request body), so identical embedding requests
// short-circuit the upstream call. It never sees raw input text for storage:
// callers pass a pre-hashed key, and the value is the validated embedding JSON.
package embedcache

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
)

const (
	defaultMaxEntries = 256
	// maxCacheBytes keeps the cache bounded even when every response reaches
	// the 32 MiB upstream validation limit.
	maxCacheBytes = 64 << 20
)

// Cache is a goroutine-safe LRU keyed by a request fingerprint. It stores the
// serialized embedding response body for exact-repeat requests.
type Cache struct {
	mu sync.Mutex
	// max is atomic so Resize can skip work (and avoid the mutex) when the
	// runtime setting is unchanged, which is the per-request steady state.
	max      atomic.Int32
	maxBytes int64
	bytes    int64
	entries  map[string]*list.Element
	lru      *list.List
}

type entry struct {
	key      string
	response []byte
}

// New returns a bounded LRU cache holding at most maxEntries responses.
func New(maxEntries int) *Cache {
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	cache := &Cache{
		maxBytes: maxCacheBytes,
		entries:  make(map[string]*list.Element, maxEntries),
		lru:      list.New(),
	}
	cache.max.Store(int32(maxEntries))
	return cache
}

// Get returns a cached response body for key, or nil on a miss.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	// LRU: most recently used moves to the front.
	c.lru.MoveToFront(element)
	return element.Value.(*entry).response, true
}

// Put stores response for key, evicting the least-recently-used entry when the
// cache exceeds its bound. Duplicate keys refresh position and value.
func (c *Cache) Put(key string, response []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	responseBytes := int64(len(response))
	if responseBytes > c.maxBytes {
		return
	}
	if element, ok := c.entries[key]; ok {
		cached := element.Value.(*entry)
		c.bytes += responseBytes - int64(len(cached.response))
		cached.response = response
		c.lru.MoveToFront(element)
		c.evictLocked()
		return
	}
	element := c.lru.PushFront(&entry{key: key, response: response})
	c.entries[key] = element
	c.bytes += responseBytes
	c.evictLocked()
}

func (c *Cache) evictLocked() {
	for len(c.entries) > int(c.max.Load()) || c.bytes > c.maxBytes {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		cached := oldest.Value.(*entry)
		c.bytes -= int64(len(cached.response))
		c.lru.Remove(oldest)
		delete(c.entries, cached.key)
	}
}

// Resize changes the entry bound and immediately removes excess LRU entries.
// The byte bound remains fixed so runtime setting changes cannot make the
// process retain an unbounded response set. The common per-request call with an
// unchanged value returns without taking the lock.
func (c *Cache) Resize(maxEntries int) {
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	if int(c.max.Load()) == maxEntries {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.max.Load() == int32(maxEntries) {
		return
	}
	c.max.Store(int32(maxEntries))
	c.evictLocked()
}

// Len returns the current number of cached responses.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Bytes returns the total serialized response size currently retained.
func (c *Cache) Bytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// Fingerprint builds a deterministic, bounded cache key from the complete
// normalized upstream request body. Hashing the body keeps all response-
// affecting fields in the key without storing raw input text.
func Fingerprint(requestBody []byte) string {
	hash := sha256.Sum256(requestBody)
	return hex.EncodeToString(hash[:])
}
