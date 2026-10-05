package agent

import (
	"context"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestCompanySearchPlanRecordsTheSearchTrace pins the company half of the Search results
// tab: every planned query's hits are recorded per query, split into what the entity gate
// kept and what it excluded — the same split the roster's synthesis is held to.
func TestCompanySearchPlanRecordsTheSearchTrace(t *testing.T) {
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	engine := NewEngine(&mockLLM{}, &profileSearcherMock{results: []models.SearchResult{
		{Title: "Acme Corporation — leadership", Link: "https://acme.com/team", Snippet: "The leadership team of Acme Corporation."},
		{Title: "Markets sink on ceasefire worries", Link: "https://news.example/x", Snippet: "Nifty below support as exporters slide."},
	}}, &mockScraper{}, store, 5, 3, 10)
	matcher := newEntityMatcher(models.CompanyBrief{CompanyName: "Acme Corporation", Domain: "acme.com"})

	results, trace, err := NewCompanyAgent(engine).searchPlan(context.Background(), "session",
		models.QueryPlan{Queries: []models.PlannedQuery{{Query: "Acme leadership executive team management", Purpose: "identify leadership"}}},
		nil, matcher)
	if err != nil {
		t.Fatalf("searchPlan: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected both hits merged into the run's results, got %d", len(results))
	}
	if trace == nil || len(trace.Queries) != 1 {
		t.Fatalf("expected one traced query, got %+v", trace)
	}
	query := trace.Queries[0]
	if len(query.Kept) != 1 || query.Kept[0].Link != "https://acme.com/team" {
		t.Errorf("expected the company-naming hit kept, got %+v", query.Kept)
	}
	if len(query.Excluded) != 1 || query.Excluded[0].Link != "https://news.example/x" {
		t.Errorf("expected the unrelated hit excluded, got %+v", query.Excluded)
	}
	if query.Purpose != "identify leadership" || query.Query != "Acme leadership executive team management" {
		t.Errorf("expected the query and its planned purpose carried on the trace, got %+v", query)
	}
}
