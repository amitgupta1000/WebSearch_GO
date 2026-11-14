package agent

// demand_chain_eval_test.go is the evaluation harness for the buyer pipeline.
// It is opt-in because it spends real search/API budget and needs credentials:
//
//	DEMAND_EVAL=1 go test ./internal/agent/ -run TestDemandChainEval -v -timeout 90m
//
// For each eval product it runs the full demand-chain workflow (the sharpened
// two-step buyer scope) and scores the run deterministically against a
// ground-truth expectation table: which direct-consuming stages the value
// chain inference must capture, which stage names would indicate step-3+
// scope leakage, and which non-buyer (bait/vendor) names must not reach the
// verified results. A per-product Markdown scorecard and a combined summary
// are written to DEMAND_EVAL_DIR (default: the repo's eval/ directory), so
// the harness converts "the scope should be sharper" into measurable claims.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/config"
	"github.com/amitgupta1000/WebSearch_GO/internal/llm"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// demandEvalExpectation is the ground truth for one product. Stage fragments
// are matched case-insensitively against inferred stage names; expectations
// are deliberately structural (stage-level), not company-name-level, so the
// harness measures the pipeline rather than one run's discovery luck.
type demandEvalExpectation struct {
	activity   string
	researchID string
	// countries scopes the run; empty means global. Country lanes (importer/
	// tender) only exist under a geography, so tender-driven buyers such as
	// GNFC can only surface in a scoped run.
	countries []string
	// directStages are consuming processes that must be captured at distance 1
	// for the chain inference to count as correct for this product.
	directStages []string
	// buyerMarkers are company-name fragments a strong run is expected to
	// surface (verified or at least as a stage candidate); scored
	// informationally, not as a hard failure.
	buyerMarkers []string
	// scopeLeakStages are processes that consume the product only at step 3+
	// (or not at all); their presence anywhere in the inferred chain is a
	// scope leak under the raw-material buyer definition.
	scopeLeakStages []string
	// nonBuyerMarkers are bait/vendor fragments that must never reach the
	// verified results.
	nonBuyerMarkers []string
}

var demandEvalProducts = []demandEvalExpectation{
	{
		activity:   "Methanol",
		researchID: "eval-methanol",
		directStages: []string{
			"formaldehyde", "acetic acid", "mtbe", "dimethyl ether",
			"olefin", "methylamine", "methyl chloride", "biodiesel",
		},
		scopeLeakStages: []string{
			"paint", "coating", "plywood", "pharmaceutical", "construction",
			"automotive", "cosmetic",
		},
		nonBuyerMarkers: []string{"market research", "buyers list", "sample report", "research and markets", "acs publications", "august wilhelm", "biodiesel producers", "tdi products", "technology by covestro"},
	},
	{
		activity:   "Toluene Diisocyanate",
		researchID: "eval-tdi",
		directStages: []string{
			"foam", "polyurethane", "elastomer", "coating", "adhesive", "sealant",
		},
		// Furniture, mattresses and car seats consume foam, not TDI: under the
		// raw-material reading they are step-3+ conversions of a step-1 output,
		// so their presence in the chain is a scope leak the definition must
		// justify or the harness will flag it.
		scopeLeakStages: []string{
			"furniture", "mattress", "bedding", "upholstery", "automotive", "car seat",
		},
		nonBuyerMarkers: []string{"market research", "buyers list", "sample report", "research and markets", "acs publications", "august wilhelm", "biodiesel producers", "tdi products", "technology by covestro"},
	},
	{
		// India-scoped methanol run, prompted by a live tender: "Supply of
		// Methanol to GNFC, Bharuch Plant" (Gujarat Narmada Valley Fertilizers
		// and Chemicals). GNFC is an integrated fertilizer-and-chemicals
		// company whose formaldehyde plant consumes methanol as feedstock —
		// a step-1 buyer under the raw-material definition, and a probe of
		// whether the country tender lanes surface real tender-driven demand.
		activity:        "Methanol",
		researchID:      "eval-methanol-india",
		countries:       []string{"India"},
		directStages:    []string{"formaldehyde", "acetic acid", "mtbe", "dimethyl ether", "olefin", "methylamine", "biodiesel"},
		buyerMarkers:    []string{"gnfc", "narmada"},
		nonBuyerMarkers: []string{"market research", "buyers list", "sample report", "research and markets", "acs publications", "august wilhelm", "biodiesel producers", "tdi products", "technology by covestro"},
	},
}

// demandEvalStats carries the measurable outcome of one product run.
type demandEvalStats struct {
	capturedDirect map[string]int // expected fragment -> distance the model labelled it
	missedDirect   []string
	buyerHits      map[string][]string // expected-buyer fragment -> where it surfaced
	scopeLeaks     []string
	baitLeaks      []string
	strongBuyers   []string // verified results tied to an expected direct stage
	derivedBuyers  []string // verified results tied to a distance-2 stage
	otherBuyers    []string // verified results with no stage or an unexpected one
	intermediaries []string // verified results that look like traders/brokers
	droppedStages  []string // parsed from the run's audit warning
	queriesUsed    int
	searchHits     int
	verifiedCount  int
	leadsRejected  int
	pagesEnriched  int
	verificationQ  int
}

// demandEvalRun executes one product through the live pipeline and scores it.
func demandEvalRun(t *testing.T, ctx context.Context, eng *Engine, exp demandEvalExpectation) (*models.DemandChainReport, *demandEvalStats) {
	t.Helper()
	eventCh := make(chan models.AgentEvent, 256)
	go func() {
		for range eventCh {
		}
	}()
	agent := NewDemandChainAgent(eng)
	report, err := agent.Research(ctx, exp.researchID, models.DemandChainQuery{
		Activity:            exp.activity,
		Objective:           fmt.Sprintf("Identify direct and one-step-derived buyers of %s", exp.activity),
		Countries:           exp.countries,
		MaxCompanies:        12,
		DiscoveryQueries:    36,
		VerificationQueries: 120,
		MaxLeadsEnriched:    40,
		// People discovery is out of scope for this eval: it measures buyer
		// identification, not contacts, so the budget stays on verification.
	}, eventCh)
	close(eventCh)
	if err != nil {
		t.Fatalf("%s Research: %v", exp.activity, err)
	}
	return report, demandEvalScore(exp, report)
}

// demandEvalScore scores a finished report against the expectation table.
func demandEvalScore(exp demandEvalExpectation, report *models.DemandChainReport) *demandEvalStats {
	stats := &demandEvalStats{capturedDirect: map[string]int{}, buyerHits: map[string][]string{}}
	// Chain scoring: capture and distance-labelling of the expected direct
	// stages, plus any scope leaks anywhere in the inferred chain.
	for _, fragment := range exp.directStages {
		caught := false
		for _, stage := range report.Chain {
			if strings.Contains(strings.ToLower(stage.Name), fragment) {
				stats.capturedDirect[fragment] = stage.Distance
				caught = true
				break
			}
		}
		if !caught {
			stats.missedDirect = append(stats.missedDirect, fragment)
		}
	}
	for _, fragment := range exp.scopeLeakStages {
		for _, stage := range report.Chain {
			if strings.Contains(strings.ToLower(stage.Name), fragment) {
				stats.scopeLeaks = append(stats.scopeLeaks, fmt.Sprintf("%s (labelled step %d)", stage.Name, stage.Distance))
				break
			}
		}
	}
	// Result scoring: buyer-fit tiers, bait leakage, intermediary leakage.
	stageDistance := stageDistanceMap(report)
	for _, result := range report.Results {
		stats.verifiedCount++
		name := strings.ToLower(result.Name)
		bait := false
		for _, marker := range exp.nonBuyerMarkers {
			if strings.Contains(name, marker) {
				stats.baitLeaks = append(stats.baitLeaks, result.Name)
				bait = true
			}
		}
		if !bait && isIntermediarySignal(result.Name, result.Domain) {
			stats.intermediaries = append(stats.intermediaries, result.Name)
		}
		stage := strings.TrimSpace(result.Stage)
		expectedDirect := false
		for _, fragment := range exp.directStages {
			if strings.Contains(strings.ToLower(stage), fragment) {
				expectedDirect = true
				break
			}
		}
		entry := fmt.Sprintf("%s (%s)", result.Name, nonEmptyStageLabel(stage, stageDistance))
		switch {
		case expectedDirect:
			stats.strongBuyers = append(stats.strongBuyers, entry)
		case stage != "" && stageDistance[stage] > models.StageDistanceDirect:
			stats.derivedBuyers = append(stats.derivedBuyers, entry)
		default:
			stats.otherBuyers = append(stats.otherBuyers, entry)
		}
	}
	// Expected-buyer fragments: verified results and the stage-candidate
	// roster both count, because a tender-driven buyer may surface as a
	// lead before its evidence reaches the verification threshold.
	for _, fragment := range exp.buyerMarkers {
		for _, result := range report.Results {
			if strings.Contains(strings.ToLower(result.Name), fragment) {
				stats.buyerHits[fragment] = append(stats.buyerHits[fragment], fmt.Sprintf("verified: %s (%.2f)", result.Name, result.Confidence))
			}
		}
		for _, section := range report.StageCandidates {
			for _, company := range section.Companies {
				if strings.Contains(strings.ToLower(company.Name), fragment) {
					stats.buyerHits[fragment] = append(stats.buyerHits[fragment], fmt.Sprintf("stage candidate [%s]: %s (%s)", section.Stage.Name, company.Name, company.Status))
				}
			}
		}
	}
	// The dropped-stage audit trail, parsed from the run's warnings.
	for _, warning := range report.Warnings {
		if !strings.Contains(warning, "dropped as out of buyer scope") {
			continue
		}
		if colon := strings.Index(warning, ": "); colon >= 0 {
			for _, name := range strings.Split(strings.TrimSuffix(warning[colon+2:], "."), ", ") {
				if trimmed := strings.TrimSpace(name); trimmed != "" {
					stats.droppedStages = append(stats.droppedStages, trimmed)
				}
			}
		}
	}
	stats.queriesUsed = report.QueriesUsed
	stats.searchHits = report.SearchHits
	stats.leadsRejected = report.LeadsRejected
	stats.pagesEnriched = report.PagesEnriched
	stats.verificationQ = report.VerificationQueries
	return stats
}

func nonEmptyStageLabel(stage string, distances map[string]int) string {
	if stage == "" {
		return "no stage"
	}
	if distance, ok := distances[stage]; ok && distance > models.StageDistanceDirect {
		return fmt.Sprintf("step %d", distance)
	}
	return stage
}

func stageDistanceMap(report *models.DemandChainReport) map[string]int {
	out := make(map[string]int, len(report.Chain))
	for _, stage := range report.Chain {
		out[stage.Name] = stage.Distance
	}
	return out
}

func orNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

// renderDemandEvalMarkdown renders one product's scorecard.
func renderDemandEvalMarkdown(exp demandEvalExpectation, report *models.DemandChainReport, stats *demandEvalStats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Demand eval — %s\n\n", exp.activity)
	fmt.Fprintf(&b, "_Run %s · %d discovery queries (%d hits) · %d verification queries · %d pages enriched · %d leads rejected · completed %s_\n\n",
		exp.researchID, stats.queriesUsed, stats.searchHits, stats.verificationQ, stats.pagesEnriched, stats.leadsRejected,
		report.CompletedAt.Format("2006-01-02 15:04"))
	b.WriteString("## Inferred chain\n\n")
	b.WriteString("| Stage | Steps from the product | Rationale |\n| :--- | :--- | :--- |\n")
	for _, stage := range report.Chain {
		fmt.Fprintf(&b, "| %s | %d | %s |\n", stage.Name, stage.Distance, stage.Rationale)
	}
	b.WriteString("\n## Scorecard\n\n")
	fmt.Fprintf(&b, "- **Expected direct stages captured: %d/%d**\n", len(stats.capturedDirect), len(exp.directStages))
	fragments := make([]string, 0, len(stats.capturedDirect))
	for fragment := range stats.capturedDirect {
		fragments = append(fragments, fragment)
	}
	sort.Strings(fragments)
	for _, fragment := range fragments {
		fmt.Fprintf(&b, "  - %s — labelled step %d\n", fragment, stats.capturedDirect[fragment])
	}
	if len(stats.missedDirect) > 0 {
		fmt.Fprintf(&b, "  - missed: %s\n", strings.Join(stats.missedDirect, ", "))
	}
	fmt.Fprintf(&b, "- **Scope leaks in the chain: %d** — %s\n", len(stats.scopeLeaks), orNone(stats.scopeLeaks))
	fmt.Fprintf(&b, "- **Proposals dropped by the two-step gate: %d** — %s\n", len(stats.droppedStages), orNone(stats.droppedStages))
	fmt.Fprintf(&b, "- **Bait leakage in verified results: %d** — %s\n", len(stats.baitLeaks), orNone(stats.baitLeaks))
	fmt.Fprintf(&b, "- **Intermediary leakage in verified results: %d** — %s\n", len(stats.intermediaries), orNone(stats.intermediaries))
	if len(stats.buyerHits) > 0 {
		fmt.Fprintf(&b, "- **Expected-buyer fragments surfaced: %d/%d**\n", len(stats.buyerHits), len(exp.buyerMarkers))
		fragments := make([]string, 0, len(stats.buyerHits))
		for fragment := range stats.buyerHits {
			fragments = append(fragments, fragment)
		}
		sort.Strings(fragments)
		for _, fragment := range fragments {
			fmt.Fprintf(&b, "  - %q — %s\n", fragment, strings.Join(stats.buyerHits[fragment], "; "))
		}
		missedBuyers := orNone(nil)
		var missing []string
		for _, fragment := range exp.buyerMarkers {
			if _, ok := stats.buyerHits[fragment]; !ok {
				missing = append(missing, fragment)
			}
		}
		if len(missing) > 0 {
			missedBuyers = strings.Join(missing, ", ")
		}
		fmt.Fprintf(&b, "  - missed: %s\n", missedBuyers)
	}
	fmt.Fprintf(&b, "- **Verified buyers: %d** — %d tied to expected direct stage(s), %d to a derived stage, %d unclassified\n\n",
		stats.verifiedCount, len(stats.strongBuyers), len(stats.derivedBuyers), len(stats.otherBuyers))
	b.WriteString("## Verified buyers\n\n")
	if len(report.Results) == 0 {
		b.WriteString("_None._\n\n")
	}
	stageDistance := stageDistanceMap(report)
	for _, result := range report.Results {
		fmt.Fprintf(&b, "- **%s** — stage: %s · country: %s · status: %s · confidence: %.2f\n",
			result.Name, nonEmptyStageLabel(strings.TrimSpace(result.Stage), stageDistance), result.Country, result.Status, result.Confidence)
	}
	b.WriteString("\n## Warnings\n\n")
	if len(report.Warnings) == 0 {
		b.WriteString("_None._\n")
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(&b, "- %s\n", warning)
	}
	return b.String()
}

// TestDemandChainEval runs the harness. Opt-in; see the file header.
func TestDemandChainEval(t *testing.T) {
	if os.Getenv("DEMAND_EVAL") != "1" {
		t.Skip("set DEMAND_EVAL=1 (with SERPER_API_KEY and Gemini credentials) to run the buyer-pipeline evaluation harness")
	}
	outDir := strings.TrimSpace(os.Getenv("DEMAND_EVAL_DIR"))
	if outDir == "" {
		outDir = "../../eval"
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("create the eval output dir: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Minute)
	defer cancel()

	// Wired exactly like the server and the live test: credentials resolve
	// through config, including the Windows registry-aware fallback.
	cfg := config.Load()
	gemini, err := llm.NewGeminiClient(ctx, cfg.GeminiModel)
	if err != nil {
		t.Fatalf("gemini client: %v", err)
	}
	serper := tools.NewSerperClient(cfg.SerperAPIKey)
	scraper := tools.NewScraperWithBrowser(nil, 15*time.Second, false)
	defer scraper.Close()
	eng := NewEngine(gemini, serper, scraper, cache.NewMemoryCache(200), 40, 10, 20)

	summary := &strings.Builder{}
	fmt.Fprintf(summary, "# Demand eval summary — two-step buyer scope\n\n_Generated %s_\n\n", time.Now().Format("2006-01-02 15:04"))
	products := demandEvalProducts
	if only := strings.TrimSpace(os.Getenv("DEMAND_EVAL_ONLY")); only != "" {
		products = nil
		for _, id := range strings.Split(only, ",") {
			id = strings.TrimSpace(id)
			for _, exp := range demandEvalProducts {
				if exp.researchID == id {
					products = append(products, exp)
				}
			}
		}
		if products == nil {
			t.Fatalf("DEMAND_EVAL_ONLY=%q matched no eval product", only)
		}
	}
	for _, exp := range products {
		report, stats := demandEvalRun(t, ctx, eng, exp)
		buyerLine := ""
		if len(exp.buyerMarkers) > 0 {
			buyerLine = fmt.Sprintf("- Expected-buyer fragments surfaced: **%d/%d**\n", len(stats.buyerHits), len(exp.buyerMarkers))
		}
		summary.WriteString(fmt.Sprintf("## %s (%s)\n\n- Expected direct stages captured: **%d/%d** (missed: %s)\n- Scope leaks: **%d**; dropped by the gate: **%d**\n- Verified buyers: **%d** (%d direct-tied, %d derived, %d unclassified); bait leakage: **%d**; intermediary leakage: **%d**\n%s\n",
			exp.activity, strings.Join(exp.countries, ", "), len(stats.capturedDirect), len(exp.directStages), orNone(stats.missedDirect),
			len(stats.scopeLeaks), len(stats.droppedStages),
			stats.verifiedCount, len(stats.strongBuyers), len(stats.derivedBuyers), len(stats.otherBuyers),
			len(stats.baitLeaks), len(stats.intermediaries), buyerLine))
		path := filepath.Join(outDir, exp.researchID+"-scorecard.md")
		if err := os.WriteFile(path, []byte(renderDemandEvalMarkdown(exp, report, stats)), 0o644); err != nil {
			t.Fatalf("write the scorecard: %v", err)
		}
		t.Logf("scorecard -> %s", path)
	}
	summaryPath := filepath.Join(outDir, "demand-eval-summary.md")
	if err := os.WriteFile(summaryPath, []byte(summary.String()), 0o644); err != nil {
		t.Fatalf("write the eval summary: %v", err)
	}
	t.Logf("summary -> %s", summaryPath)
}
