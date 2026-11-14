package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestPersonResearchBuildsTheFlaggedSocialSection pins the contract of the separate social
// section: attributable public accounts are collected with the basis and confidence that
// admitted them, name-only and different-employer hits are left out, the fixed warning travels
// with the section, and no social URL is fetched as employment evidence.
func TestPersonResearchBuildsTheFlaggedSocialSection(t *testing.T) {
	profileURL := "https://www.linkedin.com/in/jane-doe"
	searcher := &profileSearcherMock{results: []models.SearchResult{
		{Title: "Jane Doe - VP Procurement - Acme Corporation | LinkedIn", Link: profileURL, Snippet: "Jane Doe — VP Procurement at Acme Corporation, Mumbai."},
		{Title: "Jane Doe (@janedoe) / X", Link: "https://x.com/janedoe", Snippet: "Jane Doe, VP Procurement at Acme Corporation. 2025-03-12 post on supply chains."},
		{Title: "Jane Doe | Facebook", Link: "https://www.facebook.com/janedoe", Snippet: "Jane Doe · Mumbai · Procurement lead."},
		{Title: "Jane Doe on LinkedIn: procurement outlook", Link: "https://www.linkedin.com/posts/janedoe_procurement-activity-123", Snippet: "Jane Doe shares a 2025-02-01 note on supply chains at Acme Corporation."},
		{Title: "Jane Doe | Instagram", Link: "https://www.instagram.com/janedoe", Snippet: "Jane Doe shares design projects."},
		{Title: "Jane Doe - CTO - OtherCorp Ltd | X", Link: "https://x.com/otherjane", Snippet: "Jane Doe CTO at OtherCorp Ltd, Mumbai."},
	}}
	scraper := &sessionAwareScraperMock{recovered: recoveredProfile(profileURL)}
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	engine := NewEngine(&mockLLM{}, searcher, scraper, store, 5, 3, 10)

	profile, err := NewPersonAgent(engine).ResearchPerson(context.Background(), "session",
		models.PersonQuery{
			Name: "Jane Doe", Company: "Acme Corporation", Location: "Mumbai", Confirmed: true,
			SeedURL: profileURL, SeedTitle: "Jane Doe - VP Procurement - Acme Corporation | LinkedIn",
			SeedSnippet: "Jane Doe — VP Procurement at Acme Corporation, Mumbai.",
		}, nil)
	if err != nil {
		t.Fatalf("ResearchPerson: %v", err)
	}

	if len(profile.Socials) != 3 {
		t.Fatalf("expected three attributable social signals, got %+v", profile.Socials)
	}
	wantPlatforms := []string{"X/Twitter", "Facebook", "LinkedIn posts"}
	for index, signal := range profile.Socials {
		if signal.Platform != wantPlatforms[index] {
			t.Errorf("signal %d platform = %q, want %q", index, signal.Platform, wantPlatforms[index])
		}
		if signal.URL == "" || signal.IdentityBasis == "" || signal.Confidence <= 0 {
			t.Errorf("signal %d lacks its evidence: %+v", index, signal)
		}
		if signal.Source == "" {
			t.Errorf("signal %d does not record the plan section that surfaced it", index)
		}
	}
	if profile.Socials[0].ObservedAt != "2025-03-12" {
		t.Errorf("expected the X snippet's date, got %q", profile.Socials[0].ObservedAt)
	}
	if profile.Socials[2].ObservedAt != "2025-02-01" {
		t.Errorf("expected the LinkedIn post's date, got %q", profile.Socials[2].ObservedAt)
	}
	if profile.SocialNote != models.PersonSocialDisclaimer {
		t.Errorf("the social section must carry the fixed disclaimer, got %q", profile.SocialNote)
	}
	if len(profile.PublicAccounts) != len(profile.Socials) {
		t.Errorf("public_accounts must project the social section exactly, got %d vs %d", len(profile.PublicAccounts), len(profile.Socials))
	}
	for _, signal := range profile.Socials {
		if strings.Contains(strings.Join(profile.Sources, " "), signal.URL) {
			t.Errorf("a social URL must never be listed as an employment source: %s", signal.URL)
		}
	}
	if scraper.hardCalls != 1 || scraper.scrapeURLs != 0 {
		t.Fatalf("expected only the confirmed profile to be fetched, hardCalls=%d scrapeURLs=%d", scraper.hardCalls, scraper.scrapeURLs)
	}
}

// TestPersonSocialSectionOmitsNameOnlyHits pins the admission rule on its own, without a
// full run: the section is allowed to be thin, but not to present a stranger as the person.
func TestPersonSocialSectionOmitsNameOnlyHits(t *testing.T) {
	gate := newPersonGate(models.PersonQuery{Name: "Jane Doe", Company: "Acme"})
	if _, _, ok := gate.personalizationVerdict(models.SearchResult{
		Title: "Jane Doe | Facebook", Link: "https://www.facebook.com/janedoe", Snippet: "Jane Doe shares design projects.",
	}); ok {
		t.Fatal("a name-only social hit must not reach the flagged section")
	}
	if basis, confidence, ok := gate.personalizationVerdict(models.SearchResult{
		Title: "Jane Doe | Facebook", Link: "https://www.facebook.com/janedoe", Snippet: "Jane Doe at Acme Corporation.",
	}); !ok || basis != "name plus confirmed company" || confidence != 0.7 {
		t.Fatalf("a company-matched social hit must be admitted with its basis, got %q %.2f %v", basis, confidence, ok)
	}
}
