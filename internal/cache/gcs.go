package cache

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/gcs"
)

// gcsDefaultPrefix namespaces cache objects inside the bucket. Configuration
// supplies the real value; this is the fallback when it is blank.
const gcsDefaultPrefix = "WebSearch_GO/cache/"

// GCSCache stores cache entries as objects in a Cloud Storage bucket, which makes
// the cache survive restarts and lets several instances share it.
//
// Cloud Storage has no per-object TTL, so every object carries its own deadline
// (see envelope) and is validated on read. Pair the bucket with a lifecycle rule —
// for example, delete objects older than 30 days — to reclaim the space that
// entries expire from logically.
type GCSCache struct {
	client *gcs.Client
	bucket string
	prefix string
}

// NewGCSCache builds a store backed by bucket using Application Default
// Credentials, so on Cloud Run it needs no key material.
func NewGCSCache(ctx context.Context, bucket, prefix string) (*GCSCache, error) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return nil, errors.New("GCS cache bucket is not configured")
	}
	client, err := gcs.NewClient(ctx, gcs.ReadWriteScope)
	if err != nil {
		return nil, err
	}
	return newGCSCacheWithClient(client, bucket, prefix), nil
}

// newGCSCacheWithClient wires a store around an already-built client. Tests use it
// with a stub server, which is what keeps the backend testable without credentials.
func newGCSCacheWithClient(client *gcs.Client, bucket, prefix string) *GCSCache {
	if prefix = strings.TrimSpace(prefix); prefix == "" {
		prefix = gcsDefaultPrefix
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &GCSCache{client: client, bucket: bucket, prefix: prefix}
}

// Name identifies the backend in logs.
func (c *GCSCache) Name() string { return "gcs" }

// objectName maps a cache key onto an object path inside the bucket.
func (c *GCSCache) objectName(key string) string { return c.prefix + key }

// Get downloads and unwraps one entry. A missing object, an expired envelope, or a
// transport failure all read as a miss, which keeps the caller's behaviour
// identical to the in-memory store.
func (c *GCSCache) Get(ctx context.Context, key string) ([]byte, bool) {
	raw, found, err := c.client.Download(ctx, c.bucket, c.objectName(key))
	if err != nil {
		log.Printf("[WARN] gcs cache get failed for %s: %v", key, err)
		return nil, false
	}
	if !found {
		return nil, false
	}
	value, ok := decodeEnvelope(raw)
	if !ok {
		// Expired entries are left in place for the bucket lifecycle rule to
		// reclaim; deleting on read would turn a read path into a write path.
		return nil, false
	}
	return value, true
}

// Set uploads value as a JSON-envelope object carrying its own expiry.
func (c *GCSCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	payload := encodeEnvelope(value, ttl)
	if payload == nil {
		return
	}
	if err := c.client.Upload(ctx, c.bucket, c.objectName(key), "application/json", payload); err != nil {
		log.Printf("[WARN] gcs cache set failed for %s: %v", key, err)
	}
}

// Delete removes one entry.
func (c *GCSCache) Delete(ctx context.Context, key string) {
	if err := c.client.Delete(ctx, c.bucket, c.objectName(key)); err != nil {
		log.Printf("[WARN] gcs cache delete failed for %s: %v", key, err)
	}
}

// Close is a no-op: object storage holds no session state, and the shared client
// may be used by the output exporter as well.
func (c *GCSCache) Close() error { return nil }

// describe is used in tests and diagnostics.
func (c *GCSCache) describe() string {
	return fmt.Sprintf("gcs bucket %s prefix %s", c.bucket, c.prefix)
}
