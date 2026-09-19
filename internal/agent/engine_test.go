package agent

import (
	"context"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
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
		URL:     targetURL,
		Title:   "Test Page",
		Content: "This is valid scraped content for testing purposes that exceeds the minimum required length.",
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
