package agent

import (
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

func TestMarketLanesIncludeRequestedGeography(t *testing.T) {
	lanes := marketLanes(models.MarketLandscapeQuery{
		Activity:  "oil blending",
		Regions:   []string{"Southeast Asia", "Middle East"},
		Countries: []string{"Singapore"},
	})
	for _, expected := range []string{"company operator manufacturer", "Singapore oil blending", "Southeast Asia oil blending", "Middle East oil blending"} {
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
