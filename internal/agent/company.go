package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// CompanyAgent conducts multi-phase organizational discovery and hierarchy mapping.
type CompanyAgent struct {
	engine *Engine
}

// NewCompanyAgent initializes the company research specialist agent.
func NewCompanyAgent(engine *Engine) *CompanyAgent {
	return &CompanyAgent{engine: engine}
}

// ResearchCompany executes the end-to-end research workflow for an organization.
func (ca *CompanyAgent) ResearchCompany(
	ctx context.Context,
	sessionID string,
	companyName string,
	eventCh chan<- models.AgentEvent,
) (*models.OrgChart, error) {
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventThinking,
		Step:      1,
		Content:   fmt.Sprintf("Initiating company research for '%s'...", companyName),
	})

	// Step 1: Initial Search for Company Overview & Leadership Pages
	query1 := fmt.Sprintf("%s leadership executive team management organization", companyName)
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventToolCall,
		Step:      2,
		ToolName:  "serper_search",
		ToolInput: query1,
		Content:   "Searching for company leadership and overview pages...",
	})

	searchResults, err := ca.engine.SearchCached(ctx, query1, ca.engine.MaxSearchResults())
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
		Content:   fmt.Sprintf("Found %d search results.", len(searchResults)),
	})

	// Collect context and scrape top candidate pages
	var gatheredContext strings.Builder
	gatheredContext.WriteString("=== SEARCH SNIPPETS ===\n")
	gatheredContext.WriteString(FormatSearchSnippets(searchResults))
	gatheredContext.WriteString("\n=== SCRAPED CONTENT ===\n")

	var sources []string
	scrapeCount := 0
	for _, sr := range searchResults {
		if scrapeCount >= ca.engine.MaxScrapes() {
			break
		}
		// Skip generic aggregators if possible or pick high confidence URLs
		if strings.Contains(sr.Link, "wikipedia.org") || strings.Contains(sr.Link, "about") || strings.Contains(sr.Link, "team") || strings.Contains(sr.Link, "leadership") || scrapeCount == 0 {
			Emit(eventCh, models.AgentEvent{
				SessionID: sessionID,
				Type:      models.EventToolCall,
				Step:      4 + scrapeCount,
				ToolName:  "web_scraper",
				ToolInput: sr.Link,
				Content:   fmt.Sprintf("Scraping %s...", sr.Link),
			})

			scrapeRes, err := ca.engine.ScrapeCached(ctx, sr.Link)
			if err == nil && len(scrapeRes.Content) > minScrapeContentLen {
				gatheredContext.WriteString(fmt.Sprintf("\n--- Source: %s (%s) ---\n%s\n", sr.Title, sr.Link, scrapeRes.Content))
				sources = append(sources, sr.Link)
				scrapeCount++
			}
		}
	}

	// Step 2: Second search pass for functional departments & key executives
	query2 := fmt.Sprintf("%s VP Director Head Engineering Sales Product Finance", companyName)
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventToolCall,
		Step:      7,
		ToolName:  "serper_search",
		ToolInput: query2,
		Content:   "Searching for departmental heads and functional leads...",
	})

	secResults, err := ca.engine.SearchCached(ctx, query2, ca.engine.MaxSearchResults())
	if err == nil {
		gatheredContext.WriteString("\n=== FUNCTIONAL SEARCH SNIPPETS ===\n")
		gatheredContext.WriteString(FormatSearchSnippets(secResults))
		for _, r := range secResults {
			sources = append(sources, r.Link)
		}
	}

	// Step 3: Synthesis via Gemini 2.5 Flash Lite
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventThinking,
		Step:      8,
		Content:   "Synthesizing organization chart and department breakdown with Gemini...",
	})

	systemPrompt := `You are an expert corporate intelligence analyst.
Analyze the provided web search and scraped website contents about the company.
Build a comprehensive OrgChart JSON matching this structure:
{
  "company_name": "string",
  "domain": "string",
  "overview": "string",
  "headquarters": "string",
  "leadership": [
    {
      "name": "string",
      "title": "string",
      "department": "string",
      "location": "string",
      "bio": "string",
      "reports_to": "string"
    }
  ],
  "departments": [
    {
      "name": "string",
      "head": "string",
      "description": "string",
      "members": [
        {
          "name": "string",
          "title": "string",
          "location": "string"
        }
      ]
    }
  ],
  "locations": [
    {
      "city": "string",
      "country": "string",
      "headcount_est": "string"
    }
  ]
}`

	var chart models.OrgChart
	if err := ca.engine.SynthesizeJSON(ctx, systemPrompt, gatheredContext.String(), &chart); err != nil {
		return nil, fmt.Errorf("synthesis failed: %w", err)
	}

	chart.Sources = sources
	chart.LastUpdated = time.Now()

	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventComplete,
		Step:      9,
		Content:   fmt.Sprintf("Completed research for %s. Discovered %d executives across %d departments.", chart.CompanyName, len(chart.Leadership), len(chart.Departments)),
	})

	return &chart, nil
}
