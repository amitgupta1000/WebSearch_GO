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

	// The plan asks the confirmed identity from several directions instead of one broad
	// question and two afterthoughts: profile, role, appointments, career history, and the
	// people-data aggregators. Every query is anchored on the confirmed name and company —
	// an unanchored query is how another person's pages used to reach the synthesiser.
	plan := limitPersonPlan(buildPersonPlan(personName, companyName, location))
	finishSearches := timer.start("planned searches")

	// The profile the operator confirmed the identity from is evidence about the right
	// person even when its fetch is walled, so its snippet is always kept — and the page is
	// read first, because that is the page the identity was confirmed on. It leads the
	// context: everything after it is judged against it.
	confirmedSeed := strings.TrimSpace(query.SeedURL)
	var gatheredContext strings.Builder
	var personalizationContext strings.Builder
	if confirmedSeed != "" {
		gatheredContext.WriteString("=== CONFIRMED IDENTITY (chosen by the operator) ===\n")
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

	// planResults carries every query's gate-passing snippets in plan order, so the fetch
	// phase can pick pages from all of the searches rather than the first one's. searchTrace
	// records what each search returned — kept and excluded, before cross-query dedupe —
	// behind the Search results tab and the searches.json artifact.
	type planSearch struct {
		entry   personPlanEntry
		results []models.SearchResult
	}
	planResults := make([]planSearch, 0, len(plan))
	searchTrace := &models.SearchTrace{Queries: make([]models.QueryTrace, 0, len(plan)+1)}
	if confirmedSeed != "" {
		searchTrace.Queries = append(searchTrace.Queries, models.QueryTrace{
			Query:   confirmedSeed,
			Purpose: "confirmed profile (chosen by the operator)",
			Kept: []models.SearchResult{{
				Title:   firstNonEmpty(query.SeedTitle, "Confirmed profile"),
				Link:    confirmedSeed,
				Snippet: query.SeedSnippet,
			}},
		})
	}
	seenSnippet := make(map[string]bool, 32)
	successfulSearches := 0
	for _, entry := range plan {
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventToolCall,
			Step:      2,
			ToolName:  "serper_search",
			ToolInput: entry.query,
			Content:   fmt.Sprintf("Searching (%s): %s", entry.section, entry.query),
		})
		results, err := pa.engine.SearchCached(ctx, entry.query, pa.engine.MaxSearchResults())
		if err != nil {
			// One failed search is not a failed run: the plan has more directions, and the
			// run only fails when nothing at all came back.
			continue
		}
		successfulSearches++
		searchQueries = append(searchQueries, entry.query)
		// Snippets are free-floating assertions, so the gate holds them harder than it
		// holds a fetched page: a snippet must name the confirmed company or location to
		// enter the dossier. That is the rule that stops a same-named person's snippet
		// from being synthesised into this person's resume.
		var kept, excluded []models.SearchResult
		if entry.personalization {
			kept, excluded = gate.filterPersonalization(results)
		} else {
			kept, excluded = gate.filterSnippets(results)
		}
		if !entry.personalization {
			gateExcluded = append(gateExcluded, excluded...)
		}
		searchTrace.Queries = append(searchTrace.Queries, models.QueryTrace{
			Query:    entry.query,
			Purpose:  entry.section,
			Kept:     kept,
			Excluded: excluded,
		})
		fresh := make([]models.SearchResult, 0, len(kept))
		for _, result := range kept {
			key := strings.ToLower(result.Link)
			if seenSnippet[key] {
				// The same hit answers several plan questions; the context lists it once.
				continue
			}
			seenSnippet[key] = true
			fresh = append(fresh, result)
		}
		gateKept += len(fresh)
		if len(fresh) > 0 {
			target := &gatheredContext
			if entry.personalization {
				target = &personalizationContext
			}
			fmt.Fprintf(target, "\n=== %s ===\n", entry.section)
			target.WriteString(FormatSearchSnippets(fresh))
		}
		planResults = append(planResults, planSearch{entry: entry, results: fresh})
	}
	finishSearches(fmt.Sprintf("%d/%d search(es) returned hits", successfulSearches, len(plan)))
	if successfulSearches == 0 {
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventError,
			Content:   "Every planned search failed.",
		})
		return nil, fmt.Errorf("every planned search failed")
	}
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventToolResult,
		Step:      3,
		ToolName:  "serper_search",
		Content:   fmt.Sprintf("Planned searches: %d/%d succeeded; %d source(s) passed the identity check.", successfulSearches, len(plan), gateKept),
	})

	var sources []string
	scrapeCount := 0
	var gatheredScrapes strings.Builder
	handler := newHardURLHandler(pa.engine)

	// Choose the pages worth fetching first, then fetch the rest in parallel: the
	// choice is cheap and the fetches are latency-bound, so doing them one at a
	// time only adds the network round trips together. Pages are picked from every planned
	// search's gate-passing results, which is what turns the extra searches into extra
	// evidence rather than extra snippets.
	fetchTargets := make([]models.SearchResult, 0, pa.engine.MaxScrapes())
	seedFetched := false
	if confirmedSeed != "" {
		fetchTargets = append(fetchTargets, models.SearchResult{
			Title:   firstNonEmpty(query.SeedTitle, "Confirmed profile"),
			Link:    confirmedSeed,
			Snippet: query.SeedSnippet,
		})
	}
	seenCandidate := make(map[string]bool, len(fetchTargets))
	for _, target := range fetchTargets {
		seenCandidate[strings.ToLower(target.Link)] = true
	}
	addCandidate := func(sr models.SearchResult, verdict personVerdict) {
		key := strings.ToLower(sr.Link)
		if key == "" || seenCandidate[key] {
			return
		}
		// The gate decides which pages are worth a fetch at all: a page whose headline
		// states a different employer for the name is a different person, and a page that
		// never names the person is noise — neither is spent a scrape on. A page that names
		// the person and states no employer is still fetched, because a career history
		// lists past employers in its body, but it is counted as name-only.
		if !verdict.usable() {
			gateExcluded = append(gateExcluded, sr)
			return
		}
		seenCandidate[key] = true
		fetchTargets = append(fetchTargets, sr)
	}
	// Professional profiles first: they are what a session can actually open.
	for _, batch := range planResults {
		if batch.entry.snippetsOnly {
			continue
		}
		for _, sr := range batch.results {
			if len(fetchTargets) >= pa.engine.MaxScrapes() {
				break
			}
			if isProfileSourceURL(sr.Link) {
				addCandidate(sr, gate.classify(sr.Link, sr.Title, sr.Snippet))
			}
		}
	}
	// Then any page the gate matched — it names the person and the confirmed employer or
	// place, which makes it worth reading even when it is not a profile host. This is the
	// second way the extra searches pay: previously only the first non-profile hit was
	// ever fetched.
	for _, batch := range planResults {
		if batch.entry.snippetsOnly {
			continue
		}
		for _, sr := range batch.results {
			if len(fetchTargets) >= pa.engine.MaxScrapes() {
				break
			}
			if seenCandidate[strings.ToLower(sr.Link)] {
				continue
			}
			if verdict := gate.classify(sr.Link, sr.Title, sr.Snippet); verdict.Status == personVerdictMatched {
				addCandidate(sr, verdict)
			}
		}
	}
	// Last resort: when nothing matched, the first usable hit is still read rather than
	// returning a dossier with no fetched page at all.
	if len(fetchTargets) == 0 {
	pickFirstUsable:
		for _, batch := range planResults {
			if batch.entry.snippetsOnly {
				continue
			}
			for _, sr := range batch.results {
				if verdict := gate.classify(sr.Link, sr.Title, sr.Snippet); verdict.usable() {
					addCandidate(sr, verdict)
					break pickFirstUsable
				}
			}
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
	if personalizationContext.Len() > 0 {
		gatheredContext.WriteString("\n=== PUBLIC PERSONALIZATION CONTEXT (not identity or employment proof) ===\n")
		gatheredContext.WriteString(personalizationContext.String())
	}
	// A confirmed profile that could not be fetched still belongs in the dossier's sources:
	// its snippet was used as evidence, and the operator pointed at it.
	if confirmedSeed != "" && !seedFetched {
		sources = append(sources, confirmedSeed)
	}

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

	identityContext := strings.TrimSpace(strings.Join([]string{companyName, location}, ", "))
	identity := personName
	identityRule := "Use ONLY evidence tied to this person: the confirmed profile, or a source that names the person together with an employer the evidence states. A row the evidence does not tie to this person will be dropped by the post-synthesis check."
	if companyName != "" {
		identity = fmt.Sprintf("%s (%s)", personName, identityContext)
		identityRule = fmt.Sprintf("Use ONLY evidence tied to this person: the confirmed profile, or a source that names the person together with %q. A row the evidence does not tie to this person will be dropped by the post-synthesis check.", companyName)
	}

	systemPrompt := `You are an elite talent intelligence researcher.
This dossier is about ONE person, confirmed by the operator: ` + identity + `.
The evidence may contain several people who share this name. Their careers belong to a different dossier. ` + identityRule + `
Synthesize the provided web search and scraped profiles into a PersonProfile JSON:
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
      "source": "string (the URL from the evidence that supports this row)",
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
  "public_interests": ["string"],
  "recent_themes": ["string"],
  "personalization_evidence": [
    {
      "platform": "facebook | instagram | x",
      "url": "string",
      "summary": "string",
      "identity_basis": "company/location match | name-only possible account",
      "confidence": 0.0
    }
  ],
  "confidence_score": 0.95
}
Rules:
- Set "source" on every experience row to the URL in the evidence that supports it.
- If the evidence does not support a field or a section, return it empty — an empty dossier is better than an invented or blended one.
- Never mix another person's role, employer, education, or skills into this dossier.
- Public personalization is optional context only. Never use social evidence to establish identity,
  current employment, title, location, or other sensitive personal attributes.
- Include public interests or recurring themes only when directly supported by the labelled
  personalization context. Mark name-only social matches as possible accounts with low confidence.`

	var profile models.PersonProfile
	if err := pa.engine.SynthesizeJSON(ctx, systemPrompt, gatheredContext.String(), &profile); err != nil {
		return nil, fmt.Errorf("person synthesis failed: %w", err)
	}

	finishSynthesis("dossier synthesised")

	// QC holds the model's output to the same identity the gate held the evidence to: a
	// synthesised row whose employer is not the confirmed one survives only when a reverse
	// lookup ties the two, and the headline employer is the operator's confirmed one.
	finishQC := timer.start("dossier QC")
	qcExcluded, qcLookups := pa.qcPersonProfile(ctx, &profile, gate)
	excludedRows = append(excludedRows, qcExcluded...)
	finishQC(fmt.Sprintf("%d row(s) kept, %d dropped, %d lookup(s)", len(profile.Experiences), len(qcExcluded), qcLookups))
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventThinking,
		Step:      8,
		ToolName:  "person_qc",
		Content: fmt.Sprintf("Dossier QC: %d experience row(s) kept, %d dropped, %d reverse lookup(s) spent, %d overlapping timeline(s) flagged.",
			len(profile.Experiences), len(qcExcluded), qcLookups, countFlaggedRows(profile.Experiences)),
	})

	// The multilateral view: a person search answers with several people who share the name,
	// so the dossier also presents the other identities' most relevant snippets — grouped by
	// the employer each source states — instead of discarding them.
	profile.SameNamed = gate.sameNamedGroups(dedupeResults(gateExcluded))
	profile.SearchTrace = searchTrace

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
