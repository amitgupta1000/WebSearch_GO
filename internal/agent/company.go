package agent

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// CompanyAgent conducts multi-phase organizational discovery and hierarchy mapping.
type CompanyAgent struct {
	engine *Engine
}

const maxCompanyReviewIterations = 3

// minQCIterationBudget is the minimum time that must remain before another
// QC-driven iteration (search + scrape + synthesis) is worth starting.
const minQCIterationBudget = 60 * time.Second

// hasBudgetForIteration reports whether the context allows another full research
// iteration. Contexts without a deadline are treated as unbounded.
func hasBudgetForIteration(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return time.Until(deadline) >= minQCIterationBudget
}

const maxCompanySearchQueries = 10

const (
	companyPlanningTimeout  = 20 * time.Second
	companySemanticTimeout  = 8 * time.Second
	companySynthesisTimeout = 45 * time.Second
	companyReviewTimeout    = 30 * time.Second
	companyQuestionTimeout  = 45 * time.Second
)

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
		Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventThinking, Step: 7, Content: fmt.Sprintf("Evaluating relevance of %d search results...", len(state.SearchResults))})
		state.RankedResults = ca.rankResults(ctx, brief, state.SearchResults)
		Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolResult, Step: 7, ToolName: "relevance_ranker", Content: fmt.Sprintf("Selected %d relevant candidates.", len(state.RankedResults))})
		newEvidence, _, scrapeReport := ca.scrapeRanked(ctx, sessionID, state.RankedResults, state.SearchResults, state.Evidence, eventCh)
		state.Evidence = appendUniqueEvidence(state.Evidence, newEvidence)
		state.ScrapeReport = appendUniqueScrapeReports(state.ScrapeReport, scrapeReport)
		if len(state.Evidence) == 0 {
			state.Evidence = appendUniqueEvidence(state.Evidence, searchSnippetEvidence(state.RankedResults, state.SearchResults, ca.engine.MaxScrapes()))
		}
		if len(state.Evidence) == 0 {
			return nil, state, fmt.Errorf("no usable company evidence was collected")
		}

		chart, err = ca.synthesizeChart(ctx, brief, state.Evidence, state.SearchResults, sessionID, eventCh)
		if err != nil {
			return nil, state, err
		}
		chart.ScrapeReport = state.ScrapeReport
		review := ca.reviewChart(ctx, brief, chart, state.Evidence, sessionID, eventCh)
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

func (ca *CompanyAgent) synthesizeChart(ctx context.Context, brief models.CompanyBrief, evidence []models.Evidence, results []models.SearchResult, sessionID string, eventCh chan<- models.AgentEvent) (*models.OrgChart, error) {
	Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventThinking, Step: 8, Content: "Synthesizing the organization chart from selected evidence..."})
	prompt := `You are an expert corporate intelligence analyst. Use only the supplied evidence and search snippets. Do not invent employees, titles, locations, reporting lines, or facts not supported by evidence. Return only the top-level OrgChart JSON with company_name, domain, overview, headquarters, leadership, departments, and locations. Do not wrap the result in an org_chart object. The headquarters field MUST be a plain string such as "New Delhi, India". The locations field may contain objects with city, country, address, and headcount_est as a string. Image metadata is supporting evidence; do not invent visual facts from image URLs. Include supported department leaders even when their exact title is not in the search query: consider functional variants such as Chief, C-level, President, Executive Vice President, Senior Vice President, Vice President, VP, Head, Director, and General Manager. Treat procurement, purchasing, strategic sourcing, supply chain, logistics, operations, marketing, brand, communications, growth, and digital marketing as relevant department functions. Conference speaker evidence may identify a person's company, function, and industry relevance, but conference participation alone does not prove that the person is a current executive; label or place such people conservatively unless current employment is corroborated. Profile candidates are snippets from professional networks: when one pairs a person's name with a functional title and the target company, or a related group entity, treat it as evidence of that person's function, list them in the matching department, and record which entity they belong to in their title. Promote a profile candidate into leadership only for board or C-level titles. Prefer listing a named profile candidate in a department over leaving that department empty. Every object in the departments array must use the shape {"name": "<function>", "head": "<optional person>", "description": "<optional summary>", "members": [{"name": "<person>", "title": "<their title>", "public_source": "<source url>"}]}. List every person you attribute to a function inside that function's members array, and never leave members empty when you have named a person for that function.`
	var chart models.OrgChart
	contextData := fmt.Sprintf("Research brief: %+v\n\nSearch snippets:\n%s\n\nSelected evidence:\n%s", brief, FormatSearchSnippets(results), formatEvidence(evidence))
	if profiles := formatProfileCandidates(evidence); profiles != "" {
		contextData += "\n\n" + profiles
	}
	llmCtx, cancel := context.WithTimeout(ctx, companySynthesisTimeout)
	defer cancel()
	if err := ca.engine.SynthesizeJSON(llmCtx, prompt, contextData, &chart); err != nil {
		return nil, fmt.Errorf("synthesis failed: %w", err)
	}
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

func (ca *CompanyAgent) reviewChart(ctx context.Context, brief models.CompanyBrief, chart *models.OrgChart, evidence []models.Evidence, sessionID string, eventCh chan<- models.AgentEvent) models.QualityReview {
	var review models.QualityReview
	request := fmt.Sprintf("Brief:\n%+v\n\nDraft chart:\n%+v\n\nEvidence:\n%s", brief, *chart, formatEvidence(evidence))
	llmCtx, cancel := context.WithTimeout(ctx, companyReviewTimeout)
	defer cancel()
	err := ca.engine.GenerateStructured(llmCtx, `Act as a strict research quality reviewer. Evaluate whether the draft answers the brief and whether claims are supported by the supplied evidence. Return JSON with verdict (pass or revise), score from 0 to 1, gaps, contradictions, unsupported_claims, reason, and at most four focused follow_up_queries. Use revise only for material gaps or contradictions. Never invent facts.`, request, &review)
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
	freshRanked := make([]models.RankedResult, 0, len(freshResults))
	for index, result := range freshResults {
		if index >= ca.engine.MaxScrapes() {
			break
		}
		freshRanked = append(freshRanked, models.RankedResult{URL: result.Link, Relevant: true, Score: 1 - float64(index)/100, Reason: "selected for question follow-up", EvidenceType: "question_search"})
	}
	freshEvidence, _, _ := ca.scrapeRanked(ctx, "question", freshRanked, freshResults, nil, nil)
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

func searchSnippetEvidence(ranked []models.RankedResult, results []models.SearchResult, limit int) []models.Evidence {
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
		evidence = append(evidence, models.Evidence{
			URL:          result.Link,
			Title:        result.Title,
			Content:      result.Snippet,
			Score:        item.Score * 0.75,
			EvidenceType: "search_snippet",
		})
	}
	return evidence
}

func (ca *CompanyAgent) planQueries(ctx context.Context, brief models.CompanyBrief) models.QueryPlan {
	var plan models.QueryPlan
	request := fmt.Sprintf("Company: %s\nDomain: %s\nIndustry: %s\nCountry: %s\nCity: %s\nAreas: %s\nOutput: %s\nTime: %s\nNotes: %s", brief.CompanyName, brief.Domain, brief.Industry, brief.Country, brief.City, strings.Join(brief.AreasOfInterest, ", "), strings.Join(brief.DesiredOutput, ", "), brief.TimeScope, brief.UserNotes)
	llmCtx, cancel := context.WithTimeout(ctx, companyPlanningTimeout)
	defer cancel()
	err := ca.engine.GenerateStructured(llmCtx, `Create a focused company web research query plan. Return JSON with a queries array. Each query needs query, purpose, target_area, and priority from 1 to 5. Cover leadership, locations, and user-requested areas. Treat procurement, sourcing, supply chain, and marketing as explicit department targets. Search conference speaker lists for Argus Petrochemicals Conference, Platts Global Energy Trading, and S&P Global Chemicals Summits when relevant to the company or industry. For department-level executives, create targeted queries using title variants such as CPO, Chief Procurement Officer, CSCO, Chief Supply Chain Officer, VP Sourcing, VP Supply Chain, CMO, Chief Marketing Officer, VP Sales, CRO, VP Engineering, CTO, and CFO. You may use site:linkedin.com/in/ in search queries to find public profile snippets, but do not assume LinkedIn pages can be directly scraped or that snippets prove employment without corroboration.`, request, &plan)
	if err == nil && len(plan.Queries) > 0 {
		return limitQueryPlan(plan, maxCompanySearchQueries)
	}
	return fallbackQueryPlan(brief)
}

func fallbackQueryPlan(brief models.CompanyBrief) models.QueryPlan {
	base := strings.TrimSpace(strings.Join([]string{brief.CompanyName, brief.Country, brief.City}, " "))
	queries := []models.PlannedQuery{
		{Query: base + " leadership executive team", Purpose: "identify leadership", TargetArea: "leadership", Priority: 1},
		{Query: base + " procurement purchasing sourcing strategic sourcing supply chain logistics CPO CSCO VP Head Director", Purpose: "identify procurement and supply chain executives", TargetArea: "procurement, sourcing, and supply chain", Priority: 2},
		{Query: base + " marketing brand communications growth digital marketing CMO VP Head Director", Purpose: "identify marketing executives", TargetArea: "marketing", Priority: 2},
		{Query: base + " chemicals procurement sourcing supply chain marketing speaker Argus Petrochemicals Conference Platts Global Energy Trading S&P Global Chemicals Summit", Purpose: "identify relevant industry conference speakers", TargetArea: "industry conference speakers", Priority: 2},
		{Query: base + " department leaders executives Chief VP Head Director General Manager", Purpose: "identify additional department executives", TargetArea: "department leadership", Priority: 2},
		{Query: base + " offices locations headquarters", Purpose: "identify locations", TargetArea: "locations", Priority: 3},
	}
	for _, area := range brief.AreasOfInterest {
		area = strings.TrimSpace(area)
		if area != "" {
			queries = append(queries, models.PlannedQuery{Query: base + " " + area, Purpose: "investigate requested area", TargetArea: area, Priority: 2})
		}
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
			if isBlockedSourceURL(result.Link) {
				continue
			}
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
	bm25Ranked := bm25RankResults(brief, results)
	if len(bm25Ranked) == 0 {
		return bm25Ranked
	}
	bm25Ranked = applySourceSignals(brief, bm25Ranked)

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
				return mergeRankings(bm25Ranked, normalized)
			}
		}
	case <-llmCtx.Done():
	}
	return bm25Ranked
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
		ranked = append(ranked, models.RankedResult{URL: result.Link, Relevant: score >= 0.2, Score: score, Reason: reason, EvidenceType: "bm25"})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	return ranked
}

func applySourceSignals(brief models.CompanyBrief, ranked []models.RankedResult) []models.RankedResult {
	if brief.Domain == "" {
		brief.Domain = inferOfficialDomain(brief.CompanyName, ranked)
	}
	for index := range ranked {
		item := &ranked[index]
		profile := classifySource(brief, item.URL)
		item.SourceType = profile.sourceType
		item.Official = profile.official
		item.QualityScore = profile.quality
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
	official := false
	primary := false
	if brief.Domain != "" {
		domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(brief.Domain), "www."))
		official = rawHost == domain || strings.HasSuffix(rawHost, "."+domain)
		primary = rawHost == domain
	}
	if strings.Contains(host, "ziprecruiter") || strings.Contains(host, "indeed.") || strings.Contains(host, "glassdoor.") || strings.Contains(host, "zoominfo.") || strings.Contains(host, "rocketreach.") {
		return sourceProfile{sourceType: "aggregator", quality: 0.1}
	}
	if strings.Contains(host, "linkedin.") {
		return sourceProfile{sourceType: "professional_profile", quality: 0.6}
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
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(value)), "/")
}

func (ca *CompanyAgent) scrapeRanked(ctx context.Context, sessionID string, ranked []models.RankedResult, searchResults []models.SearchResult, existing []models.Evidence, eventCh chan<- models.AgentEvent) ([]models.Evidence, []string, []models.ScrapeReportEntry) {
	evidence := make([]models.Evidence, 0, ca.engine.MaxScrapes())
	sources := make([]string, 0, ca.engine.MaxScrapes())
	report := make([]models.ScrapeReportEntry, 0, ca.engine.MaxScrapes()*2)
	known := make(map[string]bool, len(existing))
	for _, item := range existing {
		known[normalizeURL(item.URL)] = true
	}
	for _, item := range ranked {
		if item.SourceType != "professional_profile" || known[normalizeURL(item.URL)] {
			continue
		}
		if snippet := snippetEvidenceForURL(item, searchResults); snippet != nil {
			evidence = append(evidence, *snippet)
			known[normalizeURL(item.URL)] = true
		}
		report = append(report, models.ScrapeReportEntry{URL: item.URL, SourceType: item.SourceType, Status: "skipped", Reason: "public search snippet retained; direct professional-profile scraping is disabled"})
	}
	usableScrapes := 0
	attemptLimit := ca.engine.MaxScrapes() * 2
	for _, item := range selectScrapeCandidates(ranked, attemptLimit) {
		if usableScrapes >= ca.engine.MaxScrapes() {
			break
		}
		if known[normalizeURL(item.URL)] {
			continue
		}
		parsed, err := url.ParseRequestURI(item.URL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			report = append(report, models.ScrapeReportEntry{URL: item.URL, SourceType: item.SourceType, Status: "failed", Reason: "invalid target URL"})
			continue
		}
		Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolCall, Step: 8 + len(evidence), ToolName: "web_scraper", ToolInput: item.URL, Content: "Scraping " + item.SourceType + " source..."})
		scrape, err := ca.engine.ScrapeCached(ctx, item.URL)
		entry := models.ScrapeReportEntry{URL: item.URL, SourceType: item.SourceType, Status: "failed"}
		if err != nil {
			entry.Reason = err.Error()
			report = append(report, entry)
			if snippet := snippetEvidenceForURL(item, searchResults); snippet != nil {
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
			entry.Status = "blocked"
			entry.Reason = scrape.BlockReason
			report = append(report, entry)
			if snippet := snippetEvidenceForURL(item, searchResults); snippet != nil {
				evidence = append(evidence, *snippet)
			}
			continue
		}
		if len(scrape.Content) <= minScrapeContentLen {
			entry.Reason = "insufficient extracted content"
			report = append(report, entry)
			if snippet := snippetEvidenceForURL(item, searchResults); snippet != nil {
				evidence = append(evidence, *snippet)
			}
			continue
		}
		if len(scrape.Images) > 0 {
			entry.OCRStatus = "attempted"
			ocrCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			textByURL, ocrErr := ca.engine.ExtractImageTextDetailed(ocrCtx, scrape.Images)
			cancel()
			if ocrErr != nil {
				entry.OCRStatus = "failed"
				entry.OCRReason = ocrErr.Error()
			} else {
				entry.OCRStatus = "success"
			}
			for index := range scrape.Images {
				if text := textByURL[scrape.Images[index].URL]; text != "" {
					scrape.Images[index].ExtractedText = text
					entry.OCRImagesWithText++
				}
			}
		} else {
			entry.OCRStatus = "not_applicable"
		}
		evidence = append(evidence, models.Evidence{URL: scrape.CanonicalURL, Title: scrape.Title, Content: scrape.Content, Headings: scrape.Headings, JSONLD: scrape.JSONLD, Images: scrape.Images, Score: item.Score, EvidenceType: item.EvidenceType, SourceType: item.SourceType, QualityScore: scrape.QualityScore})
		if evidence[len(evidence)-1].URL == "" {
			evidence[len(evidence)-1].URL = item.URL
		}
		sources = append(sources, item.URL)
		usableScrapes++
		entry.Status = "success"
		report = append(report, entry)
	}
	return evidence, sources, report
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

func snippetEvidenceForURL(item models.RankedResult, results []models.SearchResult) *models.Evidence {
	for _, result := range results {
		if normalizeURL(result.Link) != normalizeURL(item.URL) || strings.TrimSpace(result.Snippet) == "" {
			continue
		}
		return &models.Evidence{
			URL:          result.Link,
			Title:        result.Title,
			Content:      result.Snippet,
			Score:        item.Score * 0.65,
			EvidenceType: "search_snippet_fallback",
			SourceType:   item.SourceType,
			QualityScore: 0.25,
		}
	}
	return nil
}

func selectScrapeCandidates(ranked []models.RankedResult, limit int) []models.RankedResult {
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
		case "leadership":
			return 4
		case "industry_event":
			return 5
		case "external":
			return 6
		default:
			return 6
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
		if candidate.SourceType == "professional_profile" || candidate.SourceType == "blocked" || isBlockedSourceURL(candidate.URL) {
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
			if candidate.SourceType == "professional_profile" || candidate.SourceType == "blocked" || isBlockedSourceURL(candidate.URL) {
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

func rootHost(host string) string {
	host = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(host), "www."))
	parts := strings.Split(host, ".")
	if len(parts) > 2 {
		return strings.Join(parts[len(parts)-2:], ".")
	}
	return host
}

func formatEvidence(evidence []models.Evidence) string {
	var builder strings.Builder
	for index, item := range evidence {
		builder.WriteString(fmt.Sprintf("\n--- Evidence %d: %s (%s), score %.2f ---\n%s\n", index+1, item.Title, item.URL, item.Score, item.Content))
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
