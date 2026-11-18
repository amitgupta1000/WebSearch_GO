package ontology

import (
	"strings"
	"testing"
)

// TestResolveOilBlendingRanksFamilies pins the operator's example end to end:
// "oil blending" is ambiguous, lubricants rank first, and the other petroleum
// families follow — oleochemicals is a ranked variant of the feedstock family,
// never the silent answer.
func TestResolveOilBlendingRanksFamilies(t *testing.T) {
	resolution := Resolve("Oil Blending")
	if !resolution.Ambiguous {
		t.Fatalf("resolution = %+v, want the oil hypernym flagged ambiguous", resolution)
	}
	if len(resolution.Families) < 2 {
		t.Fatalf("families = %+v, want the ranked variants", resolution.Families)
	}
	if got := resolution.Families[0].ID; got != "cat:lubricants" {
		t.Errorf("rank 0 = %q, want cat:lubricants (the dominant indexed phrase)", got)
	}
	if got := resolution.Families[0].Reason; got != "hypernym" {
		t.Errorf("rank 0 reason = %q, want hypernym", got)
	}
	seen := map[string]bool{}
	for _, family := range resolution.Families {
		seen[family.ID] = true
	}
	for _, want := range []string{"cat:fuel-oils", "cat:oleochemical-feedstocks"} {
		if !seen[want] {
			t.Errorf("families = %+v, want the variant %q", resolution.Families, want)
		}
	}
	if len(resolution.Processes) == 0 || resolution.Processes[0].ID != "proc:blending" {
		t.Errorf("processes = %+v, want proc:blending", resolution.Processes)
	}
	if !containsString(resolution.SearchTerms, "lubricant blending") {
		t.Errorf("search terms = %v, want the indexed lubricant phrase", resolution.SearchTerms)
	}
	// End-product leaves are what identify end-user companies.
	leaves := map[string]bool{}
	for _, leaf := range resolution.EndProducts {
		leaves[leaf.ID] = true
	}
	if !leaves["end:greases"] {
		t.Errorf("end products = %+v, want the grease-manufacturing leaf", resolution.EndProducts)
	}
}

// TestResolveQualifiedOilBlendingKeepsItsOwnFamily is the precise variant: a
// qualified oil activity must not fall back to the hypernym's dominant answer.
func TestResolveQualifiedOilBlendingKeepsItsOwnFamily(t *testing.T) {
	cases := []struct{ activity, want string }{
		{"fuel oil blending", "cat:fuel-oils"},
		{"lubricant blending", "cat:lubricants"},
		{"base oil blending", "cat:base-oils"},
		{"crude oil blending", "sub:crude-oil"},
		{"used cooking oil", "cat:oleochemical-feedstocks"},
		{"n-butanol", "cat:alcohol-solvents"},
		{"linear alkyl benzene", "cat:aromatics"},
		{"palm oil", "cat:oleochemical-feedstocks"},
	}
	for _, testCase := range cases {
		resolution := Resolve(testCase.activity)
		if len(resolution.Families) == 0 {
			t.Errorf("%s resolved to no family: %+v", testCase.activity, resolution)
			continue
		}
		if got := resolution.Families[0].ID; got != testCase.want {
			t.Errorf("%s rank 0 = %q, want %q (all: %+v)", testCase.activity, got, testCase.want, resolution.Families)
		}
	}
}

// TestResolveProcessOnlyUsesItsOutput pins the bare-process case: "esterification"
// names no product, so the family comes from what the process makes.
func TestResolveProcessOnlyUsesItsOutput(t *testing.T) {
	resolution := Resolve("esterification")
	if len(resolution.Processes) == 0 || resolution.Processes[0].ID != "proc:esterification" {
		t.Fatalf("processes = %+v, want proc:esterification", resolution.Processes)
	}
	if len(resolution.Families) == 0 || resolution.Families[0].ID != "cat:esters" {
		t.Errorf("families = %+v, want cat:esters from the process output", resolution.Families)
	}
	if resolution.Families[0].Reason != "process-output" {
		t.Errorf("reason = %q, want process-output", resolution.Families[0].Reason)
	}
}

// TestResolveUnmodelledActivityWarns pins the honest failure: an unknown activity
// resolves to nothing and says so, instead of guessing a family.
func TestResolveUnmodelledActivityWarns(t *testing.T) {
	resolution := Resolve("ABP")
	if len(resolution.Families) != 0 || len(resolution.Matches) != 0 {
		t.Errorf("resolution = %+v, want no invented match", resolution)
	}
	if len(resolution.Warnings) == 0 || !strings.Contains(strings.Join(resolution.Warnings, " "), "unmodelled") {
		t.Errorf("warnings = %v, want the unmodelled notice", resolution.Warnings)
	}
}

// TestResolveRequireApprovedGatesProposedNodes pins the post-curation switch: with
// the seed (everything proposed) nothing resolves, and once the nodes the
// activity needs are approved it resolves again.
func TestResolveRequireApprovedGatesProposedNodes(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	strict := Options{RequireApproved: true, MaxFamilies: 5, MaxHops: 2}
	if resolution := document.Resolve("Oil Blending", strict); len(resolution.Families) != 0 {
		t.Fatalf("proposed nodes resolved under RequireApproved: %+v", resolution.Families)
	}
	approved := StatusApproved
	source := SourceOperator
	for _, id := range []string{"cat:oils", "cat:lubricants", "cat:fuel-oils", "cat:oleochemical-feedstocks", "proc:blending", "sub:crude-oil", "cat:petrochemicals", "end:greases", "sub:base-oil", "cat:base-oils", "cat:fatty-acids"} {
		if node := document.Nodes[id]; node != nil {
			node.Status = approved
			node.Source = source
		}
	}
	resolution := document.Resolve("Oil Blending", strict)
	if len(resolution.Families) == 0 || resolution.Families[0].ID != "cat:lubricants" {
		t.Fatalf("approved oil blending = %+v, want lubricants first", resolution.Families)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}
