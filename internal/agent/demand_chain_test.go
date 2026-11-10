package agent

import (
	"context"
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
	candidates    []models.MarketLandscapeLeadCandidate
	people        []models.MarketLandscapePersonCandidate
	err           error
	systemPrompts []string
}

func (m *demandLLM) GenerateStructured(ctx context.Context, systemPrompt, userPrompt string, target any) error {
	m.mu.Lock()
	m.systemPrompts = append(m.systemPrompts, systemPrompt)
	err := m.err
	stages, candidates, people := m.stages, m.candidates, m.people
	m.mu.Unlock()
	if err != nil {
		return err
	}
	switch typed := target.(type) {
	case *[]models.ValueChainStage:
		*typed = append(*typed, stages...)
	case *[]models.MarketLandscapeLeadCandidate:
		*typed = append(*typed, candidates...)
	case *[]models.MarketLandscapePersonCandidate:
		*typed = append(*typed, people...)
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
	for _, expected := range []string{"biodiesel production companies", "biodiesel plant", "buyer procurement purchase", "tender RFP e-procurement", "importer import data", "raw material feedstock consumption", "industrial applications manufacturers", "distributor wholesaler of", "Malaysia importer tender procurement"} {
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
	if defaults[0] != "biodiesel production companies" {
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
		query.MaxLeadsEnriched != demandMaxLeadsEnriched || query.MaxCompanies != 20 {
		t.Fatalf("normalized = %+v, want the mode defaults", query)
	}

	deep := models.DemandChainQuery{Activity: "x", DiscoveryQueries: 500, VerificationQueries: 900, MaxLeadsEnriched: 200, MaxCompanies: 99}
	normalizeDemandBudget(&deep)
	// MaxCompanies resets to the default when out of range, like the shipped
	// behaviour; the search budgets clamp to their runaway caps.
	if deep.DiscoveryQueries != 60 || deep.VerificationQueries != 250 || deep.MaxLeadsEnriched != 60 || deep.MaxCompanies != 20 {
		t.Fatalf("capped = %+v, want the runaway caps", deep)
	}
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
	// house, so the roster carries GreenDiesel exactly once — merged, verified
	// — and keeps the never-verified licensing lead honestly as a lead.
	greenDieselCount, leadCount := 0, 0
	for _, company := range biodiesel.Companies {
		if strings.Contains(company.Name, "GreenDiesel") {
			greenDieselCount++
			if company.Status == "lead" {
				t.Errorf("the merged GreenDiesel entry must carry its verified status, got %q", company.Status)
			}
		}
		if company.Status == "lead" {
			leadCount++
		}
	}
	if greenDieselCount != 1 {
		t.Errorf("GreenDiesel appears %d time(s) in the roster, want exactly once after dedupe: %+v", greenDieselCount, biodiesel.Companies)
	}
	if leadCount == 0 {
		t.Errorf("the roster must keep unverified leads, got %+v", biodiesel.Companies)
	}
	general, ok := byName["General demand lanes"]
	if !ok {
		t.Fatalf("missing the demand-lane bucket in %+v", report.StageCandidates)
	}
	if len(general.Companies) == 0 {
		t.Error("the demand-lane bucket must keep its companies")
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

// TestDemandPeopleDiscoveryUsesProcurementRoles pins the buyer-side people
// phase: the search carries the procurement role vocabulary (not the seller's
// sales roles), extracted people land on the verified buyer with their source,
// and the RocketReach path reports itself unavailable without a client.
func TestDemandPeopleDiscoveryUsesProcurementRoles(t *testing.T) {
	var webQueries []string
	searcher := &queryRecordingSearcher{hits: map[string][]models.SearchResult{
		"procurement": {{
			Title: "GreenDiesel Sdn Bhd appoints new procurement head", Link: "https://news.example.com/greendiesel-procurement",
			Snippet: "GreenDiesel Sdn Bhd named Asha Rao as head of procurement and purchasing.",
		}},
	}}
	searcher.recorder = &webQueries
	llm := &demandLLM{people: []models.MarketLandscapePersonCandidate{{Name: "Asha Rao"}}}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, searcher))

	companies := []*models.MarketLandscapeCompany{{
		EntityKey: "company:name:greendiesel", Name: "GreenDiesel Sdn Bhd",
		Status: "verified", Confidence: 0.84, EvidenceType: "company-page",
	}}
	used := agent.discoverDemandPeople(context.Background(), companies, demandPeopleQueryReserve/2, nil)
	if used != 1 {
		t.Fatalf("people searches = %d, want 1", used)
	}
	joined := strings.Join(webQueries, " | ")
	for _, role := range []string{"procurement", "purchasing", "sourcing", "supply chain"} {
		if !strings.Contains(strings.ToLower(joined), role) {
			t.Errorf("people search %q is missing the buyer role %q", joined, role)
		}
	}
	if strings.Contains(strings.ToLower(joined), "sales") {
		t.Errorf("the seller-side sales vocabulary leaked into a buyer run: %q", joined)
	}
	if len(companies[0].KeyPeople) != 1 || companies[0].KeyPeople[0] != "Asha Rao" {
		t.Fatalf("key people = %+v, want the extracted procurement contact", companies[0].KeyPeople)
	}
	if companies[0].KeyPeopleSources["Asha Rao"] != "web search" {
		t.Errorf("source = %q, want web search", companies[0].KeyPeopleSources["Asha Rao"])
	}
}

// queryRecordingSearcher wraps a hit table and records every query, so tests
// can pin the role vocabulary the people phase searches with.
type queryRecordingSearcher struct {
	hits     map[string][]models.SearchResult
	recorder *[]string
}

func (m *queryRecordingSearcher) Search(ctx context.Context, query string, maxResults int) ([]models.SearchResult, error) {
	*m.recorder = append(*m.recorder, query)
	for fragment, hits := range m.hits {
		if strings.Contains(strings.ToLower(query), strings.ToLower(fragment)) {
			return hits, nil
		}
	}
	return nil, nil
}

// bidirectional and keeps unknown countries rather than guessing.
func TestDemandCountryMatchesKeepsTheRunInsideItsGeography(t *testing.T) {
	countries := []string{"India"}
	if !demandCountryMatches("India", countries) || !demandCountryMatches("INDIA", countries) ||
		!demandCountryMatches("Republic of India", countries) || !demandCountryMatches("", countries) {
		t.Error("Indian variants and unknown countries must be kept")
	}
	if demandCountryMatches("Japan", countries) || demandCountryMatches("Germany", countries) {
		t.Error("out-of-geography countries must not match")
	}
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
