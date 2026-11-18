package agent

import "testing"

// TestTradeProductSuggestionsNeverStayEmpty pins the suggestion-first design:
// the picker is the product, so a near spelling must yield names to choose
// from even when no word-level signal exists. "styren" shares no whole token
// with "STYRENE MONOMER" - the character-similarity fallback must offer it
// (and POLYSTYRENE) as loose suggestions, and the run must NOT auto-pick one
// of them.
func TestTradeProductSuggestionsNeverStayEmpty(t *testing.T) {
	matches := TradeProductMatches("styren", tradeProductCompactVocabulary)
	if len(matches) < 2 {
		t.Fatalf("candidates = %+v, want at least STYRENE MONOMER and POLYSTYRENE as suggestions", matches)
	}
	names := map[string]bool{}
	for _, match := range matches {
		names[match.Name] = true
		if match.Exact {
			t.Errorf("%q must not be exact for a partial spelling", match.Name)
		}
		if match.Grade == "" {
			t.Errorf("%q carries no grade label", match.Name)
		}
	}
	if !names["STYRENE MONOMER"] || !names["POLYSTYRENE"] {
		t.Fatalf("suggestions = %v, want STYRENE MONOMER and POLYSTYRENE among them", names)
	}

	// Loose suggestions are never picked automatically: the raw activity is
	// what the run would query unless the operator selects one.
	product, _, candidates, source := ResolveTradeProduct("styren", "", tradeProductCompactVocabulary)
	if source != TradeProductSourceRaw || product != "styren" {
		t.Fatalf("resolution = (%q, %q), want the raw fallback with suggestions", product, source)
	}
	if len(candidates) == 0 {
		t.Error("the raw fallback must still carry the suggestion list")
	}
}

// TestTradeProductGradeLabels pins the label vocabulary the picker renders.
func TestTradeProductGradeLabels(t *testing.T) {
	cases := []struct {
		activity string
		wantTop  string
		wantGrad string
	}{
		{"styrenemonomer", "STYRENE MONOMER", TradeProductGradeExact},
		{"nbutanol", "N BUTANOL (NBA)", TradeProductGradeStrong},
	}
	for _, tc := range cases {
		matches := TradeProductMatches(tc.activity, tradeProductCompactVocabulary)
		if len(matches) == 0 || matches[0].Name != tc.wantTop || matches[0].Grade != tc.wantGrad {
			t.Errorf("%s: top = %+v, want %s/%s", tc.activity, firstMatch(matches), tc.wantTop, tc.wantGrad)
		}
	}
	// A word-level match carries the related label.
	matches := TradeProductMatches("butanol tertiary", tradeProductCompactVocabulary)
	if len(matches) == 0 || matches[0].Grade != TradeProductGradeRelated {
		t.Errorf("butanol tertiary: top = %+v, want a related grade", firstMatch(matches))
	}
}

func firstMatch(matches []TradeProductMatch) TradeProductMatch {
	if len(matches) == 0 {
		return TradeProductMatch{}
	}
	return matches[0]
}
