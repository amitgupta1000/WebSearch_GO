package agent

import "testing"

// tradeProductCompactVocabulary mirrors the live catalogue's shapes for the
// styrene family plus the butanol family.
var tradeProductCompactVocabulary = []string{
	"STYRENE",
	"STYRENE MONOMER",
	"POLYSTYRENE",
	"N BUTANOL (NBA)",
	"ISOBUTANOL",
}

// TestTradeProductMatchesJoinsSplitWords pins the compact-form matching: an
// operator who types "styrenemonomer" (no space) must still reach the
// catalogue's "STYRENE MONOMER" — the word-joining is punctuation blindness's
// twin, and a live sample query failed without it. A spaced exact match keeps
// working, and the joined-word fragment stays a candidate, never an automatic
// pick, when the canonical name carries more words.
func TestTradeProductMatchesJoinsSplitWords(t *testing.T) {
	// Joined words, exact once the spaces are stripped.
	matches := TradeProductMatches("styrenemonomer", tradeProductCompactVocabulary)
	if len(matches) == 0 {
		t.Fatal("no candidates for styrenemonomer")
	}
	if matches[0].Name != "STYRENE MONOMER" || !matches[0].Exact || matches[0].Score != 100 {
		t.Fatalf("top match = %+v, want STYRENE MONOMER exact/100", matches[0])
	}
	product, _, _, source := ResolveTradeProduct("styrenemonomer", "", tradeProductCompactVocabulary)
	if source != TradeProductSourceVocabulary || product != "STYRENE MONOMER" {
		t.Fatalf("resolution = (%q, %q), want the vocabulary match", product, source)
	}

	// Spaced exact still resolves.
	if product, _, _, source := ResolveTradeProduct("styrene monomer", "", tradeProductCompactVocabulary); source != TradeProductSourceVocabulary || product != "STYRENE MONOMER" {
		t.Fatalf("spaced resolution = (%q, %q), want the vocabulary match", product, source)
	}

	// A joined fragment of a longer canonical name is a candidate, not a pick.
	matches = TradeProductMatches("nbutanol", tradeProductCompactVocabulary)
	if len(matches) == 0 || matches[0].Name != "N BUTANOL (NBA)" {
		t.Fatalf("candidates = %+v, want N BUTANOL (NBA) first", matches)
	}
	if matches[0].Exact {
		t.Error("nbutanol must not claim an exact match against N BUTANOL (NBA)")
	}
	if matches[0].Score != 80 {
		t.Errorf("nbutanol score = %d, want the 80 joined-fragment score", matches[0].Score)
	}
}
