package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// Harvest turns the trade DB's canonical product vocabulary into proposed
// substance additions. It is how the operator's first tier stays aligned with
// what the customs data actually standardizes to: names already covered by a
// label or alias are skipped, and the rest arrive as `status: proposed`,
// `source: trade-db`, with no family — the reviewer assigns families, and the
// linter keeps them visible as warnings until they do.
func Harvest(base *Ontology, names []string) (map[string]*Correction, []string) {
	corrections := map[string]*Correction{}
	skipped := make([]string, 0)
	for _, name := range names {
		label := strings.TrimSpace(name)
		if label == "" {
			continue
		}
		if len(base.Lookup(label)) > 0 {
			skipped = append(skipped, label)
			continue
		}
		id := "sub:" + Slug(label)
		if _, exists := base.Nodes[id]; exists || corrections[id] != nil {
			skipped = append(skipped, label)
			continue
		}
		kind := KindSubstance
		text := label
		status := StatusProposed
		source := SourceTradeDB
		corrections[id] = &Correction{
			Kind:   &kind,
			Label:  &text,
			Status: &status,
			Source: &source,
		}
	}
	sort.Strings(skipped)
	return corrections, skipped
}

// Slug builds a stable node ID fragment from a label: lowercase alphanumerics
// joined by single dashes. "CAUSTIC SODA" → "caustic-soda".
func Slug(label string) string {
	var b strings.Builder
	b.Grow(len(label))
	gap := false
	for _, r := range strings.ToLower(strings.TrimSpace(label)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			gap = false
		default:
			if !gap && b.Len() > 0 {
				b.WriteByte('-')
				gap = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// HarvestDocument wraps harvested corrections as a document whose nodes are
// empty, so `merge -base data/chemicals.json -patch harvested.json` folds them
// onto the reviewed base without touching it by hand.
func HarvestDocument(revision string, corrections map[string]*Correction) *Ontology {
	if strings.TrimSpace(revision) == "" {
		revision = "trade-db-harvest"
	}
	return &Ontology{
		SchemaVersion: SchemaVersion,
		Revision:      revision,
		Notes:         fmt.Sprintf("Trade DB canonical product names harvested as proposed substances (%d).", len(corrections)),
		Nodes:         map[string]*Node{},
		Corrections:   corrections,
	}
}
