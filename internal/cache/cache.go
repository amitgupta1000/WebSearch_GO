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

// MemoryCache provides thread-safe TTL caching for search queries and scraped web
// content. The number of entries is bounded by maxEntries: when the cache grows
// beyond that, the oldest entries are evicted FIFO so long-running processes
// cannot accumulate unbounded memory.
type MemoryCache struct {
	mu         sync.RWMutex
	entries    map[string]cacheEntry
	queue      []string // insertion order, used for FIFO eviction
	maxEntries int
	done       chan struct{} // closed by Close to stop the cleanup goroutine
}

// DefaultMaxEntries is used when a non-positive limit is provided.
const DefaultMaxEntries = 2048

// NewMemoryCache initializes an in-memory cache bounded to maxEntries entries
// (falling back to DefaultMaxEntries when maxEntries <= 0).
func NewMemoryCache(maxEntries int) *MemoryCache {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	c := &MemoryCache{
		entries:    make(map[string]cacheEntry),
		maxEntries: maxEntries,
		done:       make(chan struct{}),
	}
	// Background garbage collection routine
	go c.startCleanup(10 * time.Minute)
	return c
}

// Close stops the background cleanup goroutine. Safe to call multiple times.
func (c *MemoryCache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
		// already closed
	default:
		close(c.done)
	}
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

// Set saves an item with a specific Time-To-Live duration, evicting the oldest
// entries when the cache exceeds its configured maximum size.
func (c *MemoryCache) Set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[key]; !exists {
		c.queue = append(c.queue, key)
	}
	c.entries[key] = cacheEntry{
		value:      value,
		expiration: time.Now().Add(ttl),
	}

	for len(c.entries) > c.maxEntries && len(c.queue) > 0 {
		oldest := c.queue[0]
		c.queue = c.queue[1:]
		delete(c.entries, oldest)
	}
}

func (c *MemoryCache) startCleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
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
}
