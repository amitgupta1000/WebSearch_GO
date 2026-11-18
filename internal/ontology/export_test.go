package ontology

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoaderRejectsUnknownFields pins the reviewer-safety rule: a typo in the
// curated file (hs_code for hs_codes) must fail loudly instead of being dropped.
func TestLoaderRejectsUnknownFields(t *testing.T) {
	path := writeTemp(t, `{"schema_version":1,"revision":"x","nodes":{"sub:a":{"kind":"substance","label":"A","hs_code":["1"],"status":"proposed"}}}`)
	if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("LoadFile = %v, want an unknown-field error", err)
	}
	path = writeTemp(t, `{"schema_version":99,"revision":"x","nodes":{}}`)
	if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("LoadFile = %v, want a schema-version error", err)
	}
}

// TestMarshalIsDeterministic pins the roundtrip artifact: emitting twice gives
// identical bytes, corrections are cleared, and hyponym rank order survives.
func TestMarshalIsDeterministic(t *testing.T) {
	document, err := LoadFile(filepath.Join("data", "chemicals.json"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	document.Corrections = map[string]*Correction{"sub:benzene": {Status: statusPtr(StatusApproved)}}
	first, err := Marshal(document)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	second, err := Marshal(document)
	if err != nil {
		t.Fatalf("Marshal twice: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("Marshal is not deterministic")
	}
	if strings.Contains(string(first), `"corrections":`) {
		t.Error("the canonical artifact must not carry an unapplied corrections block")
	}
	path := writeTemp(t, string(first))
	reloaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Nodes["cat:oils"].Hyponyms; len(got) == 0 || got[0] != "cat:lubricants" {
		t.Errorf("hyponym rank order lost: %v", got)
	}
	var probe map[string]any
	if err := json.Unmarshal(first, &probe); err != nil {
		t.Fatalf("artifact is not valid JSON: %v", err)
	}
}

// TestHarvestAddsOnlyUnknownNames pins the trade-DB seed path: a canonical name
// already covered by a label or alias is skipped, and a new one arrives as a
// proposed substance with no invented family.
func TestHarvestAddsOnlyUnknownNames(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	corrections, skipped := Harvest(document, []string{"CAUSTIC SODA", "N-BUTANOL", "ACETONE CYANOHYDRIN", "  "})
	if len(corrections) != 1 {
		t.Fatalf("corrections = %v, want exactly the unknown name", corrections)
	}
	correction := corrections["sub:acetone-cyanohydrin"]
	if correction == nil || correction.Kind == nil || *correction.Kind != KindSubstance {
		t.Fatalf("harvested correction = %+v", correction)
	}
	if correction.Categories != nil {
		t.Error("harvest must not invent a family for an unreviewed name")
	}
	if len(skipped) != 2 {
		t.Errorf("skipped = %v, want both covered names", skipped)
	}
	if Slug("CAUSTIC SODA") != "caustic-soda" {
		t.Errorf("Slug = %q", Slug("CAUSTIC SODA"))
	}
}

// TestDiffReportsReviewerEdits covers the audit path: the diff names the added,
// removed and changed nodes between the submitted and returned document.
func TestDiffReportsReviewerEdits(t *testing.T) {
	before, err := Seed()
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	after := &Ontology{SchemaVersion: SchemaVersion, Revision: "curated", Nodes: map[string]*Node{}}
	for id, node := range before.Nodes {
		after.Nodes[id] = node.clone()
	}
	after.Nodes["cat:aromatics"].Status = StatusRejected
	after.Nodes["cat:aromatics"].ExpertNote = "confirmed by reviewer"
	delete(after.Nodes, "sub:pvc-resin")
	after.Nodes["sub:new-thing"] = &Node{Kind: KindSubstance, Label: "New thing", Categories: []string{"cat:aromatics"}, Status: StatusProposed}

	report := Diff(before, after)
	for _, want := range []string{"`sub:new-thing`", "`sub:pvc-resin`", "`cat:aromatics`", "status", "expert_note"} {
		if !strings.Contains(report, want) {
			t.Errorf("diff is missing %q:\n%s", want, report)
		}
	}
}

func statusPtr(value Status) *Status { return &value }

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return path
}
