package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestFillEntityGapsSpendsLookupsOnTheAreasNothingCovers is the proactive half of the
// name+company lookup policy: a thin roster is filled from a company-anchored query for an
// area the collected evidence never mentions, and a same-named firm cannot ride in on it.
func TestFillEntityGapsSpendsLookupsOnTheAreasNothingCovers(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "Sanjay Chemicals", Domain: "sanjaychemindia.com", EntityConfirmed: true}
	searcher := &datedSearcher{results: []models.SearchResult{
		{
			Title:   "Ravi Shah - Head of Procurement - Sanjay Chemicals (India) Pvt Ltd",
			Link:    "https://www.linkedin.com/in/ravi-shah-procurement",
			Snippet: "Ravi Shah, Head of Procurement at Sanjay Chemicals",
		},
		{
			// A same-first-word firm. "Sanjay Chemicals and Allied" would match on the
			// name key (both tokens present, which is the documented weakness of the
			// name-only match); "Sanjay Chlorides" fails on a token, so it is genuinely
			// off-entity and must not be admitted by an anchored query.
			Title:   "Procurement head - Sanjay Chlorides Pvt Ltd",
			Link:    "https://www.sanjaychlorides-other.com/team",
			Snippet: "a different firm entirely",
		},
	}}
	agent := NewCompanyAgent(newValidationEngine(searcher, 4, true))
	state := &models.CompanyResearchContext{
		Brief: brief,
		Evidence: []models.Evidence{{
			URL: "http://sanjaychemindia.com/about.php", Title: "Management", Content: "The managing director leads the company.",
		}},
	}
	gate := &models.EntityGate{}
	matcher := agent.entityMatcherFor(brief)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	agent.fillEntityGaps(ctx, "sess_test", brief, state, nil, matcher, gate)

	if gate.GapLookups == 0 {
		t.Fatalf("expected lookups for the uncovered areas, gate=%+v", gate)
	}
	// Only the company-named result is kept; the same-named firm is not, and URL dedup
	// means one lookup adding it is all that counts.
	if gate.GapEvidence != 1 {
		t.Fatalf("expected exactly one added source, got %d (%+v)", gate.GapEvidence, state.Evidence)
	}
	added := state.Evidence[len(state.Evidence)-1]
	if added.SourceType != "entity-lookup" || !strings.Contains(added.Content, "Head of Procurement") {
		t.Fatalf("unexpected gap evidence: %+v", added)
	}
	// The added snippet names the company, which is what makes running these lookups
	// before the gate can inspect rows safe.
	if match := matcher.match(added.URL, added.Title, added.Content); !match.Matched {
		t.Fatalf("expected the gap evidence to name the company, got %+v", match)
	}
	// Leadership is already covered by the collected evidence, so no budget is spent there.
	for _, query := range searcher.queries {
		if strings.Contains(strings.ToLower(query), "leadership") {
			t.Errorf("expected no lookup for a covered area, got %q", query)
		}
	}
}

// TestFillEntityGapsLeavesTheValidationReserveAlone keeps the two lookup passes from
// cannibalising each other: filling gaps only runs while twice the reserve remains, so it
// can never be the reason the pass that rescues and dates rows does not run.
func TestFillEntityGapsLeavesTheValidationReserveAlone(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "Sanjay Chemicals", Domain: "sanjaychemindia.com"}
	searcher := &datedSearcher{results: []models.SearchResult{
		{Title: "Sanjay Chemicals team", Link: "https://sanjaychemindia.com/team", Snippet: "Sanjay Chemicals"},
	}}
	agent := NewCompanyAgent(newValidationEngine(searcher, 4, true))
	state := &models.CompanyResearchContext{Brief: brief}
	gate := &models.EntityGate{}

	tight, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agent.fillEntityGaps(tight, "sess_test", brief, state, nil, agent.entityMatcherFor(brief), gate)
	if gate.GapLookups != 0 || len(searcher.queries) != 0 {
		t.Fatalf("expected no gap lookups inside the reserve, got %d lookups (%v)", gate.GapLookups, searcher.queries)
	}

	roomy, cancelRoomy := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelRoomy()
	agent.fillEntityGaps(roomy, "sess_test", brief, state, nil, agent.entityMatcherFor(brief), gate)
	if gate.GapLookups == 0 {
		t.Fatal("expected gap lookups when the budget allows")
	}
}

// TestLookupPassReportsPeopleItLeftUnchecked covers the reporting half of "run the lookups
// aggressively": "0 lookups" and "1 lookup, 1 candidate left unchecked" describe very
// different coverage, and the artifact must not leave the reader guessing.
func TestLookupPassReportsPeopleItLeftUnchecked(t *testing.T) {
	chart, state, brief := freshnessFixture()
	gate := &models.EntityGate{Confirmed: true, Keys: []string{"domain:kljresources.in"}}
	agent := NewCompanyAgent(newValidationEngine(&datedSearcher{}, 1, true))

	report := agent.validateChart(context.Background(), "sess_test", brief, chart, state, nil, gate)

	if report.LookupsUsed != 1 {
		t.Fatalf("expected the cap to stop the pass after one lookup, got %d", report.LookupsUsed)
	}
	if gate.LookupsDeferred != 1 {
		t.Fatalf("expected the second candidate to be reported as unchecked, got %d", gate.LookupsDeferred)
	}
	if !strings.Contains(report.Skipped, "left unchecked") {
		t.Fatalf("expected the skip reason to say how many were left unchecked, got %q", report.Skipped)
	}
}

// TestEvidenceCoversIsDeliberatelyGenerous documents the coverage test's bias: an area the
// evidence merely touches must not cost a search, because the point is to fill gaps, not to
// top up what is already there.
func TestEvidenceCoversIsDeliberatelyGenerous(t *testing.T) {
	evidence := []models.Evidence{{
		URL: "https://example.com/team", Title: "Our leadership", Content: "The procurement desk reports to the CFO.",
	}}
	cases := []struct {
		name     string
		keywords []string
		covered  bool
	}{
		{"title match", []string{"leadership"}, true},
		{"content match", []string{"cfo"}, true},
		{"word inside content", []string{"procurement"}, true},
		{"unrelated area", []string{"supply chain"}, false},
	}
	for _, tc := range cases {
		if got := evidenceCovers(evidence, tc.keywords); got != tc.covered {
			t.Errorf("%s: expected covered=%v, got %v", tc.name, tc.covered, got)
		}
	}
	if evidenceCovers(nil, []string{"leadership"}) {
		t.Error("no evidence covers nothing")
	}
}
