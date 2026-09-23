package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// scriptedScraper is a PageFetcher whose response and timing are controlled by
// the test. It records how many calls were made, how many of them were
// conditional revalidations, and the peak number of concurrent calls.
type scriptedScraper struct {
	mu               sync.Mutex
	calls            int
	conditionalCalls int
	inFlight         int
	maxInFlight      int
	delay            time.Duration
	response         func(targetURL string) (*models.ScrapeResult, error)
}

func (s *scriptedScraper) ScrapeURL(ctx context.Context, targetURL string) (*models.ScrapeResult, error) {
	s.mu.Lock()
	s.calls++
	s.inFlight++
	if s.inFlight > s.maxInFlight {
		s.maxInFlight = s.inFlight
	}
	if _, _, conditional := tools.ConditionalRequestValidators(ctx); conditional {
		s.conditionalCalls++
	}
	delay, response := s.delay, s.response
	s.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}
	var result *models.ScrapeResult
	var err error
	if response != nil {
		result, err = response(targetURL)
	} else {
		result = &models.ScrapeResult{URL: targetURL, Content: "scraped " + targetURL}
	}

	s.mu.Lock()
	s.inFlight--
	s.mu.Unlock()
	return result, err
}

func (s *scriptedScraper) stats() (calls, conditionalCalls, maxInFlight int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.conditionalCalls, s.maxInFlight
}

func TestScrapeCachedBatchPreservesOrderAndBoundsConcurrency(t *testing.T) {
	store := cache.NewMemoryCache(64)
	defer store.Close()

	const delay = 30 * time.Millisecond
	scraper := &scriptedScraper{
		delay: delay,
		response: func(targetURL string) (*models.ScrapeResult, error) {
			return &models.ScrapeResult{URL: targetURL, Content: "content of " + targetURL}, nil
		},
	}
	engine := NewEngine(&mockLLM{}, &mockSearcher{}, scraper, store, 10, 12, 10, WithScrapeConcurrency(3))

	targets := []string{
		"https://a.example/1", "https://b.example/2", "https://c.example/3",
		"https://d.example/4", "https://e.example/5",
	}
	started := time.Now()
	outcomes := engine.ScrapeCachedBatch(context.Background(), targets)
	elapsed := time.Since(started)

	if len(outcomes) != len(targets) {
		t.Fatalf("got %d outcomes, want %d", len(outcomes), len(targets))
	}
	for index, outcome := range outcomes {
		if outcome.URL != targets[index] {
			t.Errorf("outcome %d URL = %q, want %q", index, outcome.URL, targets[index])
		}
		if outcome.Err != nil {
			t.Fatalf("outcome %d failed: %v", index, outcome.Err)
		}
		if outcome.Result == nil || outcome.Result.URL != targets[index] {
			t.Errorf("outcome %d result = %+v, want the result for %s", index, outcome.Result, targets[index])
		}
	}

	calls, _, maxInFlight := scraper.stats()
	if calls != len(targets) {
		t.Errorf("scraper called %d times, want %d", calls, len(targets))
	}
	if maxInFlight < 2 {
		t.Errorf("peak concurrency was %d: the batch did not run in parallel", maxInFlight)
	}
	if maxInFlight > 3 {
		t.Errorf("peak concurrency was %d, want at most the configured 3", maxInFlight)
	}
	if serial := time.Duration(len(targets)) * delay; elapsed >= serial {
		t.Errorf("batch took %v, no faster than the %v a serial loop would take", elapsed, serial)
	}
}

func TestScrapeCachedBatchHandlesEmptyInput(t *testing.T) {
	store := cache.NewMemoryCache(8)
	defer store.Close()
	engine := NewEngine(&mockLLM{}, &mockSearcher{}, &scriptedScraper{}, store, 10, 12, 10)

	if outcomes := engine.ScrapeCachedBatch(context.Background(), nil); len(outcomes) != 0 {
		t.Fatalf("got %d outcomes for no targets", len(outcomes))
	}
}

// seedScrapeEntry writes a cache entry as if it had been stored at storedAt.
func seedScrapeEntry(t *testing.T, store cache.Store, targetURL string, result *models.ScrapeResult, storedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	cache.SetJSON(ctx, store, scrapeCacheKey(ctx, targetURL), cachedScrapeEntry{
		Kind:     cachedKindResult,
		Result:   result,
		StoredAt: storedAt,
	}, scrapeTTL)
}

func TestScrapeCachedRevalidatesStaleEntriesAndKeepsContentOnNotModified(t *testing.T) {
	store := cache.NewMemoryCache(64)
	defer store.Close()

	targetURL := "https://example.com/about"
	cached := &models.ScrapeResult{
		URL:          targetURL,
		CanonicalURL: targetURL,
		Title:        "Cached page",
		Content:      strings.Repeat("cached content ", 20),
		ETag:         `"v1"`,
		LastModified: "Wed, 21 Oct 2015 07:28:00 GMT",
		FetchedAt:    time.Now().Add(-72 * time.Hour),
	}
	scraper := &scriptedScraper{response: func(targetURL string) (*models.ScrapeResult, error) {
		return &models.ScrapeResult{URL: targetURL, NotModified: true}, nil
	}}
	engine := NewEngine(&mockLLM{}, &mockSearcher{}, scraper, store, 10, 12, 10, WithScrapeRevalidation(time.Hour))

	seedScrapeEntry(t, store, targetURL, cached, time.Now().Add(-72*time.Hour))

	result, err := engine.ScrapeCached(context.Background(), targetURL)
	if err != nil {
		t.Fatalf("ScrapeCached: %v", err)
	}
	if _, conditionalCalls, _ := scraper.stats(); conditionalCalls != 1 {
		t.Errorf("expected exactly one conditional revalidation, got %d", conditionalCalls)
	}
	if result.Content != cached.Content {
		t.Error("a 304 response must keep the cached content")
	}
	if time.Since(result.FetchedAt) > time.Minute {
		t.Error("expected the cached page's FetchedAt to be refreshed")
	}

	ctx := context.Background()
	var refreshed cachedScrapeEntry
	if !cache.GetJSON(ctx, store, scrapeCacheKey(ctx, targetURL), &refreshed) {
		t.Fatal("expected the refreshed entry to still be cached")
	}
	if time.Since(refreshed.StoredAt) > time.Minute {
		t.Error("expected the entry to be restamped, so it is not revalidated again immediately")
	}
}

func TestScrapeCachedServesFreshEntriesWithoutRefetching(t *testing.T) {
	store := cache.NewMemoryCache(64)
	defer store.Close()

	targetURL := "https://example.com/team"
	cached := &models.ScrapeResult{URL: targetURL, Content: "cached body", ETag: `"v1"`}
	scraper := &scriptedScraper{}
	engine := NewEngine(&mockLLM{}, &mockSearcher{}, scraper, store, 10, 12, 10, WithScrapeRevalidation(time.Hour))

	seedScrapeEntry(t, store, targetURL, cached, time.Now())

	result, err := engine.ScrapeCached(context.Background(), targetURL)
	if err != nil {
		t.Fatalf("ScrapeCached: %v", err)
	}
	if result.Content != "cached body" {
		t.Errorf("content = %q, want the cached body", result.Content)
	}
	if calls, _, _ := scraper.stats(); calls != 0 {
		t.Errorf("a fresh entry must not be refetched, but the scraper was called %d times", calls)
	}
}

func TestScrapeCachedSkipsRevalidationWhenDisabled(t *testing.T) {
	store := cache.NewMemoryCache(64)
	defer store.Close()

	targetURL := "https://example.com/about"
	cached := &models.ScrapeResult{URL: targetURL, Content: "cached body", ETag: `"v1"`}
	scraper := &scriptedScraper{}
	engine := NewEngine(&mockLLM{}, &mockSearcher{}, scraper, store, 10, 12, 10, WithScrapeRevalidation(0))

	seedScrapeEntry(t, store, targetURL, cached, time.Now().Add(-72*time.Hour))

	result, err := engine.ScrapeCached(context.Background(), targetURL)
	if err != nil {
		t.Fatalf("ScrapeCached: %v", err)
	}
	if result.Content != "cached body" {
		t.Errorf("content = %q, want the cached body", result.Content)
	}
	if calls, _, _ := scraper.stats(); calls != 0 {
		t.Errorf("revalidation is disabled, but the scraper was called %d times", calls)
	}
}

func TestScrapeCachedReplacesContentWhenRevalidationReportsChange(t *testing.T) {
	store := cache.NewMemoryCache(64)
	defer store.Close()

	targetURL := "https://example.com/about"
	cached := &models.ScrapeResult{URL: targetURL, Content: "old body", ETag: `"v1"`}
	scraper := &scriptedScraper{response: func(targetURL string) (*models.ScrapeResult, error) {
		return &models.ScrapeResult{URL: targetURL, Content: "new body", ETag: `"v2"`}, nil
	}}
	engine := NewEngine(&mockLLM{}, &mockSearcher{}, scraper, store, 10, 12, 10, WithScrapeRevalidation(time.Hour))

	seedScrapeEntry(t, store, targetURL, cached, time.Now().Add(-72*time.Hour))

	result, err := engine.ScrapeCached(context.Background(), targetURL)
	if err != nil {
		t.Fatalf("ScrapeCached: %v", err)
	}
	if result.Content != "new body" {
		t.Errorf("content = %q, want the refreshed body", result.Content)
	}
	if result.ETag != `"v2"` {
		t.Errorf("ETag = %q, want the refreshed validator", result.ETag)
	}
}

func TestScrapeCachedKeepsCachedContentWhenRevalidationFails(t *testing.T) {
	store := cache.NewMemoryCache(64)
	defer store.Close()

	targetURL := "https://example.com/about"
	cached := &models.ScrapeResult{URL: targetURL, Content: "cached body", ETag: `"v1"`}
	scraper := &scriptedScraper{response: func(string) (*models.ScrapeResult, error) {
		return nil, errors.New("connection reset by peer")
	}}
	engine := NewEngine(&mockLLM{}, &mockSearcher{}, scraper, store, 10, 12, 10, WithScrapeRevalidation(time.Hour))

	seedScrapeEntry(t, store, targetURL, cached, time.Now().Add(-72*time.Hour))

	result, err := engine.ScrapeCached(context.Background(), targetURL)
	if err != nil {
		t.Fatalf("a failed revalidation must not fail the run: %v", err)
	}
	if result.Content != "cached body" {
		t.Errorf("content = %q, want the cached body to be kept", result.Content)
	}
}

func TestScrapeCachedDoesNotRevalidateWithoutValidators(t *testing.T) {
	store := cache.NewMemoryCache(64)
	defer store.Close()

	targetURL := "https://example.com/no-validators"
	// No ETag and no Last-Modified: the server gave us nothing to revalidate with,
	// so the cached copy is the best available answer.
	cached := &models.ScrapeResult{URL: targetURL, Content: "cached body"}
	scraper := &scriptedScraper{}
	engine := NewEngine(&mockLLM{}, &mockSearcher{}, scraper, store, 10, 12, 10, WithScrapeRevalidation(time.Hour))

	seedScrapeEntry(t, store, targetURL, cached, time.Now().Add(-72*time.Hour))

	result, err := engine.ScrapeCached(context.Background(), targetURL)
	if err != nil || result.Content != "cached body" {
		t.Fatalf("got (%v, %v), want the cached body", result, err)
	}
	if calls, _, _ := scraper.stats(); calls != 0 {
		t.Errorf("a page without validators cannot be revalidated, but the scraper was called %d times", calls)
	}
}
