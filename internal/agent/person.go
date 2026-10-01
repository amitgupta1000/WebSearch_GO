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
	// The identity gate is what stops a same-named person at another employer from reaching
	// the dossier: the company and location the operator confirmed are acceptance rules, not
	// just search terms. Everything the run collects — snippets and fetched pages — is held
	// to them, and every exclusion is listed in the dossier's identity check.
	gate := newPersonGate(query)
	timer := newPhaseTimer()
	var scrapeReport []models.ScrapeReportEntry
	var searchQueries []string
	var gateExcluded []models.SearchResult
	gateKept, gateNameOnly := 0, 0
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

	searchQueries = append(searchQueries, query1)
	finishPrimarySearch := timer.start("primary search")
	searchResults, err := pa.engine.SearchCached(ctx, query1, pa.engine.MaxSearchResults())
	if err != nil {
		finishPrimarySearch("failed")
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventError,
			Content:   fmt.Sprintf("Search error: %v", err),
		})
		return nil, err
	}
	finishPrimarySearch(fmt.Sprintf("%d hits", len(searchResults)))

	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventToolResult,
		Step:      3,
		ToolName:  "serper_search",
		Content:   fmt.Sprintf("Found %d search matches across the web.", len(searchResults)),
	})

	// Snippets are free-floating assertions, so the gate holds them harder than it holds a
	// fetched page: a snippet must name the confirmed company or location to enter the
	// dossier. That is the rule that stops a same-named person's snippet from being
	// synthesised into this person's resume.
	var gatheredContext strings.Builder
	keptPrimary, primaryExcluded := gate.filterSnippets(searchResults)
	gateExcluded = append(gateExcluded, primaryExcluded...)
	gateKept += len(keptPrimary)
	gatheredContext.WriteString("=== PRIMARY SEARCH SNIPPETS ===\n")
	gatheredContext.WriteString(FormatSearchSnippets(keptPrimary))

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
		// The gate decides which pages are worth a fetch at all: a page whose headline
		// states a different employer for the name is a different person, and a page that
		// never names the person is noise — neither is spent a scrape on. A page that names
		// the person and states no employer is still fetched, because a career history
		// lists past employers in its body, but it is counted as name-only.
		if verdict := gate.classify(sr.Link, sr.Title, sr.Snippet); !verdict.usable() {
			gateExcluded = append(gateExcluded, sr)
			continue
		}
		if isProfileSourceURL(sr.Link) || len(fetchTargets) == 0 {
			fetchTargets = append(fetchTargets, sr)
		}
	}

	// Professional-profile walls are the norm in person mode, so a profile URL is fetched
	// through the session first when the operator has supplied one: a cookie-authenticated
	// plain fetch is the cheapest request there is, and on a live profile it is the one that
	// actually returned the page. Only when that fails does the URL go to the bounded
	// hard-URL ladder; with no session at all it goes straight there, exactly as before,
	// because an unauthenticated fetch of an authwall buys nothing.
	// Everything that is not a profile is fetched in parallel below: the fetches are
	// latency-bound, so doing them one at a time only adds the round trips together.
	parallelTargets := make([]models.SearchResult, 0, len(fetchTargets))
	fetchURLs := make([]string, 0, len(fetchTargets))
	finishFetches := timer.start("profile fetches")
	scrapeReportEntry := func(sr models.SearchResult, status, reason, content string) {
		entry := models.ScrapeReportEntry{URL: sr.Link, SourceType: personReportSourceType(sr.Link), Status: status, Reason: reason}
		if content != "" {
			entry.WordCount = len(strings.Fields(content))
		}
		scrapeReport = append(scrapeReport, entry)
	}
	for _, sr := range fetchTargets {
		if isProfessionalProfileURL(sr.Link) {
			if pa.engine.SessionCookiesFor(sr.Link) {
				Emit(eventCh, models.AgentEvent{
					SessionID: sessionID,
					Type:      models.EventToolCall,
					Step:      4 + scrapeCount,
					ToolName:  "web_scraper",
					ToolInput: sr.Link,
					Content:   "Fetching the profile with the exported session (no render)...",
				})
				if result := fetchProfileWithSession(ctx, pa.engine, sr.Link); result != nil {
					gatheredScrapes.WriteString(fmt.Sprintf("\n--- Source: %s (%s) ---\n%s\n", sr.Title, sr.Link, result.Content))
					sources = append(sources, sr.Link)
					scrapeCount++
					gateKept++
					scrapeReportEntry(sr, "ok", "cookie-authenticated session fetch", result.Content)
					continue
				}
				scrapeReportEntry(sr, "failed", "session fetch returned no usable page", "")
			}
			if handler.available() {
				outcome := handler.handle(ctx, sessionID, sr.Link, "professional profile", 4+scrapeCount, eventCh, tools.HardScrapeOptions{})
				if outcome.Status == "recovered" && outcome.Result != nil && len(outcome.Result.Content) > minScrapeContentLen {
					gatheredScrapes.WriteString(fmt.Sprintf("\n--- Source: %s (%s) ---\n%s\n", sr.Title, sr.Link, outcome.Result.Content))
					sources = append(sources, sr.Link)
					scrapeCount++
					gateKept++
					scrapeReportEntry(sr, "ok", "recovered by the hard-URL ladder", outcome.Result.Content)
				} else {
					scrapeReportEntry(sr, "failed", "hard-URL ladder: "+outcome.Status, "")
				}
				continue
			}
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
			reason := "no usable content"
			if scrapeErr != nil {
				reason = scrapeErr.Error()
			} else if result != nil && result.Blocked {
				reason = "blocked by the site"
			}
			scrapeReportEntry(sr, "failed", reason, "")
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
		// The fetched body is judged once more now that it can be read in full: a page that
		// never states an employer is admitted but counted as name-only, so the dossier's
		// identity check shows how much of the evidence rests on the name alone.
		if verdict := gate.classify(sr.Link, sr.Title, result.Content); verdict.Status == personVerdictNameOnly {
			gateNameOnly++
		} else {
			gateKept++
		}
		scrapeReportEntry(sr, "ok", "", result.Content)
		if sr.Link == confirmedSeed {
			seedFetched = true
		}
	}
	finishFetches(fmt.Sprintf("%d page(s) read", scrapeCount))
	gatheredContext.WriteString(gatheredScrapes.String())
	// A confirmed profile that could not be fetched still belongs in the dossier's sources:
	// its snippet was used as evidence, and the operator pointed at it.
	if confirmedSeed != "" && !seedFetched {
		sources = append(sources, confirmedSeed)
	}

	// Step 2: Query for conference talks, articles, or career milestones
	query2 := fmt.Sprintf("%s speaker interview articles career education", query1)
	searchQueries = append(searchQueries, query2)
	finishCareerSearch := timer.start("career + directory search")
	secResults, err := pa.engine.SearchCached(ctx, query2, pa.engine.MaxSearchResults())
	if err == nil {
		keptSec, secExcluded := gate.filterSnippets(secResults)
		gateExcluded = append(gateExcluded, secExcluded...)
		gateKept += len(keptSec)
		gatheredContext.WriteString("\n=== ADDITIONAL CAREER SNIPPETS ===\n")
		gatheredContext.WriteString(FormatSearchSnippets(keptSec))
		for _, r := range keptSec {
			sources = append(sources, r.Link)
		}
	}

	// Step 3: the aggregator domains that publish a title and employer for a named person.
	// This is the replacement for a LinkedIn profile, which cannot be read without a session:
	// the snippets are labelled as directory claims rather than profile facts, because that is
	// what they are, and the pages are usually paywalled for anything more.
	if aggregatorQuery := sourceSiteQuery(query1, alternativeProfileSources); aggregatorQuery != "" {
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventToolCall,
			Step:      6,
			ToolName:  "serper_search",
			ToolInput: aggregatorQuery,
			Content:   "Searching the people-data aggregators for a title and employer...",
		})
		searchQueries = append(searchQueries, aggregatorQuery)
		if aggResults, aggErr := pa.engine.SearchCached(ctx, aggregatorQuery, pa.engine.MaxSearchResults()); aggErr == nil && len(aggResults) > 0 {
			keptAgg, aggExcluded := gate.filterSnippets(aggResults)
			gateExcluded = append(gateExcluded, aggExcluded...)
			gateKept += len(keptAgg)
			gatheredContext.WriteString("\n=== DIRECTORY CLAIMS (aggregator snippets: a stated title and employer, not verified employment) ===\n")
			gatheredContext.WriteString(FormatSearchSnippets(keptAgg))
			for _, r := range keptAgg {
				sources = append(sources, r.Link)
			}
		}
	}
	finishCareerSearch("career and directory snippets filtered")

	// The identity check is reported the way the entity gate is in company mode: what was kept,
	// what was excluded, and why — so a same-named person's absence from the dossier is
	// visible rather than silent.
	excludedRows := gate.exclusionsFrom(dedupeResults(gateExcluded))
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventThinking,
		Step:      7,
		ToolName:  "person_gate",
		Content: fmt.Sprintf("Identity check: %d source(s) kept, %d name-only, %d excluded as same-named or unrelated.",
			gateKept, gateNameOnly, len(excludedRows)),
	})

	// Step 3: Structured synthesis with Gemini 2.5 Flash Lite
	finishSynthesis := timer.start("synthesis")
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

	finishSynthesis("dossier synthesised")
	profile.Sources = sources
	profile.PersonGate = &models.PersonGate{
		Name:     gate.name,
		Company:  gate.company,
		Kept:     gateKept,
		NameOnly: gateNameOnly,
		Dropped:  len(excludedRows),
		Excluded: excludedRows,
	}
	profile.ScrapeReport = scrapeReport
	profile.Timings = timer.results()
	profile.SearchQueries = searchQueries
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
