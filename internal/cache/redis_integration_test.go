package cache

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRedisCacheDetachedFromCallerCancel pins the deadline-leak fix: a caller
// whose context is already cancelled or expired (a finished run, a disconnected
// client) must neither lose its cache writes nor fail its reads — cache ops are
// bounded by their own per-op timeout, never by the caller's lifetime. This is
// the regression that spammed "[WARN] redis cache get failed ... context
// deadline exceeded" at the tail of every long landscape run and silently
// dropped the results the next run would have reused.
func TestRedisCacheDetachedFromCallerCancel(t *testing.T) {
	if os.Getenv("REDIS_INTEGRATION") != "1" {
		t.Skip("set REDIS_INTEGRATION=1 to run against a real Redis")
	}

	store, err := NewRedisCache(context.Background(), liveRedisURL(t), "websearch-go:test:")
	if err != nil {
		t.Fatalf("connect to Redis: %v", err)
	}
	defer store.Close()

	// A caller whose deadline already expired: the classic end-of-run shape.
	deadCtx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond)

	key := fmt.Sprintf("detached:%d", time.Now().UnixNano())
	defer store.Delete(context.Background(), key)

	store.Set(deadCtx, key, []byte("survives"), time.Minute)
	got, ok := store.Get(deadCtx, key)
	if !ok {
		t.Fatal("a cancelled caller's context must not fail the cache write or the read")
	}
	if string(got) != "survives" {
		t.Fatalf("round trip returned %q, want %q", got, "survives")
	}
}

// TestRedisCacheRoundTripLive exercises the Redis backend against a real server.
// It is opt-in because it needs reachable infrastructure:
//
//	REDIS_INTEGRATION=1 go test ./internal/cache/ -run Live -v
//
// The connection comes from REDIS_URL, or is assembled from REDIS_HOST /
// REDIS_PORT / REDIS_PASSWORD — the same two shapes the server accepts.
func TestRedisCacheRoundTripLive(t *testing.T) {
	if os.Getenv("REDIS_INTEGRATION") != "1" {
		t.Skip("set REDIS_INTEGRATION=1 to run against a real Redis")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := NewRedisCache(ctx, liveRedisURL(t), "websearch-go:test:")
	if err != nil {
		t.Fatalf("connect to Redis: %v", err)
	}
	defer store.Close()

	key := fmt.Sprintf("roundtrip:%d", time.Now().UnixNano())
	defer store.Delete(ctx, key)

	if _, ok := store.Get(ctx, key); ok {
		t.Fatal("expected a miss for a fresh key")
	}

	payload := []byte(`{"kind":"result","content":"hello"}`)
	store.Set(ctx, key, payload, time.Minute)
	got, ok := store.Get(ctx, key)
	if !ok {
		t.Fatal("expected the value to be readable back from Redis")
	}
	if string(got) != string(payload) {
		t.Fatalf("round trip returned %q, want %q", got, payload)
	}

	// Expiry is Redis's own, so a short TTL must disappear without any help from
	// this process.
	expiringKey := key + ":ttl"
	store.Set(ctx, expiringKey, []byte("short-lived"), time.Second)
	if _, ok := store.Get(ctx, expiringKey); !ok {
		t.Fatal("expected the short-lived entry to exist immediately")
	}
	time.Sleep(1200 * time.Millisecond)
	if _, ok := store.Get(ctx, expiringKey); ok {
		t.Error("expected Redis to expire the entry on its own")
	}

	store.Delete(ctx, key)
	if _, ok := store.Get(ctx, key); ok {
		t.Error("expected a deleted key to be gone")
	}
}

// liveRedisURL builds the connection URL the way the server configuration does,
// so the test covers the shape an operator actually deploys.
func liveRedisURL(t *testing.T) string {
	t.Helper()
	if configured := strings.TrimSpace(os.Getenv("REDIS_URL")); configured != "" {
		return configured
	}
	host := strings.TrimSpace(os.Getenv("REDIS_HOST"))
	if host == "" {
		t.Fatal("set REDIS_URL, or REDIS_HOST with REDIS_PORT / REDIS_PASSWORD")
	}
	port := 6379
	if configured := strings.TrimSpace(os.Getenv("REDIS_PORT")); configured != "" {
		parsed, err := strconv.Atoi(configured)
		if err != nil {
			t.Fatalf("REDIS_PORT %q is not a number", configured)
		}
		port = parsed
	}
	credentials := ""
	if password := os.Getenv("REDIS_PASSWORD"); password != "" {
		credentials = url.UserPassword("", password).String() + "@"
	}
	return fmt.Sprintf("redis://%s%s:%d", credentials, host, port)
}
