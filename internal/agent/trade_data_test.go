package agent

import (
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestCompanyTradeDataQueriesAreAnchored pins the company-search requirement:
// every run carries explicit import/export/customs/shipment slots and every
// slot names the company, so the entity gate cannot discard them as generic
// industry searches.
func TestCompanyTradeDataQueriesAreAnchored(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "KLJ Resources Limited"}
	queries := companyTradeDataQueries(brief)
	if len(queries) < 2 {
		t.Fatalf("trade queries = %d, want at least 2", len(queries))
	}
	matcher := newEntityMatcher(brief)
	joined := ""
	for _, query := range queries {
		joined += strings.ToLower(query.Query) + "\n"
		if !matcher.covers(query.Query) {
			t.Errorf("trade query %q is not anchored on the company", query.Query)
		}
		if query.TargetArea != "trade data" {
			t.Errorf("trade query %q target area = %q, want trade data", query.Query, query.TargetArea)
		}
	}
	for _, expected := range []string{"import", "export", "customs", "shipment", "site:volza.com", "site:trademo.com"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("trade query pack is missing %q:\n%s", expected, joined)
		}
	}
}

// TestFallbackQueryPlanIncludesTradeData proves the deterministic fallback, not
// just the planner prompt, carries trade-data searches.
func TestFallbackQueryPlanIncludesTradeData(t *testing.T) {
	plan := fallbackQueryPlan(models.CompanyBrief{CompanyName: "KLJ Resources Limited"})
	joined := ""
	for _, query := range plan.Queries {
		joined += strings.ToLower(query.Query) + "\n"
	}
	if !strings.Contains(joined, "customs") || !strings.Contains(joined, "site:volza.com") {
		t.Fatalf("fallback plan has no trade-data queries:\n%s", joined)
	}
}

// TestMarketLanesIncludeTradeData covers the seller-side discovery requirement.
func TestMarketLanesIncludeTradeData(t *testing.T) {
	lanes := marketLanes(models.MarketLandscapeQuery{Activity: "used cooking oil", Countries: []string{"India"}})
	seen := make(map[string]bool, len(lanes))
	for _, lane := range lanes {
		seen[lane] = true
	}
	for _, expected := range marketTradeDataLanes() {
		if !seen[expected] {
			t.Errorf("seller lanes are missing trade-data lane %q: %v", expected, lanes)
		}
	}
}

// TestDemandLanesIncludeTradeData covers the buyer-side discovery requirement.
func TestDemandLanesIncludeTradeData(t *testing.T) {
	lanes := demandChainLanes(models.DemandChainQuery{Activity: "used cooking oil"}, nil)
	terms := make(map[string]bool, len(lanes))
	for _, lane := range lanes {
		terms[lane.term] = true
	}
	expectedTerms := []string{"importer import data"}
	for _, lane := range demandTradeDataLanes() {
		expectedTerms = append(expectedTerms, lane.term)
	}
	for _, expected := range expectedTerms {
		if !terms[expected] {
			t.Errorf("buyer lanes are missing trade-data lane %q", expected)
		}
	}
}

// TestTradeDataDomainsAreNonCompanySources keeps a shipment listing from
// presenting as an operating company while retaining the distinct trade tier.
func TestTradeDataDomainsAreNonCompanySources(t *testing.T) {
	for _, domain := range []string{
		"www.volza.com", "www.trademo.com", "www.eximpedia.app",
		"www.seair.co.in", "www.zauba.com", "www.importgenius.com",
	} {
		if tier := marketSourceTier(domain); tier != "trade" {
			t.Errorf("marketSourceTier(%q) = %q, want trade", domain, tier)
		}
		if override := demandSourceOverride(domain); override != "trade" {
			t.Errorf("demandSourceOverride(%q) = %q, want trade", domain, override)
		}
		hit := models.SearchResult{Title: "Company import data", Link: "https://" + strings.TrimPrefix(domain, "www.") + "/company/acme"}
		if kind := classifyDiscoverySource(hit); kind != sourceOther {
			t.Errorf("classifyDiscoverySource(%q) = %q, want sourceOther", domain, kind)
		}
	}
}

// TestDemandVerificationAcceptsOnlyTradeImportRecords is the evidence policy:
// a trade page can supply an import claim, but not a product description or a
// generic profile.
func TestDemandVerificationAcceptsOnlyTradeImportRecords(t *testing.T) {
	importHit := models.SearchResult{
		Title:   "Acme Chemicals import data and shipment records",
		Link:    "https://www.volza.com/p/acme-chemicals/import-data/",
		Snippet: "Acme Chemicals importers, customs shipment records and buyers.",
	}
	evidence, ok := demandVerificationEvidence(importHit, "Acme Chemicals", "plasticizers", "import record")
	if !ok {
		t.Fatal("trade import record was rejected")
	}
	if evidence.SourceTier != "trade" || evidence.ClaimType != models.DemandClaimImport {
		t.Fatalf("trade evidence = %+v, want trade/import", evidence)
	}

	usageHit := models.SearchResult{
		Title:   "Acme Chemicals company profile",
		Link:    "https://www.volza.com/p/acme-chemicals/",
		Snippet: "Acme Chemicals is listed as a company in this directory.",
	}
	if _, ok := demandVerificationEvidence(usageHit, "Acme Chemicals", "plasticizers", "import record"); ok {
		t.Error("a non-import trade page must not become import evidence")
	}
}

// TestDemandStatusTradeRecordNeedsAnOwnedPage pins the corroboration rule: a
// trade import record is not verification by itself, but it upgrades one
// company-owned page with a transaction claim.
func TestDemandStatusTradeRecordNeedsAnOwnedPage(t *testing.T) {
	tradeOnly := &models.MarketLandscapeCompany{Name: "Acme Chemicals", Evidence: []models.MarketLandscapeEvidence{{
		URL: "https://www.volza.com/p/acme-chemicals/import-data", SourceTier: "trade", ClaimType: models.DemandClaimImport,
	}}}
	if status := demandStatus(tradeOnly); status != "lead" {
		t.Errorf("trade-only status = %q, want lead", status)
	}
	withOwnPage := &models.MarketLandscapeCompany{Name: "Acme Chemicals", Evidence: []models.MarketLandscapeEvidence{
		{URL: "https://www.volza.com/p/acme-chemicals/import-data", SourceTier: "trade", ClaimType: models.DemandClaimImport},
		{URL: "https://acmechemicals.com/input", SourceTier: "official", ClaimType: models.DemandClaimUsage},
	}}
	if status := demandStatus(withOwnPage); status != "verified" {
		t.Errorf("owned page plus trade import record status = %q, want verified", status)
	}
}

// TestDeepSearchComposerRequestsTradeData covers the open-ended product-research
// path, which has no fixed lane set.
func TestDeepSearchComposerRequestsTradeData(t *testing.T) {
	prompt := deepComposeSystemPrompt(models.FocusDepth)
	if !strings.Contains(strings.ToLower(prompt), "trade-data") {
		t.Fatalf("deep-search composer prompt does not request trade data:\n%s", prompt)
	}
}
