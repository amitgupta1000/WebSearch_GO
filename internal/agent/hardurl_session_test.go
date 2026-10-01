package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// sessionAwareScraperMock is a scraper whose session state can change mid-test, so the
// cheap-path rule and the cache scoping can both be exercised.
type sessionAwareScraperMock struct {
	hasSession  bool
	fingerprint string
	scrapeURLs  int
	hardCalls   int
	blocked     *models.ScrapeResult
	recovered   *models.ScrapeResult
	scrapeErr   error
}

func (m *sessionAwareScraperMock) ScrapeURL(_ context.Context, _ string) (*models.ScrapeResult, error) {
	m.scrapeURLs++
	if m.scrapeErr != nil {
		return nil, m.scrapeErr
	}
	return m.blocked, nil
}

func (m *sessionAwareScraperMock) ScrapeHard(_ context.Context, _ string, _ tools.HardScrapeOptions) (*models.ScrapeResult, error) {
	m.hardCalls++
	return m.recovered, nil
}

func (m *sessionAwareScraperMock) HasSessionCookies(string) bool { return m.hasSession }

func (m *sessionAwareScraperMock) SessionFingerprint(string) string {
	if !m.hasSession {
		return ""
	}
	return m.fingerprint
}

// TestHardURLHandlerNamesARejectedSession separates "the site said no" from "your session was
// refused": a redirect loop with a session in play is reported as a session problem, so the
// operator re-captures instead of debugging a site that is working.
func TestHardURLHandlerNamesARejectedSession(t *testing.T) {
	target := "https://in.linkedin.com/in/someone-1234"
	scraper := &hardScraperMock{
		hardErr: fmt.Errorf("%w (cookie-render → extended-settle): %w", tools.ErrHardLadderExhausted,
			fmt.Errorf("%w: playwright: net::ERR_TOO_MANY_REDIRECTS", tools.ErrSessionRejected)),
	}
	engine := newSessionTestEngine(t, scraper, target)
	handler := newHardURLHandler(engine)

	outcome := handler.handle(context.Background(), "session", target, "professional profile", 5, nil, tools.HardScrapeOptions{})
	if outcome.Status != "session-rejected" {
		t.Fatalf("expected the outcome to be a session rejection, got %+v", outcome)
	}
	if !strings.Contains(outcome.Reason, "re-capture") {
		t.Errorf("expected the reason to say what to do, got %q", outcome.Reason)
	}
}

// newSessionTestEngine builds an engine whose search returns the given LinkedIn profile, which
// is the shape the session rules apply to.
func newSessionTestEngine(t *testing.T, scraper PageFetcher, target string) *Engine {
	t.Helper()
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	return NewEngine(&mockLLM{}, &profileSearcherMock{results: []models.SearchResult{
		{Title: "Jane Doe - Acme | LinkedIn", Link: target, Snippet: "Jane Doe at Acme."},
	}}, scraper, store, 5, 3, 10)
}

// TestPersonAgentUsesTheSessionBeforeTheLadder is the change that gives an exported session
// its value: for a profile URL the cheap cookie-authenticated fetch is tried first, and the
// ladder — the expensive, most detectable path — is not touched when it works.
func TestPersonAgentUsesTheSessionBeforeTheLadder(t *testing.T) {
	target := "https://www.linkedin.com/in/jane-doe"
	scraper := &sessionAwareScraperMock{
		hasSession:  true,
		fingerprint: "abc123",
		blocked:     recoveredProfile(target),
		recovered:   recoveredProfile(target),
	}
	engine := newSessionTestEngine(t, scraper, target)
	person := NewPersonAgent(engine)

	profile, err := person.ResearchPerson(context.Background(), "session", models.PersonQuery{Name: "Jane Doe", Company: "Acme"}, nil)
	if err != nil {
		t.Fatalf("ResearchPerson: %v", err)
	}
	if scraper.scrapeURLs != 1 || scraper.hardCalls != 0 {
		t.Fatalf("expected the session fetch once and the ladder not at all, scrapeURLs=%d hardCalls=%d",
			scraper.scrapeURLs, scraper.hardCalls)
	}
	found := false
	for _, source := range profile.Sources {
		if source == target {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the profile URL in the dossier sources, got %v", profile.Sources)
	}
}

// TestPersonAgentFallsBackToTheLadderWhenTheSessionFetchFails keeps the degradation honest:
// a session that does not open the page still leaves the ladder as the next attempt.
func TestPersonAgentFallsBackToTheLadderWhenTheSessionFetchFails(t *testing.T) {
	target := "https://www.linkedin.com/in/jane-doe"
	scraper := &sessionAwareScraperMock{
		hasSession:  true,
		fingerprint: "abc123",
		scrapeErr:   errors.New("authwall"),
		recovered:   recoveredProfile(target),
	}
	engine := newSessionTestEngine(t, scraper, target)
	person := NewPersonAgent(engine)

	if _, err := person.ResearchPerson(context.Background(), "session", models.PersonQuery{Name: "Jane Doe", Company: "Acme"}, nil); err != nil {
		t.Fatalf("ResearchPerson: %v", err)
	}
	if scraper.scrapeURLs != 1 || scraper.hardCalls != 1 {
		t.Fatalf("expected the cheap fetch then the ladder, scrapeURLs=%d hardCalls=%d", scraper.scrapeURLs, scraper.hardCalls)
	}
}

// TestPersonAgentKeepsTheLadderOnlyPathWithoutASession pins that a logged-out run behaves
// exactly as before: no unauthenticated fetch of an authwall, straight to the ladder.
func TestPersonAgentKeepsTheLadderOnlyPathWithoutASession(t *testing.T) {
	target := "https://www.linkedin.com/in/jane-doe"
	scraper := &sessionAwareScraperMock{recovered: recoveredProfile(target)}
	engine := newSessionTestEngine(t, scraper, target)
	person := NewPersonAgent(engine)

	if _, err := person.ResearchPerson(context.Background(), "session", models.PersonQuery{Name: "Jane Doe", Company: "Acme"}, nil); err != nil {
		t.Fatalf("ResearchPerson: %v", err)
	}
	if scraper.scrapeURLs != 0 || scraper.hardCalls != 1 {
		t.Fatalf("expected the ladder only, scrapeURLs=%d hardCalls=%d", scraper.scrapeURLs, scraper.hardCalls)
	}
}

// TestScrapeCachedIgnoresALoggedOutFailureOnceASessionExists covers the freshness model: a
// page that failed unauthenticated is negative-cached for 15 minutes, and that entry must not
// be handed to the run for which a session was just captured. The cache key carries the
// session, so the page is fetched again.
func TestScrapeCachedIgnoresALoggedOutFailureOnceASessionExists(t *testing.T) {
	target := "https://www.linkedin.com/in/jane-doe"
	scraper := &sessionAwareScraperMock{scrapeErr: errors.New("authwall: sign in")}
	engine := newSessionTestEngine(t, scraper, target)

	if _, err := engine.ScrapeCached(context.Background(), target); err == nil {
		t.Fatal("expected the logged-out fetch to fail")
	}
	if scraper.scrapeURLs != 1 {
		t.Fatalf("expected one attempt, got %d", scraper.scrapeURLs)
	}

	// The cached failure answers the next call without a request.
	scraper.scrapeErr = nil
	scraper.blocked = recoveredProfile(target)
	if _, err := engine.ScrapeCached(context.Background(), target); err == nil {
		t.Fatal("expected the cached failure to be served while no session exists")
	}
	if scraper.scrapeURLs != 1 {
		t.Fatalf("expected the negative entry to answer without a fetch, got %d attempts", scraper.scrapeURLs)
	}

	// With a session captured, the same URL must be fetched for real.
	scraper.hasSession = true
	scraper.fingerprint = "abc123"
	if _, err := engine.ScrapeCached(context.Background(), target); err != nil {
		t.Fatalf("expected the session fetch to succeed, got %v", err)
	}
	if scraper.scrapeURLs != 2 {
		t.Fatalf("expected the session fetch to bypass the logged-out failure, got %d attempts", scraper.scrapeURLs)
	}
}
