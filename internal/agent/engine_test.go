package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

type mockSearcher struct {
	lastMaxResults int
}

func (m *mockSearcher) Search(ctx context.Context, query string, maxResults int) ([]models.SearchResult, error) {
	m.lastMaxResults = maxResults
	return []models.SearchResult{
		{Title: "Result 1", Link: "https://example.com/1", Snippet: "Snippet 1"},
	}, nil
}

type mockScraper struct {
	calledURLs []string
}

func (m *mockScraper) ScrapeURL(ctx context.Context, targetURL string) (*models.ScrapeResult, error) {
	m.calledURLs = append(m.calledURLs, targetURL)
	return &models.ScrapeResult{
		URL:      targetURL,
		Title:    "Test Page",
		Content:  "This is valid scraped content for testing purposes that exceeds the minimum required length.",
		Headings: []string{"Leadership"},
		JSONLD:   []string{`{"@type":"Organization","name":"Acme"}`},
	}, nil
}

type mockLLM struct{}

func (m *mockLLM) GenerateStructured(ctx context.Context, systemPrompt, userPrompt string, target any) error {
	return nil
}

func TestEngineLimits(t *testing.T) {
	c := cache.NewMemoryCache(100)
	defer c.Close()

	eng := NewEngine(&mockLLM{}, &mockSearcher{}, &mockScraper{}, c, 5, 15, 20)

	if eng.MaxScrapes() != 5 {
		t.Errorf("expected max scrapes 5, got %d", eng.MaxScrapes())
	}
	if eng.MaxSearchResults() != 15 {
		t.Errorf("expected max search results 15, got %d", eng.MaxSearchResults())
	}
	if eng.MaxSteps() != 20 {
		t.Errorf("expected max steps 20, got %d", eng.MaxSteps())
	}

	// Test default fallback on 0 or negative
	engDefault := NewEngine(&mockLLM{}, &mockSearcher{}, &mockScraper{}, c, 0, -1, 0)
	if engDefault.MaxScrapes() != defaultMaxScrapesPerResearch {
		t.Errorf("expected default max scrapes %d, got %d", defaultMaxScrapesPerResearch, engDefault.MaxScrapes())
	}
	if engDefault.MaxSearchResults() != defaultMaxSearchResults {
		t.Errorf("expected default max search results %d, got %d", defaultMaxSearchResults, engDefault.MaxSearchResults())
	}
	if engDefault.MaxSteps() != defaultMaxSteps {
		t.Errorf("expected default max steps %d, got %d", defaultMaxSteps, engDefault.MaxSteps())
	}
}

// failingScraper always fails, counting attempts.
type failingScraper struct {
	calls int
	err   error
}

func (s *failingScraper) ScrapeURL(context.Context, string) (*models.ScrapeResult, error) {
	s.calls++
	return nil, s.err
}

func TestScrapeCachedRemembersFailures(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	scraper := &failingScraper{err: errors.New("scraping returned status code: 403")}
	eng := NewEngine(&mockLLM{}, &mockSearcher{}, scraper, cacheStore, 1, 3, 10)

	for i := 0; i < 3; i++ {
		if _, err := eng.ScrapeCached(context.Background(), "https://paywalled.example/page"); err == nil {
			t.Fatal("expected the scrape to fail")
		}
	}
	if scraper.calls != 1 {
		t.Errorf("scraper called %d times, want 1 (failures must be cached)", scraper.calls)
	}
}

func TestScrapeCachedDoesNotCacheCancellation(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	scraper := &failingScraper{err: context.Canceled}
	eng := NewEngine(&mockLLM{}, &mockSearcher{}, scraper, cacheStore, 1, 3, 10)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 2; i++ {
		_, _ = eng.ScrapeCached(ctx, "https://example.com/page")
	}
	if scraper.calls != 2 {
		t.Errorf("scraper called %d times, want 2 (cancellations must not be cached)", scraper.calls)
	}
}

func TestScrapeCacheKeySeparatesRenderModes(t *testing.T) {
	target := "https://example.com/page"
	byDefault := scrapeCacheKey(context.Background(), target)
	withBrowser := scrapeCacheKey(tools.WithBrowserFallback(context.Background(), true), target)
	httpOnly := scrapeCacheKey(tools.WithBrowserFallback(context.Background(), false), target)

	if byDefault == withBrowser || byDefault == httpOnly || withBrowser == httpOnly {
		t.Fatalf("cache keys must differ per render mode: default=%s browser=%s http=%s", byDefault, withBrowser, httpOnly)
	}
}
