package agent

// query_plan_verification_test.go pins the semantic shape of the search plans
// both landscape modes issue for representative operator queries. The checks
// are the ones the operator's "Oil Blending" review demanded: no concatenated
// region soup, no self-repeating query, no role-word stacks no page contains,
// natural indexed phrases for process activities, and one query per geography.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/config"
	"github.com/amitgupta1000/WebSearch_GO/internal/llm"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// assertPlanSound runs the plan-level semantic invariants over one mode's
// queries: the same defects the adversarial review exists to catch, asserted
// at composition time so a regression cannot ship quietly.
func assertPlanSound(t *testing.T, label string, queries []string) {
	t.Helper()
	for _, query := range queries {
		lower := strings.ToLower(query)
		if strings.Contains(lower, "middle east africa") || strings.Contains(lower, "africa middle east") {
			t.Errorf("%s: %q concatenates two regions into one ambiguous tail", label, query)
		}
		for _, stack := range []string{"company operator manufacturer", "official company middle", "blending plant facility", "industrial oil blending"} {
			if strings.Contains(lower, stack) {
				t.Errorf("%s: %q leans on vocabulary no page contains (%q)", label, query, stack)
			}
		}
		counts := make(map[string]int)
		for _, word := range marketQueryWords(query) {
			if marketQuerySiteDomain(word) != "" {
				continue
			}
			counts[word]++
		}
		for word, count := range counts {
			if count > 1 && !marketQueryConnectorWord(word) {
				t.Errorf("%s: %q repeats its own word %q", label, query, word)
			}
		}
		if words := len(marketQueryWords(query)); words > marketQueryReviewMaxWords {
			t.Errorf("%s: %q carries %d words, cap is %d", label, query, words, marketQueryReviewMaxWords)
		}
	}
}

func TestSellerPlanSamplesStaySound(t *testing.T) {
	samples := []struct {
		label    string
		query    models.MarketLandscapeQuery
		expected []string // substrings that must appear somewhere in the plan
	}{
		{
			label: "oil blending MEA",
			query: models.MarketLandscapeQuery{Activity: "Oil Blending", Regions: []string{"Middle East", "Africa"}},
			// The process activity is searched under the natural indexed
			// phrase too, every phrase carries exactly one region, and the
			// category lanes travel per region. Oil blending's family is
			// lubricants, never oleochemicals: the two chemistries only
			// share the word "oil".
			expected: []string{
				`"lubricant blending" manufacturer Middle East`,
				`"lubricant blending" plant Africa`,
				`site:ensun.io "Oil Blending"`,
				`"Oil Blending" exporter Africa`,
				`lubricants manufacturers Middle East`,
				`lubricants companies Africa`,
			},
		},
		{
			label: "lubricant blending India",
			query: models.MarketLandscapeQuery{Activity: "lubricant blending", Countries: []string{"India"}},
			expected: []string{
				`"lubricant blending" manufacturer India`,
				`"lubricant blending" plant India`,
			},
		},
		{
			label: "linear alkyl benzene global",
			query: models.MarketLandscapeQuery{Activity: "linear alkyl benzene"},
			expected: []string{
				`"linear alkyl benzene" manufacturer`,
				`"linear alkyl benzene" shipment`,
			},
		},
		{
			label: "fuel oil blending Middle East",
			query: models.MarketLandscapeQuery{Activity: "fuel oil blending", Regions: []string{"Middle East"}},
			// The activity names fuel, so the fuel variant is its own phrase
			// and neither a generic duplicate nor the lubricant wording is
			// composed; the category lane names the fuel-oil family.
			expected: []string{
				`"fuel oil blending" manufacturer Middle East`,
				`fuel oils manufacturers Middle East`,
			},
		},
		{
			label: "crude oil blending Middle East",
			query: models.MarketLandscapeQuery{Activity: "crude oil blending", Regions: []string{"Middle East"}},
			// Crude blending is neither lubricant nor fuel blending.
			expected: []string{
				`"crude oil blending" manufacturer Middle East`,
				`crude oil manufacturers Middle East`,
			},
		},
	}
	for _, sample := range samples {
		queries := sellerPlanQueries(sample.query)
		assertPlanSound(t, sample.label, queries)
		for _, want := range sample.expected {
			found := false
			for _, query := range queries {
				if strings.Contains(strings.ToLower(query), strings.ToLower(want)) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: plan is missing %q; plan: %v", sample.label, want, queries)
			}
		}
		t.Logf("seller plan %s (%d queries):\n  %s", sample.label, len(queries), strings.Join(queries, "\n  "))
	}
}

// TestSellerPlanNeverCrossesOilFamilies pins the operator's report: a lubricant,
// fuel or crude oil blending run must never buy oleochemical search (palm fatty
// acid / glycerin / soap producers), and a fuel or crude run must not be biased
// toward lubricant operators either. The families share the word "oil", not the
// chemistry.
func TestSellerPlanNeverCrossesOilFamilies(t *testing.T) {
	cases := []struct {
		activity string
		forbid   []string
	}{
		{activity: "Oil Blending", forbid: []string{"oleochemical"}},
		{activity: "lubricant oil blending", forbid: []string{"oleochemical"}},
		{activity: "base oil blending", forbid: []string{"oleochemical", "fuel oil"}},
		{activity: "fuel oil blending", forbid: []string{"oleochemical", "lubricant blending"}},
		{activity: "crude oil blending", forbid: []string{"oleochemical", "lubricant blending"}},
		{activity: "used cooking oil", forbid: []string{"lubricant"}},
	}
	for _, testCase := range cases {
		plan := sellerPlanQueries(models.MarketLandscapeQuery{
			Activity: testCase.activity, Regions: []string{"Middle East"},
		})
		for _, query := range plan {
			for _, forbidden := range testCase.forbid {
				if strings.Contains(strings.ToLower(query), forbidden) {
					t.Errorf("%s plan contains %q, which belongs to another oil family: %q", testCase.activity, forbidden, query)
				}
			}
		}
	}
}


func sellerPlanQueries(query models.MarketLandscapeQuery) []string {
	var queries []string
	for _, search := range marketDiscoverySearches(query) {
		queries = append(queries, search.Query)
	}
	category := demandFallbackCategory(query.Activity, nil)
	for _, lane := range marketCategoryLanes(category) {
		for _, geography := range marketSearchGeographies(query) {
			composed := strings.TrimSpace(lane)
			if geography != "" {
				composed = strings.TrimSpace(lane + " " + geography)
			}
			queries = append(queries, composed)
		}
	}
	return queries
}

func TestBuyerPlanSamplesStaySound(t *testing.T) {
	samples := []struct {
		label    string
		query    models.MarketLandscapeQuery
		chain    []models.ValueChainStage
		expected []string
	}{
		{
			label: "used cooking oil Malaysia",
			query: models.MarketLandscapeQuery{Activity: "used cooking oil", Countries: []string{"Malaysia"}},
			chain: []models.ValueChainStage{{
				Name: "biodiesel production", Operators: []string{"biodiesel plant"}, Products: []string{"biodiesel"},
			}},
			expected: []string{
				"largest manufacturers of biodiesel Malaysia",
				"biodiesel plant Malaysia",
				"used cooking oil buyer procurement purchase Malaysia",
				"used cooking oil importer import data Malaysia",
				"Malaysia importer tender procurement",
			},
		},
		{
			label: "n-butanol India",
			query: models.MarketLandscapeQuery{Activity: "n-butanol", Countries: []string{"India"}},
			chain: []models.ValueChainStage{{
				Name: "plasticizer production", Operators: []string{"plasticizer plant"}, Products: []string{"dibutyl phthalate"},
			}},
			expected: []string{
				"largest manufacturers of plasticizer India",
				"plasticizer plant India",
				"India importer tender procurement",
			},
		},
		{
			label: "linear alkyl benzene MEA",
			query: models.MarketLandscapeQuery{Activity: "linear alkyl benzene", Regions: []string{"Middle East", "Africa"}},
			chain: []models.ValueChainStage{{
				Name: "surfactant production", Operators: []string{"surfactant plant"}, Products: []string{"detergent alkylate"},
			}},
			// One query per lane per region, and the region-scoped
			// importer/tender lanes never repeat their own region.
			expected: []string{
				"surfactant plant Middle East",
				"surfactant plant Africa",
				"linear alkyl benzene importer import data Middle East",
				"linear alkyl benzene importer import data Africa",
				"linear alkyl benzene Middle East importer tender procurement",
				"linear alkyl benzene Africa importer tender procurement",
			},
		},
	}
	for _, sample := range samples {
		queries := buyerPlanQueries(sample.query, sample.chain, 100)
		assertPlanSound(t, sample.label, queries)
		for _, want := range sample.expected {
			found := false
			for _, query := range queries {
				if strings.Contains(strings.ToLower(query), strings.ToLower(want)) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: plan is missing %q; plan: %v", sample.label, want, queries)
			}
		}
		t.Logf("buyer plan %s (%d queries):\n  %s", sample.label, len(queries), strings.Join(queries, "\n  "))
	}
}

// buyerPlanQueries composes the buyer's full discovery plan for one sample:
// the chain's lanes expanded across the requested geographies, budget-capped,
// and composed the way Research composes them.
func buyerPlanQueries(query models.MarketLandscapeQuery, chain []models.ValueChainStage, budget int) []string {
	dquery := models.DemandChainQuery{Activity: query.Activity, Countries: query.Countries, Regions: query.Regions}
	lanes := demandChainLanes(dquery, chain)
	runs := selectDemandLaneRuns(demandLaneRuns(lanes, demandSearchGeographies(dquery)), budget)
	queries := make([]string, 0, len(runs))
	for _, run := range runs {
		queries = append(queries, demandLaneSearchQuery(dquery.Activity, run.lane, run.geography))
	}
	return queries
}

// TestQueryProbeLive is the opt-in live verification for the adversarial query
// review: it composes the real plans for one seller and one buyer sample, runs
// the actual model review over them, then spends a bounded handful of real
// searches on the surviving queries to confirm the plans yield results.
//
//	QUERY_PROBE=1 go test ./internal/agent -run TestQueryProbeLive -v -timeout 20m
//
// It needs SERPER_API_KEY and Gemini credentials. Skipped otherwise.
func TestQueryProbeLive(t *testing.T) {
	if os.Getenv("QUERY_PROBE") != "1" {
		t.Skip("set QUERY_PROBE=1 (with SERPER_API_KEY and Gemini credentials) to probe live query quality")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
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

	probe := func(label, phase string, specs []marketSearchSpec, purposes []string, category string, scope models.MarketLandscapeQuery, budget int) {
		t.Logf("[%s] %s: %d planned quer(ies), %d purpose(s)", label, phase, len(specs), len(purposes))
		surviving, survivingPurposes, reviews, warning := reviewQueryBatch(ctx, eng, phase, specs, purposes, category, scope, nil)
		if warning != "" {
			t.Logf("[%s] review warning: %s", label, warning)
		}
		for _, row := range reviews {
			t.Logf("[%s] review %s: %q -> %q (%s)", label, row.Verdict, row.Original, row.RevisedQuery, row.Reason)
		}
		t.Logf("[%s] %d of %d quer(ies) survived the review", label, len(surviving), len(specs))
		shown := 0
		for _, spec := range surviving {
			if shown >= budget {
				break
			}
			shown++
			hits, err := eng.SearchCached(ctx, spec.Query, 10)
			if err != nil {
				t.Logf("[%s] search %q failed: %v", label, spec.Query, err)
				continue
			}
			titles := make([]string, 0, 2)
			for _, hit := range hits {
				if len(titles) == 2 {
					break
				}
				titles = append(titles, fmt.Sprintf("%.60s", hit.Title))
			}
			t.Logf("[%s] %2d hits | %s | e.g. %s", label, len(hits), spec.Query, strings.Join(titles, " / "))
		}
		if len(purposes) > 0 {
			t.Logf("[%s] surviving purposes: %v", label, survivingPurposes)
		}
	}

	// Seller sample — the operator's original review case.
	seller := models.MarketLandscapeQuery{Activity: "Oil Blending", Regions: []string{"Middle East", "Africa"}}
	agent := NewMarketLandscapeAgent(eng)
	category, _, _, _ := agent.inferMarketCategory(ctx, seller, nil)
	t.Logf("[seller] inferred category: %q", category)
	sellerSpecs := marketDiscoverySearches(seller)
	for _, lane := range marketCategoryLanes(category) {
		for _, geography := range marketSearchGeographies(seller) {
			composed := strings.TrimSpace(lane)
			if geography != "" {
				composed = strings.TrimSpace(lane + " " + geography)
			}
			sellerSpecs = append(sellerSpecs, marketSearchSpec{Lane: "category", Query: composed})
		}
	}
	probe("seller", "discovery", sellerSpecs, nil, category, seller, 10)
	probe("seller", "verification", nil, marketVerificationPurposes(), category, seller, 0)

	// Buyer sample — a representative chain for the trade's standard probe.
	buyer := models.DemandChainQuery{Activity: "used cooking oil", Regions: []string{"Middle East", "Africa"}}
	chain := []models.ValueChainStage{{
		Name: "biodiesel production", Operators: []string{"biodiesel plant", "biodiesel manufacturer"},
		Products: []string{"biodiesel"},
	}}
	lanes := demandChainLanes(buyer, chain)
	runs := selectDemandLaneRuns(demandLaneRuns(lanes, demandSearchGeographies(buyer)), 40)
	buyerSpecs := make([]marketSearchSpec, 0, len(runs))
	for _, run := range runs {
		buyerSpecs = append(buyerSpecs, marketSearchSpec{Lane: run.lane.term, Query: demandLaneSearchQuery(buyer.Activity, run.lane, run.geography)})
	}
	probe("buyer", "discovery", buyerSpecs, nil, demandFallbackCategory(buyer.Activity, chain), demandReviewScope(buyer), 10)
	probe("buyer", "verification", nil, demandVerificationPhraseList(), demandFallbackCategory(buyer.Activity, chain), demandReviewScope(buyer), 0)
}
