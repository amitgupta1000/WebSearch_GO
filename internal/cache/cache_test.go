package cache

import (
	"testing"
	"time"
)

func TestMemoryCacheSetGet(t *testing.T) {
	c := NewMemoryCache(10)
	defer c.Close()

	c.Set("key", "value", time.Minute)

	got, ok := c.Get("key")
	if !ok {
		t.Fatal("expected entry to be found")
	}
	if got != "value" {
		t.Fatalf("got %v, want %q", got, "value")
	}
}

func TestMemoryCacheExpiry(t *testing.T) {
	c := NewMemoryCache(10)
	defer c.Close()

	c.Set("key", "value", 10*time.Millisecond)
	time.Sleep(40 * time.Millisecond)

	if _, ok := c.Get("key"); ok {
		t.Fatal("expected expired entry to be invisible")
	}
}

func TestMemoryCacheFIFOEviction(t *testing.T) {
	c := NewMemoryCache(2)
	defer c.Close()

	c.Set("a", 1, time.Minute)
	c.Set("b", 2, time.Minute)
	c.Set("c", 3, time.Minute) // exceeds maxEntries -> "a" evicted

	if _, ok := c.Get("a"); ok {
		t.Fatal("expected oldest entry to be evicted")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("expected b to remain")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("expected c to remain")
	}
}

func TestMemoryCacheUpdateDoesNotDoubleQueue(t *testing.T) {
	c := NewMemoryCache(2)
	defer c.Close()

	c.Set("k", "v1", time.Minute)
	c.Set("k", "v2", time.Minute) // update, must not add a second queue entry
	c.Set("x", "vx", time.Minute)
	c.Set("y", "vy", time.Minute) // evicts the true oldest ("k")

	if _, ok := c.Get("k"); ok {
		t.Fatal("expected k to be evicted after overflow")
	}
	if _, ok := c.Get("x"); !ok {
		t.Fatal("expected x to remain")
	}
	if _, ok := c.Get("y"); !ok {
		t.Fatal("expected y to remain")
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
