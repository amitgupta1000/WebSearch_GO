package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// hardScraperMock satisfies PageFetcher and HardURLScraper: the normal path
// returns a scripted result, the ladder is fully scripted.
type hardScraperMock struct {
	scrapeURLs int
	hardCalls  int
	hardOpts   []tools.HardScrapeOptions
	blocked    *models.ScrapeResult
	recovered  *models.ScrapeResult
	hardErr    error
}

func (m *hardScraperMock) ScrapeURL(_ context.Context, targetURL string) (*models.ScrapeResult, error) {
	m.scrapeURLs++
	return m.blocked, nil
}

func (m *hardScraperMock) ScrapeHard(_ context.Context, targetURL string, opts tools.HardScrapeOptions) (*models.ScrapeResult, error) {
	m.hardCalls++
	m.hardOpts = append(m.hardOpts, opts)
	if m.hardErr != nil {
		return nil, m.hardErr
	}
	return m.recovered, nil
}

func recoveredProfile(targetURL string) *models.ScrapeResult {
	return &models.ScrapeResult{
		URL:            targetURL,
		Title:          "Jane Doe - Acme",
		Content:        strings.Repeat("Jane Doe leads procurement at Acme Corporation. ", 12),
		WordCount:      60,
		QualityScore:   0.7,
		Extractor:      "playwright-content-root",
		FallbackStatus: "succeeded",
		FallbackReason: "cookie-render",
	}
}

func blockedResult(targetURL string) *models.ScrapeResult {
	return &models.ScrapeResult{
		URL:          targetURL,
		Title:        "Checking your browser",
		Content:      "Please verify you are human.",
		Blocked:      true,
		BlockReason:  "human-verification challenge",
		QualityScore: 0,
	}
}

func newHardTestEngine(scraper PageFetcher) (*Engine, *cache.MemoryCache) {
	store := cache.NewMemoryCache(100)
	return NewEngine(&mockLLM{}, &mockSearcher{}, scraper, store, 5, 3, 10), store
}

func TestHardURLHandlerRecoversBlockedPage(t *testing.T) {
	target := "https://acme.com/leadership"
	scraper := &hardScraperMock{blocked: blockedResult(target), recovered: recoveredProfile(target)}
	engine, store := newHardTestEngine(scraper)
	defer store.Close()

	handler := newHardURLHandler(engine)
	outcome := handler.handle(context.Background(), "session", target, "blocked page", 1, nil, tools.HardScrapeOptions{})

	if outcome.Status != "recovered" || outcome.Result == nil {
		t.Fatalf("expected recovery, got %+v", outcome)
	}
	if outcome.Result.FallbackReason != "cookie-render" {
		t.Errorf("unexpected winning tactic: %q", outcome.Result.FallbackReason)
	}
	if scraper.scrapeURLs != 0 || scraper.hardCalls != 1 {
		t.Errorf("handler must go straight to the ladder, scrapeURLs=%d hardCalls=%d", scraper.scrapeURLs, scraper.hardCalls)
	}
	if scraper.hardOpts[0].AlreadyRendered {
		t.Error("a fresh escalation must not skip the plain-render step")
	}
}

func TestHardURLHandlerCapsEscalationsPerRun(t *testing.T) {
	scraper := &hardScraperMock{recovered: recoveredProfile("https://acme.com/leadership")}
	engine, store := newHardTestEngine(scraper)
	defer store.Close()

	handler := newHardURLHandler(engine)
	for i := 0; i < engine.HardURLsPerRun(); i++ {
		// Distinct targets so each escalation runs the ladder instead of
		// hitting the success cache.
		target := fmt.Sprintf("https://acme.com/page-%d", i+1)
		if outcome := handler.handle(context.Background(), "session", target, "blocked page", 1, nil, tools.HardScrapeOptions{}); outcome.Status != "recovered" {
			t.Fatalf("escalation %d: expected recovery, got %+v", i+1, outcome)
		}
	}
	outcome := handler.handle(context.Background(), "session", "https://acme.com/page-final", "blocked page", 1, nil, tools.HardScrapeOptions{})
	if outcome.Status != "deferred" {
		t.Fatalf("expected the per-run cap to defer escalation %d, got %+v", engine.HardURLsPerRun()+1, outcome)
	}
	if scraper.hardCalls != engine.HardURLsPerRun() {
		t.Errorf("ladder ran %d times, want %d", scraper.hardCalls, engine.HardURLsPerRun())
	}
}

func TestHardURLHandlerDefersWhenRunBudgetIsLow(t *testing.T) {
	target := "https://acme.com/leadership"
	scraper := &hardScraperMock{recovered: recoveredProfile(target)}
	engine, store := newHardTestEngine(scraper)
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	handler := newHardURLHandler(engine)
	outcome := handler.handle(ctx, "session", target, "blocked page", 1, nil, tools.HardScrapeOptions{})
	if outcome.Status != "deferred" || scraper.hardCalls != 0 {
		t.Fatalf("expected deferral with under %s remaining, got %+v", handler.urlBudget()+hardURLPostLadderMargin, outcome)
	}
}

func TestHardURLHandlerUnavailableWhenDisabledOrUnsupported(t *testing.T) {
	target := "https://acme.com/leadership"

	// A scraper without the ladder interface (plain test double) disables it.
	engine, store := newHardTestEngine(&mockScraper{})
	defer store.Close()
	handler := newHardURLHandler(engine)
	if outcome := handler.handle(context.Background(), "session", target, "blocked page", 1, nil, tools.HardScrapeOptions{}); outcome.Status != "unavailable" {
		t.Fatalf("expected unavailable without HardURLScraper support, got %+v", outcome)
	}

	// The operator toggle disables it even when the scraper supports it.
	scraper := &hardScraperMock{recovered: recoveredProfile(target)}
	engine2, store2 := newHardTestEngine(scraper)
	defer store2.Close()
	engine2.SetHardURLHandler(false)
	handler2 := newHardURLHandler(engine2)
	if outcome := handler2.handle(context.Background(), "session", target, "blocked page", 1, nil, tools.HardScrapeOptions{}); outcome.Status != "unavailable" {
		t.Fatalf("expected unavailable after SetHardURLHandler(false), got %+v", outcome)
	}
	if scraper.hardCalls != 0 {
		t.Errorf("ladder ran %d times while disabled", scraper.hardCalls)
	}
}

func TestScrapeHardCachedSeparatesModesAndCachesFailures(t *testing.T) {
	target := "https://acme.com/leadership"

	// Success on the hard mode must not leak into (or collide with) the
	// normal render modes, and successes are cached.
	scraper := &hardScraperMock{blocked: blockedResult(target), recovered: recoveredProfile(target)}
	engine, store := newHardTestEngine(scraper)
	defer store.Close()

	if _, err := engine.ScrapeCached(context.Background(), target); err != nil {
		t.Fatalf("ScrapeCached returned error: %v", err)
	}
	for i := 0; i < 2; i++ {
		res, err := engine.ScrapeHardCached(context.Background(), target, tools.HardScrapeOptions{})
		if err != nil || res == nil || res.FallbackReason != "cookie-render" {
			t.Fatalf("hard ladder run %d: res=%v err=%v", i+1, res, err)
		}
	}
	if scraper.hardCalls != 1 {
		t.Errorf("ladder ran %d times, want 1 (successes must be cached)", scraper.hardCalls)
	}
	if scraper.scrapeURLs+scraper.hardCalls != 2 {
		t.Errorf("expected one normal scrape and one ladder run, got %d calls", scraper.scrapeURLs+scraper.hardCalls)
	}

	// Failures are negative-cached under the hard mode only.
	failing := &hardScraperMock{hardErr: fmt.Errorf("%w (plain-render → extended-settle): captcha challenge", tools.ErrHardLadderExhausted)}
	engine2, store2 := newHardTestEngine(failing)
	defer store2.Close()
	for i := 0; i < 2; i++ {
		if _, err := engine2.ScrapeHardCached(context.Background(), target, tools.HardScrapeOptions{}); err == nil {
			t.Fatal("expected the ladder to fail")
		}
	}
	if failing.hardCalls != 1 {
		t.Errorf("ladder ran %d times, want 1 (failures must be negative-cached)", failing.hardCalls)
	}

	if hardScrapeCacheKey(target) == scrapeCacheKey(context.Background(), target) ||
		hardScrapeCacheKey(target) == scrapeCacheKey(tools.WithBrowserFallback(context.Background(), true), target) {
		t.Fatal("hard-mode cache keys must differ from default and browser modes")
	}
}

func TestCompanyScrapeRankedRecoversBlockedPage(t *testing.T) {
	target := "https://acme.com/leadership"
	scraper := &hardScraperMock{blocked: blockedResult(target), recovered: recoveredProfile(target)}
	engine, store := newHardTestEngine(scraper)
	defer store.Close()

	agent := NewCompanyAgent(engine)
	ranked := []models.RankedResult{{URL: target, Relevant: true, Score: 0.9, SourceType: "official_leadership"}}
	evidence, sources, report := agent.scrapeRanked(context.Background(), "session", ranked, nil, nil, nil, nil)

	if len(evidence) != 1 || evidence[0].QualityScore != 0.7 {
		t.Fatalf("expected recovered page evidence, got %+v", evidence)
	}
	if len(sources) != 1 || sources[0] != target {
		t.Fatalf("expected recovered source, got %v", sources)
	}
	if len(report) != 1 || report[0].Status != "recovered" || report[0].HandlerStatus != "recovered" || report[0].HandlerTactics != "cookie-render" {
		t.Fatalf("expected a recovered scrape report entry, got %+v", report)
	}
}

func TestCompanyScrapeRankedFallsBackToSnippetWhenLadderFails(t *testing.T) {
	target := "https://www.linkedin.com/in/jane-doe"
	scraper := &hardScraperMock{
		hardErr: fmt.Errorf("%w (plain-render → cookie-render → extended-settle): captcha challenge", tools.ErrHardLadderExhausted),
	}
	engine, store := newHardTestEngine(scraper)
	defer store.Close()

	agent := NewCompanyAgent(engine)
	ranked := []models.RankedResult{{URL: target, Relevant: true, Score: 0.9, SourceType: "professional_profile"}}
	results := []models.SearchResult{{Title: "Jane Doe - Acme | LinkedIn", Link: target, Snippet: "Jane Doe is Chief Technology Officer at Acme."}}
	evidence, _, report := agent.scrapeRanked(context.Background(), "session", ranked, results, nil, nil, nil)

	if len(evidence) != 1 || evidence[0].EvidenceType != "search_snippet_fallback" {
		t.Fatalf("expected snippet fallback after a failed ladder, got %+v", evidence)
	}
	if len(report) != 1 || report[0].Status != "skipped" || report[0].HandlerStatus != "failed" {
		t.Fatalf("expected the handler failure to be recorded, got %+v", report)
	}
	if !strings.Contains(report[0].Reason, "hard-URL handler could not recover") {
		t.Errorf("expected the report reason to explain the handler outcome, got %q", report[0].Reason)
	}
}

// The legacy path must stay unchanged: without a HardURLScraper the
// professional-profile policy is exactly what it was before the handler.
func TestCompanyScrapeRankedKeepsLegacyProfilePolicyWithoutHandler(t *testing.T) {
	engine, store := newHardTestEngine(&mockScraper{})
	defer store.Close()

	agent := NewCompanyAgent(engine)
	ranked := []models.RankedResult{{URL: "https://www.linkedin.com/in/jane-doe", Relevant: true, Score: 0.9, SourceType: "professional_profile"}}
	results := []models.SearchResult{{Title: "Jane Doe - Acme | LinkedIn", Link: ranked[0].URL, Snippet: "Jane Doe is CTO at Acme."}}
	_, _, report := agent.scrapeRanked(context.Background(), "session", ranked, results, nil, nil, nil)
	if len(report) != 1 || report[0].Status != "skipped" || report[0].HandlerStatus != "" {
		t.Fatalf("expected the legacy skipped entry, got %+v", report)
	}
	if !strings.Contains(report[0].Reason, "direct professional-profile scraping is disabled") {
		t.Errorf("expected the legacy skip reason, got %q", report[0].Reason)
	}
}

// profileSearcherMock lets ResearchPerson see a LinkedIn hit as the first result.
type profileSearcherMock struct{ results []models.SearchResult }

func (m *profileSearcherMock) Search(context.Context, string, int) ([]models.SearchResult, error) {
	return m.results, nil
}

func TestPersonAgentRoutesProfessionalProfilesThroughHandler(t *testing.T) {
	target := "https://www.linkedin.com/in/jane-doe"
	scraper := &hardScraperMock{recovered: recoveredProfile(target)}
	store := cache.NewMemoryCache(100)
	defer store.Close()

	engine := NewEngine(&mockLLM{}, &profileSearcherMock{results: []models.SearchResult{
		{Title: "Jane Doe - Acme | LinkedIn", Link: target, Snippet: "Jane Doe at Acme."},
	}}, scraper, store, 5, 3, 10)

	person := NewPersonAgent(engine)
	profile, err := person.ResearchPerson(context.Background(), "session", models.PersonQuery{Name: "Jane Doe", Company: "Acme"}, nil)
	if err != nil {
		t.Fatalf("ResearchPerson returned error: %v", err)
	}
	if scraper.hardCalls != 1 || scraper.scrapeURLs != 0 {
		t.Fatalf("expected the profile to be escalated once, scrapeURLs=%d hardCalls=%d", scraper.scrapeURLs, scraper.hardCalls)
	}
	found := false
	for _, source := range profile.Sources {
		if source == target {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the recovered profile among sources, got %v", profile.Sources)
	}
}

func TestScrapeHardCachedSurfacesUnavailableWithoutLadderSupport(t *testing.T) {
	engine, store := newHardTestEngine(&mockScraper{})
	defer store.Close()

	if _, err := engine.ScrapeHardCached(context.Background(), "https://acme.com/page", tools.HardScrapeOptions{}); !errors.Is(err, tools.ErrHardLadderUnavailable) {
		t.Fatalf("expected ErrHardLadderUnavailable, got %v", err)
	}
}
