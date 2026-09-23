package cache

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestGCSCacheRoundTripLive exercises the Cloud Storage backend against a real
// bucket. It is opt-in because it needs a bucket that accepts writes:
//
//	GCS_INTEGRATION=1 GCS_CACHE_BUCKET=my-cache go test ./internal/cache/ -run Live -v
//
// Credentials come from Application Default Credentials, which honours
// GOOGLE_APPLICATION_CREDENTIALS.
func TestGCSCacheRoundTripLive(t *testing.T) {
	if os.Getenv("GCS_INTEGRATION") != "1" {
		t.Skip("set GCS_INTEGRATION=1 (and GCS_CACHE_BUCKET) to run against a real bucket")
	}
	bucket := strings.TrimSpace(os.Getenv("GCS_CACHE_BUCKET"))
	if bucket == "" {
		t.Skip("set GCS_CACHE_BUCKET to a bucket the credentials can write to")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Building the store is where credentials are resolved, so a failure here is
	// a misconfigured GOOGLE_APPLICATION_CREDENTIALS or missing ambient identity.
	store, err := NewGCSCache(ctx, bucket, "websearch-go/test/")
	if err != nil {
		t.Fatalf("build the GCS cache: %v", err)
	}
	defer store.Close()

	key := fmt.Sprintf("roundtrip-%d", time.Now().UnixNano())
	defer store.Delete(ctx, key)

	if _, ok := store.Get(ctx, key); ok {
		t.Fatal("expected a miss for a fresh key")
	}

	payload := []byte(`{"kind":"result","content":"hello"}`)
	store.Set(ctx, key, payload, time.Minute)
	got, ok := store.Get(ctx, key)
	if !ok {
		t.Fatal("expected the value to be readable back from the bucket")
	}
	if string(got) != string(payload) {
		t.Fatalf("round trip returned %q, want %q", got, payload)
	}

	// The expiry lives inside the object, so a stale object must read as a miss
	// even though the object itself is still there.
	stale := key + "-stale"
	store.Set(ctx, stale, []byte("short-lived"), -time.Minute)
	if _, ok := store.Get(ctx, stale); ok {
		t.Error("expected an entry past its embedded expiry to read as a miss")
	}
	store.Delete(ctx, stale)

	store.Delete(ctx, key)
	if _, ok := store.Get(ctx, key); ok {
		t.Error("expected a deleted key to be gone")
	}
}
