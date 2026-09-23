package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// renderCounter records how many times the browser fallback was asked to render.
type renderCounter struct {
	mu    sync.Mutex
	calls int
}

func (r *renderCounter) Render(context.Context, string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return "<html><head><title>Rendered</title></head><body><p>" +
		strings.Repeat("rendered content ", 40) + "</p></body></html>", nil
}

func (*renderCounter) Close() error { return nil }

func (r *renderCounter) renderCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// htmlBody is a page with enough text to pass the scraper's quality checks.
func htmlBody() string {
	return "<html><head><title>Page</title></head><body><p>" +
		strings.Repeat("body text ", 40) + "</p></body></html>"
}

func newTestScraper() *Scraper {
	// Per-host limits are disabled so each test controls its own timing.
	return NewScraper(nil, 2*time.Second, WithPrivateHostAccess(), WithHostLimits(0, 0, 0))
}

func TestParseRetryAfterSeconds(t *testing.T) {
	if got := parseRetryAfter(" 12 ", time.Now()); got != 12*time.Second {
		t.Errorf("parseRetryAfter(\" 12 \") = %v, want 12s", got)
	}
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	now := time.Now()
	when := now.Add(30 * time.Second).UTC().Format(http.TimeFormat)
	got := parseRetryAfter(when, now)
	if got < 25*time.Second || got > 35*time.Second {
		t.Errorf("parseRetryAfter(%q) = %v, want about 30s", when, got)
	}
}

func TestParseRetryAfterIgnoresUnusableValues(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute).UTC().Format(http.TimeFormat)
	for _, value := range []string{"", "0", "-5", "soon", past, "Tue, 32 Zzz 2024 99:99:99 GMT"} {
		if got := parseRetryAfter(value, now); got != 0 {
			t.Errorf("parseRetryAfter(%q) = %v, want 0", value, got)
		}
	}
}

func TestScrapeURLSendsConditionalHeadersAndReportsNotModified(t *testing.T) {
	var receivedIfNoneMatch, receivedIfModifiedSince string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedIfNoneMatch = r.Header.Get("If-None-Match")
		receivedIfModifiedSince = r.Header.Get("If-Modified-Since")
		w.Header().Set("ETag", `"v2"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	scraper := newTestScraper()
	defer scraper.Close()

	ctx := WithConditionalRequest(context.Background(), `"v1"`, "Wed, 21 Oct 2015 07:28:00 GMT")
	result, err := scraper.ScrapeURL(ctx, server.URL)
	if err != nil {
		t.Fatalf("ScrapeURL: %v", err)
	}
	if !result.NotModified {
		t.Fatal("a 304 response must be reported as NotModified")
	}
	if receivedIfNoneMatch != `"v1"` {
		t.Errorf("If-None-Match = %q, want %q", receivedIfNoneMatch, `"v1"`)
	}
	if receivedIfModifiedSince == "" {
		t.Error("If-Modified-Since was not sent")
	}
	if result.Content != "" {
		t.Errorf("a 304 response carries no content, got %q", result.Content)
	}
}

func TestScrapeURLRecordsResponseValidators(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, htmlBody())
	}))
	defer server.Close()

	scraper := newTestScraper()
	defer scraper.Close()

	result, err := scraper.ScrapeURL(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("ScrapeURL: %v", err)
	}
	if result.ETag != `"abc123"` {
		t.Errorf("ETag = %q, want %q", result.ETag, `"abc123"`)
	}
	if result.LastModified == "" {
		t.Error("Last-Modified was not recorded")
	}
}

func TestScrapeURLDoesNotRenderMissingPages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	renderer := &renderCounter{}
	scraper := newTestScraper()
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return renderer, nil }
	scraper.browserEnabled = true
	defer scraper.Close()

	if _, err := scraper.ScrapeURL(context.Background(), server.URL); err == nil {
		t.Fatal("expected a 404 to be reported as an error")
	}
	if renderer.renderCalls() != 0 {
		t.Errorf("a missing page must not be rendered, render called %d times", renderer.renderCalls())
	}
}

func TestScrapeURLStillRendersForbiddenPages(t *testing.T) {
	// A 403 is exactly the case a browser can sometimes resolve, so it must keep
	// going to the renderer.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	renderer := &renderCounter{}
	scraper := newTestScraper()
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return renderer, nil }
	scraper.browserEnabled = true
	defer scraper.Close()

	result, err := scraper.ScrapeURL(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("ScrapeURL: %v", err)
	}
	if renderer.renderCalls() != 1 {
		t.Errorf("render called %d times, want 1", renderer.renderCalls())
	}
	if !result.FallbackAttempted {
		t.Error("the result should record that the fallback ran")
	}
}

func TestScrapeURLHonoursRetryAfterHint(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, htmlBody())
	}))
	defer server.Close()

	scraper := NewScraper(nil, 5*time.Second, WithPrivateHostAccess(),
		WithHostLimits(0, 0, 0), WithMaxRetryAfter(2*time.Second))
	// Keep the normal backoff negligible so the test measures the wait the server
	// actually asked for.
	scraper.fetchBackoff = time.Millisecond
	defer scraper.Close()

	started := time.Now()
	if _, err := scraper.ScrapeURL(context.Background(), server.URL); err != nil {
		t.Fatalf("ScrapeURL: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond {
		t.Errorf("the Retry-After hint was ignored: the retry happened after %v", elapsed)
	}
	if attempts != 2 {
		t.Errorf("server saw %d attempts, want 2", attempts)
	}
}
