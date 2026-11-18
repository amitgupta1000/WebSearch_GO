package ontology

import (
	"strings"
	"testing"
)

// TestLintCatchesStructuralFaults pins the guards that make the submitted JSON
// safe to load: dangling references, kind mismatches, duplicate entry names,
// cycles, and the value-chain leaf requirement.
func TestLintCatchesStructuralFaults(t *testing.T) {
	base := func() *Ontology {
		return &Ontology{
			SchemaVersion: SchemaVersion,
			Revision:      "test",
			Nodes: map[string]*Node{
				"cat:root":  {Kind: KindCategory, Label: "Root", Status: StatusApproved, Source: SourceOperator},
				"cat:child": {Kind: KindCategory, Label: "Child", Parent: "cat:root", Status: StatusApproved, Source: SourceOperator},
				"sub:feed":  {Kind: KindSubstance, Label: "Feed", Categories: []string{"cat:child"}, HSCodes: []string{"1234.56"}, Status: StatusApproved, Source: SourceOperator},
				"proc:op":   {Kind: KindProcess, Label: "Op", OperatesOn: []string{"sub:feed"}, Produces: []string{"sub:out"}, Status: StatusApproved, Source: SourceOperator},
				"sub:out":   {Kind: KindSubstance, Label: "Out", Categories: []string{"cat:child"}, HSCodes: []string{"1234.57"}, Status: StatusApproved, Source: SourceOperator},
				"end:leaf":  {Kind: KindEndProduct, Label: "Leaf", Consumes: []string{"sub:out"}, OperatorPhrases: []string{"leaf maker"}, Status: StatusApproved, Source: SourceOperator},
			},
		}
	}
	if err := Validate(base()); err != nil {
		t.Fatalf("clean document must validate: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Ontology)
		want   string
	}{
		{"dangling reference", func(o *Ontology) { o.Nodes["sub:feed"].Categories = []string{"cat:nope"} }, "references unknown node"},
		{"wrong kind", func(o *Ontology) { o.Nodes["sub:feed"].Categories = []string{"sub:out"} }, "this field takes category"},
		{"duplicate alias", func(o *Ontology) { o.Nodes["sub:out"].Aliases = []string{"Feed"} }, "already an entry name"},
		{"parent cycle", func(o *Ontology) { o.Nodes["cat:root"].Parent = "cat:child" }, "parent cycle"},
		{"derived cycle", func(o *Ontology) {
			o.Nodes["sub:feed"].DerivedTo = []string{"sub:out"}
			o.Nodes["sub:out"].DerivedTo = []string{"sub:feed"}
		}, "derived_to cycle"},
		{"leaf without phrases", func(o *Ontology) { o.Nodes["end:leaf"].OperatorPhrases = nil }, "cannot drive company discovery"},
		{"rejected reference", func(o *Ontology) { o.Nodes["sub:out"].Status = StatusRejected }, "references rejected node"},
	}
	for _, testCase := range cases {
		document := base()
		testCase.mutate(document)
		found := false
		for _, issue := range Lint(document) {
			if issue.Severity == SeverityError && strings.Contains(issue.Message, testCase.want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: no error containing %q; issues = %v", testCase.name, testCase.want, Lint(document))
		}
		if err := Validate(document); err == nil {
			t.Errorf("%s: Validate accepted a broken document", testCase.name)
		}
	}
	// The ID prefix must agree with the kind.
	document := base()
	document.Nodes["wrong:id"] = &Node{Kind: KindSubstance, Label: "Wrong", Status: StatusProposed}
	if err := Validate(document); err == nil || !strings.Contains(err.Error(), "must start with") {
		t.Errorf("Validate = %v, want the ID-prefix error", err)
	}
}
