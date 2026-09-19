package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type cacheEntry struct {
	value      any
	expiration time.Time
}

// MemoryCache provides thread-safe TTL caching for search queries and scraped web content.
type MemoryCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
}

// NewMemoryCache initializes an in-memory cache.
func NewMemoryCache() *MemoryCache {
	c := &MemoryCache{
		entries: make(map[string]cacheEntry),
	}
	// Background garbage collection routine
	go c.startCleanup(10 * time.Minute)
	return c
}

// HashKey generates a deterministic SHA-256 hash for queries or URLs.
func HashKey(prefix, input string) string {
	h := sha256.Sum256([]byte(input))
	return prefix + ":" + hex.EncodeToString(h[:])
}

// Get retrieves an item if present and not expired.
func (c *MemoryCache) Get(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[key]
	if !exists {
		return nil, false
	}

	if time.Now().After(entry.expiration) {
		return nil, false
	}

	return entry.value, true
}

// Set saves an item with a specific Time-To-Live duration.
func (c *MemoryCache) Set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = cacheEntry{
		value:      value,
		expiration: time.Now().Add(ttl),
	}
}

func (c *MemoryCache) startCleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.expiration) {
				delete(c.entries, k)
			}
		}
		c.mu.Unlock()
	}
}
