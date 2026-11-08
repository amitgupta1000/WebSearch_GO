package agent

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

const (
	// deepDefaultRounds is how many compose-search-extract-review rounds a run
	// gets by default, and deepMaxRounds is the hard cap.
	deepDefaultRounds = 3
	deepMaxRounds     = 5
	// deepDefaultSearchResults and deepMaxSearchResults bound hits per query.
	deepDefaultSearchResults = 8
	deepMaxSearchResults     = 10
	// deepDefaultScrapes and deepMaxScrapes bound total page fetches per run.
	deepDefaultScrapes = 12
	deepMaxScrapes     = 24
	// deepSearchConcurrency bounds parallel searches within one round.
	deepSearchConcurrency = 3
	// Decomposition sizes: the focus preference decides how widely the
	// question is taken apart before anything is searched.
	deepSubQuestionsRecency = 5
	deepSubQuestionsDepth   = 10
	// deepQueriesPerSubQuestion caps the custom queries composed per
	// sub-question. Composition is deliberately generous; selection is what
	// keeps the run bounded.
	deepQueriesPerSubQuestion = 5
	// deepMaxExecutedQueries caps the searches actually issued in one round,
	// sampled round-robin so every sub-question is covered.
	deepMaxExecutedQueries = 10
	// deepMaxJudgeCandidates bounds one relevance-judging call.
	deepMaxJudgeCandidates = 60
	// deepScrapeBatchSize caps pages read per round.
	deepScrapeBatchSize = 8
	// deepQAPairsPerSource caps extractive pairs taken per source.
	deepQAPairsPerSource = 3
	// deepMaxExtractChars bounds the page text sent to one extraction call.
	deepMaxExtractChars = 40000
	// deepSourceContentChars caps each source's content kept as evidence.
	deepSourceContentChars = 6000
	// deepMaxSynthesisChars bounds the total evidence text sent to synthesis.
	deepMaxSynthesisChars = 100000
	// Recency bonuses added to a candidate's judge score when the run favours
	// recency: a dated hit lifts a little, one dated in the current year more.
	deepRecencyYearBonus  = 0.2
	deepRecencyDatedBonus = 0.1
)

// Exported caps let the server bound the same budgets the agent enforces
// without duplicating the numbers, so the two cannot drift apart.
const (
	DeepMaxRounds        = deepMaxRounds
	DeepMaxSearchResults = deepMaxSearchResults
	DeepMaxScrapes       = deepMaxScrapes
)

// DeepSearchAgent runs the open-ended research workflow: decompose the
// question into sub-questions, compose custom queries, search, select results
// for relevance, scrape, extract grounded QA pairs, review coverage and rerun,
// and only then assemble the cited report. Every LLM stage is role-scoped —
// design, classify, extract, assemble — and none of them may contribute the
// model's own knowledge to the corpus or the output.
type DeepSearchAgent struct {
	engine *Engine
	now    func() time.Time
}

func NewDeepSearchAgent(engine *Engine) *DeepSearchAgent {
	return &DeepSearchAgent{engine: engine, now: time.Now}
}

func (a *DeepSearchAgent) Research(ctx context.Context, researchID string, query models.DeepSearchQuery, eventCh chan<- models.AgentEvent) (*models.DeepSearchReport, error) {
	query.Question = strings.TrimSpace(query.Question)
	if query.Question == "" {
		return nil, fmt.Errorf("deep search question is required")
	}
	switch strings.ToLower(strings.TrimSpace(query.Focus)) {
	case models.FocusRecency:
		query.Focus = models.FocusRecency
	default:
		query.Focus = models.FocusDepth
	}
	query.MaxRounds = deepBounded(query.MaxRounds, deepDefaultRounds, deepMaxRounds)
	query.MaxSearchResults = deepBounded(query.MaxSearchResults, deepDefaultSearchResults, deepMaxSearchResults)
	query.MaxScrapes = deepBounded(query.MaxScrapes, deepDefaultScrapes, deepMaxScrapes)

	now := a.now().UTC()
	report := &models.DeepSearchReport{
		ResearchID: researchID, Question: query.Question, Focus: query.Focus,
		AsOf: now, StartedAt: now,
	}
	subQuestionCount := deepSubQuestionsDepth
	if query.Focus == models.FocusRecency {
		subQuestionCount = deepSubQuestionsRecency
	}

	// Phase 1 — decompose. The planner takes the question apart into
	// sub-questions; it is told to design, not to answer.
	emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Decomposing the question into %d sub-question(s) (%s focus)", subQuestionCount, query.Focus), "", "")
	var plan models.DeepSearchPlan
	planPrompt := fmt.Sprintf("Research question: %s\nAs of: %s", query.Question, now.Format(time.RFC3339))
	if err := a.engine.GenerateStructured(ctx, deepPlanSystemPrompt(subQuestionCount, query.Focus), planPrompt, &plan); err != nil {
		return nil, fmt.Errorf("deep search decomposition failed: %w", err)
	}
	plan.SubQuestions = deepCleanList(plan.SubQuestions, subQuestionCount)
	if len(plan.SubQuestions) == 0 {
		plan.SubQuestions = []string{query.Question}
		report.Warnings = append(report.Warnings, "The planner returned no sub-questions; the raw question was used as the only one.")
	}
	report.Plan = plan
	emitAgentEvent(eventCh, models.EventToolCall, fmt.Sprintf("Plan: %s", plan.Understanding), "decompose",
		strings.Join(plan.SubQuestions, " | "))

	// Phase 2 — compose. Each sub-question gets its own custom search
	// queries; each round executes a bounded round-robin sample of them.
	emitAgentEvent(eventCh, models.EventThinking, "Composing custom search queries", "", "")
	var queryPlan models.DeepSearchQueryPlan
	if err := a.engine.GenerateStructured(ctx, deepComposeSystemPrompt(query.Focus),
		deepComposePrompt(query.Question, plan.SubQuestions), &queryPlan); err != nil {
		return nil, fmt.Errorf("deep search query composition failed: %w", err)
	}
	report.QueryPlan = deepNormalizeQueryPlan(queryPlan, plan.SubQuestions, report)
	if deepComposedCount(report.QueryPlan) == 0 {
		// Fall back to searching the sub-questions themselves.
		report.QueryPlan = deepFallbackQueryPlan(plan.SubQuestions)
		report.Warnings = append(report.Warnings, "The composer returned no queries; the sub-questions were used as the searches.")
	}
	emitAgentEvent(eventCh, models.EventToolCall,
		fmt.Sprintf("Composed %d query(ies) across %d sub-question(s)", deepComposedCount(report.QueryPlan), len(report.QueryPlan.Entries)),
		"compose", deepQueryPlanSummary(report.QueryPlan))

		// Round state: URLs already seen, the run-level scrape budget, and the
	// reviewer's rerun queries tagged for the round that should run them.
	// All are only touched on this goroutine: a round's searches run
	// concurrently, but their results are consumed after the round joins.
	visited := make(map[string]bool)
	scrapeBudget := query.MaxScrapes
	taggedReruns := make([]string, 0, 8)

	for round := 1; round <= query.MaxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Round 1 runs the composed queries; later rounds run only the
		// rerun queries the previous review requested.
		var queries []string
		subQuestionByQuery := make(map[string]string)
		if round == 1 {
			queries, subQuestionByQuery = deepSelectExecutedQueries(report.QueryPlan, deepMaxExecutedQueries)
		} else {
			queries = deepTakeTagged(taggedReruns, round)
			queries = deepCleanList(queries, deepMaxExecutedQueries)
		}
		if len(queries) == 0 {
			break
		}
		roundTrace := models.DeepSearchRound{Round: round, Queries: queries}
		if round == 1 {
			roundTrace.Composed = deepComposedCount(report.QueryPlan)
		}

		// Phase 3 — search. Queries in a round are independent, so they run
		// concurrently through the cached search path.
		outcomes := deepSearchRound(ctx, a.engine, queries, query.MaxSearchResults, eventCh)
		for index, outcome := range outcomes {
			report.QueriesUsed++
			if outcome.err != nil {
				if outcome.err == context.Canceled || outcome.err == context.DeadlineExceeded {
					return nil, outcome.err
				}
				report.Warnings = append(report.Warnings, fmt.Sprintf("round %d search %q failed: %v", round, queries[index], outcome.err))
				continue
			}
			roundTrace.SearchHits += len(outcome.hits)
			report.SearchHits += len(outcome.hits)
		}

		// Phase 4 — select. Candidates carry their sub-question attribution;
		// the judge sees only title, snippet, and date.
		candidates := deepBuildCandidates(outcomes, queries, subQuestionByQuery, visited, round)
		roundTrace.Candidates = len(candidates)
		if len(candidates) == 0 {
			report.Rounds = append(report.Rounds, roundTrace)
			continue
		}
		emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Judging %d candidate(s) for relevance", len(candidates)), "", "")
		selection, judgeErr := deepJudgeCandidates(ctx, a.engine, query, candidates)
		ordered := deepOrderCandidates(candidates, selection, judgeErr, query.Focus, now)

		// Phase 5 — read. Spend the scrape budget on the best-ranked pages.
		batch := scrapeBudget
		if batch > deepScrapeBatchSize {
			batch = deepScrapeBatchSize
		}
		if batch > len(ordered) {
			batch = len(ordered)
		}
		scrapeBudget -= batch
		roundTrace.Selected = batch
		scraped := deepReadRound(ctx, a.engine, report, round, ordered[:batch], eventCh, a.now().UTC())
		roundTrace.Scraped = scraped

		// Phase 6 — extract. Page text becomes grounded QA pairs.
		if scraped > 0 {
			roundTrace.QAPairs = deepExtractRound(ctx, a.engine, report, round, query, eventCh)
		}
		report.Rounds = append(report.Rounds, roundTrace)

		// Phase 7 — review. The last round and an empty corpus never need a
		// rerun plan; there is nothing to review against.
		if round == query.MaxRounds || len(report.QAPairs) == 0 {
			break
		}
		emitAgentEvent(eventCh, models.EventThinking, "Reviewing coverage of the collected answers", "", "")
		var review models.DeepSearchReview
		if err := a.engine.GenerateStructured(ctx, deepReviewSystemPrompt(),
			deepReviewPrompt(query.Question, report.Plan.SubQuestions, report.QAPairs), &review); err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("round %d review failed: %v", round, err))
			break
		}
		stored := &report.Rounds[len(report.Rounds)-1]
		stored.Complete = review.Complete
		stored.Missing = review.Missing
		if review.Complete {
			break
		}
		reruns := deepCleanList(review.RerunQueries, deepMaxExecutedQueries)
		if len(reruns) == 0 {
			break
		}
		emitAgentEvent(eventCh, models.EventToolCall,
			fmt.Sprintf("Round %d review: %d gap(s): %s", round, len(review.Missing), strings.Join(review.Missing, "; ")),
			"review", strings.Join(reruns, " | "))
		taggedReruns = append(taggedReruns, deepTaggedQueries(reruns, round)...)
	}
	report.RoundsRun = len(report.Rounds)
	report.PagesScraped = len(report.Sources)

	return report, deepFinish(ctx, a.engine, report, query, a.now().UTC(), eventCh)
}

// deepBuildCandidates turns a round's search outcomes into deduplicated
// candidates, keeping the sub-question each hit's query was composed for.
func deepBuildCandidates(outcomes []deepSearchOutcome, queries []string, subQuestionByQuery map[string]string, visited map[string]bool, round int) []models.DeepSearchCandidate {
	candidates := make([]models.DeepSearchCandidate, 0, len(outcomes)*4)
	for index, outcome := range outcomes {
		if outcome.err != nil || index >= len(queries) {
			continue
		}
		for _, hit := range outcome.hits {
			key := deepCanonicalURL(hit.Link)
			if key == "" || visited[key] {
				continue
			}
			visited[key] = true
			candidates = append(candidates, models.DeepSearchCandidate{
				ID:          fmt.Sprintf("r%d-%d", round, len(candidates)+1),
				SubQuestion: subQuestionByQuery[queries[index]],
				Title:       hit.Title,
				Snippet:     hit.Snippet,
				Date:        hit.Date,
				URL:         hit.Link,
			})
		}
	}
	if len(candidates) > deepMaxJudgeCandidates {
		candidates = candidates[:deepMaxJudgeCandidates]
	}
	return candidates
}

// deepJudgeCandidates asks the relevance judge which candidates are worth
// reading. The judge classifies only from title, snippet, and date.
func deepJudgeCandidates(ctx context.Context, eng *Engine, query models.DeepSearchQuery, candidates []models.DeepSearchCandidate) (models.DeepSearchSelection, error) {
	var selection models.DeepSearchSelection
	err := eng.GenerateStructured(ctx, deepJudgeSystemPrompt(query.Focus),
		deepJudgePrompt(query.Question, candidates), &selection)
	return selection, err
}

// deepOrderCandidates ranks the candidates the judge kept, applying the
// recency bonus on top of the judge score when the run favours recency. A
// failed judge call falls back to keeping every candidate in search order.
func deepOrderCandidates(candidates []models.DeepSearchCandidate, selection models.DeepSearchSelection, judgeErr error, focus string, now time.Time) []models.DeepSearchCandidate {
	if judgeErr != nil {
		return candidates
	}
	scores := make(map[string]float64, len(selection.Kept))
	for _, hit := range selection.Kept {
		scores[hit.ID] = hit.Score
	}
	type scored struct {
		candidate models.DeepSearchCandidate
		score     float64
	}
	ranked := make([]scored, 0, len(candidates))
	for _, candidate := range candidates {
		score, kept := scores[candidate.ID]
		if !kept {
			continue
		}
		if focus == models.FocusRecency {
			score += deepRecencyBonus(candidate.Date, now)
		}
		ranked = append(ranked, scored{candidate: candidate, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	ordered := make([]models.DeepSearchCandidate, 0, len(ranked))
	for _, item := range ranked {
		ordered = append(ordered, item.candidate)
	}
	return ordered
}

// deepRecencyBonus rewards dated hits when the run favours recency.
func deepRecencyBonus(date string, now time.Time) float64 {
	date = strings.TrimSpace(date)
	if date == "" {
		return 0
	}
	if strings.Contains(date, strconv.Itoa(now.Year())) {
		return deepRecencyYearBonus
	}
	return deepRecencyDatedBonus
}

// deepReadRound fetches the round's selected URLs, keeps every readable page
// as a source with its sub-question attribution, and reports how many pages
// were added.
func deepReadRound(ctx context.Context, eng *Engine, report *models.DeepSearchReport, round int, selected []models.DeepSearchCandidate, eventCh chan<- models.AgentEvent, fetchedAt time.Time) int {
	if len(selected) == 0 {
		return 0
	}
	emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Reading %d selected page(s)", len(selected)), "", "")
	urls := make([]string, 0, len(selected))
	for _, candidate := range selected {
		urls = append(urls, candidate.URL)
	}
	fetched := eng.ScrapeCachedBatch(ctx, urls)
	byURL := make(map[string]models.DeepSearchCandidate, len(selected))
	for _, candidate := range selected {
		byURL[deepCanonicalURL(candidate.URL)] = candidate
	}
	added := 0
	for index, outcome := range fetched {
		if outcome.Err != nil || outcome.Result == nil {
			continue
		}
		content := strings.TrimSpace(outcome.Result.Content)
		if content == "" {
			continue
		}
		if len(content) > deepSourceContentChars {
			content = content[:deepSourceContentChars]
		}
		source := models.DeepSearchSource{
			URL:       outcome.URL,
			Title:     outcome.Result.Title,
			Host:      deepHost(outcome.URL),
			Content:   content,
			Round:     round,
			FetchedAt: fetchedAt,
		}
		if index < len(urls) {
			if candidate, ok := byURL[deepCanonicalURL(urls[index])]; ok {
				source.SubQuestion = candidate.SubQuestion
				source.Snippet = candidate.Snippet
			}
		}
		report.Sources = append(report.Sources, source)
		added++
	}
	return added
}

// deepExtractRound turns the round's fresh sources into grounded QA pairs.
// Pairs pointing at a URL that was not scraped are dropped with a warning —
// extraction may only quote what the run actually read.
func deepExtractRound(ctx context.Context, eng *Engine, report *models.DeepSearchReport, round int, query models.DeepSearchQuery, eventCh chan<- models.AgentEvent) int {
	roundSources := make([]models.DeepSearchSource, 0, len(report.Sources))
	for _, source := range report.Sources {
		if source.Round == round {
			roundSources = append(roundSources, source)
		}
	}
	if len(roundSources) == 0 {
		return 0
	}
	emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Extracting answers from %d page(s)", len(roundSources)), "", "")
	var extraction models.DeepSearchExtraction
	prompt := fmt.Sprintf("Research question: %s\n\nSub-questions:\n%s\n\nPages read this round:\n%s",
		query.Question, deepBulletedList(report.Plan.SubQuestions), deepSourceBlocks(roundSources))
	if len(prompt) > deepMaxExtractChars {
		prompt = prompt[:deepMaxExtractChars]
	}
	if err := eng.GenerateStructured(ctx, deepExtractSystemPrompt(deepQAPairsPerSource), prompt, &extraction); err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("round %d extraction failed: %v", round, err))
		return 0
	}
	scrapedURLs := make(map[string]bool, len(roundSources))
	for _, source := range roundSources {
		scrapedURLs[source.URL] = true
	}
	perSource := make(map[string]int, len(roundSources))
	seen := make(map[string]bool)
	added := 0
	for _, pair := range extraction.Pairs {
		pair.Question = strings.TrimSpace(pair.Question)
		pair.Answer = strings.TrimSpace(pair.Answer)
		pair.URL = strings.TrimSpace(pair.URL)
		if pair.Question == "" || pair.Answer == "" || pair.URL == "" {
			continue
		}
		if !scrapedURLs[pair.URL] {
			report.Warnings = append(report.Warnings, fmt.Sprintf("round %d extraction cited unscraped URL %q and was dropped", round, pair.URL))
			continue
		}
		if perSource[pair.URL] >= deepQAPairsPerSource {
			continue
		}
		dedupeKey := pair.URL + "\x00" + strings.ToLower(pair.Question)
		if seen[dedupeKey] {
			continue
		}
		seen[dedupeKey] = true
		perSource[pair.URL]++
		pair.Round = round
		report.QAPairs = append(report.QAPairs, pair)
		added++
	}
	return added
}

// deepFinish closes the run: synthesize the cited report from the QA pairs,
// or close out honestly when nothing was read or nothing could be extracted.
func deepFinish(ctx context.Context, eng *Engine, report *models.DeepSearchReport, query models.DeepSearchQuery, completedAt time.Time, eventCh chan<- models.AgentEvent) error {
	if len(report.QAPairs) == 0 {
		if len(report.Sources) == 0 {
			report.Warnings = append(report.Warnings, "No page could be read during the run; no report was synthesized.")
		} else {
			report.Warnings = append(report.Warnings, "Pages were read but no grounded answers could be extracted from them; no report was synthesized.")
		}
		report.CompletedAt = completedAt
		emitAgentEvent(eventCh, models.EventComplete, "Deep search finished without usable evidence", "", "")
		return nil
	}
	emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Assembling the report from %d answer pair(s)", len(report.QAPairs)), "", "")
	if err := deepSynthesize(ctx, eng, report, query); err != nil {
		return err
	}
	report.CompletedAt = completedAt
	emitAgentEvent(eventCh, models.EventComplete,
		fmt.Sprintf("Deep search complete: %d round(s), %d source(s), %d answer pair(s), %d finding(s)",
			report.RoundsRun, len(report.Sources), len(report.QAPairs), len(report.Synthesis.Findings)), "", "")
	return nil
}

// deepSynthesize assembles the final report from the QA pairs alone. Raw page
// text is not re-sent: the pairs are the entire corpus, so nothing outside
// them — and nothing from the model's own knowledge — can enter the report.
func deepSynthesize(ctx context.Context, eng *Engine, report *models.DeepSearchReport, query models.DeepSearchQuery) error {
	var synthesis models.DeepSearchSynthesis
	prompt := fmt.Sprintf("Research question: %s\nFocus: %s\nAs of: %s\n\nSub-questions:\n%s\n\nExtracted question-answer pairs:\n%s",
		query.Question, query.Focus, report.AsOf.Format(time.RFC3339),
		deepBulletedList(report.Plan.SubQuestions), deepQAPairBlocks(report.QAPairs))
	if len(prompt) > deepMaxSynthesisChars {
		prompt = prompt[:deepMaxSynthesisChars]
		report.Warnings = append(report.Warnings, "Assembly context was bounded; not every answer pair was sent to the model.")
	}
	if err := eng.GenerateStructured(ctx, deepSynthesisSystemPrompt(query.Focus), prompt, &synthesis); err != nil {
		return fmt.Errorf("deep search assembly failed: %w", err)
	}
	if strings.TrimSpace(synthesis.Title) == "" || strings.TrimSpace(synthesis.Summary) == "" || strings.TrimSpace(synthesis.Report) == "" {
		return fmt.Errorf("deep search assembly returned an incomplete title, summary, or report")
	}
	deepDropUnsourced(&synthesis, report.Sources, report)
	report.Synthesis = synthesis
	return nil
}

// deepDropUnsourced removes findings and citations that point at URLs the run
// never read. A general deep-search run keeps its report when one citation
// hallucinated — invalid entries are dropped with a warning instead of failing
// the run, so the grounding rule is enforced even against model mistakes.
func deepDropUnsourced(synthesis *models.DeepSearchSynthesis, sources []models.DeepSearchSource, report *models.DeepSearchReport) {
	known := make(map[string]bool, len(sources))
	for _, source := range sources {
		known[source.URL] = true
	}
	filter := func(findings []models.DeepSearchFinding) []models.DeepSearchFinding {
		kept := make([]models.DeepSearchFinding, 0, len(findings))
		for _, finding := range findings {
			urls := make([]string, 0, len(finding.URLs))
			for _, cited := range finding.URLs {
				if known[cited] {
					urls = append(urls, cited)
				}
			}
			if len(urls) == 0 {
				continue
			}
			finding.URLs = urls
			kept = append(kept, finding)
		}
		return kept
	}
	before := len(synthesis.Findings) + len(synthesis.Citations)
	synthesis.Findings = filter(synthesis.Findings)
	synthesis.Citations = filter(synthesis.Citations)
	if len(synthesis.Findings)+len(synthesis.Citations) < before {
		report.Warnings = append(report.Warnings, "Some model-claimed citations did not match a collected source URL and were dropped.")
	}
}

// deepSearchRound fans one round's queries out over the searcher.
func deepSearchRound(ctx context.Context, eng *Engine, queries []string, maxResults int, eventCh chan<- models.AgentEvent) []deepSearchOutcome {
	outcomes := make([]deepSearchOutcome, len(queries))
	limit := deepSearchConcurrency
	if limit > len(queries) {
		limit = len(queries)
	}
	semaphore := make(chan struct{}, limit)
	var waitGroup sync.WaitGroup
	for index, query := range queries {
		waitGroup.Add(1)
		go func(index int, query string) {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			if err := ctx.Err(); err != nil {
				outcomes[index].err = err
				return
			}
			emitAgentEvent(eventCh, models.EventToolCall, "Searching", "search", query)
			outcomes[index].hits, outcomes[index].err = eng.SearchCached(ctx, query, maxResults)
		}(index, query)
	}
	waitGroup.Wait()
	return outcomes
}

type deepSearchOutcome struct {
	hits []models.SearchResult
	err  error
}

// deepNormalizeQueryPlan trims, dedupes, and caps the composed queries, and
// drops entries with nothing usable. Normalization problems are reported as
// run warnings, not errors.
func deepNormalizeQueryPlan(queryPlan models.DeepSearchQueryPlan, subQuestions []string, report *models.DeepSearchReport) models.DeepSearchQueryPlan {
	known := make(map[string]bool, len(subQuestions))
	for _, sub := range subQuestions {
		known[strings.ToLower(sub)] = true
	}
	normalized := models.DeepSearchQueryPlan{Entries: make([]models.DeepSubQuestionQueries, 0, len(queryPlan.Entries))}
	for _, entry := range queryPlan.Entries {
		entry.SubQuestion = strings.TrimSpace(entry.SubQuestion)
		entry.Queries = deepCleanList(entry.Queries, deepQueriesPerSubQuestion)
		if entry.SubQuestion == "" || len(entry.Queries) == 0 {
			continue
		}
		if !known[strings.ToLower(entry.SubQuestion)] {
			report.Warnings = append(report.Warnings, fmt.Sprintf("The composer invented sub-question %q; its queries were kept.", entry.SubQuestion))
		}
		normalized.Entries = append(normalized.Entries, entry)
	}
	return normalized
}

// deepFallbackQueryPlan searches the sub-questions themselves.
func deepFallbackQueryPlan(subQuestions []string) models.DeepSearchQueryPlan {
	entries := make([]models.DeepSubQuestionQueries, 0, len(subQuestions))
	for _, sub := range subQuestions {
		entries = append(entries, models.DeepSubQuestionQueries{SubQuestion: sub, Queries: []string{sub}})
	}
	return models.DeepSearchQueryPlan{Entries: entries}
}

func deepComposedCount(plan models.DeepSearchQueryPlan) int {
	total := 0
	for _, entry := range plan.Entries {
		total += len(entry.Queries)
	}
	return total
}

func deepQueryPlanSummary(plan models.DeepSearchQueryPlan) string {
	parts := make([]string, 0, len(plan.Entries))
	for _, entry := range plan.Entries {
		parts = append(parts, fmt.Sprintf("%s: %s", entry.SubQuestion, strings.Join(entry.Queries, " / ")))
	}
	return strings.Join(parts, " | ")
}

// deepSelectExecutedQueries samples the composed queries round-robin across
// sub-questions, so a wide decomposition still gets one search each before a
// narrow one gets its second. It returns the executed queries and the
// sub-question each belongs to.
func deepSelectExecutedQueries(plan models.DeepSearchQueryPlan, cap int) ([]string, map[string]string) {
	queries := make([]string, 0, cap)
	subByQuery := make(map[string]string, cap)
	seen := make(map[string]bool, cap)
	for offset := 0; len(queries) < cap; offset++ {
		progressed := false
		for _, entry := range plan.Entries {
			if offset >= len(entry.Queries) || len(queries) >= cap {
				continue
			}
			query := entry.Queries[offset]
			key := strings.ToLower(query)
			if seen[key] {
				continue
			}
			seen[key] = true
			queries = append(queries, query)
			subByQuery[query] = entry.SubQuestion
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return queries, subByQuery
}

// deepTakeTagged pulls the rerun queries tagged for this round. Rerun queries
// carry an r<N>| prefix marking the round that should run them.
func deepTakeTagged(tagged []string, round int) []string {
	prefix := fmt.Sprintf("r%d|", round)
	queries := make([]string, 0, len(tagged))
	for _, query := range tagged {
		if strings.HasPrefix(query, prefix) {
			queries = append(queries, strings.TrimPrefix(query, prefix))
		}
	}
	return queries
}

// deepTaggedQueries marks rerun queries with the round that should run them.
func deepTaggedQueries(queries []string, round int) []string {
	prefix := fmt.Sprintf("r%d|", round+1)
	tagged := make([]string, 0, len(queries))
	for _, query := range queries {
		tagged = append(tagged, prefix+query)
	}
	return tagged
}

func deepBulletedList(values []string) string {
	lines := make([]string, 0, len(values))
	for _, value := range values {
		lines = append(lines, "- "+value)
	}
	return strings.Join(lines, "\n")
}

// deepSourceBlocks renders a round's scraped pages for the extractor.
func deepSourceBlocks(sources []models.DeepSearchSource) string {
	blocks := make([]string, 0, len(sources))
	for _, source := range sources {
		sub := ""
		if source.SubQuestion != "" {
			sub = " | serves: " + source.SubQuestion
		}
		blocks = append(blocks, fmt.Sprintf("[url=%s] %s%s\n%s", source.URL, source.Title, sub, source.Content))
	}
	return strings.Join(blocks, "\n\n")
}

// deepQAPairBlocks renders the extracted pairs for the reviewer and the
// assembler. These pairs are the entire corpus of the final report.
func deepQAPairBlocks(pairs []models.DeepSearchQAPair) string {
	blocks := make([]string, 0, len(pairs))
	for index, pair := range pairs {
		block := fmt.Sprintf("[pair %d] url=%s | round=%d\nQ: %s\nA: %s", index+1, pair.URL, pair.Round, pair.Question, pair.Answer)
		if pair.SubQuestion != "" {
			block += "\nServes sub-question: " + pair.SubQuestion
		}
		if pair.Quote != "" {
			block += "\nQuote: " + pair.Quote
		}
		blocks = append(blocks, block)
	}
	return strings.Join(blocks, "\n\n")
}

func deepComposePrompt(question string, subQuestions []string) string {
	return fmt.Sprintf("Research question: %s\n\nSub-questions:\n%s", question, deepBulletedList(subQuestions))
}

func deepJudgePrompt(question string, candidates []models.DeepSearchCandidate) string {
	var sb strings.Builder
	sb.WriteString("Research question:\n" + question + "\n\nCandidates:\n")
	for _, candidate := range candidates {
		sb.WriteString(fmt.Sprintf("[id=%s] serves: %s | title: %s | date: %s | url: %s\nsnippet: %s\n\n",
			candidate.ID, candidate.SubQuestion, candidate.Title, candidate.Date, candidate.URL, candidate.Snippet))
	}
	return sb.String()
}

func deepReviewPrompt(question string, subQuestions []string, pairs []models.DeepSearchQAPair) string {
	return fmt.Sprintf("Research question: %s\n\nSub-questions:\n%s\n\nAnswers collected so far:\n%s",
		question, deepBulletedList(subQuestions), deepQAPairBlocks(pairs))
}

// The grounding clause every deep-search prompt carries. The LLM's own
// knowledge is never an admissible source: it designs, classifies, extracts,
// and assembles — it does not contribute facts.
const deepGroundingClause = "You never add facts from your own knowledge. Work only with the material supplied to you; if it does not contain something, say so instead of filling the gap yourself."

func deepPlanSystemPrompt(subQuestions int, focus string) string {
	focusNote := "Aim for comprehensive coverage of every distinct facet of the question."
	if focus == models.FocusRecency {
		focusNote = "Aim for the facets that decide the CURRENT state of the topic: latest status, recent decisions, and what changed most recently."
	}
	return fmt.Sprintf(`You are a research planner. Decompose the research question into exactly %d distinct sub-questions.
Return exactly one JSON object with these keys:
{
  "understanding": "one-sentence restatement of what is being asked",
  "sub_questions": ["..."]
}
%s
Sub-questions must be answerable from web sources, must not overlap, and together must
cover the question. %s`, subQuestions, focusNote, deepGroundingClause)
}

func deepComposeSystemPrompt(focus string) string {
	focusNote := "Preise authoritative and comprehensive sources."
	if focus == models.FocusRecency {
		focusNote = "Include recency qualifiers (the current year, or the word 'latest') where it is natural to do so."
	}
	return fmt.Sprintf(`You are a search-query composer. For each sub-question, write up to 5 custom web-search queries a search engine can answer.
Return exactly one JSON object with these keys:
{
  "entries": [{"sub_question": "the sub-question verbatim", "queries": ["..."]}]
}
Vary phrasing, key entities, and phrasing style across the queries so the results complement
each other rather than duplicate. No boolean operators, no site: prefixes unless a specific
authoritative site is clearly required. %s %s`, focusNote, deepGroundingClause)
}

func deepJudgeSystemPrompt(focus string) string {
	focusNote := "Prefer candidates that promise comprehensive, authoritative coverage of their sub-question."
	if focus == models.FocusRecency {
		focusNote = "Prefer candidates that promise the most recent information; an undated hit is worth less than a dated one."
	}
	return fmt.Sprintf(`You are a relevance judge. Given a research question and candidate search hits (title, snippet, date), decide which candidates are worth reading to answer the question.
Return exactly one JSON object with these keys:
{
  "kept": [{"id": "the candidate id", "score": 0.0, "reason": "short reason"}]
}
Keep only candidates whose title or snippet shows they can contribute evidence for their
sub-question. Score kept candidates from 0 to 1 on how well they promise to answer it.
%s
Judge only what the title, snippet, and date say. Do not assume a page's content.`, focusNote)
}

func deepExtractSystemPrompt(pairsPerSource int) string {
	return fmt.Sprintf(`You are a strict extractive reader. From each supplied page, extract at most %d question-answer pairs that the page text actually supports.
Return exactly one JSON object with these keys:
{
  "pairs": [{"sub_question": "the sub-question this serves, verbatim", "question": "the specific question answered", "answer": "what the page says, faithful to it", "quote": "short verbatim quote supporting the answer", "url": "the exact supplied page url"}]
}
Answers must be grounded in the page text: restate what the page says, never what you know.
If a page answers nothing relevant, skip it. Never invent a URL. %s`, pairsPerSource, deepGroundingClause)
}

func deepReviewSystemPrompt() string {
	return `You are a coverage reviewer. Given the research question, its sub-questions, and the answers collected so far, decide whether the evidence is sufficient to assemble a comprehensive answer.
Return exactly one JSON object with these keys:
{
  "complete": true|false,
  "missing": ["named gaps"],
  "rerun_queries": ["..."]
}
Judge coverage only against the supplied sub-questions and answers. A gap matters only when a
sub-question is unanswered or answers contradict each other unresolved. rerun_queries must
target the gaps directly and differ from the queries already run. %s`
}

func deepSynthesisSystemPrompt(focus string) string {
	focusNote := ""
	if focus == models.FocusRecency {
		focusNote = "Foreground the latest developments and attach their dates from the answers."
	}
	return fmt.Sprintf(`You are a report assembler. Assemble the final research report using ONLY the supplied question-answer pairs.
Return exactly one JSON object with these keys:
{
  "title": "non-empty report title",
  "summary": "non-empty concise summary",
  "report": "non-empty complete research report as markdown",
  "findings": [{"statement": "...", "urls": ["supplied pair url"]}],
  "open_questions": ["..."],
  "citations": [{"statement": "...", "urls": ["supplied pair url"]}]
}
The title, summary, and report are required and must never be empty. Structure the report with
markdown headings. Every factual claim must trace to one or more supplied answer pairs, and
every URL in findings and citations must be a supplied pair URL. Where answers contradict each
other, present both attributed versions instead of silently picking one. If the pairs leave a
facet of the question unanswered, name it in open_questions instead of writing around it.
%s
%s`, focusNote, deepGroundingClause)
}

// deepCanonicalURL normalizes a hit URL for dedupe: the host is lowercased,
// "www." stripped, and the fragment dropped. Unlike the news mode, the query
// string is kept — on the open web it often is the page.
func deepCanonicalURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return ""
	}
	parsed.Fragment = ""
	hostname := strings.ToLower(parsed.Hostname())
	hostname = strings.TrimPrefix(hostname, "www.")
	if port := parsed.Port(); port != "" {
		parsed.Host = hostname + ":" + port
	} else {
		parsed.Host = hostname
	}
	return strings.TrimRight(parsed.String(), "/")
}

func deepHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
}

// deepBounded clamps a caller-supplied budget between a default and a cap.
func deepBounded(value, defaultValue, maxValue int) int {
	if value <= 0 {
		return defaultValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

// deepCleanList trims, dedupes, and caps a list of strings.
func deepCleanList(values []string, capValue int) []string {
	cleaned := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[strings.ToLower(value)] {
			continue
		}
		seen[strings.ToLower(value)] = true
		cleaned = append(cleaned, value)
		if len(cleaned) >= capValue {
			break
		}
	}
	return cleaned
}










