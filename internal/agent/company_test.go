package agent

import (
	"context"
	"testing"

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
