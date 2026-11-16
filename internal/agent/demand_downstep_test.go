package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// downstepLLM scripts the two GenerateStructured calls a buyer run makes before
// discovery: the value-chain inference, then the intermediate-category
// downstep. Responses are returned in order; a per-call failure flag forces a
// scripted error.
type downstepLLM struct {
	mu        sync.Mutex
	calls     int
	responses []any
	fail      []bool
	prompts   []string
}

func (l *downstepLLM) GenerateStructured(ctx context.Context, systemPrompt, userPrompt string, target any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	l.prompts = append(l.prompts, userPrompt)
	idx := l.calls - 1
	if idx < len(l.fail) && l.fail[idx] {
		return fmt.Errorf("scripted failure")
	}
	if idx >= len(l.responses) {
		return fmt.Errorf("no scripted response for call %d", l.calls)
	}
	data, err := json.Marshal(l.responses[idx])
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// promptForCall returns the user prompt one call received, so tests can assert
// the downstep asked the intermediate-consumer question.
func (l *downstepLLM) promptForCall(call int) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if call-1 < len(l.prompts) {
		return l.prompts[call-1]
	}
	return ""
}

// TestIntermediateCategoryResolvesOneDownstep pins the dead-end fix: for
// n-butanol the validated category "acrylates and acetates" is an intermediate
// product family, so its consumers (polymers/copolymers made from the esters)
// are resolved one step down and enter the chain as distance-2 stages.
func TestIntermediateCategoryResolvesOneDownstep(t *testing.T) {
	llm := &downstepLLM{responses: []any{
		models.DemandChainInference{
			Category: "acrylates and acetates",
			Stages: []models.ValueChainStage{{
				Name: "Acrylate ester production", Distance: models.StageDistanceDirect,
				Rationale: "Esterifies n-butanol with acrylic acid into butyl acrylate.",
			}},
		},
		[]models.ValueChainStage{
			{Name: "Acrylic polymer and copolymer production", Rationale: "Polymerizes butyl acrylate from the acrylate ester family.", Operators: []string{"acrylic polymer manufacturers"}},
			{Name: "Superabsorbent polymer production", Rationale: "Consumes acrylate esters as the monomer feed."},
		},
	}}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, &demandSearcher{}))
	stages, category, _, _, inferred, warnings := agent.inferDemandChainWithCategory(context.Background(), models.DemandChainQuery{Activity: "n-butanol"}, make(chan models.AgentEvent, 16))
	if !inferred || category != "acrylates and acetates" {
		t.Fatalf("category = %q inferred = %t, want the acrylate category", category, inferred)
	}
	llm.mu.Lock()
	calls := llm.calls
	llm.mu.Unlock()
	if calls != 2 {
		t.Fatalf("GenerateStructured calls = %d, want inference plus one downstep", calls)
	}
	if prompt := llm.promptForCall(2); !strings.Contains(prompt, "acrylates and acetates") || !strings.Contains(prompt, "consumed as an input") {
		t.Errorf("downstep prompt = %q, want the intermediate consumer question", prompt)
	}
	found := map[string]models.ValueChainStage{}
	for _, stage := range stages {
		found[strings.ToLower(stage.Name)] = stage
	}
	polymer, ok := found["acrylic polymer and copolymer production"]
	if !ok {
		t.Fatalf("stages = %v, want the downstep consumer in the chain", stageNames(stages))
	}
	if polymer.Distance != models.StageDistanceDerived || polymer.Source != "category" {
		t.Errorf("polymer stage = %+v, want distance 2 sourced from the category", polymer)
	}
	if _, ok := found["superabsorbent polymer production"]; !ok {
		t.Errorf("stages = %v, want the second consumer", stageNames(stages))
	}
	joined := strings.ToLower(strings.Join(warnings, " "))
	if !strings.Contains(joined, "intermediate product family") || !strings.Contains(joined, "acrylic polymer and copolymer production") {
		t.Errorf("warnings = %v, want the downstep audit line", warnings)
	}
	lanes := demandChainLanes(models.DemandChainQuery{Activity: "n-butanol"}, stages)
	foundLane := false
	for _, lane := range lanes {
		if strings.Contains(lane.term, "largest manufacturers of acrylic polymer and copolymer") {
			foundLane = true
		}
	}
	if !foundLane {
		t.Errorf("lanes = %v, want a lane for the downstep stage", lanes)
	}
}

// TestDownstepFallsBackToKnownFamilies covers the scripted-failure path: when
// the downstep call fails, the deterministic consumer families stand in so the
// chain still reaches the intermediate's buyers.
func TestDownstepFallsBackToKnownFamilies(t *testing.T) {
	llm := &downstepLLM{
		responses: []any{
			models.DemandChainInference{
				Category: "acrylates",
				Stages:   []models.ValueChainStage{{Name: "Acrylate ester production", Distance: models.StageDistanceDirect}},
			},
		},
		fail: []bool{false, true},
	}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, &demandSearcher{}))
	stages, _, _, _, _, warnings := agent.inferDemandChainWithCategory(context.Background(), models.DemandChainQuery{Activity: "n-butanol"}, nil)
	joined := strings.ToLower(strings.Join(stagesToStrings(stages), "|"))
	if !strings.Contains(joined, "acrylic polymer and copolymer production") || !strings.Contains(joined, "superabsorbent polymer production") {
		t.Fatalf("stages = %v, want the known acrylate consumers", stagesToStrings(stages))
	}
	for _, stage := range stages {
		if stage.Source == "category" && len(stage.Operators) == 1 && !strings.HasSuffix(stage.Operators[0], " manufacturers") {
			t.Errorf("deterministic stage %q operators = %v, want the manufacturer vocabulary", stage.Name, stage.Operators)
		}
	}
	if !strings.Contains(strings.Join(warnings, " "), "intermediate product family") {
		t.Errorf("warnings = %v, want the downstep audit line", warnings)
	}
}

// TestDownstepDedupesAgainstInferredStages keeps the chain honest: a consumer
// the inference already covers under another label ("butyl acrylate
// manufacturing" vs "butyl acrylate production") is skipped, while genuinely
// new consumers survive. Shared role words like "production" must not count as
// overlap.
func TestDownstepDedupesAgainstInferredStages(t *testing.T) {
	llm := &downstepLLM{responses: []any{
		models.DemandChainInference{
			Category: "acrylates",
			Stages: []models.ValueChainStage{
				{Name: "Acrylate ester production", Distance: models.StageDistanceDirect},
				{Name: "Butyl acrylate production", Distance: models.StageDistanceDirect},
			},
		},
		[]models.ValueChainStage{
			{Name: "Butyl acrylate manufacturing", Rationale: "duplicate of an inferred stage"},
			{Name: "Superabsorbent polymer production", Rationale: "Consumes acrylate esters."},
		},
	}}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, &demandSearcher{}))
	stages, _, _, _, _, _ := agent.inferDemandChainWithCategory(context.Background(), models.DemandChainQuery{Activity: "n-butanol"}, nil)
	names := stagesToStrings(stages)
	count := 0
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), "butyl acrylate") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("butyl acrylate stages = %d (%v), want the inferred one only", count, names)
	}
	if !strings.Contains(strings.ToLower(strings.Join(names, "|")), "superabsorbent polymer production") {
		t.Errorf("stages = %v, want the genuinely new consumer", names)
	}
}

// TestDownstepSkipsNonIntermediateCategories pins the scope: a category that is
// not an intermediate product family earns no downstep call, and the inferred
// stages pass through unchanged.
func TestDownstepSkipsNonIntermediateCategories(t *testing.T) {
	llm := &downstepLLM{responses: []any{
		models.DemandChainInference{
			Category: "adhesives and sealants",
			Stages:   []models.ValueChainStage{{Name: "Adhesive manufacturing", Distance: models.StageDistanceDirect}},
		},
	}}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, &demandSearcher{}))
	stages, _, _, _, _, _ := agent.inferDemandChainWithCategory(context.Background(), models.DemandChainQuery{Activity: "n-butanol"}, nil)
	llm.mu.Lock()
	calls := llm.calls
	llm.mu.Unlock()
	if calls != 1 {
		t.Errorf("calls = %d, want no downstep call for a non-intermediate category", calls)
	}
	if len(stages) != 1 {
		t.Errorf("stages = %v, want the inferred stage unchanged", stagesToStrings(stages))
	}
}

// TestDownstepUpgradesKnownDirectConsumers covers the chemistry override: when
// the downstep proposes a stage that is a known DIRECT consumer of the product
// (plasticizer production esterifies phthalic anhydride with n-butanol), its
// distance is 1, not the chain position's 2.
func TestDownstepUpgradesKnownDirectConsumers(t *testing.T) {
	llm := &downstepLLM{responses: []any{
		models.DemandChainInference{
			Category: "esters",
			Stages:   []models.ValueChainStage{{Name: "Acetate ester production", Distance: models.StageDistanceDirect}},
		},
		[]models.ValueChainStage{
			{Name: "Plasticizer production", Rationale: "Esterifies phthalic anhydride with n-butanol into dibutyl phthalate."},
		},
	}}
	agent := NewDemandChainAgent(demandChainTestEngine(llm, &demandSearcher{}))
	stages, _, _, _, _, _ := agent.inferDemandChainWithCategory(context.Background(), models.DemandChainQuery{Activity: "n-butanol"}, nil)
	for _, stage := range stages {
		if strings.EqualFold(stage.Name, "Plasticizer production") && stage.Source == "category" {
			if stage.Distance != models.StageDistanceDirect {
				t.Errorf("plasticizer distance = %d, want the known-direct upgrade to 1", stage.Distance)
			}
			return
		}
	}
	t.Fatalf("stages = %v, want the plasticizer downstep stage", stagesToStrings(stages))
}

func stagesToStrings(stages []models.ValueChainStage) []string {
	out := make([]string, 0, len(stages))
	for _, stage := range stages {
		out = append(out, stage.Name+" ("+fmt.Sprint(stage.Distance)+","+stage.Source+")")
	}
	return out
}
