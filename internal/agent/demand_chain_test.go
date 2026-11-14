package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// demandLLM is a scripted LLM for the buyer pipeline: it fills whichever target
// the agent hands it from the queued values and records the system prompts, so
// tests can pin which phases ran.
type demandLLM struct {
	mu            sync.Mutex
	stages        []models.ValueChainStage
	category      string
	candidates    []models.MarketLandscapeLeadCandidate
	gapCandidates []models.MarketLandscapeLeadCandidate
	people        []models.MarketLandscapePersonCandidate
	review        models.DemandChainReview
	chainReview   models.ValueChainReview
	err           error
	systemPrompts []string
}

func (m *demandLLM) GenerateStructured(ctx context.Context, systemPrompt, userPrompt string, target any) error {
	m.mu.Lock()
	m.systemPrompts = append(m.systemPrompts, systemPrompt)
	err := m.err
	stages, candidates, people := m.stages, m.candidates, m.people
	category := m.category
	gapCandidates := m.gapCandidates
	review := m.review
	chainReview := m.chainReview
	m.mu.Unlock()
	if err != nil {
		return err
	}
	switch typed := target.(type) {
	case *[]models.ValueChainStage:
		*typed = append(*typed, stages...)
	case *models.DemandChainInference:
		typed.Category = category
		typed.Stages = append(typed.Stages, stages...)
	case *[]models.MarketLandscapeLeadCandidate:
		if strings.Contains(userPrompt, "Extract every actual company that operates this consuming stage") && len(gapCandidates) > 0 {
			*typed = append(*typed, gapCandidates...)
		} else {
			*typed = append(*typed, candidates...)
		}
	case *[]models.MarketLandscapePersonCandidate:
		*typed = append(*typed, people...)
	case *models.DemandChainReview:
		*typed = review
	case *models.ValueChainReview:
		*typed = chainReview
	}
	return nil
}

// demandSearcher routes scripted hits by query substring, so a test can serve
// different pages to discovery and verification searches; it records every
// query thread-safely, because the pipeline searches with bounded concurrency.
type demandSearcher struct {
	mu      sync.Mutex
	hits    map[string][]models.SearchResult
	queries []string
}

func (m *demandSearcher) Search(ctx context.Context, query string, maxResults int) ([]models.SearchResult, error) {
	m.mu.Lock()
	m.queries = append(m.queries, query)
	for fragment, hits := range m.hits {
		if strings.Contains(strings.ToLower(query), strings.ToLower(fragment)) {
			m.mu.Unlock()
			return hits, nil
		}
	}
	m.mu.Unlock()
	return nil, nil
}

func demandChainTestEngine(llm LLMClient, searcher Searcher) *Engine {
	return NewEngine(llm, searcher, &mockScraper{}, cache.NewMemoryCache(10), 5, 3, 10)
}

// TestSelectDemandLanesReservesDirectEnquiryAndTender pins the selection cap:
// direct-enquiry and tender lanes survive the cap, and the selected set stays at
// the requested size.

// TestDemandChainLanesCoverStagesThenDemand pins the lane order: the chain's
// product-plus-sector company query first, then operator vocabulary, then
// product-shaped demand lanes, then
// geography-scoped importer/tender lanes. Distributors and wholesalers are
// retained as buyers (they purchase to resell), so their lane always runs;
// only the deeper value-chain stages were scoped out.
func TestDemandChainLanesCoverStagesThenDemand(t *testing.T) {
	chain := []models.ValueChainStage{{
		Name: "biodiesel production", Operators: []string{"biodiesel plant", "biorefinery"},
	}}
	terms := func(query models.DemandChainQuery) []string {
		out := make([]string, 0, 10)
		for _, lane := range demandChainLanes(query, chain) {
			out = append(out, lane.term)
			if lane.term == "biodiesel plant" && lane.stage != "biodiesel production" {
				t.Errorf("operator lane %q must carry its stage, got %q", lane.term, lane.stage)
			}
		}
		return out
	}
	query := models.DemandChainQuery{Activity: "used cooking oil", Countries: []string{"Malaysia"}}
	defaults := terms(query)
	for _, expected := range []string{"largest manufacturers of biodiesel", "biodiesel plant", "buyer procurement purchase", "tender RFP e-procurement", "importer import data", "raw material feedstock consumption", "industrial applications manufacturers", "distributor wholesaler of", "Malaysia importer tender procurement"} {
		found := false
		for _, term := range defaults {
			if term == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected lane %q in %v", expected, defaults)
		}
	}
	if defaults[0] != "largest manufacturers of biodiesel" {
		t.Fatalf("stage company lane must be first, got %q", defaults[0])
	}
	for _, banned := range []string{"applications uses industries"} {
		for _, term := range defaults {
			if term == banned {
				t.Errorf("lane %q must not run under the two-step buyer scope, found in %v", banned, defaults)
			}
		}
	}
}

func TestSelectDemandLanesReservesDirectEnquiryAndTender(t *testing.T) {
	lanes := []demandLane{
		{term: "soap manufacturing companies", stage: "soap manufacturing"},
		{term: "soap plant", stage: "soap manufacturing"},
		{term: "buyer procurement purchase"},
		{term: "tender RFP e-procurement"},
		{term: "importer import data"},
	}
	selected := selectDemandLanes(lanes, 4)
	terms := make(map[string]bool, len(selected))
	for _, lane := range selected {
		terms[lane.term] = true
	}
	for _, required := range []string{"soap manufacturing companies", "buyer procurement purchase", "tender RFP e-procurement"} {
		if !terms[required] {
			t.Errorf("selected lanes must include %q, got %+v", required, selected)
		}
	}
	if len(selected) != 4 {
		t.Fatalf("selected %d lanes, want 4: %+v", len(selected), selected)
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
		if stage.Distance != models.StageDistanceDirect {
			t.Errorf("operator stage %+v must be scoped as a direct consumer", stage)
		}
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

// TestDemandChainInferenceEnforcesTwoStepScope pins the buyer definition:
// unlabelled distances normalize to direct consumption, a step-2 stage stays,
// and a stage deeper than two transformation steps is dropped as out of scope.
func TestDemandChainInferenceEnforcesTwoStepScope(t *testing.T) {
	stages := []models.ValueChainStage{
		{Name: "unlabelled"},
		{Name: "direct consumption", Distance: models.StageDistanceDirect},
		{Name: "derived consumption", Distance: models.StageDistanceDerived},
		{Name: "end-use sector", Distance: 3},
	}
	llm := &demandLLM{stages: stages}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, &demandSearcher{}))
	chain, inferred, _ := agent.inferDemandChain(context.Background(), models.DemandChainQuery{Activity: "palm oil"}, nil)
	if !inferred {
		t.Fatal("the chain must be reported as inferred")
	}
	want := map[string]int{
		"unlabelled":          models.StageDistanceDirect,
		"direct consumption":  models.StageDistanceDirect,
		"derived consumption": models.StageDistanceDerived,
	}
	if len(chain) != len(want) {
		t.Fatalf("chain = %+v, want %d stages (the step-3 stage must be dropped)", chain, len(want))
	}
	for _, stage := range chain {
		if want[stage.Name] != stage.Distance {
			t.Errorf("stage %q distance = %d, want %d", stage.Name, stage.Distance, want[stage.Name])
		}
	}
}

// TestDemandChainLanesFavourDirectConsumers: under the same discovery budget,
// a derived (step-2) stage earns one fewer operator lane than a direct stage,
// so the queries land on the strongest buyer hypotheses first.
func TestDemandChainLanesFavourDirectConsumers(t *testing.T) {
	chain := []models.ValueChainStage{
		{Name: "oleochemicals", Distance: models.StageDistanceDirect, Operators: []string{"oleochemical plant", "fatty acid plant"}},
		{Name: "soap making", Distance: models.StageDistanceDerived, Operators: []string{"soap plant", "detergent plant"}},
	}
	counts := make(map[string]int)
	for _, lane := range demandChainLanes(models.DemandChainQuery{Activity: "palm oil"}, chain) {
		counts[lane.stage]++
	}
	if counts["oleochemicals"] != 2 || counts["soap making"] != 1 {
		t.Errorf("stage lane counts = %v, want the direct stage at 2 and the derived stage at 1", counts)
	}
}

// TestDemandBuyerFitConfidence pins the step penalty: direct consumers keep
// their confidence, derived consumers carry a deterministic step penalty, and
// the floor keeps a heavily penalized candidate from collapsing to zero.
func TestDemandBuyerFitConfidence(t *testing.T) {
	direct := &models.ValueChainStage{Name: "s", Distance: models.StageDistanceDirect}
	derived := &models.ValueChainStage{Name: "s", Distance: models.StageDistanceDerived}
	if got := demandBuyerFitConfidence(0.84, direct); got != 0.84 {
		t.Errorf("direct-stage confidence = %v, want unchanged", got)
	}
	if got := demandBuyerFitConfidence(0.84, nil); got != 0.84 {
		t.Errorf("untied confidence = %v, want unchanged", got)
	}
	if got := demandBuyerFitConfidence(0.84, derived); got < 0.789 || got > 0.791 {
		t.Errorf("derived-stage confidence = %v, want 0.79", got)
	}
	if got := demandBuyerFitConfidence(0.40, derived); got < 0.349 || got > 0.351 {
		t.Errorf("derived-stage low confidence = %v, want 0.35", got)
	}
	if got := demandBuyerFitConfidence(0.32, derived); got != 0.30 {
		t.Errorf("floored confidence = %v, want 0.30", got)
	}
}

// TestDemandChainProducerFlagDemotesTheExtractorJudgement: when the
// extraction flags a candidate as a producer of the product itself, the
// company is demoted from the results — it enters the run as a lead and its
// evidence stays in the appendix unless transactional proof exists.
func TestDemandChainProducerFlagDemotesTheExtractorJudgement(t *testing.T) {
	llm := &demandLLM{
		stages: []models.ValueChainStage{{Name: "biodiesel production", Operators: []string{"biodiesel plant"}}},
		candidates: []models.MarketLandscapeLeadCandidate{
			{Name: "Methanex", Producer: true, EvidenceURL: "https://methanex.com"},
		},
	}
	searcher := &demandSearcher{hits: map[string][]models.SearchResult{
		"biodiesel plant": {{
			Title: "Used cooking oil biodiesel plant in Malaysia", Link: "https://greendiesel.com.my/plant",
			Snippet: "Produces biodiesel from used cooking oil feedstock.",
		}},
		"official site": {{
			Title: "Methanex | Home", Link: "https://methanex.com",
			Snippet: "Methanex operates a used cooking oil handling terminal.",
		}},
	}}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, searcher))
	report, err := agent.Research(context.Background(), "r", models.DemandChainQuery{Activity: "used cooking oil"}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	for _, result := range report.Results {
		if strings.Contains(result.Name, "Methanex") {
			t.Errorf("a producer-only company must be demoted from the results, got %+v", result)
		}
	}
}

// TestDemandVerificationAggregatorHostedEvidence: a tender listing hosted on
// a portal domain that names the buyer is real claim evidence about that
// buyer — the aggregator gate stops the portal masquerading as a company, it
// must not erase the buyer's tender record. Publishers, by contrast, never
// verify.
func TestDemandVerificationAggregatorHostedEvidence(t *testing.T) {
	tenderHit := models.SearchResult{
		Title:   "Tender for Supply of Methanol to GNFC, Bharuch Plant",
		Link:    "https://indiantender.in/gnfc-methanol",
		Snippet: "Gujarat Narmada Valley Fertilizers and Chemicals Limited invited bids for the supply of Methanol.",
	}
	evidence, ok := demandVerificationEvidence(tenderHit, "GNFC", "methanol", "tender or procurement record")
	if !ok {
		t.Fatal("tender evidence hosted on an aggregator portal must still verify the buyer it names")
	}
	if evidence.ClaimType != models.DemandClaimTender {
		t.Errorf("claim type = %q, want demand-tender", evidence.ClaimType)
	}
	publisherHit := models.SearchResult{
		Title:   "Methanol carbonylation kinetics",
		Link:    "https://pubs.acs.org/doi/10.1021/example",
		Snippet: "Methanol carbonylation experiments reference Acme Chemicals catalysts.",
	}
	if _, ok := demandVerificationEvidence(publisherHit, "Acme Chemicals", "methanol", "own site"); ok {
		t.Error("publisher-hosted hits must never verify a company")
	}
	directoryHit := models.SearchResult{
		Title:   "Acme Chemicals - trade directory listing",
		Link:    "https://www.tradeindia.com/acme-chemicals",
		Snippet: "Acme Chemicals buys methanol for its plant.",
	}
	if _, ok := demandVerificationEvidence(directoryHit, "Acme Chemicals", "methanol", "own site"); ok {
		t.Error("directory-hosted hits are corroboration only and must never verify a company")
	}
}

// TestDemandProducerSignal pins the producer gate's precision: every marker
// embeds the product name itself, so a downstream manufacturer's page never
// misfires, and the buyer's own input language is never producer evidence.
func TestDemandProducerSignal(t *testing.T) {
	producer := models.SearchResult{
		Title:   "Methanex Corporation",
		Snippet: "Methanex is the world's largest methanol producer and supplier.",
	}
	if !demandProducerSignal(producer, "methanol") {
		t.Error("a producer self-description must be flagged")
	}
	downstream := models.SearchResult{
		Title:   "Reichhold | Resins",
		Snippet: "Reichhold is a manufacturer of resins, consuming methanol as a raw material for formaldehyde production.",
	}
	if demandProducerSignal(downstream, "methanol") {
		t.Error("a manufacturer of a downstream product must not be flagged as a producer of the product itself")
	}
	usage := models.SearchResult{
		Title:   "Acme Chemicals",
		Snippet: "We purchase methanol as a raw material for acetic acid production.",
	}
	if demandProducerSignal(usage, "methanol") {
		t.Error("the buyer's own input language must not be flagged as producer evidence")
	}
}

// TestDemandVerificationPurposesFollowTheDiscoveryClaim: the verification
// searches run cheapest-first for what the discovery lane already claimed.
func TestDemandVerificationPurposesFollowTheDiscoveryClaim(t *testing.T) {
	firstKind := func(claim string) string {
		return demandVerificationPurposes(claim)[0].kind
	}
	if got := firstKind(models.DemandClaimTender); got != "tender or procurement record" {
		t.Errorf("a tender-discovered lead must verify via tender evidence first, got %q", got)
	}
	if got := firstKind(models.DemandClaimImport); got != "import record" {
		t.Errorf("an import-discovered lead must verify via trade data first, got %q", got)
	}
	if got := firstKind(models.DemandClaimOperator); got != "own site" {
		t.Errorf("a stage-discovered lead must verify via the company's own words first, got %q", got)
	}
}

// TestDemandSourceOverrideAndName: tender portals, B2B marketplaces and
// publishers are demand signals and bibliography — never qualifying tiers,
// never verified companies.
func TestDemandSourceOverrideAndName(t *testing.T) {
	for domain, want := range map[string]string{
		"indiantender.in":         "aggregator",
		"www.tendersontime.com":   "aggregator",
		"tradeindia.com":          "aggregator",
		"mdpi.com":                "publisher",
		"pubs.acs.org":            "publisher",
		"onlinelibrary.wiley.com": "publisher",
		"www.imarcgroup.com":      "consultancy",
		"statista.com":            "consultancy",
		"www.intratec.us":         "consultancy",
		"engconfintl.org":         "institution",
		"iea.org":                 "institution",
		"www.greendiesel.com.my":  "",
	} {
		if got := demandSourceOverride(domain); got != want {
			t.Errorf("demandSourceOverride(%q) = %q, want %q", domain, got, want)
		}
	}
	for _, name := range []string{
		"Latest Alcohol Tenders in India 2026 - TendersOnTime",
		"Carbonylation of Methanol: A Versatile Reaction - Wiley Online Library",
		"mdpi.com",
		"IMARC Group",
		"Intratec",
		"Methanol Carbonylation - Energyscope",
		"Methanol Carbonylation – Energyscope",
		"Community Exposure to Toluene Diisocyanate",
		"Methanol Use In Gasoline",
		"NPTEL IIT Guwahati",
		"engconfintl.org",
		"chemprob.org",
		"ACS Publications",
		"August Wilhelm von Hofmann",
		"Biodiesel producers",
		"Biodiesel Production and Distribution",
		"Renewable Toluene Diisocyanate (TDI) Production Plant",
		"TDI Products",
		"Technology by Covestro",
		"Biodiesel plant licensing in India",
	} {
		if !demandSourceName(name, "methanol") {
			t.Errorf("demandSourceName(%q) must reject a source masquerading as a buyer", name)
		}
	}
	for _, name := range []string{
		"Gujarat Narmada Valley Fertilizers & Chemicals Limited",
		"GreenDiesel Sdn Bhd",
		"Aldehydes India",
		"Kunnathan Polymers",
		"INEOS-GNFC",
		"Acme Products Ltd",
		"Acme Distribution",
	} {
		if demandSourceName(name, "methanol") {
			t.Errorf("demandSourceName(%q) must not reject a real company", name)
		}
	}
}

// TestDemandNormalizeCandidateName: an echoed page title or headline collapses
// to its company segment — longest pipe part, then pre-colon, then
// pre-possessive.
func TestDemandNormalizeCandidateName(t *testing.T) {
	for name, want := range map[string]string{
		"Butyl Acrylate | Products | Mitsubishi Chemical Corporation":             "Mitsubishi Chemical Corporation",
		"INEOS-GNFC's New Plant: Is India's Methanol-Acetic Acid Bet Paying Off?": "INEOS-GNFC",
		"Acme Chemicals: Formaldehyde Supplier":                                   "Acme Chemicals",
		"GreenDiesel Sdn Bhd":                                                     "GreenDiesel Sdn Bhd",
	} {
		if got := demandNormalizeCandidateName(name); got != want {
			t.Errorf("demandNormalizeCandidateName(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestDemandProducerGateGradesSupplySide: a producer with no demand claim is
// demoted, while an integrated producer-consumer with transactional proof
// stays — penalized, not excluded.
func TestDemandProducerGateGradesSupplySide(t *testing.T) {
	producer := &models.MarketLandscapeCompany{
		EntityKey: "company:name:methanex", Name: "Methanex", EvidenceType: "company-page",
		Confidence: 0.70, Activity: "methanol",
		Evidence: []models.MarketLandscapeEvidence{{
			URL: "https://methanex.com", ClaimType: models.DemandClaimOperator,
			SourceTier: "official", Status: "producer-signal",
		}},
	}
	if !demandIsProducerWithoutDemand(producer) {
		t.Fatal("a producer with no demand claim must be demoted")
	}
	integrated := &models.MarketLandscapeCompany{
		EntityKey: "company:name:gnfc", Name: "GNFC", EvidenceType: "company-page",
		Confidence: 0.98, Activity: "methanol",
		Evidence: []models.MarketLandscapeEvidence{
			{URL: "https://gnfc.in", ClaimType: models.DemandClaimOperator, SourceTier: "official", Status: "producer-signal"},
			{URL: "https://tender.example.com/gnfc", ClaimType: models.DemandClaimTender, SourceTier: "official", Status: "tender or procurement record"},
		},
	}
	if demandIsProducerWithoutDemand(integrated) {
		t.Fatal("an integrated producer-consumer with transactional proof must not be demoted")
	}
	if !demandHasProducerSignal(integrated) || demandHasProducerSignal(producer) == false {
		t.Fatal("both companies must carry the producer signal")
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
	// Alias-level dedupe: the discovery candidate and the verified company are
	// one house, so the results carry GreenDiesel exactly once.
	if len(report.Results) != 1 {
		t.Fatalf("results = %d entries (%+v), want the deduplicated single consumer", len(report.Results), report.Results)
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

// TestDemandChainBudgetOverride: the query's budget fields drive the run —
// zero keeps the defaults, and both caps are honoured exactly.
func TestDemandChainBudgetOverride(t *testing.T) {
	llm := &demandLLM{
		stages: []models.ValueChainStage{{
			Name: "biodiesel production", Operators: []string{"biodiesel plant"},
		}},
		candidates: []models.MarketLandscapeLeadCandidate{{
			Name: "GreenDiesel Sdn Bhd", Country: "Malaysia",
			EvidenceURL: "https://greendiesel.com.my/plant",
		}},
	}
	searcher := &demandSearcher{hits: map[string][]models.SearchResult{
		"biodiesel plant": {{
			Title: "Used cooking oil biodiesel plant in Malaysia", Link: "https://greendiesel.com.my/plant",
			Snippet: "Produces biodiesel from used cooking oil feedstock.",
		}},
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
		Activity:            "used cooking oil",
		DiscoveryQueries:    4,
		VerificationQueries: 2,
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if report.QueriesUsed != 4 {
		t.Errorf("discovery queries = %d, want exactly the 4-query budget", report.QueriesUsed)
	}
	if report.VerificationQueries != 2 {
		t.Errorf("verification queries = %d, want exactly the 2-query budget", report.VerificationQueries)
	}
}

// TestDemandChainDefaultsAreKept: zero budget fields fall back to the mode
// defaults, and oversized budgets are capped — the console and the shipped
// behaviour never change.
func TestDemandChainDefaultsAreKept(t *testing.T) {
	query := models.DemandChainQuery{Activity: "x"}
	normalizeDemandBudget(&query)
	if query.DiscoveryQueries != demandMaxDiscoveryQueries ||
		query.VerificationQueries != demandMaxVerificationQueries ||
		query.MaxLeadsEnriched != demandMaxLeadsEnriched || query.MaxCompanies != 48 {
		t.Fatalf("normalized = %+v, want the mode defaults", query)
	}

	deep := models.DemandChainQuery{Activity: "x", DiscoveryQueries: 500, VerificationQueries: 900, MaxLeadsEnriched: 200, MaxCompanies: 99}
	normalizeDemandBudget(&deep)
	// MaxCompanies resets to the default when out of range, like the shipped
	// behaviour; the search budgets clamp to their runaway caps.
	if deep.DiscoveryQueries != 100 || deep.VerificationQueries != 350 || deep.MaxLeadsEnriched != 120 || deep.MaxCompanies != 48 {
		t.Fatalf("capped = %+v, want the runaway caps", deep)
	}
}

func TestDemandChainDecomposesBroadSolventStage(t *testing.T) {
	stages := []models.ValueChainStage{{
		Name:     "Solvent Production",
		Products: []string{"ethyl acetate", "butyl acetate"},
	}}
	lanes := demandChainLanes(models.DemandChainQuery{Activity: "acetic acid"}, stages)
	if !containsDemandLane(lanes, "largest manufacturers of ethyl acetate") ||
		!containsDemandLane(lanes, "largest manufacturers of butyl acetate") {
		t.Fatalf("expected product-level solvent lanes, got %+v", lanes)
	}
}

func containsDemandLane(lanes []demandLane, term string) bool {
	for _, lane := range lanes {
		if lane.term == term {
			return true
		}
	}
	return false
}

// TestDemandExtractionRejectsTitlesAndCountries pins the two deterministic
// extraction guards: navigation titles collapse to their longest segment, and
// a candidate whose name equals its own country is a geography, not a buyer.
func TestDemandExtractionRejectsTitlesAndCountries(t *testing.T) {
	llm := &demandLLM{
		stages: []models.ValueChainStage{{Name: "biodiesel production", Operators: []string{"biodiesel plant"}}},
		candidates: []models.MarketLandscapeLeadCandidate{
			{Name: "Butyl Acrylate | Products | Mitsubishi Chemical Corporation", Country: ""},
			{Name: "Philippines", Country: "Philippines"},
			{Name: "GreenDiesel Sdn Bhd", Country: "Malaysia"},
		},
	}
	searcher := &demandSearcher{hits: map[string][]models.SearchResult{
		"biodiesel plant": {{
			Title: "Used cooking oil biodiesel plant in Malaysia", Link: "https://greendiesel.com.my/plant",
			Snippet: "Produces biodiesel from used cooking oil feedstock.",
		}},
		// Verification hits so the cleaned candidates can reach the results:
		// the per-company evidence gate picks the hit that names each one.
		"official site": []models.SearchResult{
			{Title: "Mitsubishi Chemical Corporation — Butyl Acrylate", Link: "https://www.mitsubishichemical.com/butanol", Snippet: "Butanol is a key raw material for our butyl acrylate production."},
			{Title: "GreenDiesel Sdn Bhd | Biodiesel Plant", Link: "https://www.greendiesel.com.my/plant", Snippet: "We use used cooking oil as feedstock."},
		},
	}}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, searcher))
	report, err := agent.Research(context.Background(), "r", models.DemandChainQuery{Activity: "used cooking oil"}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}

	names := make([]string, 0, len(report.Results))
	for _, result := range report.Results {
		names = append(names, result.Name)
	}
	joined := strings.Join(names, "; ")
	if !strings.Contains(joined, "Mitsubishi Chemical Corporation") || strings.Contains(joined, "|") {
		t.Errorf("the page title must collapse to the company segment, got %q", joined)
	}
	if strings.Contains(joined, "Philippines") {
		t.Errorf("a bare country name must be rejected as a company, got %q", joined)
	}
	if !strings.Contains(joined, "GreenDiesel Sdn Bhd") {
		t.Errorf("the plausible candidate must survive, got %q", joined)
	}
}

// TestDemandChainGroupsStageCandidates keeps the stage roster honest: every
// company the operator lanes surfaced stays visible under its consuming stage
// (verified or not), and demand-lane companies get their own bucket rather
// than being dropped at assembly.
func TestDemandChainGroupsStageCandidates(t *testing.T) {
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
		"biodiesel plant": []models.SearchResult{
			{
				Title: "Used cooking oil biodiesel plant in Malaysia", Link: "https://greendiesel.com.my/plant",
				Snippet: "Produces biodiesel from used cooking oil feedstock.",
			},
			{
				// A second stage-relevant lead that never reaches verification:
				// the roster must keep it honestly as a lead.
				Title: "Biodiesel plant licensing in India", Link: "https://biofuelsindia.example.com/license",
				Snippet: "Licensing a biodiesel plant requires used cooking oil feedstock approvals.",
			},
		},
		"tender RFP": {{
			Title: "Indiachem Solvents | Importer of industrial solvents", Link: "https://indiachem.example.com/import",
			Snippet: "Importer of industrial solvents including used cooking oil derivatives.",
		}},
		"official site": {{
			Title: "GreenDiesel Sdn Bhd | Biodiesel Plant", Link: "https://www.greendiesel.com.my/plant",
			Snippet: "We use used cooking oil as our feedstock.",
		}},
	}}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, searcher))
	report, err := agent.Research(context.Background(), "r", models.DemandChainQuery{Activity: "used cooking oil"}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(report.StageCandidates) == 0 {
		t.Fatal("expected stage candidate sections")
	}
	byName := make(map[string]models.StageCandidates)
	for _, section := range report.StageCandidates {
		byName[section.Stage.Name] = section
	}
	biodiesel, ok := byName["biodiesel production"]
	if !ok {
		t.Fatalf("missing the biodiesel section in %+v", report.StageCandidates)
	}
	if biodiesel.Stage.Rationale == "" {
		t.Error("the section must carry the stage's rationale")
	}
	// Alias-level dedupe: the discovery lead and the verified company are one
	// house, so the roster carries GreenDiesel exactly once — merged and
	// verified. Raw discovery leads stay in the appendix until classified.
	greenDieselCount := 0
	for _, company := range biodiesel.Companies {
		if strings.Contains(company.Name, "GreenDiesel") {
			greenDieselCount++
			if company.Status == "lead" {
				t.Errorf("the merged GreenDiesel entry must carry its verified status, got %q", company.Status)
			}
		}
	}
	if greenDieselCount != 1 {
		t.Errorf("GreenDiesel appears %d time(s) in the roster, want exactly once after dedupe: %+v", greenDieselCount, biodiesel.Companies)
	}
	for _, section := range report.StageCandidates {
		for _, company := range section.Companies {
			if company.Status == "lead" {
				t.Errorf("raw discovery lead %q must not appear in the company roster", company.Name)
			}
		}
	}
	if len(report.Appendix) == 0 {
		t.Error("raw discovery leads must remain auditable in the appendix")
	}
}

// TestDemandSameCompanyRules pins the alias rules and, just as importantly,
// the refusals: conglomerate siblings and differently-named companies must
// never merge.
func TestDemandSameCompanyRules(t *testing.T) {
	aliases := [][2]string{
		{"IndianOil", "Indian Oil Corporation"},
		{"Indian Oil Corporation", "IndianOil"},
		{"Acme Ltd", "Acme"},
		{"Acme", "Acme Ltd"},
		{"S D Fine Chem Limited", "S D Fine Chem"},
		{"Ishwar Chemicals & Gases", "Ishwar Chemicals and Gases"},
	}
	for _, pair := range aliases {
		if !demandSameCompany(pair[0], pair[1]) {
			t.Errorf("expected alias merge for %q / %q", pair[0], pair[1])
		}
	}
	distinct := [][2]string{
		{"Adani", "Adani Ports"},
		{"Adani Ports", "Adani Power"},
		{"Tata Motors", "Tata Steel"},
		{"Punjab Chemicals", "Punjab Chemicals & Crop Protection Limited"},
		{"OQ SAOC", "OQ Chemicals GmbH"},
		{"IndianOil CBO", "Indian Oil Corporation"},
		{"", "Acme"},
	}
	for _, pair := range distinct {
		if demandSameCompany(pair[0], pair[1]) {
			t.Errorf("expected %q / %q to stay separate", pair[0], pair[1])
		}
	}
}

// TestDemandDedupeNeverMergesSharedTenderPages: a tender portal that names
// several bidders is cited by several companies' verification evidence —
// shared URLs are identity only between a discovery lead and the verified
// company extracted from that page, never between two established entries.
func TestDemandDedupeNeverMergesSharedTenderPages(t *testing.T) {
	shared := models.MarketLandscapeEvidence{URL: "https://tenderportal.example.com/uco", Title: "UCO tender", SourceTier: "official"}
	pool := []*models.MarketLandscapeCompany{
		{EntityKey: "company:a", Name: "Alpha Chemicals", Status: "verified",
			Confidence: 0.84, EvidenceType: "company-page", Evidence: []models.MarketLandscapeEvidence{shared}},
		{EntityKey: "company:b", Name: "Beta Solvents Pvt Ltd", Status: "verified",
			Confidence: 0.80, EvidenceType: "company-page", Evidence: []models.MarketLandscapeEvidence{shared}},
	}
	merged := demandDedupeCompanies(pool)
	if len(merged) != 2 {
		t.Fatalf("merged = %d companies (%s), want 2 — shared tender pages are not identity", len(merged), dedupeNames(merged))
	}
}

// TestDemandDedupeCompaniesMergesAliases pins the merge itself: the strongest
// member represents the house with the more complete name on ties, evidence
// unions without duplicates, and genuinely different companies stay separate.
func TestDemandDedupeCompaniesMergesAliases(t *testing.T) {
	evidence := func(url string) models.MarketLandscapeEvidence {
		return models.MarketLandscapeEvidence{URL: url, Title: "evidence", SourceTier: "official"}
	}
	pool := []*models.MarketLandscapeCompany{
		{EntityKey: "company:name:indianoil", Name: "IndianOil", Country: "India", Status: "verified",
			Confidence: 0.98, EvidenceType: "company-page", Evidence: []models.MarketLandscapeEvidence{evidence("https://iocl.com/1")}},
		{EntityKey: "company:name:indian-oil-corp", Name: "Indian Oil Corporation", Status: "corroborated",
			Confidence: 0.98, EvidenceType: "company-page", Evidence: []models.MarketLandscapeEvidence{func() models.MarketLandscapeEvidence {
				e := evidence("https://trade-data.example.com/iocl")
				e.SourceTier = "directory"
				return e
			}()}},
		{EntityKey: "company:name:acme-ltd", Name: "Acme Ltd", Status: "verified",
			Confidence: 0.70, EvidenceType: "company-page", Evidence: []models.MarketLandscapeEvidence{evidence("https://acme.com/1")}},
		{EntityKey: "company:name:acme", Name: "Acme", Status: "lead",
			Confidence: 0.45, EvidenceType: "discovery-lead", Evidence: []models.MarketLandscapeEvidence{evidence("https://acme.com/1")}},
		{EntityKey: "company:name:punjab", Name: "Punjab Chemicals", Status: "lead",
			Confidence: 0.45, EvidenceType: "discovery-lead"},
		{EntityKey: "company:name:punjab-crop", Name: "Punjab Chemicals & Crop Protection Limited", Status: "verified",
			Confidence: 0.78, EvidenceType: "company-page"},
	}
	merged := demandDedupeCompanies(pool)
	if len(merged) != 4 {
		t.Fatalf("merged = %d companies (%s), want 4", len(merged), dedupeNames(merged))
	}
	byName := make(map[string]models.MarketLandscapeCompany)
	for _, rep := range merged {
		byName[rep.Name] = *rep
	}
	iocl, ok := byName["Indian Oil Corporation"]
	if !ok {
		t.Fatalf("expected the more complete name to represent the IndianOil house, got %q", dedupeNames(merged))
	}
	if len(iocl.Evidence) != 2 {
		t.Errorf("IndianOil evidence = %d record(s), want the union of both variants", len(iocl.Evidence))
	}
	if iocl.Status != "corroborated" {
		t.Errorf("IndianOil status = %q, want the strongest verdict", iocl.Status)
	}
	acme, ok := byName["Acme Ltd"]
	if !ok || len(acme.Evidence) != 1 {
		t.Errorf("Acme bucket = %+v, want one representative with deduplicated evidence", acme)
	}
	if byName["Punjab Chemicals"].Name == "" || byName["Punjab Chemicals & Crop Protection Limited"].Name == "" {
		t.Error("Punjab Chemicals and its Crop Protection arm must stay separate")
	}
}

// dedupeNames renders a merged pool for failure messages.
func dedupeNames(companies []*models.MarketLandscapeCompany) string {
	out := make([]string, 0, len(companies))
	for _, company := range companies {
		out = append(out, company.Name)
	}
	return strings.Join(out, "; ")
}

// TestDemandChainLanesCapOperatorVocabularyPerStage keeps the default budget
// frugal (two operator phrases per stage) and lets a deep discovery budget
// widen the vocabulary to three.
func TestDemandChainLanesCapOperatorVocabularyPerStage(t *testing.T) {
	chain := []models.ValueChainStage{{
		Name: "oleochemicals", Operators: []string{"oleochemical plant", "fatty acid plant", "glycerine plant", "surfactant plant"},
	}}
	stageLanes := func(query models.DemandChainQuery) int {
		count := 0
		for _, lane := range demandChainLanes(query, chain) {
			if lane.stage == "oleochemicals" {
				count++
			}
		}
		return count
	}
	if got := stageLanes(models.DemandChainQuery{Activity: "palm oil"}); got != 2 {
		t.Fatalf("default-budget stage lanes = %d, want 2", got)
	}
	if got := stageLanes(models.DemandChainQuery{Activity: "palm oil", DiscoveryQueries: 40}); got != 3 {
		t.Fatalf("deep-budget stage lanes = %d, want 3", got)
	}
}

// TestDemandStageCoverageQC pins the hard floor: every inferred stage and the separate
// distribution category needs at least six identity-passed companies; the general demand-lane
// bucket is not a stage and is excluded.
func TestDemandStageCoverageQC(t *testing.T) {
	makeCompanies := func(name string, count int) []models.MarketLandscapeCompany {
		out := make([]models.MarketLandscapeCompany, 0, count)
		for i := 0; i < count; i++ {
			out = append(out, models.MarketLandscapeCompany{Name: fmt.Sprintf("%s %d", name, i)})
		}
		return out
	}
	sections := []models.StageCandidates{
		{Stage: models.ValueChainStage{Name: "biodiesel production"}, Companies: makeCompanies("stage", 5)},
		{Stage: models.ValueChainStage{Name: demandDistributionStageName, Source: "category"}, Companies: makeCompanies("dist", 6)},
		{Stage: models.ValueChainStage{Name: "General demand lanes", Source: "lanes"}, Companies: makeCompanies("general", 1)},
	}
	coverage := demandStageCoverage(sections)
	if len(coverage) != 2 {
		t.Fatalf("coverage = %+v, want only the stage and distribution category", coverage)
	}
	if coverage[0].Pass || coverage[0].Count != 5 || coverage[0].Minimum != demandMinStageCompanies {
		t.Errorf("stage coverage = %+v, want FAIL at 5/%d", coverage[0], demandMinStageCompanies)
	}
	if !coverage[1].Pass || coverage[1].Count != 6 {
		t.Errorf("distribution coverage = %+v, want PASS at 6", coverage[1])
	}
}

// TestDemandQCRejectsNames: the judge's rejections are applied only to names already in the
// report, the evidence remains auditable, and the review is recorded on the report.
func TestDemandQCRejectsNames(t *testing.T) {
	llm := &demandLLM{
		stages: []models.ValueChainStage{{Name: "biodiesel production", Operators: []string{"biodiesel plant"}}},
		candidates: []models.MarketLandscapeLeadCandidate{
			{Name: "GreenDiesel Sdn Bhd", Country: "Malaysia", Reason: "uses used cooking oil", EvidenceURL: "https://greendiesel.com.my/plant"},
			{Name: "Acme Chemicals", Country: "India", Reason: "uses used cooking oil", EvidenceURL: "https://acmechemicals.com/input"},
		},
		review: models.DemandChainReview{
			Verdict: "revise", Score: 0.6,
			Rejected: []models.DemandRejectedName{{Name: "Acme Chemicals", Reason: "source is a product page, not buyer evidence"}},
			Reason:   "one row rejected",
		},
	}
	searcher := &demandSearcher{hits: map[string][]models.SearchResult{
		"biodiesel plant": {{
			Title: "GreenDiesel Sdn Bhd | Biodiesel Plant", Link: "https://greendiesel.com.my/plant",
			Snippet: "We use used cooking oil as our feedstock.",
		}},
		"buyer procurement purchase": {{
			Title: "Biodiesel buyers and feedstock procurement report", Link: "https://example.com/buyers",
			Snippet: "Acme Chemicals and other buyers purchase used cooking oil feedstock.",
		}},
		"Acme Chemicals": {{
			Title: "Acme Chemicals | Biodiesel input", Link: "https://acmechemicals.com/input",
			Snippet: "Acme Chemicals uses used cooking oil as an input.",
		}},
		"official site": {
			{
				Title: "GreenDiesel Sdn Bhd | Biodiesel Plant", Link: "https://greendiesel.com.my/plant",
				Snippet: "We use used cooking oil as our feedstock.",
			},
			{
				Title: "Acme Chemicals | Biodiesel input", Link: "https://acmechemicals.com/input",
				Snippet: "Acme Chemicals uses used cooking oil as an input.",
			},
		},
	}}
	report, err := NewDemandChainAgent(demandChainTestEngine(llm, searcher)).Research(context.Background(), "qc", models.DemandChainQuery{Activity: "used cooking oil"}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	for _, result := range report.Results {
		if strings.EqualFold(result.Name, "Acme Chemicals") {
			t.Errorf("QC-rejected company still in results: %+v", report.Results)
		}
	}
	for _, section := range report.StageCandidates {
		for _, company := range section.Companies {
			if strings.EqualFold(company.Name, "Acme Chemicals") {
				t.Errorf("QC-rejected company still in stage roster: %+v", section.Companies)
			}
		}
	}
	if len(report.Reviews) != 1 || report.Reviews[0].Verdict != "revise" {
		t.Fatalf("reviews = %+v, want one recorded revise", report.Reviews)
	}
	if len(report.StageCoverage) < 2 {
		t.Errorf("stage coverage = %+v, want the stage and distribution category", report.StageCoverage)
	}
	found := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "Buyer QC removed Acme Chemicals") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a QC removal warning in %v", report.Warnings)
	}
}

// TestDemandReadsResearchPagesForBuyerNames pins the P0 fix: a research/news page is read for
// the companies it names even though the page itself can never be promoted as a buyer.
func TestDemandReadsResearchPagesForBuyerNames(t *testing.T) {
	llm := &demandLLM{
		stages: []models.ValueChainStage{{Name: "biodiesel production", Operators: []string{"biodiesel plant"}}},
		candidates: []models.MarketLandscapeLeadCandidate{{
			Name: "GreenDiesel Sdn Bhd", Country: "Malaysia", EvidenceURL: "https://greendiesel.com.my/plant",
		}},
	}
	searcher := &demandSearcher{hits: map[string][]models.SearchResult{
		"biodiesel plant": {{
			Title: "GreenDiesel Sdn Bhd | Biodiesel Plant", Link: "https://greendiesel.com.my/plant",
			Snippet: "We use used cooking oil as our feedstock.",
		}},
		"buyer procurement purchase": {{
			Title: "Biodiesel buyers report: procurement and feedstock", Link: "https://example.com/biodiesel-buyers",
			Snippet: "The report names GreenDiesel Sdn Bhd among the buyers of used cooking oil.",
		}},
	}}
	scraper := &mockScraper{}
	engine := NewEngine(llm, searcher, scraper, cache.NewMemoryCache(10), 5, 3, 10)
	if _, err := NewDemandChainAgent(engine).Research(context.Background(), "read", models.DemandChainQuery{Activity: "used cooking oil"}, nil); err != nil {
		t.Fatalf("Research: %v", err)
	}
	found := false
	for _, called := range scraper.calledURLs {
		if strings.Contains(called, "biodiesel-buyers") {
			found = true
		}
	}
	if !found {
		t.Fatalf("research page was never read; scraped URLs = %v", scraper.calledURLs)
	}
}

// TestDemandDistributionCategoryIsSeparate: distributors and wholesalers get their own roster
// section instead of being folded into a manufacturing stage or the general-lane bucket.
func TestDemandDistributionCategoryIsSeparate(t *testing.T) {
	if !demandCompanyTitle("Acme Distribution") {
		t.Error("demandCompanyTitle(\"Acme Distribution\") rejected a plausible distributor name")
	}
	pool := []*models.MarketLandscapeCompany{{
		Name: "Acme Distribution Pvt Ltd", Domain: "acmedistribution.com", Stage: demandDistributionStageName,
		EvidenceType: "company-page", Confidence: 0.7,
		Evidence: []models.MarketLandscapeEvidence{{URL: "https://acmedistribution.com", Title: "Acme Distribution", SourceTier: "official"}},
	}}
	sections, _ := buildStageCandidates(pool, []models.ValueChainStage{{Name: "biodiesel production"}})
	var distribution *models.StageCandidates
	for i := range sections {
		if sections[i].Stage.Source == "category" {
			distribution = &sections[i]
		}
	}
	if distribution == nil || distribution.Stage.Name != demandDistributionStageName {
		t.Fatalf("distribution category missing from sections: %+v", sections)
	}
	if len(distribution.Companies) != 1 || distribution.Companies[0].Name != "Acme Distribution Pvt Ltd" {
		t.Fatalf("distribution companies = %+v, want the channel operator", distribution.Companies)
	}
}

// TestDemandStageGapCoverageReachesSix pins the coverage requirement end to end: bounded gap
// searches add enough identity-passed companies for every stage and the separate distribution
// category to clear the QC floor.
func TestDemandStageGapCoverageReachesSix(t *testing.T) {
	llm := &demandLLM{stages: []models.ValueChainStage{{Name: "biodiesel production", Operators: []string{"biodiesel plant"}}}}
	stageHits := make([]models.SearchResult, 0, 6)
	distributionHits := make([]models.SearchResult, 0, 6)
	for i := 1; i <= 6; i++ {
		stageHits = append(stageHits, models.SearchResult{
			Title: fmt.Sprintf("Acme%d Chemicals Pvt Ltd", i), Link: fmt.Sprintf("https://acme%d.com", i),
			Snippet: "The plant consumes used cooking oil as feedstock.",
		})
		distributionHits = append(distributionHits, models.SearchResult{
			Title: fmt.Sprintf("Delta%d Distribution Pvt Ltd", i), Link: fmt.Sprintf("https://delta%d.com", i),
			Snippet: "Distributor of used cooking oil and related products.",
		})
	}
	searcher := &demandSearcher{hits: map[string][]models.SearchResult{
		"biodiesel plant":                   stageHits,
		"biodiesel production manufacturer": stageHits,
		"distributor OR wholesaler of":      distributionHits,
	}}
	report, err := NewDemandChainAgent(demandChainTestEngine(llm, searcher)).Research(context.Background(), "coverage", models.DemandChainQuery{Activity: "used cooking oil"}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	byStage := make(map[string]int)
	for _, section := range report.StageCandidates {
		byStage[section.Stage.Name] = len(section.Companies)
	}
	if byStage["biodiesel production"] < demandMinStageCompanies {
		t.Errorf("biodiesel production coverage = %d, want at least %d: %+v", byStage["biodiesel production"], demandMinStageCompanies, report.StageCandidates)
	}
	if byStage[demandDistributionStageName] < demandMinStageCompanies {
		t.Errorf("distribution coverage = %d, want at least %d: %+v", byStage[demandDistributionStageName], demandMinStageCompanies, report.StageCandidates)
	}
	for _, item := range report.StageCoverage {
		if !item.Pass {
			t.Errorf("stage coverage failed: %+v (searches %d)", item, report.GapQueries)
		}
	}
}

// TestDemandStatusDirectoryOnlyIsLead: a directory/trade hit is corroboration, never a
// verification. Adding the company's own page plus the tender record reaches verified.
func TestDemandStatusDirectoryOnlyIsLead(t *testing.T) {
	directoryOnly := &models.MarketLandscapeCompany{Name: "Acme Chemicals", Evidence: []models.MarketLandscapeEvidence{{
		URL: "https://tradeindia.com/acme", SourceTier: "directory", ClaimType: models.DemandClaimTender,
	}}}
	if status := demandStatus(directoryOnly); status != "lead" {
		t.Errorf("directory-only status = %q, want lead", status)
	}
	withOwnPage := &models.MarketLandscapeCompany{Name: "Acme Chemicals", Evidence: []models.MarketLandscapeEvidence{
		{URL: "https://tradeindia.com/acme", SourceTier: "directory", ClaimType: models.DemandClaimTender},
		{URL: "https://acmechemicals.com/input", SourceTier: "official", ClaimType: models.DemandClaimUsage},
	}}
	if status := demandStatus(withOwnPage); status != "verified" {
		t.Errorf("owned-page plus transaction status = %q, want verified", status)
	}
}

// TestDemandStageForFollowUp keeps judge follow-ups anchored: a query naming a stage or channel
// runs against that roster; an unmapped query is refused rather than dumped into a general bucket.
func TestDemandStageForFollowUp(t *testing.T) {
	chain := []models.ValueChainStage{{Name: "biodiesel production", Operators: []string{"biodiesel plant"}}}
	if got := demandStageForFollowUp("biodiesel plant buyers in Malaysia", chain); got != "biodiesel production" {
		t.Errorf("stage follow-up = %q", got)
	}
	if got := demandStageForFollowUp("used cooking oil distributors in India", chain); got != demandDistributionStageName {
		t.Errorf("distribution follow-up = %q", got)
	}
	if got := demandStageForFollowUp("used cooking oil market overview", []models.ValueChainStage{{Name: "stage one"}, {Name: "stage two"}}); got != "" {
		t.Errorf("unmapped follow-up = %q, want empty", got)
	}
}

// TestDemandCandidateUsesCanonicalCompanySegment: a company page title often puts its capability
// tagline in the longest pipe segment; the roster must keep the brand, not the descriptor.
func TestDemandCandidateUsesCanonicalCompanySegment(t *testing.T) {
	hit := models.SearchResult{
		Title: "Acme Foam | Polyurethane Foam Manufacturer", Link: "https://acmefoam.com",
		Snippet: "Acme Foam purchases TDI for flexible foam production.",
	}
	candidate := demandCandidate(hit, demandLane{term: "foam manufacturer", stage: "Polyurethane Foam Manufacturing"}, models.DemandChainQuery{Activity: "Toluene Diisocyanate"})
	if candidate == nil {
		t.Fatal("candidate was nil")
	}
	if candidate.Name != "Acme Foam" {
		t.Errorf("candidate name = %q, want the brand segment not the descriptor", candidate.Name)
	}
}

// TestDemandStageGapTermsPrefersOperatorVocabulary: the gap search must use the chain's operator
// phrases (which find companies) before the stage-name-plus-manufacturer fallback (which mostly
// finds directories and listicles).
func TestDemandStageGapTermsPrefersOperatorVocabulary(t *testing.T) {
	chain := []models.ValueChainStage{{
		Name:      "Polyurethane Foam Manufacturing",
		Operators: []string{"flexible foam manufacturers", "foam producers"},
	}}
	terms := demandStageGapTerms(models.DemandChainQuery{Activity: "Toluene Diisocyanate"}, chain[0].Name, chain)
	if len(terms) < 2 || terms[0] != "flexible foam manufacturers" {
		t.Fatalf("gap terms = %v, want operator vocabulary first", terms)
	}
	foundLabel := false
	for _, term := range terms {
		if strings.Contains(term, "polyurethane foam manufacturer") {
			foundLabel = true
		}
	}
	if !foundLabel {
		t.Errorf("gap terms = %v, want a normalized stage-label fallback", terms)
	}
}

// gapRosterScraper returns a roster-shaped page for the stage-gap reader test.
type gapRosterScraper struct {
	mu      sync.Mutex
	called  []string
	content string
}

func (s *gapRosterScraper) ScrapeURL(ctx context.Context, targetURL string) (*models.ScrapeResult, error) {
	s.mu.Lock()
	s.called = append(s.called, targetURL)
	content := s.content
	s.mu.Unlock()
	return &models.ScrapeResult{URL: targetURL, Title: "Polyurethane foam manufacturers", Content: content}, nil
}

// TestDemandGapReadsListicleAndExtractsCompanies pins the listicle path: a gap hit that is a
// roster page is read, and the companies it names are added as stage-roster candidates rather
// than the page title becoming the company.
func TestDemandGapReadsListicleAndExtractsCompanies(t *testing.T) {
	stageName := "Polyurethane Foam Manufacturing"
	pageURL := "https://example.com/gap-roster"
	gapNames := []models.MarketLandscapeLeadCandidate{
		{Name: "Acme Foam", Country: "India", EvidenceURL: pageURL},
		{Name: "Bharat Foam Works", Country: "India", EvidenceURL: pageURL},
		{Name: "Chennai Flexible Foam", Country: "India", EvidenceURL: pageURL},
		{Name: "Deccan Foam Products", Country: "India", EvidenceURL: pageURL},
		{Name: "Eastern Foam Manufacturing", Country: "India", EvidenceURL: pageURL},
		{Name: "Faridabad Foam Industries", Country: "India", EvidenceURL: pageURL},
	}
	llm := &demandLLM{gapCandidates: gapNames}
	searcher := &demandSearcher{hits: map[string][]models.SearchResult{
		"flexible foam manufacturers": {{
			Title: "Top polyurethane foam manufacturers in India", Link: pageURL,
			Snippet: "Acme Foam, Bharat Foam Works and Chennai Flexible Foam are listed.",
		}},
	}}
	scraper := &gapRosterScraper{content: "Acme Foam; Bharat Foam Works; Chennai Flexible Foam; Deccan Foam Products; Eastern Foam Manufacturing; Faridabad Foam Industries"}
	engine := NewEngine(llm, searcher, scraper, cache.NewMemoryCache(10), 5, 3, 10)
	report := &models.DemandChainReport{Chain: []models.ValueChainStage{{Name: stageName, Operators: []string{"flexible foam manufacturers"}}}}
	companies := make(map[string]*models.MarketLandscapeCompany)
	coverage := []models.DemandStageCoverage{{Stage: stageName, Count: 0, Minimum: demandMinStageCompanies, Pass: false}}
	added := NewDemandChainAgent(engine).fillDemandStageGaps(context.Background(), models.DemandChainQuery{Activity: "Toluene Diisocyanate"}, coverage, companies, report, make(chan models.AgentEvent, 32))
	if added < len(gapNames) {
		t.Fatalf("gap fill added %d company(ies), want at least %d: %+v", added, len(gapNames), companies)
	}
	if report.PagesEnriched == 0 || len(scraper.called) == 0 || scraper.called[0] != pageURL {
		t.Errorf("gap roster page was not read: pages=%d called=%v", report.PagesEnriched, scraper.called)
	}
	found := 0
	for _, company := range companies {
		if company.Stage == stageName && company.EvidenceType == "roster-extracted" {
			found++
		}
	}
	if found < len(gapNames) {
		t.Errorf("roster-extracted companies = %d, want at least %d: %+v", found, len(gapNames), companies)
	}
	pool := make([]*models.MarketLandscapeCompany, 0, len(companies))
	for _, company := range companies {
		pool = append(pool, company)
	}
	sections, _ := buildStageCandidates(pool, report.Chain)
	coverage = demandStageCoverage(sections)
	if len(coverage) == 0 || !coverage[0].Pass {
		t.Errorf("stage coverage = %+v, want a passing %s roster", coverage, stageName)
	}
}

// TestDemandCategoryValidation pins the anti-overbreadth gate: a category must be a specific
// buyer-side product family related to the product/stages. Coal may be "coal products", never
// "fuel"; TDI may be "polyurethanes", never "chemicals".
func TestDemandCategoryValidation(t *testing.T) {
	polyStages := []models.ValueChainStage{{
		Name: "Polyurethane Foam Manufacturing", Products: []string{"flexible polyurethane foam"},
		Operators: []string{"foam manufacturers"},
	}}
	solventStages := []models.ValueChainStage{{Name: "Solvent Production", Products: []string{"ethyl acetate"}}}
	coalStages := []models.ValueChainStage{{Name: "Coal Combustion", Operators: []string{"coal plant"}}}
	valid := []struct {
		activity, category string
		stages             []models.ValueChainStage
	}{
		{"Toluene Diisocyanate", "polyurethanes", polyStages},
		{"acetic acid", "solvents", solventStages},
		{"coal", "coal products", coalStages},
	}
	for _, item := range valid {
		category, source, warnings := validateDemandCategory(item.category, "", item.activity, item.stages)
		if category != item.category || source != "model" || len(warnings) != 0 {
			t.Errorf("validate(%q, %q) = %q/%q/%v, want the model category", item.activity, item.category, category, source, warnings)
		}
	}
	invalid := []struct {
		activity, category string
		stages             []models.ValueChainStage
	}{
		{"coal", "fuel", coalStages},
		{"coal", "energy", coalStages},
		{"Toluene Diisocyanate", "chemicals", polyStages},
		{"Toluene Diisocyanate", "automotive", polyStages},
		{"Toluene Diisocyanate", "polyurethanes manufacturing", polyStages},
		{"Toluene Diisocyanate", "unknown unrelated category", polyStages},
	}
	for _, item := range invalid {
		category, source, warnings := validateDemandCategory(item.category, "", item.activity, item.stages)
		if category != "" || source != "rejected" || len(warnings) == 0 {
			t.Errorf("validate(%q, %q) = %q/%q/%v, want rejection", item.activity, item.category, category, source, warnings)
		}
	}
}

// TestDemandStageBroadenTermsUsesProductCategory: the first-pass category is the first fallback
// query dimension, but only after it has passed the validation gate.
func TestDemandStageBroadenTermsUsesProductCategory(t *testing.T) {
	terms := demandStageBroadenTerms(models.DemandChainQuery{Activity: "Toluene Diisocyanate"}, "Polyurethane Foam Manufacturing", nil, "aromatics")
	if len(terms) < 2 || terms[0] != "aromatics manufacturers" || terms[1] != "aromatics companies" {
		t.Fatalf("broaden terms = %v, want the validated product category first", terms)
	}
}

// TestDemandInferenceCategoryIsValidated: the first-pass category travels with the chain but only
// when it clears the same deterministic gate used by the fallback search.
func TestDemandInferenceCategoryIsValidated(t *testing.T) {
	llm := &demandLLM{
		category: "polyurethanes",
		stages:   []models.ValueChainStage{{Name: "Polyurethane Foam Manufacturing", Operators: []string{"foam manufacturers"}}},
	}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, &demandSearcher{}))
	chain, category, rationale, source, inferred, warnings := agent.inferDemandChainWithCategory(context.Background(), models.DemandChainQuery{Activity: "Toluene Diisocyanate"}, nil)
	if !inferred || len(chain) != 1 {
		t.Fatalf("chain=%+v inferred=%v", chain, inferred)
	}
	if category != "polyurethanes" || source != "model" || rationale == "" {
		t.Errorf("category=%q source=%q rationale=%q warnings=%v", category, source, rationale, warnings)
	}
}

func TestDemandInferenceRejectsOverbroadCategory(t *testing.T) {
	llm := &demandLLM{
		category: "fuel",
		stages:   []models.ValueChainStage{{Name: "Coal Combustion", Operators: []string{"coal plant"}}},
	}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, &demandSearcher{}))
	_, category, _, source, _, warnings := agent.inferDemandChainWithCategory(context.Background(), models.DemandChainQuery{Activity: "coal"}, nil)
	if category == "fuel" || source != "fallback" {
		t.Errorf("category=%q source=%q, want a rejected/fallback category", category, source)
	}
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning, "rejected") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings=%v, want a category rejection", warnings)
	}
}

// TestDemandAdversarialChainReviewRejectsWrongDirectStage: a mattress stage claimed as a direct
// TDI consumer must not survive as distance 1 just because the model offered it.
func TestDemandAdversarialChainReviewRejectsWrongDirectStage(t *testing.T) {
	llm := &demandLLM{chainReview: models.ValueChainReview{
		Verdict: "revise", Score: 0.8,
		RejectedStages: []models.DemandStageRejection{{Name: "Mattress Manufacturing", Reason: "consumes foam, not TDI directly"}},
	}}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, &demandSearcher{}))
	chain := []models.ValueChainStage{
		{Name: "Polyurethane Foam Manufacturing", Distance: models.StageDistanceDirect},
		{Name: "Mattress Manufacturing", Distance: models.StageDistanceDirect},
	}
	reviewed, review, warnings := agent.reviewDemandChain(context.Background(), models.DemandChainQuery{Activity: "Toluene Diisocyanate"}, chain, nil)
	if review == nil || review.Verdict != "revise" {
		t.Fatalf("review = %+v, want revise", review)
	}
	if len(reviewed) != 1 || reviewed[0].Name != "Polyurethane Foam Manufacturing" {
		t.Errorf("reviewed chain = %+v, want the direct foam stage only", reviewed)
	}
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning, "Mattress Manufacturing") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want the rejected mattress stage", warnings)
	}
}

// TestDemandAdversarialChainReviewCorrectsDistance: a mattress plant can remain as a derived stage
// when the judge corrects it to distance 2 rather than rejecting it.
func TestDemandAdversarialChainReviewCorrectsDistance(t *testing.T) {
	llm := &demandLLM{chainReview: models.ValueChainReview{
		Verdict: "revise", Score: 0.7,
		DistanceCorrections: []models.DemandDistanceCorrection{{Name: "Mattress Manufacturing", Distance: models.StageDistanceDerived}},
	}}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, &demandSearcher{}))
	chain := []models.ValueChainStage{{Name: "Mattress Manufacturing", Distance: models.StageDistanceDirect}}
	reviewed, _, _ := agent.reviewDemandChain(context.Background(), models.DemandChainQuery{Activity: "Toluene Diisocyanate"}, chain, nil)
	if len(reviewed) != 1 || reviewed[0].Distance != models.StageDistanceDerived {
		t.Errorf("reviewed chain = %+v, want the mattress stage corrected to distance 2", reviewed)
	}
}

// TestDemandEvalLeaksAreRejected pins the false positives the live TDI eval exposed after the
// first QC pass: product/report phrases, article headlines, agencies and category list titles.
func TestDemandEvalLeaksAreRejected(t *testing.T) {
	for _, name := range []string{
		"China TDI Foam Chemical Manufacturers Suppliers Factory",
		"Toluene diisocyanates (TDI), TDA and DNT REACH",
		"Covestro pioneers the supply of renewable TDI",
		"Federal Transit Administration",
		"PASSPort",
		"Customs Ruling NY N208431",
		"PFA Members",
		"Wholesale Central",
	} {
		if demandCompanyTitleFor(name, "Toluene Diisocyanate") {
			t.Errorf("demandCompanyTitleFor(%q) admitted a leaked non-company", name)
		}
	}
}
