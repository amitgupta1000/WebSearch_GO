package cache

import (
	"context"
	"testing"
	"time"
)

func TestMemoryCacheSetGet(t *testing.T) {
	c := NewMemoryCache(10)
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "key", []byte("value"), time.Minute)

	got, ok := c.Get(ctx, "key")
	if !ok {
		t.Fatal("expected entry to be found")
	}
	if string(got) != "value" {
		t.Fatalf("got %q, want %q", got, "value")
	}
}

func TestMemoryCacheExpiry(t *testing.T) {
	c := NewMemoryCache(10)
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "key", []byte("value"), 10*time.Millisecond)
	time.Sleep(40 * time.Millisecond)

	if _, ok := c.Get(ctx, "key"); ok {
		t.Fatal("expected expired entry to be invisible")
	}
}

func TestMemoryCacheFIFOEviction(t *testing.T) {
	c := NewMemoryCache(2)
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "a", []byte("1"), time.Minute)
	c.Set(ctx, "b", []byte("2"), time.Minute)
	c.Set(ctx, "c", []byte("3"), time.Minute) // exceeds maxEntries -> "a" evicted

	if _, ok := c.Get(ctx, "a"); ok {
		t.Fatal("expected oldest entry to be evicted")
	}
	if _, ok := c.Get(ctx, "b"); !ok {
		t.Fatal("expected b to remain")
	}
	if _, ok := c.Get(ctx, "c"); !ok {
		t.Fatal("expected c to remain")
	}
}

func TestMemoryCacheUpdateDoesNotDoubleQueue(t *testing.T) {
	c := NewMemoryCache(2)
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "k", []byte("v1"), time.Minute)
	c.Set(ctx, "k", []byte("v2"), time.Minute) // update, must not add a second queue entry
	c.Set(ctx, "x", []byte("vx"), time.Minute)
	c.Set(ctx, "y", []byte("vy"), time.Minute) // evicts the true oldest ("k")

	if _, ok := c.Get(ctx, "k"); ok {
		t.Fatal("expected k to be evicted after overflow")
	}
	if _, ok := c.Get(ctx, "x"); !ok {
		t.Fatal("expected x to remain")
	}
	if _, ok := c.Get(ctx, "y"); !ok {
		t.Fatal("expected y to remain")
	}
}

// A deleted key must not be shadowed by its stale FIFO position: eviction walks
// the queue, and a key deleted without cleaning up the queue would otherwise be
// evicted twice and skip the real oldest entry.
func TestMemoryCacheDeleteKeepsEvictionOrderHonest(t *testing.T) {
	c := NewMemoryCache(2)
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "a", []byte("1"), time.Minute)
	c.Set(ctx, "b", []byte("2"), time.Minute)
	c.Delete(ctx, "a")
	c.Set(ctx, "c", []byte("3"), time.Minute)
	c.Set(ctx, "d", []byte("4"), time.Minute) // must evict "b" (the true oldest)

	if _, ok := c.Get(ctx, "b"); ok {
		t.Fatal("expected b to be evicted as the oldest live entry")
	}
	if _, ok := c.Get(ctx, "c"); !ok {
		t.Fatal("expected c to remain")
	}
	if _, ok := c.Get(ctx, "d"); !ok {
		t.Fatal("expected d to remain")
	}
}

func TestMemoryCacheIgnoresNonPositiveTTL(t *testing.T) {
	c := NewMemoryCache(10)
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "key", []byte("value"), 0)
	if _, ok := c.Get(ctx, "key"); ok {
		t.Fatal("expected a non-positive TTL to store nothing")
	}
}

func TestHashKeyDeterministicAndPrefixed(t *testing.T) {
	a := HashKey("search", "hello")
	b := HashKey("search", "hello")
	if a != b {
		t.Fatal("HashKey should be deterministic")
	}
	if a == HashKey("scrape", "hello") {
		t.Fatal("HashKey should include the prefix")
	}
	if want := len("search:") + 64; len(a) != want {
		t.Fatalf("HashKey length = %d, want %d", len(a), want)
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	encoder := encodeEnvelope([]byte("payload"), time.Minute)
	value, ok := decodeEnvelope(encoder)
	if !ok {
		t.Fatal("expected a fresh envelope to decode")
	}
	if string(value) != "payload" {
		t.Fatalf("got %q, want %q", value, "payload")
	}
}

func TestEnvelopeRejectsExpiredAndMalformed(t *testing.T) {
	expired := encodeEnvelope([]byte("payload"), -time.Minute)
	if _, ok := decodeEnvelope(expired); ok {
		t.Fatal("expected an expired envelope to be rejected")
	}
	if _, ok := decodeEnvelope([]byte("not json")); ok {
		t.Fatal("expected malformed data to be rejected")
	}
}
