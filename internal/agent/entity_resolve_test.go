package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// resultSearcher returns a fixed result set, standing in for the provider.
type resultSearcher struct{ results []models.SearchResult }

func (s *resultSearcher) Search(_ context.Context, _ string, _ int) ([]models.SearchResult, error) {
	return s.results, nil
}

// TestResolveEntityCandidatesOffersCompaniesNotDirectories covers the point of the
// dialogue: the operator chooses between real candidate domains — the chemicals company
// and the same-named recruitment firm — not between aggregator pages, and the
// registration number arrives with the page it was read from.
func TestResolveEntityCandidatesOffersCompaniesNotDirectories(t *testing.T) {
	searcher := &resultSearcher{results: []models.SearchResult{
		{Title: "CJ Shah - Home", Link: "https://www.cjshah.com/", Snippet: "CJ Shah group, Mumbai"},
		{Title: "CJ Shah & Co - recruitment consultants", Link: "https://www.cjshahco.com/", Snippet: "CJ Shah and Co, Mumbai"},
		{Title: "CJ SHAH AND CO PRIVATE LIMITED - ZaubaCorp", Link: "https://www.zaubacorp.com/company/CJ-SHAH-AND-CO-PRIVATE-LIMITED/U74999MH2005PTC152345", Snippet: "CIN: U74999MH2005PTC152345"},
		{Title: "CJ Shah | LinkedIn", Link: "https://www.linkedin.com/company/cj-shah/", Snippet: "CJ Shah on LinkedIn"},
		{Title: "CJ Shah salaries | AmbitionBox", Link: "https://www.ambitionbox.com/salaries/cj-shah-and-co-salaries", Snippet: "Salaries"},
	}}
	engine := NewEngine(&mockLLM{}, searcher, &failingScraper{}, cache.NewMemoryCache(50), 10, 5, 20)
	agent := NewCompanyAgent(engine)

	candidates, err := agent.ResolveEntityCandidates(context.Background(), "CJ Shah", "India", "Mumbai")
	if err != nil {
		t.Fatalf("ResolveEntityCandidates: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected the two candidate companies, got %+v", candidates)
	}
	domains := make(map[string]models.EntityCandidate, len(candidates))
	for _, candidate := range candidates {
		domains[candidate.Domain] = candidate
		if isRegistryHost(candidate.Domain) {
			t.Errorf("a directory must never be offered as the entity domain: %+v", candidate)
		}
	}
	top := candidates[0]
	if top.Domain != "cjshah.com" {
		t.Fatalf("expected the name-echoing domain first, got %+v", candidates)
	}
	if top.Score <= domains["cjshahco.com"].Score {
		t.Errorf("expected the name-echoing domain to outrank the other candidate: %+v", candidates)
	}
	// The CIN is offered with its source, not silently attached.
	if top.RegistrationID != "U74999MH2005PTC152345" {
		t.Fatalf("expected the registry CIN on the top candidate, got %q", top.RegistrationID)
	}
	if !strings.Contains(top.RegistrationSource, "zaubacorp.com") {
		t.Fatalf("expected the CIN's source, got %q", top.RegistrationSource)
	}
}

// TestResolveEntityCandidatesDropsNonNameMatchingDomains is the safety rule the live
// dialogue needed: a rating agency or a sanctions list mentions the company and its
// searches return it, but choosing one as "the company" would make every page on that
// domain count as company material. They are dropped when a name-matching domain exists.
func TestResolveEntityCandidatesDropsNonNameMatchingDomains(t *testing.T) {
	searcher := &resultSearcher{results: []models.SearchResult{
		{Title: "CJ Shah Group - Home", Link: "https://cjshahgroup.in/", Snippet: "CJ Shah Group"},
		{Title: "CJ Shah Limited - rating rationale", Link: "https://www.crisilratings.com/rating/CJShah.html", Snippet: "CJ Shah Limited rating"},
		{Title: "OpenSanctions: CJ SHAH", Link: "https://www.opensanctions.org/entities/CJ-SHAH/", Snippet: "CJ Shah"},
	}}
	engine := NewEngine(&mockLLM{}, searcher, &failingScraper{}, cache.NewMemoryCache(50), 10, 5, 20)
	agent := NewCompanyAgent(engine)

	candidates, err := agent.ResolveEntityCandidates(context.Background(), "CJ Shah", "India", "Mumbai")
	if err != nil {
		t.Fatalf("ResolveEntityCandidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Domain != "cjshahgroup.in" {
		t.Fatalf("expected only the name-matching domain, got %+v", candidates)
	}
	if !candidates[0].NameMatch {
		t.Errorf("expected the surviving candidate to be flagged as a name match: %+v", candidates[0])
	}
}

func TestResolveEntityCandidatesRequiresAName(t *testing.T) {
	engine := NewEngine(&mockLLM{}, &resultSearcher{}, &failingScraper{}, cache.NewMemoryCache(10), 10, 5, 20)
	agent := NewCompanyAgent(engine)
	if _, err := agent.ResolveEntityCandidates(context.Background(), "   ", "", ""); err == nil {
		t.Fatal("expected an error for an empty name")
	}
}

// TestRegistrationIDPattern pins the CIN shape, including the ones that appear in real
// registry URLs.
func TestRegistrationIDPattern(t *testing.T) {
	cases := map[string]string{
		"https://www.zaubacorp.com/company/X/U74999MH2005PTC152345": "U74999MH2005PTC152345",
		"L67120WB1986PLC041487": "L67120WB1986PLC041487",
		"no identifier here":    "",
	}
	for input, want := range cases {
		if got := firstRegistrationID(input); got != want {
			t.Errorf("firstRegistrationID(%q) = %q, want %q", input, got, want)
		}
	}
}
