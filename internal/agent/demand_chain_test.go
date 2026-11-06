package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// demandLLM is a scripted LLM for the buyer pipeline: it fills whichever target
// the agent hands it from the queued values and records the system prompts, so
// tests can pin which phases ran.
type demandLLM struct {
	stages        []models.ValueChainStage
	candidates    []models.MarketLandscapeLeadCandidate
	err           error
	systemPrompts []string
}

func (m *demandLLM) GenerateStructured(ctx context.Context, systemPrompt, userPrompt string, target any) error {
	m.systemPrompts = append(m.systemPrompts, systemPrompt)
	if m.err != nil {
		return m.err
	}
	switch typed := target.(type) {
	case *[]models.ValueChainStage:
		*typed = append(*typed, m.stages...)
	case *[]models.MarketLandscapeLeadCandidate:
		*typed = append(*typed, m.candidates...)
	}
	return nil
}

// demandSearcher routes scripted hits by query substring, so a test can serve
// different pages to discovery and verification searches; it records every
// query, so tests can pin that the product and geography travelled.
type demandSearcher struct {
	hits    map[string][]models.SearchResult
	queries []string
}

func (m *demandSearcher) Search(ctx context.Context, query string, maxResults int) ([]models.SearchResult, error) {
	m.queries = append(m.queries, query)
	for fragment, hits := range m.hits {
		if strings.Contains(strings.ToLower(query), strings.ToLower(fragment)) {
			return hits, nil
		}
	}
	return nil, nil
}

func demandChainTestEngine(llm LLMClient, searcher Searcher) *Engine {
	return NewEngine(llm, searcher, &mockScraper{}, cache.NewMemoryCache(10), 5, 3, 10)
}

// TestDemandChainLanesCoverStagesThenDemand pins the lane order: the chain's
// operator vocabulary first, then the product-shaped demand lanes, then
// geography-scoped importer/tender lanes.
func TestDemandChainLanesCoverStagesThenDemand(t *testing.T) {
	lanes := demandChainLanes(models.DemandChainQuery{
		Activity:  "used cooking oil",
		Countries: []string{"Malaysia"},
	}, []models.ValueChainStage{{
		Name: "biodiesel production", Operators: []string{"biodiesel plant", "biorefinery"},
	}})
	terms := make([]string, 0, len(lanes))
	for _, lane := range lanes {
		terms = append(terms, lane.term)
		if lane.term == "biodiesel plant" && lane.stage != "biodiesel production" {
			t.Errorf("operator lane %q must carry its stage, got %q", lane.term, lane.stage)
		}
	}
	for _, expected := range []string{"biodiesel plant", "buyer procurement purchase", "tender RFP e-procurement", "importer import data", "Malaysia importer tender procurement"} {
		found := false
		for _, term := range terms {
			if term == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected lane %q in %v", expected, terms)
		}
	}
}

// TestDemandChainInferenceOverrideSkipsTheModel: the operator's stages win
// outright, the source is labelled, and no LLM call happens.
func TestDemandChainInferenceOverrideSkipsTheModel(t *testing.T) {
	llm := &demandLLM{}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, &demandSearcher{}))
	chain, inferred, _ := agent.inferDemandChain(context.Background(), models.DemandChainQuery{
		Activity: "used cooking oil", StagesOverride: []string{"oleochemicals", "", "animal feed"},
	}, nil)
	if inferred {
		t.Error("operator-supplied stages must not be reported as model-inferred")
	}
	if len(chain) != 2 || chain[0].Name != "oleochemicals" || chain[1].Name != "animal feed" {
		t.Fatalf("chain = %+v, want the operator's stages", chain)
	}
	for _, stage := range chain {
		if stage.Source != "operator" || len(stage.Operators) == 0 {
			t.Errorf("operator stage %+v must carry its source and vocabulary", stage)
		}
	}
	if len(llm.systemPrompts) != 0 {
		t.Errorf("the inference call must be skipped when stages are overridden, got %d call(s)", len(llm.systemPrompts))
	}
}

// TestDemandChainInferenceFallsBackOnFailure: a dead model degrades to a
// product-named generic stage with a warning, never a failed run.
func TestDemandChainInferenceFallsBackOnFailure(t *testing.T) {
	llm := &demandLLM{err: context.DeadlineExceeded}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, &demandSearcher{}))
	chain, inferred, warnings := agent.inferDemandChain(context.Background(), models.DemandChainQuery{Activity: "sulfur"}, nil)
	if inferred {
		t.Error("a failed inference must not claim the chain was inferred")
	}
	if len(chain) != 1 || chain[0].Source != "fallback" || chain[0].Name != "sulfur" {
		t.Fatalf("chain = %+v, want a single fallback stage named after the product", chain)
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "falling back") {
		t.Errorf("expected a fallback warning, got %v", warnings)
	}
}

// TestDemandChainInferenceCapsAndFillsVocabulary: the model's proposal is
// capped at the stage budget and every stage gains operator vocabulary.
func TestDemandChainInferenceCapsAndFillsVocabulary(t *testing.T) {
	stages := make([]models.ValueChainStage, 0, demandMaxStages+3)
	for index := 0; index < demandMaxStages+3; index++ {
		stages = append(stages, models.ValueChainStage{Name: string(rune('a'+index)) + "-stage"})
	}
	llm := &demandLLM{stages: stages}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, &demandSearcher{}))
	chain, inferred, _ := agent.inferDemandChain(context.Background(), models.DemandChainQuery{Activity: "palm oil"}, nil)
	if !inferred || len(chain) != demandMaxStages {
		t.Fatalf("inferred=%v stages=%d, want %d capped stages", inferred, len(chain), demandMaxStages)
	}
	for _, stage := range chain {
		if len(stage.Operators) == 0 {
			t.Errorf("stage %q must gain default operator vocabulary", stage.Name)
		}
	}
}

// TestDemandCandidateTagsStageAndFiltersNoise pins the discovery heuristics:
// stage tagging, demand-lane language checks, bait, and the intermediary
// exclusion toggle.
func TestDemandCandidateTagsStageAndFiltersNoise(t *testing.T) {
	lane := demandLane{term: "biodiesel plant", stage: "biodiesel production"}
	query := models.DemandChainQuery{Activity: "used cooking oil"}

	candidate := demandCandidate(models.SearchResult{
		Title: "GreenDiesel Sdn Bhd | Biodiesel Plant", Link: "https://www.greendiesel.com.my/plant",
	}, lane, query)
	if candidate == nil || candidate.EvidenceType != "company-page" || candidate.Stage != "biodiesel production" {
		t.Fatalf("candidate = %+v, want a company-page tagged with its stage", candidate)
	}
	if candidate.Evidence[0].ClaimType != models.DemandClaimOperator {
		t.Errorf("stage-operator lane claim = %q, want %q", candidate.Evidence[0].ClaimType, models.DemandClaimOperator)
	}

	// A demand lane without buyer language stays a discovery lead.
	demandLaneHit := demandLane{term: "tender RFP e-procurement"}
	quiet := demandCandidate(models.SearchResult{
		Title: "GreenDiesel Sdn Bhd | About", Link: "https://greendiesel.com.my/about",
	}, demandLaneHit, query)
	if quiet == nil || quiet.EvidenceType != "discovery-lead" {
		t.Errorf("a demand-lane hit without buyer language = %+v, want a discovery lead", quiet)
	}

	// Buyer bait is always a discovery lead.
	bait := demandCandidate(models.SearchResult{
		Title: "Market research: buyers of used cooking oil", Link: "https://researchvendor.example.com/report",
	}, demandLaneHit, query)
	if bait == nil || bait.EvidenceType != "discovery-lead" {
		t.Errorf("bait = %+v, want a discovery lead", bait)
	}

	// Intermediaries are excluded unless the operator asks for them.
	trader := demandCandidate(models.SearchResult{
		Title: "PalmOil Trading House", Link: "https://palmoiltrading.example.com",
	}, lane, query)
	if trader == nil || trader.Evidence[0].Status != "excluded" {
		t.Errorf("intermediary = %+v, want excluded by default", trader)
	}
	traderQuery := query
	traderQuery.IncludeIntermediaries = true
	kept := demandCandidate(models.SearchResult{
		Title: "PalmOil Trading House", Link: "https://palmoiltrading.example.com",
	}, lane, traderQuery)
	if kept == nil || kept.Evidence[0].Status == "excluded" {
		t.Errorf("intermediary with the toggle on = %+v, want it kept", kept)
	}
}

// TestDemandClaimFromTextAndWeights pins the claim taxonomy and the directness
// ladder, and the tier gate on verification evidence.
func TestDemandClaimFromTextAndWeights(t *testing.T) {
	cases := map[string]struct {
		hit    models.SearchResult
		claim  string
		weight float64
	}{
		"tender":   {models.SearchResult{Title: "State tender for biodiesel supply", Snippet: "e-procurement notice"}, models.DemandClaimTender, 0.12},
		"import":   {models.SearchResult{Title: "Customs import data", Snippet: "importer of used cooking oil"}, models.DemandClaimImport, 0.12},
		"usage":    {models.SearchResult{Title: "Our process", Snippet: "we use used cooking oil as feedstock"}, models.DemandClaimUsage, 0.10},
		"hiring":   {models.SearchResult{Title: "Job opening", Snippet: "procurement manager for feedstocks"}, models.DemandClaimHiring, 0.08},
		"operator": {models.SearchResult{Title: "Biodiesel plant operator", Snippet: "operates a biorefinery"}, models.DemandClaimOperator, 0.08},
	}
	for name, want := range cases {
		claim := demandClaimFromText(want.hit)
		if claim != want.claim {
			t.Errorf("%s: claim = %q, want %q", name, claim, want.claim)
		}
		if weight := demandClaimWeight(claim); weight != want.weight {
			t.Errorf("%s: weight = %.2f, want %.2f", name, weight, want.weight)
		}
	}

	// A verification hit from a non-qualifying tier is never evidence.
	rejected, ok := demandVerificationEvidence(models.SearchResult{
		Title: "Reuters: biodiesel demand", Link: "https://www.reuters.com/article",
	}, "GreenDiesel", "used cooking oil", "own site")
	if ok {
		t.Errorf("news-tier hit accepted as verification evidence: %+v", rejected)
	}

	// A qualifying hit with buyer language naming the company is evidence.
	accepted, ok := demandVerificationEvidence(models.SearchResult{
		Title: "GreenDiesel wins tender for used cooking oil supply", Link: "https://greendiesel.example.com/procurement",
	}, "GreenDiesel", "used cooking oil", "tender or procurement record")
	if !ok || accepted.ClaimType != models.DemandClaimTender {
		t.Errorf("tender hit = %+v ok=%v, want tender evidence", accepted, ok)
	}
}

// TestDemandChainResearchAssemblesVerifiedConsumers runs the whole pipeline
// offline: inference feeds stage lanes, discovery merges the operator page, and
// verification promotes the extracted consumer with tender evidence.
func TestDemandChainResearchAssemblesVerifiedConsumers(t *testing.T) {
	llm := &demandLLM{
		stages: []models.ValueChainStage{{
			Name: "biodiesel production", Rationale: "Biodiesel is produced from used cooking oil.",
			Operators: []string{"biodiesel plant"},
		}},
		candidates: []models.MarketLandscapeLeadCandidate{{
			Name: "GreenDiesel Sdn Bhd", Country: "Malaysia",
			EvidenceURL: "https://greendiesel.com.my/plant",
		}},
	}
	searcher := &demandSearcher{hits: map[string][]models.SearchResult{
		// The stage-operator lane surfaces the plant page, but its title does
		// not carry the domain token, so it enters the verification queue as a
		// discovery lead tagged with the consuming stage.
		"biodiesel plant": {{
			Title: "Used cooking oil biodiesel plant in Malaysia", Link: "https://greendiesel.com.my/plant",
			Snippet: "Produces biodiesel from used cooking oil feedstock.",
		}},
		// Verification purpose 1 ("official site") returns the company's own
		// page; purpose 2 returns a tender record naming the company.
		"official site": {{
			Title: "GreenDiesel Sdn Bhd | Biodiesel Plant", Link: "https://www.greendiesel.com.my/plant",
			Snippet: "We use used cooking oil as our feedstock.",
		}},
		"tender OR RFP": {{
			Title:   "GreenDiesel Sdn Bhd wins state tender for used cooking oil supply",
			Link:    "https://tenderportal.example.com/uco-tender",
			Snippet: "E-procurement award: GreenDiesel Sdn Bhd to purchase used cooking oil feedstock.",
		}},
	}}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, searcher))

	report, err := agent.Research(context.Background(), "research-1", models.DemandChainQuery{
		Activity: "used cooking oil", Countries: []string{"Malaysia"},
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if !report.ChainInferred || len(report.Chain) != 1 || report.Chain[0].Name != "biodiesel production" {
		t.Fatalf("chain = %+v inferred=%v, want the model's stage", report.Chain, report.ChainInferred)
	}
	if len(report.Results) == 0 {
		t.Fatalf("results = %+v, want the verified consumer", report.Results)
	}
	top := report.Results[0]
	if top.Name != "GreenDiesel Sdn Bhd" || top.Stage != "biodiesel production" {
		t.Fatalf("top result = %+v, want the verified consumer with its stage", top)
	}
	if top.Status != "verified" && top.Status != "corroborated" {
		t.Errorf("status = %q, want verified or corroborated", top.Status)
	}
	foundTender := false
	for _, evidence := range top.Evidence {
		if evidence.ClaimType == models.DemandClaimTender {
			foundTender = true
		}
	}
	if !foundTender {
		t.Errorf("expected tender-claim evidence on %+v", top.Evidence)
	}
	if report.VerificationQueries == 0 || report.QueriesUsed == 0 {
		t.Errorf("queries: discovery=%d verification=%d, want both counted", report.QueriesUsed, report.VerificationQueries)
	}
	// The execution must reflect the user's product + region: the report
	// records the resolved scope, and every verification search carries both
	// the product and the geography.
	if report.Geography != "Malaysia" {
		t.Errorf("geography = %q, want the run's resolved scope", report.Geography)
	}
	verificationQueries := 0
	for _, query := range searcher.queries {
		if strings.Contains(query, `"GreenDiesel Sdn Bhd"`) {
			verificationQueries++
			if !strings.Contains(query, "used cooking oil") || !strings.Contains(query, "Malaysia") {
				t.Errorf("verification query %q must carry the product and the geography", query)
			}
		}
	}
	if verificationQueries == 0 {
		t.Errorf("expected verification searches to be recorded, got none in %v", searcher.queries)
	}
	foundHonesty := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "Private procurement is invisible") {
			foundHonesty = true
		}
	}
	if !foundHonesty {
		t.Errorf("expected the honesty warning in %v", report.Warnings)
	}
}
