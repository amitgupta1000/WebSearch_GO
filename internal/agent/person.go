package agent

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// socialISODatePattern finds an explicit calendar date written into a social title or
// snippet. Social snippets carry dates in prose far more often than in the URL, and the
// generic evidence-date pass deliberately does not treat arbitrary ISO numbers in content as
// dates; the social section wants that explicit date, so it reads it here.
var socialISODatePattern = regexp.MustCompile(`(?i)\b(20\d{2})-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])\b`)

// socialObservedAt returns the newest date a social snippet proves, or "" when it is undated.
func socialObservedAt(result models.SearchResult) string {
	if match := socialISODatePattern.FindString(result.Title + " " + result.Snippet); match != "" {
		if when, err := time.Parse("2006-01-02", match); err == nil {
			return when.Format("2006-01-02")
		}
	}
	if when, _, ok := evidenceDate(models.Evidence{
		URL: result.Link, Title: result.Title, Content: result.Snippet,
	}); ok {
		return when.Format("2006-01-02")
	}
	return ""
}

// rocketReachSource is the RocketReach surface the person agent needs: the paid
// person lookup and the free disambiguating search. *tools.RocketReachClient
// satisfies it; tests script it.
type rocketReachSource interface {
	Enabled() bool
	Lookup(ctx context.Context, name, company, linkedinURL string) (*tools.RocketReachProfile, error)
	LookupByID(ctx context.Context, id int) (*tools.RocketReachProfile, error)
	Search(ctx context.Context, conditions map[string][]string) ([]tools.RocketReachProfile, error)
}

// PersonAgent conducts disambiguation, online footprint analysis, and dossier compilation.
type PersonAgent struct {
	engine      *Engine
	rocketReach rocketReachSource
}

// NewPersonAgent initializes the person research specialist agent.
func NewPersonAgent(engine *Engine, rocketReach ...*tools.RocketReachClient) *PersonAgent {
	var client rocketReachSource
	if len(rocketReach) > 0 {
		client = rocketReach[0]
	}
	return &PersonAgent{engine: engine, rocketReach: client}
}

// ResearchPerson conducts deep multi-source research to construct a professional dossier.
// personDirectorURLPattern matches the registry director pages whose URL
// carries the person's DIN: zaubacorp's /<NAME-SLUG>-<DIN> and WeeDoo's
// /director/<NAME-SLUG>/<DIN>.
var personDirectorURLPattern = regexp.MustCompile(`(?i)(?:zaubacorp\.com/([a-z0-9-]+?)-|weedoo\.in/director/([a-z0-9-]+?)/)(\d{8})(?:[/?#]|$)`)

// harvestPersonDIN freezes the (name, DIN) pair from the run's own search
// results — kept and gate-excluded alike, because the director page that
// reveals the DIN may name a group company the gate counts as a different
// employer. The slug must name the researched person (initials-aware, so "K L
// Jain" freezes on "kanhaiya-lal-jain"), and a namesake's slug never does.
func harvestPersonDIN(results []models.SearchResult, personName string) (string, string) {
	for _, result := range results {
		match := personDirectorURLPattern.FindStringSubmatch(result.Link)
		if match == nil {
			continue
		}
		slug := match[1]
		if slug == "" {
			slug = match[2]
		}
		if !personNamesMatch(strings.ReplaceAll(slug, "-", " "), personName) {
			continue
		}
		return match[3], strings.ToUpper(slug)
	}
	// A labelled DIN in a hit's text freezes too, when it sits next to the
	// person's name.
	for _, result := range results {
		if din := labelledDINFor(result.Title+" "+result.Snippet, personName); din != "" {
			return din, ""
		}
	}
	return "", ""
}

func (pa *PersonAgent) ResearchPerson(
	ctx context.Context,
	sessionID string,
	query models.PersonQuery,
	eventCh chan<- models.AgentEvent,
) (*models.PersonProfile, error) {
	personName := strings.TrimSpace(query.Name)
	companyName := strings.TrimSpace(query.Company)
	location := strings.TrimSpace(query.Location)
	country := strings.TrimSpace(query.Country)
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
	// RocketReach is the highest-priority person source: it can provide the
	// structured profile and contact details before LinkedIn-resistant web research.
	rocketReachProfile := pa.lookupRocketReach(ctx, sessionID, query, eventCh)
	rocketReachWarning := ""
	if query.RequireRocketReach && rocketReachProfile == nil {
		rocketReachWarning = "RocketReach was unavailable or returned no matching profile. Continued with web-based research; contact details and LinkedIn enrichment may be incomplete."
	}

	// The plan asks the confirmed identity from several directions instead of one broad
	// question and two afterthoughts: profile, role, appointments, career history, and the
	// people-data aggregators. Every query is anchored on the confirmed name and company —
	// an unanchored query is how another person's pages used to reach the synthesiser.
	plan := limitPersonPlan(buildPersonPlan(personName, companyName, location, country))
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
	// behind the Search results tab and the report appendix.
	type planSearch struct {
		entry   personPlanEntry
		results []models.SearchResult
	}
	planResults := make([]planSearch, 0, len(plan))
	socialSignals := make([]models.SocialSignal, 0, personSocialSignalCap)
	seenSocial := make(map[string]bool)
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
	type searchOutcome struct {
		results []models.SearchResult
		err     error
	}
	searchOutcomes := make([]searchOutcome, len(plan))
	workers := personSearchConcurrency
	if workers > len(plan) {
		workers = len(plan)
	}
	jobs := make(chan int, len(plan))
	var waitGroup sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for index := range jobs {
				entry := plan[index]
				searchOutcomes[index].results, searchOutcomes[index].err =
					pa.engine.SearchCached(ctx, entry.query, pa.engine.MaxSearchResults())
			}
		}()
	}
	for index, entry := range plan {
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventToolCall,
			Step:      2 + index,
			ToolName:  "serper_search",
			ToolInput: entry.query,
			Content:   fmt.Sprintf("Searching (%s): %s", entry.section, entry.query),
		})
		jobs <- index
	}
	close(jobs)
	waitGroup.Wait()

	for index, entry := range plan {
		results, err := searchOutcomes[index].results, searchOutcomes[index].err
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
			// Social hits are never part of the employment identity check: they belong to
			// the separate, flagged social section.
			for _, result := range excluded {
				if isPublicSocialURL(result.Link) {
					continue
				}
				gateExcluded = append(gateExcluded, result)
			}
		}
		searchTrace.Queries = append(searchTrace.Queries, models.QueryTrace{
			Query:    entry.query,
			Purpose:  entry.section,
			Kept:     kept,
			Excluded: excluded,
		})
		fresh := make([]models.SearchResult, 0, len(kept))
		for _, result := range kept {
			if !entry.personalization && isPublicSocialURL(result.Link) {
				// A social hit that arrived through an employment query is not employment
				// evidence. The dedicated social queries collect it under the flagged
				// section, where its snippet can be judged with the warning attached.
				continue
			}
			key := strings.ToLower(result.Link)
			if seenSnippet[key] {
				// The same hit answers several plan questions; the context lists it once.
				continue
			}
			seenSnippet[key] = true
			fresh = append(fresh, result)
		}
		if !entry.personalization {
			gateKept += len(fresh)
		}
		if len(fresh) > 0 {
			target := &gatheredContext
			if entry.personalization {
				target = &personalizationContext
			}
			fmt.Fprintf(target, "\n=== %s ===\n", entry.section)
			target.WriteString(FormatSearchSnippets(fresh))
			if entry.personalization {
				for _, result := range fresh {
					key := normalizeURL(result.Link)
					if key == "" || seenSocial[key] {
						continue
					}
					basis, confidence, ok := gate.personalizationVerdict(result)
					if !ok {
						continue
					}
					seenSocial[key] = true
					// The result's own URL decides its platform: a broad query can return a
					// hit from another platform, and the section must label it correctly.
					platform := publicPlatform(result.Link)
					if platform == "social" {
						platform = firstNonEmpty(entry.platform, "social")
					}
					signal := models.SocialSignal{
						Platform:      platform,
						URL:           result.Link,
						Title:         strings.TrimSpace(result.Title),
						Snippet:       strings.TrimSpace(result.Snippet),
						Source:        entry.section,
						IdentityBasis: basis,
						Confidence:    confidence,
						ObservedAt:    socialObservedAt(result),
					}
					socialSignals = append(socialSignals, signal)
					if len(socialSignals) >= personSocialSignalCap {
						break
					}
				}
			}
		}
		planResults = append(planResults, planSearch{entry: entry, results: fresh})
	}
	// The operator-confirmed profile is the strongest social tie available. Include it in the
	// flagged section even when no planned social query surfaced it, and upgrade the entry when
	// one did, so the section never presents the seed as a weaker "possible account".
	if confirmedSeed != "" && isPublicSocialURL(confirmedSeed) {
		if key := normalizeURL(confirmedSeed); key != "" {
			signal := models.SocialSignal{
				Platform:      publicPlatform(confirmedSeed),
				URL:           confirmedSeed,
				Title:         firstNonEmpty(query.SeedTitle, "Confirmed profile"),
				Snippet:       strings.TrimSpace(query.SeedSnippet),
				Source:        "confirmed profile (chosen by the operator)",
				IdentityBasis: "confirmed by the operator",
				Confidence:    1,
				ObservedAt: socialObservedAt(models.SearchResult{
					Link: confirmedSeed, Title: query.SeedTitle, Snippet: query.SeedSnippet,
				}),
			}
			replaced := false
			for index := range socialSignals {
				if normalizeURL(socialSignals[index].URL) != key {
					continue
				}
				socialSignals[index] = signal
				replaced = true
				break
			}
			if !replaced {
				socialSignals = append([]models.SocialSignal{signal}, socialSignals...)
				if len(socialSignals) > personSocialSignalCap {
					socialSignals = socialSignals[:personSocialSignalCap]
				}
			}
			seenSocial[key] = true
		}
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
	// FREEZE the identity before the fetch phase: the registry director pages
	// (WeeDoo, zaubacorp) carry the person's DIN in their URL, and the freeze
	// scans kept and gate-excluded hits alike — the director page that reveals
	// the DIN may name a group company the gate counted as a different
	// employer. Once frozen, every DIN-bearing source is identity-confirmed.
	allResults := make([]models.SearchResult, 0, 32)
	for _, batch := range planResults {
		allResults = append(allResults, batch.results...)
	}
	allResults = append(allResults, gateExcluded...)
	frozenDIN, frozenSlug := harvestPersonDIN(allResults, personName)
	if frozenDIN != "" {
		gate.anchorIdentity(frozenDIN)
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventThinking,
			Step:      3,
			Content:   fmt.Sprintf("Registry identity frozen: %s / DIN %s — director pages from WeeDoo and zaubacorp will be read directly.", personName, frozenDIN),
		})
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
	// A social URL is never a page-fetch target in person mode: the flagged social section is
	// built from its snippet, and fetching it would place unverified social content in the
	// employment evidence. The confirmed identity block below still carries the operator's
	// chosen snippet when the confirmed profile happens to be a social page.
	fetchTargets := make([]models.SearchResult, 0, pa.engine.MaxScrapes())
	seedFetched := false
	if confirmedSeed != "" && !isPublicSocialURL(confirmedSeed) {
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
	// The frozen (name, DIN) pair makes the registry director pages
	// deterministic: both sites are fetched directly, ahead of the
	// search-derived pages, because they are identity-anchored by construction
	// (the URL carries the frozen DIN) and list every company the person
	// serves.
	if frozenDIN != "" && frozenSlug != "" {
		registryTargets := []models.SearchResult{
			{
				Title: "WeeDoo director page for " + personName,
				Link:  "https://weedoo.in/director/" + frozenSlug + "/" + frozenDIN,
			},
			{
				Title: "zaubacorp director page for " + personName,
				Link:  "https://www.zaubacorp.com/" + frozenSlug + "-" + frozenDIN,
			},
		}
		registry := make([]models.SearchResult, 0, len(registryTargets))
		for _, target := range registryTargets {
			if !seenCandidate[strings.ToLower(target.Link)] {
				seenCandidate[strings.ToLower(target.Link)] = true
				registry = append(registry, target)
			}
		}
		// Keep the confirmed seed first, registry pages second, and re-slot the
		// search-derived candidates after them.
		fetchTargets = append(registry, fetchTargets...)
	}
	addCandidate := func(sr models.SearchResult, verdict personVerdict) {
		key := strings.ToLower(sr.Link)
		if key == "" || seenCandidate[key] {
			return
		}
		// Social pages are outreach context, not evidence. Their snippet is already in the
		// flagged social section; fetching one would put unverified social content one step
		// from the employment dossier.
		if isPublicSocialURL(sr.Link) {
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
	// its snippet was used as evidence, and the operator pointed at it. A social seed is the
	// exception: it is presented in the flagged social section, not as employment evidence.
	if confirmedSeed != "" && !seedFetched && !isPublicSocialURL(confirmedSeed) {
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
  "public_accounts": [
    {
      "platform": "X/Twitter",
      "url": "string",
      "identity_basis": "string",
      "confidence": 0.0
    }
  ],
  "personalization_evidence": [
    {
      "platform": "X/Twitter",
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
  current employment, title, location, or other sensitive personal attributes. It is displayed in
  a separate, clearly flagged social section: do not copy its accounts, posts, or claims into the
  executive summary, experience, education, skills, or online presence.
- Include public interests or recurring themes only when directly supported by the labelled
  personalization context, and keep them general. The system generates public_accounts
  deterministically from the search snippets, so that field may be omitted.`

	var profile models.PersonProfile
	evidenceContext := gatheredContext.String()
	if rocketReachProfile != nil {
		evidenceContext = "=== PRIORITY ROCKETREACH PROFILE ===\n" + formatRocketReachProfile(rocketReachProfile) + "\n\n" + evidenceContext
	}
	if err := pa.engine.SynthesizeJSON(ctx, systemPrompt, evidenceContext, &profile); err != nil {
		return nil, fmt.Errorf("person synthesis failed: %w", err)
	}
	// Provider is a trust marker: QC keeps a provider-sourced row at another employer. Only the
	// agent may set it, so a model that echoes "provider": "RocketReach" into its JSON cannot
	// smuggle an invented row past the employer gate.
	clearModelProvider(&profile)
	// The social section is deterministic and separate from the synthesised dossier: every
	// signal carries the search snippet, its identity basis, a confidence and the date the
	// snippet proves, and the fixed warning travels with it. The model's public_accounts output
	// is replaced by this projection so an invented handle can never reach the operator.
	profile.PublicAccounts = publicAccountsFromSocials(socialSignals)
	profile.Socials = socialSignals
	if len(socialSignals) > 0 {
		profile.SocialNote = models.PersonSocialDisclaimer
	}
	if rocketReachWarning != "" {
		profile.Warnings = append(profile.Warnings, rocketReachWarning)
	}

	finishSynthesis("dossier synthesised")
	profile.Sources = sources
	if rocketReachProfile != nil {
		mergeRocketReachProfile(&profile, rocketReachProfile)
		if warning := rocketReachPastRoleWarning(rocketReachProfile, companyName); warning != "" {
			profile.Warnings = append(profile.Warnings, warning)
		}
		applyRocketReachContact(&profile, rocketReachProfile)
	}

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

// lookupRocketReach runs the priority RocketReach pipeline before any other
// search: one authoritative record can spare the run most of its web
// research. The direct lookup (the operator's seed profile first, then the name
// plus the confirmed employer) escalates through RocketReach's free search
// endpoint when it misses, so a same-named profile at the confirmed employer is
// found and enriched instead of the miss quietly abandoning the source. Paid
// contact enrichment is spent only on the one profile the confirmed identity
// matches.
func (pa *PersonAgent) lookupRocketReach(ctx context.Context, sessionID string, query models.PersonQuery, eventCh chan<- models.AgentEvent) *tools.RocketReachProfile {
	if pa.rocketReach == nil || !pa.rocketReach.Enabled() {
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID, Type: models.EventToolResult, Step: 1,
			ToolName: "rocketreach_lookup", Content: "skipped: RocketReach is not configured",
		})
		return nil
	}
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventToolCall,
		Step:      1,
		ToolName:  "rocketreach_lookup",
		ToolInput: fmt.Sprintf("%s | %s | %s", query.Name, query.Company, query.SeedURL),
		Content:   "Priority enrichment: retrieving the structured profile, LinkedIn URL, and contact details...",
	})
	result, err := pa.rocketReach.Lookup(ctx, query.Name, query.Company, query.SeedURL)
	if err != nil {
		log.Printf("[PERSON][session %s] RocketReach lookup unavailable: %v", sessionID, err)
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventToolResult,
			Step:      1,
			ToolName:  "rocketreach_lookup",
			Content:   fmt.Sprintf("unavailable: %v", err),
		})
		return nil
	}
	if result == nil {
		// The one-shot lookup missed. The free search endpoint can still find
		// the right profile among same-named people, and only the matched
		// candidate costs a lookup credit.
		result = pa.searchRocketReachCandidate(ctx, sessionID, query, eventCh)
	}
	if result == nil {
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventToolResult,
			Step:      1,
			ToolName:  "rocketreach_lookup",
			Content:   "no matching contact found; no lookup credit charged",
		})
		return nil
	}
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID, Type: models.EventToolResult, Step: 1,
		ToolName: "rocketreach_lookup",
		Content: fmt.Sprintf("priority profile retrieved: LinkedIn=%t, %d job(s), %d education record(s), %d skill(s), %d email(s), %d phone number(s)",
			result.LinkedInURL != "", len(result.JobHistory), len(result.Education), len(result.Skills), len(result.Emails), len(result.Phones)),
	})
	return result
}

// searchRocketReachCandidate finds the person through RocketReach's free search
// endpoint when the direct lookup misses, and fetches the full profile for the
// one candidate worth a paid lookup. Search results carry summary facts only, so
// the selected candidate's full record — contact details included — comes back
// with a second lookup pinned to its profile ID (or LinkedIn URL when the summary
// has no ID). If that paid fetch fails, the search profile itself is still the
// structured record the run wants.
//
// A person who has left the confirmed employer is still that identity: the first
// search is filtered by current employer, and when it selects nobody the search is
// repeated once without that filter so a past role at the confirmed company can
// disambiguate the profile. The retry is free and bounded to one extra request.
func (pa *PersonAgent) searchRocketReachCandidate(ctx context.Context, sessionID string, query models.PersonQuery, eventCh chan<- models.AgentEvent) *tools.RocketReachProfile {
	name := strings.TrimSpace(query.Name)
	if name == "" {
		return nil
	}
	base := map[string][]string{"name": []string{name}}
	if location := strings.TrimSpace(query.Location); location != "" {
		base["location"] = []string{location}
	}
	company := strings.TrimSpace(query.Company)
	currentEmployer := make(map[string][]string, len(base)+1)
	for key, value := range base {
		currentEmployer[key] = value
	}
	if company != "" {
		currentEmployer["current_employer"] = []string{company}
	}

	profiles, err := pa.rocketReach.Search(ctx, currentEmployer)
	if err != nil {
		log.Printf("[PERSON][session %s] RocketReach search fallback unavailable: %v", sessionID, err)
		return nil
	}
	candidate, basis := selectRocketReachCandidateWithBasis(profiles, query)
	if candidate == nil && company != "" {
		// The current-employer filter is the reason a former director or employee
		// disappears. Ask the free search once more without it; the selection then
		// uses career history to decide whether this is the same person.
		if pastProfiles, retryErr := pa.rocketReach.Search(ctx, base); retryErr == nil {
			if selected, selectedBasis := selectRocketReachCandidateWithBasis(pastProfiles, query); selected != nil {
				candidate, basis = selected, selectedBasis
				profiles = pastProfiles
			}
		} else {
			log.Printf("[PERSON][session %s] RocketReach past-employer search fallback unavailable: %v", sessionID, retryErr)
		}
	}
	if candidate == nil {
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID, Type: models.EventToolResult, Step: 1,
			ToolName: "rocketreach_lookup",
			Content:  fmt.Sprintf("search fallback returned %d profile(s) but none matched the confirmed identity; no lookup credit charged", len(profiles)),
		})
		return nil
	}
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID, Type: models.EventThinking, Step: 1,
		Content: fmt.Sprintf("RocketReach search fallback pinned %s via %s; fetching its full record...", candidate.Name, basis),
	})
	var result *tools.RocketReachProfile
	if candidate.ID > 0 {
		// The ID is the exact profile the search selected; a name/employer lookup
		// would re-run the same ambiguity this step just resolved.
		result, err = pa.rocketReach.LookupByID(ctx, candidate.ID)
		if err != nil {
			log.Printf("[PERSON][session %s] RocketReach candidate lookup by ID unavailable: %v", sessionID, err)
			result = nil
		}
	}
	if result == nil {
		result, err = pa.rocketReach.Lookup(ctx, candidate.Name, candidate.CurrentEmployer, candidate.LinkedInURL)
		if err != nil {
			log.Printf("[PERSON][session %s] RocketReach candidate lookup unavailable: %v", sessionID, err)
			return candidate
		}
	}
	return result
}

// selectRocketReachCandidate picks the one profile worth a paid lookup. The order is the
// identity evidence an operator would use: the confirmed profile URL, then the confirmed
// employer as the current employer, then — because a director or long-serving employee may
// have moved on — a past role at the confirmed employer, and finally a lone exactly-named
// profile when the run has no company or seed to anchor on. An ambiguous set is left alone
// rather than guessed at.
func selectRocketReachCandidate(profiles []tools.RocketReachProfile, query models.PersonQuery) *tools.RocketReachProfile {
	candidate, _ := selectRocketReachCandidateWithBasis(profiles, query)
	return candidate
}

// rocketReachRank orders the identity evidence a profile states. Higher is stronger; the
// ranking is what keeps a current employer above a past one, and a past one above a name.
const (
	rocketReachRankNone = iota
	rocketReachRankLoneName
	rocketReachRankPastEmployer
	rocketReachRankCurrentEmployer
	rocketReachRankSeed
)

// rocketReachCandidateMatch ranks one profile against the confirmed identity and returns the
// human-readable basis the run can report. The basis is what lets the dossier say "matched by
// a past role at X" instead of silently treating a former employee as a current one.
func rocketReachCandidateMatch(profile tools.RocketReachProfile, query models.PersonQuery) (int, string) {
	seed := strings.TrimSpace(query.SeedURL)
	if seed != "" && strings.EqualFold(strings.TrimSpace(profile.LinkedInURL), seed) {
		return rocketReachRankSeed, "the confirmed profile URL"
	}
	company := strings.TrimSpace(query.Company)
	if company == "" {
		return rocketReachRankNone, ""
	}
	if rocketReachEmployerMatches(profile.CurrentEmployer, company) {
		return rocketReachRankCurrentEmployer, "the confirmed employer as their current employer"
	}
	if job, ok := rocketReachPastEmployerJob(profile, company); ok {
		return rocketReachRankPastEmployer, fmt.Sprintf("a past role at %s", firstNonEmpty(strings.TrimSpace(job.Company), company))
	}
	return rocketReachRankNone, ""
}

// selectRocketReachCandidateWithBasis is selectRocketReachCandidate plus the match basis and
// a deterministic tie-break: seed, current employer, then the most recent past role. A lone
// exact-name profile is only considered when there is no company or seed, because a name alone
// is not an identity.
func selectRocketReachCandidateWithBasis(profiles []tools.RocketReachProfile, query models.PersonQuery) (*tools.RocketReachProfile, string) {
	bestIndex := -1
	bestRank := rocketReachRankNone
	bestBasis := ""
	bestRecency := ""
	company := strings.TrimSpace(query.Company)
	for index := range profiles {
		rank, basis := rocketReachCandidateMatch(profiles[index], query)
		if rank == rocketReachRankNone {
			continue
		}
		recency := ""
		if rank == rocketReachRankPastEmployer {
			recency = rocketReachPastEmployerRecency(profiles[index], company)
		}
		if rank > bestRank || (rank == bestRank && recency > bestRecency) {
			bestIndex, bestRank, bestBasis, bestRecency = index, rank, basis, recency
		}
	}
	if bestIndex >= 0 {
		return &profiles[bestIndex], bestBasis
	}
	if company != "" || strings.TrimSpace(query.SeedURL) != "" {
		return nil, ""
	}
	matchedIndex := -1
	matches := 0
	for index := range profiles {
		if !strings.EqualFold(strings.TrimSpace(profiles[index].Name), strings.TrimSpace(query.Name)) {
			continue
		}
		matchedIndex = index
		matches++
	}
	if matches == 1 {
		return &profiles[matchedIndex], "a unique exact-name match"
	}
	return nil, ""
}

// rocketReachPastEmployerJob returns the most recent job at the confirmed company, which is
// the past-role evidence a profile is matched on. An open-ended role sorts newest: if a person
// is still at the company, RocketReach's current_employer field is the stale part.
func rocketReachPastEmployerJob(profile tools.RocketReachProfile, company string) (tools.RocketReachJob, bool) {
	var best tools.RocketReachJob
	bestKey := ""
	found := false
	for _, job := range profile.JobHistory {
		if !rocketReachEmployerMatches(job.Company, company) {
			continue
		}
		key := rocketReachJobRecencyKey(job)
		if !found || key > bestKey {
			best, bestKey, found = job, key, true
		}
	}
	return best, found
}

// rocketReachPastEmployerRecency is the sort key for competing past-role matches.
func rocketReachPastEmployerRecency(profile tools.RocketReachProfile, company string) string {
	job, ok := rocketReachPastEmployerJob(profile, company)
	if !ok {
		return ""
	}
	return rocketReachJobRecencyKey(job)
}

// rocketReachJobRecencyKey renders a job's end and start dates for lexical comparison. The
// end date dominates; an empty end date means the role is still open and therefore newest.
// RocketReach publishes dates as calendar strings ("2020-01-01", "2020"), which sort reliably
// in that shape.
func rocketReachJobRecencyKey(job tools.RocketReachJob) string {
	end := strings.TrimSpace(job.EndDate)
	if end == "" {
		end = "9999-12-31"
	}
	return end + "|" + strings.TrimSpace(job.StartDate)
}

// rocketReachPastRoleWarning explains a profile that was matched by career history rather than
// a current role. It is a warning, never a rejection: a former director is still the person the
// operator asked about, but the dossier must not present the past role as current. The match is
// silent otherwise, because the profile's current employer is what the merger fills.
func rocketReachPastRoleWarning(result *tools.RocketReachProfile, company string) string {
	if result == nil || strings.TrimSpace(company) == "" {
		return ""
	}
	if strings.TrimSpace(result.CurrentEmployer) != "" && rocketReachEmployerMatches(result.CurrentEmployer, company) {
		return ""
	}
	job, ok := rocketReachPastEmployerJob(*result, company)
	if !ok {
		return ""
	}
	period := ""
	if joined := joinNonEmpty(" - ", job.StartDate, job.EndDate); joined != "" {
		period = " (" + joined + ")"
	}
	return fmt.Sprintf("RocketReach matched this person by a past role at %s%s as %s; their current employer is listed as %s. Verify the current role before treating it as current.",
		firstNonEmpty(strings.TrimSpace(job.Company), strings.TrimSpace(company)), period,
		firstNonEmpty(strings.TrimSpace(job.Title), "a role"),
		firstNonEmpty(strings.TrimSpace(result.CurrentEmployer), "unknown"))
}

// rocketReachContactDetails converts a provider profile into the dossier's contact rows,
// dropping grade-F addresses (RocketReach's inferred patterns rather than verified addresses).
// It is shared by the explicit person dossier and the company roster's on-demand enrichment so
// both surfaces apply exactly the same rule.
func rocketReachContactDetails(result *tools.RocketReachProfile) (emails []models.PersonContactEmail, phones []models.PersonContactPhone) {
	if result == nil {
		return nil, nil
	}
	for _, email := range result.Emails {
		if strings.EqualFold(strings.TrimSpace(email.Grade), "F") {
			continue
		}
		emails = append(emails, models.PersonContactEmail{
			Email: email.Email, Type: email.Type, Grade: email.Grade,
		})
	}
	for _, phone := range result.Phones {
		phones = append(phones, models.PersonContactPhone{
			Number: phone.Number, Type: phone.Type,
		})
	}
	return emails, phones
}

func applyRocketReachContact(profile *models.PersonProfile, result *tools.RocketReachProfile) {
	if result == nil {
		return
	}
	enrichment := &models.PersonContactEnrichment{Source: models.ProviderRocketReach, LinkedInURL: result.LinkedInURL}
	enrichment.Emails, enrichment.Phones = rocketReachContactDetails(result)
	profile.ContactEnrichment = enrichment
}

// clearModelProvider removes any Provider value the synthesis model returned. Provider is a
// trust marker used by dossier QC to keep provider-sourced rows; it must only be set by the
// agent after a real provider lookup.
func clearModelProvider(profile *models.PersonProfile) {
	if profile == nil {
		return
	}
	for index := range profile.Experiences {
		profile.Experiences[index].Provider = ""
	}
	for index := range profile.Education {
		profile.Education[index].Provider = ""
	}
}

func formatRocketReachProfile(result *tools.RocketReachProfile) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Name: %s\nCurrent title: %s\nCurrent employer: %s\nLocation: %s\nLinkedIn: %s\n",
		result.Name, result.CurrentTitle, result.CurrentEmployer,
		firstNonEmpty(result.Location, joinNonEmpty(", ", result.City, result.Region, result.Country)),
		result.LinkedInURL)
	if len(result.JobHistory) > 0 {
		builder.WriteString("Job history:\n")
		for _, job := range result.JobHistory {
			fmt.Fprintf(&builder, "- %s at %s (%s - %s), %s\n", job.Title, job.Company, job.StartDate, job.EndDate, job.Location)
		}
	}
	if len(result.Education) > 0 {
		builder.WriteString("Education:\n")
		for _, education := range result.Education {
			fmt.Fprintf(&builder, "- %s, %s, %s\n", firstNonEmpty(education.Institution, education.School), education.Degree, firstNonEmpty(education.Field, education.Major))
		}
	}
	if len(result.Skills) > 0 {
		fmt.Fprintf(&builder, "Skills: %s\n", strings.Join(result.Skills, ", "))
	}
	if len(result.Emails) > 0 || len(result.Phones) > 0 {
		fmt.Fprintf(&builder, "Contact details: %d email(s), %d phone number(s) retrieved from RocketReach.\n", len(result.Emails), len(result.Phones))
	}
	return builder.String()
}

func mergeRocketReachProfile(profile *models.PersonProfile, result *tools.RocketReachProfile) {
	if result == nil {
		return
	}
	source := strings.TrimSpace(result.LinkedInURL)
	if source == "" {
		source = "RocketReach"
	}
	if profile.CurrentTitle == "" {
		profile.CurrentTitle = result.CurrentTitle
	}
	if profile.CurrentCompany == "" {
		profile.CurrentCompany = result.CurrentEmployer
	}
	if profile.Location == "" {
		profile.Location = firstNonEmpty(result.Location, joinNonEmpty(", ", result.City, result.Region, result.Country))
	}
	if profile.OnlinePresence == nil {
		profile.OnlinePresence = map[string]string{}
	}
	if profile.OnlinePresence["linkedin"] == "" && result.LinkedInURL != "" {
		profile.OnlinePresence["linkedin"] = result.LinkedInURL
	}
	for _, job := range result.JobHistory {
		role := strings.TrimSpace(job.Title)
		company := strings.TrimSpace(job.Company)
		if role == "" && company == "" {
			continue
		}
		experience := models.Experience{
			Role: role, Company: company, Location: job.Location,
			Period:   joinNonEmpty(" - ", job.StartDate, job.EndDate),
			Source:   source,
			Provider: models.ProviderRocketReach,
		}
		if job.Description != "" {
			experience.Highlights = []string{job.Description}
		}
		if !containsExperience(profile.Experiences, experience) {
			profile.Experiences = append(profile.Experiences, experience)
		}
	}
	for _, item := range rocketReachEducationRows(result) {
		if !containsEducation(profile.Education, item) {
			profile.Education = append(profile.Education, item)
		}
	}
	for _, skill := range result.Skills {
		skill = strings.TrimSpace(skill)
		if skill != "" && !containsString(profile.KeySkills, skill) {
			profile.KeySkills = append(profile.KeySkills, skill)
		}
	}
	if result.LinkedInURL != "" && !containsString(profile.Sources, result.LinkedInURL) {
		profile.Sources = append(profile.Sources, result.LinkedInURL)
	}
}

// publicAccountsFromSocials projects the flagged social section onto the API's older
// public_accounts field, so a consumer that already reads public_accounts sees exactly the
// same deterministic candidates the console and exports present under the social heading.
// Returning nil for no signals keeps the JSON field omitted.
func publicAccountsFromSocials(signals []models.SocialSignal) []models.PublicAccount {
	if len(signals) == 0 {
		return nil
	}
	accounts := make([]models.PublicAccount, 0, len(signals))
	for _, signal := range signals {
		accounts = append(accounts, models.PublicAccount{
			Platform:      signal.Platform,
			URL:           signal.URL,
			IdentityBasis: signal.IdentityBasis,
			Confidence:    signal.Confidence,
		})
	}
	return accounts
}

func joinNonEmpty(separator string, values ...string) string {
	var nonEmpty []string
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			nonEmpty = append(nonEmpty, strings.TrimSpace(value))
		}
	}
	return strings.Join(nonEmpty, separator)
}

func containsExperience(items []models.Experience, candidate models.Experience) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Role), strings.TrimSpace(candidate.Role)) &&
			strings.EqualFold(strings.TrimSpace(item.Company), strings.TrimSpace(candidate.Company)) &&
			strings.EqualFold(strings.TrimSpace(item.Period), strings.TrimSpace(candidate.Period)) {
			return true
		}
	}
	return false
}

func containsEducation(items []models.Education, candidate models.Education) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Institution), strings.TrimSpace(candidate.Institution)) &&
			strings.EqualFold(strings.TrimSpace(item.Degree), strings.TrimSpace(candidate.Degree)) &&
			strings.EqualFold(strings.TrimSpace(item.Field), strings.TrimSpace(candidate.Field)) {
			return true
		}
	}
	return false
}

func containsString(items []string, candidate string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}
