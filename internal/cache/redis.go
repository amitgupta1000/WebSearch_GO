package cache

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisPingTimeout bounds the startup reachability check.
const redisPingTimeout = 5 * time.Second

// RedisCache stores cache entries in Redis, so results survive a restart and are
// shared by every instance of the server (several Cloud Run instances, or a
// restarted container, can reuse each other's search results and scraped pages).
//
// Expiry is delegated to Redis itself (`SET key value EX`), which is exact and
// needs no background sweeper.
type RedisCache struct {
	client *redis.Client
	prefix string
}

// NewRedisCache connects to Redis at url (redis:// or rediss://, as accepted by
// the Redis CLI) and verifies the connection. A failure here is returned to the
// caller so the server can fall back to the in-memory store instead of starting
// with a cache it cannot use.
func NewRedisCache(ctx context.Context, url, prefix string) (*RedisCache, error) {
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("redis cache URL is empty")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis cache URL: %w", err)
	}
	client := redis.NewClient(options)

	pingCtx, cancel := context.WithTimeout(ctx, redisPingTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect to redis cache: %w", err)
	}
	return &RedisCache{client: client, prefix: prefix}, nil
}

// Name identifies the backend in logs.
func (c *RedisCache) Name() string { return "redis" }

// key namespaces cache keys so one Redis database can be shared with other users
// of the same instance.
func (c *RedisCache) key(key string) string { return c.prefix + key }

// Get returns the cached value. A missing key is a miss; any other error is
// logged and treated as a miss so a Redis outage degrades to "no cache" rather
// than failing the run.
func (c *RedisCache) Get(ctx context.Context, key string) ([]byte, bool) {
	raw, err := c.client.Get(ctx, c.key(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false
	}
	if err != nil {
		log.Printf("[WARN] redis cache get failed for %s: %v", key, err)
		return nil, false
	}
	return raw, true
}

// Set stores value under key for ttl.
func (c *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	if err := c.client.Set(ctx, c.key(key), value, ttl).Err(); err != nil {
		log.Printf("[WARN] redis cache set failed for %s: %v", key, err)
	}
}

// Delete removes key.
func (c *RedisCache) Delete(ctx context.Context, key string) {
	if err := c.client.Del(ctx, c.key(key)).Err(); err != nil {
		log.Printf("[WARN] redis cache delete failed for %s: %v", key, err)
	}
}

// Close releases the connection pool.
func (c *RedisCache) Close() error { return c.client.Close() }
