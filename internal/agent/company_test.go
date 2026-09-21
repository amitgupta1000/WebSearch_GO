package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

type qcMockLLM struct {
	reviewCalls int
}

func (m *qcMockLLM) GenerateStructured(_ context.Context, _ string, _ string, target any) error {
	switch value := target.(type) {
	case *models.OrgChart:
		value.CompanyName = "Acme"
	case *models.QualityReview:
		m.reviewCalls++
		if m.reviewCalls < 3 {
			value.Verdict = "revise"
			value.FollowUpQueries = []models.PlannedQuery{{Query: "Acme focused leadership update", Purpose: "resolve QC gap", TargetArea: "leadership", Priority: 1}}
		} else {
			value.Verdict = "pass"
		}
	}
	return nil
}

type answerMockLLM struct{}

func (answerMockLLM) GenerateStructured(_ context.Context, _ string, _ string, target any) error {
	if value, ok := target.(*models.QuestionAnswer); ok {
		value.Answer = "The cached evidence identifies Acme's executive team."
		value.Confidence = 0.9
	}
	return nil
}

type invalidRankingLLM struct{}

func (invalidRankingLLM) GenerateStructured(_ context.Context, _ string, _ string, target any) error {
	value := reflect.ValueOf(target).Elem().FieldByName("RankedResults")
	value.Set(reflect.ValueOf([]models.RankedResult{{
		URL:      "https://wrong.example/result",
		Relevant: true,
		Score:    1,
	}}))
	return nil
}

type blockingRankingLLM struct{}

func (blockingRankingLLM) GenerateStructured(ctx context.Context, _ string, _ string, _ any) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestFallbackQueryPlanUsesCompanyScope(t *testing.T) {
	plan := fallbackQueryPlan(models.CompanyBrief{
		CompanyName: "Acme",
		ResearchPreferences: models.ResearchPreferences{
			Country:         "Germany",
			City:            "Berlin",
			AreasOfInterest: []string{"subsidiaries"},
		},
	})

	if len(plan.Queries) != 7 {
		t.Fatalf("expected seven planned queries, got %d", len(plan.Queries))
	}
	if plan.Queries[0].Query != "Acme Germany Berlin leadership executive team" {
		t.Errorf("unexpected first query: %q", plan.Queries[0].Query)
	}
	foundInterest := false
	for _, query := range plan.Queries {
		if query.TargetArea == "subsidiaries" {
			foundInterest = true
			break
		}
	}
	if !foundInterest {
		t.Errorf("expected interest query target area in plan, got %+v", plan.Queries)
	}
	foundConferenceQuery := false
	for _, query := range plan.Queries {
		if strings.Contains(query.Query, "Argus Petrochemicals Conference") && strings.Contains(query.Query, "Platts Global Energy Trading") && strings.Contains(query.Query, "S&P Global Chemicals Summit") {
			foundConferenceQuery = true
			break
		}
	}
	if !foundConferenceQuery {
		t.Errorf("expected chemical conference speaker query in plan, got %+v", plan.Queries)
	}
}

func TestDeterministicRankPrefersOfficialDomain(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "Acme", Domain: "acme.com"}
	result := deterministicRank(brief, models.SearchResult{
		Title:   "Acme leadership team",
		Link:    "https://acme.com/about/leadership",
		Snippet: "Acme executive leadership and departments",
	})

	if !result.Relevant || result.Score < 0.8 {
		t.Errorf("expected a highly relevant official result, got %+v", result)
	}
}

func TestRankResultsFallsBackWhenLLMReturnsUnknownURLs(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	engine := NewEngine(invalidRankingLLM{}, &mockSearcher{}, &mockScraper{}, cacheStore, 1, 3, 10)
	agent := NewCompanyAgent(engine)
	results := []models.SearchResult{{Title: "Acme leadership", Link: "https://acme.com/leadership", Snippet: "Acme executive team"}}
	ranked := agent.rankResults(context.Background(), models.CompanyBrief{CompanyName: "Acme"}, results)
	if len(ranked) != 1 || ranked[0].URL != results[0].Link {
		t.Fatalf("expected deterministic fallback candidate, got %+v", ranked)
	}
}

func TestSearchSnippetEvidenceHandlesScrapeFailure(t *testing.T) {
	ranked := []models.RankedResult{{URL: "https://acme.com/leadership", Relevant: true, Score: 0.8}}
	results := []models.SearchResult{{Title: "Acme leadership", Link: "https://acme.com/leadership", Snippet: "Acme executive team"}}
	evidence := searchSnippetEvidence(ranked, results, 1)
	if len(evidence) != 1 || evidence[0].EvidenceType != "search_snippet" {
		t.Fatalf("expected search snippet evidence, got %+v", evidence)
	}
}

func TestScrapeRankedRetainsStructuredMetadata(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	engine := NewEngine(&mockLLM{}, &mockSearcher{}, &mockScraper{}, cacheStore, 1, 3, 10)
	agent := NewCompanyAgent(engine)
	evidence, _, _ := agent.scrapeRanked(context.Background(), "session", []models.RankedResult{{
		URL:        "https://acme.com/leadership",
		Relevant:   true,
		Score:      0.9,
		SourceType: "official_leadership",
	}}, nil, nil, nil)
	if len(evidence) != 1 || len(evidence[0].Headings) != 1 || len(evidence[0].JSONLD) != 1 {
		t.Fatalf("expected structured metadata to be retained, got %+v", evidence)
	}
	_, _, report := agent.scrapeRanked(context.Background(), "session", []models.RankedResult{{
		URL:        "https://acme.com/leadership",
		Relevant:   true,
		Score:      0.9,
		SourceType: "official_leadership",
	}}, nil, nil, nil)
	if len(report) != 1 || report[0].Status != "success" {
		t.Fatalf("expected successful scrape report, got %+v", report)
	}
	formatted := formatEvidence(evidence)
	if !strings.Contains(formatted, "Headings: Leadership") || !strings.Contains(formatted, "Structured metadata:") {
		t.Fatalf("expected structured metadata in formatted evidence, got %q", formatted)
	}
}

func TestLinkedInSearchSnippetIsRetainedWithoutDirectScrape(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	engine := NewEngine(&mockLLM{}, &mockSearcher{}, &mockScraper{}, cacheStore, 1, 3, 10)
	agent := NewCompanyAgent(engine)
	ranked := []models.RankedResult{{
		URL:        "https://www.linkedin.com/in/jane-doe",
		Relevant:   true,
		Score:      0.9,
		SourceType: "professional_profile",
	}}
	results := []models.SearchResult{{
		Title:   "Jane Doe - Chief Technology Officer - Acme | LinkedIn",
		Link:    ranked[0].URL,
		Snippet: "Jane Doe is Chief Technology Officer at Acme.",
	}}
	evidence, _, _ := agent.scrapeRanked(context.Background(), "session", ranked, results, nil, nil)
	if len(evidence) != 1 || evidence[0].EvidenceType != "search_snippet_fallback" {
		t.Fatalf("expected LinkedIn snippet fallback, got %+v", evidence)
	}
}

func TestFormatEvidenceIncludesImageOCR(t *testing.T) {
	formatted := formatEvidence([]models.Evidence{{
		URL:    "https://acme.com/leadership",
		Images: []models.Image{{URL: "https://acme.com/ceo.jpg", ExtractedText: "Chief Executive Officer"}},
	}})
	if !strings.Contains(formatted, "OCR: Chief Executive Officer") {
		t.Fatalf("expected OCR text in formatted evidence, got %q", formatted)
	}
}

func TestNormalizeRankedResultsAcceptsTrailingSlash(t *testing.T) {
	ranked := normalizeRankedResults(
		[]models.RankedResult{{URL: "HTTPS://ACME.COM/leadership/", Relevant: true, Score: 0.9}},
		[]models.SearchResult{{Link: "https://acme.com/leadership"}},
	)
	if len(ranked) != 1 {
		t.Fatalf("expected normalized URL to match, got %+v", ranked)
	}
}

func TestBM25RanksOfficialCompanyResult(t *testing.T) {
	results := []models.SearchResult{
		{Title: "Energy market commentary", Link: "https://news.example.com/story", Snippet: "A broad energy industry article."},
		{Title: "Acme leadership team", Link: "https://acme.com/about/leadership", Snippet: "Acme executive leadership and company departments."},
	}
	ranked := bm25RankResults(models.CompanyBrief{CompanyName: "Acme", Domain: "acme.com"}, results)
	if len(ranked) != 2 || ranked[0].URL != "https://acme.com/about/leadership" {
		t.Fatalf("expected official company result first, got %+v", ranked)
	}
	if ranked[0].EvidenceType != "bm25" {
		t.Errorf("expected BM25 evidence type, got %q", ranked[0].EvidenceType)
	}
}

func TestSourceSignalsPreferOfficialLeadershipOverAggregator(t *testing.T) {
	results := []models.SearchResult{
		{Title: "Acme executives", Link: "https://www.ziprecruiter.com/company/acme", Snippet: "Acme leadership information"},
		{Title: "Acme leadership", Link: "https://acme.com/about/leadership", Snippet: "Acme executive team"},
	}
	ranked := applySourceSignals(models.CompanyBrief{CompanyName: "Acme", Domain: "acme.com"}, bm25RankResults(models.CompanyBrief{CompanyName: "Acme", Domain: "acme.com"}, results))
	if len(ranked) != 2 || ranked[0].SourceType != "official_leadership" || !ranked[0].Official {
		t.Fatalf("expected official leadership first, got %+v", ranked)
	}
	if ranked[1].Relevant {
		t.Errorf("expected aggregator to be excluded, got %+v", ranked[1])
	}
}

func TestSourceSignalsPreferPrimaryDomainOverOfficialSubdomain(t *testing.T) {
	ranked := applySourceSignals(models.CompanyBrief{CompanyName: "Acme", Domain: "acme.com"}, []models.RankedResult{
		{URL: "https://europe.acme.com/leadership", Relevant: true, Score: 0.70},
		{URL: "https://acme.com/about/leadership", Relevant: true, Score: 0.68},
	})
	if len(ranked) != 2 || ranked[0].URL != "https://acme.com/about/leadership" {
		t.Fatalf("expected primary domain first, got %+v", ranked)
	}
	if !strings.Contains(ranked[0].Reason, "primary domain") {
		t.Errorf("expected primary-domain reason, got %q", ranked[0].Reason)
	}
}

func TestClassifySourceRecognizesIndustryEventSources(t *testing.T) {
	profile := classifySource(models.CompanyBrief{}, "https://www.argusmedia.com/en/events/argus-petrochemicals-conference/speakers")
	if profile.sourceType != "industry_event" || profile.quality < 0.7 {
		t.Fatalf("expected industry event source, got %+v", profile)
	}
}

func TestWikipediaSourcesAreBlocked(t *testing.T) {
	urls := []string{
		"https://en.wikipedia.org/wiki/Acme",
		"https://www.wikipedia.org/wiki/Acme",
		"https://en.m.wikipedia.org/wiki/Acme",
	}
	for _, targetURL := range urls {
		if !isBlockedSourceURL(targetURL) {
			t.Errorf("expected Wikipedia URL to be blocked: %s", targetURL)
		}
	}
	filtered := filterBlockedSearchResults([]models.SearchResult{
		{Link: urls[0]},
		{Link: "https://acme.com/leadership"},
	})
	if len(filtered) != 1 || filtered[0].Link != "https://acme.com/leadership" {
		t.Fatalf("expected only non-Wikipedia result, got %+v", filtered)
	}
}

func TestInferOfficialDomainFromSearchResults(t *testing.T) {
	ranked := []models.RankedResult{
		{URL: "https://www.chevron.com/who-we-are/leadership"},
		{URL: "https://energydigital.com/news/chevron"},
	}
	if got := inferOfficialDomain("Chevron", ranked); got != "chevron.com" {
		t.Errorf("expected chevron.com, got %q", got)
	}
}

func TestRankResultsReturnsBM25WhenSemanticRerankerTimesOut(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	engine := NewEngine(blockingRankingLLM{}, &mockSearcher{}, &mockScraper{}, cacheStore, 1, 3, 10)
	agent := NewCompanyAgent(engine)
	results := []models.SearchResult{{Title: "Acme leadership", Link: "https://acme.com/leadership", Snippet: "Acme executive team"}}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	ranked := agent.rankResults(ctx, models.CompanyBrief{CompanyName: "Acme"}, results)
	if time.Since(started) > time.Second {
		t.Fatalf("BM25 fallback took too long: %v", time.Since(started))
	}
	if len(ranked) != 1 || ranked[0].EvidenceType != "bm25" {
		t.Fatalf("expected BM25 fallback, got %+v", ranked)
	}
}

func TestCompanyWorkflowUsesFallbackStages(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	engine := NewEngine(&mockLLM{}, &mockSearcher{}, &mockScraper{}, cacheStore, 1, 3, 10)
	agent := NewCompanyAgent(engine)
	chart, err := agent.ResearchCompanyWithBrief(context.Background(), "session", models.CompanyBrief{CompanyName: "Acme"}, nil)
	if err != nil {
		t.Fatalf("ResearchCompanyWithBrief() error: %v", err)
	}
	if chart.CompanyName != "Acme" {
		t.Errorf("expected fallback company name Acme, got %q", chart.CompanyName)
	}
}

type capturingSynthesisLLM struct {
	userPrompt string
}

func (m *capturingSynthesisLLM) GenerateStructured(_ context.Context, _ string, userPrompt string, target any) error {
	if value, ok := target.(*models.OrgChart); ok {
		m.userPrompt = userPrompt
		value.CompanyName = "Acme"
	}
	return nil
}

type failingReviewLLM struct{}

func (failingReviewLLM) GenerateStructured(_ context.Context, _ string, _ string, target any) error {
	switch value := target.(type) {
	case *models.OrgChart:
		value.CompanyName = "Acme"
		return nil
	case *models.QualityReview:
		return errors.New("review model unavailable")
	}
	return nil
}

func TestFormatProfileCandidatesRendersOnlyProfileSnippets(t *testing.T) {
	formatted := formatProfileCandidates([]models.Evidence{
		{
			URL:          "https://in.linkedin.com/in/jane-doe",
			Title:        "Jane Doe - Head of Procurement - Acme | LinkedIn",
			Content:      "Jane Doe is Head of Procurement at Acme.",
			EvidenceType: "search_snippet_fallback",
		},
		{
			URL:          "https://acme.com/leadership",
			Title:        "Leadership",
			Content:      "Acme executive team and board of directors.",
			EvidenceType: "scraped_page",
		},
	})

	if !strings.Contains(formatted, "Head of Procurement") || !strings.Contains(formatted, "jane-doe") {
		t.Fatalf("expected the profile candidate to be listed, got %q", formatted)
	}
	if strings.Contains(formatted, "acme.com/leadership") {
		t.Fatalf("scraped pages must not be listed as profile candidates: %q", formatted)
	}
	if got := formatProfileCandidates(nil); got != "" {
		t.Fatalf("expected an empty block when there are no profile snippets, got %q", got)
	}
}

func TestSynthesizeChartFeedsProfileCandidatesToTheModel(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	llm := &capturingSynthesisLLM{}
	engine := NewEngine(llm, &mockSearcher{}, &mockScraper{}, cacheStore, 1, 3, 10)
	agent := NewCompanyAgent(engine)

	evidence := []models.Evidence{{
		URL:          "https://in.linkedin.com/in/jane-doe",
		Title:        "Jane Doe - Head of Procurement - Acme | LinkedIn",
		Content:      "Jane Doe is Head of Procurement at Acme.",
		EvidenceType: "search_snippet_fallback",
	}}
	if _, err := agent.synthesizeChart(context.Background(), models.CompanyBrief{CompanyName: "Acme"}, evidence, nil, "session", nil); err != nil {
		t.Fatalf("synthesizeChart() error: %v", err)
	}
	if !strings.Contains(llm.userPrompt, "Profile candidates") {
		t.Fatalf("expected a profile candidate block in the synthesis prompt, got %q", llm.userPrompt)
	}
	if !strings.Contains(llm.userPrompt, "Head of Procurement") {
		t.Fatalf("expected the candidate headline in the synthesis prompt, got %q", llm.userPrompt)
	}
}

func TestReviewChartDoesNotReportFailedReviewAsPass(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	engine := NewEngine(failingReviewLLM{}, &mockSearcher{}, &mockScraper{}, cacheStore, 1, 3, 10)
	agent := NewCompanyAgent(engine)

	review := agent.reviewChart(context.Background(), models.CompanyBrief{CompanyName: "Acme"}, &models.OrgChart{CompanyName: "Acme"}, nil, "session", nil)
	if review.Verdict == "pass" {
		t.Fatalf("a failed QC review must not be reported as a pass: %+v", review)
	}
	if review.Verdict != "unavailable" || review.Score != 0 {
		t.Fatalf("expected an unavailable verdict with a zero score, got %+v", review)
	}
	if !strings.Contains(review.Reason, "review model unavailable") {
		t.Fatalf("expected the underlying failure in the reason, got %q", review.Reason)
	}
}

func TestHasBudgetForIteration(t *testing.T) {
	if !hasBudgetForIteration(context.Background()) {
		t.Error("a context without a deadline must be treated as unbounded")
	}

	plenty, cancelPlenty := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelPlenty()
	if !hasBudgetForIteration(plenty) {
		t.Error("expected budget to be available for a full iteration")
	}

	scarce, cancelScarce := context.WithTimeout(context.Background(), time.Second)
	defer cancelScarce()
	if hasBudgetForIteration(scarce) {
		t.Error("expected the budget to be too small for another iteration")
	}
}

func TestCompanyWorkflowStopsAfterThreeQCReviews(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	llm := &qcMockLLM{}
	engine := NewEngine(llm, &mockSearcher{}, &mockScraper{}, cacheStore, 1, 3, 10)
	agent := NewCompanyAgent(engine)
	chart, state, err := agent.ResearchCompanyRun(context.Background(), "session", models.CompanyBrief{CompanyName: "Acme"}, nil)
	if err != nil {
		t.Fatalf("ResearchCompanyRun() error: %v", err)
	}
	if chart == nil || len(state.Reviews) != 3 || llm.reviewCalls != 3 {
		t.Fatalf("expected exactly three QC reviews, got chart=%v reviews=%d calls=%d", chart != nil, len(state.Reviews), llm.reviewCalls)
	}
}

func TestAnswerQuestionUsesRetainedEvidence(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	engine := NewEngine(answerMockLLM{}, &mockSearcher{}, &mockScraper{}, cacheStore, 1, 3, 10)
	agent := NewCompanyAgent(engine)
	state := &models.CompanyResearchContext{
		Brief:    models.CompanyBrief{CompanyName: "Acme"},
		Evidence: []models.Evidence{{URL: "https://acme.com/leadership", Title: "Leadership", Content: "Acme executive team"}},
	}
	answer, err := agent.AnswerQuestion(context.Background(), state, &models.OrgChart{CompanyName: "Acme"}, "Who leads Acme?")
	if err != nil {
		t.Fatalf("AnswerQuestion() error: %v", err)
	}
	if answer.UsedFreshSearch || answer.Answer == "" {
		t.Errorf("expected retained-evidence answer, got %+v", answer)
	}
}
