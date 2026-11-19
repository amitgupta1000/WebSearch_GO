package ontology

import (
	"fmt"
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
	Via    string `json:"via"` // "label", "alias", "search-term", "joined" or "leading"
	Status Status `json:"status"`
	// Phrase is the entry name that matched the activity text. Specificity is
	// read from it, never from the node's longest entry: the generic alias
	// "crude" must not outrank the phrase "crude palm oil" it merely sits in.
	Phrase string `json:"phrase,omitempty"`
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

// Options tune resolution. RequireApproved is the runtime gate: only nodes the
// review has approved may resolve, so unreviewed chemistry can never reach a
// search. It is on by default — the shipped document is approved.
type Options struct {
	RequireApproved bool
	MaxFamilies     int
	MaxHops         int
}

// DefaultOptions resolve only approved nodes (the review is complete) and walk at
// most the two transformation steps the landscape scope allows.
var DefaultOptions = Options{RequireApproved: true, MaxFamilies: 5, MaxHops: 2}

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
	// A phrase absorbed by a longer match is not a second product: "crude palm
	// oil" is palm oil, never a mention of crude oil through the alias "crude".
	result.Matches = suppressContainedMatches(result.Matches)
	// The subject is the most specific phrase the activity matched: "crude oil
	// blending" is about crude oil, and "crude palm oil" about palm oil.
	subject := longestMatch(o, result.Matches)
	if subject == nil && len(result.Processes) == 0 {
		// Distinguish "not in the graph" from "in the graph but not approved":
		// the operator needs to know whether to curate or to approve.
		if options.RequireApproved {
			if pending := o.pendingMatches(text); pending > 0 {
				result.Warnings = append(result.Warnings, fmt.Sprintf("%d matching node(s) are not approved yet and were excluded; approve them before they can resolve", pending))
				return result
			}
		}
		result.Warnings = append(result.Warnings, "no ontology node matches this activity; it is unmodelled — review it and add it to the graph")
		return result
	}
	if options.RequireApproved {
		if pending := o.pendingMatches(text); pending > 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%d matching node(s) are not approved yet and were excluded; approve them before they can resolve", pending))
		}
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
//
// When no product matched but the activity is the leading token sequence of a
// category or process name, that node matches too: descriptive labels
// ("Saponification and fatty acid neutralization") are what a reviewer writes, and
// the activity people type is the head ("saponification"). The fallback is
// deliberately narrow — it never applies to substances, so "hydrogen" cannot drift
// to "Hydrogen peroxide", and it never runs when a product already matched.
func (o *Ontology) matchEntries(text string, requireApproved bool) []Match {
	matches := o.exactEntryMatches(text, requireApproved)
	for _, match := range matches {
		if match.Kind != KindProcess {
			return sortedMatches(o, matches)
		}
	}
	phrase := strings.TrimSpace(text)
	for _, id := range o.NodeIDs() {
		node := o.Nodes[id]
		if node == nil || (node.Kind != KindCategory && node.Kind != KindProcess) {
			continue
		}
		if node.Status == StatusRejected || (requireApproved && node.Status != StatusApproved) {
			continue
		}
		if leadingTokens(phrase, node.Label) || anyLeadingTokens(phrase, node.Aliases) {
			matches = append(matches, Match{ID: id, Label: node.Label, Kind: node.Kind, Via: "leading", Status: node.Status, Phrase: phrase})
		}
	}
	return sortedMatches(o, matches)
}

// exactEntryMatches is the whole-phrase pass over entry names: a node's label and
// aliases, plus — for processes — its search terms, because those are the indexed
// phrasing of the activity itself ("oil blending" names the blending process).
func (o *Ontology) exactEntryMatches(text string, requireApproved bool) []Match {
	var matches []Match
	// A joined spelling ("toluenediisocyanate") is one token after normalization;
	// only that shape takes the compact pass below, so two separate names can
	// never merge into one.
	normalized := strings.TrimSpace(text)
	singleToken := normalized != "" && !strings.Contains(normalized, " ")
	for _, id := range o.NodeIDs() {
		node := o.Nodes[id]
		if node == nil || node.Status == StatusRejected {
			continue
		}
		if requireApproved && node.Status != StatusApproved {
			continue
		}
		// The longest entry that matches names the node: "crude palm oil"
		// reaches palm oil through its alias even though the label "Palm oil"
		// matches the same text too. Recording the phrase -- not the node -- is
		// what lets the ranking see that the one-token alias "crude" is the
		// least specific reading of this activity.
		phrase, via := "", ""
		consider := func(entry, entryVia string) {
			if !matchedPhrase(text, entry) {
				return
			}
			if phrase == "" || len(normalize(entry)) > len(normalize(phrase)) {
				phrase, via = entry, entryVia
			}
		}
		consider(node.Label, "label")
		for _, alias := range node.Aliases {
			consider(alias, "alias")
		}
		if phrase == "" && singleToken && joinedEntryName(normalized, node) {
			// The activity is an entry name with its spaces folded away: the
			// spelling people type ("toluenediisocyanate", "styrenemonomer").
			phrase, via = normalized, "joined"
		}
		if phrase == "" && node.Kind == KindProcess {
			for _, term := range node.SearchTerms {
				consider(term, "search-term")
			}
		}
		if phrase != "" {
			matches = append(matches, Match{ID: id, Label: node.Label, Kind: node.Kind, Via: via, Status: node.Status, Phrase: phrase})
		}
	}
	return matches
}

func sortedMatches(o *Ontology, matches []Match) []Match {
	sort.SliceStable(matches, func(i, j int) bool {
		left, right := matches[i], matches[j]
		if leftTokens, rightTokens := matchTokens(left), matchTokens(right); leftTokens != rightTokens {
			return leftTokens > rightTokens
		}
		if leftChars, rightChars := len(normalize(left.Phrase)), len(normalize(right.Phrase)); leftChars != rightChars {
			return leftChars > rightChars
		}
		if (left.Kind == KindSubstance) != (right.Kind == KindSubstance) {
			return left.Kind == KindSubstance
		}
		if (left.Via == "leading") != (right.Via == "leading") {
			// A leading match only says the activity opens a longer descriptive
			// name; an exact entry name claims the whole phrase, so it wins.
			return right.Via == "leading"
		}
		if leftLength, rightLength := nameLength(o.Nodes[left.ID]), nameLength(o.Nodes[right.ID]); leftLength != rightLength {
			return leftLength > rightLength
		}
		return left.Label < right.Label
	})
	return matches
}

// matchTokens counts the words of the phrase that matched: the first measure of
// specificity, because a phrase naming two words claims more of the activity
// than a generic one-word alias.
func matchTokens(match Match) int {
	return len(strings.Fields(normalize(match.Phrase)))
}

// suppressContainedMatches drops a match whose phrase is only part of the span a
// longer match claimed. "crude palm oil" names palm oil through the entry "crude
// palm oil"; the one-token alias "crude" on the crude-oil node lies inside that
// span, so the activity did not name a second product. A one-token match is also
// dropped when a multi-token match shares the activity: a bare modifier is never
// an independent product reading ("crude glycerin" is a glycerin grade, not a
// crude-oil mention).
func suppressContainedMatches(matches []Match) []Match {
	if len(matches) < 2 {
		return matches
	}
	longest := 0
	for _, match := range matches {
		if tokens := matchTokens(match); tokens > longest {
			longest = tokens
		}
	}
	kept := make([]Match, 0, len(matches))
	for _, match := range matches {
		key := normalize(match.Phrase)
		absorbed := longest >= 2 && matchTokens(match) == 1
		if !absorbed && key != "" {
			for _, other := range matches {
				if other.ID == match.ID {
					continue
				}
				container := normalize(other.Phrase)
				if len(container) > len(key) && strings.Contains(" "+container+" ", " "+key+" ") {
					absorbed = true
					break
				}
			}
		}
		if !absorbed {
			kept = append(kept, match)
		}
	}
	return kept
}

// leadingTokens reports whether the activity phrase opens the name, token by
// token ("saponification" opens "Saponification and fatty acid neutralization";
// "ester" does not open "Esters", because tokens are compared whole).
func leadingTokens(phrase, name string) bool {
	needle := strings.Fields(normalize(phrase))
	hay := strings.Fields(normalize(name))
	if len(needle) == 0 || len(hay) < len(needle) {
		return false
	}
	for i := range needle {
		if needle[i] != hay[i] {
			return false
		}
	}
	return true
}

func anyLeadingTokens(phrase string, names []string) bool {
	for _, name := range names {
		if leadingTokens(phrase, name) {
			return true
		}
	}
	return false
}

func matchedPhrase(text, phrase string) bool {
	key := normalize(phrase)
	if key == "" {
		return false
	}
	return strings.Contains(text, " "+key+" ")
}

// joinedEntryMinLength is the shortest compact form the joined-word pass
// accepts. Short entries ("TDI", "MEK") are already exact aliases, and letting
// them into a substring-style comparison would only create collisions.
const joinedEntryMinLength = 6

// compactName folds a name to its unspaced comparable form: the shape an
// operator types when the words are joined ("toluenediisocyanate") and the
// space-free form the trade-DB alignment compares entry names on.
func compactName(value string) string {
	return strings.ReplaceAll(normalize(value), " ", "")
}

// joinedEntryName reports whether a single-token activity is one of the node's
// entry names with its word spaces folded away. The comparison is exact, so it
// never widens a match: "toluenediisocyanate" meets "Toluene diisocyanate",
// but "isocyanate" does not meet "Toluene diisocyanate".
func joinedEntryName(activity string, node *Node) bool {
	if len(activity) < joinedEntryMinLength {
		return false
	}
	for _, entry := range append([]string{node.Label}, node.Aliases...) {
		if compactName(entry) == activity {
			return true
		}
	}
	return false
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

// longestMatch picks the subject: the most specific phrase the activity named.
// Specificity is read from the matched phrase (tokens, then characters), so a
// generic alias ("crude") can never outrank the longer entry it sits inside
// ("crude palm oil"). Matches arrive sorted by that measure, so the first one is
// the subject; the copy keeps the caller's slice untouched.
func longestMatch(o *Ontology, matches []Match) *Node {
	if len(matches) == 0 {
		return nil
	}
	sorted := sortedMatches(o, append([]Match(nil), matches...))
	return o.Nodes[sorted[0].ID]
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
	add := func(id, reason, via string, distance int) {
		if id == "" || seen[id] {
			return
		}
		node := o.Nodes[id]
		if node == nil || node.Status == StatusRejected {
			return
		}
		seen[id] = true
		families = append(families, Family{ID: id, Label: node.Label, Kind: node.Kind, Reason: reason, Via: via, Distance: distance, Status: node.Status})
	}
	if subject != nil {
		switch {
		case subject.Hypernym && len(subject.Hyponyms) > 0:
			ambiguous = true
			for _, id := range subject.Hyponyms {
				add(id, "hypernym", subject.ID, 0)
			}
		case subject.Kind == KindCategory:
			add(subject.ID, "subject", subject.ID, 0)
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
				add(subject.ID, "subject", subject.ID, 0)
			} else {
				add(primary, "category", subject.ID, 0)
				for _, id := range candidates {
					if id != primary && o.depth(id) >= 3 {
						add(id, "category", subject.ID, 0)
					}
				}
			}
			for _, id := range subject.DerivedTo {
				target := o.Nodes[id]
				if target == nil || target.Status == StatusRejected {
					continue
				}
				if target.Kind == KindCategory {
					// A downstream family is one hop further out than the subject,
					// so it gets one less hop to reach an end-product. Without this
					// a broad hub family fans every member into the leaf walk.
					add(id, "downstream", subject.ID, 1)
					continue
				}
				if category := o.mostSpecificCategory(target); category != "" {
					add(category, "downstream", subject.ID, 1)
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
					add(id, "process-output", process.ID, 0)
					continue
				}
				if category := o.mostSpecificCategory(target); category != "" {
					add(category, "process-output", process.ID, 0)
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
	members := o.categoryMembers()
	queue := make([]step, 0, 8)
	if subject != nil {
		queue = append(queue, step{id: subject.ID})
	}
	for _, family := range families {
		queue = append(queue, step{id: family.ID, distance: family.Distance})
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
		// A family is searched through its members: the family "Soap bases" leads
		// to soap noodles, which leads to the soap manufacturers.
		if node.Kind == KindCategory {
			for _, member := range members[current.id] {
				queue = append(queue, step{id: member, distance: current.distance + 1})
			}
		}
		for _, id := range append(append([]string(nil), node.DerivedTo...), node.ConsumedIn...) {
			queue = append(queue, step{id: id, distance: current.distance + 1})
		}
	}
	return leaves
}

// pendingMatches counts the nodes whose entry names the activity matches but
// which the review has not approved, so the warning can say "approve them"
// instead of claiming the activity is unmodelled.
func (o *Ontology) pendingMatches(text string) int {
	count := 0
	for _, match := range o.exactEntryMatches(text, false) {
		if match.Status != StatusApproved {
			count++
		}
	}
	return count
}

// categoryMembers indexes every category to the nodes that declare it, so a
// family walk can reach its members without a reverse edge on each node.
func (o *Ontology) categoryMembers() map[string][]string {
	members := make(map[string][]string, len(o.Nodes))
	for id, node := range o.Nodes {
		if node == nil || node.Status == StatusRejected {
			continue
		}
		for _, category := range node.Categories {
			members[category] = append(members[category], id)
		}
	}
	return members
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
