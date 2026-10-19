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
	"github.com/amitgupta1000/WebSearch_GO/internal/testutil"
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

type documentTriageMockLLM struct {
	decision string
}

func (m documentTriageMockLLM) GenerateStructured(_ context.Context, _ string, _ string, target any) error {
	value := target.(*documentTriageDecision)
	value.Decision = m.decision
	value.Confidence = 0.95
	value.EntityMatch = true
	value.TopicMatch = true
	value.DateMatch = true
	value.Reason = "test decision"
	return nil
}

func TestFallbackQueryPlanUsesCompanyScope(t *testing.T) {
	brief := models.CompanyBrief{
		CompanyName: "Acme",
		Industry:    "chemicals",
		ResearchPreferences: models.ResearchPreferences{
			Country:         "Germany",
			City:            "Berlin",
			AreasOfInterest: []string{"subsidiaries"},
		},
	}

	plan := fallbackQueryPlan(brief)

	// Every query must be anchored on the company: the entity gate drops the ones
	// that are not, so the fallback plan has to satisfy its own filter.
	matcher := newEntityMatcher(brief)
	for _, query := range plan.Queries {
		if !matcher.covers(query.Query) {
			t.Errorf("fallback query %q is not anchored on the company", query.Query)
		}
	}
	if len(plan.Queries) < 6 || len(plan.Queries) > maxCompanySearchQueries {
		t.Fatalf("expected a full plan, got %d queries", len(plan.Queries))
	}
	if plan.Queries[0].Query != "Acme Germany Berlin leadership executive team management" {
		t.Errorf("unexpected first query: %q", plan.Queries[0].Query)
	}

	foundInterest, foundConference, foundLocations := false, false, false
	for _, query := range plan.Queries {
		switch {
		case query.TargetArea == "subsidiaries":
			foundInterest = true
		case strings.Contains(strings.ToLower(query.Query), "conference speaker"):
			foundConference = true
			if !strings.Contains(query.Query, "Acme") {
				t.Errorf("a conference query must name the company, got %q", query.Query)
			}
		case query.TargetArea == "locations":
			foundLocations = true
		}
	}
	if !foundInterest {
		t.Errorf("expected interest query target area in plan, got %+v", plan.Queries)
	}
	if !foundConference {
		t.Errorf("expected an anchored conference speaker query, got %+v", plan.Queries)
	}
	if !foundLocations {
		t.Errorf("expected a locations query in plan, got %+v", plan.Queries)
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
	evidence := searchSnippetEvidence(ranked, results, 1, nil)
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
	}}, nil, nil, nil, nil)
	if len(evidence) != 1 || len(evidence[0].Headings) != 1 || len(evidence[0].JSONLD) != 1 {
		t.Fatalf("expected structured metadata to be retained, got %+v", evidence)
	}
	_, _, report := agent.scrapeRanked(context.Background(), "session", []models.RankedResult{{
		URL:        "https://acme.com/leadership",
		Relevant:   true,
		Score:      0.9,
		SourceType: "official_leadership",
	}}, nil, nil, nil, nil)
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
	evidence, _, _ := agent.scrapeRanked(context.Background(), "session", ranked, results, nil, nil, nil)
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
	ranked := applySourceSignals(models.CompanyBrief{CompanyName: "Acme", Domain: "acme.com"}, bm25RankResults(models.CompanyBrief{CompanyName: "Acme", Domain: "acme.com"}, results), true)
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
	}, true)
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

func TestCompanyTokensDropCorporateAndGenericWords(t *testing.T) {
	tokens := companyTokens("KLJ Resources Limited")
	if len(tokens) != 2 {
		t.Fatalf("companyTokens() = %v, want the two distinctive words", tokens)
	}
	for _, token := range tokens {
		if token == "limited" {
			t.Errorf("a corporate suffix must not identify a company: %v", tokens)
		}
	}
	for _, want := range []string{"klj", "resources"} {
		found := false
		for _, token := range tokens {
			if token == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %q in %v", want, tokens)
		}
	}

	if got := companyTokens("Acme Group Holdings"); len(got) != 1 || got[0] != "acme" {
		t.Errorf("companyTokens(Acme Group Holdings) = %v, want [acme]", got)
	}
	if got := companyTokens("Limited"); len(got) != 0 {
		t.Errorf("a generic-only name has nothing to match on, got %v", got)
	}
}

func TestPruneUnconnectedDropsOtherCompaniesProfiles(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "KLJ Resources Limited"}
	ranked := []models.RankedResult{
		{URL: "https://kljresources.in/management.php", Relevant: true, Score: 0.8},
		{URL: "https://ke.linkedin.com/in/jasper-ogari-a9264691", Relevant: true, Score: 0.6},
		{URL: "https://be.linkedin.com/in/hilde-eylenbosch-29359b20", Relevant: true, Score: 0.5},
	}
	byURL := map[string]models.SearchResult{
		"https://kljresources.in/management.php": {Title: "KLJ Resources Limited - Management", Snippet: "Board and management team"},
		"https://ke.linkedin.com/in/jasper-ogari-a9264691": {
			Title: "Jasper Ogari - LinkedIn", Snippet: "Experience: gemeente Dilbeek - Location: Ternat, Belgium"},
		"https://be.linkedin.com/in/hilde-eylenbosch-29359b20": {
			Title: "Hilde Eylenbosch - gemeente Dilbeek", Snippet: "500+ connections on LinkedIn"},
	}

	got := pruneUnconnected(brief, ranked, byURL)
	if len(got) != 1 || !strings.Contains(got[0].URL, "kljresources.in") {
		t.Fatalf("expected only the company page to survive, got %+v", got)
	}
}

func TestPruneUnconnectedKeepsOfficialDomainMatches(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "Zenith Holdings", Domain: "zh-corp.example"}
	ranked := []models.RankedResult{{URL: "https://zh-corp.example/team", Relevant: true, Score: 0.4}}
	byURL := map[string]models.SearchResult{
		"https://zh-corp.example/team": {Title: "Our team", Snippet: "Meet the leadership"},
	}

	// The snippet never names the company, so only the domain rule can keep it.
	if got := pruneUnconnected(brief, ranked, byURL); len(got) != 1 {
		t.Fatalf("an official-domain match must be kept, got %+v", got)
	}
}

func TestPruneUnconnectedNeverStarvesTheCandidateList(t *testing.T) {
	ranked := []models.RankedResult{{URL: "https://example.com/report", Relevant: true, Score: 0.5}}
	byURL := map[string]models.SearchResult{
		"https://example.com/report": {Title: "Market report", Snippet: "Industry news"},
	}

	got := pruneUnconnected(models.CompanyBrief{CompanyName: "Acme Robotics"}, ranked, byURL)
	if len(got) != 1 {
		t.Fatalf("filtering must not empty the candidate list, got %+v", got)
	}

	// A name with nothing distinctive to match on must be left untouched.
	if got := pruneUnconnected(models.CompanyBrief{CompanyName: "Limited"}, ranked, byURL); len(got) != 1 {
		t.Fatalf("a generic-only name must not trigger filtering, got %+v", got)
	}
}

func TestRankResultsPrunesCandidatesUnconnectedToTheCompany(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	engine := NewEngine(&mockLLM{}, &mockSearcher{}, &mockScraper{}, cacheStore, 5, 12, 10)
	agent := NewCompanyAgent(engine)

	results := []models.SearchResult{
		{Title: "KLJ Resources Limited - Management", Link: "https://kljresources.in/management.php", Snippet: "KLJ Resources Limited board and management team"},
		{Title: "Jasper Ogari - LinkedIn", Link: "https://ke.linkedin.com/in/jasper-ogari-a9264691", Snippet: "Experience: gemeente Dilbeek - Location: Ternat, Belgium"},
		{Title: "Hilde Eylenbosch - gemeente Dilbeek", Link: "https://be.linkedin.com/in/hilde-eylenbosch-29359b20", Snippet: "500+ connections on LinkedIn"},
	}

	ranked := agent.rankResults(context.Background(), models.CompanyBrief{CompanyName: "KLJ Resources Limited"}, results)

	if len(ranked) != 1 || !strings.Contains(ranked[0].URL, "kljresources.in") {
		t.Fatalf("expected only the company page to reach the scrape stage, got %+v", ranked)
	}
}

func TestProfessionalProfileScrapingIsOffByDefault(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	scraper := &mockScraper{}
	engine := NewEngine(&mockLLM{}, &mockSearcher{}, scraper, cacheStore, 5, 3, 10)
	if engine.ProfileScrapingEnabled() {
		t.Fatal("profile scraping must be off unless explicitly enabled")
	}
	agent := NewCompanyAgent(engine)

	profileURL := "https://www.linkedin.com/in/jane-doe"
	ranked := []models.RankedResult{{URL: profileURL, Relevant: true, Score: 0.9, SourceType: "professional_profile"}}
	results := []models.SearchResult{{Title: "Jane Doe - Chief Technology Officer - Acme | LinkedIn", Link: profileURL, Snippet: "Jane Doe is Chief Technology Officer at Acme."}}

	evidence, _, report := agent.scrapeRanked(context.Background(), "session", ranked, results, nil, nil, nil)

	if len(scraper.calledURLs) != 0 {
		t.Errorf("profile pages must not be fetched by default, got %v", scraper.calledURLs)
	}
	if len(evidence) != 1 || evidence[0].EvidenceType != "search_snippet_fallback" {
		t.Fatalf("expected the snippet fallback to be retained, got %+v", evidence)
	}
	if len(report) != 1 || report[0].Status != "skipped" {
		t.Fatalf("expected a skipped report entry, got %+v", report)
	}
}

func TestProfessionalProfileScrapingOptInFetchesProfiles(t *testing.T) {
	cacheStore := cache.NewMemoryCache(100)
	defer cacheStore.Close()

	scraper := &mockScraper{}
	engine := NewEngine(&mockLLM{}, &mockSearcher{}, scraper, cacheStore, 5, 3, 10, WithProfileScraping(true))
	if !engine.ProfileScrapingEnabled() {
		t.Fatal("expected profile scraping to be enabled")
	}
	agent := NewCompanyAgent(engine)

	profileURL := "https://www.linkedin.com/in/jane-doe"
	ranked := []models.RankedResult{{URL: profileURL, Relevant: true, Score: 0.9, SourceType: "professional_profile"}}
	results := []models.SearchResult{{Title: "Jane Doe - Chief Technology Officer - Acme | LinkedIn", Link: profileURL, Snippet: "Jane Doe is Chief Technology Officer at Acme."}}

	evidence, _, report := agent.scrapeRanked(context.Background(), "session", ranked, results, nil, nil, nil)

	if len(scraper.calledURLs) != 1 || scraper.calledURLs[0] != profileURL {
		t.Errorf("expected the profile to be fetched when opted in, got %v", scraper.calledURLs)
	}
	if len(evidence) != 1 || evidence[0].EvidenceType == "search_snippet_fallback" {
		t.Fatalf("expected scraped page evidence, got %+v", evidence)
	}
	for _, entry := range report {
		if entry.Status == "skipped" {
			t.Errorf("no profile should be skipped when opted in, got %+v", entry)
		}
	}
}

// TestValidationReserveIsKeptForTheLookups pins the starvation fix that a live run exposed:
// 158 s of a 180 s deadline went to scraping, so the freshness pass ran zero reverse lookups
// and two named directors were excluded for want of a source. Inside the reserve no further
// scrape batch starts; with budget to spare the page is fetched as before.
func TestValidationReserveIsKeptForTheLookups(t *testing.T) {
	if hasValidationReserve(mustDeadline(t, 5*time.Second)) {
		t.Error("five seconds left is inside the reserve")
	}
	if !hasValidationReserve(mustDeadline(t, 5*time.Minute)) {
		t.Error("five minutes left leaves plenty of reserve")
	}
	if !hasValidationReserve(context.Background()) {
		t.Error("a context without a deadline imposes no reserve")
	}

	engine := NewEngine(&mockLLM{}, &mockSearcher{}, &mockScraper{}, cache.NewMemoryCache(50), 10, 5, 20)
	agent := NewCompanyAgent(engine)
	ranked := []models.RankedResult{{
		URL: "https://acme.com/leadership", Relevant: true, Score: 0.9, SourceType: "official_leadership",
	}}

	// Inside the reserve: nothing is fetched, so the lookups keep their budget.
	tight := mustDeadline(t, 5*time.Second)
	evidence, sources, report := agent.scrapeRanked(tight, "session", ranked, nil, nil, nil, nil)
	if len(evidence) != 0 || len(sources) != 0 || len(report) != 0 {
		t.Fatalf("expected no scraping inside the reserve, got %d evidence, %d sources, %d report rows",
			len(evidence), len(sources), len(report))
	}

	// With budget to spare the page is fetched.
	roomy := mustDeadline(t, 2*time.Minute)
	evidence, _, _ = agent.scrapeRanked(roomy, "session", ranked, nil, nil, nil, nil)
	if len(evidence) != 1 {
		t.Fatalf("expected the page to be fetched when the budget allows, got %d evidence items", len(evidence))
	}
}

// mustDeadline returns a context with the given deadline, for the reserve tests.
func mustDeadline(t *testing.T, within time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	t.Cleanup(cancel)
	return ctx
}

func TestTriageDocumentCandidatesSkipsIrrelevantPDF(t *testing.T) {
	engine := NewEngine(documentTriageMockLLM{decision: "not_relevant"}, &mockSearcher{}, &mockScraper{}, cache.NewMemoryCache(10), 2, 3, 10)
	agent := NewCompanyAgent(engine)
	brief := models.CompanyBrief{CompanyName: "Acme"}
	candidates := []models.RankedResult{{
		URL:        "https://example.test/other-company-report.pdf",
		SourceType: "report",
	}}
	results := []models.SearchResult{{
		Link:    candidates[0].URL,
		Title:   "Other Company Annual Report",
		Snippet: "A report for another company",
	}}

	kept, report := agent.triageDocumentCandidates(context.Background(), brief, candidates, results)
	if len(kept) != 0 {
		t.Fatalf("expected irrelevant PDF to be removed, kept %d candidates", len(kept))
	}
	if len(report) != 1 || report[0].TriageDecision != "not_relevant" || report[0].Status != "skipped" {
		t.Fatalf("unexpected triage report: %+v", report)
	}
}

type mockPreviewScraper struct {
	mockScraper
	previewBytes []byte
}

func (m *mockPreviewScraper) FetchDocumentPreview(ctx context.Context, targetURL string, maxBytes int) ([]byte, string, error) {
	return m.previewBytes, "application/pdf", nil
}

type sequenceTriageMockLLM struct {
	decisions []string
	call      int
}

func (m *sequenceTriageMockLLM) GenerateStructured(_ context.Context, _ string, _ string, target any) error {
	value := target.(*documentTriageDecision)
	idx := m.call
	if idx >= len(m.decisions) {
		idx = len(m.decisions) - 1
	}
	m.call++
	value.Decision = m.decisions[idx]
	value.Confidence = 0.95
	value.EntityMatch = true
	value.TopicMatch = true
	value.DateMatch = true
	value.Reason = "sequence decision"
	return nil
}

func TestTriageDocumentCandidatesPromotesOnPreview(t *testing.T) {
	llm := &sequenceTriageMockLLM{decisions: []string{"uncertain", "relevant"}}
	scraper := &mockPreviewScraper{
		previewBytes: testutil.MinimalPDF("Acme Corporation Official Annual Report 2025"),
	}
	engine := NewEngine(llm, &mockSearcher{}, scraper, cache.NewMemoryCache(10), 2, 3, 10)
	agent := NewCompanyAgent(engine)
	brief := models.CompanyBrief{CompanyName: "Acme"}
	candidates := []models.RankedResult{{
		URL:        "https://example.test/vague-filing.pdf",
		SourceType: "report",
	}}
	results := []models.SearchResult{{
		Link:    candidates[0].URL,
		Title:   "Filing Document",
		Snippet: "Vague document description",
	}}

	kept, report := agent.triageDocumentCandidates(context.Background(), brief, candidates, results)
	if len(kept) != 1 {
		t.Fatalf("expected candidate to be promoted after preview, got %d kept candidates", len(kept))
	}
	if len(report) != 0 {
		t.Fatalf("expected no skipped report entries for promoted candidate, got %d", len(report))
	}
}

func TestRemoveVacancyPlaceholdersKeepsNamedEmployees(t *testing.T) {
	chart := &models.OrgChart{
		Leadership: []models.Employee{
			{Name: "Manager, Logistics and Purchasing", Title: "Manager, Logistics and Purchasing"},
			{Name: "Leadership Team", Title: "Highly experienced leadership team with extensive background"},
			{Name: "Jane Doe", Title: "Director of Logistics"},
		},
		Departments: []models.Department{{
			Name: "Purchasing",
			Head: "Open Position",
			Members: []models.Employee{
				{Name: "Procurement Manager", Title: "Procurement Manager"},
				{Name: "John Smith", Title: "Head of Purchasing"},
			},
		}},
	}

	removeVacancyPlaceholders(chart)

	if len(chart.Leadership) != 1 || chart.Leadership[0].Name != "Jane Doe" {
		t.Fatalf("expected only the named leader to remain, got %+v", chart.Leadership)
	}
	if chart.Departments[0].Head != "" || len(chart.Departments[0].Members) != 1 ||
		chart.Departments[0].Members[0].Name != "John Smith" {
		t.Fatalf("expected vacancy department entries removed, got %+v", chart.Departments[0])
	}
}

func TestSelectLatestDocumentFamilies(t *testing.T) {
	candidates := []models.RankedResult{
		{URL: "https://example.test/annual-report-2024-25.pdf", SourceType: "report"},
		{URL: "https://example.test/annual-report-2025-26.pdf", SourceType: "report"},
		{URL: "https://example.test/sustainability-report-2025-26.pdf", SourceType: "report"},
	}
	results := map[string]models.SearchResult{
		normalizeURL(candidates[0].URL): {Link: candidates[0].URL, Title: "Acme Annual Report 2024-25"},
		normalizeURL(candidates[1].URL): {Link: candidates[1].URL, Title: "Acme Annual Report 2025-26"},
		normalizeURL(candidates[2].URL): {Link: candidates[2].URL, Title: "Acme Sustainability Report 2025-26"},
	}

	kept, report := selectLatestDocumentFamilies(candidates, results, "")
	if len(kept) != 2 || kept[0].URL != candidates[1].URL {
		t.Fatalf("expected latest annual report and distinct sustainability report, got %+v", kept)
	}
	if len(report) != 1 || report[0].URL != candidates[0].URL {
		t.Fatalf("expected older annual report to be reported as skipped, got %+v", report)
	}
}

func TestDedupeChartEmployeesPrefersLeadership(t *testing.T) {
	chart := &models.OrgChart{
		Leadership: []models.Employee{
			{Name: "Jane Doe", Title: "Chief Executive Officer", Bio: "Executive biography"},
			{Name: "Jane Doe", Title: "Director"},
		},
		Departments: []models.Department{
			{Name: "Operations", Members: []models.Employee{
				{Name: "Jane Doe", Title: "Operations Director"},
				{Name: "John Smith", Title: "Head of Operations"},
			}},
			{Name: "Procurement", Members: []models.Employee{
				{Name: "John Smith", Title: "Procurement Lead"},
			}},
		},
	}

	dedupeChartEmployees(chart)

	if len(chart.Leadership) != 1 || chart.Leadership[0].Name != "Jane Doe" ||
		chart.Leadership[0].Title != "Chief Executive Officer" {
		t.Fatalf("expected one canonical leadership row, got %+v", chart.Leadership)
	}
	if len(chart.Departments[0].Members) != 1 || chart.Departments[0].Members[0].Name != "John Smith" {
		t.Fatalf("expected Jane duplicate removed from department, got %+v", chart.Departments[0].Members)
	}
	if len(chart.Departments[1].Members) != 0 {
		t.Fatalf("expected John duplicate removed from second department, got %+v", chart.Departments[1].Members)
	}
}
