package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// CompanyAgent conducts multi-phase organizational discovery and hierarchy mapping.
type CompanyAgent struct {
	engine      *Engine
	rocketReach *tools.RocketReachClient
}

const maxCompanyReviewIterations = 3

const companySearchConcurrency = 4

// minQCIterationBudget is the minimum time that must remain before another
// QC-driven iteration (search + scrape + synthesis) is worth starting.
const minQCIterationBudget = 60 * time.Second

// validationReserve is the slice of a run's budget kept back for the freshness pass, whose
// reverse lookups are the only thing that can tie a person to the company *after* the run
// has finished collecting.
//
// This is not theoretical: a live run spent 158 s of a 180 s deadline scraping, and the
// freshness pass then reported "no run budget left for reverse lookups" — 5 stale and 5
// undated rows, plus two named directors excluded for lack of a source, went unchecked.
const validationReserve = 25 * time.Second

// gapLookupReserve is the same idea for the anchored lookups that run before synthesis:
// they need headroom to query and read their results.
const gapLookupReserve = 15 * time.Second

// synthesisReserve is what the synthesis and QC review need between the two lookup passes.
// It is part of scrapeReserve because both passes run after scraping, so a scrape that spends
// synthesis's time is the same failure as one that spends the lookups' time.
const synthesisReserve = 15 * time.Second

// scrapeReserve is what no fetch may touch: the synthesis and both lookup passes. One runs
// before synthesis to fill what the evidence does not cover, the other after it to date and
// rescue rows. A live run measured scrape 155.0 s of a 180 s budget and then "0 reverse
// lookup(s)" with 15 roster rows left unchecked and no gap lookups at all — bounding a
// batch's *start* was not enough, because a batch already in flight overruns any check.
// Fetching therefore runs on a context that stops this much short of the deadline.
const scrapeReserve = validationReserve + synthesisReserve + gapLookupReserve

// hasValidationReserve reports whether enough of the run's budget remains to start another
// scrape batch and still leave the freshness pass its reserve.
func hasValidationReserve(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return time.Until(deadline) > validationReserve
}

// scrapeDeadlineContext returns a context that stops fetching scrapeReserve short of the
// run's own deadline. A context without a deadline imposes nothing; a deadline already
// inside the reserve yields an expired context, so nothing is fetched at all.
func scrapeDeadlineContext(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, deadline.Add(-scrapeReserve))
}

// hasBudgetForIteration reports whether the context allows another full research
// iteration. Contexts without a deadline are treated as unbounded.
func hasBudgetForIteration(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return time.Until(deadline) >= minQCIterationBudget
}

const maxCompanySearchQueries = 24

const (
	companyPlanningTimeout  = 20 * time.Second
	companySemanticTimeout  = 8 * time.Second
	companySynthesisTimeout = 45 * time.Second
	companyReviewTimeout    = 30 * time.Second
	companyQuestionTimeout  = 45 * time.Second
)

// NewCompanyAgent initializes the company research specialist agent.
func NewCompanyAgent(engine *Engine, rocketReach ...*tools.RocketReachClient) *CompanyAgent {
	agent := &CompanyAgent{engine: engine}
	if len(rocketReach) > 0 {
		agent.rocketReach = rocketReach[0]
	}
	return agent
}

// ResearchCompany preserves the original company-only entry point.
func (ca *CompanyAgent) ResearchCompany(ctx context.Context, sessionID, companyName string, eventCh chan<- models.AgentEvent) (*models.OrgChart, error) {
	return ca.ResearchCompanyWithBrief(ctx, sessionID, models.CompanyBrief{CompanyName: companyName}, eventCh)
}

// ResearchCompanyWithBrief runs planning, search, ranking, scraping, and synthesis.
func (ca *CompanyAgent) ResearchCompanyWithBrief(ctx context.Context, sessionID string, brief models.CompanyBrief, eventCh chan<- models.AgentEvent) (*models.OrgChart, error) {
	chart, _, err := ca.ResearchCompanyRun(ctx, sessionID, brief, eventCh)
	return chart, err
}

// ResearchCompanyRun executes bounded research/QC iterations and returns retained context.
func (ca *CompanyAgent) ResearchCompanyRun(ctx context.Context, sessionID string, brief models.CompanyBrief, eventCh chan<- models.AgentEvent) (chart *models.OrgChart, state *models.CompanyResearchContext, err error) {
	brief.CompanyName = strings.TrimSpace(brief.CompanyName)
	if brief.CompanyName == "" {
		return nil, nil, fmt.Errorf("company name is required")
	}

	timer := newPhaseTimer()
	// The entity gate is built first: the deferred block below reports it, and every
	// later stage consults the same identity.
	matcher := ca.entityMatcherFor(brief)
	gate := &models.EntityGate{Confirmed: brief.EntityConfirmed, Mode: ca.engine.EntityGateMode()}
	if matcher != nil {
		gate.Keys = matcher.describe()
	}
	// Timings are attached on every return path, including failures: a run that
	// died in the scraper should still say how long it spent there. That is what
	// makes "the scraper is slow" a measured claim rather than a guess.
	defer func() {
		// The freshness pass runs last and before the artifacts are written, so
		// the rows it labels are the rows that get saved. Its own budget check
		// means a run that spent everything still gets the offline half.
		if chart != nil {
			finishValidate := timer.start("validate")
			report := ca.validateChart(ctx, sessionID, brief, chart, state, eventCh, gate)
			finishValidate(fmt.Sprintf("%d rows, %d lookups", report.RowsChecked, report.LookupsUsed))
			if state != nil {
				state.Validation = report
			}
			chart.Validation = report
			chart.EntityGate = gate
			summary := fmt.Sprintf("Entity gate (%s): %s | %d/%d candidates named the company, %d snippets dropped, %d off-entity people removed, %d rescued by lookup",
				ca.engine.EntityGateMode(), strings.Join(gate.Keys, ", "),
				gate.CandidatesTagged, gate.CandidatesTagged+gate.CandidatesNone, gate.SnippetsDropped, gate.RowsOffEntity, gate.RescuedByLookup)
			if gate.GapLookups > 0 {
				summary += fmt.Sprintf(", %d anchored gap lookup(s) added %d source(s)", gate.GapLookups, gate.GapEvidence)
			}
			if gate.LookupsDeferred > 0 {
				summary += fmt.Sprintf(", %d candidate(s) left unchecked by the lookup pass", gate.LookupsDeferred)
			}
			Emit(eventCh, models.AgentEvent{
				SessionID: sessionID,
				Type:      models.EventToolResult,
				Step:      33,
				ToolName:  "entity_gate",
				Content:   summary,
			})
		}
		timings := timer.results()
		if state != nil {
			state.Timings = timings
		}
		if chart != nil {
			chart.Timings = timings
			// Exporting runs on a detached context inside the engine, so a run
			// that used its whole research budget still saves its results.
			if exports := ca.engine.ExportCompany(ctx, chart); len(exports) > 0 {
				chart.Exports = exports
				Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolResult, Step: 31, ToolName: "exporter", Content: exportSummary(exports)})
			}
		}
		if report := timer.report(); report != "" {
			Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventTiming, Step: 30, ToolName: "phase_timer", Content: report})
		}
	}()

	state = &models.CompanyResearchContext{Brief: brief}
	state.EntityGate = gate
	var rocketReachValidationTrace []models.QueryTrace
	if brief.UseRocketReachDiscovery {
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID, Type: models.EventThinking, ToolName: "rocketreach_search",
			Content: "RocketReach discovery enabled; starting before company web research.",
		})
		rrResults, rrDiscovery, rrValidationEvidence, rrTrace := ca.discoverRocketReach(ctx, sessionID, brief, eventCh)
		state.SearchResults = appendUniqueSearchResults(state.SearchResults, rrResults)
		state.Evidence = appendUniqueEvidence(state.Evidence, rrValidationEvidence)
		state.Evidence = appendUniqueEvidence(state.Evidence, rocketReachEvidence(rrResults))
		state.RocketReachDiscovery = append(state.RocketReachDiscovery, rrDiscovery...)
		rocketReachValidationTrace = rrTrace
	} else {
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID, Type: models.EventThinking, ToolName: "rocketreach_search",
			Content: "RocketReach discovery disabled for this Company-mode run.",
		})
	}

	finishPlan := timer.start("plan queries")
	plan, droppedQueries := ca.planQueries(ctx, brief)
	gate.QueriesDropped = droppedQueries
	finishPlan(fmt.Sprintf("%d queries (%d dropped as unanchored)", len(plan.Queries), droppedQueries))

	for iteration := 0; iteration < maxCompanyReviewIterations; iteration++ {
		state.Iteration = iteration + 1
		state.QueryPlan = plan
		if iteration > 0 {
			Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventThinking, Step: 10 + iteration, Content: fmt.Sprintf("Running focused QC follow-up %d of %d...", iteration, maxCompanyReviewIterations-1)})
		}

		finishSearch := timer.start("search")
		results, searchTrace, searchErr := ca.searchPlan(ctx, sessionID, plan, eventCh, matcher)
		finishSearch(fmt.Sprintf("%d queries -> %d results", len(plan.Queries), len(results)))
		if searchErr != nil {
			return nil, state, searchErr
		}
		// The primary search's trace is the one the dossier publishes: QC follow-ups spend
		// their own queries, which the validation report already accounts for.
		if state.SearchTrace == nil {
			state.SearchTrace = searchTrace
			state.SearchTrace.Queries = append(state.SearchTrace.Queries, rocketReachValidationTrace...)
		}
		state.SearchResults = appendUniqueSearchResults(state.SearchResults, results)
		Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventThinking, Step: 7, Content: fmt.Sprintf("Evaluating relevance of %d search results...", len(state.SearchResults))})
		finishRank := timer.start("rank")
		state.RankedResults = ca.rankResults(ctx, brief, state.SearchResults)
		gate.CandidatesTagged, gate.CandidatesNone = 0, 0
		for _, candidate := range state.RankedResults {
			if candidate.EntityKey != "" {
				gate.CandidatesTagged++
			} else {
				gate.CandidatesNone++
			}
		}
		finishRank(fmt.Sprintf("%d candidates (%d named the company)", len(state.RankedResults), gate.CandidatesTagged))
		Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolResult, Step: 7, ToolName: "relevance_ranker", Content: fmt.Sprintf("Selected %d relevant candidates.", len(state.RankedResults))})
		finishScrape := timer.start("scrape")
		newEvidence, _, scrapeReport := ca.scrapeRankedWithBrief(ctx, sessionID, brief, state.RankedResults, state.SearchResults, state.Evidence, eventCh, matcher)
		finishScrape(fmt.Sprintf("%d attempts -> %d pages kept", len(scrapeReport), len(newEvidence)))
		state.Evidence = appendUniqueEvidence(state.Evidence, newEvidence)
		state.ScrapeReport = appendUniqueScrapeReports(state.ScrapeReport, scrapeReport)
		state.Evidence = appendUniqueEvidence(state.Evidence, allSearchSnippetEvidence(state.SearchResults, matcher))
		if len(state.Evidence) == 0 {
			state.Evidence = appendUniqueEvidence(state.Evidence, searchSnippetEvidence(state.RankedResults, state.SearchResults, ca.engine.MaxScrapes(), matcher))
		}
		if len(state.Evidence) == 0 {
			return nil, state, fmt.Errorf("no usable company evidence was collected")
		}

		// Date every source before synthesis: the model has to be able to prefer a
		// 2025 filing over a 2020 one, and the validation pass reuses the same
		// dates without another request.
		annotateEvidenceDates(state.Evidence)
		// Now, before synthesis, spend a few company-anchored lookups on the areas the
		// evidence does not cover. The post-synthesis pass validates and rescues rows that
		// exist; this is what gives a thin roster something to be built from, and its
		// queries name the company, so no unanchored snippet can enter here.
		ca.fillEntityGaps(ctx, sessionID, brief, state, eventCh, matcher, gate)
		// Only snippets that name the company reach the synthesiser. A snippet is a
		// name and a title, and without this filter the results of every planned
		// query are fair game for the roster.
		synthesisSnippets, droppedSnippets := filterEntitySnippets(state.SearchResults, matcher)
		gate.SnippetsDropped = droppedSnippets
		finishSynthesize := timer.start("synthesize")
		chart, err = ca.synthesizeChart(ctx, brief, state.Evidence, synthesisSnippets, sessionID, eventCh)
		finishSynthesize(fmt.Sprintf("%d evidence items", len(state.Evidence)))
		if err != nil {
			return nil, state, err
		}
		chart.ScrapeReport = state.ScrapeReport
		chart.SearchTrace = state.SearchTrace
		chart.RocketReachDiscovery = state.RocketReachDiscovery
		finishReview := timer.start("review (QC)")
		review := ca.reviewChart(ctx, brief, chart, state.Evidence, sessionID, eventCh)
		finishReview(review.Verdict)
		state.Reviews = append(state.Reviews, review)
		if review.Verdict != "revise" || len(review.FollowUpQueries) == 0 || iteration == maxCompanyReviewIterations-1 {
			Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventComplete, Step: 20 + iteration, Content: fmt.Sprintf("Completed research for %s after %d QC review(s).", chart.CompanyName, len(state.Reviews))})
			return chart, state, nil
		}
		// A follow-up iteration costs another full search/scrape/synthesis pass.
		// Only start one that can finish: returning the chart we already have
		// beats failing the whole run on the deadline.
		if !hasBudgetForIteration(ctx) {
			Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventComplete, Step: 20 + iteration, Content: fmt.Sprintf("Completed research for %s after %d QC review(s); QC follow-up skipped (time budget exhausted).", chart.CompanyName, len(state.Reviews))})
			return chart, state, nil
		}
		plan = limitQueryPlan(models.QueryPlan{Queries: review.FollowUpQueries}, 4)
	}

	return chart, state, nil
}

func (ca *CompanyAgent) discoverRocketReach(ctx context.Context, sessionID string, brief models.CompanyBrief, eventCh chan<- models.AgentEvent) ([]models.SearchResult, []models.RocketReachDiscovery, []models.Evidence, []models.QueryTrace) {
	if ca.rocketReach == nil || !ca.rocketReach.Enabled() {
		return nil, nil, nil, nil
	}
	titleGroups := [][]string{
		{"director", "chief executive officer", "chief financial officer", "chief operating officer", "managing director"},
		{"procurement", "purchasing", "supply chain", "manager", "general manager", "deputy general manager", "vice president", "vice-president", "additional director", "executive director"},
		{"finance", "legal", "operations", "manager", "general manager", "deputy general manager", "vice president", "vice-president", "additional director", "executive director"},
		{"founder", "chairman", "president", "vice president"},
	}
	var results []models.SearchResult
	var discovery []models.RocketReachDiscovery
	var validationEvidence []models.Evidence
	var validationTrace []models.QueryTrace
	seen := map[string]bool{}
	validated := 0
	const maxValidationCandidates = 8
	for _, titles := range titleGroups {
		titleLabel := strings.Join(titles, ", ")
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID, Type: models.EventToolCall, ToolName: "rocketreach_search",
			Content: "Searching RocketReach key persons for " + brief.CompanyName + " (" + titleLabel + ")",
		})
		profiles, err := ca.rocketReach.Search(ctx, map[string][]string{
			"current_employer": {brief.CompanyName},
			"current_title":    titles,
		})
		if err != nil {
			Emit(eventCh, models.AgentEvent{
				SessionID: sessionID, Type: models.EventToolResult, ToolName: "rocketreach_search",
				Content: fmt.Sprintf("RocketReach company discovery unavailable: %v", err),
			})
			continue
		}
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID, Type: models.EventToolResult, ToolName: "rocketreach_search",
			Content: fmt.Sprintf("RocketReach returned %d profile(s) for %s", len(profiles), brief.CompanyName),
		})
		for _, profile := range profiles {
			name := strings.TrimSpace(profile.Name)
			employer := strings.TrimSpace(profile.CurrentEmployer)
			if name == "" || !rocketReachEmployerMatches(employer, brief.CompanyName) {
				continue
			}
			key := strings.ToLower(name + "\x00" + employer)
			if seen[key] {
				continue
			}
			seen[key] = true
			if validated >= maxValidationCandidates {
				continue
			}
			validationQuery := fmt.Sprintf("%q %q", name, brief.CompanyName)
			Emit(eventCh, models.AgentEvent{
				SessionID: sessionID, Type: models.EventToolCall, ToolName: "rocketreach_candidate_validation",
				Content: "Validating RocketReach candidate with indexed search: " + name,
			})
			validationHits, validationErr := ca.engine.SearchCached(ctx, validationQuery, ca.engine.MaxSearchResults())
			validationTrace = append(validationTrace, models.QueryTrace{
				Query: validationQuery, Purpose: "indexed validation of RocketReach candidate", Kept: validationHits,
			})
			if validationErr != nil {
				Emit(eventCh, models.AgentEvent{
					SessionID: sessionID, Type: models.EventToolResult, ToolName: "rocketreach_candidate_validation",
					Content: fmt.Sprintf("Validation unavailable for %s: %v", name, validationErr),
				})
				continue
			}
			if !hasRocketReachCandidateEvidence(name, brief.CompanyName, validationHits) {
				Emit(eventCh, models.AgentEvent{
					SessionID: sessionID, Type: models.EventToolResult, ToolName: "rocketreach_candidate_validation",
					Content: "Rejected RocketReach candidate without corroborating indexed evidence: " + name,
				})
				continue
			}
			validated++
			for _, hit := range validationHits {
				validationEvidence = append(validationEvidence, models.Evidence{
					URL: hit.Link, Title: hit.Title, Content: hit.Snippet,
					EvidenceType: "indexed_candidate_validation", SourceType: "google",
					Score: 0.8, QualityScore: 0.8,
				})
			}
			Emit(eventCh, models.AgentEvent{
				SessionID: sessionID, Type: models.EventToolResult, ToolName: "rocketreach_candidate_validation",
				Content: fmt.Sprintf("Validated RocketReach candidate %s with %d indexed hit(s)", name, len(validationHits)),
			})
			link := strings.TrimSpace(profile.LinkedInURL)
			snippet := fmt.Sprintf("%s is listed at %s as %s", name, employer, strings.TrimSpace(profile.CurrentTitle))
			results = append(results, models.SearchResult{
				Title: name + " - " + strings.TrimSpace(profile.CurrentTitle),
				Link:  link, Snippet: snippet,
			})
			discovery = append(discovery, models.RocketReachDiscovery{
				ProfileID: profile.ID, Name: name, Title: profile.CurrentTitle,
				Employer: employer, LinkedInURL: link,
				Query: brief.CompanyName + " / " + titleLabel,
			})
		}
	}
	return results, discovery, validationEvidence, validationTrace
}

func hasRocketReachCandidateEvidence(name, company string, hits []models.SearchResult) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	company = strings.ToLower(strings.TrimSpace(company))
	if name == "" || company == "" {
		return false
	}
	for _, hit := range hits {
		text := strings.ToLower(hit.Title + " " + hit.Snippet)
		if strings.Contains(text, name) && strings.Contains(text, company) {
			return true
		}
	}
	return false
}

func rocketReachEvidence(results []models.SearchResult) []models.Evidence {
	evidence := make([]models.Evidence, 0, len(results))
	for _, result := range results {
		evidence = append(evidence, models.Evidence{
			URL: result.Link, Title: result.Title, Content: result.Snippet,
			EvidenceType: "rocketreach_profile", SourceType: "rocketreach",
			Score: 0.7, QualityScore: 0.7,
		})
	}
	return evidence
}

func (ca *CompanyAgent) synthesizeChart(ctx context.Context, brief models.CompanyBrief, evidence []models.Evidence, results []models.SearchResult, sessionID string, eventCh chan<- models.AgentEvent) (*models.OrgChart, error) {
	Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventThinking, Step: 8, Content: "Synthesizing the organization chart from selected evidence..."})
	prompt := `You are an expert corporate intelligence analyst. Use only the supplied evidence and search snippets. Do not invent employees, titles, locations, reporting lines, or facts not supported by evidence. A job listing, vacancy, requisition, careers page, hiring announcement, or open position is not an employee and must never become a leadership or department member. Never put a role title such as "Manager, Logistics and Purchasing" in the name field; if no actual person's full name is supported, omit the row. Return only the top-level OrgChart JSON with company_name, domain, overview, brief, headquarters, leadership, departments, and locations. Do not wrap the result in an org_chart object. The brief must be a concise object with business_description, key_products, and key_locations. Each key_locations entry must be an object with city and country fields (address optional), never a plain string. Use key_products only for products the company manufactures, trades, or sells, and use key_locations for supported important offices, facilities, or operating locations. Leave brief fields empty when the evidence does not support them; do not infer products or locations from the company name or industry alone. The headquarters field MUST be a plain string such as "New Delhi, India". The locations field may contain objects with city, country, address, and headcount_est as a string. Image metadata is supporting evidence; do not invent visual facts from image URLs. Include supported leadership members by default. Also include every person the supplied evidence explicitly names in a role at the company — for example a professional-network profile that names the person, their title, and the company — placing each in the department their title belongs to and recording that page as their public_source, even when that function was not requested in the Areas field: the Areas field steers which functions get dedicated searches, it does not suppress people the evidence already names. Do not broaden an unspecified request into extra searches for procurement, operations, marketing, finance, engineering, or other departments, and do not create a department the evidence does not staff. For requested functions, consider title variants such as Chief, C-level, President, Executive Vice President, Senior Vice President, Vice President, VP, Head, Director, and General Manager. Conference speaker evidence may identify a person's company, function, and industry relevance, but conference participation alone does not prove that the person is a current executive; label or place such people conservatively unless current employment is corroborated. Distinguish current employment from former, previous, past, or advisory work: a person who previously worked for the researched company but now works elsewhere must not be listed as current leadership. Profile candidates are snippets from professional networks: when one pairs a person's name with a functional title and the target company (or one of the aliases the request lists), treat it as evidence of that person's function, list them in the matching department, and record which entity they belong to in their title. Promote a profile candidate into leadership only for board or C-level titles. Prefer listing a named profile candidate in a matching department over leaving that department empty, but only when the company is named in the same snippet. Every object in the departments array must use the shape {"name": "<function>", "head": "<optional person>", "description": "<optional summary>", "members": [{"name": "<person>", "title": "<their title>", "public_source": "<source url>"}]}. List every person you attribute to a function inside that function's members array, and never leave members empty when you have named a person for that function. Every evidence item is stamped with the newest date its source proves (as of YYYY-MM-DD). Use those dates: when two sources disagree about a person's role, prefer the newer one, and do not present someone whose only sources are several years old as a current executive — either record their dated role or leave them out. Every person you list must be tied to the researched company stated at the top of the request: a page that never names that company is not evidence about it, however relevant it looks.`
	var chart models.OrgChart
	contextData := fmt.Sprintf("Research brief: %+v\n\nSearch snippets:\n%s\n\nSelected evidence:\n%s", brief, FormatSearchSnippets(results), formatEvidence(evidence))
	if matcher := ca.entityMatcherFor(brief); matcher != nil {
		contextData = matcher.promptLine() + "\n\n" + contextData
	}
	if profiles := formatProfileCandidates(evidence); profiles != "" {
		contextData += "\n\n" + profiles
	}
	llmCtx, cancel := context.WithTimeout(ctx, companySynthesisTimeout)
	defer cancel()
	if err := ca.engine.SynthesizeJSON(llmCtx, prompt, contextData, &chart); err != nil {
		return nil, fmt.Errorf("synthesis failed: %w", err)
	}
	removeVacancyPlaceholders(&chart)
	dedupeChartEmployees(&chart)
	if chart.CompanyName == "" {
		chart.CompanyName = brief.CompanyName
	}
	if chart.Domain == "" {
		chart.Domain = brief.Domain
	}
	chart.Images = evidenceImages(evidence)
	chart.Sources = evidenceSources(evidence)
	chart.LastUpdated = time.Now()
	return &chart, nil
}

// dedupeChartEmployees keeps one canonical row per normalized person name.
// Leadership wins over department membership because it is the more specific
// placement; duplicate rows are merged rather than emitted twice.
func dedupeChartEmployees(chart *models.OrgChart) {
	if chart == nil {
		return
	}
	seen := make(map[string]models.Employee)
	leadershipIndex := make(map[string]int)
	leadership := make([]models.Employee, 0, len(chart.Leadership))
	for _, employee := range chart.Leadership {
		key := normalizePersonName(employee.Name)
		if key == "" {
			continue
		}
		if existing, ok := seen[key]; ok {
			seen[key] = mergeEmployee(existing, employee)
			leadership[leadershipIndex[key]] = seen[key]
			continue
		}
		seen[key] = employee
		leadershipIndex[key] = len(leadership)
		leadership = append(leadership, employee)
	}
	chart.Leadership = leadership

	for departmentIndex := range chart.Departments {
		department := &chart.Departments[departmentIndex]
		members := make([]models.Employee, 0, len(department.Members))
		for _, employee := range department.Members {
			key := normalizePersonName(employee.Name)
			if key == "" {
				continue
			}
			if existing, ok := seen[key]; ok {
				seen[key] = mergeEmployee(existing, employee)
				continue
			}
			seen[key] = employee
			members = append(members, employee)
		}
		department.Members = members
	}
}

func mergeEmployee(primary, duplicate models.Employee) models.Employee {
	if len(strings.TrimSpace(duplicate.Bio)) > len(strings.TrimSpace(primary.Bio)) {
		primary.Bio = duplicate.Bio
	}
	if len(strings.TrimSpace(duplicate.Title)) > len(strings.TrimSpace(primary.Title)) {
		primary.Title = duplicate.Title
	}
	if primary.PublicSource == "" {
		primary.PublicSource = duplicate.PublicSource
	}
	if primary.PublicEmail == "" {
		primary.PublicEmail = duplicate.PublicEmail
		primary.EmailSource = duplicate.EmailSource
	}
	if primary.AsOf == "" {
		primary.AsOf = duplicate.AsOf
	}
	if primary.Verification == "" {
		primary.Verification = duplicate.Verification
	}
	primary.Tags = appendUniqueStrings(primary.Tags, duplicate.Tags...)
	primary.VerifiedBy = appendUniqueStrings(primary.VerifiedBy, duplicate.VerifiedBy...)
	return primary
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]bool, len(values)+len(additions))
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range additions {
		if value != "" && !seen[value] {
			values = append(values, value)
			seen[value] = true
		}
	}
	return values
}

// removeVacancyPlaceholders protects the chart when a model mistakes a job
// posting title for a person's name. Real employees normally have a distinct
// name and title; role-only rows are safe to discard before validation/export.
func removeVacancyPlaceholders(chart *models.OrgChart) {
	if chart == nil {
		return
	}
	leadership := chart.Leadership[:0]
	for _, employee := range chart.Leadership {
		if !isVacancyPlaceholder(employee.Name, employee.Title, employee.Bio) {
			leadership = append(leadership, employee)
		}
	}
	chart.Leadership = leadership
	for index := range chart.Departments {
		department := &chart.Departments[index]
		members := department.Members[:0]
		for _, employee := range department.Members {
			if !isVacancyPlaceholder(employee.Name, employee.Title, employee.Bio) {
				members = append(members, employee)
			}
		}
		department.Members = members
		if isVacancyPlaceholder(department.Head, department.Head, "") {
			department.Head = ""
		}
	}
}

func isVacancyPlaceholder(name, title, bio string) bool {
	name = strings.ToLower(strings.Join(strings.Fields(name), " "))
	title = strings.ToLower(strings.Join(strings.Fields(title), " "))
	bio = strings.ToLower(strings.Join(strings.Fields(bio), " "))
	if name == "" {
		return true
	}
	for _, marker := range []string{"vacancy", "job opening", "open position", "requisition", "apply now", "we are hiring", "not specified"} {
		if strings.Contains(name, marker) || strings.Contains(title, marker) || strings.Contains(bio, marker) {
			return true
		}
	}
	for _, groupLabel := range []string{
		"leadership team", "executive team", "management team", "senior management",
		"executive leadership", "board of directors", "board members", "our team",
		"the team", "team members",
	} {
		if name == groupLabel {
			return true
		}
	}
	if name != title || title == "" {
		return false
	}
	for _, role := range []string{"chief", "officer", "president", "vice president", "director", "manager", "head", "lead", "engineer", "analyst", "specialist", "coordinator", "supervisor", "procurement", "purchasing", "logistics", "operations"} {
		if strings.Contains(name, role) {
			return true
		}
	}
	return false
}

// entityGateActive reports whether the gate filters anything. It is kept on the agent
// so the ranking helpers, which are free functions, can be told without reaching into
// the engine.
func (ca *CompanyAgent) entityGateActive() bool {
	return ca.engine == nil || ca.engine.EntityGateActive()
}

// entityMatcherFor builds the run's entity matcher, or nil when the gate is off. A
// nil matcher means "no gate" everywhere it is used, which keeps the off mode a true
// comparison baseline rather than a subtly different pipeline.
func (ca *CompanyAgent) entityMatcherFor(brief models.CompanyBrief) *entityMatcher {
	if ca.engine != nil && !ca.engine.EntityGateActive() {
		return nil
	}
	return newEntityMatcher(brief)
}

func (ca *CompanyAgent) reviewChart(ctx context.Context, brief models.CompanyBrief, chart *models.OrgChart, evidence []models.Evidence, sessionID string, eventCh chan<- models.AgentEvent) models.QualityReview {
	var review models.QualityReview
	entityLine := ""
	if matcher := ca.entityMatcherFor(brief); matcher != nil {
		entityLine = "\n\nResearched entity:\n" + matcher.promptLine()
	}
	request := fmt.Sprintf("Brief:\n%+v\n\nDraft chart:\n%+v\n\nEvidence:\n%s%s", brief, *chart, formatEvidence(evidence), entityLine)
	llmCtx, cancel := context.WithTimeout(ctx, companyReviewTimeout)
	defer cancel()
	err := ca.engine.GenerateStructured(llmCtx, `Act as a strict research quality reviewer. Evaluate whether the draft answers the brief and whether claims are supported by the supplied evidence. Return JSON with verdict (pass or revise), score from 0 to 1, gaps, contradictions, unsupported_claims, reason, and at most four focused follow_up_queries. Use revise only for material gaps or contradictions. Never invent facts. Evidence items are stamped with the date their source proves (as of YYYY-MM-DD), so also flag any person whose newest source is years old, and report as a contradiction when one title — for example Chief Financial Officer — is claimed by two different people. Every person in the chart must be tied to the researched company named in the request: report anyone whose evidence does not connect to that company as an unsupported claim. People the evidence names at the company belong in the chart even when their function was not in the brief's Areas; do not report their presence as a gap or an unsupported claim while their evidence stands.`, request, &review)
	review.Verdict = strings.ToLower(strings.TrimSpace(review.Verdict))
	if err != nil || review.Verdict == "" {
		// Never report a failed or empty review as a passing one: doing so hides
		// the failure and lets unsupported output through unchecked.
		if err != nil {
			log.Printf("[WARN][session %s] QC review unavailable: %v", sessionID, err)
			review.Reason = fmt.Sprintf("QC review unavailable: %v", err)
		} else {
			log.Printf("[WARN][session %s] QC review returned no verdict", sessionID)
			review.Reason = "QC review returned no verdict"
		}
		review.Verdict = "unavailable"
		review.Score = 0
		review.Gaps = nil
		review.Contradictions = nil
		review.UnsupportedClaims = nil
		review.FollowUpQueries = nil
		return review
	}
	if review.Verdict != "revise" {
		review.Verdict = "pass"
	}
	Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventThinking, Step: 9, Content: fmt.Sprintf("QC review: %s (%.2f)%s", review.Verdict, review.Score, review.Reason)})
	return review
}

// AnswerQuestion answers a post-run company question from retained evidence or fresh search.
func (ca *CompanyAgent) AnswerQuestion(ctx context.Context, state *models.CompanyResearchContext, chart *models.OrgChart, question string) (*models.QuestionAnswer, error) {
	question = strings.TrimSpace(question)
	if state == nil || chart == nil || question == "" {
		return nil, fmt.Errorf("company research context, chart, and question are required")
	}

	var answer models.QuestionAnswer
	cachedPrompt := fmt.Sprintf("Company brief:\n%+v\n\nOrg chart:\n%+v\n\nCached evidence:\n%s\n\nQuestion: %s", state.Brief, *chart, formatEvidence(state.Evidence), question)
	cachedCtx, cancel := context.WithTimeout(ctx, companyQuestionTimeout)
	defer cancel()
	err := ca.engine.GenerateStructured(cachedCtx, `Answer the user's question using only the supplied company research and cached evidence. If the evidence is insufficient or contradictory, set needs_fresh_search to true and provide a focused search_query. Otherwise answer directly, cite relevant source URLs in sources, and set needs_fresh_search to false. Return JSON.`, cachedPrompt, &answer)
	if err == nil && strings.TrimSpace(answer.Answer) != "" && !answer.NeedsFreshSearch {
		answer.Question = question
		answer.Sources = evidenceSources(state.Evidence)
		return &answer, nil
	}

	query := strings.TrimSpace(answer.SearchQuery)
	if query == "" {
		query = state.Brief.CompanyName + " " + question
	}
	freshResults, err := ca.engine.SearchFresh(ctx, query, ca.engine.MaxSearchResults())
	if err != nil {
		return nil, fmt.Errorf("fresh question search failed: %w", err)
	}
	freshResults = filterBlockedSearchResults(freshResults)
	// The question path is gated too: a fresh follow-up search is exactly where an
	// unrelated company's page can enter a finished chart.
	matcher := ca.entityMatcherFor(state.Brief)
	freshRanked := make([]models.RankedResult, 0, len(freshResults))
	for index, result := range freshResults {
		if index >= ca.engine.MaxScrapes() {
			break
		}
		freshRanked = append(freshRanked, models.RankedResult{URL: result.Link, Relevant: true, Score: 1 - float64(index)/100, Reason: "selected for question follow-up", EvidenceType: "question_search"})
	}
	freshEvidence, _, _ := ca.scrapeRanked(ctx, "question", freshRanked, freshResults, nil, nil, matcher)
	combinedEvidence := append(append([]models.Evidence{}, state.Evidence...), freshEvidence...)
	var finalAnswer models.QuestionAnswer
	finalPrompt := fmt.Sprintf("Company brief:\n%+v\n\nOrg chart:\n%+v\n\nOriginal evidence:\n%s\n\nFresh search query: %s\nFresh search results:\n%s\n\nFresh evidence:\n%s\n\nQuestion: %s", state.Brief, *chart, formatEvidence(state.Evidence), query, FormatSearchSnippets(freshResults), formatEvidence(freshEvidence), question)
	finalCtx, finalCancel := context.WithTimeout(ctx, companyQuestionTimeout)
	defer finalCancel()
	if err := ca.engine.GenerateStructured(finalCtx, `Answer the user's company research question using the supplied original and fresh evidence. Prefer direct evidence, acknowledge uncertainty, and cite source URLs in sources. Return JSON with question, answer, confidence, and sources.`, finalPrompt, &finalAnswer); err != nil {
		return nil, fmt.Errorf("question answer synthesis failed: %w", err)
	}
	finalAnswer.Question = question
	finalAnswer.SearchQuery = query
	finalAnswer.UsedFreshSearch = true
	finalAnswer.NeedsFreshSearch = false
	finalAnswer.Sources = evidenceSources(combinedEvidence)
	return &finalAnswer, nil
}

func appendUniqueSearchResults(existing, additions []models.SearchResult) []models.SearchResult {
	seen := make(map[string]bool, len(existing)+len(additions))
	for _, result := range existing {
		seen[strings.ToLower(strings.TrimSpace(result.Link))] = true
	}
	for _, result := range additions {
		key := strings.ToLower(strings.TrimSpace(result.Link))
		if key != "" && !seen[key] {
			seen[key] = true
			existing = append(existing, result)
		}
	}
	return existing
}

func appendUniqueEvidence(existing, additions []models.Evidence) []models.Evidence {
	seen := make(map[string]bool, len(existing)+len(additions))
	for _, item := range existing {
		seen[item.URL] = true
	}
	for _, item := range additions {
		if item.URL != "" && !seen[item.URL] {
			seen[item.URL] = true
			existing = append(existing, item)
		}
	}
	return existing
}

func evidenceSources(evidence []models.Evidence) []string {
	sources := make([]string, 0, len(evidence))
	for _, item := range evidence {
		sources = append(sources, item.URL)
	}
	return sources
}

func evidenceImages(evidence []models.Evidence) []models.Image {
	const maxImages = 24
	images := make([]models.Image, 0, maxImages)
	seen := make(map[string]bool)
	for _, item := range evidence {
		for _, image := range item.Images {
			key := normalizeURL(image.URL)
			if key == "" || seen[key] || len(images) >= maxImages {
				continue
			}
			seen[key] = true
			images = append(images, image)
		}
	}
	return images
}

func searchSnippetEvidence(ranked []models.RankedResult, results []models.SearchResult, limit int, matcher *entityMatcher) []models.Evidence {
	byURL := make(map[string]models.SearchResult, len(results))
	for _, result := range results {
		byURL[normalizeURL(result.Link)] = result
	}
	evidence := make([]models.Evidence, 0, limit)
	for _, item := range ranked {
		if len(evidence) >= limit {
			break
		}
		result, ok := byURL[normalizeURL(item.URL)]
		if !ok || strings.TrimSpace(result.Snippet) == "" {
			continue
		}
		if !snippetNamesEntity(result, matcher) {
			continue
		}
		entry := models.Evidence{
			URL:          result.Link,
			Title:        result.Title,
			Content:      result.Snippet,
			Score:        item.Score * 0.75,
			EvidenceType: "search_snippet",
		}
		if when, ok := searchResultDate(result); ok {
			entry.AsOf = when
			entry.DateSignal = dateSignalSearch
		}
		evidence = append(evidence, entry)
	}
	return evidence
}

// maxCompanyRegistryPages bounds the WeeDoo enrichment in company mode: the
// same three-page cap the related-entity segment uses.
const maxCompanyRegistryPages = 3

// constructedRegistryMarker marks a registry candidate that was constructed
// from the company name and CIN rather than found by a search hit; the fetched
// page must name the company before its content becomes evidence.
const constructedRegistryMarker = "constructed registry URL"

// companyRegistrationNumberPattern matches an Indian CIN — 21 characters: L/U,
// five digits, two letters, four digits, three letters, six digits — which
// registry pages and directory URLs carry.
var companyRegistrationNumberPattern = regexp.MustCompile(`(?i)\b[LU]\d{5}[A-Z]{2}\d{4}[A-Z]{3}\d{6}\b`)

// companyRegistrationNumber returns the Indian CIN for the run: the operator's
// registration id when it is a CIN, else the first CIN the kept search results
// published (Economictimes, ZaubaCorp and TheCompanyCheck URLs carry it).
func companyRegistrationNumber(brief models.CompanyBrief, results []models.SearchResult) string {
	if id := strings.ToUpper(strings.TrimSpace(brief.RegistrationID)); companyRegistrationNumberPattern.MatchString(id) {
		return id
	}
	for _, result := range results {
		haystack := strings.ToUpper(result.Title + " " + result.Snippet + " " + result.Link)
		if cin := companyRegistrationNumberPattern.FindString(haystack); cin != "" {
			return cin
		}
	}
	return ""
}

// registrySlugVariants renders the company name the way WeeDoo's company-page
// slugs spell it — uppercase, non-alphanumerics collapsed to hyphens — with the
// Limited/Ltd suffix spelled both ways, since registries index both forms.
func registrySlugVariants(company string) []string {
	name := strings.ToUpper(strings.TrimSpace(company))
	if name == "" {
		return nil
	}
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, name)
	slug = strings.Trim(slug, "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	if slug == "" {
		return nil
	}
	variants := []string{slug}
	ltd := strings.Replace(slug, "-LIMITED-", "-LTD-", 1)
	if ltd == slug && strings.HasSuffix(slug, "-LIMITED") {
		ltd = strings.TrimSuffix(slug, "-LIMITED") + "-LTD"
	}
	if ltd != slug {
		variants = append(variants, ltd)
	}
	return variants
}

// isCompanyRegistryURL reports whether a URL is a WeeDoo registry page — the
// Indian company directory whose pages list directors, DINs, addresses and
// group links.
func isCompanyRegistryURL(targetURL string) bool {
	return strings.Contains(strings.ToLower(targetURL), "weedoo.")
}

// companyRegistryCandidates assembles the WeeDoo registry pages to fetch:
// first the registry URLs the run's own search hits surfaced, then — because
// the search provider barely indexes WeeDoo and site: queries usually return
// nothing — the directory URL constructed from the company name and its CIN
// (the operator's registration id, or a CIN a registry snippet published).
// Every candidate still has to pass the entity gate or, for a constructed URL,
// name the company once fetched.
func companyRegistryCandidates(brief models.CompanyBrief, results []models.SearchResult, matcher *entityMatcher) []models.SearchResult {
	candidates := make([]models.SearchResult, 0, maxCompanyRegistryPages)
	seen := make(map[string]bool)
	for _, result := range results {
		if len(candidates) >= maxCompanyRegistryPages {
			break
		}
		link := strings.TrimSpace(result.Link)
		if !isCompanyRegistryURL(link) || seen[normalizeURL(link)] {
			continue
		}
		if !snippetNamesEntity(result, matcher) {
			continue
		}
		seen[normalizeURL(link)] = true
		candidates = append(candidates, result)
	}
	if len(candidates) >= maxCompanyRegistryPages {
		return candidates
	}
	cin := companyRegistrationNumber(brief, results)
	if cin == "" {
		return candidates
	}
	for _, slug := range registrySlugVariants(brief.CompanyName) {
		if len(candidates) >= maxCompanyRegistryPages {
			break
		}
		link := "https://weedoo.in/company/" + slug + "/" + cin
		if seen[normalizeURL(link)] {
			continue
		}
		seen[normalizeURL(link)] = true
		candidates = append(candidates, models.SearchResult{
			Title:   "WeeDoo registry page for " + brief.CompanyName,
			Link:    link,
			Snippet: constructedRegistryMarker + " (company name + CIN)",
		})
	}
	return candidates
}

// constructedPageNamesCompany verifies a constructed WeeDoo URL after the
// fetch: the page must name the company — through the entity matcher, or a
// plain name check when the gate is off — before its content becomes evidence.
func constructedPageNamesCompany(matcher *entityMatcher, brief models.CompanyBrief, scrape *models.ScrapeResult) bool {
	if matcher != nil {
		return matcher.match(scrape.CanonicalURL, scrape.Title, scrape.Content).Matched
	}
	return strings.Contains(strings.ToLower(scrape.Content), strings.ToLower(strings.TrimSpace(brief.CompanyName)))
}

// allSearchSnippetEvidence keeps the public snippet for every collected result.
// Full-page evidence remains preferred by appendUniqueEvidence, while snippets
// preserve useful indexed facts when a page is blocked, paywalled, or simply
// outside the scrape budget.
func allSearchSnippetEvidence(results []models.SearchResult, matcher *entityMatcher) []models.Evidence {
	evidence := make([]models.Evidence, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.Snippet) == "" || !snippetNamesEntity(result, matcher) {
			continue
		}
		sourceType := "search_snippet"
		evidenceType := "search_snippet"
		quality := 0.2
		switch {
		case isIndexedProfileSourceURL(result.Link):
			sourceType = "indexed_profile"
			evidenceType = "search_snippet_fallback"
			quality = 0.25
		case isCompanyRegistryURL(result.Link):
			sourceType = "company_registry"
			quality = 0.3
		case matchesPromotedCompanyAggregator(result.Link):
			sourceType = "aggregator"
			quality = 0.25
		case matchesGlobalCompanySource(result.Link):
			sourceType = "global_source"
			quality = 0.25
		case matchesAlternativeProfileSource(result.Link):
			sourceType = "aggregator"
			quality = 0.1
		case strings.Contains(strings.ToLower(result.Link), "naukri."):
			sourceType = "aggregator"
			quality = 0.1
		case isBlockedSourceURL(result.Link):
			sourceType = "blocked"
			quality = 0.1
		}
		entry := models.Evidence{
			URL:          result.Link,
			Title:        result.Title,
			Content:      result.Snippet,
			EvidenceType: evidenceType,
			SourceType:   sourceType,
			QualityScore: quality,
		}
		if when, ok := searchResultDate(result); ok {
			entry.AsOf = when
			entry.DateSignal = dateSignalSearch
		}
		evidence = append(evidence, entry)
	}
	return evidence
}

// planQueries returns the run's query plan and how many planned queries the entity
// gate dropped for not naming the company.
func (ca *CompanyAgent) planQueries(ctx context.Context, brief models.CompanyBrief) (models.QueryPlan, int) {
	var plan models.QueryPlan
	request := fmt.Sprintf("Company: %s\nDomains: %s\nAliases: %s\nRegistration: %s\nIndustry: %s\nCountry: %s\nCity: %s\nAreas: %s\nOutput: %s\nTime: %s\nNotes: %s",
		brief.CompanyName, strings.Join(entityDomains(brief), ", "), strings.Join(brief.Aliases, ", "), brief.RegistrationID, brief.Industry,
		brief.Country, brief.City, strings.Join(brief.AreasOfInterest, ", "),
		strings.Join(brief.DesiredOutput, ", "), brief.TimeScope, brief.UserNotes)
	request += fmt.Sprintf("\nPerson of interest: %s", brief.PersonOfInterest)
	llmCtx, cancel := context.WithTimeout(ctx, companyPlanningTimeout)
	defer cancel()
	err := ca.engine.GenerateStructured(llmCtx, `Create a focused company web research query plan. Return JSON with a queries array. Each query needs query, purpose, target_area, and priority from 1 to 5. EVERY query must contain the company name, one of its aliases, or its domain — a query without one of those is discarded before it runs, because an unanchored query returns another company's pages that merely look relevant. Prefer "site:<domain>" queries for the company's own pages. Always cover leadership and locations. Add functional departments only when they are explicitly listed in Areas; do not broaden an unspecified request into procurement, marketing, finance, engineering, or generic department searches. Write department queries against the company ("<company> chief procurement officer", not "chief procurement officer India"). Conference speaker lists may only be searched together with the company name. Use title variants relevant to any explicitly requested department. You may use site:linkedin.com/in/ in search queries to find public profile snippets, but do not assume LinkedIn pages can be directly scraped or that snippets prove employment without corroboration. `+profileSourceHint(), request, &plan)
	if err != nil || len(plan.Queries) == 0 {
		return fallbackQueryPlan(brief), 0
	}
	// The planner is instructed to anchor every query, but an LLM plan is not a
	// guarantee, and an unanchored query is what produced a 2016 conference archive
	// and a Malaysian research paper in one live run. The filter is deterministic.
	if ca.engine != nil && !ca.engine.EntityGateActive() {
		return limitQueryPlan(plan, maxCompanySearchQueries), 0
	}
	enforced, dropped := enforceEntityQueries(limitQueryPlan(plan, maxCompanySearchQueries), brief)
	return enforced, dropped
}

// enforceEntityQueries drops planned queries that are not anchored on the company and
// refills the plan with anchored ones, returning the enforced plan and how many
// queries it dropped. It is the earliest gate: a query that is not about the company
// never runs, so the pages it would have returned are never fetched, ranked or mined.
func enforceEntityQueries(plan models.QueryPlan, brief models.CompanyBrief) (models.QueryPlan, int) {
	matcher := newEntityMatcher(brief)
	kept := make([]models.PlannedQuery, 0, len(plan.Queries))
	seen := make(map[string]bool, len(plan.Queries))
	dropped := 0
	for _, query := range plan.Queries {
		trimmed := strings.TrimSpace(query.Query)
		if trimmed == "" {
			dropped++
			continue
		}
		if !matcher.covers(trimmed) {
			dropped++
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			dropped++
			continue
		}
		seen[key] = true
		query.Query = trimmed
		kept = append(kept, query)
	}

	// Refill with queries that are safe by construction, which also guarantees a
	// site:<domain> query when a domain is known. The dedicated indexed-profile slots
	// are reserved so a full LLM plan cannot crowd them out.
	reserve := len(indexedProfileQueries(brief)) + len(personOfInterestQueries(brief))
	kept = limitQueryPlan(models.QueryPlan{Queries: kept}, maxCompanySearchQueries-reserve).Queries
	refill := entityAnchoredQueries(brief)
	for _, personQuery := range personOfInterestQueries(brief) {
		key := strings.ToLower(personQuery.Query)
		if !seen[key] {
			refill = append([]models.PlannedQuery{personQuery}, refill...)
		}
	}
	for _, indexed := range indexedProfileQueries(brief) {
		key := strings.ToLower(indexed.Query)
		if !seen[key] {
			refill = append([]models.PlannedQuery{indexed}, refill...)
		}
	}
	for _, anchor := range refill {
		if len(kept) >= maxCompanySearchQueries {
			break
		}
		key := strings.ToLower(strings.TrimSpace(anchor.Query))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		kept = append(kept, anchor)
	}
	return limitQueryPlan(models.QueryPlan{Queries: kept}, maxCompanySearchQueries), dropped
}

func personOfInterestQueries(brief models.CompanyBrief) []models.PlannedQuery {
	person := strings.TrimSpace(brief.PersonOfInterest)
	company := strings.TrimSpace(brief.CompanyName)
	if person == "" || company == "" {
		return nil
	}
	personQuery := fmt.Sprintf("%q %s", person, companyNameSearchQuery(company))
	return []models.PlannedQuery{
		{Query: personQuery, Purpose: "validate the person of interest against the company", TargetArea: "person of interest", Priority: 1},
		{Query: `site:linkedin.com/in/ ` + personQuery, Purpose: "find the person of interest in LinkedIn's Google index", TargetArea: "person of interest", Priority: 1},
		{Query: personQuery + ` (current OR appointed OR director OR executive OR leadership)`, Purpose: "validate the person's current company role", TargetArea: "person of interest", Priority: 1},
	}
}

// entityAnchoredQueries are the queries that cannot stray: they either restrict the
// search to the company's own domain or name the company outright. The location is
// included with the name because it is the cheapest way to disambiguate a company
// whose name is also a person's name.
func entityAnchoredQueries(brief models.CompanyBrief) []models.PlannedQuery {
	base := strings.TrimSpace(strings.Join([]string{brief.CompanyName, brief.Country, brief.City}, " "))
	queries := make([]models.PlannedQuery, 0, 6)
	if domain := normalizeDomain(brief.Domain); domain != "" {
		queries = append(queries,
			models.PlannedQuery{Query: "site:" + domain + " leadership management team", Purpose: "read the company's own leadership pages", TargetArea: "leadership", Priority: 1},
			models.PlannedQuery{Query: "site:" + domain + " offices locations contact", Purpose: "read the company's own locations", TargetArea: "locations", Priority: 3},
			models.PlannedQuery{Query: "site:" + domain, Purpose: "any other page on the company's own domain", TargetArea: "general", Priority: 4},
		)
	}
	if base != "" {
		queries = append(queries,
			models.PlannedQuery{Query: base + " leadership executive team management", Purpose: "identify leadership", TargetArea: "leadership", Priority: 1},
		)
	}
	// The indexed profile sources and promoted aggregators publish the same
	// title-and-employer facts a LinkedIn profile would; reserve their slots so they
	// cannot be crowded out.
	queries = append(queries, indexedProfileQueries(brief)...)
	queries = append(queries, personOfInterestQueries(brief)...)
	return queries
}

// indexedProfileQueries reserves one company-name-only Google-index slot for LinkedIn
// and each promoted aggregator, plus LinkedIn role-specific slots. Their snippets are
// the fallback when the profile itself is unavailable.
func indexedProfileQueries(brief models.CompanyBrief) []models.PlannedQuery {
	company := strings.TrimSpace(brief.CompanyName)
	if company == "" {
		return nil
	}
	nameQuery := companyNameSearchQuery(company)
	queries := []models.PlannedQuery{
		{Query: `site:linkedin.com/in/ ` + nameQuery, Purpose: "find people associated with the company in LinkedIn's Google index", TargetArea: "leadership", Priority: 1},
	}
	for _, host := range companySourceHosts(brief) {
		queries = append(queries, models.PlannedQuery{
			Query:      `site:` + host + ` ` + nameQuery,
			Purpose:    "find company records in " + host + "'s Google index",
			TargetArea: "company profile",
			Priority:   1,
		})
	}
	for _, area := range brief.AreasOfInterest {
		if strings.TrimSpace(area) == "" {
			continue
		}
		queries = append(queries, models.PlannedQuery{
			Query:      `site:linkedin.com/in/ ` + nameQuery + ` ` + strings.TrimSpace(area),
			Purpose:    "find people for the requested focus area in LinkedIn's Google index",
			TargetArea: strings.TrimSpace(area),
			Priority:   1,
		})
	}
	if registrationID := strings.TrimSpace(brief.RegistrationID); registrationID != "" {
		for _, host := range companySourceHosts(brief) {
			queries = append(queries, models.PlannedQuery{
				Query:      `site:` + host + ` ` + fmt.Sprintf("%q", registrationID),
				Purpose:    "find the company registration record in " + host + "'s Google index",
				TargetArea: "company profile",
				Priority:   2,
			})
		}
	}
	return queries
}

// companyNameSearchQuery keeps company-only slots focused while covering common
// legal-name variants used by registries and indexed company pages.
func companyNameSearchQuery(company string) string {
	trimmed := strings.TrimSpace(company)
	variants := []string{fmt.Sprintf("%q", trimmed)}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasSuffix(lower, " limited"):
		variants = append(variants, fmt.Sprintf("%q", strings.TrimSpace(trimmed[:len(trimmed)-len("limited")])+" LTD"))
	case strings.HasSuffix(lower, " ltd"):
		variants = append(variants, fmt.Sprintf("%q", strings.TrimSpace(trimmed[:len(trimmed)-len("ltd")])+" Limited"))
	}
	if len(variants) == 1 {
		return variants[0]
	}
	return "(" + strings.Join(variants, " OR ") + ")"
}

func fallbackQueryPlan(brief models.CompanyBrief) models.QueryPlan {
	base := strings.TrimSpace(strings.Join([]string{brief.CompanyName, brief.Country, brief.City}, " "))
	// Anchored queries come first: when the planner fails, the company's own domain
	// is the best material available, and every query below still names the company.
	queries := entityAnchoredQueries(brief)
	queries = append(queries,
		models.PlannedQuery{Query: base + " offices locations headquarters", Purpose: "identify locations", TargetArea: "locations", Priority: 3},
	)
	for _, area := range brief.AreasOfInterest {
		area = strings.TrimSpace(area)
		if area != "" {
			queries = append(queries, models.PlannedQuery{Query: base + " " + area, Purpose: "investigate requested area", TargetArea: area, Priority: 2})
		}
	}
	// The conference angle stays, but anchored on the company: a bare industry query
	// is what pulled a 2016 summit delegate list into a live run.
	if industry := strings.TrimSpace(brief.Industry); industry != "" {
		queries = append(queries, models.PlannedQuery{Query: base + " " + industry + " conference speaker", Purpose: "identify industry conference speakers", TargetArea: "industry conference speakers", Priority: 3})
	}
	return limitQueryPlan(models.QueryPlan{Queries: queries}, maxCompanySearchQueries)
}

func limitQueryPlan(plan models.QueryPlan, limit int) models.QueryPlan {
	seen := make(map[string]bool)
	queries := make([]models.PlannedQuery, 0, limit)
	for _, query := range plan.Queries {
		query.Query = strings.TrimSpace(query.Query)
		key := strings.ToLower(query.Query)
		if query.Query == "" || seen[key] || len(queries) >= limit {
			continue
		}
		seen[key] = true
		queries = append(queries, query)
	}
	return models.QueryPlan{Queries: queries}
}

func (ca *CompanyAgent) searchPlan(ctx context.Context, sessionID string, plan models.QueryPlan, eventCh chan<- models.AgentEvent, matcher *entityMatcher) ([]models.SearchResult, *models.SearchTrace, error) {
	var all []models.SearchResult
	seen := make(map[string]bool)
	trace := &models.SearchTrace{Queries: make([]models.QueryTrace, 0, len(plan.Queries))}
	var lastErr error
	type searchOutcome struct {
		results []models.SearchResult
		err     error
	}
	outcomes := make([]searchOutcome, len(plan.Queries))
	workers := companySearchConcurrency
	if workers > len(plan.Queries) {
		workers = len(plan.Queries)
	}
	jobs := make(chan int, len(plan.Queries))
	var waitGroup sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for index := range jobs {
				planned := plan.Queries[index]
				outcomes[index].results, outcomes[index].err = ca.engine.SearchCached(ctx, planned.Query, ca.engine.MaxSearchResults())
			}
		}()
	}
	for index, planned := range plan.Queries {
		Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolCall, Step: 3 + index, ToolName: "serper_search", ToolInput: planned.Query, Content: planned.Purpose})
		jobs <- index
	}
	close(jobs)
	waitGroup.Wait()

	for index, planned := range plan.Queries {
		results, err := outcomes[index].results, outcomes[index].err
		if err != nil {
			lastErr = err
			continue
		}
		usable := make([]models.SearchResult, 0, len(results))
		for _, result := range results {
			usable = append(usable, result)
			key := strings.ToLower(strings.TrimSpace(result.Link))
			if key != "" && !seen[key] {
				seen[key] = true
				all = append(all, result)
			}
		}
		// The trace records what this query returned and what the entity gate made of it,
		// per query — the raw material behind the Search results tab.
		// artifact, so a run's findings can be audited or reused outside the UI.
		kept, excluded := splitEntitySnippets(usable, matcher)
		trace.Queries = append(trace.Queries, models.QueryTrace{
			Query:    planned.Query,
			Purpose:  planned.Purpose,
			Kept:     kept,
			Excluded: excluded,
		})
	}
	if len(all) == 0 && lastErr != nil {
		return nil, trace, fmt.Errorf("company search failed: %w", lastErr)
	}
	Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolResult, Step: 7, ToolName: "serper_search", Content: fmt.Sprintf("Collected %d unique search results.", len(all))})
	return all, trace, nil
}

func (ca *CompanyAgent) rankResults(ctx context.Context, brief models.CompanyBrief, results []models.SearchResult) []models.RankedResult {
	bm25Ranked := bm25RankResults(brief, results)
	if len(bm25Ranked) == 0 {
		return bm25Ranked
	}
	bm25Ranked = applySourceSignals(brief, bm25Ranked, ca.entityGateActive())

	semanticCandidates := bm25Ranked
	if len(semanticCandidates) > 12 {
		semanticCandidates = semanticCandidates[:12]
	}
	candidateResults := make([]models.SearchResult, 0, len(semanticCandidates))
	byURL := make(map[string]models.SearchResult, len(results))
	for _, result := range results {
		byURL[normalizeURL(result.Link)] = result
	}
	for _, candidate := range semanticCandidates {
		if result, ok := byURL[normalizeURL(candidate.URL)]; ok {
			candidateResults = append(candidateResults, result)
		}
	}

	var response struct {
		RankedResults []models.RankedResult `json:"ranked_results"`
	}
	llmCtx, cancel := context.WithTimeout(ctx, companySemanticTimeout)
	defer cancel()
	semanticDone := make(chan error, 1)
	go func() {
		semanticDone <- ca.engine.GenerateStructured(llmCtx, `Rerank these top company research results semantically. Return JSON with ranked_results. Use only URLs present in the input. Mark results relevant only when they plausibly concern the requested company, location, and areas. Score from 0 to 1 and explain each decision.`, fmt.Sprintf("Brief: %+v\nTop BM25 results:\n%s", brief, FormatSearchSnippets(candidateResults)), &response)
	}()
	select {
	case err := <-semanticDone:
		if err == nil && len(response.RankedResults) > 0 {
			if normalized := normalizeRankedResults(response.RankedResults, candidateResults); len(normalized) > 0 {
				return pruneUnconnected(brief, mergeRankings(bm25Ranked, normalized), byURL)
			}
		}
	case <-llmCtx.Done():
	}
	return pruneUnconnected(brief, bm25Ranked, byURL)
}

func bm25RankResults(brief models.CompanyBrief, results []models.SearchResult) []models.RankedResult {
	if len(results) == 0 {
		return nil
	}
	query := tokenize(strings.Join([]string{
		brief.CompanyName,
		brief.Domain,
		brief.Industry,
		brief.Country,
		brief.City,
		strings.Join(brief.AreasOfInterest, " "),
	}, " "))
	if len(query) == 0 {
		return nil
	}

	documents := make([][]string, len(results))
	documentFrequency := make(map[string]int)
	totalLength := 0
	for index, result := range results {
		documents[index] = tokenize(result.Title + " " + result.Snippet + " " + result.Link)
		totalLength += len(documents[index])
		seen := make(map[string]bool)
		for _, token := range documents[index] {
			if !seen[token] {
				documentFrequency[token]++
				seen[token] = true
			}
		}
	}
	averageLength := float64(totalLength) / float64(len(documents))
	if averageLength == 0 {
		averageLength = 1
	}

	ranked := make([]models.RankedResult, 0, len(results))
	for index, result := range results {
		rawScore := bm25Score(query, documents[index], documentFrequency, len(documents), averageLength)
		score := rawScore / (rawScore + 2)
		reason := fmt.Sprintf("BM25 lexical score %.2f", rawScore)
		if brief.Domain != "" && strings.Contains(strings.ToLower(result.Link), strings.ToLower(brief.Domain)) {
			score += 0.15
			reason += "; official domain match"
		}
		if strings.Contains(strings.ToLower(result.Title+" "+result.Snippet), strings.ToLower(brief.CompanyName)) {
			score += 0.1
		}
		if score > 1 {
			score = 1
		}
		ranked = append(ranked, models.RankedResult{
			URL: result.Link, Title: result.Title, Snippet: result.Snippet,
			Relevant: score >= 0.2, Score: score, Reason: reason, EvidenceType: "bm25",
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	return ranked
}

func applySourceSignals(brief models.CompanyBrief, ranked []models.RankedResult, gateActive bool) []models.RankedResult {
	if brief.Domain == "" {
		brief.Domain = inferOfficialDomain(brief.CompanyName, ranked)
	}
	matcher := newEntityMatcher(brief)
	for index := range ranked {
		item := &ranked[index]
		profile := classifySource(brief, item.URL)
		item.SourceType = profile.sourceType
		item.Official = profile.official
		item.QualityScore = profile.quality
		// Entity gate: record how this candidate is tied to the company, and push the
		// ones that name it nowhere to the back. A page about another company can be
		// perfectly on-brief — a 2016 procurement summit archive, a wealth-management
		// roundup — which is the failure this prevents. The penalty keeps such a page
		// reachable while scrape budget remains, but it can never outrank a page that
		// carries the company's name, domain or registration id.
		if gateActive {
			if match := matcher.match(item.URL, item.Title, item.Snippet); match.Matched {
				item.EntityKey = match.Key
				item.Score += 0.05
				item.Reason += "; entity=" + match.Key
			} else {
				item.Score -= 0.35
				item.Reason += "; entity=none"
			}
		}
		item.Score += 0.18 * (profile.quality - 0.5)
		if profile.official {
			item.Score += 0.12
		}
		if profile.primary {
			item.Score += 0.14
			item.Reason += "; primary domain"
		}
		if profile.quality < 0.25 {
			item.Score -= 0.2
			item.Relevant = false
		}
		item.Reason += "; source=" + profile.sourceType
		if item.Score < 0 {
			item.Score = 0
		}
		if item.Score > 1 {
			item.Score = 1
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	return ranked
}

func inferOfficialDomain(companyName string, ranked []models.RankedResult) string {
	tokens := tokenize(companyName)
	if len(tokens) == 0 {
		return ""
	}
	for _, item := range ranked {
		parsed, err := url.Parse(item.URL)
		if err != nil {
			continue
		}
		host := rootHost(parsed.Hostname())
		joinedHost := strings.NewReplacer(".", "", "-", "", "_", "").Replace(host)
		matched := false
		for _, token := range tokens {
			if len(token) >= 4 && strings.Contains(joinedHost, token) {
				matched = true
				break
			}
		}
		if matched {
			return host
		}
	}
	return ""
}

// lowQualityHosts are hosts that are never evidence about a company: job boards, content
// farms, company directories, document dumps and social networks. Quality 0.1 puts them
// below the relevance floor, so they are not fetched at all.
var lowQualityHosts = []string{
	"naukri.", "ziprecruiter", "indeed.", "glassdoor.", "shine.",
	"ambitionbox.", "iipmr", "scribd.", "unlistedcart", "cphi-online.", "indiamart.", "tradeindia.",
	"exportersindia.", "studylib", "docslib", "coursehero", "yumpu", "issuu.", "calameo", "pubhtml5",
	"slideshare", "wikipedia.", "wikidata.", "facebook.", "instagram.", "tiktok.", "zoominfo.",
}

//var additionalLowQualityHosts = []string{
//	"zoominfo.", "rocketreach.", "twitter.", "x.com",
//}

type sourceProfile struct {
	sourceType string
	official   bool
	primary    bool
	quality    float64
}

func classifySource(brief models.CompanyBrief, targetURL string) sourceProfile {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return sourceProfile{sourceType: "unknown", quality: 0.1}
	}
	rawHost := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	host := rootHost(rawHost)
	path := strings.ToLower(parsed.Path)
	if isBlockedSourceURL(targetURL) {
		return sourceProfile{sourceType: "blocked", quality: 0}
	}
	if isIndexedProfileSourceURL(targetURL) {
		return sourceProfile{sourceType: "indexed_profile", quality: 0.45}
	}
	if companySourceProfile(brief) == SourceProfileGlobal && matchesGlobalCompanySource(targetURL) {
		return sourceProfile{sourceType: "global_source", quality: 0.45}
	}
	official := false
	primary := false
	if brief.Domain != "" {
		domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(brief.Domain), "www."))
		official = rawHost == domain || strings.HasSuffix(rawHost, "."+domain)
		primary = rawHost == domain
	}
	// Job boards, content farms, company directories and social networks are full of
	// people and titles, which is exactly why they look relevant — and why live runs
	// pulled sources from naukri, shine, iipmr, scribd, an Instagram reel and a document
	// dump. They rank with the aggregators, so they are dropped before they can seed a
	// roster.
	if containsAny(host, lowQualityHosts...) {
		return sourceProfile{sourceType: "aggregator", quality: 0.1}
	}
	if strings.Contains(host, "crunchbase.") {
		return sourceProfile{sourceType: "aggregator", quality: 0.1}
	}
	if strings.Contains(host, "linkedin.") {
		return sourceProfile{sourceType: "professional_profile", quality: 0.6}
	}
	// WeeDoo publishes registry-style company records — directors with DINs,
	// addresses, group and subsidiary links — the enrichment the related-entity
	// segment scrapes by hand. Company mode fetches these as a priority source
	// instead of leaving them at the bottom of the scrape ladder.
	if containsAny(host, "weedoo.") && companySourceProfile(brief) == SourceProfileIndia {
		return sourceProfile{sourceType: "company_registry", quality: 0.75}
	}
	if containsAny(host, "argusmedia", "spglobal", "platts") {
		return sourceProfile{sourceType: "industry_event", quality: 0.75}
	}
	if official && containsAny(path, "leadership", "executive", "management", "board", "team") {
		return sourceProfile{sourceType: "official_leadership", official: true, primary: primary, quality: 1}
	}
	if official && containsAny(path, "location", "office", "contact", "where-we-are") {
		return sourceProfile{sourceType: "official_locations", official: true, primary: primary, quality: 0.95}
	}
	if official && containsAny(path, "annual", "investor", "filing", "report") {
		return sourceProfile{sourceType: "official_report", official: true, primary: primary, quality: 0.95}
	}
	if official {
		return sourceProfile{sourceType: "official", official: true, primary: primary, quality: 0.9}
	}
	if containsAny(path, "leadership", "executive", "management", "board", "team") {
		return sourceProfile{sourceType: "leadership", quality: 0.7}
	}
	return sourceProfile{sourceType: "external", quality: 0.55}
}

func containsAny(value string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}

func bm25Score(query, document []string, documentFrequency map[string]int, documentCount int, averageLength float64) float64 {
	termFrequency := make(map[string]int)
	for _, token := range document {
		termFrequency[token]++
	}
	const k1 = 1.2
	const b = 0.75
	documentLength := float64(len(document))
	score := 0.0
	seenQueryTerms := make(map[string]bool)
	for _, term := range query {
		if seenQueryTerms[term] {
			continue
		}
		seenQueryTerms[term] = true
		frequency := float64(termFrequency[term])
		if frequency == 0 {
			continue
		}
		df := float64(documentFrequency[term])
		idf := math.Log(1 + (float64(documentCount)-df+0.5)/(df+0.5))
		normalizer := frequency + k1*(1-b+b*documentLength/averageLength)
		score += idf * (frequency * (k1 + 1) / normalizer)
	}
	return score
}

func tokenize(value string) []string {
	var tokens []string
	var builder strings.Builder
	flush := func() {
		if builder.Len() > 1 {
			tokens = append(tokens, builder.String())
		}
		builder.Reset()
	}
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			builder.WriteRune(character)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

func mergeRankings(bm25Ranked, semanticRanked []models.RankedResult) []models.RankedResult {
	semanticByURL := make(map[string]models.RankedResult, len(semanticRanked))
	for _, item := range semanticRanked {
		semanticByURL[normalizeURL(item.URL)] = item
	}
	merged := make([]models.RankedResult, 0, len(bm25Ranked))
	for _, item := range bm25Ranked {
		if semantic, ok := semanticByURL[normalizeURL(item.URL)]; ok {
			item.Score = 0.65*item.Score + 0.35*semantic.Score
			item.Relevant = semantic.Relevant && item.Relevant
			item.Reason += "; semantic rerank: " + semantic.Reason
			item.EvidenceType = "bm25+semantic"
		}
		merged = append(merged, item)
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })
	return merged
}

func deterministicRank(brief models.CompanyBrief, result models.SearchResult) models.RankedResult {
	text := strings.ToLower(result.Title + " " + result.Snippet)
	score := 0.35
	if strings.Contains(text, strings.ToLower(brief.CompanyName)) {
		score += 0.3
	}
	for _, term := range append(brief.AreasOfInterest, "leadership", "executive", "department", "location") {
		term = strings.ToLower(strings.TrimSpace(term))
		if term != "" && strings.Contains(text, term) {
			score += 0.05
		}
	}
	if brief.Domain != "" && strings.Contains(strings.ToLower(result.Link), strings.ToLower(brief.Domain)) {
		score += 0.2
	}
	if score > 1 {
		score = 1
	}
	return models.RankedResult{URL: result.Link, Relevant: score >= 0.45, Score: score, Reason: "matched company and research terms", EvidenceType: "search_result"}
}

// companyNameStopWords lists corporate, geographic and industry words that appear
// in so many company names that matching one proves nothing about a search result.
var companyNameStopWords = map[string]bool{
	"ltd": true, "limited": true, "llc": true, "llp": true, "inc": true, "incorporated": true,
	"corp": true, "corporation": true, "company": true, "co": true, "plc": true, "gmbh": true,
	"pvt": true, "private": true, "holdings": true, "holding": true, "group": true,
	"industries": true, "industry": true, "international": true, "global": true,
	"enterprise": true, "enterprises": true, "services": true, "solutions": true,
	"technologies": true, "technology": true, "systems": true, "ventures": true,
	"india": true, "usa": true, "uk": true, "singapore": true, "dubai": true,
}

// companyTokens returns the distinctive tokens of a company name. Generic
// corporate suffixes are dropped, so "KLJ Resources Limited" is recognised by
// "klj" or "resources" and never by "limited".
func companyTokens(name string) []string {
	tokens := make([]string, 0, 4)
	for _, token := range tokenize(name) {
		if !companyNameStopWords[token] {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

// hasCompanyConnection reports whether a result plausibly concerns the target
// company: either its host matches the official domain, or its text (title,
// snippet or URL) carries a distinctive company-name token.
func hasCompanyConnection(brief models.CompanyBrief, tokens []string, result models.SearchResult) bool {
	if domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(brief.Domain), "www.")); domain != "" {
		if parsed, err := url.Parse(result.Link); err == nil {
			rawHost := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
			if rawHost == domain || strings.HasSuffix(rawHost, "."+domain) {
				return true
			}
		}
	}
	haystack := strings.ToLower(result.Title + " " + result.Snippet + " " + result.Link)
	for _, token := range tokens {
		if strings.Contains(haystack, token) {
			return true
		}
	}
	return false
}

// pruneUnconnected drops ranking candidates that show no connection to the target
// company.
//
// BM25 marks anything sharing the query's tokens as relevant (score >= 0.2), which
// in practice means every result the search engine returned — including other
// companies' profiles and off-domain pages that merely matched the query's
// keywords. Those candidates previously consumed scrape budget and polluted the
// evidence.
//
// The unfiltered set is returned when filtering would empty the list, so an
// unusual company name can never starve a run of candidates.
func pruneUnconnected(brief models.CompanyBrief, ranked []models.RankedResult, byURL map[string]models.SearchResult) []models.RankedResult {
	tokens := companyTokens(brief.CompanyName)
	if len(tokens) == 0 {
		return ranked
	}

	kept := make([]models.RankedResult, 0, len(ranked))
	for _, item := range ranked {
		result, known := byURL[normalizeURL(item.URL)]
		// Candidates with no snippet to inspect are kept rather than guessed away.
		if !known || hasCompanyConnection(brief, tokens, result) {
			kept = append(kept, item)
		}
	}
	if len(kept) == 0 {
		return ranked
	}
	if dropped := len(ranked) - len(kept); dropped > 0 {
		log.Printf("[RANK] dropped %d of %d candidates with no connection to %q", dropped, len(ranked), brief.CompanyName)
	}
	return kept
}

func normalizeRankedResults(ranked []models.RankedResult, results []models.SearchResult) []models.RankedResult {
	known := make(map[string]bool, len(results))
	for _, result := range results {
		known[normalizeURL(result.Link)] = true
	}
	filtered := ranked[:0]
	for _, item := range ranked {
		if known[normalizeURL(item.URL)] && item.Relevant {
			filtered = append(filtered, item)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Score > filtered[j].Score })
	return filtered
}

func normalizeURL(value string) string {
	trimmed := strings.TrimSpace(value)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return strings.TrimRight(strings.ToLower(trimmed), "/")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(strings.TrimPrefix(parsed.Host, "www."))
	parsed.Fragment = ""
	parsed.RawFragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.RawQuery != "" {
		if query, parseErr := url.ParseQuery(parsed.RawQuery); parseErr == nil {
			for key := range query {
				if trackingQueryParams[strings.ToLower(key)] {
					query.Del(key)
				}
			}
			// Encode sorts the keys, so two URLs that differ only in parameter
			// order collapse into one key.
			parsed.RawQuery = query.Encode()
		}
	}
	return strings.TrimRight(parsed.String(), "/")
}

// trackingQueryParams are query parameters that identify a campaign or a
// referrer rather than a distinct document. Stripping them keeps a page's
// tracking-link and canonical forms from counting as two separate sources.
var trackingQueryParams = map[string]bool{
	"utm_source": true, "utm_medium": true, "utm_campaign": true, "utm_term": true,
	"utm_content": true, "utm_id": true, "utm_name": true, "utm_reader": true,
	"gclid": true, "fbclid": true, "msclkid": true, "yclid": true, "twclid": true,
	"mc_cid": true, "mc_eid": true, "igshid": true, "ref_src": true, "si": true,
	"_ga": true, "hsctatracking": true,
}

// contentHash fingerprints a page's text so identical content served under
// several URLs is only collected once. Corporate sites are the common case:
// /about, /about-us and /company routinely return the same document, and paying
// for three scrapes and three copies of the text helps nobody.
func contentHash(content string) string {
	normalized := strings.Join(strings.Fields(strings.ToLower(content)), " ")
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// orderProfileEscalations returns the professional-profile candidates ordered by
// how likely the hard-URL ladder is to read them, preserving the ranker's order
// within each shape.
//
// It exists because the escalation cap is small and the shapes are not equally
// recoverable: measured across live runs, LinkedIn posts and company pages came
// back on the first render (1-2 s, 160-2,919 words) while every logged-out person
// profile exhausted both steps (15-25 s) and fell back to its snippet. Ordering by
// shape makes the cap spend its slots on pages that can actually be kept, and the
// remaining profiles still contribute their snippets as before.
func orderProfileEscalations(ranked []models.RankedResult) []models.RankedResult {
	profiles := make([]models.RankedResult, 0, len(ranked))
	for _, item := range ranked {
		if item.SourceType == "professional_profile" || item.SourceType == "indexed_profile" {
			profiles = append(profiles, item)
		}
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		return tools.HardURLShapeOf(profiles[i].URL).EscalationPriority() <
			tools.HardURLShapeOf(profiles[j].URL).EscalationPriority()
	})
	return profiles
}

func (ca *CompanyAgent) scrapeRanked(ctx context.Context, sessionID string, ranked []models.RankedResult, searchResults []models.SearchResult, existing []models.Evidence, eventCh chan<- models.AgentEvent, matcher *entityMatcher) ([]models.Evidence, []string, []models.ScrapeReportEntry) {
	return ca.scrapeRankedWithBrief(ctx, sessionID, models.CompanyBrief{}, ranked, searchResults, existing, eventCh, matcher)
}

func (ca *CompanyAgent) scrapeRankedWithBrief(ctx context.Context, sessionID string, brief models.CompanyBrief, ranked []models.RankedResult, searchResults []models.SearchResult, existing []models.Evidence, eventCh chan<- models.AgentEvent, matcher *entityMatcher) ([]models.Evidence, []string, []models.ScrapeReportEntry) {
	documentTarget := tools.DocumentTarget{
		CompanyName:     brief.CompanyName,
		Aliases:         append([]string(nil), brief.Aliases...),
		AreasOfInterest: append([]string(nil), brief.AreasOfInterest...),
		TimeScope:       brief.TimeScope,
	}
	ctx = tools.WithDocumentTarget(ctx, documentTarget)
	evidence := make([]models.Evidence, 0, ca.engine.MaxScrapes())
	sources := make([]string, 0, ca.engine.MaxScrapes())
	report := make([]models.ScrapeReportEntry, 0, ca.engine.MaxScrapes()*2)
	known := make(map[string]bool, len(existing))
	contentSeen := make(map[string]string, len(existing))
	for _, item := range existing {
		known[normalizeURL(item.URL)] = true
		if strings.TrimSpace(item.Content) != "" {
			contentSeen[contentHash(item.Content)] = item.URL
		}
	}
	usableScrapes := 0
	handler := newHardURLHandler(ca.engine)
	// Every fetch below runs on a context that stops short of the run's deadline, so the
	// two lookup passes always get their slice: the anchored lookups before synthesis and
	// the freshness/rescue pass after it. A run that spends everything here has no way to
	// tie a person to the company at all.
	scrapeCtx, cancelScrape := scrapeDeadlineContext(ctx)
	defer cancelScrape()
	// WeeDoo registry pages are fetched first, as a bounded priority source: an
	// Indian company's directory page lists directors with DINs, addresses and
	// group links that its Google snippet truncates — the same enrichment the
	// related-entity segment scrapes by hand. The pass is entity-gated like every
	// other source and spends the ordinary scrape budget, because it runs on the
	// same deadline that protects the lookup passes.
	registry := companyRegistryCandidates(brief, searchResults, matcher)
	constructed := make(map[string]bool)
	registryURLs := make([]string, 0, len(registry))
	for _, target := range registry {
		if strings.Contains(target.Snippet, constructedRegistryMarker) {
			constructed[normalizeURL(target.Link)] = true
		}
		registryURLs = append(registryURLs, target.Link)
	}
	if len(registryURLs) > 0 {
		Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolCall, Step: 8, ToolName: "web_scraper",
			ToolInput: registryURLs[0], Content: fmt.Sprintf("Scraping %d priority WeeDoo registry page(s)...", len(registryURLs))})
		successes := 0
		for index, outcome := range ca.engine.ScrapeCachedBatch(scrapeCtx, registryURLs) {
			if successes >= maxCompanyRegistryPages {
				break
			}
			target := registry[index]
			entry := models.ScrapeReportEntry{URL: target.Link, SourceType: "company_registry", Status: "failed"}
			if outcome.Err != nil {
				entry.Reason = outcome.Err.Error()
				report = append(report, entry)
				continue
			}
			scrape := outcome.Result
			if scrape == nil || strings.TrimSpace(scrape.Content) == "" {
				entry.Reason = "no usable registry page"
				report = append(report, entry)
				continue
			}
			if constructed[normalizeURL(target.Link)] && !constructedPageNamesCompany(matcher, brief, scrape) {
				// A constructed URL is a guess until the page itself proves it:
				// the fetched content must name the company, or the entry is
				// recorded as a miss and dropped.
				entry.Reason = "constructed registry page does not name the company"
				report = append(report, entry)
				continue
			}
			entry.Status = "success"
			entry.Reason = "priority registry enrichment (WeeDoo)"
			entry.WordCount = scrape.WordCount
			entry.QualityScore = scrape.QualityScore
			entry.Extractor = scrape.Extractor
			item := models.RankedResult{
				URL: target.Link, Title: target.Title, Snippet: target.Snippet, Score: 0.9,
				Reason: "priority registry enrichment", EvidenceType: "company_registry", SourceType: "company_registry",
			}
			known[normalizeURL(target.Link)] = true
			evidence = append(evidence, ca.evidenceWithOCR(scrapeCtx, item, scrape, &entry))
			sources = append(sources, target.Link)
			usableScrapes++
			successes++
			report = append(report, entry)
		}
	}
	// Professional profiles are attempted through the bounded ladder whenever it
	// is available: it renders public pages under its own budget, reports a wall it
	// cannot pass, and never solves a challenge. The ALLOW_PROFILE_SCRAPING opt-in
	// decides what happens otherwise — opted in, a profile enters the normal scrape
	// path below and its full text can contribute; not opted in, only its public
	// search snippet is kept.
	// The ladder's escalation slots are scarce (HARD_URLS_PER_RUN), so profile
	// candidates are offered in the order the ladder can actually read: a public
	// post or company page renders on the first step in a second or two, while a
	// logged-out person profile is an authwall that exhausts the ladder in 15-25 s.
	for _, item := range orderProfileEscalations(ranked) {
		if known[normalizeURL(item.URL)] {
			continue
		}
		if item.SourceType == "indexed_profile" {
			if snippet := snippetEvidenceForURL(item, searchResults, matcher); snippet != nil {
				evidence = append(evidence, *snippet)
			}
			known[normalizeURL(item.URL)] = true
			report = append(report, models.ScrapeReportEntry{
				URL: item.URL, SourceType: item.SourceType, Status: "skipped",
				Reason: "public search snippet retained; indexed profile sources are snippet-only",
			})
			continue
		}
		// A session the operator supplied is tried the cheap way first, and it does not need
		// ALLOW_PROFILE_SCRAPING: that flag gates the batch path, while a session is explicit
		// consent for this host and one authenticated GET is less intrusive than the render
		// the default already performs for every profile candidate.
		if session := fetchProfileWithSession(scrapeCtx, ca.engine, item.URL); session != nil {
			entry := models.ScrapeReportEntry{
				URL: item.URL, SourceType: item.SourceType, Status: "recovered",
				Reason:       "professional profile read with the exported session (no render)",
				WordCount:    session.WordCount,
				QualityScore: session.QualityScore,
				ImagesFound:  len(session.Images),
				Extractor:    session.Extractor,
			}
			evidence = append(evidence, ca.evidenceWithOCR(scrapeCtx, item, session, &entry))
			sources = append(sources, item.URL)
			known[normalizeURL(item.URL)] = true
			usableScrapes++
			report = append(report, entry)
			continue
		}
		if handler.available() {
			outcome := handler.handle(scrapeCtx, sessionID, item.URL, "professional profile", 8+len(evidence), eventCh, tools.HardScrapeOptions{})
			if outcome.Status == "recovered" && outcome.Result != nil {
				entry := models.ScrapeReportEntry{
					URL: item.URL, SourceType: item.SourceType, Status: "recovered",
					Reason:         "professional profile recovered by the hard-URL handler",
					WordCount:      outcome.Result.WordCount,
					QualityScore:   outcome.Result.QualityScore,
					ImagesFound:    len(outcome.Result.Images),
					Extractor:      outcome.Result.Extractor,
					HandlerStatus:  outcome.Status,
					HandlerTactics: outcome.Result.FallbackReason,
				}
				evidence = append(evidence, ca.evidenceWithOCR(scrapeCtx, item, outcome.Result, &entry))
				sources = append(sources, item.URL)
				known[normalizeURL(item.URL)] = true
				usableScrapes++
				report = append(report, entry)
				continue
			}
			report = append(report, models.ScrapeReportEntry{
				URL: item.URL, SourceType: item.SourceType, Status: "skipped",
				Reason:        "public search snippet retained; the hard-URL handler could not recover the profile",
				HandlerStatus: outcome.Status,
			})
			if snippet := snippetEvidenceForURL(item, searchResults, matcher); snippet != nil {
				evidence = append(evidence, *snippet)
			}
			if !ca.engine.ProfileScrapingEnabled() {
				known[normalizeURL(item.URL)] = true
			}
			continue
		}
		if ca.engine.ProfileScrapingEnabled() {
			// No ladder, but profiles are opted in, so the normal scrape path below
			// is what should fetch this page.
			continue
		}
		if snippet := snippetEvidenceForURL(item, searchResults, matcher); snippet != nil {
			evidence = append(evidence, *snippet)
			known[normalizeURL(item.URL)] = true
		}
		report = append(report, models.ScrapeReportEntry{URL: item.URL, SourceType: item.SourceType, Status: "skipped", Reason: "public search snippet retained; direct professional-profile scraping is disabled"})
	}
	attemptLimit := ca.engine.MaxScrapes() * 2
	candidates := selectScrapeCandidates(ranked, attemptLimit, ca.engine.ProfileScrapingEnabled())
	if brief.CompanyName != "" {
		unseen := candidates[:0]
		for _, candidate := range candidates {
			if !known[normalizeURL(candidate.URL)] {
				unseen = append(unseen, candidate)
			}
		}
		var triageReport []models.ScrapeReportEntry
		candidates, triageReport = ca.triageDocumentCandidates(scrapeCtx, brief, unseen, searchResults)
		report = append(report, triageReport...)
	}

	// Candidates are fetched in small parallel batches rather than one at a time.
	// Each fetch is dominated by network latency, so four concurrent fetches turn
	// roughly 10 x 1.5s of serial waiting into about a quarter of that. The batch
	// is sized to the remaining scrape budget plus one worker's worth of slack for
	// candidates that turn out unusable, so parallelism never fetches far more
	// pages than the run needs — and any extra page is cached for the next QC
	// iteration or follow-up question anyway.
	nextCandidate := 0
	var batchItems []models.RankedResult
	var batchOutcomes []ScrapeOutcome
	nextOutcome := 0

	for nextCandidate < len(candidates) && usableScrapes < ca.engine.MaxScrapes() {
		if nextOutcome >= len(batchOutcomes) {
			// The scrape context expires scrapeReserve before the run's own deadline, so
			// this is what keeps synthesis and both lookup passes their budget. A run that
			// spends everything here runs no lookups at all, which is how named people end
			// up excluded for want of a source the lookups existed to find.
			if scrapeCtx.Err() != nil {
				break
			}

			batchSize := ca.engine.MaxScrapes() - usableScrapes + ca.engine.ScrapeConcurrency() - 1
			if batchSize < 1 {
				batchSize = 1
			}
			batchItems = batchItems[:0]
			batchURLs := make([]string, 0, batchSize)
			for nextCandidate < len(candidates) && len(batchURLs) < batchSize {
				candidate := candidates[nextCandidate]
				nextCandidate++
				if known[normalizeURL(candidate.URL)] {
					continue
				}
				parsed, err := url.ParseRequestURI(candidate.URL)
				if err != nil || parsed.Scheme == "" || parsed.Host == "" {
					report = append(report, models.ScrapeReportEntry{URL: candidate.URL, SourceType: candidate.SourceType, Status: "failed", Reason: "invalid target URL"})
					continue
				}
				Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolCall, Step: 8 + len(evidence) + len(batchItems), ToolName: "web_scraper", ToolInput: candidate.URL, Content: "Scraping " + candidate.SourceType + " source..."})
				batchItems = append(batchItems, candidate)
				batchURLs = append(batchURLs, candidate.URL)
			}
			if len(batchURLs) == 0 {
				break
			}
			batchOutcomes = ca.engine.ScrapeCachedBatch(scrapeCtx, batchURLs)
			nextOutcome = 0
		}
		if usableScrapes >= ca.engine.MaxScrapes() {
			break
		}
		item := batchItems[nextOutcome]
		scrape, err := batchOutcomes[nextOutcome].Result, batchOutcomes[nextOutcome].Err
		nextOutcome++
		entry := models.ScrapeReportEntry{URL: item.URL, SourceType: item.SourceType, Status: "failed"}
		if err != nil {
			entry.Reason = err.Error()
			report = append(report, entry)
			if snippet := snippetEvidenceForURL(item, searchResults, matcher); snippet != nil {
				evidence = append(evidence, *snippet)
			}
			continue
		}
		if scrape == nil {
			entry.Reason = "scraper returned no result"
			report = append(report, entry)
			continue
		}
		entry.WordCount = scrape.WordCount
		entry.QualityScore = scrape.QualityScore
		entry.ImagesFound = len(scrape.Images)
		entry.Extractor = scrape.Extractor
		entry.FallbackStatus = scrape.FallbackStatus
		entry.FallbackReason = scrape.FallbackReason
		entry.FallbackError = scrape.FallbackError
		entry.ContentTruncated = scrape.ContentTruncated
		if scrape.Blocked {
			if handler.available() {
				outcome := handler.handle(scrapeCtx, sessionID, item.URL, "blocked page", 8+len(evidence), eventCh, tools.HardScrapeOptions{AlreadyRendered: scrape.FallbackStatus == "blocked"})
				if outcome.Status == "recovered" && outcome.Result != nil {
					entry.Status = "recovered"
					entry.Reason = "recovered by the hard-URL handler (" + outcome.Result.FallbackReason + ")"
					entry.WordCount = outcome.Result.WordCount
					entry.QualityScore = outcome.Result.QualityScore
					entry.ImagesFound = len(outcome.Result.Images)
					entry.Extractor = outcome.Result.Extractor
					entry.HandlerStatus = outcome.Status
					entry.HandlerTactics = outcome.Result.FallbackReason
					evidence = append(evidence, ca.evidenceWithOCR(scrapeCtx, item, outcome.Result, &entry))
					sources = append(sources, item.URL)
					usableScrapes++
					report = append(report, entry)
					continue
				}
				entry.HandlerStatus = outcome.Status
			}
			entry.Status = "blocked"
			entry.Reason = scrape.BlockReason
			report = append(report, entry)
			if snippet := snippetEvidenceForURL(item, searchResults, matcher); snippet != nil {
				evidence = append(evidence, *snippet)
			}
			continue
		}
		if len(scrape.Content) <= minScrapeContentLen {
			entry.Reason = "insufficient extracted content"
			report = append(report, entry)
			if snippet := snippetEvidenceForURL(item, searchResults, matcher); snippet != nil {
				evidence = append(evidence, *snippet)
			}
			continue
		}
		// The same document served under several URLs (a corporate /about,
		// /about-us and /company are routinely identical) must only be collected
		// once: a second copy adds nothing to the LLM context and pushes a
		// genuinely different source out of the budget.
		fingerprint := contentHash(scrape.Content)
		if duplicateOf, duplicate := contentSeen[fingerprint]; duplicate {
			entry.Status = "skipped"
			entry.Reason = fmt.Sprintf("duplicate content already collected from %s", duplicateOf)
			report = append(report, entry)
			continue
		}
		contentSeen[fingerprint] = item.URL
		if canonical := normalizeURL(scrape.CanonicalURL); canonical != "" {
			// The canonical URL is the document's own statement of where it
			// lives, so a later candidate pointing at the same page can be
			// skipped before it costs a fetch.
			known[canonical] = true
		}
		// OCR and evidence assembly live in evidenceWithOCR, shared with the
		// profile-recovery path, so an entry is only ever filled in one place.
		evidence = append(evidence, ca.evidenceWithOCR(scrapeCtx, item, scrape, &entry))
		sources = append(sources, item.URL)
		usableScrapes++
		entry.Status = "success"
		report = append(report, entry)
	}
	return evidence, sources, report
}

type documentTriageDecision struct {
	Decision     string   `json:"decision"`
	Confidence   float64  `json:"confidence"`
	EntityMatch  bool     `json:"entity_match"`
	TopicMatch   bool     `json:"topic_match"`
	DateMatch    bool     `json:"date_match"`
	MatchedTerms []string `json:"matched_terms"`
	Reason       string   `json:"reason"`
}

const maxDocumentPreviewTriages = 3

// triageDocumentCandidates applies a 2-stage conservative relevance gate to PDF-looking
// candidates before the scraper downloads them:
//  1. Fast metadata triage from search title, snippet, and URL.
//  2. Bounded preview triage (fetching initial bytes and extracting the first pages) for
//     uncertain/borderline candidates.
func (ca *CompanyAgent) triageDocumentCandidates(ctx context.Context, brief models.CompanyBrief, candidates []models.RankedResult, searchResults []models.SearchResult) ([]models.RankedResult, []models.ScrapeReportEntry) {
	byURL := make(map[string]models.SearchResult, len(searchResults))
	for _, result := range searchResults {
		byURL[normalizeURL(result.Link)] = result
	}
	kept := make([]models.RankedResult, 0, len(candidates))
	report := make([]models.ScrapeReportEntry, 0)
	previewsAttempted := 0

	for _, candidate := range candidates {
		result, ok := byURL[normalizeURL(candidate.URL)]
		if !ok || !looksLikePDF(candidate.URL, result.Title, candidate.SourceType) {
			kept = append(kept, candidate)
			continue
		}

		decision := documentTriageDecision{}
		prompt := fmt.Sprintf(`Classify whether this document is worth downloading for the research request.
Return JSON with the exact fields:
- "decision": "relevant" | "not_relevant" | "uncertain"
- "confidence": number between 0.0 and 1.0
- "entity_match": boolean (true if metadata matches the target company)
- "topic_match": boolean (true if metadata matches the industry/topics)
- "date_match": boolean (true if metadata matches the time scope)
- "matched_terms": array of string keywords found
- "reason": short explanation string

Use "relevant" only when the supplied metadata supports the requested entity and scope.
Use "not_relevant" for a clear mismatch. Use "uncertain" when metadata is insufficient.
Do not infer facts that are not present.

Research request:
company: %q
industry: %q
topic: %q
time_scope: %q
country: %q
city: %q
notes: %q

Search result:
title: %q
url: %q
snippet: %q
source_type: %q`,
			brief.CompanyName, brief.Industry, strings.Join(brief.AreasOfInterest, ", "),
			brief.TimeScope, brief.Country, brief.City, brief.UserNotes,
			result.Title, result.Link, result.Snippet, candidate.SourceType)
		triageCtx, cancel := context.WithTimeout(ctx, companySemanticTimeout)
		err := ca.engine.llmClient.GenerateStructured(triageCtx,
			"You are a conservative document relevance classifier. Use only the supplied metadata.",
			prompt, &decision)
		cancel()

		decision.Decision = strings.ToLower(strings.TrimSpace(decision.Decision))
		entry := models.ScrapeReportEntry{
			URL: candidate.URL, SourceType: candidate.SourceType,
			TriageDecision: decision.Decision, TriageConfidence: decision.Confidence,
		}
		topicRequired := strings.TrimSpace(brief.Industry) != "" || len(brief.AreasOfInterest) > 0
		dateRequired := strings.TrimSpace(brief.TimeScope) != ""

		if err != nil {
			entry.Status = "skipped"
			entry.Reason = "document triage failed: " + err.Error()
			report = append(report, entry)
			continue
		}

		if decision.Decision == "relevant" && decision.Confidence >= 0.80 && decision.EntityMatch &&
			(!topicRequired || decision.TopicMatch) && (!dateRequired || decision.DateMatch) {
			kept = append(kept, candidate)
			continue
		}

		if decision.Decision == "not_relevant" {
			entry.Status = "skipped"
			entry.Reason = "document triage: " + decision.Reason
			report = append(report, entry)
			continue
		}

		// For uncertain / borderline metadata decisions, attempt a bounded preview triage if budget permits.
		if previewsAttempted < maxDocumentPreviewTriages && ctx.Err() == nil {
			previewsAttempted++
			previewCtx, cancelPreview := context.WithTimeout(ctx, 10*time.Second)
			previewBytes, _, previewErr := ca.engine.FetchDocumentPreview(previewCtx, candidate.URL, 512<<10)
			cancelPreview()

			if previewErr == nil && len(previewBytes) > 0 {
				previewExtractCtx, cancelExtract := context.WithTimeout(ctx, 5*time.Second)
				previewText, extractErr := tools.ExtractPDFPreviewText(previewExtractCtx, previewBytes, 3, 3000)
				cancelExtract()

				if extractErr == nil && strings.TrimSpace(previewText) != "" {
					previewDecision := documentTriageDecision{}
					previewPrompt := fmt.Sprintf(`Classify whether this document is relevant to the research request based on its initial page text.
Return JSON with the exact fields:
- "decision": "relevant" | "not_relevant" | "uncertain"
- "confidence": number between 0.0 and 1.0
- "entity_match": boolean (true if preview confirms the target company)
- "topic_match": boolean (true if preview confirms the industry/topics)
- "date_match": boolean (true if preview confirms the time scope)
- "matched_terms": array of string keywords found
- "reason": short explanation string

Use "relevant" only when the preview confirms the requested company and scope.
Use "not_relevant" if the preview reveals this document is for a different entity or unrelated topic.
Use "uncertain" if the preview is still insufficient.

Research request:
company: %q
industry: %q
topic: %q
time_scope: %q
country: %q
city: %q
notes: %q

Search result:
title: %q
url: %q

Document preview (first pages text):
%s`,
						brief.CompanyName, brief.Industry, strings.Join(brief.AreasOfInterest, ", "),
						brief.TimeScope, brief.Country, brief.City, brief.UserNotes,
						result.Title, result.Link, previewText)

					previewLlmCtx, cancelPreviewLlm := context.WithTimeout(ctx, companySemanticTimeout)
					previewLlmErr := ca.engine.llmClient.GenerateStructured(previewLlmCtx,
						"You are a conservative document relevance classifier inspecting initial document text.",
						previewPrompt, &previewDecision)
					cancelPreviewLlm()

					if previewLlmErr == nil {
						previewDecision.Decision = strings.ToLower(strings.TrimSpace(previewDecision.Decision))
						entry.TriageDecision = previewDecision.Decision
						entry.TriageConfidence = previewDecision.Confidence

						if previewDecision.Decision == "relevant" && previewDecision.Confidence >= 0.80 && previewDecision.EntityMatch &&
							(!topicRequired || previewDecision.TopicMatch) && (!dateRequired || previewDecision.DateMatch) {
							kept = append(kept, candidate)
							continue
						}
						entry.Status = "skipped"
						if previewDecision.Decision == "not_relevant" {
							entry.Reason = "document preview: " + previewDecision.Reason
						} else {
							entry.Reason = "document preview uncertain: " + previewDecision.Reason
						}
						report = append(report, entry)
						continue
					}
				}
			}
		}

		entry.Status = "skipped"
		entry.Reason = "document triage uncertain: " + decision.Reason
		report = append(report, entry)
	}
	kept, familyReport := selectLatestDocumentFamilies(kept, byURL, brief.TimeScope)
	return kept, append(report, familyReport...)
}

var documentPeriodPattern = regexp.MustCompile(`(?i)\b(?:fy\s*)?(20\d{2})(?:\s*[-/]\s*(\d{2,4}))?\b`)
var documentShortPeriodPattern = regexp.MustCompile(`(?i)\b(?:fy\s*)?(\d{2})\s*[-/]\s*(\d{2})\b`)

type documentPeriodInfo struct {
	endYear int
	hasYear bool
}

// selectLatestDocumentFamilies prevents near-duplicate annual filings from
// consuming scrape slots. It keeps the newest period for each normalized title
// family and records older candidates as skipped.
func selectLatestDocumentFamilies(candidates []models.RankedResult, searchResults map[string]models.SearchResult, timeScope string) ([]models.RankedResult, []models.ScrapeReportEntry) {
	upperYear := documentScopeUpperYear(timeScope)
	winners := make(map[string]int, len(candidates))
	familyOrder := make([]string, 0, len(candidates))
	for index, candidate := range candidates {
		result, hasResult := searchResults[normalizeURL(candidate.URL)]
		family := ""
		if hasResult && looksLikePDF(candidate.URL, result.Title, candidate.SourceType) {
			family = documentFamilyKey(candidate, result)
		}
		if family == "" {
			family = "url:" + normalizeURL(candidate.URL)
		}
		if _, seen := winners[family]; !seen {
			winners[family] = index
			familyOrder = append(familyOrder, family)
			continue
		}
		current := candidates[winners[family]]
		if newerDocumentCandidate(candidate, result, current, searchResults[normalizeURL(current.URL)], upperYear) {
			winners[family] = index
		}
	}

	selected := make([]models.RankedResult, 0, len(winners))
	report := make([]models.ScrapeReportEntry, 0)
	for _, family := range familyOrder {
		winner := winners[family]
		selected = append(selected, candidates[winner])
		for index, candidate := range candidates {
			if index == winner {
				continue
			}
			otherResult := searchResults[normalizeURL(candidate.URL)]
			if documentFamilyKey(candidate, otherResult) != family {
				continue
			}
			report = append(report, models.ScrapeReportEntry{
				URL: candidate.URL, SourceType: candidate.SourceType, Status: "skipped",
				Reason: fmt.Sprintf("older similar document superseded by %s", candidates[winner].URL),
			})
		}
	}
	return selected, report
}

func documentFamilyKey(candidate models.RankedResult, result models.SearchResult) string {
	title := strings.TrimSpace(result.Title)
	if title == "" {
		title = strings.TrimSpace(candidate.Title)
	}
	if title == "" {
		title = strings.TrimSpace(candidate.URL)
	}
	title = strings.ToLower(title)
	title = documentPeriodPattern.ReplaceAllString(title, " ")
	title = documentShortPeriodPattern.ReplaceAllString(title, " ")
	title = strings.NewReplacer("-", " ", "_", " ", "/", " ", ".", " ", ":", " ").Replace(title)
	return strings.Join(strings.Fields(title), " ")
}

func documentCandidatePeriod(candidate models.RankedResult, result models.SearchResult) documentPeriodInfo {
	value := strings.Join([]string{
		result.Title, candidate.Title, candidate.URL, result.Snippet,
	}, " ")
	if shortMatch := documentShortPeriodPattern.FindStringSubmatch(value); len(shortMatch) > 0 {
		start, end := 0, 0
		fmt.Sscanf(shortMatch[1], "%d", &start)
		fmt.Sscanf(shortMatch[2], "%d", &end)
		return documentPeriodInfo{endYear: 2000 + end, hasYear: start > 0 && end > 0}
	}
	match := documentPeriodPattern.FindStringSubmatch(value)
	if len(match) == 0 {
		return documentPeriodInfo{}
	}
	year := 0
	fmt.Sscanf(match[1], "%d", &year)
	if match[2] != "" {
		end := match[2]
		if len(end) == 2 {
			end = match[1][:2] + end
		}
		fmt.Sscanf(end, "%d", &year)
	}
	return documentPeriodInfo{endYear: year, hasYear: year > 0}
}

func newerDocumentCandidate(candidate models.RankedResult, candidateResult models.SearchResult, current models.RankedResult, currentResult models.SearchResult, upperYear int) bool {
	candidatePeriod := documentCandidatePeriod(candidate, candidateResult)
	currentPeriod := documentCandidatePeriod(current, currentResult)
	if upperYear > 0 {
		if candidatePeriod.hasYear && candidatePeriod.endYear > upperYear {
			return false
		}
		if currentPeriod.hasYear && currentPeriod.endYear > upperYear {
			return true
		}
	}
	if candidatePeriod.hasYear != currentPeriod.hasYear {
		return candidatePeriod.hasYear
	}
	return candidatePeriod.hasYear && candidatePeriod.endYear > currentPeriod.endYear
}

func documentScopeUpperYear(scope string) int {
	shortMatches := documentShortPeriodPattern.FindAllStringSubmatch(scope, -1)
	upper := 0
	for _, match := range shortMatches {
		end := 0
		fmt.Sscanf(match[2], "%d", &end)
		if end > 0 && 2000+end > upper {
			upper = 2000 + end
		}
	}
	matches := documentPeriodPattern.FindAllStringSubmatch(scope, -1)
	for _, match := range matches {
		year := 0
		fmt.Sscanf(match[1], "%d", &year)
		if match[2] != "" {
			end := match[2]
			if len(end) == 2 {
				end = match[1][:2] + end
			}
			fmt.Sscanf(end, "%d", &year)
		}
		if year > upper {
			upper = year
		}
	}
	return upper
}

func looksLikePDF(targetURL, title, sourceType string) bool {
	value := strings.ToLower(targetURL + " " + title + " " + sourceType)
	return strings.Contains(value, ".pdf") || strings.Contains(value, "pdf") ||
		strings.Contains(value, "annual report") || strings.Contains(value, "filing")
}

// evidenceWithOCR converts a successful scrape into evidence, running bounded
// image OCR when images are present. entry (optional) accumulates the OCR
// diagnostics that the scrape report renders.
func (ca *CompanyAgent) evidenceWithOCR(ctx context.Context, item models.RankedResult, scrape *models.ScrapeResult, entry *models.ScrapeReportEntry) models.Evidence {
	if len(scrape.Images) > 0 {
		if entry != nil {
			entry.OCRStatus = "attempted"
		}
		ocrCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		textByURL, ocrErr := ca.engine.ExtractImageTextDetailed(ocrCtx, scrape.Images)
		cancel()
		if ocrErr != nil {
			if entry != nil {
				entry.OCRStatus = "failed"
				entry.OCRReason = ocrErr.Error()
			}
		} else if entry != nil {
			entry.OCRStatus = "success"
		}
		for index := range scrape.Images {
			if text := textByURL[scrape.Images[index].URL]; text != "" {
				scrape.Images[index].ExtractedText = text
				if entry != nil {
					entry.OCRImagesWithText++
				}
			}
		}
	} else if entry != nil {
		entry.OCRStatus = "not_applicable"
	}
	evidence := models.Evidence{
		URL:          scrape.CanonicalURL,
		Title:        scrape.Title,
		Content:      scrape.Content,
		Headings:     scrape.Headings,
		JSONLD:       scrape.JSONLD,
		Images:       scrape.Images,
		Score:        item.Score,
		EvidenceType: item.EvidenceType,
		SourceType:   item.SourceType,
		QualityScore: scrape.QualityScore,
		LastModified: scrape.LastModified,
	}
	if evidence.URL == "" {
		evidence.URL = item.URL
	}
	// Dating here (rather than only in the later pass) means the person who
	// reads the scrape report sees the same date the synthesiser does. The
	// helper mutates through the slice, so the value is returned from it.
	dated := []models.Evidence{evidence}
	annotateEvidenceDates(dated)
	return dated[0]
}

func appendUniqueScrapeReports(existing, additions []models.ScrapeReportEntry) []models.ScrapeReportEntry {
	seen := make(map[string]bool, len(existing)+len(additions))
	for _, item := range existing {
		seen[normalizeURL(item.URL)] = true
	}
	for _, item := range additions {
		key := normalizeURL(item.URL)
		if key != "" && !seen[key] {
			seen[key] = true
			existing = append(existing, item)
		}
	}
	return existing
}

func filterBlockedSearchResults(results []models.SearchResult) []models.SearchResult {
	filtered := make([]models.SearchResult, 0, len(results))
	for _, result := range results {
		if !isBlockedSourceURL(result.Link) {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func isBlockedSourceURL(targetURL string) bool {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	return host == "wikipedia.org" || strings.HasSuffix(host, ".wikipedia.org")
}

// formatProfileCandidates renders professional-network snippet evidence as an
// explicit candidate list.
//
// Company mode never scrapes profile pages (see scrapeRanked), so a collected
// snippet is frequently the only signal that a person holds a given function at
// the company. Labelling those snippets separately keeps the synthesizer from
// overlooking them among the general snippet and full-page evidence.
func formatProfileCandidates(evidence []models.Evidence) string {
	var sb strings.Builder
	for _, item := range evidence {
		if item.EvidenceType != "search_snippet_fallback" {
			continue
		}
		title := strings.TrimSpace(item.Title)
		content := strings.TrimSpace(item.Content)
		if title == "" && content == "" {
			continue
		}
		if sb.Len() == 0 {
			sb.WriteString("Profile candidates (indexed professional-network headlines; a name paired with a functional title and a company is evidence of that person's function):\n")
		}
		sb.WriteString(fmt.Sprintf("- %s | %s | %s\n", title, content, item.URL))
	}
	return strings.TrimSpace(sb.String())
}

func snippetEvidenceForURL(item models.RankedResult, results []models.SearchResult, matcher *entityMatcher) *models.Evidence {
	for _, result := range results {
		if normalizeURL(result.Link) != normalizeURL(item.URL) || strings.TrimSpace(result.Snippet) == "" {
			continue
		}
		if !snippetNamesEntity(result, matcher) {
			// The snippet describes somebody else's company. Keeping it as evidence
			// is exactly how an unrelated name becomes a roster entry — a name and a
			// title is all the synthesiser needs.
			continue
		}
		entry := models.Evidence{
			URL:          result.Link,
			Title:        result.Title,
			Content:      result.Snippet,
			Score:        item.Score * 0.65,
			EvidenceType: "search_snippet_fallback",
			SourceType:   item.SourceType,
			QualityScore: 0.25,
		}
		if when, ok := searchResultDate(result); ok {
			entry.AsOf = when
			entry.DateSignal = dateSignalSearch
		}
		return &entry
	}
	return nil
}

// excludedScrapeCandidate reports whether a ranked candidate must not be fetched.
//
// Blocked sources are always excluded. Professional-profile pages are excluded
// unless the operator has opted into fetching them, which is the second of the two
// places that policy is enforced (the other is the snippet-only evidence loop in
// scrapeRanked).
func excludedScrapeCandidate(candidate models.RankedResult, allowProfiles bool) bool {
	if candidate.SourceType == "blocked" || isBlockedSourceURL(candidate.URL) {
		return true
	}
	if candidate.SourceType == "indexed_profile" {
		return true
	}
	return candidate.SourceType == "professional_profile" && !allowProfiles
}

func selectScrapeCandidates(ranked []models.RankedResult, limit int, allowProfiles bool) []models.RankedResult {
	candidates := append([]models.RankedResult(nil), ranked...)
	priority := func(sourceType string) int {
		switch sourceType {
		case "official_leadership":
			return 0
		case "official_report":
			return 1
		case "official_locations":
			return 2
		case "official":
			return 3
		case "company_registry":
			return 4
		case "leadership":
			return 5
		case "industry_event":
			return 6
		case "external":
			return 7
		default:
			return 7
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		pi, pj := priority(candidates[i].SourceType), priority(candidates[j].SourceType)
		if pi != pj {
			return pi < pj
		}
		return candidates[i].Score > candidates[j].Score
	})
	selected := make([]models.RankedResult, 0, limit)
	domainCounts := make(map[string]int)
	for _, candidate := range candidates {
		if len(selected) >= limit {
			break
		}
		if excludedScrapeCandidate(candidate, allowProfiles) {
			continue
		}
		domain := sourceDomain(candidate.URL)
		if domain != "" && domainCounts[domain] > 0 && !(candidate.Official && domainCounts[domain] < 2) {
			continue
		}
		selected = append(selected, candidate)
		if domain != "" {
			domainCounts[domain]++
		}
	}
	if len(selected) < limit {
		for _, candidate := range candidates {
			if len(selected) >= limit {
				break
			}
			if excludedScrapeCandidate(candidate, allowProfiles) {
				continue
			}
			alreadySelected := false
			for _, chosen := range selected {
				if normalizeURL(chosen.URL) == normalizeURL(candidate.URL) {
					alreadySelected = true
					break
				}
			}
			if !alreadySelected {
				selected = append(selected, candidate)
			}
		}
	}
	return selected
}

func sourceDomain(targetURL string) string {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return ""
	}
	return rootHost(parsed.Hostname())
}

// publicSuffixLabels are the second-level labels that belong to a registry suffix rather
// than to the registrable name: sanjaychem.co.in is a company in India, not the host
// "co.in". Without this the root of a .co.in or .co.uk site is the suffix itself, and a
// domain key of "co.in" matches every .co.in host on the web — a gate that accepts
// everything is worse than no gate.
var publicSuffixLabels = map[string]bool{
	"ac": true, "co": true, "com": true, "edu": true, "firm": true, "gen": true,
	"go": true, "gov": true, "id": true, "ind": true, "ltd": true, "mil": true,
	"net": true, "ne": true, "or": true, "org": true, "plc": true, "sch": true,
}

// rootHost reduces a host to the domain that identifies its owner: the registrable name
// plus its suffix. Subdomains are dropped (mail.acme.com -> acme.com) and multi-part
// suffixes are kept (acme.co.uk -> acme.co.uk).
func rootHost(host string) string {
	host = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(host), "www."))
	parts := strings.Split(host, ".")
	if len(parts) < 3 {
		return host
	}
	// A second-level label that is part of the suffix means the registrable name is one
	// label further left: sub.acme.co.uk keeps three labels, acme.co.uk keeps two.
	if publicSuffixLabels[parts[len(parts)-2]] {
		if len(parts) > 3 {
			return strings.Join(parts[len(parts)-3:], ".")
		}
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func formatEvidence(evidence []models.Evidence) string {
	var builder strings.Builder
	for index, item := range evidence {
		dated := ""
		if !item.AsOf.IsZero() {
			dated = fmt.Sprintf(", as of %s", item.AsOf.UTC().Format(asOfLayout))
		}
		builder.WriteString(fmt.Sprintf("\n--- Evidence %d: %s (%s), score %.2f%s ---\n%s\n", index+1, item.Title, item.URL, item.Score, dated, item.Content))
		if len(item.Headings) > 0 {
			builder.WriteString("Headings: ")
			builder.WriteString(strings.Join(item.Headings, " | "))
			builder.WriteString("\n")
		}
		for _, metadata := range item.JSONLD {
			metadata = strings.TrimSpace(metadata)
			if metadata == "" {
				continue
			}
			if len(metadata) > 2000 {
				metadata = metadata[:2000]
			}
			builder.WriteString("Structured metadata: ")
			builder.WriteString(metadata)
			builder.WriteString("\n")
		}
		for _, image := range item.Images {
			builder.WriteString("Image: ")
			builder.WriteString(image.URL)
			if image.Alt != "" {
				builder.WriteString(" | alt: ")
				builder.WriteString(image.Alt)
			}
			if image.Caption != "" {
				builder.WriteString(" | caption: ")
				builder.WriteString(image.Caption)
			}
			if image.ExtractedText != "" {
				builder.WriteString(" | OCR: ")
				builder.WriteString(image.ExtractedText)
			}
			builder.WriteString("\n")
		}
	}
	return builder.String()
}
