package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestClassifySourcePrioritizesWeeDooRegistry pins the source class: a WeeDoo
// page is company_registry in the India profile (the default for an Indian
// company), never in an explicitly global run, and never overrides the
// company's own pages.
func TestClassifySourcePrioritizesWeeDooRegistry(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "KLJ Resources Limited", Domain: "kljresources.in"}
	profile := classifySource(brief, "https://weedoo.in/company/klj-resources-limited")
	if profile.sourceType != "company_registry" || profile.quality < 0.75 {
		t.Fatalf("expected the WeeDoo registry class, got %+v", profile)
	}
	officialPage := classifySource(brief, "https://kljresources.in/management.php")
	if officialPage.sourceType != "official_leadership" {
		t.Fatalf("the company's own pages must keep their class, got %+v", officialPage)
	}
	global := models.CompanyBrief{CompanyName: "Acme Corp", SourceProfile: "global"}
	if classified := classifySource(global, "https://weedoo.in/company/acme"); classified.sourceType == "company_registry" {
		t.Fatal("the explicitly global source profile must not prioritise WeeDoo")
	}
}

// TestSelectScrapeCandidatesFetchesRegistryBeforeGenericPages pins the ladder:
// the registry page is fetched after the company's own pages but before
// leadership and external pages.
func TestSelectScrapeCandidatesFetchesRegistryBeforeGenericPages(t *testing.T) {
	ranked := []models.RankedResult{
		{URL: "https://example.com/news", SourceType: "external", Score: 0.9},
		{URL: "https://weedoo.in/company/klj-resources", SourceType: "company_registry", Score: 0.4},
		{URL: "https://www.kljresources.in/team", SourceType: "official", Official: true, Score: 0.5},
	}
	selected := selectScrapeCandidates(ranked, 3, false)
	if len(selected) != 3 || selected[0].SourceType != "official" || selected[1].SourceType != "company_registry" {
		t.Fatalf("expected official first then the registry page, got %+v", selected)
	}
	if selected[2].SourceType != "external" {
		t.Fatalf("expected generic pages after the registry page, got %+v", selected)
	}
}

// TestCompanyRegistryCandidatesAreEntityGatedAndCapped pins the enrichment's
// candidate selection: only the entity gate's accepted WeeDoo pages, in trace
// order, at most the three-page cap related mode uses.
func TestCompanyRegistryCandidatesAreEntityGatedAndCapped(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "KLJ Resources Limited", Domain: "kljresources.in", EntityConfirmed: true}
	matcher := newEntityMatcher(brief)
	results := []models.SearchResult{
		{Title: "KLJ Resources Limited directors", Link: "https://weedoo.in/company/klj-1", Snippet: "Directors and DINs of KLJ Resources Limited"},
		{Title: "KLJ Resources Limited overview", Link: "https://weedoo.in/company/klj-2", Snippet: "KLJ Resources Limited company overview"},
		{Title: "Sanjay Chlorides directors", Link: "https://weedoo.in/company/sanjay-chlorides", Snippet: "Directors of Sanjay Chlorides Pvt Ltd"},
		{Title: "KLJ Resources registration", Link: "https://weedoo.in/company/klj-3", Snippet: "Registration record of KLJ Resources Limited"},
		{Title: "KLJ Resources group", Link: "https://weedoo.in/company/klj-4", Snippet: "Group structure of KLJ Resources Limited"},
		{Title: "Unrelated directory page", Link: "https://weedoo.in/company/sanjay-chlorides-2", Snippet: "Sanjay Chlorides directors"},
		{Title: "Not a registry page", Link: "https://kljresources.in/about", Snippet: "KLJ Resources Limited about us"},
	}
	candidates := companyRegistryCandidates(results, matcher)
	if len(candidates) != 3 {
		t.Fatalf("expected the three-page cap, got %d (%+v)", len(candidates), candidates)
	}
	for _, candidate := range candidates {
		if strings.Contains(strings.ToLower(candidate.Title), "chlorides") {
			t.Fatalf("a directory page about another company must stay excluded, got %+v", candidate)
		}
		if !isCompanyRegistryURL(candidate.Link) {
			t.Fatalf("only registry pages may be selected, got %+v", candidate)
		}
	}
}

// TestScrapeRankedFetchesWeeDooRegistryFirst runs the scrape phase end to end:
// the WeeDoo page the searches surfaced is fetched as priority registry
// evidence with its full page content, recorded on the scrape report, and is
// fetched before the ordinary candidates.
func TestScrapeRankedFetchesWeeDooRegistryFirst(t *testing.T) {
	store := cache.NewMemoryCache(50)
	t.Cleanup(func() { store.Close() })
	engine := NewEngine(&mockLLM{}, &mockSearcher{}, &mockScraper{}, store, 5, 3, 20)
	agent := NewCompanyAgent(engine)
	brief := models.CompanyBrief{CompanyName: "KLJ Resources Limited", Domain: "kljresources.in", EntityConfirmed: true}
	matcher := agent.entityMatcherFor(brief)
	registryHit := models.SearchResult{
		Title:   "KLJ Resources Limited directors",
		Link:    "https://weedoo.in/company/klj-resources-limited",
		Snippet: "Directors and DINs of KLJ Resources Limited",
	}
	ranked := []models.RankedResult{
		{URL: "https://www.kljresources.in/team", Title: "Team", Snippet: "KLJ Resources Limited team", SourceType: "official", Official: true, Score: 0.9},
	}

	evidence, _, scrapeReport := agent.scrapeRankedWithBrief(context.Background(), "sess_test", brief, ranked, []models.SearchResult{registryHit}, nil, nil, matcher)

	registryIndex := -1
	for index, item := range evidence {
		if item.SourceType == "company_registry" && item.EvidenceType == "company_registry" {
			registryIndex = index
		}
	}
	if registryIndex != 0 {
		t.Fatalf("expected registry evidence first in the scrape, at index %d (%+v)", registryIndex, evidence)
	}
	if !strings.Contains(evidence[registryIndex].Content, "valid scraped content") {
		t.Fatalf("expected the full page as registry evidence, got %+v", evidence[registryIndex])
	}
	found := false
	for _, entry := range scrapeReport {
		if entry.SourceType == "company_registry" && entry.Status == "success" && strings.Contains(entry.Reason, "registry enrichment") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a successful registry scrape report entry, got %+v", scrapeReport)
	}
}