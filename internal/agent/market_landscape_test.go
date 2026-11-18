package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

func TestMarketLanesIncludeRequestedGeography(t *testing.T) {
	lanes := marketLanes(models.MarketLandscapeQuery{
		Activity:  "oil blending",
		Regions:   []string{"Southeast Asia", "Middle East"},
		Countries: []string{"Singapore"},
	})
	for _, expected := range []string{"manufacturer", "plant", "distributor", "wholesaler", "blending plant", "Singapore manufacturer", "Southeast Asia manufacturer", "Middle East manufacturer"} {
		found := false
		for _, lane := range lanes {
			if lane == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected lane %q in %v", expected, lanes)
		}
	}
	for _, retired := range []string{"company operator manufacturer", "facility operator", "official company"} {
		for _, lane := range lanes {
			if lane == retired {
				t.Errorf("report lanes must use the vocabulary the planner actually searches, got %q in %v", retired, lanes)
			}
		}
	}
}

func TestMarketLanesFollowActivity(t *testing.T) {
	lanes := marketLanes(models.MarketLandscapeQuery{Activity: "drone manufacturing"})
	for _, lane := range lanes {
		if lane == "blending plant" {
			t.Fatalf("oil-blending lane leaked into drone research: %v", lanes)
		}
	}
}

func TestMarketDiscoverySearchesUseNaturalProcessTerms(t *testing.T) {
	// The run's own composition: the operator's wording plus the graph's indexed
	// phrases for the resolved family.
	resolution := resolveActivity(context.Background(), nil, "oil blending", nil)
	searches := marketDiscoverySearchesWithTerms(models.MarketLandscapeQuery{
		Activity: "oil blending",
		Regions:  []string{"Middle East", "Africa"},
	}, resolution.searchTerms())
	queries := make(map[string]bool, len(searches))
	for _, search := range searches {
		queries[search.Query] = true
		if strings.Contains(search.Query, "Middle East Africa") {
			t.Fatalf("query should not concatenate regional scopes: %q", search.Query)
		}
		if strings.Contains(search.Query, "company operator manufacturer") {
			t.Fatalf("query should not use the generic role pile: %q", search.Query)
		}
	}
	for _, expected := range []string{
		`"oil blending" manufacturer Middle East`,
		`"lubricant blending" plant Africa`,
		`"oil blending" distributor Middle East`,
		`"oil blending" wholesaler Africa`,
		`site:ensun.io "oil blending"`,
	} {
		if !queries[expected] {
			t.Errorf("missing query %q in %v", expected, searches)
		}
	}
	for _, retired := range []string{" facility", " company"} {
		for _, search := range searches {
			if strings.Contains(search.Query, retired) && !strings.Contains(search.Query, "site:") {
				t.Errorf("a %q lane qualifies almost any page and must not be composed: %q", strings.TrimSpace(retired), search.Query)
			}
		}
	}
}

func TestMarketActivitySearchTermsPreserveOperatorInput(t *testing.T) {
	got := marketActivitySearchTerms("specialty polymer processing")
	if len(got) != 1 || got[0] != "specialty polymer processing" {
		t.Fatalf("activity terms = %v, want the operator activity unchanged", got)
	}
}

// TestMarketBlendingCategoryResolvesTheBlendedFamily pins the operator's report:
// "Oil Blending" must broaden to lubricants, not oleochemicals. The qualifier
// decides the family, because "oil" alone spans unrelated chemistries.
func TestMarketBlendingCategoryResolvesTheBlendedFamily(t *testing.T) {
	cases := []struct{ activity, want string }{
		{"Oil Blending", "lubricants"},
		{"oil blending", "lubricants"},
		{"lubricant oil blending", "lubricants"},
		{"base oil blending", "lubricants"},
		{"fuel oil blending", "fuel oils"},
		{"bunker fuel blending", "fuel oils"},
		{"crude oil blending", "crude oil"},
		{"used cooking oil", "oleochemicals"},
		{"palm oil", "oleochemicals"},
		{"palm kernel oil", "oleochemicals"},
		{"crude oil", "crude oil"},
		{"fuel oil", "fuel oils"},
		{"process oil", "lubricants"},
	}
	for _, testCase := range cases {
		if got := demandFallbackCategory(testCase.activity, nil); got != testCase.want {
			t.Errorf("demandFallbackCategory(%q) = %q, want %q", testCase.activity, got, testCase.want)
		}
		lanes := marketCategoryLanes(demandFallbackCategory(testCase.activity, nil))
		if len(lanes) == 0 {
			t.Errorf("%q produced no category lanes", testCase.activity)
		}
	}
	// A fuel cell is not a fuel-oil activity: the shared word must not decide.
	if got := demandFallbackCategory("fuel cell manufacturing", nil); got == "fuel oils" {
		t.Errorf("demandFallbackCategory(%q) = %q, must not resolve a fuel-oil family", "fuel cell manufacturing", got)
	}
}

// TestMarketActivitySearchTermsStayInsideTheOilFamily pins the split: the helper
// only echoes the operator's wording, and the family phrases a run searches come
// from the graph — so a generic oil blend gains the indexed lubricant wording while
// a fuel, crude or feedstock blend never does.
func TestMarketActivitySearchTermsStayInsideTheOilFamily(t *testing.T) {
	if terms := marketActivitySearchTerms("Oil Blending"); len(terms) != 1 || terms[0] != "Oil Blending" {
		t.Errorf("marketActivitySearchTerms = %v, want the operator's wording alone", terms)
	}
	terms := func(activity string) []string {
		resolution := resolveActivity(context.Background(), nil, activity, nil)
		out := make([]string, 0, 8)
		for _, search := range marketDiscoverySearchesWithTerms(models.MarketLandscapeQuery{Activity: activity}, resolution.searchTerms()) {
			out = append(out, strings.ToLower(search.Query))
		}
		return out
	}
	contains := func(list []string, want string) bool {
		for _, entry := range list {
			if strings.Contains(entry, want) {
				return true
			}
		}
		return false
	}
	if plan := terms("Oil Blending"); !contains(plan, "lubricant blending") {
		t.Errorf("oil blending plan = %v, want the graph's indexed lubricant phrase", plan)
	}
	for _, activity := range []string{"fuel oil blending", "crude oil blending", "palm oil blending"} {
		plan := terms(activity)
		if contains(plan, "lubricant blending") {
			t.Errorf("%s plan = %v, must not be expanded to lubricant blending", activity, plan)
		}
	}
	if plan := terms("palm oil blending"); !contains(plan, "fatty acids") && !contains(plan, "oleochemical") {
		resolution := resolveActivity(context.Background(), nil, "palm oil blending", nil)
		if resolution.primaryCategory() != "Oleochemical feedstocks" {
			t.Errorf("palm oil category = %q, want the feedstock family; plan = %v", resolution.primaryCategory(), plan)
		}
	}
}

// TestMarketVerificationPurposesUseIndexedRoles pins the role vocabulary the
// operator reviewed: a purpose is appended after the quoted company and activity,
// so it must be a noun real pages carry. "Facility operator" and
// "registry trade source" spent one search per candidate and matched only
// directories; the replacement names the three seller roles the report groups.
func TestMarketVerificationPurposesUseIndexedRoles(t *testing.T) {
	purposes := marketVerificationPurposes()
	joined := strings.ToLower(strings.Join(purposes, " | "))
	for _, banned := range []string{"facility operator", "company operator", "official company", "registry trade source"} {
		if strings.Contains(joined, banned) {
			t.Errorf("verification purposes = %v, must not carry the unindexed phrase %q", purposes, banned)
		}
	}
	for _, want := range []string{"official website", "plant", "distributor"} {
		found := false
		for _, purpose := range purposes {
			if purpose == want {
				found = true
			}
		}
		if !found {
			t.Errorf("purposes = %v, want %q", purposes, want)
		}
	}
	for _, purpose := range purposes {
		if words := len(strings.Fields(purpose)); words == 0 || words > marketQueryReviewMaxPurposeWords {
			t.Errorf("purpose %q is outside the bounded phrase length", purpose)
		}
	}
}

func TestMarketCandidateUsesCanonicalDomainAndEvidence(t *testing.T) {
	candidate := marketCandidate(models.SearchResult{
		Title: "Example Blending | Example",
		Link:  "https://www.example.com/services/blending",
	}, "operator manufacturer", models.MarketLandscapeQuery{Activity: "oil blending"})
	if candidate == nil {
		t.Fatal("expected candidate")
	}
	if candidate.EntityKey != "company:example.com" || candidate.Domain != "example.com" {
		t.Fatalf("unexpected identity: %#v", candidate)
	}
	if len(candidate.Evidence) != 1 || candidate.Evidence[0].ClaimType != "activity" {
		t.Fatalf("expected tagged activity evidence: %#v", candidate.Evidence)
	}
}

func TestMarketStatusRequiresCorroborationOrOfficialEvidence(t *testing.T) {
	candidate := &models.MarketLandscapeCompany{
		EvidenceType: "discovery-lead",
		Evidence:     []models.MarketLandscapeEvidence{{SourceTier: "directory"}},
	}
	if got := marketStatus(candidate); got != "lead" {
		t.Fatalf("got %q, want lead", got)
	}
	candidate.EvidenceType = "company-page"
	candidate.Evidence = append(candidate.Evidence, models.MarketLandscapeEvidence{SourceTier: "directory"})
	if got := marketStatus(candidate); got != "verified" {
		t.Fatalf("got %q, want verified", got)
	}
}

func TestExcludedMarketDomain(t *testing.T) {
	if !excludedMarketDomain("example-logistics.com", []string{"logistics"}) {
		t.Fatal("expected logistics exclusion")
	}
	if excludedMarketDomain("example-energy.com", []string{"logistics"}) {
		t.Fatal("did not expect energy exclusion")
	}
}

func TestMarketSourceAndTitleClassification(t *testing.T) {
	if got := marketSourceTier("www.reuters.com"); got != "news" {
		t.Fatalf("got %q, want news", got)
	}
	if got := marketSourceTier("www.facebook.com"); got != "social" {
		t.Fatalf("got %q, want social", got)
	}
	if got := marketSourceTier("www.grandviewresearch.com"); got != "research" {
		t.Fatalf("got %q, want research", got)
	}
	if !isResearchDocumentTitle("India Soap Manufacturers Market Research Report") {
		t.Fatal("expected a research report title to be classified as non-company evidence")
	}
	candidate := marketCandidate(models.SearchResult{
		Title:   "India Soap Manufacturers Market Research Report",
		Snippet: "Market size and forecast for soap manufacturers.",
		Link:    "https://www.grandviewresearch.com/industry-analysis/soap",
	}, "manufacturer", models.MarketLandscapeQuery{Activity: "soap"})
	if candidate == nil || candidate.EvidenceType != "discovery-lead" {
		t.Fatalf("research report must remain a lead, got %+v", candidate)
	}
	if !isGenericMarketTitle("Top 10 Oil Companies in the Middle East") {
		t.Fatal("expected list headline to be a discovery lead")
	}
	if hasCompanyIdentitySignal("Toll Blending Services", "apac.gulfoilltd.com") {
		t.Fatal("generic service title should not identify the company")
	}
	if hasMarketActivityEvidence(models.SearchResult{Title: "Crude Oil Trading"}, "oil blending") {
		t.Fatal("trading-only evidence should not satisfy oil blending")
	}
	if !hasMarketActivityEvidence(models.SearchResult{Title: "Industrial Oil Blending Plant"}, "oil blending") {
		t.Fatal("blending evidence should satisfy the activity check")
	}
}
