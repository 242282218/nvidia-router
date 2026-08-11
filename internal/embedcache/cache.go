// Package embedcache provides a bounded, in-memory exact-match cache for
// embedding requests. It is deliberately deterministic (cache key = model +
// input hash), so identical embeddings requests short-circuit the upstream
// call. It never sees raw input text for storage: callers pass a pre-hashed
// key, and the value is the validated embedding JSON the upstream produced.
package embedcache

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
)

// Cache is a goroutine-safe LRU keyed by a request fingerprint. It stores the
// serialized embedding response body for exact-repeat inputs.
type Cache struct {
	mu      sync.Mutex
	max     int
	entries map[string]*list.Element
	lru     *list.List
}

type entry struct {
	key      string
	response []byte
}

// New returns a bounded LRU cache holding at most maxEntries responses.
func New(maxEntries int) *Cache {
	if maxEntries <= 0 {
		maxEntries = 256
	}
	return &Cache{
		max:     maxEntries,
		entries: make(map[string]*list.Element, maxEntries),
		lru:     list.New(),
	}
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
	if element, ok := c.entries[key]; ok {
		element.Value.(*entry).response = response
		c.lru.MoveToFront(element)
		return
	}
	element := c.lru.PushFront(&entry{key: key, response: response})
	c.entries[key] = element
	for len(c.entries) > c.max {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		c.lru.Remove(oldest)
		delete(c.entries, oldest.Value.(*entry).key)
	}
}

// Len returns the current number of cached responses.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Fingerprint builds a deterministic, bounded cache key for a set of input
// texts under a model. It hashes the concatenation so the key stays short
// regardless of input size and never stores raw input text.
func Fingerprint(model string, inputs []string) string {
	hash := sha256.Sum256([]byte(model + "\x00" + strings.Join(inputs, "\x00")))
	return model + ":" + hex.EncodeToString(hash[:])
}
