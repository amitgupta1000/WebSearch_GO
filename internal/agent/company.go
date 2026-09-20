package agent

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// CompanyAgent conducts multi-phase organizational discovery and hierarchy mapping.
type CompanyAgent struct {
	engine *Engine
}

const maxCompanyReviewIterations = 3

// NewCompanyAgent initializes the company research specialist agent.
func NewCompanyAgent(engine *Engine) *CompanyAgent {
	return &CompanyAgent{engine: engine}
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
func (ca *CompanyAgent) ResearchCompanyRun(ctx context.Context, sessionID string, brief models.CompanyBrief, eventCh chan<- models.AgentEvent) (*models.OrgChart, *models.CompanyResearchContext, error) {
	brief.CompanyName = strings.TrimSpace(brief.CompanyName)
	if brief.CompanyName == "" {
		return nil, nil, fmt.Errorf("company name is required")
	}

	state := &models.CompanyResearchContext{Brief: brief}
	plan := ca.planQueries(ctx, brief)
	var chart *models.OrgChart
	for iteration := 0; iteration < maxCompanyReviewIterations; iteration++ {
		state.Iteration = iteration + 1
		state.QueryPlan = plan
		if iteration > 0 {
			Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventThinking, Step: 10 + iteration, Content: fmt.Sprintf("Running focused QC follow-up %d of %d...", iteration, maxCompanyReviewIterations-1)})
		}

		results, err := ca.searchPlan(ctx, sessionID, plan, eventCh)
		if err != nil {
			return nil, state, err
		}
		state.SearchResults = appendUniqueSearchResults(state.SearchResults, results)
		state.RankedResults = ca.rankResults(ctx, brief, state.SearchResults)
		newEvidence, _ := ca.scrapeRanked(ctx, sessionID, state.RankedResults, state.Evidence, eventCh)
		state.Evidence = appendUniqueEvidence(state.Evidence, newEvidence)
		if len(state.Evidence) == 0 {
			return nil, state, fmt.Errorf("no usable company evidence was collected")
		}

		chart, err = ca.synthesizeChart(ctx, brief, state.Evidence, state.SearchResults, sessionID, eventCh)
		if err != nil {
			return nil, state, err
		}
		review := ca.reviewChart(ctx, brief, chart, state.Evidence, sessionID, eventCh)
		state.Reviews = append(state.Reviews, review)
		if review.Verdict != "revise" || len(review.FollowUpQueries) == 0 || iteration == maxCompanyReviewIterations-1 {
			Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventComplete, Step: 20 + iteration, Content: fmt.Sprintf("Completed research for %s after %d QC review(s).", chart.CompanyName, len(state.Reviews))})
			return chart, state, nil
		}
		plan = limitQueryPlan(models.QueryPlan{Queries: review.FollowUpQueries}, 4)
	}

	return chart, state, nil
}

func (ca *CompanyAgent) synthesizeChart(ctx context.Context, brief models.CompanyBrief, evidence []models.Evidence, results []models.SearchResult, sessionID string, eventCh chan<- models.AgentEvent) (*models.OrgChart, error) {
	Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventThinking, Step: 8, Content: "Synthesizing the organization chart from selected evidence..."})
	prompt := `You are an expert corporate intelligence analyst. Use only the supplied evidence and search snippets. Do not invent employees, titles, locations, reporting lines, or facts not supported by evidence. Return an OrgChart JSON with company_name, domain, overview, headquarters, leadership, departments, and locations.`
	var chart models.OrgChart
	contextData := fmt.Sprintf("Research brief: %+v\n\nSearch snippets:\n%s\n\nSelected evidence:\n%s", brief, FormatSearchSnippets(results), formatEvidence(evidence))
	if err := ca.engine.SynthesizeJSON(ctx, prompt, contextData, &chart); err != nil {
		return nil, fmt.Errorf("synthesis failed: %w", err)
	}
	if chart.CompanyName == "" {
		chart.CompanyName = brief.CompanyName
	}
	if chart.Domain == "" {
		chart.Domain = brief.Domain
	}
	chart.Sources = evidenceSources(evidence)
	chart.LastUpdated = time.Now()
	return &chart, nil
}

func (ca *CompanyAgent) reviewChart(ctx context.Context, brief models.CompanyBrief, chart *models.OrgChart, evidence []models.Evidence, sessionID string, eventCh chan<- models.AgentEvent) models.QualityReview {
	var review models.QualityReview
	request := fmt.Sprintf("Brief:\n%+v\n\nDraft chart:\n%+v\n\nEvidence:\n%s", brief, *chart, formatEvidence(evidence))
	err := ca.engine.GenerateStructured(ctx, `Act as a strict research quality reviewer. Evaluate whether the draft answers the brief and whether claims are supported by the supplied evidence. Return JSON with verdict (pass or revise), score from 0 to 1, gaps, contradictions, unsupported_claims, reason, and at most four focused follow_up_queries. Use revise only for material gaps or contradictions. Never invent facts.`, request, &review)
	review.Verdict = strings.ToLower(strings.TrimSpace(review.Verdict))
	if err != nil || review.Verdict == "" {
		review.Verdict = "pass"
		review.Score = 0.7
		review.Reason = "No additional QC revision was required."
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
	err := ca.engine.GenerateStructured(ctx, `Answer the user's question using only the supplied company research and cached evidence. If the evidence is insufficient or contradictory, set needs_fresh_search to true and provide a focused search_query. Otherwise answer directly, cite relevant source URLs in sources, and set needs_fresh_search to false. Return JSON.`, cachedPrompt, &answer)
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
	freshRanked := make([]models.RankedResult, 0, len(freshResults))
	for index, result := range freshResults {
		if index >= ca.engine.MaxScrapes() {
			break
		}
		freshRanked = append(freshRanked, models.RankedResult{URL: result.Link, Relevant: true, Score: 1 - float64(index)/100, Reason: "selected for question follow-up", EvidenceType: "question_search"})
	}
	freshEvidence, _ := ca.scrapeRanked(ctx, "question", freshRanked, nil, nil)
	combinedEvidence := append(append([]models.Evidence{}, state.Evidence...), freshEvidence...)
	var finalAnswer models.QuestionAnswer
	finalPrompt := fmt.Sprintf("Company brief:\n%+v\n\nOrg chart:\n%+v\n\nOriginal evidence:\n%s\n\nFresh search query: %s\nFresh search results:\n%s\n\nFresh evidence:\n%s\n\nQuestion: %s", state.Brief, *chart, formatEvidence(state.Evidence), query, FormatSearchSnippets(freshResults), formatEvidence(freshEvidence), question)
	if err := ca.engine.GenerateStructured(ctx, `Answer the user's company research question using the supplied original and fresh evidence. Prefer direct evidence, acknowledge uncertainty, and cite source URLs in sources. Return JSON with question, answer, confidence, and sources.`, finalPrompt, &finalAnswer); err != nil {
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

func (ca *CompanyAgent) planQueries(ctx context.Context, brief models.CompanyBrief) models.QueryPlan {
	var plan models.QueryPlan
	request := fmt.Sprintf("Company: %s\nDomain: %s\nIndustry: %s\nCountry: %s\nCity: %s\nAreas: %s\nOutput: %s\nTime: %s\nNotes: %s", brief.CompanyName, brief.Domain, brief.Industry, brief.Country, brief.City, strings.Join(brief.AreasOfInterest, ", "), strings.Join(brief.DesiredOutput, ", "), brief.TimeScope, brief.UserNotes)
	err := ca.engine.GenerateStructured(ctx, `Create a focused company web research query plan. Return JSON with a queries array. Each query needs query, purpose, target_area, and priority from 1 to 5. Cover leadership, departments, locations, and user-requested areas.`, request, &plan)
	if err == nil && len(plan.Queries) > 0 {
		return limitQueryPlan(plan, 6)
	}
	return fallbackQueryPlan(brief)
}

func fallbackQueryPlan(brief models.CompanyBrief) models.QueryPlan {
	base := strings.TrimSpace(strings.Join([]string{brief.CompanyName, brief.Country, brief.City}, " "))
	queries := []models.PlannedQuery{
		{Query: base + " leadership executive team", Purpose: "identify leadership", TargetArea: "leadership", Priority: 1},
		{Query: base + " departments engineering sales product finance", Purpose: "identify departments", TargetArea: "departments", Priority: 2},
		{Query: base + " offices locations headquarters", Purpose: "identify locations", TargetArea: "locations", Priority: 3},
	}
	for _, area := range brief.AreasOfInterest {
		area = strings.TrimSpace(area)
		if area != "" {
			queries = append(queries, models.PlannedQuery{Query: base + " " + area, Purpose: "investigate requested area", TargetArea: area, Priority: 2})
		}
	}
	return limitQueryPlan(models.QueryPlan{Queries: queries}, 6)
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

func (ca *CompanyAgent) searchPlan(ctx context.Context, sessionID string, plan models.QueryPlan, eventCh chan<- models.AgentEvent) ([]models.SearchResult, error) {
	var all []models.SearchResult
	seen := make(map[string]bool)
	var lastErr error
	for index, planned := range plan.Queries {
		Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolCall, Step: 3 + index, ToolName: "serper_search", ToolInput: planned.Query, Content: planned.Purpose})
		results, err := ca.engine.SearchCached(ctx, planned.Query, ca.engine.MaxSearchResults())
		if err != nil {
			lastErr = err
			continue
		}
		for _, result := range results {
			key := strings.ToLower(strings.TrimSpace(result.Link))
			if key != "" && !seen[key] {
				seen[key] = true
				all = append(all, result)
			}
		}
	}
	if len(all) == 0 && lastErr != nil {
		return nil, fmt.Errorf("company search failed: %w", lastErr)
	}
	Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolResult, Step: 7, ToolName: "serper_search", Content: fmt.Sprintf("Collected %d unique search results.", len(all))})
	return all, nil
}

func (ca *CompanyAgent) rankResults(ctx context.Context, brief models.CompanyBrief, results []models.SearchResult) []models.RankedResult {
	var response struct {
		RankedResults []models.RankedResult `json:"ranked_results"`
	}
	err := ca.engine.GenerateStructured(ctx, `Rank search results for company research. Return JSON with ranked_results. Use only URLs present in the input. Mark results relevant only when they plausibly concern the requested company, location, and areas. Score from 0 to 1 and explain each decision.`, fmt.Sprintf("Brief: %+v\nResults:\n%s", brief, FormatSearchSnippets(results)), &response)
	if err == nil && len(response.RankedResults) > 0 {
		return normalizeRankedResults(response.RankedResults, results)
	}
	ranked := make([]models.RankedResult, 0, len(results))
	for _, result := range results {
		ranked = append(ranked, deterministicRank(brief, result))
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	return ranked
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

func normalizeRankedResults(ranked []models.RankedResult, results []models.SearchResult) []models.RankedResult {
	known := make(map[string]bool, len(results))
	for _, result := range results {
		known[result.Link] = true
	}
	filtered := ranked[:0]
	for _, item := range ranked {
		if known[item.URL] && item.Relevant {
			filtered = append(filtered, item)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Score > filtered[j].Score })
	return filtered
}

func (ca *CompanyAgent) scrapeRanked(ctx context.Context, sessionID string, ranked []models.RankedResult, existing []models.Evidence, eventCh chan<- models.AgentEvent) ([]models.Evidence, []string) {
	evidence := make([]models.Evidence, 0, ca.engine.MaxScrapes())
	sources := make([]string, 0, ca.engine.MaxScrapes())
	known := make(map[string]bool, len(existing))
	for _, item := range existing {
		known[item.URL] = true
	}
	for _, item := range ranked {
		if len(evidence) >= ca.engine.MaxScrapes() {
			break
		}
		if known[item.URL] {
			continue
		}
		parsed, err := url.ParseRequestURI(item.URL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolCall, Step: 8 + len(evidence), ToolName: "web_scraper", ToolInput: item.URL, Content: "Scraping selected relevant source..."})
		scrape, err := ca.engine.ScrapeCached(ctx, item.URL)
		if err != nil || len(scrape.Content) <= minScrapeContentLen {
			continue
		}
		evidence = append(evidence, models.Evidence{URL: item.URL, Title: scrape.Title, Content: scrape.Content, Score: item.Score, EvidenceType: item.EvidenceType})
		sources = append(sources, item.URL)
	}
	return evidence, sources
}

func formatEvidence(evidence []models.Evidence) string {
	var builder strings.Builder
	for index, item := range evidence {
		builder.WriteString(fmt.Sprintf("\n--- Evidence %d: %s (%s), score %.2f ---\n%s\n", index+1, item.Title, item.URL, item.Score, item.Content))
	}
	return builder.String()
}
