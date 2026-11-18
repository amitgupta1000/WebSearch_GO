package agent

// trade_product_semantic.go is the semantic suggestion layer behind the
// console's trade-product picker.
//
// The canonical vocabulary is the DB's own 727-name catalogue, and no string
// heuristic applies uniformly to all of it (hyphens, joined words, trade
// synonyms like "MEK" for "METHYL ETHYL KETONE", word order). String
// similarity therefore only narrows the field — deterministically, to the 40
// closest names — and one bounded structured model call ranks semantic
// closeness among those candidates. The model may only pick names from the
// candidate list; an answer outside it is discarded, never offered as a
// suggestion. When the model is missing or fails, the prefilter's own ranking
// is the answer (fail-soft), so the picker stays populated without a model.
// The operator always picks; nothing here auto-resolves a run.

import (
	"context"
	"fmt"
	"strings"
)

// *Engine satisfies LLMClient (defined in engine.go); pin it so a signature
// drift fails here instead of silently dropping the semantic layer.
var _ LLMClient = (*Engine)(nil)

const (
	// tradeProductPrefilterLimit is how many string-ranked candidates the
	// semantic stage judges. 40 is small enough for one bounded model call
	// and wide enough to hold every plausible synonym of a typed name.
	tradeProductPrefilterLimit = 40
	// tradeProductSemanticLimit caps the suggestions offered to the operator.
	tradeProductSemanticLimit = 5
)

// SemanticTradeProductMatches ranks the canonical vocabulary against the
// operator's activity for the picker: string prefilter first, then a model
// re-rank, then a fail-soft fallback to the prefilter. The returned names are
// always entries of the given vocabulary.
func SemanticTradeProductMatches(ctx context.Context, llm LLMClient, activity string, vocabulary []string) []TradeProductMatch {
	prefiltered := rankTradeProductNames(activity, vocabulary, tradeProductPrefilterLimit)
	if len(prefiltered) == 0 || llm == nil {
		return prefiltered
	}

	candidates := make(map[string]bool, len(prefiltered))
	lines := make([]string, 0, len(prefiltered))
	for _, candidate := range prefiltered {
		candidates[strings.ToLower(candidate.Name)] = true
		lines = append(lines, "- "+candidate.Name)
	}

	var target struct {
		Matches []struct {
			Name      string `json:"name"`
			Rationale string `json:"rationale,omitempty"`
		} `json:"matches"`
	}
	system := "You match an operator's typed product name onto a customs database's canonical chemical names. " +
		"Rank semantic closeness: which candidate names the same substance the operator means, including trade " +
		"synonyms (e.g. \"MEK\" = \"METHYL ETHYL KETONE\"), common misspellings, word order and joined words. " +
		"Return at most 5 candidates, closest first, as JSON matching {matches: [{name, rationale}]}. " +
		"Use ONLY names from the candidate list; never invent a name. One short rationale per pick."
	prompt := fmt.Sprintf("Operator typed: %q\nCandidate canonical names:\n%s", strings.TrimSpace(activity), strings.Join(lines, "\n"))
	if err := llm.GenerateStructured(ctx, system, prompt, &target); err != nil {
		return prefiltered[:min(len(prefiltered), tradeProductSemanticLimit)]
	}

	suggestions := make([]TradeProductMatch, 0, tradeProductSemanticLimit)
	seen := make(map[string]bool, tradeProductSemanticLimit)
	for _, pick := range target.Matches {
		name := strings.TrimSpace(pick.Name)
		key := strings.ToLower(name)
		if name == "" || seen[key] || !candidates[key] {
			continue
		}
		seen[key] = true
		suggestions = append(suggestions, TradeProductMatch{
			Name:      name,
			Score:     90 - len(suggestions)*5,
			Grade:     TradeProductGradeSemantic,
			Rationale: strings.TrimSpace(pick.Rationale),
		})
		if len(suggestions) == tradeProductSemanticLimit {
			break
		}
	}
	if len(suggestions) == 0 {
		return prefiltered[:min(len(prefiltered), tradeProductSemanticLimit)]
	}
	return suggestions
}
