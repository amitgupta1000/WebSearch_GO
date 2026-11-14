package agent

// market_landscape_eval_test.go is the opt-in live evaluation harness for the producer/seller
// pipeline. It spends real search/API budget and needs credentials:
//
//	MARKET_EVAL=1 go test ./internal/agent/ -run TestMarketLandscapeEval -v -timeout 90m
//
// It mirrors the buyer eval: run the live pipeline, score coverage/category/fallback behaviour, and
// write per-product Markdown scorecards plus a summary to eval/. The harness is deliberately
// informational rather than a hard quality assertion because live discovery varies by day.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/config"
	"github.com/amitgupta1000/WebSearch_GO/internal/llm"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

type marketEvalCase struct {
	activity  string
	countries []string
	slug      string
}

var marketEvalCases = []marketEvalCase{
	{activity: "lubricant blending", countries: []string{"India"}, slug: "lubricant-blending"},
	{activity: "linear alkyl benzene", slug: "linear-alkyl-benzene"},
}

func TestMarketLandscapeEval(t *testing.T) {
	if os.Getenv("MARKET_EVAL") != "1" {
		t.Skip("set MARKET_EVAL=1 (with SERPER_API_KEY and Gemini credentials) to run the producer/seller evaluation harness")
	}
	outDir := strings.TrimSpace(os.Getenv("MARKET_EVAL_DIR"))
	if outDir == "" {
		outDir = "../../eval"
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("create eval dir: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	cfg := config.Load()
	gemini, err := llm.NewGeminiClient(ctx, cfg.GeminiModel)
	if err != nil {
		t.Fatalf("gemini client: %v", err)
	}
	serper := tools.NewSerperClient(cfg.SerperAPIKey)
	scraper := tools.NewScraperWithBrowser(nil, 15*time.Second, false)
	defer scraper.Close()
	eng := NewEngine(gemini, serper, scraper, cache.NewMemoryCache(200), 40, 10, 20)

	cases := marketEvalCases
	if only := strings.TrimSpace(os.Getenv("MARKET_EVAL_ONLY")); only != "" {
		cases = nil
		for _, exp := range marketEvalCases {
			if exp.slug == only {
				cases = append(cases, exp)
			}
		}
		if len(cases) == 0 {
			t.Fatalf("MARKET_EVAL_ONLY=%q matched no market eval case", only)
		}
	}
	summary := &strings.Builder{}
	fmt.Fprintf(summary, "# Producer/Seller eval summary\n\n_Generated %s_\n\n", time.Now().Format("2006-01-02 15:04"))
	for _, exp := range cases {
		eventCh := make(chan models.AgentEvent, 256)
		go func() {
			for event := range eventCh {
				if strings.Contains(event.Content, "extraction") || strings.Contains(event.Content, "Market category") || strings.Contains(event.Content, "QC review") {
					t.Logf("[event] %s: %s", event.Type, event.Content)
				}
			}
		}()
		report, err := NewMarketLandscapeAgent(eng).Research(ctx, "market-eval-"+exp.slug, models.MarketLandscapeQuery{
			Activity: exp.activity, Countries: exp.countries, MaxCompanies: 48,
		}, eventCh)
		close(eventCh)
		if err != nil {
			t.Fatalf("%s Research: %v", exp.activity, err)
		}
		scorecard := marketEvalScorecard(exp, report)
		path := filepath.Join(outDir, "eval-market-"+exp.slug+".md")
		if err := os.WriteFile(path, []byte(scorecard), 0o644); err != nil {
			t.Fatalf("write scorecard: %v", err)
		}
		t.Logf("scorecard -> %s", path)
		fmt.Fprintf(summary, "## %s\n\n%s\n\n", exp.activity, scorecard)
	}
	summaryPath := filepath.Join(outDir, "market-eval-summary.md")
	if err := os.WriteFile(summaryPath, []byte(summary.String()), 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	t.Logf("summary -> %s", summaryPath)
}

func marketEvalScorecard(exp marketEvalCase, report *models.MarketLandscapeReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Producer/Seller eval — %s\n\n", exp.activity)
	fmt.Fprintf(&b, "_%d queries · %d hits · %d leads · %d extracted · %d resolved · %d pages enriched · %d fallback queries_\n\n",
		report.QueriesUsed, report.SearchHits, report.LeadsReviewed, report.LeadsExtracted, report.LeadsResolved, report.PagesEnriched, report.FallbackQueries)
	if report.ProductCategory != "" {
		fmt.Fprintf(&b, "- **Category**: %s (%s)\n", report.ProductCategory, report.CategorySource)
	}
	if report.Coverage != nil {
		fmt.Fprintf(&b, "- **Coverage**: %d/%d (%s)\n", report.Coverage.Companies, report.Coverage.Minimum, map[bool]string{true: "PASS", false: "FAIL"}[report.Coverage.Pass])
	}
	fmt.Fprintf(&b, "- **Verified companies**: %d\n- **Candidates**: %d\n\n## Results\n\n", len(report.Results), report.CandidatesFound)
	if len(report.Results) == 0 {
		b.WriteString("No verified companies were promoted.\n")
	} else {
		for _, result := range report.Results {
			fmt.Fprintf(&b, "- **%s** — %s · %s · %.2f\n", result.Name, result.Country, result.Status, result.Confidence)
		}
	}
	if len(report.Reviews) > 0 || len(report.Warnings) > 0 {
		b.WriteString("\n## Review and warnings\n\n")
		for _, review := range report.Reviews {
			fmt.Fprintf(&b, "- QC: **%s** (%.2f) — %s\n", review.Verdict, review.Score, review.Reason)
			for _, rejection := range review.Rejected {
				fmt.Fprintf(&b, "  - Rejected: **%s** — %s\n", rejection.Name, rejection.Reason)
			}
		}
		for _, warning := range report.Warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
	}
	return b.String()
}
