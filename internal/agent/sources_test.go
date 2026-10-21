package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestAlternativeProfileSourcesRespectTheCompanyDenyList is the guard that keeps the query
// builder honest: ZoomInfo is on lowQualityHosts, so a company-mode query for it would return
// pages the gate throws away before fetching them. The list is derived from the deny-list
// rather than hand-maintained, so the two cannot drift.
func TestAlternativeProfileSourcesRespectTheCompanyDenyList(t *testing.T) {
	accepted := companyAcceptedProfileSources()
	joined := strings.Join(accepted, ",")
	for _, want := range []string{"crunchbase.com", "clearbit.com", "apollo.io", "hunter.io"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %s to be usable for company queries, got %v", want, accepted)
		}
	}
	if strings.Contains(joined, "zoominfo.com") {
		t.Errorf("zoominfo is on the company deny-list, so no company query may target it: %v", accepted)
	}

	// The person path keeps all five: a directory page is a claim about a named person, which
	// that path already labels, rather than company evidence.
	if len(alternativeProfileSources) != 5 || !strings.Contains(strings.Join(alternativeProfileSources, ","), "zoominfo.com") {
		t.Errorf("expected all five aggregators to be available to the person path, got %v", alternativeProfileSources)
	}
}

func TestIndexedProfileQueriesUseCompanyOnlyAggregatorSlotsAndLinkedInRoleSlots(t *testing.T) {
	queries := indexedProfileQueries(models.CompanyBrief{CompanyName: "Sanjay Chemicals"})
	text := make([]string, 0, len(queries))
	for _, query := range queries {
		text = append(text, query.Query)
	}
	joined := strings.Join(text, "\n")
	for _, host := range []string{
		"site:linkedin.com/in/",
		"site:economictimes.indiatimes.com",
		"site:zaubacorp.com",
		"site:thecompanycheck.com",
		"site:tracxn.com",
		"site:crunchbase.com",
	} {
		want := 1
		if host == "site:linkedin.com/in/" {
			want = 3
		}
		if strings.Count(joined, host) != want {
			t.Errorf("expected %d dedicated queries for %s, got %d", want, host, strings.Count(joined, host))
		}
	}
	for _, query := range queries[:6] {
		if strings.Contains(query.Query, "OR") {
			t.Errorf("company-name-only slot must not contain role terms: %q", query.Query)
		}
	}
	if strings.Contains(joined, "site:naukri.com") {
		t.Error("Naukri must not receive a dedicated company search slot")
	}
}

// TestSourceSiteQueryShape pins the rendering: a base plus one site: per host, OR-joined.
func TestSourceSiteQueryShape(t *testing.T) {
	got := sourceSiteQuery("Jane Doe", []string{"a.com", "b.com"})
	if got != "Jane Doe (site:a.com OR site:b.com)" {
		t.Errorf("sourceSiteQuery = %q", got)
	}
	if sourceSiteQuery("", []string{"a.com"}) != "" || sourceSiteQuery("Jane Doe", nil) != "" {
		t.Error("expected an empty query when either half is missing")
	}
}

// TestIsProfileSourceURLAcceptsAggregators keeps the fetch priority in step with the query
// builder: a domain the planner searches but the fetch list ignores would contribute nothing.
func TestIsProfileSourceURLAcceptsAggregators(t *testing.T) {
	for _, target := range []string{
		"https://www.crunchbase.com/person/jane-doe",
		"https://www.zoominfo.com/p/Jane-Doe/1234",
		"https://clearbit.com/jane",
		"https://www.apollo.io/people/Jane/Doe",
		"https://hunter.io/email-verifier/jane",
		"https://www.linkedin.com/in/jane-doe",
		"https://en.wikipedia.org/wiki/Jane_Doe",
		"https://jane.example.com/about-me",
	} {
		if !isProfileSourceURL(target) {
			t.Errorf("expected %s to be worth fetching for a dossier", target)
		}
	}
	if isProfileSourceURL("https://timesofindia.indiatimes.com/business/story") {
		t.Error("a news article is not a profile source")
	}
}

// TestPersonResolveSkipsTheAggregatorPassWhenTheCorePassesAreProductive keeps the extra query
// bounded: it is a fallback for a thin answer, not a third search on every run.
func TestPersonResolveSkipsTheAggregatorPassWhenTheCorePassesAreProductive(t *testing.T) {
	searcher := &datedSearcher{results: []models.SearchResult{
		{Title: "Jane Doe - Head of Procurement - Acme | LinkedIn", Link: "https://www.linkedin.com/in/jane-doe", Snippet: "Jane Doe at Acme"},
		{Title: "Jane Doe - Director - Acme Corp", Link: "https://acme.com/team/jane-doe", Snippet: "Jane Doe, Director"},
	}}
	agent := NewPersonAgent(NewEngine(&mockLLM{}, searcher, &failingScraper{}, cache.NewMemoryCache(50), 10, 5, 20))

	candidates, err := agent.ResolvePersonCandidates(context.Background(), "Jane Doe", "Acme", "")
	if err != nil {
		t.Fatalf("ResolvePersonCandidates: %v", err)
	}
	if len(candidates) < 2 {
		t.Fatalf("expected the core passes to find candidates, got %+v", candidates)
	}
	if len(searcher.queries) != personResolveQueryLimit {
		t.Fatalf("expected exactly the core passes, got %v", searcher.queries)
	}
}

func TestEnforceEntityQueriesReservesDedicatedSourceSlots(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "Sanjay Chemicals", Domain: "sanjaychemindia.com"}
	full := make([]models.PlannedQuery, 0, maxCompanySearchQueries)
	for index := 0; index < maxCompanySearchQueries; index++ {
		full = append(full, models.PlannedQuery{
			Query:   fmt.Sprintf("Sanjay Chemicals department %d", index),
			Purpose: "planner output",
		})
	}

	plan, dropped := enforceEntityQueries(models.QueryPlan{Queries: full}, brief)
	if dropped != 0 {
		t.Fatalf("expected every anchored planner query to survive, dropped %d", dropped)
	}
	if len(plan.Queries) != maxCompanySearchQueries {
		t.Fatalf("expected the plan to stay at %d queries, got %d", maxCompanySearchQueries, len(plan.Queries))
	}
	found := false
	for _, query := range plan.Queries {
		if strings.Contains(query.Query, "site:crunchbase.com \"Sanjay Chemicals\"") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the dedicated Crunchbase query to hold its reserved slot, got %+v", plan.Queries)
	}
}
