package ontology

import (
	"encoding/json"
	"testing"
)

// TestSubgraphIsBounded pins the prompt lexicon: one hop from a substance
// includes its family, its derivative and the process it feeds, but not a node
// three hops away.
func TestSubgraphIsBounded(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	lexicon := document.Subgraph([]string{"sub:n-butanol"}, 1)
	ids := map[string]bool{}
	for _, id := range lexicon.IDs() {
		ids[id] = true
	}
	for _, want := range []string{"sub:n-butanol", "cat:alcohol-solvents", "sub:butyl-acrylate", "proc:esterification"} {
		if !ids[want] {
			t.Errorf("one-hop lexicon = %v, missing %q", lexicon.IDs(), want)
		}
	}
	if ids["end:pvc-compound"] {
		t.Errorf("one-hop lexicon reached a node two hops away: %v", lexicon.IDs())
	}
	raw, err := lexicon.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("lexicon is not valid JSON: %v", err)
	}
	if len(lexicon.Seeds) != 1 || lexicon.Seeds[0] != "sub:n-butanol" {
		t.Errorf("seeds = %v", lexicon.Seeds)
	}
	// Two hops must reach further without unbounded growth.
	deeper := document.Subgraph([]string{"sub:n-butanol"}, 2)
	if len(deeper.Nodes) <= len(lexicon.Nodes) {
		t.Errorf("two hops (%d nodes) should exceed one hop (%d nodes)", len(deeper.Nodes), len(lexicon.Nodes))
	}
}

// TestExpandWalksBothDirections pins the graph replacement for the hand-written
// consumer/direct-input tables.
func TestExpandWalksBothDirections(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	downstream := document.Expand([]string{"sub:n-butanol"}, Downstream, 2)
	reached := map[string]int{}
	for _, node := range downstream {
		reached[node.ID] = node.Distance
	}
	if distance, ok := reached["sub:butyl-acrylate"]; !ok || distance != 1 {
		t.Errorf("downstream = %v, want butyl acrylate at distance 1", reached)
	}
	if distance, ok := reached["end:pvc-compound"]; !ok || distance != 2 {
		t.Errorf("downstream = %v, want the PVC-compounding leaf at distance 2", reached)
	}
	if _, ok := reached["end:detergents"]; ok {
		t.Errorf("downstream = %v, LABSA path leaked into the butanol chain", reached)
	}
	upstream := document.Expand([]string{"end:pvc-compound"}, Upstream, 2)
	feeding := map[string]bool{}
	for _, node := range upstream {
		feeding[node.ID] = true
	}
	for _, want := range []string{"sub:pvc-resin", "sub:dibutyl-phthalate", "cat:plasticizers"} {
		if !feeding[want] {
			t.Errorf("upstream of the PVC leaf = %v, missing %q", feeding, want)
		}
	}
}

// TestCanonicalTradeMatchMapsTheDatabaseVocabulary pins the trade-DB bridge: a
// canonical name maps to exactly one node, and an unknown or ambiguous name is
// reported instead of guessed.
func TestCanonicalTradeMatchMapsTheDatabaseVocabulary(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	matches, unmatched := document.CanonicalTradeMatch([]string{"CAUSTIC SODA", "N-BUTANOL", "ACETONE CYANOHYDRIN", "TOLUENE"})
	if matches["CAUSTIC SODA"] != "sub:caustic-soda" {
		t.Errorf("CAUSTIC SODA → %q", matches["CAUSTIC SODA"])
	}
	if matches["N-BUTANOL"] != "sub:n-butanol" {
		t.Errorf("N-BUTANOL → %q", matches["N-BUTANOL"])
	}
	if matches["TOLUENE"] != "sub:toluene" {
		t.Errorf("TOLUENE → %q", matches["TOLUENE"])
	}
	if len(unmatched) != 1 || unmatched[0] != "ACETONE CYANOHYDRIN" {
		t.Errorf("unmatched = %v, want only the unknown name", unmatched)
	}
}

// TestResolveJSONIsSerializable keeps the resolution usable as an API response.
func TestResolveJSONIsSerializable(t *testing.T) {
	resolution := Resolve("oil blending")
	raw, err := json.Marshal(resolution)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(raw) < 100 {
		t.Errorf("resolution JSON = %s", raw)
	}
}
