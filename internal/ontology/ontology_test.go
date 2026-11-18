package ontology

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSeedLoadsAndLintsClean pins the shipped document: it must decode under
// the strict decoder, carry no lint errors, and resolve the operator's own
// examples through the graph.
func TestSeedLoadsAndLintsClean(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(document.Nodes) < 100 {
		t.Fatalf("seed has %d nodes, want the reviewed skeleton", len(document.Nodes))
	}
	// The operator's report: "oil blending" must not resolve to oleochemicals,
	// and the oil hypernym must rank the petroleum family first.
	oil := document.Nodes["cat:oils"]
	if oil == nil || !oil.Hypernym || len(oil.Hyponyms) < 2 {
		t.Fatalf("cat:oils = %+v, want the ambiguous oil hypernym with ranked variants", oil)
	}
	if oil.Hyponyms[0] != "cat:lubricants" {
		t.Errorf("oil hyponyms = %v, want lubricants ranked first", oil.Hyponyms)
	}
	if matches := document.Lookup("n-butanol"); len(matches) != 1 || matches[0] != "sub:n-butanol" {
		t.Errorf("Lookup(n-butanol) = %v", matches)
	}
	if matches := document.Lookup("Butyl Alcohol"); len(matches) != 1 || matches[0] != "sub:n-butanol" {
		t.Errorf("Lookup by alias = %v, want sub:n-butanol", matches)
	}
}

// TestReviewArtifactsCoverEveryNode keeps the worksheet honest: every node the
// reviewer must judge appears in the review markdown, and the lint report
// carries each issue.
func TestReviewArtifactsCoverEveryNode(t *testing.T) {
	document, err := LoadFile(filepath.Join("data", "chemicals.json"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	review := ReviewMarkdown(document)
	for _, id := range document.NodeIDs() {
		if !strings.Contains(review, "`"+id+"`") {
			t.Errorf("review sheet is missing node %q", id)
		}
	}
	if !strings.Contains(review, "## How to review this") || !strings.Contains(review, "corrections") {
		t.Error("review sheet must explain the review and return workflow")
	}
	issues := Lint(document)
	report := IssuesMarkdown(document, issues)
	if !strings.Contains(report, "block the runtime load") {
		t.Errorf("lint report must explain what blocks a load: %s", report)
	}
}

// TestCorrectionsApply pins the reviewer's edit path: field replacement, node
// addition and deletion, all on top of the shipped document.
func TestCorrectionsApply(t *testing.T) {
	base, err := Seed()
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	before := len(base.Nodes)
	approved := StatusApproved
	source := SourceOperator
	aliases := []string{"abs", "styrene acrylonitrile"}
	note := "reviewed"
	kind := KindSubstance
	label := "Acrylonitrile butadiene styrene"
	corrections := map[string]*Correction{
		"sub:styrene": {Status: &approved, Source: &source, ExpertNote: &note},
		"sub:abs":     {Kind: &kind, Label: &label, Aliases: &aliases, Status: &approved, Source: &source},
		"sub:diesel":  {Delete: true},
	}
	if err := ApplyCorrections(base, corrections); err != nil {
		t.Fatalf("ApplyCorrections: %v", err)
	}
	if len(base.Nodes) != before+1-1 {
		t.Errorf("node count = %d, want %d after one add and one delete", len(base.Nodes), before)
	}
	if base.Nodes["sub:styrene"].Status != StatusApproved || base.Nodes["sub:styrene"].ExpertNote != "reviewed" {
		t.Errorf("styrene correction not applied: %+v", base.Nodes["sub:styrene"])
	}
	if base.Nodes["sub:diesel"] != nil {
		t.Error("deleted node is still present")
	}
	if got := base.Nodes["sub:abs"]; got == nil || len(got.Aliases) != 2 {
		t.Errorf("added node = %+v", got)
	}
	if _, err := Seed(); err != nil {
		t.Fatalf("Seed after mutation: %v", err)
	}
}

// TestMergeAcceptsFullFileAndPatch covers both return shapes: the reviewer sends
// the whole edited document, or only a corrections block.
func TestMergeAcceptsFullFileAndPatch(t *testing.T) {
	base, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	note := "confirmed"
	source := SourceOperator
	patch := &Ontology{
		SchemaVersion: SchemaVersion,
		Revision:      "curated-1",
		ReviewedBy:    "domain expert",
		Nodes:         map[string]*Node{},
		Corrections:   map[string]*Correction{"cat:aromatics": {ExpertNote: &note, Source: &source}},
	}
	merged, err := Merge(base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if merged.Revision != "curated-1" || merged.ReviewedBy != "domain expert" {
		t.Errorf("merged metadata = %q/%q", merged.Revision, merged.ReviewedBy)
	}
	if merged.Nodes["cat:aromatics"].ExpertNote != "confirmed" || merged.Nodes["cat:aromatics"].Source != SourceOperator {
		t.Errorf("patch correction did not apply: %+v", merged.Nodes["cat:aromatics"])
	}
	if base.Nodes["cat:aromatics"].ExpertNote != "" || base.Nodes["cat:aromatics"].Source == SourceOperator {
		t.Error("merge mutated the base document")
	}
	// A full-file return replaces the node outright.
	replacement := &Node{Kind: KindCategory, Label: "Aromatics", Parent: "cat:petrochemicals", Status: StatusApproved}
	full, err := Merge(base, &Ontology{SchemaVersion: SchemaVersion, Revision: "curated-2", Nodes: map[string]*Node{"cat:aromatics": replacement}})
	if err != nil {
		t.Fatalf("Merge full file: %v", err)
	}
	if full.Nodes["cat:aromatics"].Label != "Aromatics" || len(full.Nodes["cat:aromatics"].Aliases) != 0 {
		t.Errorf("full-file replacement lost: %+v", full.Nodes["cat:aromatics"])
	}
}

func stringPtr(value string) *string { return &value }
