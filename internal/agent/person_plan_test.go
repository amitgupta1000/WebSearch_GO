package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestPersonPlanAnchorsEveryQuery pins the plan: every query carries the confirmed name, the
// confirmed company anchors the company run's searches, the aggregator query keeps its
// snippets-only slot, and the plan stays inside its cap.
func TestPersonPlanAnchorsEveryQuery(t *testing.T) {
	plan := buildPersonPlan("Dilip Parmar", "Sanjay Chemicals (India) Pvt Ltd", "Vapi", "India")
	if len(plan) != 6 {
		t.Fatalf("expected six planned searches for a company run, got %d: %+v", len(plan), plan)
	}
	seenSections := make(map[string]bool, len(plan))
	for _, entry := range plan {
		if !strings.Contains(entry.query, `"Dilip Parmar"`) {
			t.Errorf("planned query %q is not anchored on the person", entry.query)
		}
		if entry.section == personDirectorySection {
			if !strings.Contains(entry.query, "site:crunchbase.com") || !entry.snippetsOnly {
				t.Errorf("the directory entry must target the aggregators and stay snippets-only, got %+v", entry)
			}
		} else if entry.snippetsOnly && !entry.personalization {
			t.Errorf("only the directory entry is snippets-only, got %+v", entry)
		}
		social := 0
		for _, entry := range plan {
			if entry.personalization {
				social++
			}
		}
		if social != 1 {
			t.Fatalf("expected one bounded X/Twitter personalization query, got %d", social)
		}
		if entry.section == "" || seenSections[entry.section] {
			t.Errorf("every plan entry needs a unique context section, got %q", entry.section)
		}
		seenSections[entry.section] = true
	}
	if !strings.Contains(plan[0].query, "Sanjay Chemicals") {
		t.Errorf("the primary query must carry the confirmed company, got %q", plan[0].query)
	}

	alone := buildPersonPlan("Jane Doe", "", "", "")
	if len(alone) != 4 {
		t.Fatalf("expected four planned searches for a name-alone run, got %d", len(alone))
	}
	for _, entry := range alone {
		if strings.Contains(entry.query, "Acme") {
			t.Errorf("a name-alone plan must not invent a company, got %q", entry.query)
		}
	}
}

// TestPersonResearchRunsThePlannedSearches proves the plan replaced the three hardcoded
// searches: the dossier records every planned query it issued.
func TestPersonResearchRunsThePlannedSearches(t *testing.T) {
	target := "https://www.linkedin.com/in/jane-doe"
	scraper := &sessionAwareScraperMock{recovered: recoveredProfile(target)}
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	engine := NewEngine(&mockLLM{}, &profileSearcherMock{results: []models.SearchResult{
		{Title: "Jane Doe - VP Procurement - Acme Corporation | LinkedIn", Link: target, Snippet: "Jane Doe — VP Procurement at Acme Corporation, Mumbai."},
	}}, scraper, store, 5, 3, 10)

	profile, err := NewPersonAgent(engine).ResearchPerson(context.Background(), "session",
		models.PersonQuery{Name: "Jane Doe", Company: "Acme Corporation"}, nil)
	if err != nil {
		t.Fatalf("ResearchPerson: %v", err)
	}
	if len(profile.SearchQueries) != 6 {
		t.Fatalf("expected the six planned searches, got %v", profile.SearchQueries)
	}
	for _, query := range profile.SearchQueries {
		if !strings.Contains(query, `"Jane Doe"`) {
			t.Errorf("every issued query must stay anchored on the person, got %q", query)
		}
	}
}

// TestPersonQCDropsRowsAnotherPersonOwns is the post-synthesis half of the fix: with
// validation disabled (no rescue lookups), a synthesised row whose employer is not the
// confirmed one is dropped, a row at the confirmed employer survives, a row with no employer
// at all is dropped, and the headline employer is corrected to the confirmed one. Everything
// dropped is listed for the dossier's identity check.
func TestPersonQCDropsRowsAnotherPersonOwns(t *testing.T) {
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	engine := NewEngine(&mockLLM{}, &profileSearcherMock{}, &mockScraper{}, store, 5, 3, 10, WithValidation(false, 0, 0, 0))
	gate := newPersonGate(models.PersonQuery{Name: "Jane Doe", Company: "Acme Corporation"})
	profile := &models.PersonProfile{
		CurrentCompany: "OtherCorp Ltd",
		Experiences: []models.Experience{
			{Role: "VP Procurement", Company: "Acme Corporation", Period: "2019-present"},
			{Role: "CTO", Company: "OtherCorp Ltd", Period: "2015-2019"},
			{Role: "Mystery role"},
		},
	}

	excluded, lookups := NewPersonAgent(engine).qcPersonProfile(context.Background(), profile, gate)
	if lookups != 0 {
		t.Errorf("validation is disabled, so no lookups should have been spent, got %d", lookups)
	}
	if len(profile.Experiences) != 1 || profile.Experiences[0].Company != "Acme Corporation" {
		t.Fatalf("expected only the confirmed-employer row to survive, got %+v", profile.Experiences)
	}
	if profile.CurrentCompany != "Acme Corporation" {
		t.Errorf("expected the headline employer corrected to the confirmed one, got %q", profile.CurrentCompany)
	}
	if len(excluded) != 3 {
		t.Fatalf("expected the blended row, the unnamed row, and the wrong headline employer excluded, got %+v", excluded)
	}
	for _, row := range excluded {
		if !strings.Contains(row.Reason, "QC") && !strings.Contains(row.Reason, "corrected") {
			t.Errorf("every exclusion must say why, got %+v", row)
		}
	}
}

// TestPersonQCRescuesARowThroughALookup pins the career-history half of the rule: a row at a
// different employer survives when a bounded snippet-only reverse lookup ties the name to
// that employer — which is how this person's past job is told apart from another person's
// current one.
func TestPersonQCRescuesARowThroughALookup(t *testing.T) {
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	searcher := &profileSearcherMock{results: []models.SearchResult{
		{Title: "Jane Doe — career", Snippet: "Jane Doe began her career at OtherCorp Ltd before joining Acme Corporation."},
	}}
	engine := NewEngine(&mockLLM{}, searcher, &mockScraper{}, store, 5, 3, 10)
	gate := newPersonGate(models.PersonQuery{Name: "Jane Doe", Company: "Acme Corporation"})
	profile := &models.PersonProfile{
		CurrentCompany: "Acme Corporation",
		Experiences: []models.Experience{
			{Role: "Engineer", Company: "OtherCorp Ltd", Period: "2012-2015", Source: "https://example.com/jane"},
		},
	}

	excluded, lookups := NewPersonAgent(engine).qcPersonProfile(context.Background(), profile, gate)
	if lookups != 1 {
		t.Fatalf("expected exactly one rescue lookup, got %d", lookups)
	}
	if len(profile.Experiences) != 1 || profile.Experiences[0].Company != "OtherCorp Ltd" {
		t.Fatalf("expected the past-employer row rescued, got %+v", profile.Experiences)
	}
	if len(excluded) != 0 {
		t.Fatalf("a rescued row is not an exclusion, got %+v", excluded)
	}
}

// TestPersonQCAcceptsTheEmployerSpelledDifferently pins the token-based employer comparison:
// a synthesised row whose employer is the confirmed one spelled differently ("Pvt. Ltd." vs
// "Pvt Ltd") survives — the comparison is over significant tokens, not substrings.
func TestPersonQCAcceptsTheEmployerSpelledDifferently(t *testing.T) {
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	engine := NewEngine(&mockLLM{}, &profileSearcherMock{}, &mockScraper{}, store, 5, 3, 10)
	gate := newPersonGate(models.PersonQuery{Name: "Dilip Parmar", Company: "Sanjay Chemicals (India) Pvt Ltd"})
	profile := &models.PersonProfile{
		CurrentCompany: "Sanjay Chemicals (India) Pvt. Ltd.",
		Experiences: []models.Experience{
			{Role: "Director", Company: "Sanjay Chemicals (India) Pvt. Ltd.", Source: "https://india5000.com/sanjay-chemicals"},
		},
	}

	excluded, lookups := NewPersonAgent(engine).qcPersonProfile(context.Background(), profile, gate)
	if len(excluded) != 0 || lookups != 0 {
		t.Fatalf("the confirmed employer spelled differently is not an exclusion, got %+v", excluded)
	}
	if len(profile.Experiences) != 1 || profile.CurrentCompany != "Sanjay Chemicals (India) Pvt. Ltd." {
		t.Fatalf("QC must leave a matching employer untouched, got %+v", profile)
	}
}

// TestPersonQCDedupesRepeatedRows pins the repetition fix: the model writes the same role
// once per supporting snippet, and the dossier lists that fact once.
func TestPersonQCDedupesRepeatedRows(t *testing.T) {
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	engine := NewEngine(&mockLLM{}, &profileSearcherMock{}, &mockScraper{}, store, 5, 3, 10)
	gate := newPersonGate(models.PersonQuery{Name: "Jane Doe", Company: "Acme Corporation"})
	profile := &models.PersonProfile{
		CurrentCompany: "Acme Corporation",
		Experiences: []models.Experience{
			{Role: "Managing Director", Company: "Acme Corporation"},
			{Role: "Managing Director", Company: "Acme Corporation"},
			{Role: "Managing Director", Company: "Acme Corporation"},
			{Role: "VP Procurement", Company: "Acme Corporation", Period: "2010-2018"},
		},
	}

	excluded, lookups := NewPersonAgent(engine).qcPersonProfile(context.Background(), profile, gate)
	if len(excluded) != 0 || lookups != 0 {
		t.Fatalf("dedupe is not an exclusion, got %+v", excluded)
	}
	if len(profile.Experiences) != 2 {
		t.Fatalf("expected the repeated row collapsed to one, got %+v", profile.Experiences)
	}
}

// TestParsePeriodYears pins the tolerant period reader QC's overlap check depends on:
// free-text model-written periods, open-ended phrases running to the current year, reversed
// ranges, and dates that carry a day.
func TestParsePeriodYears(t *testing.T) {
	now := time.Now().Year()
	cases := []struct {
		period    string
		wantStart int
		wantEnd   int
		wantOk    bool
	}{
		{"2019-present", 2019, now, true},
		{"2019 - Present", 2019, now, true},
		{"Jan 2015 – Dec 2019", 2015, 2019, true},
		{"2010-2018", 2010, 2018, true},
		{"2019-2015", 2015, 2019, true},
		{"Present", now, now, true},
		{"till date", now, now, true},
		{"18 July, 2019", 2019, 2019, true},
		{"Q1 2019 onwards", 2019, now, true},
		{"Unknown", 0, 0, false},
		{"", 0, 0, false},
		{"several years", 0, 0, false},
	}
	for _, testCase := range cases {
		start, end, ok := parsePeriodYears(testCase.period)
		if ok != testCase.wantOk || (ok && (start != testCase.wantStart || end != testCase.wantEnd)) {
			t.Errorf("parsePeriodYears(%q) = (%d, %d, %v), want (%d, %d, %v)",
				testCase.period, start, end, ok, testCase.wantStart, testCase.wantEnd, testCase.wantOk)
		}
	}
}

// TestPersonQCFlagsOverlappingTimelines pins the answer to "two possible posts with
// overlapping timelines": rows at *different* employers whose timelines overlap by more than a
// handover year are both flagged — concurrent roles or a blended row — and kept, because only
// the cited sources can tell those apart. Overlaps at the *same* employer are left alone (a
// board seat alongside an executive one), and a one-year handover is not a flag.
func TestPersonQCFlagsOverlappingTimelines(t *testing.T) {
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	// The past-employer rows survive QC only through their rescue lookups, so the searcher
	// ties the name to each employer — which is exactly the evidence that makes them
	// legitimate career history in the first place.
	searcher := &profileSearcherMock{results: []models.SearchResult{
		{Title: "Jane Doe — career", Snippet: "Jane Doe began her career at OtherCorp Ltd before moving on."},
		{Title: "Jane Doe — early roles", Snippet: "Jane Doe worked at ThirdCorp Industries as an engineer."},
	}}
	engine := NewEngine(&mockLLM{}, searcher, &mockScraper{}, store, 5, 3, 10)
	gate := newPersonGate(models.PersonQuery{Name: "Jane Doe", Company: "Acme Corporation"})
	profile := &models.PersonProfile{
		CurrentCompany: "Acme Corporation",
		Experiences: []models.Experience{
			{Role: "Managing Director", Company: "Acme Corporation", Period: "2019-present", Source: "https://x/1"},
			{Role: "CTO", Company: "OtherCorp Ltd", Period: "2016-2021", Source: "https://x/2"},
			{Role: "Engineer", Company: "ThirdCorp Industries", Period: "2010-2014", Source: "https://x/3"},
			{Role: "Board Member", Company: "Acme Corporation", Period: "2018-2020", Source: "https://x/4"},
		},
	}

	excluded, lookups := NewPersonAgent(engine).qcPersonProfile(context.Background(), profile, gate)
	if len(excluded) != 0 || lookups != 2 {
		t.Fatalf("expected both past-employer rows rescued, got %+v (%d lookups)", excluded, lookups)
	}
	if len(profile.Experiences) != 4 {
		t.Fatalf("expected every row kept, got %+v", profile.Experiences)
	}
	if profile.Experiences[0].Flag == "" || !strings.Contains(profile.Experiences[0].Flag, "OtherCorp Ltd") {
		t.Errorf("the MD row must be flagged against the overlapping CTO row, got %q", profile.Experiences[0].Flag)
	}
	if profile.Experiences[1].Flag == "" || !strings.Contains(profile.Experiences[1].Flag, "Managing Director") || !strings.Contains(profile.Experiences[1].Flag, "Board Member") {
		t.Errorf("the CTO row overlaps two rows and must be flagged against both, got %q", profile.Experiences[1].Flag)
	}
	if profile.Experiences[2].Flag != "" {
		t.Errorf("a non-overlapping row must not be flagged, got %q", profile.Experiences[2].Flag)
	}
	// Two roles at one employer overlap legitimately; but the board seat also overlaps the
	// other-employer CTO row, which is a flag.
	if !strings.Contains(profile.Experiences[3].Flag, "OtherCorp Ltd") {
		t.Errorf("the board seat must be flagged against the other-employer row it overlaps, got %q", profile.Experiences[3].Flag)
	}
}

// TestPersonQCSameEmployerOverlapIsNotFlagged pins the concurrency carve-out: two roles at the
// same employer share years all the time, and that is not worth a flag.
func TestPersonQCSameEmployerOverlapIsNotFlagged(t *testing.T) {
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	engine := NewEngine(&mockLLM{}, &profileSearcherMock{}, &mockScraper{}, store, 5, 3, 10)
	gate := newPersonGate(models.PersonQuery{Name: "Jane Doe", Company: "Acme Corporation"})
	profile := &models.PersonProfile{
		CurrentCompany: "Acme Corporation",
		Experiences: []models.Experience{
			{Role: "Managing Director", Company: "Acme Corporation", Period: "2019-present"},
			{Role: "Executive Director", Company: "Acme Corporation", Period: "2018-2020"},
		},
	}

	if _, _ = NewPersonAgent(engine).qcPersonProfile(context.Background(), profile, gate); profile.Experiences[0].Flag != "" || profile.Experiences[1].Flag != "" {
		t.Fatalf("two roles at one employer overlap legitimately, got %q / %q",
			profile.Experiences[0].Flag, profile.Experiences[1].Flag)
	}
}

// TestPersonQCHandoverYearIsNotFlagged pins the threshold: "2015-2019" then "2019-present"
// share one year — a normal handover, not a blend.
func TestPersonQCHandoverYearIsNotFlagged(t *testing.T) {
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	searcher := &profileSearcherMock{results: []models.SearchResult{
		{Title: "Jane Doe — career", Snippet: "Jane Doe served as CTO at OtherCorp Ltd until 2019."},
	}}
	engine := NewEngine(&mockLLM{}, searcher, &mockScraper{}, store, 5, 3, 10)
	gate := newPersonGate(models.PersonQuery{Name: "Jane Doe", Company: "Acme Corporation"})
	profile := &models.PersonProfile{
		CurrentCompany: "Acme Corporation",
		Experiences: []models.Experience{
			{Role: "Managing Director", Company: "Acme Corporation", Period: "2019-present"},
			{Role: "CTO", Company: "OtherCorp Ltd", Period: "2015-2019"},
		},
	}

	excluded, lookups := NewPersonAgent(engine).qcPersonProfile(context.Background(), profile, gate)
	if len(excluded) != 0 || lookups != 1 {
		t.Fatalf("expected the past-employer row rescued, got %+v (%d lookups)", excluded, lookups)
	}
	if profile.Experiences[0].Flag != "" || profile.Experiences[1].Flag != "" {
		t.Fatalf("a one-year handover is not a blend, got %q / %q",
			profile.Experiences[0].Flag, profile.Experiences[1].Flag)
	}
}

// TestPersonQCSkipsNameAloneRuns pins the fallback: with no company confirmed there is nothing
// to hold rows to, so QC leaves the dossier untouched rather than emptying it.
func TestPersonQCSkipsNameAloneRuns(t *testing.T) {
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	engine := NewEngine(&mockLLM{}, &profileSearcherMock{}, &mockScraper{}, store, 5, 3, 10)
	gate := newPersonGate(models.PersonQuery{Name: "Jane Doe"})
	profile := &models.PersonProfile{
		CurrentCompany: "OtherCorp Ltd",
		Experiences:    []models.Experience{{Role: "CTO", Company: "OtherCorp Ltd"}},
	}

	excluded, lookups := NewPersonAgent(engine).qcPersonProfile(context.Background(), profile, gate)
	if excluded != nil || lookups != 0 {
		t.Fatalf("a name-alone run has no confirmed employer to hold rows to, got %+v (%d lookups)", excluded, lookups)
	}
	if len(profile.Experiences) != 1 || profile.CurrentCompany != "OtherCorp Ltd" {
		t.Fatalf("QC must not touch a name-alone dossier, got %+v", profile)
	}
}
