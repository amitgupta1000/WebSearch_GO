package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/config"
	"github.com/amitgupta1000/WebSearch_GO/internal/export"
	"github.com/amitgupta1000/WebSearch_GO/internal/llm"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// Live integration test for the buyer pipeline. Opt-in because it spends real
// search/API budget and needs credentials:
//
//	DEMAND_INTEGRATION=1 DEMAND_LIVE_DIR=%TEMP%\crystal-live go test ./internal/agent/ -run DemandChainLive -v -timeout 40m
//
// It runs the full demand-chain workflow for a sample product + geography and
// writes the branded artifacts to the given directory.
func TestDemandChainLive(t *testing.T) {
	if os.Getenv("DEMAND_INTEGRATION") != "1" {
		t.Skip("set DEMAND_INTEGRATION=1 (with SERPER_API_KEY and Gemini credentials) to run the live buyer pipeline")
	}
	outDir := strings.TrimSpace(os.Getenv("DEMAND_LIVE_DIR"))
	if outDir == "" {
		t.Skip("set DEMAND_LIVE_DIR to choose where the live artifacts are written")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Clients are wired exactly like the server wires them — through config —
	// so the credentials resolve the same way, including the Windows
	// registry-aware fallback for variables set after this terminal's parent
	// started.
	cfg := config.Load()
	gemini, err := llm.NewGeminiClient(ctx, cfg.GeminiModel)
	if err != nil {
		t.Fatalf("gemini client: %v", err)
	}
	serper := tools.NewSerperClient(cfg.SerperAPIKey)
	rocketReach := tools.NewRocketReachClient(cfg.RocketReachAPIKey)
	scraper := tools.NewScraperWithBrowser(nil, 15*time.Second, false)
	defer scraper.Close()

	exporter := export.New(export.NewFilesystemDestination(outDir), scraper, "live/", nil)
	eng := NewEngine(gemini, serper, scraper, cache.NewMemoryCache(200), 40, 10, 20, WithExporter(exporter))

	// The event channel is drained exactly like the SSE console does, so the
	// RocketReach discovery calls are observable in the test output.
	eventCh := make(chan models.AgentEvent, 256)
	var events []models.AgentEvent
	eventsDone := make(chan struct{})
	go func() {
		for event := range eventCh {
			events = append(events, event)
		}
		close(eventsDone)
	}()

	agent := NewDemandChainAgent(eng, rocketReach)
	report, err := agent.Research(ctx, "live-nbutanol-india", models.DemandChainQuery{
		Activity:     "N-Butanol",
		Objective:    "Find potential buyers of N-Butanol in India",
		Countries:    []string{"India"},
		MaxCompanies: 15,
		// A deliberately deep budget for the sample run: the levers are
		// query-driven, so the shipped mode defaults stay untouched.
		DiscoveryQueries:        60,
		VerificationQueries:     250,
		MaxLeadsEnriched:        60,
		IncludePeople:           true,
		UseRocketReachDiscovery: true,
	}, eventCh)
	close(eventCh)
	<-eventsDone
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	for _, event := range events {
		if strings.Contains(event.Content, "RocketReach") || strings.Contains(event.Content, "procurement") {
			t.Logf("[event] %s: %s", event.Type, event.Content)
		}
	}

	t.Logf("geography: %q; chain inferred: %v", report.Geography, report.ChainInferred)
	for _, stage := range report.Chain {
		t.Logf("stage [%s] %s — %s (operators: %s)", stage.Source, stage.Name, stage.Rationale, strings.Join(stage.Operators, "; "))
	}
	t.Logf("lanes: %d queries, %d hits, %d verification queries, %d pages enriched", report.QueriesUsed, report.SearchHits, report.VerificationQueries, report.PagesEnriched)
	for index, result := range report.Results {
		claims := make([]string, 0, len(result.Evidence))
		for _, evidence := range result.Evidence {
			claims = append(claims, evidence.ClaimType+"("+evidence.SourceTier+")")
		}
		t.Logf("result %d: %s | stage=%s | country=%s | status=%s | confidence=%.2f | claims=%s",
			index+1, result.Name, result.Stage, result.Country, result.Status, result.Confidence, strings.Join(claims, ","))
	}
	for _, warning := range report.Warnings {
		t.Logf("warning: %s", warning)
	}
	for _, artifact := range report.Exports {
		if artifact.Failed() {
			t.Errorf("%s export failed: %s", artifact.Format, artifact.Error)
			continue
		}
		t.Logf("artifact %s -> %s", artifact.Format, artifact.URI)
	}
	if len(report.Results) == 0 {
		t.Errorf("the live run verified no buyers; check the warnings and the appendix")
	}
}
