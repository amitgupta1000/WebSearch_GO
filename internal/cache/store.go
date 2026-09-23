package cache

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"
)

// Store is the cache surface used by the research engine. It is deliberately
// small and byte-oriented so the same engine code can run against an in-process
// map, Redis, or object storage.
//
// Implementations do not return errors: a cache is an optimization, so a failed
// lookup or write must degrade to a miss (logged by the implementation) rather
// than break a research run.
type Store interface {
	// Get returns the cached bytes for key and whether they were present and
	// unexpired. The returned slice must be treated as read-only.
	Get(ctx context.Context, key string) ([]byte, bool)
	// Set stores value under key for ttl. A non-positive ttl stores nothing.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration)
	// Delete removes key. Deleting an absent key is not an error.
	Delete(ctx context.Context, key string)
	// Close releases resources held by the store.
	Close() error
	// Name identifies the backend in logs and run reports (e.g. "memory").
	Name() string
}

// GetJSON decodes the value stored at key into target. It reports false when the
// key is missing or expired, or when the stored value is not valid JSON for
// target (which a backend change or a schema change can both cause).
func GetJSON(ctx context.Context, store Store, key string, target any) bool {
	if store == nil {
		return false
	}
	raw, ok := store.Get(ctx, key)
	if !ok {
		return false
	}
	return json.Unmarshal(raw, target) == nil
}

// SetJSON encodes value as JSON and stores it under key for ttl.
func SetJSON(ctx context.Context, store Store, key string, value any, ttl time.Duration) {
	if store == nil {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	store.Set(ctx, key, raw, ttl)
}

// envelope wraps a byte payload with its own expiry timestamp.
//
// Object stores (GCS) have no per-object TTL, so an entry has to carry its own
// deadline; decoding it in a single read keeps the backend to one round trip per
// lookup. Backends with native TTLs (memory, Redis) store the raw payload
// instead, which is why this type lives here rather than in the GCS backend.
type envelope struct {
	ExpiresAt int64  `json:"expires_at"`
	Value     string `json:"value"` // standard base64
}

// encodeEnvelope wraps value with an absolute expiry derived from ttl.
func encodeEnvelope(value []byte, ttl time.Duration) []byte {
	wrapped := envelope{
		ExpiresAt: time.Now().Add(ttl).Unix(),
		Value:     base64.StdEncoding.EncodeToString(value),
	}
	raw, err := json.Marshal(wrapped)
	if err != nil {
		return nil
	}
	return raw
}

// decodeEnvelope unwraps a payload written by encodeEnvelope. It reports false
// for data that is malformed or past its expiry.
func decodeEnvelope(raw []byte) ([]byte, bool) {
	var wrapped envelope
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, false
	}
	if wrapped.ExpiresAt > 0 && time.Now().Unix() >= wrapped.ExpiresAt {
		return nil, false
	}
	value, err := base64.StdEncoding.DecodeString(wrapped.Value)
	if err != nil {
		return nil, false
	}
	return value, true
}
