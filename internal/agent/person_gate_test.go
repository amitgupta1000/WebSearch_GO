package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestPersonGateClassifiesSources pins the identity rule: a source reaches the dossier when it
// names the person, and — when a company was confirmed — a headline that states a different
// employer is a different person. A court-case or news headline that merely carries the name
// is not a stated employer, and a past employer in the body is a career history, not a
// contradiction.
func TestPersonGateClassifiesSources(t *testing.T) {
	gate := newPersonGate(models.PersonQuery{Name: "Jane Doe", Company: "Acme Corporation", Location: "Mumbai"})
	cases := []struct {
		name         string
		url          string
		title        string
		text         string
		want         string
		wantEmployer string
	}{
		{"names person and employer", "https://x/1", "Jane Doe - VP Procurement - Acme Corporation | LinkedIn", "Based in Mumbai.", personVerdictMatched, ""},
		{"names person and the pinned location", "https://x/2", "Jane Doe opens the Mumbai plant", "The expansion was announced on Monday.", personVerdictMatched, ""},
		{"names person, states no employer", "https://x/3", "Jane Doe will keynote the supply chain summit", "The annual summit returns in March.", personVerdictNameOnly, ""},
		{"different employer in the headline", "https://x/4", "Jane Doe - CTO - OtherCorp Ltd | LinkedIn", "View the full profile.", personVerdictConflicting, "OtherCorp Ltd"},
		{"role at a different employer", "https://x/5", "Jane Doe, VP Procurement at OtherCorp Ltd", "Contact details and history.", personVerdictConflicting, "OtherCorp Ltd"},
		{"does not name the person", "https://x/6", "Janet Doe - VP Procurement - Acme Corporation | LinkedIn", "Profile and history.", personVerdictUnrelated, ""},
		{"a court-case headline is not a stated employer", "https://x/7", "Jane Doe vs Bharat Enterprises on 3 March | Indian Kanoon", "The appeal concerned a customs assessment.", personVerdictNameOnly, ""},
		{"a past employer in the body is not a contradiction", "https://x/8", "Jane Doe - VP Procurement | LinkedIn", "Jane Doe previously worked at OtherCorp Ltd before joining Acme Corporation.", personVerdictMatched, ""},
	}
	for _, testCase := range cases {
		verdict := gate.classify(testCase.url, testCase.title, testCase.text)
		if verdict.Status != testCase.want {
			t.Errorf("%s: status = %q, want %q", testCase.name, verdict.Status, testCase.want)
		}
		if verdict.Employer != testCase.wantEmployer {
			t.Errorf("%s: employer = %q, want %q", testCase.name, verdict.Employer, testCase.wantEmployer)
		}
	}

	seeded := newPersonGate(models.PersonQuery{Name: "Jane Doe", SeedURL: "https://www.linkedin.com/in/jane-doe"})
	if verdict := seeded.classify("https://www.linkedin.com/in/jane-doe", "Some odd title", ""); verdict.Status != personVerdictConfirmed {
		t.Errorf("the confirmed seed is accepted even without context: status = %q, want %q", verdict.Status, personVerdictConfirmed)
	}

	// Sources punctuate legal names differently from how the operator typed them; the match
	// is token-based, so "Pvt. Ltd." matches a pinned "Pvt Ltd" and the right person's own
	// evidence is not excluded over a period. (Found live: the confirmed person's own hit
	// was dropped as name-only because of exactly this.)
	legalName := newPersonGate(models.PersonQuery{Name: "Dilip Parmar", Company: "Sanjay Chemicals (India) Pvt Ltd"})
	if verdict := legalName.classify("https://x/10", "Dilip Parmar - Sanjay Chemicals (India) Pvt. Ltd.", ""); verdict.Status != personVerdictMatched {
		t.Errorf("a legal name spelled with different punctuation must still match: %q", verdict.Status)
	}
	if verdict := legalName.classify("https://x/11", "Dilip Parmar - Director - OtherChem Pvt. Ltd.", ""); verdict.Status != personVerdictConflicting {
		t.Errorf("a different employer must still be caught despite the punctuation: %q", verdict.Status)
	}
	// Sources also drop the qualifier a legal name carries: "works at Sanjay Chemicals Pvt"
	// is the confirmed employer even though the pinned name carries "(India) Pvt Ltd".
	if verdict := legalName.classify("https://x/12", "Dilip Parmar's email & phone - Sanjay Chemicals Pvt", "Dilip Parmar works as a Managing Director at Sanjay Chemicals Pvt, a Manufacturing company."); verdict.Status != personVerdictMatched {
		t.Errorf("the employer with its qualifier dropped must still match: %q", verdict.Status)
	}
	if verdict := legalName.classify("https://x/13", "Dilip Parmar - Director - Sanjay Sourcing Private Limited", "Board of directors."); verdict.Status != personVerdictConflicting {
		t.Errorf("a namesake at a similarly named company must not match: %q", verdict.Status)
	}
}

// TestPersonGateGroupsTheSameNamedPeople pins the multilateral view: conflicting sources
// group by the employer each one states (each group is another person with the name), the
// material that names the person but states no employer groups under the unattributed key and
// closes the list, sources that never name the person stay out, and the caps hold.
func TestPersonGateGroupsTheSameNamedPeople(t *testing.T) {
	gate := newPersonGate(models.PersonQuery{Name: "Jane Doe", Company: "Acme Corporation"})
	excluded := []models.SearchResult{
		{Title: "Jane Doe - CTO - OtherCorp Ltd | LinkedIn", Link: "https://x/a", Snippet: "Jane Doe — CTO at OtherCorp Ltd."},
		{Title: "Jane Doe - Engineer - OtherCorp Ltd | Company blog", Link: "https://x/b", Snippet: "Posts by Jane Doe about distributed systems."},
		{Title: "Jane Doe - CFO - ThirdCorp Industries | LinkedIn", Link: "https://x/c", Snippet: "Jane Doe — CFO at ThirdCorp Industries."},
		{Title: "Jane Doe will keynote the supply chain summit", Link: "https://x/d", Snippet: "Jane Doe joins the speaker lineup."},
		{Title: "Jane Doe - Alumni - State University", Link: "https://x/e", Snippet: "Class of 2005."},
		{Title: "Janet Doe - VP - Acme Corporation | LinkedIn", Link: "https://x/f", Snippet: "Profile and history."},
	}
	groups := gate.sameNamedGroups(excluded)
	if len(groups) != 3 {
		t.Fatalf("expected three identity groups, got %+v", groups)
	}
	if groups[0].Employer != "OtherCorp Ltd" || len(groups[0].Snippets) != 2 {
		t.Fatalf("expected the most-evidenced identity first with both its snippets, got %+v", groups[0])
	}
	if groups[1].Employer != "ThirdCorp Industries" || len(groups[1].Snippets) != 1 {
		t.Fatalf("expected the second identity with its snippet, got %+v", groups[1])
	}
	if groups[2].Employer != "" || len(groups[2].Snippets) != 2 {
		t.Fatalf("expected the unattributed group last with both name-only snippets, got %+v", groups[2])
	}
	for _, group := range groups {
		for _, snippet := range group.Snippets {
			if snippet.Link == "https://x/f" {
				t.Errorf("a source that never names the person must not appear in the multilateral view")
			}
		}
	}

	flood := make([]models.SearchResult, 0, personGroupSnippetCap+2)
	for index := 0; index < personGroupSnippetCap+2; index++ {
		flood = append(flood, models.SearchResult{
			Title:   "Jane Doe - CTO - OtherCorp Ltd | LinkedIn",
			Link:    fmt.Sprintf("https://x/flood-%d", index),
			Snippet: "Jane Doe — CTO at OtherCorp Ltd.",
		})
	}
	capped := gate.sameNamedGroups(flood)
	if len(capped) != 1 || len(capped[0].Snippets) != personGroupSnippetCap {
		t.Fatalf("expected the per-group snippet cap enforced, got %+v", capped)
	}
}

// TestPersonGateHoldsSnippetsHarderThanPages pins the strictness split: a snippet is a
// free-floating assertion, so when a company was confirmed it must name that company or the
// pinned location; a fetched page that names the person alone is still read. Without a
// confirmed company there is nothing to hold snippets to, so a name-alone run keeps them.
func TestPersonGateHoldsSnippetsHarderThanPages(t *testing.T) {
	results := []models.SearchResult{
		{Title: "Jane Doe - VP Procurement - Acme Corporation | LinkedIn", Link: "https://x/1", Snippet: "Jane Doe at Acme Corporation, Mumbai."},
		{Title: "Jane Doe will keynote the supply chain summit", Link: "https://x/2", Snippet: "Jane Doe joins the speaker lineup."},
	}
	gate := newPersonGate(models.PersonQuery{Name: "Jane Doe", Company: "Acme Corporation"})
	kept, excluded := gate.filterSnippets(results)
	if len(kept) != 1 || kept[0].Link != "https://x/1" {
		t.Fatalf("expected only the company-naming snippet kept, kept=%v excluded=%v", kept, excluded)
	}
	if len(excluded) != 1 || excluded[0].Link != "https://x/2" {
		t.Fatalf("expected the name-only snippet excluded, kept=%v excluded=%v", kept, excluded)
	}

	alone := newPersonGate(models.PersonQuery{Name: "Jane Doe"})
	kept, excluded = alone.filterSnippets(results)
	if len(kept) != 2 || len(excluded) != 0 {
		t.Fatalf("a name-alone run has no company to hold snippets to: kept=%d excluded=%d", len(kept), len(excluded))
	}
}

// TestPersonResearchDropsTheSameNamedPersonAtAnotherEmployer is the regression for the report
// that motivated the gate: a person run supplied with a company and a location still returned
// a same-named person at a different company in a different location, because every snippet
// the search engine loosely matched went to the synthesiser unfiltered. Now the conflicting
// source is excluded before it is fetched or synthesised, and the exclusion is listed.
func TestPersonResearchDropsTheSameNamedPersonAtAnotherEmployer(t *testing.T) {
	target := "https://www.linkedin.com/in/jane-doe"
	searcher := &profileSearcherMock{results: []models.SearchResult{
		{Title: "Jane Doe - VP Procurement - Acme Corporation | LinkedIn", Link: target, Snippet: "Jane Doe — VP Procurement at Acme Corporation, Mumbai."},
		{Title: "Jane Doe - CTO - OtherCorp Ltd | LinkedIn", Link: "https://www.linkedin.com/in/jane-doe-othercorp", Snippet: "Jane Doe — CTO at OtherCorp Ltd, Bengaluru."},
		{Title: "Jane Doe will keynote the supply chain summit", Link: "https://supplychainnews.example/jane-doe-keynote", Snippet: "Jane Doe joins the speaker lineup."},
		{Title: "Board results: Jane Doe passes with distinction", Link: "https://results.example/jane-doe-2019", Snippet: "Jane Doe passed the examination."},
	}}
	scraper := &sessionAwareScraperMock{recovered: recoveredProfile(target)}
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	engine := NewEngine(&mockLLM{}, searcher, scraper, store, 5, 3, 10)

	profile, err := NewPersonAgent(engine).ResearchPerson(context.Background(), "session",
		models.PersonQuery{Name: "Jane Doe", Company: "Acme Corporation", Location: "Mumbai"}, nil)
	if err != nil {
		t.Fatalf("ResearchPerson: %v", err)
	}

	if scraper.hardCalls != 1 {
		t.Fatalf("expected exactly one page fetch (the matched profile), got %d — the other-person page must never be fetched", scraper.hardCalls)
	}
	if profile.PersonGate == nil {
		t.Fatalf("expected the identity check to be attached to the dossier")
	}
	if profile.PersonGate.Company != "Acme Corporation" || profile.PersonGate.Dropped != 3 {
		t.Fatalf("expected three exclusions for Acme Corporation, got %+v", profile.PersonGate)
	}
	if profile.PersonGate.Kept == 0 {
		t.Fatalf("expected the matched sources to be kept, got %+v", profile.PersonGate)
	}
	var conflicting *models.OffEntityRow
	for index := range profile.PersonGate.Excluded {
		if strings.Contains(profile.PersonGate.Excluded[index].Reason, "OtherCorp Ltd") {
			conflicting = &profile.PersonGate.Excluded[index]
		}
	}
	if conflicting == nil {
		t.Fatalf("expected the different-employer source excluded with its stated employer, got %+v", profile.PersonGate.Excluded)
	}
	if conflicting.Source != "https://www.linkedin.com/in/jane-doe-othercorp" {
		t.Errorf("expected the exclusion to point at the other-person profile, got %q", conflicting.Source)
	}

	// The multilateral view: the conflicting source becomes one identity group, and the
	// name-only material becomes the unattributed group — visible instead of discarded.
	if len(profile.SameNamed) != 2 {
		t.Fatalf("expected two same-named identity groups, got %+v", profile.SameNamed)
	}
	if profile.SameNamed[0].Employer != "OtherCorp Ltd" || len(profile.SameNamed[0].Snippets) != 1 {
		t.Errorf("expected the other person's identity group with its snippet, got %+v", profile.SameNamed[0])
	}
	if profile.SameNamed[1].Employer != "" || len(profile.SameNamed[1].Snippets) != 2 {
		t.Errorf("expected the unattributed group with both name-only snippets, got %+v", profile.SameNamed[1])
	}

	// The search trace is the data behind the Search results tab: every planned query with
	// its kept and excluded hits, recorded per query before cross-query dedupe.
	if profile.SearchTrace == nil || len(profile.SearchTrace.Queries) != 5 {
		t.Fatalf("expected the five planned searches traced, got %+v", profile.SearchTrace)
	}
	first := profile.SearchTrace.Queries[0]
	if len(first.Kept) != 1 || first.Kept[0].Link != target {
		t.Errorf("expected the matched profile as the first query's kept hit, got %+v", first.Kept)
	}
	if len(first.Excluded) != 3 {
		t.Errorf("expected the other-person and name-only hits excluded on the first query, got %+v", first.Excluded)
	}
	if profile.SearchTrace.KeptCount() == 0 || profile.SearchTrace.ExcludedCount() == 0 {
		t.Errorf("expected the trace counters to be populated, got kept=%d excluded=%d",
			profile.SearchTrace.KeptCount(), profile.SearchTrace.ExcludedCount())
	}

	// The operating results the report also asked for: queries, timings, and every fetch.
	if len(profile.SearchQueries) != 5 {
		t.Errorf("expected the five planned searches recorded, got %v", profile.SearchQueries)
	}
	if len(profile.Timings) == 0 {
		t.Errorf("expected phase timings on the dossier")
	}
	if len(profile.ScrapeReport) != 1 || profile.ScrapeReport[0].Status != "ok" || profile.ScrapeReport[0].URL != target {
		t.Errorf("expected one successful fetch reported for the matched profile, got %+v", profile.ScrapeReport)
	}
	if profile.ScrapeReport[0].WordCount == 0 {
		t.Errorf("expected the report row to carry the fetched word count")
	}
}

// TestPersonResearchKeepsNameAloneRunsUnfiltered pins the fallback: with no company confirmed
// there is nothing to hold sources to, so a name-alone run excludes nothing rather than
// returning an empty dossier.
func TestPersonResearchKeepsNameAloneRunsUnfiltered(t *testing.T) {
	target := "https://www.linkedin.com/in/jane-doe"
	searcher := &profileSearcherMock{results: []models.SearchResult{
		{Title: "Jane Doe - VP Procurement - Acme Corporation | LinkedIn", Link: target, Snippet: "Jane Doe — VP Procurement at Acme Corporation, Mumbai."},
		{Title: "Jane Doe - CTO - OtherCorp Ltd | LinkedIn", Link: "https://www.linkedin.com/in/jane-doe-othercorp", Snippet: "Jane Doe — CTO at OtherCorp Ltd, Bengaluru."},
		{Title: "Jane Doe will keynote the supply chain summit", Link: "https://supplychainnews.example/jane-doe-keynote", Snippet: "Jane Doe joins the speaker lineup."},
	}}
	scraper := &sessionAwareScraperMock{recovered: recoveredProfile(target)}
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	engine := NewEngine(&mockLLM{}, searcher, scraper, store, 5, 3, 10)

	profile, err := NewPersonAgent(engine).ResearchPerson(context.Background(), "session",
		models.PersonQuery{Name: "Jane Doe"}, nil)
	if err != nil {
		t.Fatalf("ResearchPerson: %v", err)
	}
	if profile.PersonGate == nil || profile.PersonGate.Dropped != 0 {
		t.Fatalf("a name-alone run has no confirmed employer to contradict: %+v", profile.PersonGate)
	}
	if len(profile.ScrapeReport) == 0 {
		t.Fatalf("expected the fetch report even on a name-alone run")
	}
}
