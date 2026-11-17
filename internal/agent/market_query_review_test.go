package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// queryReviewLLM scripts the adversarial query review: it records every
// prompt and fills the review target with the queued verdicts.
type queryReviewLLM struct {
	mu       sync.Mutex
	response marketQueryReviewTarget
	err      error
	systems  []string
	users    []string
}

func (m *queryReviewLLM) GenerateStructured(ctx context.Context, systemPrompt, userPrompt string, target any) error {
	m.mu.Lock()
	m.systems = append(m.systems, systemPrompt)
	m.users = append(m.users, userPrompt)
	response := m.response
	err := m.err
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if typed, ok := target.(*marketQueryReviewTarget); ok {
		typed.Queries = append(typed.Queries, response.Queries...)
		typed.Purposes = append(typed.Purposes, response.Purposes...)
	}
	return nil
}

func queryReviewSearcher() *demandSearcher {
	return &demandSearcher{hits: map[string][]models.SearchResult{}}
}

func marketQueryReviewVerdicts(reviews []models.MarketQueryReview, original string) *models.MarketQueryReview {
	for i, review := range reviews {
		if review.Original == original {
			return &reviews[i]
		}
	}
	return nil
}

// TestMarketQueryReviewRewritesDropsAndRefuses pins the merge rules: an echo
// lane is rewritten when the reviewer offers a legal replacement and dropped
// otherwise, an LLM drop wins, a scope-violating rewrite is refused and the
// original runs, and a rewritten purpose replaces the verification template.
func TestMarketQueryReviewRewritesDropsAndRefuses(t *testing.T) {
	specs := []marketSearchSpec{
		{Lane: "manufacturer", Query: `"Oil Blending" manufacturer Middle East`},
		{Lane: "echo", Query: `oil blending industrial oil blending Middle East`},
		{Lane: "region", Query: `oil blending Middle East company operator manufacturer Middle East`},
		{Lane: "exporter", Query: `"Oil Blending" exporter Middle East`},
	}
	llm := &queryReviewLLM{response: marketQueryReviewTarget{
		Queries: []marketQueryVerdict{
			{Query: `oil blending industrial oil blending Middle East`, Verdict: "rewrite", RevisedQuery: `"oil blending" plant Middle East`, Reason: "natural phrasing"},
			{Query: `"Oil Blending" exporter Middle East`, Verdict: "rewrite", RevisedQuery: `oleochemicals exporter Middle East`, Reason: "trade vocabulary needs a product noun"},
			{Query: `"Oil Blending" manufacturer Middle East`, Verdict: "drop", Reason: "the category lane covers manufacturers"},
		},
		Purposes: []marketQueryVerdict{
			{Query: "registry trade source", Verdict: "rewrite", RevisedQuery: "company registry record", Reason: "no indexed page contains the phrase"},
		},
	}}
	agent := NewMarketLandscapeAgent(demandChainTestEngine(llm, queryReviewSearcher()))
	query := models.MarketLandscapeQuery{Activity: "Oil Blending", Regions: []string{"Middle East"}}
	surviving, purposes, reviews, warning := agent.reviewMarketQueryBatch(context.Background(), "discovery", specs, marketVerificationPurposes(), "oleochemicals", query, nil)
	if warning != "" {
		t.Fatalf("unexpected review warning: %v", warning)
	}
	if len(surviving) != 2 {
		t.Fatalf("expected 2 surviving queries, got %d: %v", len(surviving), surviving)
	}
	if surviving[0].Query != `"oil blending" plant Middle East` {
		t.Errorf("echo lane must run rewritten, got %q", surviving[0].Query)
	}
	if surviving[1].Query != `oleochemicals exporter Middle East` {
		t.Errorf("exporter lane must run re-anchored on the category, got %q", surviving[1].Query)
	}
	wantPurposes := []string{"official site", "facility operator", "company registry record"}
	if strings.Join(purposes, "|") != strings.Join(wantPurposes, "|") {
		t.Errorf("purposes = %v, want %v", purposes, wantPurposes)
	}
	if len(reviews) != 5 {
		t.Fatalf("expected 5 review rows, got %d: %v", len(reviews), reviews)
	}
	if review := marketQueryReviewVerdicts(reviews, specs[2].Query); review == nil || review.Verdict != "dropped" || review.Reason != "repeats its own words" {
		t.Errorf("region lane must drop on echo without a reviewer rewrite, got %+v", review)
	}
	if review := marketQueryReviewVerdicts(reviews, "registry trade source"); review == nil || review.Verdict != "rewritten" || review.Phase != "verification" {
		t.Errorf("purpose rewrite must be recorded on the verification phase, got %+v", review)
	}
	for _, fragment := range []string{"Oil Blending", "oleochemicals", "volza.com", "Middle East"} {
		if !strings.Contains(llm.users[0], fragment) {
			t.Errorf("review prompt must carry %q to the reviewer", fragment)
		}
	}
}

// TestMarketQueryReviewRefusesUnsafeRewrites pins the rewrite guardrails: a
// rewrite may not widen geography outside the requested scope, may not lean
// on a site: domain outside the operator allowlist, and may not smuggle
// geography into a verification purpose. Every refusal falls back to the
// original query or purpose.
func TestMarketQueryReviewRefusesUnsafeRewrites(t *testing.T) {
	specs := []marketSearchSpec{
		{Lane: "manufacturer", Query: `"Oil Blending" manufacturer Middle East`},
		{Lane: "plant", Query: `"Oil Blending" plant Middle East`},
		{Lane: "exporter", Query: `"Oil Blending" exporter Middle East`},
	}
	llm := &queryReviewLLM{response: marketQueryReviewTarget{
		Queries: []marketQueryVerdict{
			{Query: specs[0].Query, Verdict: "rewrite", RevisedQuery: `"Oil Blending" manufacturer India`},
			{Query: specs[1].Query, Verdict: "rewrite", RevisedQuery: `"Oil Blending" plant site:example.com`},
			{Query: specs[2].Query, Verdict: "rewrite", RevisedQuery: `site:volza.com "oil blending" exporter`},
		},
		Purposes: []marketQueryVerdict{
			{Query: "facility operator", Verdict: "rewrite", RevisedQuery: "blending facility operator UAE"},
		},
	}}
	agent := NewMarketLandscapeAgent(demandChainTestEngine(llm, queryReviewSearcher()))
	query := models.MarketLandscapeQuery{Activity: "Oil Blending", Regions: []string{"Middle East"}}
	surviving, purposes, reviews, _ := agent.reviewMarketQueryBatch(context.Background(), "discovery", specs, marketVerificationPurposes(), "oleochemicals", query, nil)
	if len(surviving) != 3 {
		t.Fatalf("every refused rewrite must fall back to its original, got %d survivors: %v", len(surviving), surviving)
	}
	if surviving[0].Query != specs[0].Query || surviving[1].Query != specs[1].Query {
		t.Errorf("geography and site violations must keep the original queries, got %q and %q", surviving[0].Query, surviving[1].Query)
	}
	if surviving[2].Query != `site:volza.com "oil blending" exporter` {
		t.Errorf("an allowlisted trade-site rewrite must be applied, got %q", surviving[2].Query)
	}
	if review := marketQueryReviewVerdicts(reviews, specs[0].Query); review == nil || review.Verdict != "kept" || !strings.Contains(review.Reason, `"india"`) {
		t.Errorf("the refused geography rewrite must be recorded as kept with its reason, got %+v", review)
	}
	wantPurposes := []string{"official site", "facility operator", "registry trade source"}
	if strings.Join(purposes, "|") != strings.Join(wantPurposes, "|") {
		t.Errorf("a purpose rewrite naming geography must be refused, got %v", purposes)
	}
}

// TestMarketQueryReviewFailsOpen pins the failure path: when the reviewer is
// unavailable the original queries and purposes run unchanged and the warning
// travels back for the report.
func TestMarketQueryReviewFailsOpen(t *testing.T) {
	specs := []marketSearchSpec{{Lane: "manufacturer", Query: `"Oil Blending" manufacturer Middle East`}}
	purposes := marketVerificationPurposes()
	llm := &queryReviewLLM{err: errors.New("review model unavailable")}
	agent := NewMarketLandscapeAgent(demandChainTestEngine(llm, queryReviewSearcher()))
	query := models.MarketLandscapeQuery{Activity: "Oil Blending", Regions: []string{"Middle East"}}
	surviving, survivingPurposes, reviews, warning := agent.reviewMarketQueryBatch(context.Background(), "discovery", specs, purposes, "oleochemicals", query, nil)
	if warning == "" || !strings.Contains(warning, "Query review unavailable") {
		t.Fatalf("the fail-open path must return a warning, got %q", warning)
	}
	if len(surviving) != 1 || surviving[0].Query != specs[0].Query {
		t.Errorf("original queries must run unchanged, got %v", surviving)
	}
	if strings.Join(survivingPurposes, "|") != strings.Join(purposes, "|") {
		t.Errorf("original purposes must run unchanged, got %v", survivingPurposes)
	}
	if reviews != nil {
		t.Errorf("no review rows may be recorded when the review failed, got %v", reviews)
	}
}

// TestMarketQueryReviewMechanicalRedundancy pins the deterministic guards:
// a later query whose words repeat an earlier same-class query without adding
// a hard constraint is dropped without needing the reviewer, while a
// site-scoped query is never redundant with an open one — an open query whose
// words sit inside a site-scoped lane's vocabulary survives, because the
// site: operator restricts a different retrieval space.
func TestMarketQueryReviewMechanicalRedundancy(t *testing.T) {
	specs := []marketSearchSpec{
		{Lane: "directory", Query: `site:ensun.io "Oil Blending" manufacturer`},
		{Lane: "company", Query: `"Oil Blending" company Middle East`},
		{Lane: "reordered", Query: `Middle East company "Oil Blending"`},
		{Lane: "open-subset", Query: `"Oil Blending" company`},
		{Lane: "open-subset-of-site", Query: `"Oil Blending" manufacturer`},
	}
	agent := NewMarketLandscapeAgent(demandChainTestEngine(&queryReviewLLM{}, queryReviewSearcher()))
	query := models.MarketLandscapeQuery{Activity: "Oil Blending", Regions: []string{"Middle East"}}
	surviving, _, reviews, warning := agent.reviewMarketQueryBatch(context.Background(), "discovery", specs, nil, "oleochemicals", query, nil)
	if warning != "" {
		t.Fatalf("unexpected review warning: %v", warning)
	}
	if len(surviving) != 3 {
		t.Fatalf("the two first-of-set lanes plus the class-separated open twin may survive, got %v", surviving)
	}
	if surviving[0].Query != specs[0].Query || surviving[1].Query != specs[1].Query || surviving[2].Query != specs[4].Query {
		t.Errorf("unexpected survivors: %v", surviving)
	}
	if len(reviews) != 2 {
		t.Fatalf("expected 2 dropped rows, got %d: %v", len(reviews), reviews)
	}
	for _, review := range reviews {
		if review.Original != specs[2].Query && review.Original != specs[3].Query {
			t.Errorf("only the reordered and open-subset lanes may drop, got %+v", review)
		}
		if review.Verdict != "dropped" || !strings.Contains(review.Reason, "redundant with query") {
			t.Errorf("redundant lanes must drop mechanically, got %+v", review)
		}
	}
}

// TestResearchCategoryLaneSearchesPerRegion pins the category-lane composition
// fix: two requested regions mean one query per lane per region, never one
// query carrying "Middle East Africa" as a token soup, and a clean keep-all
// review leaves no reviews, warnings, or verification spend behind.
func TestResearchCategoryLaneSearchesPerRegion(t *testing.T) {
	searcher := queryReviewSearcher()
	agent := NewMarketLandscapeAgent(demandChainTestEngine(&queryReviewLLM{}, searcher))
	query := models.MarketLandscapeQuery{Activity: "Oil Blending", Regions: []string{"Middle East", "Africa"}}
	var trace []models.MarketSearchTrace
	_, queriesUsed, _, verificationQueries, reviews, warnings := agent.researchCategoryLane(context.Background(), query, "oleochemicals", marketVerificationPurposes(), nil, &trace)
	if queriesUsed != 4 {
		t.Fatalf("two lanes across two regions must run 4 queries, got %d", queriesUsed)
	}
	for _, want := range []string{
		"oleochemicals manufacturers Middle East",
		"oleochemicals manufacturers Africa",
		"oleochemicals companies Middle East",
		"oleochemicals companies Africa",
	} {
		found := false
		for _, executed := range searcher.queries {
			if executed == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("category lane must run %q, executed: %v", want, searcher.queries)
		}
	}
	for _, soup := range searcher.queries {
		if strings.Contains(soup, "Middle East Africa") {
			t.Errorf("no category query may carry the concatenated region soup, got %q", soup)
		}
	}
	if verificationQueries != 0 {
		t.Errorf("a lane with no leads must spend no verification queries, got %d", verificationQueries)
	}
	if len(reviews) != 0 || len(warnings) != 0 || len(trace) != 4 {
		t.Errorf("a clean keep-all review must leave no reviews or warnings and one trace row per query, got %v / %v / %d", reviews, warnings, len(trace))
	}
}
