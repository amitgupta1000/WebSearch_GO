package ontology

import (
	"sort"
	"strings"
)

// Family is one ranked product-family candidate for an activity. Rank 0 is the
// best answer; every entry explains how it was derived so the operator (and the
// reviewer) never has to guess why a family was chosen.
type Family struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     Kind   `json:"kind"`
	Rank     int    `json:"rank"`
	Distance int    `json:"distance,omitempty"`
	// Via names the node that produced the relation.
	Via string `json:"via,omitempty"`
	// Reason is one of: subject, hypernym, category, downstream, process-output.
	Reason string `json:"reason,omitempty"`
	Status Status `json:"status"`
}

// Match is one graph node the activity text named.
type Match struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Kind   Kind   `json:"kind"`
	Via    string `json:"via"` // "label" or "alias"
	Status Status `json:"status"`
}

// Resolution is the deterministic answer to "what does this activity mean in the
// value chain": the nodes it names, the ranked families it belongs to, the
// processes it references, the industrial end-products reachable downstream (the
// company-discovery leaves), and the indexed search phrases the graph knows.
type Resolution struct {
	Activity    string   `json:"activity"`
	Matches     []Match  `json:"matches"`
	Processes   []Match  `json:"processes"`
	Families    []Family `json:"families"`
	EndProducts []Family `json:"end_products"`
	SearchTerms []string `json:"search_terms"`
	// Ambiguous is true when the activity names a hypernym ("oil") whose variants
	// are ranked in Families — the operator sees the choice, it is not silently
	// made.
	Ambiguous bool     `json:"ambiguous"`
	Warnings  []string `json:"warnings,omitempty"`
}

// Options tune resolution. RequireApproved is the post-curation gate: only nodes
// the domain expert approved may resolve.
type Options struct {
	RequireApproved bool
	MaxFamilies     int
	MaxHops         int
}

// DefaultOptions resolve every stored node (seed phase: everything is proposed)
// and walk at most the two transformation steps the landscape scope allows.
var DefaultOptions = Options{MaxFamilies: 5, MaxHops: 2}

// Resolve runs default-option resolution against the embedded document.
func Resolve(activity string) Resolution { return ResolveWith(activity, DefaultOptions) }

// ResolveWith resolves an activity against the embedded document.
func ResolveWith(activity string, options Options) Resolution {
	document, err := Load()
	if err != nil {
		return Resolution{Activity: activity, Warnings: []string{"ontology unavailable: " + err.Error()}}
	}
	return document.Resolve(activity, options)
}

// Resolve resolves one activity against this document.
func (o *Ontology) Resolve(activity string, options Options) Resolution {
	if options.MaxFamilies <= 0 {
		options.MaxFamilies = DefaultOptions.MaxFamilies
	}
	if options.MaxHops <= 0 {
		options.MaxHops = DefaultOptions.MaxHops
	}
	result := Resolution{Activity: strings.TrimSpace(activity)}
	text := " " + normalize(activity) + " "
	if normalize(activity) == "" {
		result.Warnings = append(result.Warnings, "the activity is empty")
		return result
	}
	for _, match := range o.matchEntries(text, options.RequireApproved) {
		if match.Kind == KindProcess {
			result.Processes = append(result.Processes, match)
			continue
		}
		result.Matches = append(result.Matches, match)
	}
	// The subject is the longest name the activity matched: "crude oil blending"
	// is about crude oil, not about the oil hypernym.
	subject := longestMatch(o, result.Matches)
	if subject == nil && len(result.Processes) == 0 {
		result.Warnings = append(result.Warnings, "no ontology node matches this activity; it is unmodelled — review it and add it to the graph")
		return result
	}
	result.Families, result.Ambiguous = o.familiesFor(subject, result.Processes, options)
	result.EndProducts = o.endProductsFor(subject, result.Families, options.MaxHops)
	result.SearchTerms = uniqueStrings(o.searchTermsFor(result.Processes, result.Families))
	if proposed := countProposed(result); proposed > 0 && !options.RequireApproved {
		result.Warnings = append(result.Warnings, "some matched nodes are still proposed (unreviewed); they will stop resolving once RequireApproved is enabled")
	}
	return result
}

// matchEntries finds every node whose label or alias appears in the activity as a
// whole phrase. Matching is token-boundary based, so "oil" never matches inside
// "oleochemical".
func (o *Ontology) matchEntries(text string, requireApproved bool) []Match {
	var matches []Match
	for _, id := range o.NodeIDs() {
		node := o.Nodes[id]
		if node == nil || node.Status == StatusRejected {
			continue
		}
		if requireApproved && node.Status != StatusApproved {
			continue
		}
		if matchedPhrase(text, node.Label) {
			matches = append(matches, Match{ID: id, Label: node.Label, Kind: node.Kind, Via: "label", Status: node.Status})
			continue
		}
		for _, alias := range node.Aliases {
			if matchedPhrase(text, alias) {
				matches = append(matches, Match{ID: id, Label: node.Label, Kind: node.Kind, Via: "alias", Status: node.Status})
				break
			}
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return nameLength(o.Nodes[matches[i].ID]) > nameLength(o.Nodes[matches[j].ID])
	})
	return matches
}

func matchedPhrase(text, phrase string) bool {
	key := normalize(phrase)
	if key == "" {
		return false
	}
	return strings.Contains(text, " "+key+" ")
}

// nameLength is the length of a node's longest entry name: the specificity tie
// break when an activity names several nodes at once.
func nameLength(node *Node) int {
	if node == nil {
		return 0
	}
	longest := len(normalize(node.Label))
	for _, alias := range node.Aliases {
		if length := len(normalize(alias)); length > longest {
			longest = length
		}
	}
	return longest
}

// longestMatch picks the subject: the most specific name in the activity, with a
// substance outranking a category of equal length.
func longestMatch(o *Ontology, matches []Match) *Node {
	if len(matches) == 0 {
		return nil
	}
	best := matches[0]
	bestLength := nameLength(o.Nodes[best.ID])
	for _, match := range matches[1:] {
		length := nameLength(o.Nodes[match.ID])
		if length > bestLength || (length == bestLength && match.Kind == KindSubstance && best.Kind != KindSubstance) {
			best = match
			bestLength = length
		}
	}
	return o.Nodes[best.ID]
}

// familiesFor ranks the product-family candidates. The subject decides: a
// hypernym expands to its ranked variants (the operator's "oil blending" case), a
// substance contributes its most specific family first, and its downstream
// derivatives contribute their families after that. A bare process contributes
// what it produces.
func (o *Ontology) familiesFor(subject *Node, processes []Match, options Options) ([]Family, bool) {
	var families []Family
	seen := map[string]bool{}
	ambiguous := false
	add := func(id, reason, via string) {
		if id == "" || seen[id] {
			return
		}
		node := o.Nodes[id]
		if node == nil || node.Status == StatusRejected {
			return
		}
		seen[id] = true
		families = append(families, Family{ID: id, Label: node.Label, Kind: node.Kind, Reason: reason, Via: via, Status: node.Status})
	}
	if subject != nil {
		switch {
		case subject.Hypernym && len(subject.Hyponyms) > 0:
			ambiguous = true
			for _, id := range subject.Hyponyms {
				add(id, "hypernym", subject.ID)
			}
		case subject.Kind == KindCategory:
			add(subject.ID, "subject", subject.ID)
		default:
			// Most specific family first. A top-level family ("Petrochemicals")
			// is too broad to search, so the substance stands for itself.
			candidates := append([]string(nil), subject.Categories...)
			sort.SliceStable(candidates, func(i, j int) bool { return o.depth(candidates[i]) > o.depth(candidates[j]) })
			primary := ""
			for _, id := range candidates {
				if o.depth(id) >= 3 {
					primary = id
					break
				}
			}
			if primary == "" {
				add(subject.ID, "subject", subject.ID)
			} else {
				add(primary, "category", subject.ID)
				for _, id := range candidates {
					if id != primary && o.depth(id) >= 3 {
						add(id, "category", subject.ID)
					}
				}
			}
			for _, id := range subject.DerivedTo {
				target := o.Nodes[id]
				if target == nil || target.Status == StatusRejected {
					continue
				}
				if target.Kind == KindCategory {
					add(id, "downstream", subject.ID)
					continue
				}
				if category := o.mostSpecificCategory(target); category != "" {
					add(category, "downstream", subject.ID)
				}
			}
		}
	} else {
		for _, process := range processes {
			node := o.Nodes[process.ID]
			for _, id := range node.Produces {
				target := o.Nodes[id]
				if target == nil || target.Status == StatusRejected {
					continue
				}
				if target.Kind == KindCategory {
					add(id, "process-output", process.ID)
					continue
				}
				if category := o.mostSpecificCategory(target); category != "" {
					add(category, "process-output", process.ID)
				}
			}
		}
	}
	if len(families) > options.MaxFamilies {
		families = families[:options.MaxFamilies]
	}
	for index := range families {
		families[index].Rank = index
	}
	return families, ambiguous
}

// mostSpecificCategory returns the deepest family a node declares that is
// specific enough to search (top-level families are too broad), or "".
func (o *Ontology) mostSpecificCategory(node *Node) string {
	best := ""
	bestDepth := 2 // a family must sit below the top level to be searchable
	for _, id := range node.Categories {
		if depth := o.depth(id); depth > bestDepth {
			best, bestDepth = id, depth
		}
	}
	return best
}

// depth counts how far a category sits below the root (root = 1, its children = 2).
func (o *Ontology) depth(id string) int {
	depth := 0
	seen := map[string]bool{}
	for current := id; current != "" && !seen[current]; {
		seen[current] = true
		depth++
		node := o.Nodes[current]
		if node == nil {
			break
		}
		current = strings.TrimSpace(node.Parent)
	}
	return depth
}

// endProductsFor walks the value chain downstream from the subject and its
// families (derived_to, then consumed_in) and returns the industrial-product
// leaves that identify end-user companies, with the hop distance that found them.
func (o *Ontology) endProductsFor(subject *Node, families []Family, maxHops int) []Family {
	type step struct {
		id       string
		distance int
	}
	queue := make([]step, 0, 8)
	if subject != nil {
		queue = append(queue, step{id: subject.ID})
	}
	for _, family := range families {
		queue = append(queue, step{id: family.ID})
	}
	visited := map[string]bool{}
	var leaves []Family
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.id == "" || visited[current.id] {
			continue
		}
		visited[current.id] = true
		node := o.Nodes[current.id]
		if node == nil || node.Status == StatusRejected {
			continue
		}
		if node.Kind == KindEndProduct {
			if len(leaves) < 6 {
				leaves = append(leaves, Family{
					ID: current.id, Label: node.Label, Kind: node.Kind, Rank: len(leaves),
					Distance: current.distance, Reason: "value-chain leaf", Status: node.Status,
				})
			}
			continue
		}
		if current.distance >= maxHops {
			continue
		}
		for _, id := range append(append([]string(nil), node.DerivedTo...), node.ConsumedIn...) {
			queue = append(queue, step{id: id, distance: current.distance + 1})
		}
	}
	return leaves
}

// searchTermsFor returns the indexed phrases the graph knows: the matched
// processes' own search terms first, then the family labels.
func (o *Ontology) searchTermsFor(processes []Match, families []Family) []string {
	terms := make([]string, 0, len(processes)*3+len(families))
	for _, process := range processes {
		if node := o.Nodes[process.ID]; node != nil {
			terms = append(terms, node.SearchTerms...)
		}
	}
	for _, family := range families {
		terms = append(terms, family.Label)
	}
	return terms
}

func countProposed(result Resolution) int {
	count := 0
	for _, match := range append(append([]Match(nil), result.Matches...), result.Processes...) {
		if match.Status != StatusApproved {
			count++
		}
	}
	for _, family := range append(append([]Family(nil), result.Families...), result.EndProducts...) {
		if family.Status != StatusApproved {
			count++
		}
	}
	return count
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[strings.ToLower(value)] {
			continue
		}
		seen[strings.ToLower(value)] = true
		out = append(out, value)
	}
	return out
}



