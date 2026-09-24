package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// PersonAgent conducts disambiguation, online footprint analysis, and dossier compilation.
type PersonAgent struct {
	engine *Engine
}

// NewPersonAgent initializes the person research specialist agent.
func NewPersonAgent(engine *Engine) *PersonAgent {
	return &PersonAgent{engine: engine}
}

// ResearchPerson conducts deep multi-source research to construct a professional dossier.
func (pa *PersonAgent) ResearchPerson(
	ctx context.Context,
	sessionID string,
	query models.PersonQuery,
	eventCh chan<- models.AgentEvent,
) (*models.PersonProfile, error) {
	personName := strings.TrimSpace(query.Name)
	companyName := strings.TrimSpace(query.Company)
	location := strings.TrimSpace(query.Location)
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventThinking,
		Step:      1,
		Content:   fmt.Sprintf("Initiating background dossier compilation for '%s'...", personName),
	})

	// Construct targeted search queries with disambiguation keywords
	var queryTerms []string
	queryTerms = append(queryTerms, personName)
	if companyName != "" {
		queryTerms = append(queryTerms, companyName)
	}
	if location != "" {
		queryTerms = append(queryTerms, location)
	}
	query1 := strings.Join(queryTerms, " ")

	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventToolCall,
		Step:      2,
		ToolName:  "serper_search",
		ToolInput: query1,
		Content:   fmt.Sprintf("Searching for professional profile: %s", query1),
	})

	searchResults, err := pa.engine.SearchCached(ctx, query1, pa.engine.MaxSearchResults())
	if err != nil {
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventError,
			Content:   fmt.Sprintf("Search error: %v", err),
		})
		return nil, err
	}

	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventToolResult,
		Step:      3,
		ToolName:  "serper_search",
		Content:   fmt.Sprintf("Found %d search matches across the web.", len(searchResults)),
	})

	var gatheredContext strings.Builder
	gatheredContext.WriteString("=== PRIMARY SEARCH SNIPPETS ===\n")
	gatheredContext.WriteString(FormatSearchSnippets(searchResults))

	// The profile the operator confirmed the identity from is evidence about the right
	// person even when its fetch is walled, so its snippet is always kept — and the page is
	// read first, because that is the page the identity was confirmed on.
	confirmedSeed := strings.TrimSpace(query.SeedURL)
	if confirmedSeed != "" {
		gatheredContext.WriteString("\n=== CONFIRMED IDENTITY (chosen by the operator) ===\n")
		gatheredContext.WriteString(fmt.Sprintf("%s — %s\n%s\n", firstNonEmpty(query.SeedTitle, "Confirmed profile"), confirmedSeed, query.SeedSnippet))
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventToolCall,
			Step:      3,
			ToolName:  "confirmed_profile",
			ToolInput: confirmedSeed,
			Content:   "Reading the profile the identity was confirmed from...",
		})
	}

	var sources []string
	scrapeCount := 0
	var gatheredScrapes strings.Builder
	handler := newHardURLHandler(pa.engine)

	// Choose the pages worth fetching first, then fetch the rest in parallel: the
	// choice is cheap and the fetches are latency-bound, so doing them one at a
	// time only adds the network round trips together.
	fetchTargets := make([]models.SearchResult, 0, pa.engine.MaxScrapes())
	seedFetched := false
	if confirmedSeed != "" {
		fetchTargets = append(fetchTargets, models.SearchResult{
			Title:   firstNonEmpty(query.SeedTitle, "Confirmed profile"),
			Link:    confirmedSeed,
			Snippet: query.SeedSnippet,
		})
	}
	for _, sr := range searchResults {
		if len(fetchTargets) >= pa.engine.MaxScrapes() {
			break
		}
		// Prioritize professional profiles, bio pages, interviews, personal sites, github, wiki
		if strings.Contains(sr.Link, "linkedin.") || strings.Contains(sr.Link, "wikipedia") || strings.Contains(sr.Link, "about") || strings.Contains(sr.Link, "github") || len(fetchTargets) == 0 {
			fetchTargets = append(fetchTargets, sr)
		}
	}

	// Professional-profile walls are the norm in person mode, so those URLs go
	// straight to the bounded hard-URL handler rather than feeding an auth wall to
	// the normal scrape path. Everything else is fetched in parallel below.
	parallelTargets := make([]models.SearchResult, 0, len(fetchTargets))
	fetchURLs := make([]string, 0, len(fetchTargets))
	for _, sr := range fetchTargets {
		if strings.Contains(sr.Link, "linkedin.") && handler.available() {
			outcome := handler.handle(ctx, sessionID, sr.Link, "professional profile", 4+scrapeCount, eventCh, tools.HardScrapeOptions{})
			if outcome.Status == "recovered" && outcome.Result != nil && len(outcome.Result.Content) > minScrapeContentLen {
				gatheredScrapes.WriteString(fmt.Sprintf("\n--- Source: %s (%s) ---\n%s\n", sr.Title, sr.Link, outcome.Result.Content))
				sources = append(sources, sr.Link)
				scrapeCount++
			}
			continue
		}
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventToolCall,
			Step:      4 + len(fetchURLs),
			ToolName:  "web_scraper",
			ToolInput: sr.Link,
			Content:   fmt.Sprintf("Extracting details from %s...", sr.Link),
		})
		parallelTargets = append(parallelTargets, sr)
		fetchURLs = append(fetchURLs, sr.Link)
	}

	outcomes := pa.engine.ScrapeCachedBatch(ctx, fetchURLs)

	seenContent := make(map[string]bool, len(parallelTargets))
	for index, sr := range parallelTargets {
		result, scrapeErr := outcomes[index].Result, outcomes[index].Err
		usable := scrapeErr == nil && result != nil && !result.Blocked && len(result.Content) > minScrapeContentLen

		// A challenge page used to slip in here as "content" whenever its
		// interstitial text exceeded the minimum length; a blocked result now gets
		// one bounded handler attempt instead.
		if !usable && scrapeErr == nil && result != nil && result.Blocked && handler.available() {
			escalated := handler.handle(ctx, sessionID, sr.Link, "blocked page", 4+scrapeCount, eventCh,
				tools.HardScrapeOptions{AlreadyRendered: result.FallbackStatus == "blocked"})
			if escalated.Status == "recovered" && escalated.Result != nil && len(escalated.Result.Content) > minScrapeContentLen {
				result, usable = escalated.Result, true
			}
		}
		if !usable {
			continue
		}
		fingerprint := contentHash(result.Content)
		if seenContent[fingerprint] {
			// The same biography republished on several domains adds nothing.
			continue
		}
		seenContent[fingerprint] = true
		gatheredScrapes.WriteString(fmt.Sprintf("\n--- Source: %s (%s) ---\n%s\n", sr.Title, sr.Link, result.Content))
		sources = append(sources, sr.Link)
		scrapeCount++
		if sr.Link == confirmedSeed {
			seedFetched = true
		}
	}
	gatheredContext.WriteString(gatheredScrapes.String())
	// A confirmed profile that could not be fetched still belongs in the dossier's sources:
	// its snippet was used as evidence, and the operator pointed at it.
	if confirmedSeed != "" && !seedFetched {
		sources = append(sources, confirmedSeed)
	}

	// Step 2: Query for conference talks, articles, or career milestones
	query2 := fmt.Sprintf("%s speaker interview articles career education", query1)
	secResults, err := pa.engine.SearchCached(ctx, query2, pa.engine.MaxSearchResults())
	if err == nil {
		gatheredContext.WriteString("\n=== ADDITIONAL CAREER SNIPPETS ===\n")
		gatheredContext.WriteString(FormatSearchSnippets(secResults))
		for _, r := range secResults {
			sources = append(sources, r.Link)
		}
	}

	// Step 3: Structured synthesis with Gemini 2.5 Flash Lite
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventThinking,
		Step:      7,
		Content:   "Synthesizing structured resume & executive summary with Gemini...",
	})

	systemPrompt := `You are an elite talent intelligence researcher.
Synthesize the provided web search and scraped profiles into a comprehensive PersonProfile JSON:
{
  "full_name": "string",
  "current_title": "string",
  "current_company": "string",
  "location": "string",
  "executive_summary": "string (multi-paragraph professional bio)",
  "experiences": [
    {
      "role": "string",
      "company": "string",
      "location": "string",
      "period": "string",
      "highlights": ["string"]
    }
  ],
  "education": [
    {
      "institution": "string",
      "degree": "string",
      "field": "string",
      "year": "string"
    }
  ],
  "key_skills": ["string"],
  "notable_projects": ["string"],
  "online_presence": {
    "platform_or_site": "url"
  },
  "confidence_score": 0.95
}`

	var profile models.PersonProfile
	if err := pa.engine.SynthesizeJSON(ctx, systemPrompt, gatheredContext.String(), &profile); err != nil {
		return nil, fmt.Errorf("person synthesis failed: %w", err)
	}

	profile.Sources = sources
	profile.LastUpdated = time.Now()
	if exports := pa.engine.ExportPerson(ctx, &profile); len(exports) > 0 {
		profile.Exports = exports
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventToolResult,
			Step:      9,
			ToolName:  "exporter",
			Content:   exportSummary(exports),
		})
	}

	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventComplete,
		Step:      8,
		Content:   fmt.Sprintf("Completed dossier for %s (%s at %s).", profile.FullName, profile.CurrentTitle, profile.CurrentCompany),
	})

	return &profile, nil
}
