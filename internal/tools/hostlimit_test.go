package tools

import (
	"context"
	"testing"
	"time"
)

func TestHostLimiterCapsConcurrencyPerHost(t *testing.T) {
	limiter := newHostLimiter(0, 0, 1)
	ctx := context.Background()

	release, err := limiter.acquire(ctx, "example.com")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	acquired := make(chan error, 1)
	go func() {
		secondRelease, err := limiter.acquire(ctx, "example.com")
		if err == nil {
			secondRelease()
		}
		acquired <- err
	}()

	select {
	case <-acquired:
		t.Fatal("a second request to a busy host should have waited")
	case <-time.After(50 * time.Millisecond):
	}

	release()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("second acquire after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the second acquire never proceeded after the slot was released")
	}
}

func TestHostLimiterDoesNotSerializeDifferentHosts(t *testing.T) {
	limiter := newHostLimiter(0, 0, 1)
	ctx := context.Background()

	release, err := limiter.acquire(ctx, "first.example")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	if _, err := limiter.acquire(ctx, "second.example"); err != nil {
		t.Fatalf("a different host must not be blocked by a busy one: %v", err)
	}
}

func TestHostLimiterReleaseIsIdempotent(t *testing.T) {
	limiter := newHostLimiter(0, 0, 1)
	ctx := context.Background()

	release, err := limiter.acquire(ctx, "example.com")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	release()
	release() // a double release must not free the slot a second time

	first, err := limiter.acquire(ctx, "example.com")
	if err != nil {
		t.Fatalf("acquire after double release: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		second, err := limiter.acquire(ctx, "example.com")
		if err == nil {
			second()
		}
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("a double release freed the slot twice, so two requests ran at once")
	case <-time.After(50 * time.Millisecond):
	}
	first()
}

func TestHostLimiterDisabledIsNoOp(t *testing.T) {
	limiter := newHostLimiter(0, 0, 0)
	started := time.Now()
	for index := 0; index < 5; index++ {
		release, err := limiter.acquire(context.Background(), "example.com")
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		release()
	}
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("a disabled limiter must not wait, took %v", elapsed)
	}
}

func TestHostLimiterRateSpacesRequests(t *testing.T) {
	// 20 requests/second with a burst of one: the second request must wait
	// roughly 50ms, so three requests take at least ~90ms.
	limiter := newHostLimiter(20, 1, 0)
	started := time.Now()
	for index := 0; index < 3; index++ {
		release, err := limiter.acquire(context.Background(), "example.com")
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		release()
	}
	if elapsed := time.Since(started); elapsed < 90*time.Millisecond {
		t.Fatalf("expected the rate limit to space the requests, they took %v", elapsed)
	}
}

func TestHostLimiterHonoursContextCancellation(t *testing.T) {
	limiter := newHostLimiter(0, 0, 1)
	release, err := limiter.acquire(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := limiter.acquire(ctx, "example.com"); err == nil {
		t.Fatal("waiting for a slot must stop when the context is done")
	}
}

func TestHostOfExtractsHostname(t *testing.T) {
	cases := map[string]string{
		"https://example.com/a/b?c=d": "example.com",
		"http://Example.COM:8080/x":   "Example.COM",
		"not a url":                   "",
		"":                            "",
	}
	for target, want := range cases {
		if got := hostOf(target); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", target, got, want)
		}
	}
}
