package agent

import (
	"context"
	"errors"
	"testing"
)

// tradeProductSemanticVocabulary is wide enough that the string prefilter keeps
// more than five candidates, which is when the semantic stage runs.
var tradeProductSemanticVocabulary = []string{
	"STYRENE",
	"STYRENE MONOMER",
	"POLYSTYRENE",
	"EXPANDABLE POLYSTYRENE",
	"ACRYLONITRILE STYRENE COPOLYMER",
	"STYRENE ACRYLONITRILE",
	"ALPHA METHYL STYRENE",
	"N BUTANOL (NBA)",
	"ISOBUTANOL",
}

// scriptedProductLLM answers the semantic picker with a fixed selection and
// records the prompt, so tests can pin both the ranking contract and the
// candidate-only validation.
type scriptedProductLLM struct {
	response []struct {
		Name      string `json:"name"`
		Rationale string `json:"rationale"`
	}
	err     error
	prompts []string
}

func (m *scriptedProductLLM) GenerateStructured(_ context.Context, _, userPrompt string, target any) error {
	m.prompts = append(m.prompts, userPrompt)
	if m.err != nil {
		return m.err
	}
	typed, ok := target.(*struct {
		Matches []struct {
			Name      string `json:"name"`
			Rationale string `json:"rationale,omitempty"`
		} `json:"matches"`
	})
	if !ok {
		return errors.New("unexpected target type")
	}
	for _, pick := range m.response {
		typed.Matches = append(typed.Matches, struct {
			Name      string `json:"name"`
			Rationale string `json:"rationale,omitempty"`
		}{Name: pick.Name, Rationale: pick.Rationale})
	}
	return nil
}

// TestSemanticTradeProductMatchesUsesTheModel pins the semantic layer: the
// model's selection (a subset of the prefiltered candidates) is returned in
// order, capped at five, graded semantic, with its rationale carried through.
func TestSemanticTradeProductMatchesUsesTheModel(t *testing.T) {
	llm := &scriptedProductLLM{}
	llm.response = append(llm.response, struct {
		Name      string `json:"name"`
		Rationale string `json:"rationale"`
	}{Name: "STYRENE MONOMER", Rationale: "the operator's spelling joins these two words"})
	llm.response = append(llm.response, struct {
		Name      string `json:"name"`
		Rationale string `json:"rationale"`
	}{Name: "POLYSTYRENE", Rationale: "a styrene family name"})

	matches := SemanticTradeProductMatches(context.Background(), llm, "styrenemonomer", tradeProductSemanticVocabulary)
	if len(matches) != 2 {
		t.Fatalf("matches = %+v, want the model's two picks", matches)
	}
	if matches[0].Name != "STYRENE MONOMER" || matches[0].Grade != TradeProductGradeSemantic || matches[0].Rationale == "" {
		t.Errorf("first pick = %+v, want semantic grade with rationale", matches[0])
	}
	if matches[1].Name != "POLYSTYRENE" {
		t.Errorf("second pick = %+v, want POLYSTYRENE", matches[1])
	}
	if len(llm.prompts) != 1 {
		t.Errorf("prompts = %d, want exactly one model call", len(llm.prompts))
	}
}

// TestSemanticTradeProductMatchesRejectsUnknownNames pins the hallucination
// guard: a name outside the candidate list is discarded; if every pick is
// unknown, the prefilter's own ranking answers instead.
func TestSemanticTradeProductMatchesRejectsUnknownNames(t *testing.T) {
	llm := &scriptedProductLLM{}
	llm.response = append(llm.response, struct {
		Name      string `json:"name"`
		Rationale string `json:"rationale"`
	}{Name: "UNOBTAINIUM DIOXIDE", Rationale: "invented"})

	matches := SemanticTradeProductMatches(context.Background(), llm, "unobtainium dioxide", tradeProductCompactVocabulary)
	for _, match := range matches {
		if match.Name == "UNOBTAINIUM DIOXIDE" {
			t.Fatalf("a name outside the candidate list must never surface: %+v", matches)
		}
	}
}

// TestSemanticTradeProductMatchesFailsSoftToThePrefilter pins the outage
// contract: a failing model leaves the string-ranked suggestions in place, so
// the picker is never empty because the model is down.
func TestSemanticTradeProductMatchesFailsSoftToThePrefilter(t *testing.T) {
	llm := &scriptedProductLLM{err: errors.New("model unavailable")}
	matches := SemanticTradeProductMatches(context.Background(), llm, "styrenemonomer", tradeProductSemanticVocabulary)
	if len(matches) == 0 {
		t.Fatal("model outage left the picker empty")
	}
	if matches[0].Name != "STYRENE MONOMER" {
		t.Fatalf("fallback top = %q, want the prefilter's STYRENE MONOMER", matches[0].Name)
	}
	if matches[0].Grade == TradeProductGradeSemantic {
		t.Error("a fallback suggestion must not claim the semantic grade")
	}
}

// TestSemanticTradeProductMatchesWithoutModel pins the nil-model path.
func TestSemanticTradeProductMatchesWithoutModel(t *testing.T) {
	matches := SemanticTradeProductMatches(context.Background(), nil, "styrenemonomer", tradeProductCompactVocabulary)
	if len(matches) == 0 || matches[0].Name != "STYRENE MONOMER" {
		t.Fatalf("matches = %+v, want the string prefilter", matches)
	}
}
