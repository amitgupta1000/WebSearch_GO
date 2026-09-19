package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/llm"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// Engine orchestrates multi-turn ReAct loops, coordinating tools and LLM responses.
type Engine struct {
	llmClient    *llm.GeminiClient
	serperClient *tools.SerperClient
	scraper      *tools.Scraper
	cache        *cache.MemoryCache
}

// NewEngine initializes the agentic ReAct engine.
func NewEngine(
	llmClient *llm.GeminiClient,
	serperClient *tools.SerperClient,
	scraper *tools.Scraper,
	cache *cache.MemoryCache,
) *Engine {
	return &Engine{
		llmClient:    llmClient,
		serperClient: serperClient,
		scraper:      scraper,
		cache:        cache,
	}
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

	e.cache.Set(cacheKey, results, 48*time.Hour)
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

	e.cache.Set(cacheKey, res, 7*24*time.Hour)
	return res, nil
}

// Emit sends an event to the provided event channel if available.
func Emit(ch chan<- models.AgentEvent, event models.AgentEvent) {
	if ch != nil {
		event.Timestamp = time.Now()
		select {
		case ch <- event:
		default:
		}
	}
}

// SynthesizeJSON instructs Gemini to generate clean structured JSON given aggregated notes.
func (e *Engine) SynthesizeJSON(ctx context.Context, systemPrompt, contextData string, target any) error {
	prompt := fmt.Sprintf("Context Data gathered from research:\n\n%s\n\nGenerate the complete structured output according to the system specification.", contextData)
	return e.llmClient.GenerateStructured(ctx, systemPrompt, prompt, target)
}

// FormatSearchSnippets prepares a clean markdown summary of search results for LLM consumption.
func FormatSearchSnippets(results []models.SearchResult) string {
	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. [%s](%s)\n   %s\n", i+1, r.Title, r.Link, r.Snippet))
	}
	return sb.String()
}
