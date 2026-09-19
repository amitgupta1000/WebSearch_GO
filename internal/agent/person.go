package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
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

	searchResults, err := pa.engine.SearchCached(ctx, query1, 8)
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
	for _, sr := range searchResults {
		if scrapeCount >= 3 {
			break
		}
		// Prioritize bio pages, interviews, personal sites, github, wiki
		if strings.Contains(sr.Link, "linkedin") || strings.Contains(sr.Link, "wikipedia") || strings.Contains(sr.Link, "about") || strings.Contains(sr.Link, "github") || scrapeCount == 0 {
			Emit(eventCh, models.AgentEvent{
				SessionID: sessionID,
				Type:      models.EventToolCall,
				Step:      4 + scrapeCount,
				ToolName:  "web_scraper",
				ToolInput: sr.Link,
				Content:   fmt.Sprintf("Extracting details from %s...", sr.Link),
			})

			scrapeRes, err := pa.engine.ScrapeCached(ctx, sr.Link)
			if err == nil && len(scrapeRes.Content) > 80 {
				gatheredContext.WriteString(fmt.Sprintf("\n--- Source: %s (%s) ---\n%s\n", sr.Title, sr.Link, scrapeRes.Content))
				sources = append(sources, sr.Link)
				scrapeCount++
			}
		}
	}

	// Step 2: Query for conference talks, articles, or career milestones
	query2 := fmt.Sprintf("%s speaker interview articles career education", query1)
	secResults, err := pa.engine.SearchCached(ctx, query2, 5)
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
