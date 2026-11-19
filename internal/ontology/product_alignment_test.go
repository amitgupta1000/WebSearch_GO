package ontology

import "testing"

// TestResolveJoinedSpellingNamesTheGraphEntry pins the joined-word entry pass:
// operators type "toluenediisocyanate" as readily as "toluene diisocyanate",
// and the graph must anchor on the substance instead of leaving the activity to
// a broader model guess ("isocyanates").
func TestResolveJoinedSpellingNamesTheGraphEntry(t *testing.T) {
	resolution := Resolve("toluenediisocyanate")
	if len(resolution.Matches) == 0 {
		t.Fatalf("resolution = %+v, want the joined spelling to match Toluene diisocyanate", resolution)
	}
	match := resolution.Matches[0]
	if match.ID != "sub:toluene-diisocyanate" {
		t.Fatalf("match = %+v, want sub:toluene-diisocyanate", match)
	}
	if match.Via != "joined" {
		t.Errorf("via = %q, want joined", match.Via)
	}
	if len(resolution.Families) == 0 || resolution.Families[0].ID != "cat:isocyanates" {
		t.Errorf("families = %+v, want cat:isocyanates first", resolution.Families)
	}
}

// TestResolveSpacedSpellingKeepsTheLabelPass guards the precision of the
// compact pass: a spaced label still matches through the ordinary phrase pass,
// and a joined spelling never becomes the only way to name the node.
func TestResolveSpacedSpellingKeepsTheLabelPass(t *testing.T) {
	resolution := Resolve("toluene diisocyanate")
	if len(resolution.Matches) == 0 || resolution.Matches[0].ID != "sub:toluene-diisocyanate" {
		t.Fatalf("resolution = %+v, want the spaced label to match the same node", resolution)
	}
	if via := resolution.Matches[0].Via; via == "joined" {
		t.Errorf("via = %q for a spaced label, want the ordinary label pass", via)
	}
}

// TestAlignTradeProductAnchorsTheCanonicalName pins the DB-to-graph alignment:
// the trade DB's canonical spelling, with its trade abbreviation in
// parentheses, resolves onto the graph's substance and family, so the picked
// product drives the ontology step.
func TestAlignTradeProductAnchorsTheCanonicalName(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, name := range []string{
		"TOLUENE DI ISOCYANATE (TDI)",
		"TOLUENE DI ISOCYANATE",
		"toluene-di-isocyanate",
		"TOLUENE DIISOCYANATE (TDI 80/20)",
		"TOLUENE DI ISOCYANATE (TDI) 80/20",
	} {
		resolution, ok := document.AlignTradeProduct(name, DefaultOptions)
		if !ok {
			t.Errorf("AlignTradeProduct(%q) did not align", name)
			continue
		}
		if len(resolution.Matches) == 0 || resolution.Matches[0].ID != "sub:toluene-diisocyanate" {
			t.Errorf("AlignTradeProduct(%q) matches = %+v, want sub:toluene-diisocyanate", name, resolution.Matches)
		}
		if len(resolution.Families) == 0 || resolution.Families[0].ID != "cat:isocyanates" {
			t.Errorf("AlignTradeProduct(%q) families = %+v, want cat:isocyanates first", name, resolution.Families)
		}
		if len(resolution.EndProducts) == 0 {
			t.Errorf("AlignTradeProduct(%q) produced no end-product leaves", name)
		}
	}
}

// TestAlignTradeProductLeavesUnknownNamesAlone pins the fail-soft contract: an
// unknown product is not forced onto a node, and a longer name that merely
// starts with a graph label ("ACETONE CYANOHYDRIN" vs "Acetone") is not
// mistaken for it.
func TestAlignTradeProductLeavesUnknownNamesAlone(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, name := range []string{"UNOBTAINIUM POWDER", "ACETONE CYANOHYDRIN"} {
		if resolution, ok := document.AlignTradeProduct(name, DefaultOptions); ok {
			t.Errorf("AlignTradeProduct(%q) aligned an unknown product: %+v", name, resolution)
		}
	}
}

// TestCanonicalTradeMatchUsesTheAlignmentPass pins the bridge update: a DB
// spelling the graph does not carry verbatim maps through the alignment pass
// instead of being reported unmatched, so harvests and the runtime agree.
func TestCanonicalTradeMatchUsesTheAlignmentPass(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	matches, unmatched := document.CanonicalTradeMatch([]string{"TOLUENE DI ISOCYANATE (TDI)"})
	if got := matches["TOLUENE DI ISOCYANATE (TDI)"]; got != "sub:toluene-diisocyanate" {
		t.Errorf("TDI -> %q (unmatched: %v), want sub:toluene-diisocyanate", got, unmatched)
	}
}
