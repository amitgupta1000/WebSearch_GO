package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/ontology"
)

// aliasLLM scripts the ontology alias probe and the ambiguous-family choice; it
// counts alias calls so the graph-match branch can assert it spends none.
type aliasLLM struct {
	matchID    string
	confidence string
	rationale  string
	familyID   string
	aliasCalls int
}

func (m *aliasLLM) GenerateStructured(_ context.Context, _, _ string, target any) error {
	if typed, ok := target.(*ontologyAliasProbe); ok {
		m.aliasCalls++
		typed.MatchID = m.matchID
		typed.Confidence = m.confidence
		typed.Rationale = m.rationale
	}
	if typed, ok := target.(*familyChoice); ok {
		typed.FamilyID = m.familyID
	}
	return nil
}

func aliasEngine(llm *aliasLLM) *Engine {
	return demandChainTestEngine(llm, &demandSearcher{})
}

// TestResolveActivityGraphMatch pins branch (1): a modelled activity resolves
// deterministically, with no alias call.
func TestResolveActivityGraphMatch(t *testing.T) {
	llm := &aliasLLM{}
	resolution := resolveActivity(context.Background(), aliasEngine(llm), "oil blending", nil)
	if resolution.Path != pathOntology {
		t.Fatalf("path = %q, want ontology", resolution.Path)
	}
	if len(resolution.Graph.Families) == 0 || resolution.Graph.Families[0].ID != "cat:lubricants" {
		t.Fatalf("families = %+v, want lubricants first", resolution.Graph.Families)
	}
	if resolution.primaryCategory() != "Lubricants" {
		t.Errorf("primary category = %q", resolution.primaryCategory())
	}
	if llm.aliasCalls != 0 {
		t.Errorf("a graph match must not spend an alias call, got %d", llm.aliasCalls)
	}
	if len(resolution.Warnings) != 0 {
		t.Errorf("warnings = %v, want none for a clean match", resolution.Warnings)
	}
}

// TestResolveActivityAliasBranch pins branch (2.1): an unknown wording that the
// model maps onto an approved node proceeds as a graph match, and the report says
// what happened so curation can add the alias. "metalworking fluid blending" is
// not a graph entry verbatim (the resolver needs the node's own wording), but its
// value chain is the graph's metalworking-fluids node.
func TestResolveActivityAliasBranch(t *testing.T) {
	llm := &aliasLLM{matchID: "end:metalworking-fluids", confidence: "high", rationale: "the activity produces this end product"}
	resolution := resolveActivity(context.Background(), aliasEngine(llm), "metalworking fluid blending", nil)
	if resolution.Path != pathAlias {
		t.Fatalf("path = %q, want alias (warnings %v)", resolution.Path, resolution.Warnings)
	}
	if resolution.NodeID != "end:metalworking-fluids" {
		t.Errorf("node = %q, want the alias target", resolution.NodeID)
	}
	if llm.aliasCalls != 1 {
		t.Errorf("alias calls = %d, want exactly one probe", llm.aliasCalls)
	}
	if len(resolution.Graph.Families) == 0 {
		t.Error("an alias must resolve the matched node's value chain")
	}
	joined := strings.Join(resolution.Warnings, " ")
	if !strings.Contains(joined, "alias") || !strings.Contains(joined, "end:metalworking-fluids") {
		t.Errorf("warnings = %v, want the alias audit line", resolution.Warnings)
	}
}

// TestResolveActivityRejectsWeakAliases pins the precision guards: no id, an
// unknown id, and low confidence all fall through to (2.2).
func TestResolveActivityRejectsWeakAliases(t *testing.T) {
	cases := []aliasLLM{
		{matchID: "", confidence: "high"},
		{matchID: "cat:not-a-node", confidence: "high"},
		{matchID: "cat:lubricants", confidence: "low"},
		{matchID: "cat:lubricants", confidence: ""},
	}
	for _, probe := range cases {
		llm := probe
		resolution := resolveActivity(context.Background(), aliasEngine(&llm), "metalworking fluid blending", nil)
		if resolution.Path != pathUnmodelled {
			t.Errorf("%+v: path = %q, want unmodelled", probe, resolution.Path)
		}
	}
}

// TestResolveActivityUnmodelledBranch pins (2.2): nothing applies, the path is
// model-derived and the activity is flagged for curation.
func TestResolveActivityUnmodelledBranch(t *testing.T) {
	llm := &aliasLLM{}
	resolution := resolveActivity(context.Background(), aliasEngine(llm), "unobtainium powder", nil)
	if resolution.Path != pathUnmodelled {
		t.Fatalf("path = %q, want unmodelled", resolution.Path)
	}
	if len(resolution.Graph.Families) != 0 {
		t.Errorf("families = %+v, want none", resolution.Graph.Families)
	}
	if !strings.Contains(strings.Join(resolution.Warnings, " "), "unmodelled") {
		t.Errorf("warnings = %v, want the unmodelled notice", resolution.Warnings)
	}
}

// TestAliasCandidatesStayBoundedAndStructural pins the alias prompt budget: the
// structural backbone is always offered, and the scored extras are capped.
func TestAliasCandidatesStayBoundedAndStructural(t *testing.T) {
	document, err := ontology.Shared()
	if err != nil {
		t.Fatalf("ontology: %v", err)
	}
	candidates := ontologyAliasCandidates(document, "oil blending", 5)
	if len(candidates) == 0 {
		t.Fatal("no alias candidates")
	}
	joined := strings.Join(candidates, "\n")
	if !strings.Contains(joined, "cat:lubricants") || !strings.Contains(joined, "proc:blending") {
		t.Errorf("candidates = %v, want the structural families/processes", candidates)
	}
	if !strings.Contains(joined, "sub:base-oil") {
		t.Errorf("candidates = %v, want the token-matched substances", candidates)
	}
	all := ontologyAliasCandidates(document, "oil blending", 25)
	if len(all) <= len(candidates) {
		t.Errorf("scored budget must grow the list: %d vs %d", len(all), len(candidates))
	}
}

// TestChooseOntologyFamilyUsesTheRunContext pins the ambiguity guidance: the model
// may pick a lower-ranked variant, but only from the listed ids.
func TestChooseOntologyFamilyUsesTheRunContext(t *testing.T) {
	resolution := resolveActivity(context.Background(), aliasEngine(&aliasLLM{}), "oil blending", nil)
	if resolution.primaryFamilyID() != "cat:lubricants" {
		t.Fatalf("precondition: primary = %q", resolution.primaryFamilyID())
	}
	chosen := chooseOntologyFamily(context.Background(), aliasEngine(&aliasLLM{familyID: "cat:fuel-oils"}), resolution, "Middle East", nil)
	if chosen.primaryFamilyID() != "cat:fuel-oils" {
		t.Errorf("primary = %q, want the model's choice", chosen.primaryFamilyID())
	}
	if !strings.Contains(strings.Join(chosen.Warnings, " "), "Graph ambiguity resolved") {
		t.Errorf("warnings = %v, want the variant audit line", chosen.Warnings)
	}
	// An id outside the list changes nothing.
	unchanged := chooseOntologyFamily(context.Background(), aliasEngine(&aliasLLM{familyID: "cat:soap"}), resolution, "Middle East", nil)
	if unchanged.primaryFamilyID() != "cat:lubricants" {
		t.Errorf("primary = %q, want the graph's rank order preserved", unchanged.primaryFamilyID())
	}
}

// TestDemandStagesFromOntologyCarriesGraphVocabulary pins the buyer-side mapping:
// derivative products become production stages with operator vocabulary, and the
// activity's own process never becomes its own buyer.
func TestDemandStagesFromOntologyCarriesGraphVocabulary(t *testing.T) {
	resolution := resolveActivity(context.Background(), aliasEngine(&aliasLLM{}), "n-butanol", nil)
	stages := demandStagesFromOntology(resolution.document, resolution)
	if len(stages) == 0 {
		t.Fatal("no stages from a modelled product")
	}
	found := map[string]models.ValueChainStage{}
	for _, stage := range stages {
		found[strings.ToLower(stage.Name)] = stage
	}
	acrylate, ok := found["butyl acrylate production"]
	if !ok {
		t.Fatalf("stages = %v, want the derivative production stage", stageNames(stages))
	}
	if acrylate.Distance != models.StageDistanceDirect || acrylate.Source != "ontology" {
		t.Errorf("acrylate stage = %+v, want a direct ontology stage", acrylate)
	}
	if len(acrylate.Operators) == 0 || len(acrylate.Products) == 0 {
		t.Errorf("acrylate stage = %+v, want operator and product vocabulary", acrylate)
	}
}

// TestMarketDiscoverySearchesUseGraphTerms pins the seller wiring: the graph's
// indexed phrasing joins the discovery lanes, so "oil blending" also searches
// "lubricant blending" without losing the operator's own words.
func TestMarketDiscoverySearchesUseGraphTerms(t *testing.T) {
	resolution := resolveActivity(context.Background(), aliasEngine(&aliasLLM{}), "oil blending", nil)
	query := models.MarketLandscapeQuery{Activity: "oil blending"}
	existing := marketDiscoverySearches(query)
	guided := marketDiscoverySearchesWithTerms(query, resolution.searchTerms())
	if len(guided) <= len(existing) {
		t.Fatalf("guided lanes = %d, unguided = %d, want the graph's extra terms", len(guided), len(existing))
	}
	queries := make([]string, 0, len(guided))
	for _, search := range guided {
		queries = append(queries, strings.ToLower(search.Query))
	}
	joined := strings.Join(queries, " | ")
	if !strings.Contains(joined, "oil blending") {
		t.Errorf("queries = %q, want the operator's own wording kept", joined)
	}
	if !strings.Contains(joined, "lubricant blending") {
		t.Errorf("queries = %q, want the graph's indexed phrasing", joined)
	}
}

// use: the same resolution, with the buyer side answering with the consuming
// family and stages while the seller side answers with the product family.
func TestResolveActivityAPI(t *testing.T) {
	ctx := context.Background()
	seller, err := ResolveActivity(ctx, nil, ActivityQuery{Activity: "n-butanol"})
	if err != nil {
		t.Fatalf("seller: %v", err)
	}
	if seller.Mode != ActivityModeSeller || seller.Category != "Alcohol solvents" || len(seller.Stages) != 0 {
		t.Errorf("seller = %+v, want the product family and no stages", seller)
	}
	buyer, err := ResolveActivity(ctx, nil, ActivityQuery{Activity: "n-butanol", Mode: ActivityMode("demand")})
	if err != nil {
		t.Fatalf("buyer: %v", err)
	}
	if buyer.Mode != ActivityModeBuyer {
		t.Errorf("mode = %q, want buyer", buyer.Mode)
	}
	if buyer.Category == seller.Category || buyer.Category == "" {
		t.Errorf("buyer category = %q, want the consuming family", buyer.Category)
	}
	if len(buyer.Stages) == 0 {
		t.Error("the buyer view must list the consuming stages")
	}
	if _, err := ResolveActivity(ctx, nil, ActivityQuery{Activity: "  "}); err == nil {
		t.Error("an empty activity must be an error")
	}
	unmodelled, err := ResolveActivity(ctx, nil, ActivityQuery{Activity: "unobtainium powder"})
	if err != nil {
		t.Fatalf("unmodelled: %v", err)
	}
	if unmodelled.Path != ActivityPathUnmodelled || unmodelled.Matched || len(unmodelled.CategoryLanes) != 0 {
		t.Errorf("unmodelled = %+v, want the model-planned branch", unmodelled)
	}
}

// TestResolveActivityAPIReportsAliases: with an engine the API also reports the
// model-confirmed alias branch, so tooling can see why a run was graph-guided.
func TestResolveActivityAPIReportsAliases(t *testing.T) {
	llm := &aliasLLM{matchID: "cat:lubricants", confidence: "medium", rationale: "same family"}
	resolution, err := ResolveActivity(context.Background(), aliasEngine(llm), ActivityQuery{Activity: "metalworking fluid blending"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolution.Path != ActivityPathAlias || resolution.Confidence != "medium" {
		t.Fatalf("resolution = %+v, want the alias branch", resolution)
	}
	if resolution.Category != "Lubricants" || resolution.CategorySource != ActivityPathAlias {
		t.Errorf("category = %q/%q, want the alias-mapped family", resolution.Category, resolution.CategorySource)
	}
}

// supply side, so "lubricant blending" must not seed a blending buyer stage while
// its downstream products (greases, metalworking fluids) do.
func TestDemandOntologyGuidanceSkipsSelfProcess(t *testing.T) {
	resolution := resolveActivity(context.Background(), aliasEngine(&aliasLLM{}), "lubricant blending", nil)
	stages := demandStagesFromOntology(resolution.document, resolution)
	if len(stages) == 0 {
		t.Fatal("no downstream stages for lubricant blending")
	}
	guidance := strings.ToLower(demandOntologyGuidance(stages, resolution.familyLabels()))
	if strings.Contains(guidance, "blending (distance") || strings.Contains(guidance, "- blending") {
		t.Errorf("guidance = %q, want no self-process stage", guidance)
	}
	if !strings.Contains(guidance, "greases") {
		t.Errorf("guidance = %q, want the downstream grease stage", guidance)
	}
}
