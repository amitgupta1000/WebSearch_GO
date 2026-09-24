package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestParsePersonCandidateReadsHeadlineShapes covers the only structured person data a
// snippet carries: professional networks title their hits "<name> - <role> - <employer>".
// Reading that is what turns a name into an identity with a company and a place.
func TestParsePersonCandidateReadsHeadlineShapes(t *testing.T) {
	cases := []struct {
		name    string
		typed   string
		title   string
		snippet string
		want    models.PersonCandidate
		ok      bool
	}{
		{
			name:  "professional network headline",
			title: "Sanjay Kumar Gupta - Managing Director - Sanjay Chemicals (India) Pvt Ltd | LinkedIn",
			want: models.PersonCandidate{
				Name: "Sanjay Kumar Gupta", Title: "Managing Director", Company: "Sanjay Chemicals (India) Pvt Ltd",
			},
			ok: true,
		},
		{
			name:  "role at employer",
			title: "Sanjay Gupta - Head of Procurement at Sanjay Chemicals | LinkedIn",
			want: models.PersonCandidate{
				Name: "Sanjay Gupta", Title: "Head of Procurement", Company: "Sanjay Chemicals",
			},
			ok: true,
		},
		{
			name:  "role in the same segment",
			typed: "Jane Doe",
			title: "Jane Doe, Managing Director - Acme Corp",
			want: models.PersonCandidate{
				Name: "Jane Doe", Title: "Managing Director", Company: "Acme Corp",
			},
			ok: true,
		},
		{
			name:  "honorific kept, trailing prose dropped",
			title: "Dr. Sanjay Gupta of IIT Bombay - Faculty",
			want: models.PersonCandidate{
				Name: "Dr. Sanjay Gupta",
			},
			ok: true,
		},
		{
			name:  "bare profile",
			title: "Sanjay Gupta | LinkedIn",
			want:  models.PersonCandidate{Name: "Sanjay Gupta"},
			ok:    true,
		},
		{
			name:  "a company page is not a person",
			title: "Sanjay Chemicals (India) Pvt Ltd - Home",
			ok:    false,
		},
		{
			name:  "a partial name is not an identity",
			title: "Gupta Chemicals - Suppliers",
			ok:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			typed := tc.typed
			if typed == "" {
				typed = "Sanjay Gupta"
			}
			candidate, ok := parsePersonCandidate(
				models.SearchResult{Title: tc.title, Link: "https://example.com/x", Snippet: tc.snippet},
				nameTokens(typed), "", "",
			)
			if ok != tc.ok {
				t.Fatalf("expected ok=%t, got %+v", tc.ok, candidate)
			}
			if !ok {
				return
			}
			if candidate.Name != tc.want.Name {
				t.Errorf("expected name %q, got %q", tc.want.Name, candidate.Name)
			}
			if tc.want.Title != "" && candidate.Title != tc.want.Title {
				t.Errorf("expected title %q, got %q", tc.want.Title, candidate.Title)
			}
			if tc.want.Company != "" && candidate.Company != tc.want.Company {
				t.Errorf("expected company %q, got %q", tc.want.Company, candidate.Company)
			}
		})
	}
}

// TestScorePersonCandidatePrefersContextThenSubstance pins the ranking an operator would
// apply: someone whose hit already names the company and city typed leads, and what a hit
// *states* (role, employer) outranks where it lives — a directory listing that states both is
// more useful for disambiguation than a bare profile, and the host only settles ties.
func TestScorePersonCandidatePrefersContextThenSubstance(t *testing.T) {
	profile := models.PersonCandidate{Name: "Sanjay Gupta", Title: "Managing Director", Company: "Sanjay Chemicals"}
	contextMatch := scorePersonCandidate(models.PersonCandidate{
		Name: "Sanjay Gupta", Title: "Managing Director", Company: "Sanjay Chemicals",
		MatchesCompany: true, MatchesLocation: true,
	}, "https://www.linkedin.com/in/sanjay-gupta", 2)
	profileOnLinkedIn := scorePersonCandidate(profile, "https://www.linkedin.com/in/sanjay-gupta", 2)
	profileOnDirectory := scorePersonCandidate(profile, "https://rocketreach.co/sanjay-gupta", 2)
	bareOnLinkedIn := scorePersonCandidate(models.PersonCandidate{Name: "Sanjay Gupta"}, "https://www.linkedin.com/in/x", 2)

	if !(contextMatch > profileOnLinkedIn && profileOnLinkedIn > profileOnDirectory) {
		t.Errorf("expected context > profile > directory, got context=%.2f profile=%.2f directory=%.2f",
			contextMatch, profileOnLinkedIn, profileOnDirectory)
	}
	if !(profileOnDirectory > bareOnLinkedIn) {
		t.Errorf("expected a stated role on a directory to beat a bare profile, got directory=%.2f bare=%.2f",
			profileOnDirectory, bareOnLinkedIn)
	}
}

// TestResolvePersonCandidatesRanksAndCaps covers the step's output: the person whose hit
// names the typed company comes first, non-people are dropped, and the list is a choice
// rather than a dump.
func TestResolvePersonCandidatesRanksAndCaps(t *testing.T) {
	searcher := &resultSearcher{results: []models.SearchResult{
		{Title: "Sanjay Gupta | LinkedIn", Link: "https://www.linkedin.com/in/sanjay-gupta", Snippet: "Mumbai"},
		{Title: "Sanjay Gupta - Managing Director - Sanjay Chemicals | LinkedIn", Link: "https://www.linkedin.com/in/sanjay-gupta-sanjaychemicals", Snippet: "Mumbai, Maharashtra"},
		{Title: "Sanjay Gupta - Professor - IIT Bombay", Link: "https://www.iitb.ac.in/faculty/sanjay-gupta", Snippet: "Chemistry"},
		{Title: "Sanjay Gupta - Journalist - Times of India", Link: "https://timesofindia.indiatimes.com/author/sanjay-gupta", Snippet: "Delhi"},
		{Title: "Sanjay Chemicals (India) Pvt Ltd - Home", Link: "https://sanjaychemindia.com/", Snippet: "Mumbai"},
		{Title: "Sanjay Gupta", Link: "https://rocketreach.co/sanjay-gupta-email_123", Snippet: "Director"},
	}}
	agent := NewPersonAgent(NewEngine(&mockLLM{}, searcher, &failingScraper{}, cache.NewMemoryCache(50), 10, 5, 20))

	candidates, err := agent.ResolvePersonCandidates(context.Background(), "Sanjay Gupta", "Sanjay Chemicals", "Mumbai")
	if err != nil {
		t.Fatalf("ResolvePersonCandidates: %v", err)
	}
	if len(candidates) == 0 || len(candidates) > personResolveMaxCandidates {
		t.Fatalf("expected 1..%d candidates, got %d (%+v)", personResolveMaxCandidates, len(candidates), candidates)
	}
	top := candidates[0]
	if top.Company != "Sanjay Chemicals" || !top.MatchesCompany {
		t.Fatalf("expected the hit naming the typed company to lead, got %+v", top)
	}
	for _, candidate := range candidates {
		if strings.Contains(candidate.URL, "sanjaychemindia.com") {
			t.Errorf("a company page must not be offered as a person: %+v", candidate)
		}
	}
}

// TestResolvePersonCandidatesAsksForContextWhenNoneWasTyped keeps the lookup useful on a
// bare name: with nothing to disambiguate, the follow-up query has to ask for the pages that
// state a role and employer, because a bare-name hit states neither.
func TestResolvePersonCandidatesAsksForContextWhenNoneWasTyped(t *testing.T) {
	searcher := &datedSearcher{}
	agent := NewPersonAgent(NewEngine(&mockLLM{}, searcher, &failingScraper{}, cache.NewMemoryCache(50), 10, 5, 20))

	if _, err := agent.ResolvePersonCandidates(context.Background(), "Sanjay Gupta", "", ""); err != nil {
		t.Fatalf("ResolvePersonCandidates: %v", err)
	}
	if len(searcher.queries) != personResolveQueryLimit {
		t.Fatalf("expected %d queries, got %v", personResolveQueryLimit, searcher.queries)
	}
	if !strings.Contains(searcher.queries[1], "linkedin profile") {
		t.Errorf("expected the second query to ask for profiles, got %v", searcher.queries)
	}

	// With a company typed, the tight query leads and the wider follow-up is the bare name.
	searcher.queries = nil
	if _, err := agent.ResolvePersonCandidates(context.Background(), "Sanjay Gupta", "Sanjay Chemicals", ""); err != nil {
		t.Fatalf("ResolvePersonCandidates: %v", err)
	}
	if len(searcher.queries) == 0 || searcher.queries[0] != "Sanjay Gupta Sanjay Chemicals" {
		t.Fatalf("expected the company-scoped query first, got %v", searcher.queries)
	}
}
