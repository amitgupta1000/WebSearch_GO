package ontology

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Marshal renders the document deterministically: schema version pinned,
// corrections cleared (the merge has already applied them), map keys sorted by
// encoding/json, and slice order preserved — hyponym order is the variant rank,
// so it must never be sorted. The canonical artifact is the output of this
// function, which is why the submitted seed and the curated file are diffable.
func Marshal(document *Ontology) ([]byte, error) {
	if document == nil {
		return nil, fmt.Errorf("cannot marshal a nil ontology")
	}
	out := Ontology{
		SchemaVersion: SchemaVersion,
		Revision:      document.Revision,
		ReviewedBy:    document.ReviewedBy,
		Notes:         document.Notes,
		Nodes:         document.Nodes,
	}
	if out.Nodes == nil {
		out.Nodes = map[string]*Node{}
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// EmitFile writes the canonical JSON to path.
func EmitFile(document *Ontology, path string) error {
	raw, err := Marshal(document)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// Emit writes the canonical JSON to w.
func Emit(document *Ontology, w io.Writer) error {
	raw, err := Marshal(document)
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

// ReviewMarkdown renders the document as the domain reviewer's worksheet. It is
// deliberately generated (never hand-edited): the JSON is the artifact that goes
// back, and this is the readable view of it.
func ReviewMarkdown(document *Ontology) string {
	var b strings.Builder
	revision := strings.TrimSpace(document.Revision)
	if revision == "" {
		revision = "(unrevisioned)"
	}
	fmt.Fprintf(&b, "# Chemical value-chain review — %s\n\n", revision)
	if document.ReviewedBy != "" {
		fmt.Fprintf(&b, "Reviewer: **%s**\n\n", document.ReviewedBy)
	}
	if document.Notes != "" {
		fmt.Fprintf(&b, "Notes: %s\n\n", document.Notes)
	}
	b.WriteString("## How to review this\n\n")
	b.WriteString("The JSON beside this file is the single source of truth; this sheet is its readable view.\n\n")
	b.WriteString("1. Every row is `status: proposed` until someone confirms it. Flip it to `approved` (or `rejected`) with a note.\n")
	b.WriteString("2. `source` records where a relation came from: `seed` (authored, unverified), `hs` (customs nomenclature), `echa`, `trade-db`, `operator` (you pinned it).\n")
	b.WriteString("3. Return **either** the whole edited JSON **or** a `corrections` block keyed by node ID — a present field replaces that field wholesale, so send the list you want to end up with.\n")
	b.WriteString("4. `derived_to` is downstream chemistry (feedstock → derivative). `consumed_in` is the value-chain edge to the process or industrial product that uses it. The leaves there drive company discovery.\n\n")

	counts := map[Kind]int{}
	byStatus := map[Status]int{}
	for _, id := range document.NodeIDs() {
		node := document.Nodes[id]
		counts[node.Kind]++
		byStatus[node.Status]++
	}
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- substances: **%d** · categories: **%d** · processes: **%d** · end-products: **%d**\n",
		counts[KindSubstance], counts[KindCategory], counts[KindProcess], counts[KindEndProduct])
	fmt.Fprintf(&b, "- status: approved **%d**, proposed **%d**, rejected **%d**\n\n",
		byStatus[StatusApproved], byStatus[StatusProposed], byStatus[StatusRejected])

	b.WriteString("## Category tree\n\n")
	roots := 0
	for _, id := range document.NodesOfKind(KindCategory) {
		node := document.Nodes[id]
		if node.Parent == "" {
			writeCategoryTree(&b, document, id, 0)
			roots++
		}
	}
	if roots == 0 {
		b.WriteString("_no root category_\n")
	}
	b.WriteString("\n")

	b.WriteString("## Substances\n\n")
	for _, id := range document.NodesOfKind(KindSubstance) {
		writeNodeBullet(&b, document, id)
	}
	b.WriteString("\n## Processes\n\n")
	for _, id := range document.NodesOfKind(KindProcess) {
		writeNodeBullet(&b, document, id)
	}
	b.WriteString("\n## End products (value-chain leaves → company discovery)\n\n")
	for _, id := range document.NodesOfKind(KindEndProduct) {
		writeNodeBullet(&b, document, id)
	}
	b.WriteString("\n## Lint findings\n\n")
	issues := SortedIssues(Lint(document))
	if len(issues) == 0 {
		b.WriteString("_clean_\n")
	} else {
		for _, issue := range issues {
			fmt.Fprintf(&b, "- `%s` %s\n", issue.Severity, issue.String())
		}
	}
	return b.String()
}

func writeCategoryTree(b *strings.Builder, document *Ontology, id string, depth int) {
	node := document.Nodes[id]
	if node == nil {
		return
	}
	indent := strings.Repeat("  ", depth)
	suffix := ""
	if node.Hypernym {
		variants := make([]string, 0, len(node.Hyponyms))
		for _, hyponym := range node.Hyponyms {
			if linked := document.Nodes[hyponym]; linked != nil {
				variants = append(variants, linked.Label)
				continue
			}
			variants = append(variants, hyponym)
		}
		suffix = " — **ambiguous**, ranked: " + strings.Join(variants, " > ")
	}
	if node.IsIntermediate {
		suffix += " _(intermediate)_"
	}
	if node.Status != StatusApproved {
		suffix += " _(" + string(node.Status) + ")_"
	}
	fmt.Fprintf(b, "%s- `%s` %s%s\n", indent, id, node.Label, suffix)
	var children []string
	for _, childID := range document.NodesOfKind(KindCategory) {
		if document.Nodes[childID].Parent == id {
			children = append(children, childID)
		}
	}
	for _, childID := range children {
		writeCategoryTree(b, document, childID, depth+1)
	}
}

func writeNodeBullet(b *strings.Builder, document *Ontology, id string) {
	node := document.Nodes[id]
	fmt.Fprintf(b, "### `%s` — %s\n", id, node.Label)
	writeField := func(label string, values []string) {
		var kept []string
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				kept = append(kept, strings.TrimSpace(value))
			}
		}
		if len(kept) == 0 {
			return
		}
		fmt.Fprintf(b, "- %s: %s\n", label, strings.Join(kept, "; "))
	}
	writeField("aliases", node.Aliases)
	writeField("hs", node.HSCodes)
	if node.Parent != "" {
		writeField("parent", []string{node.Parent})
	}
	writeField("families", node.Categories)
	writeField("derived to", node.DerivedTo)
	writeField("consumed in", node.ConsumedIn)
	writeField("operates on", node.OperatesOn)
	writeField("produces", node.Produces)
	writeField("consumes", node.Consumes)
	writeField("operator phrases", node.OperatorPhrases)
	writeField("search terms", node.SearchTerms)
	if node.Hypernym {
		writeField("ranked variants", node.Hyponyms)
	}
	meta := fmt.Sprintf("- status: **%s**", node.Status)
	if node.Source != "" {
		meta += ", source: " + node.Source
	}
	if node.Confidence != "" {
		meta += ", confidence: " + node.Confidence
	}
	if node.ExpertNote != "" {
		meta += " — note: " + node.ExpertNote
	}
	fmt.Fprintf(b, "%s\n\n", meta)
}

// IssuesMarkdown renders the lint report: errors first, then warnings, with a
// short explanation of what blocks a load.
func IssuesMarkdown(document *Ontology, issues []Issue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Ontology lint — %s\n\n", revisionOf(document))
	errors, warnings := 0, 0
	for _, issue := range issues {
		if issue.Severity == SeverityError {
			errors++
		} else {
			warnings++
		}
	}
	fmt.Fprintf(&b, "%d error(s), %d warning(s). Errors block the runtime load; warnings are the review work queue.\n\n", errors, warnings)
	if len(issues) == 0 {
		b.WriteString("_clean_\n")
		return b.String()
	}
	b.WriteString("| Severity | Node | Field | Finding |\n|---|---|---|---|\n")
	for _, issue := range SortedIssues(issues) {
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s |\n", issue.Severity, issue.Node, issue.Field, issue.Message)
	}
	return b.String()
}

// Diff reports what a reviewer changed between the submitted and the returned
// document, so the merge is auditable.
func Diff(before, after *Ontology) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Ontology diff: %s → %s\n\n", revisionOf(before), revisionOf(after))
	var added, removed []string
	for _, id := range after.NodeIDs() {
		if before.Nodes[id] == nil {
			added = append(added, id)
		}
	}
	for _, id := range before.NodeIDs() {
		if after.Nodes[id] == nil {
			removed = append(removed, id)
		}
	}
	b.WriteString("## Added\n\n")
	if len(added) == 0 {
		b.WriteString("_none_\n")
	}
	for _, id := range added {
		fmt.Fprintf(&b, "- `%s` %s (%s)\n", id, after.Nodes[id].Label, after.Nodes[id].Kind)
	}
	b.WriteString("\n## Removed\n\n")
	if len(removed) == 0 {
		b.WriteString("_none_\n")
	}
	for _, id := range removed {
		fmt.Fprintf(&b, "- `%s` %s (%s)\n", id, before.Nodes[id].Label, before.Nodes[id].Kind)
	}
	b.WriteString("\n## Changed\n\n")
	changed := 0
	for _, id := range after.NodeIDs() {
		oldNode := before.Nodes[id]
		newNode := after.Nodes[id]
		if oldNode == nil || newNode == nil {
			continue
		}
		oldFields := nodeFields(oldNode)
		newFields := nodeFields(newNode)
		var lines []string
		for i := range oldFields {
			if oldFields[i][1] != newFields[i][1] {
				lines = append(lines, fmt.Sprintf("  - %s: %q → %q", oldFields[i][0], oldFields[i][1], newFields[i][1]))
			}
		}
		if len(lines) == 0 {
			continue
		}
		changed++
		fmt.Fprintf(&b, "- `%s` %s\n%s\n", id, newNode.Label, strings.Join(lines, "\n"))
	}
	if changed == 0 {
		b.WriteString("_none_\n")
	}
	return b.String()
}

// nodeFields flattens a node into ordered, comparable strings.
func nodeFields(node *Node) [][2]string {
	return [][2]string{
		{"kind", string(node.Kind)},
		{"label", node.Label},
		{"aliases", strings.Join(node.Aliases, "; ")},
		{"hs_codes", strings.Join(node.HSCodes, "; ")},
		{"parent", node.Parent},
		{"categories", strings.Join(node.Categories, "; ")},
		{"derived_to", strings.Join(node.DerivedTo, "; ")},
		{"consumed_in", strings.Join(node.ConsumedIn, "; ")},
		{"operates_on", strings.Join(node.OperatesOn, "; ")},
		{"produces", strings.Join(node.Produces, "; ")},
		{"consumes", strings.Join(node.Consumes, "; ")},
		{"operator_phrases", strings.Join(node.OperatorPhrases, "; ")},
		{"search_terms", strings.Join(node.SearchTerms, "; ")},
		{"hyponyms", strings.Join(node.Hyponyms, "; ")},
		{"status", string(node.Status)},
		{"source", node.Source},
		{"confidence", node.Confidence},
		{"expert_note", node.ExpertNote},
	}
}

func revisionOf(document *Ontology) string {
	if document == nil || strings.TrimSpace(document.Revision) == "" {
		return "(unrevisioned)"
	}
	return document.Revision
}


