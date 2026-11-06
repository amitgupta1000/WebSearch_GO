package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

type newsTestSearcher struct {
	hits []models.SearchResult
}

func (s newsTestSearcher) Search(context.Context, string, int) ([]models.SearchResult, error) {
	return s.hits, nil
}

type newsTestScraper struct {
	pages map[string]*models.ScrapeResult
}

func (s newsTestScraper) ScrapeURL(_ context.Context, targetURL string) (*models.ScrapeResult, error) {
	return s.pages[targetURL], nil
}

type newsTestLLM struct{}

func (newsTestLLM) GenerateStructured(_ context.Context, _, _ string, target any) error {
	output := target.(*models.NewsSynthesis)
	output.Headline = "Current developments"
	output.Article = "A synthesized report grounded in the supplied reporting."
	output.Summary = "The latest reporting describes the current situation."
	output.Sentiment = models.NewsArticleSentiment{
		Label: "mixed", Score: 0.1, Confidence: 0.8, Rationale: "The supplied reporting contains both positive and negative developments.",
	}
	output.Citations = []models.NewsCitation{{Claim: "Current status", URLs: []string{"https://reuters.com/story"}}}
	return nil
}

func TestNewsHostAllowedUsesExactDomainBoundaries(t *testing.T) {
	if !newsHostAllowed("www.reuters.com", []string{"reuters.com"}) {
		t.Fatal("expected www subdomain to be allowed")
	}
	if newsHostAllowed("notreuters.com", []string{"reuters.com"}) {
		t.Fatal("did not expect lookalike domain to be allowed")
	}
}

func TestParseNewsTimeRelativeAndBoundary(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	if got := parseNewsTime("2 hours ago", now); !got.Equal(now.Add(-2 * time.Hour)) {
		t.Fatalf("relative date = %v", got)
	}
	if got := parseNewsTime("2026-11-06T11:00:00Z", now); !got.Equal(time.Date(2026, 11, 6, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("RFC3339 date = %v", got)
	}
}

func TestNewsCanonicalURLAndJSONLDDate(t *testing.T) {
	if got := canonicalNewsURL("https://www.reuters.com/story?id=1#top"); got != "https://reuters.com/story" {
		t.Fatalf("canonical URL = %q", got)
	}
	published, updated := newsDatesFromJSONLD([]string{`{"@type":"NewsArticle","datePublished":"2026-11-06T10:00:00Z","dateModified":"2026-11-06T11:00:00Z"}`})
	if published.IsZero() || updated.IsZero() {
		t.Fatalf("expected JSON-LD dates, got published=%v updated=%v", published, updated)
	}
}

func TestNewsChunksCarrySourceLabels(t *testing.T) {
	content := strings.Repeat("A current news paragraph. ", 200)
	chunks := newsChunks(models.NewsArticle{
		CanonicalURL: "https://reuters.com/story", URL: "https://reuters.com/story?x=1",
		Source: "reuters.com", Title: "Story", PublishedAt: time.Now(), Content: content,
	}, time.Now())
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if chunk.ArticleURL == "" || chunk.Source != "reuters.com" || len(chunk.Labels) == 0 || chunk.Text == "" {
			t.Fatalf("chunk is missing labels: %+v", chunk)
		}
	}
}

func TestNewsResearchSelectsFreshWhitelistedArticlesAndSynthesizes(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	searcher := newsTestSearcher{hits: []models.SearchResult{
		{Title: "Fresh", Link: "https://www.reuters.com/story?id=1", Snippet: "Fresh report", Date: "2026-11-06T11:00:00Z"},
		{Title: "Stale", Link: "https://bbc.com/old", Snippet: "Old report", Date: "2026-11-05T10:00:00Z"},
		{Title: "Lookalike", Link: "https://notreuters.com/story", Snippet: "Wrong source", Date: "2026-11-06T11:00:00Z"},
	}}
	scraper := newsTestScraper{pages: map[string]*models.ScrapeResult{
		"https://www.reuters.com/story?id=1": {URL: "https://www.reuters.com/story?id=1", CanonicalURL: "https://reuters.com/story", Title: "Fresh", Content: strings.Repeat("Fresh report content. ", 20)},
		"https://bbc.com/old":                {URL: "https://bbc.com/old", Title: "Old", Content: strings.Repeat("Old report content. ", 20)},
	}}
	engine := NewEngine(newsTestLLM{}, searcher, scraper, cache.NewMemoryCache(20), 10, 10, 3)
	agent := NewNewsAgent(engine)
	agent.allowedHosts = []string{"reuters.com", "bbc.com"}
	agent.now = func() time.Time { return now }

	report, err := agent.Research(context.Background(), "news-test", models.NewsQuery{Query: "current status", MaxArticles: 5}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(report.Articles) != 1 || report.Articles[0].Source != "reuters.com" {
		t.Fatalf("selected articles = %+v", report.Articles)
	}
	if report.Synthesis.Sentiment.Label != "mixed" {
		t.Fatalf("sentiment = %+v", report.Synthesis.Sentiment)
	}
	if len(report.RejectedArticles) != 1 || report.RejectedArticles[0].Status != "stale" {
		t.Fatalf("rejected articles = %+v", report.RejectedArticles)
	}
}

func TestValidateNewsSynthesisRejectsUnsupportedCitation(t *testing.T) {
	synthesis := models.NewsSynthesis{
		Headline: "Headline", Article: "Article", Summary: "Summary",
		Sentiment: models.NewsArticleSentiment{Label: "neutral"},
		Citations: []models.NewsCitation{{URLs: []string{"https://unknown.example/story"}}},
	}
	err := validateNewsSynthesis(synthesis, []models.NewsArticle{{URL: "https://reuters.com/story"}})
	if err == nil || !strings.Contains(err.Error(), "unsupplied URL") {
		t.Fatalf("expected unsupported citation error, got %v", err)
	}
}
