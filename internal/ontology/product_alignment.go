package ontology

// product_alignment.go aligns the Trade DB's canonical product vocabulary with
// the curated graph's entry names.
//
// The two name sets are authored independently: the DB stores "TOLUENE DI
// ISOCYANATE (TDI)" where the graph stores "Toluene diisocyanate" with the
// alias "tdi". Resolving only the operator's typed activity therefore anchors
// the run on whatever the graph happens to call it — often the broader family
// ("Isocyanates") — even after the operator picked the DB's exact product.
// This file closes that gap: the DB name is folded (case, punctuation,
// parentheticals) and matched against the graph's labels and aliases on their
// compact forms, so the pick anchors the node the graph curates.

import "strings"

// tradeProductAlignmentMinLength is the shortest compact graph entry the DB
// name may align on. Short entries are already exact aliases ("tdi", "mek"),
// so an equality test adds nothing for them.
const tradeProductAlignmentMinLength = 6

// tradeProductBase folds a DB canonical name to its comparable base: the text
// outside parentheses (trade abbreviations and grades live inside them) with
// any trailing spec tokens ("80/20" -> "80 20", "99.5" -> "99 5") removed, in
// compact form.
func tradeProductBase(value string) string {
	var b strings.Builder
	depth := 0
	for _, r := range value {
		switch {
		case r == '(' || r == '[':
			depth++
		case r == ')' || r == ']':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	tokens := strings.Fields(normalize(b.String()))
	for len(tokens) > 0 && isTradeGradeToken(tokens[len(tokens)-1]) {
		tokens = tokens[:len(tokens)-1]
	}
	return strings.Join(tokens, "")
}

// isTradeGradeToken reports whether a normalized token is a bare number, the
// shape a spec grade leaves behind ("80" and "20" from "80/20", "99" and "5"
// from "99.5").
func isTradeGradeToken(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// AlignTradeProduct resolves a trade-DB canonical product name onto the graph.
// The DB's spelling differs in two ways the graph does not share: it joins or
// splits words differently ("TOLUENE DI ISOCYANATE" for "Toluene diisocyanate")
// and it trails a trade abbreviation in parentheses ("(TDI)") with an optional
// grade ("80/20"). The base name -- parentheses and trailing grades removed --
// is compared to each approved substance's label and aliases in compact form,
// and must be equal, not merely a prefix: "ACETONE CYANOHYDRIN" must not
// resolve to "Acetone".
//
// The returned Resolution is the same shape Resolve produces, with the matched
// node in Matches (Via "trade-product") so callers can explain the anchor.
func (o *Ontology) AlignTradeProduct(name string, options Options) (Resolution, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Resolution{Activity: name}, false
	}
	if options.MaxFamilies <= 0 {
		options.MaxFamilies = DefaultOptions.MaxFamilies
	}
	if options.MaxHops <= 0 {
		options.MaxHops = DefaultOptions.MaxHops
	}
	base := tradeProductBase(name)
	if len(base) < tradeProductAlignmentMinLength {
		return Resolution{Activity: name}, false
	}
	bestID, bestPhrase, bestLength := "", "", 0
	for _, id := range o.NodesOfKind(KindSubstance) {
		node := o.Nodes[id]
		if node == nil || node.Status == StatusRejected {
			continue
		}
		if options.RequireApproved && node.Status != StatusApproved {
			continue
		}
		for _, entry := range append([]string{node.Label}, node.Aliases...) {
			entryCompact := compactName(entry)
			if len(entryCompact) < tradeProductAlignmentMinLength || entryCompact != base {
				continue
			}
			// Equal compacts are unique by the alias-uniqueness lint rule, but
			// keep the longest-entry tie-break so a shared spelling never
			// depends on map order.
			if len(entryCompact) > bestLength {
				bestID, bestPhrase, bestLength = id, entry, len(entryCompact)
			}
		}
	}
	if bestID == "" {
		return Resolution{Activity: name}, false
	}
	subject := o.Nodes[bestID]
	resolution := Resolution{
		Activity: name,
		Matches:  []Match{{ID: subject.ID, Label: subject.Label, Kind: subject.Kind, Via: "trade-product", Status: subject.Status, Phrase: bestPhrase}},
	}
	resolution.Families, resolution.Ambiguous = o.familiesFor(subject, nil, options)
	resolution.EndProducts = o.endProductsFor(subject, resolution.Families, options.MaxHops)
	resolution.SearchTerms = uniqueStrings(o.searchTermsFor(nil, resolution.Families))
	return resolution, true
}
