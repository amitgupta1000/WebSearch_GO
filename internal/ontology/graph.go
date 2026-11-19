package ontology

import (
	"encoding/json"
	"sort"
	"strings"
)

// LexiconNode is one node in the bounded subgraph handed to the LLM. It carries
// only the fields a prompt needs to reason about relations, and only node IDs —
// never free-text chemistry the model could misread.
type LexiconNode struct {
	ID         string   `json:"id"`
	Kind       Kind     `json:"kind"`
	Label      string   `json:"label"`
	Parent     string   `json:"parent,omitempty"`
	Aliases    []string `json:"aliases,omitempty"`
	Categories []string `json:"categories,omitempty"`
	DerivedTo  []string `json:"derived_to,omitempty"`
	ConsumedIn []string `json:"consumed_in,omitempty"`
	OperatesOn []string `json:"operates_on,omitempty"`
	Produces   []string `json:"produces,omitempty"`
	Status     Status   `json:"status"`
}

// Lexicon is the bounded subgraph the prompts may reference. It is the answer to
// "how the LLM refers to the graph" until tool-calling exists: resolve
// deterministically, hand over the neighbourhood, accept only IDs from it.
type Lexicon struct {
	Seeds []string      `json:"seeds"`
	Nodes []LexiconNode `json:"nodes"`
}

// JSON renders the lexicon compactly for prompt injection.
func (l *Lexicon) JSON() (string, error) {
	raw, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// IDs returns the lexicon's node IDs in a stable order.
func (l *Lexicon) IDs() []string {
	ids := make([]string, 0, len(l.Nodes))
	for _, node := range l.Nodes {
		ids = append(ids, node.ID)
	}
	sort.Strings(ids)
	return ids
}

// Subgraph returns the neighbourhood of the seed nodes, up to hops away, with
// edges followed in both directions (a prompt needs the parents as much as the
// children). Unknown seeds are ignored.
func (o *Ontology) Subgraph(ids []string, hops int) *Lexicon {
	if hops <= 0 {
		hops = 1
	}
	reverse := o.reverseIndex()
	type step struct {
		id       string
		distance int
	}
	queue := make([]step, 0, len(ids))
	for _, id := range ids {
		queue = append(queue, step{id: strings.TrimSpace(id)})
	}
	seen := map[string]bool{}
	lexicon := &Lexicon{Seeds: uniqueStrings(ids)}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.id == "" || seen[current.id] {
			continue
		}
		node := o.Nodes[current.id]
		if node == nil {
			continue
		}
		seen[current.id] = true
		lexicon.Nodes = append(lexicon.Nodes, LexiconNode{
			ID: current.id, Kind: node.Kind, Label: node.Label, Parent: node.Parent,
			Aliases: node.Aliases, Categories: node.Categories, DerivedTo: node.DerivedTo,
			ConsumedIn: node.ConsumedIn, OperatesOn: node.OperatesOn, Produces: node.Produces,
			Status: node.Status,
		})
		if current.distance >= hops {
			continue
		}
		for _, next := range append(append(append(append([]string(nil),
			node.DerivedTo...), node.ConsumedIn...), node.OperatesOn...), node.Produces...) {
			queue = append(queue, step{id: next, distance: current.distance + 1})
		}
		next := append(append([]string(nil), node.Categories...), node.Consumes...)
		if node.Parent != "" {
			next = append(next, node.Parent)
		}
		for _, id := range next {
			queue = append(queue, step{id: id, distance: current.distance + 1})
		}
		for _, next := range reverse[current.id] {
			queue = append(queue, step{id: next, distance: current.distance + 1})
		}
	}
	sort.SliceStable(lexicon.Nodes, func(i, j int) bool { return lexicon.Nodes[i].ID < lexicon.Nodes[j].ID })
	return lexicon
}

// reverseIndex maps a referenced node to the nodes that reference it, so the
// graph can be walked upstream without duplicating every edge.
func (o *Ontology) reverseIndex() map[string][]string {
	reverse := make(map[string][]string, len(o.Nodes))
	add := func(to, from string) {
		to = strings.TrimSpace(to)
		if to == "" {
			return
		}
		reverse[to] = append(reverse[to], from)
	}
	for id, node := range o.Nodes {
		if node == nil {
			continue
		}
		for _, target := range node.DerivedTo {
			add(target, id)
		}
		for _, target := range node.ConsumedIn {
			add(target, id)
		}
		for _, target := range node.OperatesOn {
			add(target, id)
		}
		for _, target := range node.Produces {
			add(target, id)
		}
		for _, target := range node.Categories {
			add(target, id)
		}
		add(node.Parent, id)
	}
	return reverse
}

// Direction selects which way Expand walks the chain.
type Direction int

const (
	// Downstream follows derived_to and consumed_in: what this node becomes.
	Downstream Direction = iota
	// Upstream follows the reverse edges: what feeds this node.
	Upstream
)

// Expand walks the chain from the seeds and returns the nodes it reaches with
// their hop distance. It is the graph replacement for the hand-written consumer
// and direct-input tables.
func (o *Ontology) Expand(ids []string, direction Direction, hops int) []Family {
	if hops <= 0 {
		hops = DefaultOptions.MaxHops
	}
	reverse := o.reverseIndex()
	type step struct {
		id       string
		distance int
	}
	queue := make([]step, 0, len(ids))
	for _, id := range ids {
		queue = append(queue, step{id: strings.TrimSpace(id)})
	}
	seen := map[string]bool{}
	var reached []Family
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.id == "" || seen[current.id] {
			continue
		}
		node := o.Nodes[current.id]
		if node == nil || node.Status == StatusRejected {
			continue
		}
		seen[current.id] = true
		if current.distance > 0 {
			reached = append(reached, Family{
				ID: current.id, Label: node.Label, Kind: node.Kind, Rank: len(reached),
				Distance: current.distance, Reason: directionLabel(direction), Status: node.Status,
			})
		}
		if current.distance >= hops {
			continue
		}
		var next []string
		if direction == Downstream {
			next = append(append([]string(nil), node.DerivedTo...), node.ConsumedIn...)
			next = append(next, node.Produces...)
		} else {
			// Upstream is "what feeds this node": the edges other nodes declare
			// toward it, plus the inputs this node declares for itself (an
			// end-product names its inputs in consumes, a process in operates_on).
			next = append(next, reverse[current.id]...)
			next = append(next, node.Consumes...)
			next = append(next, node.OperatesOn...)
		}
		for _, id := range next {
			queue = append(queue, step{id: id, distance: current.distance + 1})
		}
	}
	return reached
}

func directionLabel(direction Direction) string {
	if direction == Upstream {
		return "upstream"
	}
	return "downstream"
}

// CanonicalTradeMatch maps the trade DB's canonical product vocabulary onto node
// IDs. A name that matches nothing, or matches more than one node, is returned in
// unmatched: the caller logs it, and the reviewer adds or disambiguates the
// alias. It is how the DB's standardized names stay aligned with the graph.
func (o *Ontology) CanonicalTradeMatch(names []string) (map[string]string, []string) {
	matches := make(map[string]string, len(names))
	var unmatched []string
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		found := o.Lookup(name)
		if len(found) == 0 {
			// The DB's canonical spelling may trail a trade abbreviation
			// ("TOLUENE DI ISOCYANATE (TDI)") or join words the graph spaces
			// ("toluenediisocyanate"). The alignment pass reads those, so a
			// name the graph spells differently is not called unmatched.
			if aligned, ok := o.AlignTradeProduct(name, Options{RequireApproved: true}); ok && len(aligned.Matches) == 1 {
				found = []string{aligned.Matches[0].ID}
			}
		}
		if len(found) != 1 {
			unmatched = append(unmatched, name)
			continue
		}
		matches[name] = found[0]
	}
	return matches, unmatched
}

// SiblingVariants returns the other variants of the first hypernym hub that lists
// nodeID as a hyponym — the families that share the hub's ambiguity ("fuel oils"
// and "crude oil" are siblings of "lubricants" under the "oils" hub). A node with
// no hub has no siblings, and an unknown node has none either.
func (o *Ontology) SiblingVariants(nodeID string) []*Node {
	if strings.TrimSpace(nodeID) == "" || o.Nodes[nodeID] == nil {
		return nil
	}
	for _, id := range o.NodeIDs() {
		hub := o.Nodes[id]
		if hub == nil || len(hub.Hyponyms) == 0 {
			continue
		}
		listed := false
		for _, hyponym := range hub.Hyponyms {
			if hyponym == nodeID {
				listed = true
				break
			}
		}
		if !listed {
			continue
		}
		siblings := make([]*Node, 0, len(hub.Hyponyms))
		for _, siblingID := range hub.Hyponyms {
			if siblingID == nodeID {
				continue
			}
			if sibling := o.Nodes[siblingID]; sibling != nil {
				siblings = append(siblings, sibling)
			}
		}
		return siblings
	}
	return nil
}
