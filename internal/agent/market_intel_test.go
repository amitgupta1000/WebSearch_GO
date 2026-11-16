package agent

import (
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// The operator-promoted market-intelligence directories (ensun, revenuebase)
// carry a specific contract in both landscapes: classified as directory-tier
// sources (dense ranked rosters that are read ahead of company homepages,
// never an operating company themselves), surfaced by deterministic
// site-scoped lanes, and judged by the QC policies on the company's own
// evidence rather than its source.

// TestMarketIntelDomainsAreDirectorySources pins the classification: both
// domains are directory-tier on the seller side (read-first, roster sources),
// never the operating company; the buyer side reaches the same verdict
// through its own tier walk.
func TestMarketIntelDomainsAreDirectorySources(t *testing.T) {
	for _, domain := range []string{"www.ensun.io", "ensun.io", "revenuebase.ai", "www.revenuebase.ai"} {
		if tier := marketSourceTier(domain); tier != "directory" {
			t.Errorf("marketSourceTier(%q) = %q, want directory", domain, tier)
		}
		if tier := marketDiscoveryTier(domain); tier != "directory" {
			t.Errorf("marketDiscoveryTier(%q) = %q, want directory", domain, tier)
		}
		if !marketRosterSourceTier("directory") {
			t.Error("directory tier must remain a roster source")
		}
	}
	// The buyer tier walk routes through the shared override path and must
	// land on the same directory verdict.
	hit := models.SearchResult{
		Title: "Acme Chemicals - Manufacturer Profile | ensun",
		Link:  "https://ensun.io/company/acme-chemicals",
	}
	if tier := demandSourceTierForHit(hit); tier != "directory" {
		t.Errorf("demandSourceTierForHit(ensun) = %q, want directory", tier)
	}
	if kind := classifyDiscoverySource(hit); kind != sourceDirectory {
		t.Errorf("classifyDiscoverySource(ensun) = %q, want sourceDirectory — the directory must never present as the operating company", kind)
	}
	// Read priority: a roster-shaped directory page is read before company
	// homepages, which is the whole point of promoting these sources.
	if got := marketLeadReadPriority("directory", "Top acrylate manufacturers | ensun"); got != 1 {
		t.Errorf("roster-shaped directory read priority = %d, want 1 (read first)", got)
	}
	if !marketLeadReadable("directory", "Acme Chemicals profile | ensun") {
		t.Error("ensun profile pages must be readable")
	}
}

// TestMarketIntelLanesInBothLandscapes pins the deterministic site-scoped
// lanes: the seller landscape queries each directory for manufacturers of the
// product; the buyer landscape queries each directory with the product
// (product-anchored), letting verification sort buyers from sellers.
func TestMarketIntelLanesInBothLandscapes(t *testing.T) {
	seller := marketLanes(models.MarketLandscapeQuery{Activity: "used cooking oil", Countries: []string{"India"}})
	for _, want := range []string{
		"site:ensun.io used cooking oil manufacturers",
		"site:revenuebase.ai used cooking oil manufacturers",
	} {
		found := false
		for _, lane := range seller {
			if lane == want {
				found = true
			}
		}
		if !found {
			t.Errorf("seller lanes = %v, missing %q", seller, want)
		}
	}

	lanes := demandChainLanes(models.DemandChainQuery{Activity: "n-butanol"}, nil)
	seen := make(map[string]demandLane, len(lanes))
	laneTerms := make([]string, 0, len(lanes))
	for _, lane := range lanes {
		seen[lane.term] = lane
		laneTerms = append(laneTerms, lane.term)
	}
	for _, want := range []string{"site:ensun.io", "site:revenuebase.ai"} {
		lane, ok := seen[want]
		if !ok {
			t.Fatalf("buyer lanes missing %q: %v", want, laneTerms)
		}
		if !lane.productAnchored {
			t.Errorf("lane %q must be product-anchored so the query carries the product", want)
		}
		if query := demandLaneSearchQuery("n-butanol", lane, "India"); !strings.Contains(query, "n-butanol site:"+strings.TrimPrefix(want, "site:")) {
			t.Errorf("lane query = %q, want the product scoped to the directory", query)
		}
	}
}

// TestDefaultQCPromotesMarketIntelSources pins the QC-policy promotion: both
// default policies judge a directory-sourced company on its own evidence
// rather than its source, and never credit the directory as the company — the
// live run had QC dropping revenuebase-sourced rows for the source's sake.
func TestDefaultQCPromotesMarketIntelSources(t *testing.T) {
	for _, prompt := range []string{DefaultMarketQCPrompt, DefaultDemandQCPrompt} {
		lowered := strings.ToLower(prompt)
		for _, want := range []string{"ensun", "revenuebase", "judged on", "never credit the directory itself as the company"} {
			if !strings.Contains(lowered, strings.ToLower(want)) {
				t.Errorf("QC policy is missing %q:\n%s", want, prompt)
			}
		}
	}
	// The policy is still the default after the edit (the whitespace-
	// normalized comparison the console relies on keeps working).
	policy, source := demandQCPolicy(models.DemandChainQuery{QCPrompt: DefaultDemandQCPrompt})
	if source != "default" || policy != DefaultDemandQCPrompt {
		t.Fatalf("default buyer QC policy = %q/%q", policy, source)
	}
}
