package ontology

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestViewWholeGraphIsTheDocument pins the default view: every node, every edge
// whose ends are drawn, and a layout that touches each node exactly once.
func TestViewWholeGraphIsTheDocument(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	view := document.View(ViewOptions{})
	if len(view.Nodes) != len(document.Nodes) {
		t.Fatalf("nodes = %d, want %d", len(view.Nodes), len(document.Nodes))
	}
	if len(view.Edges) == 0 {
		t.Fatal("the whole-graph view must carry edges")
	}
	seen := map[string]bool{}
	for _, node := range view.Nodes {
		seen[node.ID] = true
		// The centre holds the single root when there is one; every other ring
		// must have a real position, or the picture would stack nodes on (0,0).
		if node.Ring > 0 && node.X == 0 && node.Y == 0 {
			t.Errorf("node %s sits at the origin with ring %d", node.ID, node.Ring)
		}
	}
	for _, edge := range view.Edges {
		if !seen[edge.From] || !seen[edge.To] {
			t.Errorf("edge %s→%s crosses outside the view", edge.From, edge.To)
		}
	}
	if view.KindCounts[string(KindSubstance)] == 0 || view.KindCounts[string(KindCategory)] == 0 {
		t.Errorf("kind counts = %v, want substances and categories", view.KindCounts)
	}
	if view.EdgeCounts["parent"] == 0 || view.EdgeCounts["consumed_in"] == 0 {
		t.Errorf("edge counts = %v, want the taxonomy and value-chain families", view.EdgeCounts)
	}
}

// TestViewSeededByActivity pins the "show me this activity" case: the resolver
// names the nodes, the neighbourhood stays bounded, and the seed sits at ring 0.
func TestViewSeededByActivity(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	view := document.View(ViewOptions{Seed: "oil blending", Hops: 2})
	if view.Seed != "oil blending" || view.SeedLabel == "" {
		t.Errorf("seed = %q/%q, want the requested text", view.Seed, view.SeedLabel)
	}
	if len(view.Nodes) == 0 || len(view.Nodes) >= len(document.Nodes) {
		t.Fatalf("nodes = %d, want a bounded neighbourhood", len(view.Nodes))
	}
	if view.Hops != 2 {
		t.Errorf("hops = %d, want 2", view.Hops)
	}
	labels := map[string]ViewNode{}
	for _, node := range view.Nodes {
		labels[node.ID] = node
	}
	for _, want := range []string{"cat:oils", "cat:lubricants", "proc:blending"} {
		if _, ok := labels[want]; !ok {
			t.Errorf("seed view lacks %q", want)
		}
	}
	if ring := labels["cat:oils"].Ring; ring != 0 {
		t.Errorf("the matched subject sits at ring %d, want 0", ring)
	}
	if view.HiddenNodes == 0 {
		t.Error("a seeded view must report what it left out")
	}
}

// TestViewAcceptsIdsLabelsAliasesAndUnknownSeeds pins seed resolution, including
// the honest answer for a seed the graph cannot place.
func TestViewAcceptsIdsLabelsAliasesAndUnknownSeeds(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if view := document.View(ViewOptions{Seed: "sub:n-butanol"}); len(view.Nodes) == 0 || view.Nodes[0].ID != "" {
		for _, node := range view.Nodes {
			if node.ID == "sub:n-butanol" && node.Ring != 0 {
				t.Errorf("id seed ring = %d, want 0", node.Ring)
			}
		}
	}
	if view := document.View(ViewOptions{Seed: "n-Butanol"}); len(view.Nodes) == 0 || view.SeedLabel == "" {
		t.Error("a label seed must resolve")
	}
	unknown := document.View(ViewOptions{Seed: "unobtainium powder"})
	if len(unknown.Warnings) == 0 || !strings.Contains(unknown.Warnings[0], "showing the whole graph") {
		t.Errorf("warnings = %v, want the unknown-seed notice", unknown.Warnings)
	}
	if len(unknown.Nodes) != len(document.Nodes) {
		t.Errorf("nodes = %d, want the whole graph after an unknown seed", len(unknown.Nodes))
	}
}

// TestViewFiltersKindsAndEdges pins the filters the page exposes.
func TestViewFiltersKindsAndEdges(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	substances := document.View(ViewOptions{Kinds: []Kind{KindSubstance}})
	if len(substances.Nodes) == 0 {
		t.Fatal("no substances")
	}
	for _, node := range substances.Nodes {
		if node.Kind != KindSubstance {
			t.Errorf("node %s kind = %s, want substance only", node.ID, node.Kind)
		}
	}
	valueChain := document.View(ViewOptions{Edges: []string{"consumed_in"}})
	for _, edge := range valueChain.Edges {
		if edge.Type != "consumed_in" {
			t.Errorf("edge %+v survived a consumed_in filter", edge)
		}
	}
	if len(valueChain.Edges) == 0 {
		t.Error("the consumed_in filter must keep that family")
	}
}

// TestViewLayoutIsDeterministic pins the promise that matters for review: the same
// document draws the same picture twice.
func TestViewLayoutIsDeterministic(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	first, err := json.Marshal(document.View(ViewOptions{Seed: "n-butanol", Hops: 3}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	second, err := json.Marshal(document.View(ViewOptions{Seed: "n-butanol", Hops: 3}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Error("two views of the same document differ")
	}
}
