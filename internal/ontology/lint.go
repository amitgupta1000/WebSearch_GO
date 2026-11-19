package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// Severity separates a document the runtime may load from one it must not.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Issue is one lint finding. Node and Field are empty for document-level ones.
type Issue struct {
	Severity Severity `json:"severity"`
	Node     string   `json:"node,omitempty"`
	Field    string   `json:"field,omitempty"`
	Message  string   `json:"message"`
}

func (i Issue) String() string {
	location := i.Node
	if i.Field != "" {
		location += "." + i.Field
	}
	if location == "" {
		return fmt.Sprintf("%s: %s", i.Severity, i.Message)
	}
	return fmt.Sprintf("%s: %s: %s", i.Severity, location, i.Message)
}

// Validate returns a non-nil error when the document has lint errors. Warnings
// (proposed-only nodes, missing HS codes, …) do not block a load: they are the
// reviewer's work queue, not a runtime fault.
func Validate(document *Ontology) error {
	var problems []Issue
	for _, issue := range Lint(document) {
		if issue.Severity == SeverityError {
			problems = append(problems, issue)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	lines := make([]string, 0, len(problems)+1)
	lines = append(lines, fmt.Sprintf("ontology has %d error(s):", len(problems)))
	for _, issue := range problems {
		lines = append(lines, "  "+issue.String())
	}
	return fmt.Errorf("%s", strings.Join(lines, "\n"))
}

// refFields maps a reference field to the node kinds it may point at. It is the
// schema's shape rule, in one place.
var refFields = []struct {
	field string
	value func(*Node) []string
	kinds []Kind
}{
	{"parent", func(n *Node) []string { return []string{n.Parent} }, []Kind{KindCategory}},
	{"categories", func(n *Node) []string { return n.Categories }, []Kind{KindCategory}},
	{"derived_to", func(n *Node) []string { return n.DerivedTo }, []Kind{KindSubstance, KindCategory}},
	{"consumed_in", func(n *Node) []string { return n.ConsumedIn }, []Kind{KindProcess, KindEndProduct}},
	{"operates_on", func(n *Node) []string { return n.OperatesOn }, []Kind{KindSubstance, KindCategory}},
	{"produces", func(n *Node) []string { return n.Produces }, []Kind{KindSubstance, KindCategory, KindEndProduct}},
	{"consumes", func(n *Node) []string { return n.Consumes }, []Kind{KindSubstance, KindCategory}},
	{"hyponyms", func(n *Node) []string { return n.Hyponyms }, []Kind{KindSubstance, KindCategory}},
}

var kindPrefixes = map[Kind]string{
	KindSubstance:  "sub:",
	KindCategory:   "cat:",
	KindProcess:    "proc:",
	KindEndProduct: "end:",
}

var allowedSources = map[string]bool{
	SourceSeed: true, SourceLLMReview: true, SourceHS: true, SourceECHA: true, SourceOperator: true, SourceTradeDB: true,
}

var allowedConfidence = map[string]bool{
	"": true, ConfidenceHigh: true, ConfidenceMedium: true, ConfidenceLow: true,
}

// Lint inspects the whole document. The ordering is stable so the review
// artifact and CI output are diffable.
func Lint(document *Ontology) []Issue {
	var issues []Issue
	add := func(severity Severity, node, field, message string, args ...any) {
		issues = append(issues, Issue{Severity: severity, Node: node, Field: field, Message: fmt.Sprintf(message, args...)})
	}
	if document == nil {
		add(SeverityError, "", "", "document is nil")
		return issues
	}
	if len(document.Nodes) == 0 {
		add(SeverityError, "", "nodes", "the document has no nodes")
		return issues
	}
	entryNames := map[string]string{}
	hsCodes := map[string]string{}
	proposed := 0
	roots := 0
	for _, id := range document.NodeIDs() {
		node := document.Nodes[id]
		if node == nil {
			add(SeverityError, id, "", "node is null")
			continue
		}
		prefix, known := kindPrefixes[node.Kind]
		if !known {
			add(SeverityError, id, "kind", "unknown kind %q", node.Kind)
		} else if !strings.HasPrefix(id, prefix) {
			add(SeverityError, id, "kind", "ID must start with %q for a %s", prefix, node.Kind)
		}
		if strings.TrimSpace(node.Label) == "" {
			add(SeverityError, id, "label", "label is empty")
		}
		switch node.Status {
		case StatusApproved, StatusRejected:
		case StatusProposed:
			proposed++
		default:
			add(SeverityError, id, "status", "unknown status %q", node.Status)
		}
		if node.Source != "" && !allowedSources[node.Source] {
			add(SeverityError, id, "source", "unknown source %q", node.Source)
		}
		if !allowedConfidence[node.Confidence] {
			add(SeverityError, id, "confidence", "unknown confidence %q", node.Confidence)
		}
		// Entry names must be unambiguous: a second node answering to the same
		// label or alias is exactly the shared-word failure the graph exists to
		// prevent.
		for _, name := range append([]string{node.Label}, node.Aliases...) {
			key := normalize(name)
			if key == "" {
				continue
			}
			if owner, taken := entryNames[key]; taken && owner != id {
				add(SeverityError, id, "aliases", "%q is already an entry name of %s", name, owner)
				continue
			}
			entryNames[key] = id
		}
		for _, ref := range refFields {
			for _, target := range ref.value(node) {
				target = strings.TrimSpace(target)
				if target == "" {
					continue
				}
				if ref.field == "parent" && target == id {
					add(SeverityError, id, "parent", "node is its own parent")
				}
				linked := document.Nodes[target]
				if linked == nil {
					add(SeverityError, id, ref.field, "references unknown node %q", target)
					continue
				}
				if !kindAllowed(ref.kinds, linked.Kind) {
					add(SeverityError, id, ref.field, "%q is a %s; this field takes %s", target, linked.Kind, kindList(ref.kinds))
				}
				if linked.Status == StatusRejected && node.Status != StatusRejected {
					add(SeverityError, id, ref.field, "references rejected node %q", target)
				}
			}
		}
		if node.Kind == KindCategory && node.Parent == "" {
			roots++
		}
		if node.Kind == KindSubstance && len(node.Categories) == 0 {
			add(SeverityWarning, id, "categories", "substance belongs to no category, so it cannot expand")
		}
		if node.Kind == KindSubstance && len(node.HSCodes) == 0 {
			add(SeverityWarning, id, "hs_codes", "no HS code; the trade-DB match cannot use it")
		}
		if node.Kind == KindEndProduct && len(node.OperatorPhrases) == 0 {
			add(SeverityError, id, "operator_phrases", "an end-product without operator phrases cannot drive company discovery")
		}
		if node.Kind == KindProcess && len(node.OperatesOn) == 0 {
			add(SeverityWarning, id, "operates_on", "process names no input")
		}
		if node.Kind == KindProcess && len(node.Produces) == 0 {
			add(SeverityWarning, id, "produces", "process names no output")
		}
		if node.Hypernym {
			if len(node.Hyponyms) < 2 {
				add(SeverityError, id, "hyponyms", "a hypernym needs at least two ranked hyponyms")
			}
			if node.Kind != KindCategory {
				add(SeverityError, id, "hypernym", "only a category can be a hypernym")
			}
		} else if len(node.Hyponyms) > 0 {
			add(SeverityError, id, "hyponyms", "hyponyms without hypernym")
		}
		for _, code := range node.HSCodes {
			key := strings.TrimSpace(code)
			if key == "" {
				continue
			}
			if owner, taken := hsCodes[key]; taken && owner != id {
				add(SeverityWarning, id, "hs_codes", "HS code %s is also used by %s", code, owner)
				continue
			}
			hsCodes[key] = id
		}
	}
	for _, id := range document.NodeIDs() {
		if cycle := parentCycle(document, id); len(cycle) > 0 {
			add(SeverityError, id, "parent", "parent cycle: %s", strings.Join(cycle, " → "))
		}
		if cycle := derivedCycle(document, id); len(cycle) > 0 {
			add(SeverityError, id, "derived_to", "derived_to cycle: %s", strings.Join(cycle, " → "))
		}
	}
	if proposed > 0 {
		add(SeverityWarning, "", "", "%d of %d node(s) are proposed and will not resolve until reviewed", proposed, len(document.Nodes))
	}
	if roots == 0 {
		add(SeverityWarning, "", "categories", "no parentless category: the tree has no root")
	}
	// A one-word substance entry that opens another substance's entry name is the
	// data-side hazard behind the "crude palm oil" mis-resolution: the longer
	// phrase names the other node, so the short alias cannot also be read there.
	// Resolution now prefers the longer phrase; this warning keeps the alias in
	// the review queue so the next curator does not silently re-add it.
	type modifierEntry struct{ id, name, field string }
	var modifiers []modifierEntry
	for _, id := range document.NodeIDs() {
		node := document.Nodes[id]
		if node == nil || node.Status == StatusRejected || node.Kind != KindSubstance {
			continue
		}
		if len(strings.Fields(normalize(node.Label))) == 1 {
			modifiers = append(modifiers, modifierEntry{id: id, name: node.Label, field: "label"})
		}
		for _, alias := range node.Aliases {
			if len(strings.Fields(normalize(alias))) == 1 {
				modifiers = append(modifiers, modifierEntry{id: id, name: alias, field: "aliases"})
			}
		}
	}
	for _, narrow := range modifiers {
		narrowToken := strings.Fields(normalize(narrow.name))[0]
		for _, id := range document.NodeIDs() {
			if id == narrow.id {
				continue
			}
			node := document.Nodes[id]
			if node == nil || node.Status == StatusRejected || node.Kind != KindSubstance {
				continue
			}
			for _, name := range append([]string{node.Label}, node.Aliases...) {
				tokens := strings.Fields(normalize(name))
				if len(tokens) < 2 || tokens[0] != narrowToken {
					continue
				}
				add(SeverityWarning, narrow.id, narrow.field, "%q also opens %s's entry name %q, which resolves to that node; a one-word modifier is not a separate product there", narrow.name, id, name)
			}
		}
	}
	return issues
}

func kindAllowed(allowed []Kind, kind Kind) bool {
	for _, candidate := range allowed {
		if candidate == kind {
			return true
		}
	}
	return false
}

func kindList(kinds []Kind) string {
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, string(kind))
	}
	return strings.Join(names, "|")
}

func parentCycle(document *Ontology, start string) []string {
	seen := map[string]bool{}
	var path []string
	for current := start; current != ""; {
		if seen[current] {
			return append(path, current)
		}
		seen[current] = true
		path = append(path, current)
		node := document.Nodes[current]
		if node == nil {
			return nil
		}
		current = strings.TrimSpace(node.Parent)
	}
	return nil
}

func derivedCycle(document *Ontology, start string) []string {
	state := map[string]int{}
	var stack []string
	var walk func(string) []string
	walk = func(id string) []string {
		switch state[id] {
		case 1:
			return append(append([]string{}, stack...), id)
		case 2:
			return nil
		}
		node := document.Nodes[id]
		if node == nil {
			return nil
		}
		state[id] = 1
		stack = append(stack, id)
		for _, next := range node.DerivedTo {
			if cycle := walk(strings.TrimSpace(next)); len(cycle) > 0 {
				return cycle
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
		return nil
	}
	return walk(start)
}

// SortedIssues orders issues errors-first for a readable review artifact.
func SortedIssues(issues []Issue) []Issue {
	sorted := append([]Issue(nil), issues...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Severity != sorted[j].Severity {
			return sorted[i].Severity == SeverityError
		}
		if sorted[i].Node != sorted[j].Node {
			return sorted[i].Node < sorted[j].Node
		}
		return sorted[i].Field < sorted[j].Field
	})
	return sorted
}

