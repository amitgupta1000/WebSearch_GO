package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestDemandLaneSearchQuerySkipsGeographyItCarries pins the region-collision
// fix: a region-scoped lane never gets its own region appended again, but a
// different geography still travels.
func TestDemandLaneSearchQuerySkipsGeographyItCarries(t *testing.T) {
	lane := demandLane{term: "Middle East importer tender procurement", productAnchored: true}
	if got := demandLaneSearchQuery("used cooking oil", lane, "Middle East"); got != "used cooking oil Middle East importer tender procurement" {
		t.Errorf("own-region copy must not repeat the region, got %q", got)
	}
	if got := demandLaneSearchQuery("used cooking oil", lane, "Africa"); got != "used cooking oil Middle East importer tender procurement Africa" {
		t.Errorf("a different geography must still be appended, got %q", got)
	}
	stage := demandLane{term: "biodiesel plant"}
	if got := demandLaneSearchQuery("used cooking oil", stage, "Middle East"); got != "biodiesel plant Middle East" {
		t.Errorf("stage lanes carry the geography, got %q", got)
	}
}

// TestDemandSearchGeographiesAndRegionTail pins the geography plumbing: regions
// travel separately for discovery lanes, the company- and stage-anchored tail
// alternates multi-word regions with OR, and the report header shows a plain
// comma join.
func TestDemandSearchGeographiesAndRegionTail(t *testing.T) {
	query := models.DemandChainQuery{Regions: []string{"Middle East", "Africa"}}
	if got := demandSearchGeographies(query); strings.Join(got, "|") != "Middle East|Africa" {
		t.Errorf("discovery geographies = %v, want the regions split", got)
	}
	if got := demandGeography(query); got != `"Middle East" OR Africa` {
		t.Errorf("region tail = %q, want the OR alternation", got)
	}
	if got := demandGeographyDisplay(query); got != "Middle East, Africa" {
		t.Errorf("display geography = %q, want the comma join", got)
	}
	single := models.DemandChainQuery{Regions: []string{"Africa"}}
	if got := demandGeography(single); got != "Africa" {
		t.Errorf("single-region tail = %q, want the bare region", got)
	}
	countries := models.DemandChainQuery{Countries: []string{"India", "Nepal"}}
	if got := demandGeography(countries); got != "India Nepal" {
		t.Errorf("country tail = %q, want the countries joined", got)
	}
	if got := demandSearchGeographies(countries); len(got) != 1 || got[0] != "India Nepal" {
		t.Errorf("country geographies = %v, want one joined suffix", got)
	}
}

// TestDemandPurposeRewritesIgnoreDrops pins the buyer purpose contract: a
// rewrite replaces the phrase everywhere, a drop is recorded but never removes
// the claim-conditioned kind.
func TestDemandPurposeRewritesIgnoreDrops(t *testing.T) {
	rows := []models.MarketQueryReview{
		{Phase: "verification", Original: "import data OR importer of OR customs shipment", Verdict: "rewritten", RevisedQuery: "import records customs data"},
		{Phase: "verification", Original: "official site", Verdict: "dropped", Reason: "the reviewer retired the phrase"},
	}
	rewrites := demandPurposeRewrites(rows)
	if len(rewrites) != 1 || rewrites["import data OR importer of OR customs shipment"] != "import records customs data" {
		t.Fatalf("rewrites = %v, want only the importer phrase", rewrites)
	}
	purposes := demandVerificationPurposes("")
	applied := applyDemandPurposeRewrites(purposes, rewrites)
	for i, purpose := range applied {
		if purpose.phrase == "import data OR importer of OR customs shipment" {
			t.Errorf("the importer phrase must run rewritten, got %q", purpose.phrase)
		}
		if purpose.kind != purposes[i].kind {
			t.Errorf("the kind ordering must be untouched: %q vs %q", purpose.kind, purposes[i].kind)
		}
	}
	if len(applied) != len(purposes) {
		t.Errorf("a dropped purpose must stay in the template, got %d of %d", len(applied), len(purposes))
	}
}

// TestDemandChainResearchReviewsLaneRuns pins the buyer integration end to
// end: the mandatory demand lanes run once per requested region, the review's
// drop removes exactly one region's copy, the purpose rewrite replaces the
// claim-conditioned phrase in the verification searches, the region soup never
// appears in any executed query, and the verdicts land on the report.
func TestDemandChainResearchReviewsLaneRuns(t *testing.T) {
	const procurementME = `used cooking oil buyer procurement purchase Middle East`
	llm := &demandLLM{
		stages: []models.ValueChainStage{{
			Name: "biodiesel production", Operators: []string{"biodiesel plant"},
		}},
		candidates: []models.MarketLandscapeLeadCandidate{{
			Name: "GreenDiesel Sdn Bhd", Country: "United Arab Emirates",
			EvidenceURL: "https://greendiesel.ae/plant",
		}},
		queryReview: &marketQueryReviewTarget{
			Queries: []marketQueryVerdict{
				{Query: procurementME, Verdict: "drop", Reason: "procurement portals never name the buyer"},
			},
			Purposes: []marketQueryVerdict{
				{Query: "import data OR importer of OR customs shipment", Verdict: "rewrite", RevisedQuery: "import records customs data"},
			},
		},
	}
	searcher := &demandSearcher{hits: map[string][]models.SearchResult{
		"biodiesel plant": {{
			Title:   "Used cooking oil biodiesel plant in the UAE",
			Link:    "https://greendiesel.ae/plant",
			Snippet: "Produces biodiesel from used cooking oil feedstock.",
		}},
		"import records customs data": {{
			Title:   "GreenDiesel Sdn Bhd import record",
			Link:    "https://www.greendiesel.ae/imports",
			Snippet: "Customs shipment: GreenDiesel Sdn Bhd imported used cooking oil.",
		}},
	}}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, searcher))
	report, err := agent.Research(context.Background(), "research-review", models.DemandChainQuery{
		Activity:            "used cooking oil",
		Regions:             []string{"Middle East", "Africa"},
		DiscoveryQueries:    8,
		VerificationQueries: 4,
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if report.Geography != "Middle East, Africa" {
		t.Errorf("report geography = %q, want the comma join", report.Geography)
	}
	discoveryDropped := false
	purposeRewritten := false
	for _, review := range report.QueryReviews {
		if review.Phase == "discovery" && review.Original == procurementME && review.Verdict == "dropped" {
			discoveryDropped = true
		}
		if review.Phase == "verification" && review.Verdict == "rewritten" && review.RevisedQuery == "import records customs data" {
			purposeRewritten = true
		}
	}
	if !discoveryDropped {
		t.Errorf("the dropped procurement run must be recorded on the report, got %v", report.QueryReviews)
	}
	if !purposeRewritten {
		t.Errorf("the rewritten verification purpose must be recorded on the report, got %v", report.QueryReviews)
	}
	for _, executed := range searcher.queries {
		if strings.Contains(executed, "Middle East Africa") {
			t.Errorf("no query may carry the concatenated region soup, got %q", executed)
		}
		if executed == procurementME {
			t.Errorf("the reviewed-away procurement run must not execute, got %q", executed)
		}
	}
	for _, want := range []string{
		`biodiesel plant Middle East`,
		`biodiesel plant Africa`,
		`used cooking oil buyer procurement purchase Africa`,
		`used cooking oil tender RFP e-procurement Middle East`,
		`used cooking oil tender RFP e-procurement Africa`,
		`used cooking oil distributor wholesaler of Middle East`,
		`used cooking oil distributor wholesaler of Africa`,
	} {
		found := false
		for _, executed := range searcher.queries {
			if executed == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected discovery query %q in %v", want, searcher.queries)
		}
	}
	verificationRewritten := false
	for _, executed := range searcher.queries {
		if strings.Contains(executed, `"GreenDiesel Sdn Bhd"`) && strings.Contains(executed, "import records customs data") {
			verificationRewritten = true
		}
	}
	if !verificationRewritten {
		t.Errorf("verification must search the rewritten importer phrase, queries: %v", searcher.queries)
	}
}
