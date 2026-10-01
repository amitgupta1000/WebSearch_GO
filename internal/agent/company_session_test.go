package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestCompanyScrapeRankedUsesTheSessionBeforeTheLadder is the company-mode half of the rule the
// live probe justified: a session the operator supplied reads the profile with one
// cookie-authenticated fetch, so no render is paid for. It holds with
// ALLOW_PROFILE_SCRAPING at its default (off), because that flag gates the *batch* path, not
// the use of a session the operator handed over.
func TestCompanyScrapeRankedUsesTheSessionBeforeTheLadder(t *testing.T) {
	target := "https://www.linkedin.com/in/jane-doe"
	scraper := &sessionAwareScraperMock{
		hasSession:  true,
		fingerprint: "abc123",
		blocked:     recoveredProfile(target),
	}
	engine := newSessionTestEngine(t, scraper, target)
	if engine.ProfileScrapingEnabled() {
		t.Fatal("expected the batch path to stay opted out for this test")
	}
	agent := NewCompanyAgent(engine)
	ranked := []models.RankedResult{{URL: target, Relevant: true, Score: 0.9, SourceType: "professional_profile"}}

	evidence, sources, report := agent.scrapeRanked(context.Background(), "session", ranked, nil, nil, nil, nil)

	if scraper.scrapeURLs != 1 || scraper.hardCalls != 0 {
		t.Fatalf("expected one session fetch and no render, scrapeURLs=%d hardCalls=%d", scraper.scrapeURLs, scraper.hardCalls)
	}
	if len(evidence) != 1 || len(sources) != 1 || sources[0] != target {
		t.Fatalf("expected the profile as evidence, got evidence=%d sources=%v", len(evidence), sources)
	}
	if len(report) != 1 || report[0].Status != "recovered" || !strings.Contains(report[0].Reason, "exported session") {
		t.Fatalf("expected a session-recovered report entry, got %+v", report)
	}
	if report[0].HandlerTactics != "" {
		t.Errorf("no ladder tactic should be claimed for a plain fetch, got %q", report[0].HandlerTactics)
	}
}

// TestCompanyScrapeRankedLaddersWhenTheSessionCannotReadIt keeps the degradation order: the
// session fetch is tried first and its failure does not remove the ladder as the next attempt.
func TestCompanyScrapeRankedLaddersWhenTheSessionCannotReadIt(t *testing.T) {
	target := "https://www.linkedin.com/in/jane-doe"
	scraper := &sessionAwareScraperMock{
		hasSession:  true,
		fingerprint: "abc123",
		scrapeErr:   errors.New("the site refused the request (HTTP 999)"),
		recovered:   recoveredProfile(target),
	}
	engine := newSessionTestEngine(t, scraper, target)
	agent := NewCompanyAgent(engine)
	ranked := []models.RankedResult{{URL: target, Relevant: true, Score: 0.9, SourceType: "professional_profile"}}

	evidence, _, report := agent.scrapeRanked(context.Background(), "session", ranked, nil, nil, nil, nil)

	if scraper.scrapeURLs != 1 || scraper.hardCalls != 1 {
		t.Fatalf("expected the session fetch then the ladder, scrapeURLs=%d hardCalls=%d", scraper.scrapeURLs, scraper.hardCalls)
	}
	if len(evidence) != 1 || len(report) != 1 || report[0].Status != "recovered" {
		t.Fatalf("expected the ladder to recover it, got evidence=%d report=%+v", len(evidence), report)
	}
}
