package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// Searcher performs web searches. *tools.SerperClient satisfies this interface.
type Searcher interface {
	Search(ctx context.Context, query string, maxResults int) ([]models.SearchResult, error)
}

// PageFetcher extracts readable page content. *tools.Scraper satisfies this interface.
type PageFetcher interface {
	ScrapeURL(ctx context.Context, targetURL string) (*models.ScrapeResult, error)
}

// HardURLScraper escalates URLs the normal path could not read through the
// bounded hard-URL ladder. *tools.Scraper satisfies this interface; test
// doubles that omit it simply disable the escalation path.
type HardURLScraper interface {
	ScrapeHard(ctx context.Context, targetURL string, opts tools.HardScrapeOptions) (*models.ScrapeResult, error)
}

// LLMClient generates structured output. *llm.GeminiClient satisfies this interface.
type LLMClient interface {
	GenerateStructured(ctx context.Context, systemPrompt, userPrompt string, target any) error
}

type imageTextExtractor interface {
	ExtractImageText(ctx context.Context, images []models.Image) (map[string]string, error)
}

const (
	// searchResultsTTL is how long Serper search results stay cached.
	searchResultsTTL = 48 * time.Hour
	// scrapeTTL is how long scraped page content stays cached.
	scrapeTTL = 7 * 24 * time.Hour
	// negativeScrapeTTL is how long a failed scrape is remembered so repeated
	// questions do not re-attempt a known dead end (paywalls, 403s, rejected
	// types). The scraper already retries transient network failures, so what
	// reaches here is usually deterministic.
	negativeScrapeTTL = 15 * time.Minute
	// defaultMaxScrapesPerResearch is the default cap on page scrapes per run.
	defaultMaxScrapesPerResearch = 10
	// defaultValidationLookups caps the reverse searches the freshness pass may
	// run per company run. Each lookup is one snippet-only search (no fetch, no
	// browser), so the cap is about how many rows are worth re-checking rather
	// than about cost.
	defaultValidationLookups = 4
	// defaultValidationStaleAfter and defaultValidationRecentWithin are the
	// freshness windows, expressed in months and converted to durations of 30
	// days: a source older than the stale window is reported as stale, and a
	// source inside the recent window from a company-owned site or two
	// independent domains counts as verified-recent.
	defaultValidationStaleAfterMonths   = 36
	defaultValidationRecentWithinMonths = 18
	// defaultMaxSearchResults is the default cap on search results returned per query.
	defaultMaxSearchResults = 12
	// defaultMaxSteps is the default cap on agent/tool iterations per run.
	defaultMaxSteps = 10
	// defaultScrapeConcurrency bounds how many pages are fetched at once. Pages
	// are fetched in parallel because a run has a fixed deadline and each fetch
	// is dominated by network latency; the per-host limiter in the scraper keeps
	// that parallelism from turning into hammering a single site.
	defaultScrapeConcurrency = 4
	// defaultScrapeRevalidateAfter is how long a cached page is trusted outright
	// before a conditional request checks whether it changed.
	defaultScrapeRevalidateAfter = 24 * time.Hour
	// scrapeRevalidateTimeout bounds a revalidation so a cache hit stays cheap.
	scrapeRevalidateTimeout = 6 * time.Second
	// minScrapeContentLen is the minimum useful scraped content size in bytes.
	minScrapeContentLen = 80
)

// Engine orchestrates multi-turn ReAct loops, coordinating tools and LLM responses.
type Engine struct {
	llmClient             LLMClient
	serperClient          Searcher
	scraper               PageFetcher
	cache                 cache.Store
	maxScrapesPerResearch int
	maxSearchResults      int
	maxSteps              int
	scrapeConcurrency     int
	scrapeRevalidateAfter time.Duration
	scrapeProfiles        bool
	// exporter stores finished runs as Markdown and PDF artifacts.
	exporter RunExporter
	// hardURLsEnabled gates the bounded hard-URL escalation ladder. It is on
	// by default and can be disabled with HARD_URL_HANDLER=0; when the scraper
	// cannot start a browser the ladder reports itself unavailable instead.
	hardURLsEnabled bool
	// hardURLsPerRun caps escalations per run, and hardURLBudget bounds the whole
	// ladder for one URL. Both fall back to defaults derived from the ladder's own
	// step caps.
	hardURLsPerRun int
	hardURLBudget  time.Duration
	// validateEvidence turns on the freshness pass over a finished chart, and the
	// three fields below bound and time-box it: how many reverse lookups a run
	// may spend, the age that counts as stale, and the age that counts as recent.
	validateEvidence       bool
	validationLookups      int
	validationStaleAfter   time.Duration
	validationRecentWithin time.Duration
}

// EngineOption customizes an Engine after its required dependencies are set.
type EngineOption func(*Engine)

// WithProfileScraping allows professional-profile sources (for example
// linkedin.com profiles) to be fetched directly.
//
// It is off by default: profile URLs normally contribute only their public search
// snippet as evidence, which keeps the tool clear of fetching personal-profile
// pages but limits department-level results for companies whose functional
// leadership is only visible there.
func WithProfileScraping(enabled bool) EngineOption {
	return func(e *Engine) { e.scrapeProfiles = enabled }
}

// WithScrapeConcurrency sets how many pages may be fetched at once. Values below
// one restore the default.
func WithScrapeConcurrency(concurrency int) EngineOption {
	return func(e *Engine) { e.scrapeConcurrency = concurrency }
}

// WithScrapeRevalidation sets how long a cached page is served without checking
// whether it changed. A zero or negative duration disables conditional requests,
// so every cache hit is served as-is until the entry expires.
func WithScrapeRevalidation(after time.Duration) EngineOption {
	return func(e *Engine) { e.scrapeRevalidateAfter = after }
}

// RunExporter stores the output artifacts of finished runs. *export.Exporter
// satisfies it; the interface keeps the agent package independent of the storage
// implementation.
type RunExporter interface {
	Enabled() bool
	ExportCompany(ctx context.Context, chart *models.OrgChart) []models.ExportArtifact
	ExportPerson(ctx context.Context, profile *models.PersonProfile) []models.ExportArtifact
}

// WithExporter attaches an artifact exporter. Without one, runs are not saved.
func WithExporter(exporter RunExporter) EngineOption {
	return func(e *Engine) { e.exporter = exporter }
}

// NewEngine initializes the agentic ReAct engine.
func NewEngine(
	llmClient LLMClient,
	serperClient Searcher,
	scraper PageFetcher,
	store cache.Store,
	maxScrapes int,
	maxSearchResults int,
	maxSteps int,
	opts ...EngineOption,
) *Engine {
	if maxScrapes <= 0 {
		maxScrapes = defaultMaxScrapesPerResearch
	}
	if maxSearchResults <= 0 {
		maxSearchResults = defaultMaxSearchResults
	}
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}
	engine := &Engine{
		llmClient:              llmClient,
		serperClient:           serperClient,
		scraper:                scraper,
		cache:                  store,
		maxScrapesPerResearch:  maxScrapes,
		maxSearchResults:       maxSearchResults,
		maxSteps:               maxSteps,
		scrapeConcurrency:      defaultScrapeConcurrency,
		scrapeRevalidateAfter:  defaultScrapeRevalidateAfter,
		hardURLsEnabled:        true,
		hardURLsPerRun:         defaultHardURLsPerRun,
		hardURLBudget:          defaultHardURLBudget(),
		validateEvidence:       true,
		validationLookups:      defaultValidationLookups,
		validationStaleAfter:   months(defaultValidationStaleAfterMonths),
		validationRecentWithin: months(defaultValidationRecentWithinMonths),
	}
	for _, opt := range opts {
		opt(engine)
	}
	return engine
}

// ProfileScrapingEnabled reports whether professional-profile pages may be fetched.
func (e *Engine) ProfileScrapingEnabled() bool {
	return e != nil && e.scrapeProfiles
}

// SetHardURLHandler toggles the bounded hard-URL escalation ladder. It is
// wired from HARD_URL_HANDLER at startup; disabling it restores the previous
// behaviour of never re-attempting blocked pages or professional profiles.
func (e *Engine) SetHardURLHandler(enabled bool) {
	if e != nil {
		e.hardURLsEnabled = enabled
	}
}

// HardURLsAvailable reports whether the hard-URL ladder can run: the feature
// is enabled and the scraper supports the escalation interface.
func (e *Engine) HardURLsAvailable() bool {
	if e == nil || !e.hardURLsEnabled {
		return false
	}
	_, ok := e.scraper.(HardURLScraper)
	return ok
}

// HardURLsPerRun returns how many URLs one run may escalate.
func (e *Engine) HardURLsPerRun() int {
	if e == nil || e.hardURLsPerRun <= 0 {
		return defaultHardURLsPerRun
	}
	return e.hardURLsPerRun
}

// HardURLBudget returns how long the whole ladder for one URL may take.
func (e *Engine) HardURLBudget() time.Duration {
	if e == nil || e.hardURLBudget <= 0 {
		return defaultHardURLBudget()
	}
	return e.hardURLBudget
}

// WithHardURLLimits tunes the bounded hard-URL ladder: how many URLs one run may
// escalate, and how long the ladder for one URL may take. A non-positive value
// keeps the default, whose budget is derived from the ladder's own step caps so the
// final (extended-settle) step cannot be clipped by an outside timeout.
func WithHardURLLimits(perRun int, budget time.Duration) EngineOption {
	return func(e *Engine) {
		if perRun > 0 {
			e.hardURLsPerRun = perRun
		}
		if budget > 0 {
			e.hardURLBudget = budget
		}
	}
}

// WithValidation configures the freshness pass over a finished chart. lookups
// caps the reverse searches, and the two month windows decide when a row counts
// as current or stale. Zero or negative values keep the defaults.
func WithValidation(enabled bool, lookups, staleAfterMonths, recentWithinMonths int) EngineOption {
	return func(e *Engine) {
		e.validateEvidence = enabled
		if lookups >= 0 {
			e.validationLookups = lookups
		}
		if staleAfterMonths > 0 {
			e.validationStaleAfter = months(staleAfterMonths)
		}
		if recentWithinMonths > 0 {
			e.validationRecentWithin = months(recentWithinMonths)
		}
	}
}

// months converts a month count into a duration, using 30-day months. The
// windows are coarse by nature — a person row is either backed by something
// recent or it is not — so calendar exactness would add nothing.
func months(count int) time.Duration {
	return time.Duration(count) * 30 * 24 * time.Hour
}

// ValidationEnabled reports whether the freshness pass runs.
func (e *Engine) ValidationEnabled() bool {
	if e == nil {
		return false
	}
	return e.validateEvidence
}

// ValidationLookups returns how many reverse lookups one run may spend.
func (e *Engine) ValidationLookups() int {
	if e == nil || e.validationLookups < 0 {
		return defaultValidationLookups
	}
	return e.validationLookups
}

// ValidationStaleAfter returns the age beyond which a row's newest source counts
// as stale.
func (e *Engine) ValidationStaleAfter() time.Duration {
	if e == nil || e.validationStaleAfter <= 0 {
		return months(defaultValidationStaleAfterMonths)
	}
	return e.validationStaleAfter
}

// ValidationRecentWithin returns the age inside which a corroborated source
// counts as current.
func (e *Engine) ValidationRecentWithin() time.Duration {
	if e == nil || e.validationRecentWithin <= 0 {
		return months(defaultValidationRecentWithinMonths)
	}
	return e.validationRecentWithin
}

// MaxScrapes returns the maximum number of URLs to scrape during a research run.
func (e *Engine) MaxScrapes() int {
	if e == nil || e.maxScrapesPerResearch <= 0 {
		return defaultMaxScrapesPerResearch
	}
	return e.maxScrapesPerResearch
}

// MaxSearchResults returns the maximum number of search results to fetch per query.
func (e *Engine) MaxSearchResults() int {
	if e == nil || e.maxSearchResults <= 0 {
		return defaultMaxSearchResults
	}
	return e.maxSearchResults
}

// MaxSteps returns the maximum number of agent/tool iterations per research run.
func (e *Engine) MaxSteps() int {
	if e == nil || e.maxSteps <= 0 {
		return defaultMaxSteps
	}
	return e.maxSteps
}

// ScrapeConcurrency returns how many pages may be fetched at once.
func (e *Engine) ScrapeConcurrency() int {
	if e == nil || e.scrapeConcurrency <= 0 {
		return defaultScrapeConcurrency
	}
	return e.scrapeConcurrency
}

// SearchCached performs a Google search with TTL caching.
func (e *Engine) SearchCached(ctx context.Context, query string, maxResults int) ([]models.SearchResult, error) {
	cacheKey := cache.HashKey("search", query)
	var cached []models.SearchResult
	if cache.GetJSON(ctx, e.cache, cacheKey, &cached) {
		return cached, nil
	}

	results, err := e.serperClient.Search(ctx, query, maxResults)
	if err != nil {
		return nil, err
	}
	cache.SetJSON(ctx, e.cache, cacheKey, results, searchResultsTTL)
	return results, nil
}

// SearchFresh bypasses the search cache for an explicitly requested fresh lookup.
func (e *Engine) SearchFresh(ctx context.Context, query string, maxResults int) ([]models.SearchResult, error) {
	if e.serperClient == nil {
		return nil, fmt.Errorf("search client is not configured")
	}
	results, err := e.serperClient.Search(ctx, query, maxResults)
	if err != nil {
		return nil, err
	}
	cache.SetJSON(ctx, e.cache, cache.HashKey("search", query), results, searchResultsTTL)
	return results, nil
}

// scrapeCacheKey scopes a cached scrape to the render mode in effect, so a thin
// HTTP-only result never masks a browser-rendered re-run of the same URL, and a
// negative entry from a blocked fetch does not suppress the rendered attempt.
func scrapeCacheKey(ctx context.Context, targetURL string) string {
	mode := "default"
	if override, ok := tools.BrowserFallbackOverride(ctx); ok {
		if override {
			mode = "browser"
		} else {
			mode = "http"
		}
	}
	return cache.HashKey("scrape", targetURL+"|mode="+mode)
}

// ScrapeOutcome pairs a scrape target with the result of fetching it. Outcomes
// are returned in the order the URLs were requested, so a caller keeps ranking
// order while the fetches themselves run in parallel.
type ScrapeOutcome struct {
	URL    string
	Result *models.ScrapeResult
	Err    error
}

// ScrapeCachedBatch fetches several target URLs concurrently, bounded by
// ScrapeConcurrency(), and returns one outcome per URL in the given order.
//
// Pages are fetched in parallel because a run's deadline is fixed and each fetch
// is dominated by network latency; the per-host limiter inside the scraper keeps
// that parallelism from concentrating on a single site.
func (e *Engine) ScrapeCachedBatch(ctx context.Context, targetURLs []string) []ScrapeOutcome {
	outcomes := make([]ScrapeOutcome, len(targetURLs))
	if len(targetURLs) == 0 {
		return outcomes
	}
	limit := e.ScrapeConcurrency()
	if limit > len(targetURLs) {
		limit = len(targetURLs)
	}

	semaphore := make(chan struct{}, limit)
	var waitGroup sync.WaitGroup
	for index, targetURL := range targetURLs {
		outcomes[index].URL = targetURL
		waitGroup.Add(1)
		go func(index int, targetURL string) {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			// A panic in the fetch layer (a browser/driver fault, a malformed
			// page) must fail this one URL, not the whole process: this
			// goroutine is outside net/http's per-request recovery, so an
			// unhandled panic here takes the server down with it.
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Printf("[ERROR] scrape worker panicked for %s: %v\n%s", targetURL, recovered, debug.Stack())
					outcomes[index].URL = targetURL
					outcomes[index].Err = fmt.Errorf("scrape panicked: %v", recovered)
				}
			}()
			result, err := e.ScrapeCached(ctx, targetURL)
			outcomes[index].Result = result
			outcomes[index].Err = err
		}(index, targetURL)
	}
	waitGroup.Wait()
	return outcomes
}

// cachedEntryKind discriminates what a scrape cache entry holds.
const (
	cachedKindResult  = "result"
	cachedKindFailure = "failure"
)

// cachedScrapeEntry is the value stored for one scrape target: either a page
// result or a remembered failure, plus when it was stored, which is what the
// revalidation decision is based on.
type cachedScrapeEntry struct {
	Kind     string               `json:"kind"`
	Result   *models.ScrapeResult `json:"result,omitempty"`
	Error    string               `json:"error,omitempty"`
	StoredAt time.Time            `json:"stored_at"`
}

// ScrapeCached fetches and extracts webpage text with TTL caching. Successful
// results are cached for scrapeTTL; failures are cached briefly so a repeated
// question does not pay to rediscover the same dead end. Cancellations are never
// cached.
func (e *Engine) ScrapeCached(ctx context.Context, targetURL string) (*models.ScrapeResult, error) {
	cacheKey := scrapeCacheKey(ctx, targetURL)
	var entry cachedScrapeEntry
	if cache.GetJSON(ctx, e.cache, cacheKey, &entry) {
		switch entry.Kind {
		case cachedKindFailure:
			if entry.Error != "" {
				return nil, errors.New(entry.Error)
			}
			return nil, fmt.Errorf("cached scrape failure for %s", targetURL)
		case cachedKindResult:
			if entry.Result != nil {
				return e.revalidateScrape(ctx, targetURL, cacheKey, entry), nil
			}
		}
	}

	res, err := e.scraper.ScrapeURL(ctx, targetURL)
	if err != nil {
		if ctx.Err() == nil {
			e.storeScrapeFailure(ctx, cacheKey, err)
		}
		return nil, err
	}
	e.storeScrapeResult(ctx, cacheKey, res)
	return res, nil
}

// revalidateScrape returns the cached page, refreshing it with a conditional
// request once the entry is old enough for freshness to matter.
//
// Conditional requests are what keep a 7-day cache honest without paying for a
// full download: an unchanged page answers 304 Not Modified, which costs one
// round trip. A revalidation that fails for any reason keeps the cached copy,
// because losing good evidence is worse than serving slightly stale evidence.
func (e *Engine) revalidateScrape(ctx context.Context, targetURL, cacheKey string, entry cachedScrapeEntry) *models.ScrapeResult {
	cached := entry.Result
	if cached == nil || e.scraper == nil || e.scrapeRevalidateAfter <= 0 || ctx.Err() != nil {
		return cached
	}
	if !hasRevalidationValidators(cached) || time.Since(entry.StoredAt) < e.scrapeRevalidateAfter {
		return cached
	}

	revalidateCtx, cancel := context.WithTimeout(
		tools.WithConditionalRequest(ctx, cached.ETag, cached.LastModified), scrapeRevalidateTimeout)
	defer cancel()

	fresh, err := e.scraper.ScrapeURL(revalidateCtx, targetURL)
	if err != nil || fresh == nil {
		return cached
	}
	if fresh.NotModified {
		cached.FetchedAt = time.Now()
		e.storeScrapeResult(ctx, cacheKey, cached)
		return cached
	}
	e.storeScrapeResult(ctx, cacheKey, fresh)
	return fresh
}

// hasRevalidationValidators reports whether a cached result carries the response
// validators a conditional request needs to be worth sending.
func hasRevalidationValidators(result *models.ScrapeResult) bool {
	return strings.TrimSpace(result.ETag) != "" || strings.TrimSpace(result.LastModified) != ""
}

// storeScrapeResult caches a successful page.
func (e *Engine) storeScrapeResult(ctx context.Context, cacheKey string, result *models.ScrapeResult) {
	cache.SetJSON(ctx, e.cache, cacheKey, cachedScrapeEntry{
		Kind:     cachedKindResult,
		Result:   result,
		StoredAt: time.Now(),
	}, scrapeTTL)
}

// storeScrapeFailure caches a failed attempt so a repeat question is cheap.
func (e *Engine) storeScrapeFailure(ctx context.Context, cacheKey string, cause error) {
	cache.SetJSON(ctx, e.cache, cacheKey, cachedScrapeEntry{
		Kind:     cachedKindFailure,
		Error:    cause.Error(),
		StoredAt: time.Now(),
	}, negativeScrapeTTL)
}

// exportTimeout bounds an export. Exports run on a detached context, so a run that
// just hit its research deadline can still save its results.
const exportTimeout = 90 * time.Second

// ExportCompany stores the finished chart as output artifacts, returning one
// artifact per attempted file (a failed PDF does not hide the Markdown).
func (e *Engine) ExportCompany(ctx context.Context, chart *models.OrgChart) []models.ExportArtifact {
	if e == nil || e.exporter == nil || chart == nil || !e.exporter.Enabled() {
		return nil
	}
	exportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), exportTimeout)
	defer cancel()
	return e.exporter.ExportCompany(exportCtx, chart)
}

// ExportPerson stores the finished dossier as output artifacts.
func (e *Engine) ExportPerson(ctx context.Context, profile *models.PersonProfile) []models.ExportArtifact {
	if e == nil || e.exporter == nil || profile == nil || !e.exporter.Enabled() {
		return nil
	}
	exportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), exportTimeout)
	defer cancel()
	return e.exporter.ExportPerson(exportCtx, profile)
}

// exportSummary describes a run's artifacts for the agent log, and returns "" when
// there is nothing to report.
func exportSummary(artifacts []models.ExportArtifact) string {
	if len(artifacts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.Failed() {
			parts = append(parts, fmt.Sprintf("%s failed (%s)", artifact.Format, artifact.Error))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s: %s", artifact.Format, artifact.SizeLabel(), artifact.URI))
	}
	return "Saved run output — " + strings.Join(parts, "; ")
}

// hardScrapeCacheKey scopes hard-URL ladder attempts to their own cache mode,
// so a failed escalation can never mask (or be masked by) the normal HTTP or
// per-run browser-mode entries for the same URL.
func hardScrapeCacheKey(targetURL string) string {
	return cache.HashKey("scrape", targetURL+"|mode=hard")
}

// ScrapeHardCached runs the hard-URL ladder with TTL caching. A recovered result
// is cached for scrapeTTL; a ladder that ends still blocked or thin is
// negative-cached briefly so QC follow-up iterations do not pay to repeat it.
// Cancellations are never cached.
//
// A recovered ladder result is served until it expires rather than revalidated:
// the entry already holds the escalated copy, and re-asking a challenged page
// whether it changed would only repeat the escalation.
func (e *Engine) ScrapeHardCached(ctx context.Context, targetURL string, opts tools.HardScrapeOptions) (*models.ScrapeResult, error) {
	cacheKey := hardScrapeCacheKey(targetURL)
	var entry cachedScrapeEntry
	if cache.GetJSON(ctx, e.cache, cacheKey, &entry) {
		switch entry.Kind {
		case cachedKindFailure:
			if entry.Error != "" {
				return nil, errors.New(entry.Error)
			}
			return nil, fmt.Errorf("cached hard-URL scrape failure for %s", targetURL)
		case cachedKindResult:
			if entry.Result != nil {
				return entry.Result, nil
			}
		}
	}

	hard, ok := e.scraper.(HardURLScraper)
	if !ok {
		return nil, fmt.Errorf("%w: scraper does not support the escalation ladder", tools.ErrHardLadderUnavailable)
	}
	res, err := hard.ScrapeHard(ctx, targetURL, opts)
	if err != nil {
		if ctx.Err() == nil {
			e.storeScrapeFailure(ctx, cacheKey, err)
		}
		return nil, err
	}
	e.storeScrapeResult(ctx, cacheKey, res)
	return res, nil
}

// Emit sends an event to the provided event channel if available. Events are
// sent non-blocking: if a consumer is not keeping up, the event is dropped and
// logged, but the research flow itself is never blocked by a slow consumer.
func Emit(ch chan<- models.AgentEvent, event models.AgentEvent) {
	event.Timestamp = time.Now()
	if event.ToolInput != "" {
		log.Printf("[AGENT][%s][step %d] %s: %s -> %s", event.SessionID, event.Step, event.Type, event.Content, event.ToolInput)
	} else {
		log.Printf("[AGENT][%s][step %d] %s: %s", event.SessionID, event.Step, event.Type, event.Content)
	}
	if ch == nil {
		return
	}
	select {
	case ch <- event:
	default:
		log.Printf("[WARN] agent event dropped (channel full): %s %s", event.Type, event.Content)
	}
}

// SynthesizeJSON instructs Gemini to generate clean structured JSON given aggregated notes.
func (e *Engine) SynthesizeJSON(ctx context.Context, systemPrompt, contextData string, target any) error {
	prompt := fmt.Sprintf("Context Data gathered from research:\n\n%s\n\nGenerate the complete structured output according to the system specification.", contextData)
	return e.GenerateStructured(ctx, systemPrompt, prompt, target)
}

// GenerateStructured delegates a structured JSON request to the configured LLM.
// It is used by planning and ranking stages as well as final synthesis.
func (e *Engine) GenerateStructured(ctx context.Context, systemPrompt, userPrompt string, target any) error {
	if e.llmClient == nil {
		return fmt.Errorf("LLM client is not configured")
	}
	return e.llmClient.GenerateStructured(ctx, systemPrompt, userPrompt, target)
}

func (e *Engine) ExtractImageText(ctx context.Context, images []models.Image) map[string]string {
	textByURL, _ := e.ExtractImageTextDetailed(ctx, images)
	return textByURL
}

func (e *Engine) ExtractImageTextDetailed(ctx context.Context, images []models.Image) (map[string]string, error) {
	extractor, ok := e.llmClient.(imageTextExtractor)
	if !ok || len(images) == 0 {
		return nil, fmt.Errorf("OCR extractor is unavailable")
	}
	textByURL, err := extractor.ExtractImageText(ctx, images)
	if err != nil {
		log.Printf("[WARN] image OCR skipped: %v", err)
		return nil, err
	}
	return textByURL, nil
}

// FormatSearchSnippets prepares a clean markdown summary of search results for LLM consumption.
func FormatSearchSnippets(results []models.SearchResult) string {
	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. [%s](%s)\n   %s\n", i+1, r.Title, r.Link, r.Snippet))
	}
	return sb.String()
}
