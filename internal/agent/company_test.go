package agent

import (
	"context"
	"reflect"
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

	if len(plan.Queries) != 4 {
		t.Fatalf("expected four planned queries, got %d", len(plan.Queries))
	}
	if plan.Queries[0].Query != "Acme Germany Berlin leadership executive team" {
		t.Errorf("unexpected first query: %q", plan.Queries[0].Query)
	}
	if plan.Queries[3].TargetArea != "subsidiaries" {
		t.Errorf("expected interest query target area, got %q", plan.Queries[3].TargetArea)
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
