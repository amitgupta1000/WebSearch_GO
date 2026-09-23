package cache

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/gcs"
)

// gcsStub is a minimal stand-in for the Cloud Storage JSON API. It implements the
// three requests the GCS store makes, so the backend can be tested without
// credentials or network access.
type gcsStub struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newGCSStub(t *testing.T) (*httptest.Server, *gcsStub) {
	t.Helper()
	stub := &gcsStub{objects: map[string][]byte{}}
	server := httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(server.Close)
	return server, stub
}

func (s *gcsStub) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name := r.URL.Query().Get("name")
	if name == "" {
		if parts := strings.SplitN(r.URL.Path, "/o/", 2); len(parts) == 2 {
			if unescaped, err := url.PathUnescape(parts[1]); err == nil {
				name = unescaped
			}
		}
	}

	switch r.Method {
	case http.MethodPost: // resumable-less media upload
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.objects[name] = body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	case http.MethodGet:
		payload, exists := s.objects[name]
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(payload)
	case http.MethodDelete:
		if _, exists := s.objects[name]; !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(s.objects, name)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *gcsStub) object(name string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, exists := s.objects[name]
	return payload, exists
}

func (s *gcsStub) put(name string, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[name] = payload
}

func TestGCSCacheRoundTrip(t *testing.T) {
	server, stub := newGCSStub(t)
	store := newGCSCacheWithClient(gcs.NewClientWithHTTPClient(server.Client(), server.URL), "bucket", "test-prefix/")
	ctx := context.Background()

	if _, ok := store.Get(ctx, "scrape:missing"); ok {
		t.Fatal("expected a miss for an object that does not exist")
	}

	store.Set(ctx, "scrape:key", []byte("value"), time.Minute)
	got, ok := store.Get(ctx, "scrape:key")
	if !ok || string(got) != "value" {
		t.Fatalf("Get = (%q, %v), want (%q, true)", got, ok, "value")
	}
	if _, exists := stub.object("test-prefix/scrape:key"); !exists {
		t.Error("the configured prefix was not applied to the object name")
	}

	store.Delete(ctx, "scrape:key")
	if _, ok := store.Get(ctx, "scrape:key"); ok {
		t.Fatal("expected a deleted object to read as a miss")
	}
}

func TestGCSCacheRejectsExpiredObjects(t *testing.T) {
	server, stub := newGCSStub(t)
	store := newGCSCacheWithClient(gcs.NewClientWithHTTPClient(server.Client(), server.URL), "bucket", "p/")
	// Cloud Storage has no native TTL, so an entry carries its own deadline.
	stub.put("p/stale", encodeEnvelope([]byte("old"), -time.Minute))

	if _, ok := store.Get(context.Background(), "stale"); ok {
		t.Fatal("an object past its own expiry must read as a miss")
	}
}

func TestGCSCacheSetIgnoresNonPositiveTTL(t *testing.T) {
	server, stub := newGCSStub(t)
	store := newGCSCacheWithClient(gcs.NewClientWithHTTPClient(server.Client(), server.URL), "bucket", "p/")

	store.Set(context.Background(), "key", []byte("value"), 0)
	if _, exists := stub.object("p/key"); exists {
		t.Error("a non-positive TTL must not store anything")
	}
}

func TestGCSCacheTreatsUnreachableBucketAsMiss(t *testing.T) {
	// A cache outage must degrade to "no cache", never fail the caller.
	store := newGCSCacheWithClient(gcs.NewClientWithHTTPClient(&http.Client{Timeout: 50 * time.Millisecond}, "http://127.0.0.1:1"), "bucket", "p/")
	if _, ok := store.Get(context.Background(), "key"); ok {
		t.Fatal("expected a miss when the bucket is unreachable")
	}
	store.Set(context.Background(), "key", []byte("value"), time.Minute) // must not panic
}

func TestRedisCacheRejectsEmptyURL(t *testing.T) {
	if _, err := NewRedisCache(context.Background(), "  ", "prefix"); err == nil {
		t.Fatal("expected an empty Redis URL to be rejected")
	}
}

func TestRedisCacheReportsUnreachableServer(t *testing.T) {
	// Port 1 refuses connections, so this exercises the startup check without a
	// Redis instance. The point is that a failure is reported, which is what lets
	// the server fall back to the in-memory cache.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := NewRedisCache(ctx, "redis://127.0.0.1:1", "prefix"); err == nil {
		t.Fatal("expected connecting to an unreachable Redis to fail")
	}
}

func TestBackendsReportTheirNames(t *testing.T) {
	server, _ := newGCSStub(t)
	gcs := newGCSCacheWithClient(gcs.NewClientWithHTTPClient(server.Client(), server.URL), "bucket", "p/")
	if gcs.Name() != "gcs" {
		t.Errorf("GCS backend name = %q, want %q", gcs.Name(), "gcs")
	}
	memory := NewMemoryCache(4)
	defer memory.Close()
	if memory.Name() != "memory" {
		t.Errorf("memory backend name = %q, want %q", memory.Name(), "memory")
	}
}
