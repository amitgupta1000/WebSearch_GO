package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// Searcher performs web searches. *tools.SerperClient satisfies this interface.
type Searcher interface {
	Search(ctx context.Context, query string, maxResults int) ([]models.SearchResult, error)
}

// PageFetcher extracts readable page content. *tools.Scraper satisfies this interface.
type PageFetcher interface {
	ScrapeURL(ctx context.Context, targetURL string) (*models.ScrapeResult, error)
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
	// defaultMaxScrapesPerResearch is the default cap on page scrapes per run.
	defaultMaxScrapesPerResearch = 10
	// defaultMaxSearchResults is the default cap on search results returned per query.
	defaultMaxSearchResults = 12
	// defaultMaxSteps is the default cap on agent/tool iterations per run.
	defaultMaxSteps = 10
	// minScrapeContentLen is the minimum useful scraped content size in bytes.
	minScrapeContentLen = 80
)

// Engine orchestrates multi-turn ReAct loops, coordinating tools and LLM responses.
type Engine struct {
	llmClient             LLMClient
	serperClient          Searcher
	scraper               PageFetcher
	cache                 *cache.MemoryCache
	maxScrapesPerResearch int
	maxSearchResults      int
	maxSteps              int
}

// NewEngine initializes the agentic ReAct engine.
func NewEngine(
	llmClient LLMClient,
	serperClient Searcher,
	scraper PageFetcher,
	cache *cache.MemoryCache,
	maxScrapes int,
	maxSearchResults int,
	maxSteps int,
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
	return &Engine{
		llmClient:             llmClient,
		serperClient:          serperClient,
		scraper:               scraper,
		cache:                 cache,
		maxScrapesPerResearch: maxScrapes,
		maxSearchResults:      maxSearchResults,
		maxSteps:              maxSteps,
	}
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

// SearchCached performs a Google search with TTL caching.
func (e *Engine) SearchCached(ctx context.Context, query string, maxResults int) ([]models.SearchResult, error) {
	cacheKey := cache.HashKey("search", query)
	if val, found := e.cache.Get(cacheKey); found {
		if results, ok := val.([]models.SearchResult); ok {
			return results, nil
		}
	}

	results, err := e.serperClient.Search(ctx, query, maxResults)
	if err != nil {
		return nil, err
	}

	e.cache.Set(cacheKey, results, searchResultsTTL)
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
	e.cache.Set(cache.HashKey("search", query), results, searchResultsTTL)
	return results, nil
}

// ScrapeCached fetches and extracts webpage text with TTL caching.
func (e *Engine) ScrapeCached(ctx context.Context, targetURL string) (*models.ScrapeResult, error) {
	cacheKey := cache.HashKey("scrape", targetURL)
	if val, found := e.cache.Get(cacheKey); found {
		if res, ok := val.(*models.ScrapeResult); ok {
			return res, nil
		}
	}

	res, err := e.scraper.ScrapeURL(ctx, targetURL)
	if err != nil {
		return nil, err
	}

	e.cache.Set(cacheKey, res, scrapeTTL)
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
