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
	personName string,
	companyName string,
	location string,
	eventCh chan<- models.AgentEvent,
) (*models.PersonProfile, error) {
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

	var sources []string
	scrapeCount := 0
	handler := newHardURLHandler(pa.engine)
	for _, sr := range searchResults {
		if scrapeCount >= pa.engine.MaxScrapes() {
			break
		}
		// Prioritize professional profiles, bio pages, interviews, personal sites, github, wiki
		if strings.Contains(sr.Link, "linkedin.") || strings.Contains(sr.Link, "wikipedia") || strings.Contains(sr.Link, "about") || strings.Contains(sr.Link, "github") || scrapeCount == 0 {
			if strings.Contains(sr.Link, "linkedin.") && handler.available() {
				// Profile walls are the norm, so professional profiles go
				// straight to the bounded handler instead of feeding the auth
				// wall to the normal scrape path.
				outcome := handler.handle(ctx, sessionID, sr.Link, "professional profile", 4+scrapeCount, eventCh, tools.HardScrapeOptions{})
				if outcome.Status == "recovered" && outcome.Result != nil && len(outcome.Result.Content) > minScrapeContentLen {
					gatheredContext.WriteString(fmt.Sprintf("\n--- Source: %s (%s) ---\n%s\n", sr.Title, sr.Link, outcome.Result.Content))
					sources = append(sources, sr.Link)
					scrapeCount++
				}
				continue
			}

			Emit(eventCh, models.AgentEvent{
				SessionID: sessionID,
				Type:      models.EventToolCall,
				Step:      4 + scrapeCount,
				ToolName:  "web_scraper",
				ToolInput: sr.Link,
				Content:   fmt.Sprintf("Extracting details from %s...", sr.Link),
			})

			scrapeRes, err := pa.engine.ScrapeCached(ctx, sr.Link)
			// A challenge page used to slip in here as "content" whenever its
			// interstitial text exceeded the minimum length; blocked results
			// now get one bounded handler attempt instead.
			usable := err == nil && scrapeRes != nil && !scrapeRes.Blocked && len(scrapeRes.Content) > minScrapeContentLen
			if !usable && err == nil && scrapeRes != nil && scrapeRes.Blocked && handler.available() {
				outcome := handler.handle(ctx, sessionID, sr.Link, "blocked page", 4+scrapeCount, eventCh, tools.HardScrapeOptions{AlreadyRendered: scrapeRes.FallbackStatus == "blocked"})
				if outcome.Status == "recovered" && outcome.Result != nil && len(outcome.Result.Content) > minScrapeContentLen {
					scrapeRes, usable = outcome.Result, true
				}
			}
			if usable {
				gatheredContext.WriteString(fmt.Sprintf("\n--- Source: %s (%s) ---\n%s\n", sr.Title, sr.Link, scrapeRes.Content))
				sources = append(sources, sr.Link)
				scrapeCount++
			}
		}
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

	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventComplete,
		Step:      8,
		Content:   fmt.Sprintf("Completed dossier for %s (%s at %s).", profile.FullName, profile.CurrentTitle, profile.CurrentCompany),
	})

	return &profile, nil
}
