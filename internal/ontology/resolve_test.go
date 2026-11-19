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
		{"crude palm oil", "cat:oleochemical-feedstocks"},
		{"crude palm kernel oil", "cat:oleochemical-feedstocks"},
		{"rbd palm oil", "cat:oleochemical-feedstocks"},
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

// TestRequireApprovedGateExcludesUnreviewedNodes pins the runtime gate: a node the
// review has not approved does not resolve, and the warning says "approve", not
// "unmodelled". Ungated resolution still sees it.
func TestRequireApprovedGateExcludesUnreviewedNodes(t *testing.T) {
	approved := StatusApproved
	document := &Ontology{
		SchemaVersion: SchemaVersion,
		Revision:      "test",
		Nodes: map[string]*Node{
			"cat:family":   {ID: "cat:family", Kind: KindCategory, Label: "Family", Status: approved},
			"sub:reviewed": {ID: "sub:reviewed", Kind: KindSubstance, Label: "Reviewed feed", Categories: []string{"cat:family"}, HSCodes: []string{"1111.11"}, Status: approved},
			"sub:pending":  {ID: "sub:pending", Kind: KindSubstance, Label: "Pending feed", Categories: []string{"cat:family"}, HSCodes: []string{"2222.22"}, Status: StatusProposed},
		},
	}
	strict := Options{RequireApproved: true, MaxFamilies: 5, MaxHops: 2}
	if resolution := document.Resolve("reviewed feed", strict); len(resolution.Families) == 0 {
		t.Fatalf("approved node did not resolve: %+v", resolution)
	}
	pending := document.Resolve("pending feed", strict)
	if len(pending.Families) != 0 || len(pending.Matches) != 0 {
		t.Errorf("proposed node resolved under the gate: %+v", pending)
	}
	joined := strings.Join(pending.Warnings, " ")
	if !strings.Contains(joined, "not approved") {
		t.Errorf("warnings = %v, want the approval hint", pending.Warnings)
	}
	if strings.Contains(joined, "unmodelled") {
		t.Errorf("warnings = %v, an unapproved node must not be reported as unmodelled", pending.Warnings)
	}
	if resolution := document.Resolve("pending feed", Options{MaxFamilies: 5, MaxHops: 2}); len(resolution.Families) == 0 {
		t.Errorf("ungated resolution should still see the proposed node: %+v", resolution)
	}
}

// TestApproveMarksProposedNodesOnly pins the sign-off helper: proposed nodes
// become approved, rejected ones stay rejected, and the revision records it once.
func TestApproveMarksProposedNodesOnly(t *testing.T) {
	document := &Ontology{
		SchemaVersion: SchemaVersion,
		Revision:      "curated-1",
		Nodes: map[string]*Node{
			"cat:root":     {ID: "cat:root", Kind: KindCategory, Label: "Root", Status: StatusProposed},
			"sub:keep":     {ID: "sub:keep", Kind: KindSubstance, Label: "Keep", Categories: []string{"cat:root"}, HSCodes: []string{"1111.11"}, Status: StatusRejected},
			"sub:approved": {ID: "sub:approved", Kind: KindSubstance, Label: "Approved", Categories: []string{"cat:root"}, HSCodes: []string{"2222.22"}, Status: StatusApproved},
		},
	}
	if got := Approve(document, "operator"); got != 1 {
		t.Errorf("approved %d, want 1", got)
	}
	if document.Nodes["cat:root"].Status != StatusApproved {
		t.Error("root was not approved")
	}
	if document.Nodes["sub:keep"].Status != StatusRejected {
		t.Error("a rejection must survive approval")
	}
	if document.ReviewedBy != "operator" || document.Revision != "curated-1-approved" {
		t.Errorf("metadata = %q/%q", document.ReviewedBy, document.Revision)
	}
	if got := Approve(document, "operator"); got != 0 || document.Revision != "curated-1-approved" {
		t.Errorf("re-approval = %d/%q, want idempotent", got, document.Revision)
	}
	if pending := document.PendingApproval(); len(pending) != 0 {
		t.Errorf("pending = %v, want none", pending)
	}
}

// TestResolvePrefersThePhraseThatMatched pins the operator's report: "crude palm
// oil" is a palm-oil grade, not a mention of crude oil through the generic alias
// "crude". Specificity is read from the matched phrase (two words beat one), and
// a phrase absorbed by a longer match is not a second product.
func TestResolvePrefersThePhraseThatMatched(t *testing.T) {
	for _, activity := range []string{"crude palm oil", "CRUDE PALM OIL", "Crude Palm Oil"} {
		resolution := Resolve(activity)
		if len(resolution.Matches) == 0 || resolution.Matches[0].ID != "sub:palm-oil" {
			t.Fatalf("%s matches = %+v, want sub:palm-oil first", activity, resolution.Matches)
		}
		for _, match := range resolution.Matches {
			if match.ID == "sub:crude-oil" {
				t.Errorf("%s matches = %+v: the absorbed alias \"crude\" must not survive as a second product", activity, resolution.Matches)
			}
		}
		if len(resolution.Families) == 0 || resolution.Families[0].ID != "cat:oleochemical-feedstocks" {
			t.Errorf("%s families = %+v, want cat:oleochemical-feedstocks", activity, resolution.Families)
		}
	}
	// The plain product keeps resolving to itself: the fix must not trade one
	// drift for another.
	if resolution := Resolve("crude oil"); len(resolution.Matches) == 0 || resolution.Matches[0].ID != "sub:crude-oil" {
		t.Errorf("crude oil matches = %+v, want sub:crude-oil", resolution.Matches)
	}
	// A bare modifier is never a product of its own.
	if resolution := Resolve("crude glycerin"); len(resolution.Matches) != 1 || resolution.Matches[0].ID != "sub:glycerin" {
		t.Errorf("crude glycerin matches = %+v, want only sub:glycerin", resolution.Matches)
	}
}

// TestEveryEntryNameResolvesToItsOwnNode is the adversarial sweep the hand-written
// tables lacked: it derives one case per entry name in the graph, so a generic
// alias introduced anywhere ("crude" on crude oil, against "crude palm oil" on
// palm oil) is caught without anyone thinking to write the pair down. Resolving a
// complete entry name must name that entry's node, in either direction of the
// family tree.
func TestEveryEntryNameResolvesToItsOwnNode(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	checked := 0
	for _, id := range document.NodeIDs() {
		node := document.Nodes[id]
		if node == nil || node.Status != StatusApproved {
			continue
		}
		for _, entry := range append([]string{node.Label}, node.Aliases...) {
			if len(strings.Fields(normalize(entry))) < 2 {
				// A single word is legitimately shared ("oil", "crude"): this
				// sweep is about phrases, where a longer reading always exists.
				continue
			}
			checked++
			resolution := document.Resolve(entry, DefaultOptions)
			if node.Kind == KindProcess {
				if len(resolution.Processes) == 0 || resolution.Processes[0].ID != id {
					t.Errorf("%q (an entry name of %s) named processes %+v", entry, id, resolution.Processes)
				}
				continue
			}
			if len(resolution.Matches) == 0 || resolution.Matches[0].ID != id {
				t.Errorf("%q (an entry name of %s) resolved to %+v", entry, id, resolution.Matches)
			}
		}
	}
	if checked < 50 {
		t.Fatalf("checked %d entry names, want the full sweep", checked)
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

// TestResolveDescriptiveLabels pins the review finding that descriptive labels
// must still answer the activity people type: "saponification" resolves through
// the label "Saponification and fatty acid neutralization", and "soap" resolves
// to the "Soap bases" family.
func TestResolveDescriptiveLabels(t *testing.T) {
	approved := StatusApproved
	document := &Ontology{
		SchemaVersion: SchemaVersion,
		Revision:      "test",
		Nodes: map[string]*Node{
			"cat:oleochemicals": {ID: "cat:oleochemicals", Kind: KindCategory, Label: "Oleochemicals", Status: approved},
			"cat:soap":          {ID: "cat:soap", Kind: KindCategory, Label: "Soap bases", Parent: "cat:oleochemicals", Status: approved},
			"sub:fat":           {ID: "sub:fat", Kind: KindSubstance, Label: "Fatty acids", Categories: []string{"cat:oleochemicals"}, HSCodes: []string{"3823.11"}, Status: approved},
			"proc:sap": {
				ID: "proc:sap", Kind: KindProcess, Label: "Saponification and fatty acid neutralization",
				Aliases: []string{"saponify"}, OperatesOn: []string{"sub:fat"}, Produces: []string{"cat:soap"}, Status: approved,
			},
			"sub:hydrogen":          {ID: "sub:hydrogen", Kind: KindSubstance, Label: "Hydrogen", Categories: []string{"cat:oleochemicals"}, HSCodes: []string{"2804.10"}, Status: approved},
			"sub:hydrogen-peroxide": {ID: "sub:hydrogen-peroxide", Kind: KindSubstance, Label: "Hydrogen peroxide", Categories: []string{"cat:oleochemicals"}, HSCodes: []string{"2847.00"}, Status: approved},
		},
	}
	options := Options{MaxFamilies: 5, MaxHops: 2}

	resolution := document.Resolve("saponification", options)
	if len(resolution.Processes) == 0 || resolution.Processes[0].ID != "proc:sap" {
		t.Fatalf("processes = %+v, want proc:sap via the leading name", resolution.Processes)
	}
	if len(resolution.Families) == 0 || resolution.Families[0].ID != "cat:soap" {
		t.Errorf("families = %+v, want cat:soap from the process output", resolution.Families)
	}
	if got := document.Resolve("soap", options); len(got.Families) == 0 || got.Families[0].ID != "cat:soap" {
		t.Errorf("soap families = %+v, want cat:soap", got.Families)
	}
	// Precision guard: an exact substance name must never drift to a longer name
	// that merely starts with it.
	hydrogen := document.Resolve("hydrogen", options)
	if len(hydrogen.Matches) != 1 || hydrogen.Matches[0].ID != "sub:hydrogen" {
		t.Errorf("hydrogen matches = %+v, want only sub:hydrogen", hydrogen.Matches)
	}
}

// TestLintAcceptsMachineReviewProvenance pins the review's provenance value: a
// machine review that corrects a row records source=llm-review while the status
// stays proposed.
func TestLintAcceptsMachineReviewProvenance(t *testing.T) {
	document := &Ontology{
		SchemaVersion: SchemaVersion,
		Revision:      "test",
		Nodes: map[string]*Node{
			"cat:root": {ID: "cat:root", Kind: KindCategory, Label: "Root", Status: StatusProposed, Source: SourceLLMReview},
			"sub:one": {
				ID: "sub:one", Kind: KindSubstance, Label: "One", Categories: []string{"cat:root"},
				HSCodes: []string{"1234.56"}, Status: StatusProposed, Source: SourceLLMReview,
			},
		},
	}
	if err := Validate(document); err != nil {
		t.Fatalf("llm-review provenance must validate: %v", err)
	}
}

