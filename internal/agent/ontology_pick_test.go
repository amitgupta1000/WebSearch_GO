package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/ontology"
)

// TestActivityCandidatesRankTheMatchFirst pins the suggestion list: the node the
// resolution picked comes first, and the alternatives an operator might have
// meant -- including the one the resolver rejected -- are still offered.
func TestActivityCandidatesRankTheMatchFirst(t *testing.T) {
	document, err := ontology.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	graph := document.Resolve("crude palm oil", ontology.DefaultOptions)
	candidates := activityCandidates(document, graph, "crude palm oil", ontologyCandidateLimit)
	if len(candidates) == 0 || len(candidates) > ontologyCandidateLimit {
		t.Fatalf("candidates = %+v, want 1..%d", candidates, ontologyCandidateLimit)
	}
	first := candidates[0]
	if first.NodeID != "sub:palm-oil" || first.Grade != "exact" {
		t.Errorf("rank 0 = %+v, want sub:palm-oil graded exact", first)
	}
	if first.FamilyID != "cat:oleochemical-feedstocks" {
		t.Errorf("rank 0 family = %q, want cat:oleochemical-feedstocks", first.FamilyID)
	}
	// The crude-oil reading is wrong for this activity but it is a legitimate
	// thing for an operator to want; it must be offered, and not as the match.
	sawCrudeOil := false
	for _, candidate := range candidates[1:] {
		if candidate.NodeID == "sub:crude-oil" {
			sawCrudeOil = true
			if candidate.Grade == "exact" {
				t.Errorf("crude oil graded %q, want a weaker alternative", candidate.Grade)
			}
		}
	}
	if !sawCrudeOil {
		t.Errorf("candidates = %+v, want the crude-oil alternative offered", candidates)
	}
}

// TestActivityCandidatesLeadWithVariantsWhenAmbiguous pins the ordering rule: for
// a hypernym activity the graph's ranked variants are the useful choices, for a
// specific one the alternative nodes are.
func TestActivityCandidatesLeadWithVariantsWhenAmbiguous(t *testing.T) {
	document, err := ontology.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	graph := document.Resolve("oil blending", ontology.DefaultOptions)
	if !graph.Ambiguous {
		t.Fatalf("resolution = %+v, want the hypernym flagged ambiguous", graph)
	}
	candidates := activityCandidates(document, graph, "oil blending", ontologyCandidateLimit)
	if len(candidates) < 3 {
		t.Fatalf("candidates = %+v, want the ranked variants", candidates)
	}
	sawVariant := false
	for _, candidate := range candidates {
		if candidate.Grade == "variant" {
			sawVariant = true
		}
	}
	if !sawVariant {
		t.Errorf("candidates = %+v, want at least one ranked variant", candidates)
	}
}

// TestOntologyPickAnchorsTheResolution pins the confirmation: a remembered pick
// anchors the run on that node without re-reading the text, and an explicit pick
// wins over the remembered one.
func TestOntologyPickAnchorsTheResolution(t *testing.T) {
	document, err := ontology.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	const activity = "cpo grade fixture"
	SetOntologyPick(activity, "sub:palm-oil")
	resolution := resolveActivityWithAnchor(context.Background(), nil, document, activity, "", "", nil)
	if resolution.Path != pathOperator || resolution.NodeID != "sub:palm-oil" {
		t.Fatalf("resolution = %+v, want the operator anchor on sub:palm-oil", resolution)
	}
	if len(resolution.Graph.Families) == 0 || resolution.Graph.Families[0].ID != "cat:oleochemical-feedstocks" {
		t.Errorf("families = %+v, want the palm-oil value chain", resolution.Graph.Families)
	}
	if len(resolution.Warnings) == 0 || !strings.Contains(resolution.Warnings[0], "confirmed by the operator") {
		t.Errorf("warnings = %v, want the confirmation notice", resolution.Warnings)
	}
	// An explicit confirmation for the same activity overrides the remembered one.
	explicit := resolveActivityWithAnchor(context.Background(), nil, document, activity, "", "sub:crude-oil", nil)
	if explicit.Path != pathOperator || explicit.NodeID != "sub:crude-oil" {
		t.Errorf("explicit resolution = %+v, want the explicit crude-oil anchor", explicit)
	}
	// Another activity is untouched by the pick.
	other := resolveActivityWithAnchor(context.Background(), nil, document, "glycerin", "", "", nil)
	if other.Path == pathOperator {
		t.Errorf("unrelated activity anchored by a foreign pick: %+v", other)
	}
}

// TestOntologyPickIgnoresUnapprovedNodes pins the fail-soft path: a pick naming
// nothing approved is reported and the text resolves instead.
func TestOntologyPickIgnoresUnapprovedNodes(t *testing.T) {
	document, err := ontology.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	resolution := resolveActivityWithAnchor(context.Background(), nil, document, "glycerin", "", "sub:not-a-node", nil)
	if resolution.Path == pathOperator {
		t.Fatalf("resolution = %+v, want the text path for an unknown pick", resolution)
	}
	if len(resolution.Warnings) == 0 || !strings.Contains(strings.Join(resolution.Warnings, " "), "not an approved node") {
		t.Errorf("warnings = %v, want the invalid-pick notice", resolution.Warnings)
	}
}
