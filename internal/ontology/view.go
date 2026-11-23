package ontology

// view.go turns the document into something a human can look at: a bounded,
// filtered node/edge set with a deterministic layout, rendered as SVG (static, no
// scripts) or served as JSON. Determinism matters — a curated graph that is
// reviewed twice must draw the same picture, and a test must be able to pin it.

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// EdgeTypes are the graph's edge families in rendering order. Each is a node field
// read as node → target.
var EdgeTypes = []string{"parent", "category", "derived_to", "consumed_in", "operates_on", "produces", "consumes", "hypernym"}

// ViewOptions selects what the view shows.
type ViewOptions struct {
	// Seed centres the view on one node (its neighbourhood) or, when empty, draws
	// the whole graph.
	Seed string
	// Hops is the neighbourhood radius around Seed; 0 means DefaultViewHops.
	Hops int
	// Kinds limits the node kinds; empty means every kind.
	Kinds []Kind
	// Edges limits the edge families; empty means every family.
	Edges []string
	// ApprovedOnly hides nodes that are still proposed.
	ApprovedOnly bool
}

// DefaultViewHops is the neighbourhood radius a seeded view uses.
const DefaultViewHops = 2

// ViewNode is one rendered node.
type ViewNode struct {
	ID                string   `json:"id"`
	Kind              Kind     `json:"kind"`
	Label             string   `json:"label"`
	Status            Status   `json:"status"`
	Source            string   `json:"source,omitempty"`
	Aliases           []string `json:"aliases,omitempty"`
	HSCodes           []string `json:"hs_codes,omitempty"`
	Note              string   `json:"expert_note,omitempty"`
	TransportProfile  string   `json:"transport_profile,omitempty"`
	MerchantLiquidity string   `json:"merchant_liquidity,omitempty"`
	TraversalPolicy   string   `json:"traversal_policy,omitempty"`
	// Ring is the hop distance from the seed, or the taxonomy depth for the whole
	// graph. It drives the layout and the legend.
	Ring int `json:"ring"`
	// X and Y are unit coordinates in [-1, 1].
	X float64 `json:"x"`
	Y float64 `json:"y"`
	// Degree is the number of rendered edges touching the node.
	Degree int `json:"degree"`
}

// ViewEdge is one rendered edge.
type ViewEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// GraphView is the rendered subset: what to draw, and the counts that explain it.
type GraphView struct {
	Revision    string         `json:"revision"`
	ReviewedBy  string         `json:"reviewed_by,omitempty"`
	Seed        string         `json:"seed,omitempty"`
	SeedLabel   string         `json:"seed_label,omitempty"`
	Hops        int            `json:"hops,omitempty"`
	Nodes       []ViewNode     `json:"nodes"`
	Edges       []ViewEdge     `json:"edges"`
	KindCounts  map[string]int `json:"kind_counts"`
	EdgeCounts  map[string]int `json:"edge_counts"`
	MaxRing     int            `json:"max_ring"`
	HiddenNodes int            `json:"hidden_nodes"`
	Warnings    []string       `json:"warnings,omitempty"`
}

// View builds the rendered subset of the document.
func (o *Ontology) View(options ViewOptions) *GraphView {
	view := &GraphView{
		Revision: o.Revision, ReviewedBy: o.ReviewedBy,
		KindCounts: map[string]int{}, EdgeCounts: map[string]int{},
	}
	seedText := strings.TrimSpace(options.Seed)
	seeds := o.viewSeeds(seedText)
	if seedText != "" && len(seeds) == 0 {
		view.Warnings = append(view.Warnings, fmt.Sprintf("%q is not a node id, label, alias or resolvable activity; showing the whole graph.", seedText))
	}
	view.Seed = seedText
	if len(seeds) > 0 {
		view.SeedLabel = seedText
	}
	hops := options.Hops
	if hops <= 0 {
		hops = DefaultViewHops
	}
	kinds := kindFilter(options.Kinds)
	edgesOfInterest := edgeFilter(options.Edges)

	nodes, edges, hidden := o.selectView(seeds, hops, kinds, edgesOfInterest, options.ApprovedOnly)
	view.HiddenNodes = hidden
	view.Edges = edges
	view.Nodes = nodes
	if len(seeds) > 0 {
		view.Hops = hops
	}
	for _, edge := range edges {
		view.EdgeCounts[edge.Type]++
	}
	o.assignRings(view, len(seeds) > 0)
	layoutRings(view)
	for _, node := range view.Nodes {
		view.KindCounts[string(node.Kind)]++
		if node.Ring > view.MaxRing {
			view.MaxRing = node.Ring
		}
	}
	return view
}

// MarshalView encodes the view for the JSON API.
func MarshalView(view *GraphView) ([]byte, error) { return json.Marshal(view) }

func kindFilter(kinds []Kind) map[Kind]bool {
	if len(kinds) == 0 {
		return nil
	}
	selected := make(map[Kind]bool, len(kinds))
	for _, kind := range kinds {
		selected[kind] = true
	}
	return selected
}

func edgeFilter(edges []string) map[string]bool {
	if len(edges) == 0 {
		return nil
	}
	selected := make(map[string]bool, len(edges))
	for _, edge := range edges {
		selected[strings.TrimSpace(edge)] = true
	}
	return selected
}

// findByLabel matches a node id, label or alias, case-insensitively.
func (o *Ontology) findByLabel(name string) *Node {
	lowered := strings.ToLower(strings.TrimSpace(name))
	if lowered == "" {
		return nil
	}
	for _, id := range o.NodeIDs() {
		node := o.Nodes[id]
		if strings.ToLower(node.Label) == lowered || strings.ToLower(id) == lowered {
			return node
		}
		for _, alias := range node.Aliases {
			if strings.ToLower(alias) == lowered {
				return node
			}
		}
	}
	return nil
}

// assignRings puts the whole graph in rings that mean something: category taxonomy
// depth inside, each substance just outside the family it belongs to, and the
// processes/end products — the value chain's operations — on the outer ring. A
// seeded view keeps its hop distance instead.
func (o *Ontology) assignRings(view *GraphView, seeded bool) {
	if seeded {
		return
	}
	inView := make(map[string]bool, len(view.Nodes))
	for _, node := range view.Nodes {
		inView[node.ID] = true
	}
	depth := map[string]int{}
	var depthOf func(id string) int
	depthOf = func(id string) int {
		if cached, ok := depth[id]; ok {
			return cached
		}
		node := o.Nodes[id]
		if node == nil {
			return 0
		}
		depth[id] = 0 // cycle guard: a parent loop must not recurse
		nested := 0
		if node.Parent != "" && inView[node.Parent] {
			nested = depthOf(node.Parent) + 1
		}
		depth[id] = nested
		return nested
	}
	outermost := 0
	for _, node := range view.Nodes {
		if node.Kind == KindCategory {
			if depth := depthOf(node.ID); depth > outermost {
				outermost = depth
			}
		}
	}
	outermost++
	for index := range view.Nodes {
		node := &view.Nodes[index]
		switch node.Kind {
		case KindCategory:
			node.Ring = depthOf(node.ID)
		case KindSubstance:
			node.Ring = outermost
			if source := o.Nodes[node.ID]; source != nil && len(source.Categories) > 0 {
				if family := source.Categories[0]; inView[family] {
					node.Ring = depthOf(family) + 1
				}
			}
		default:
			node.Ring = outermost
		}
	}
}

// layoutRings places each ring on a circle, sorted by label so the picture is stable
// between runs and reviews. Alternate rings are staggered so spokes do not overlap.
func layoutRings(view *GraphView) {
	if len(view.Nodes) == 0 {
		return
	}
	rings := map[int][]int{}
	maxRing := 0
	for index, node := range view.Nodes {
		rings[node.Ring] = append(rings[node.Ring], index)
		if node.Ring > maxRing {
			maxRing = node.Ring
		}
	}
	degree := map[string]int{}
	for _, edge := range view.Edges {
		degree[edge.From]++
		degree[edge.To]++
	}
	for ring, members := range rings {
		sort.SliceStable(members, func(i, j int) bool {
			left, right := view.Nodes[members[i]], view.Nodes[members[j]]
			if left.Label != right.Label {
				return left.Label < right.Label
			}
			return left.ID < right.ID
		})
		radius := 0.62
		if maxRing > 0 {
			radius = 0.95 * float64(ring) / float64(maxRing)
		}
		stagger := 0.0
		if ring%2 == 1 {
			stagger = math.Pi / float64(len(members))
		}
		for position, index := range members {
			angle := 2*math.Pi*float64(position)/float64(len(members)) + stagger - math.Pi/2
			view.Nodes[index].X = math.Cos(angle) * radius
			view.Nodes[index].Y = math.Sin(angle) * radius
			view.Nodes[index].Degree = degree[view.Nodes[index].ID]
		}
	}
}

// whole graph, or a BFS neighbourhood around the seed.
// viewSeeds turns the seed text into node ids: an id, a label or alias, or an
// activity the resolver knows. An activity seeds the nodes it named (its subject
// and the process), which is what makes "show me oil blending" a useful picture.
func (o *Ontology) viewSeeds(text string) []string {
	if text == "" {
		return nil
	}
	if o.Nodes[text] != nil {
		return []string{text}
	}
	if node := o.findByLabel(text); node != nil {
		return []string{node.ID}
	}
	resolution := o.Resolve(text, DefaultOptions)
	seeds := make([]string, 0, 5)
	seen := map[string]bool{}
	appendUnique := func(ids ...string) {
		for _, id := range ids {
			if len(seeds) >= 5 || seen[id] || o.Nodes[id] == nil {
				continue
			}
			seen[id] = true
			seeds = append(seeds, id)
		}
	}
	for _, match := range resolution.Matches {
		appendUnique(match.ID)
	}
	for _, process := range resolution.Processes {
		appendUnique(process.ID)
	}
	if len(seeds) == 0 {
		for _, family := range resolution.Families {
			appendUnique(family.ID)
		}
	}
	return seeds
}

func (o *Ontology) selectView(seeds []string, hops int, kinds map[Kind]bool, edgeTypes map[string]bool, approvedOnly bool) ([]ViewNode, []ViewEdge, int) {
	all := make([]ViewEdge, 0, len(o.Nodes)*3)
	seen := map[string]bool{}
	for _, id := range o.NodeIDs() {
		node := o.Nodes[id]
		for _, edge := range nodeEdges(id, node) {
			if o.Nodes[edge.To] == nil {
				continue
			}
			key := edge.From + "\x00" + edge.To + "\x00" + edge.Type
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, edge)
		}
	}
	allowed := func(id string) bool {
		node := o.Nodes[id]
		if node == nil {
			return false
		}
		if approvedOnly && node.Status != StatusApproved {
			return false
		}
		if kinds != nil && !kinds[node.Kind] {
			return false
		}
		return true
	}
	keep := map[string]bool{}
	ring := map[string]int{}
	if len(seeds) == 0 {
		for id := range o.Nodes {
			if allowed(id) {
				keep[id] = true
			}
		}
	} else {
		adjacency := map[string][]string{}
		for _, edge := range all {
			if edgeTypes != nil && !edgeTypes[edge.Type] {
				continue
			}
			adjacency[edge.From] = append(adjacency[edge.From], edge.To)
			adjacency[edge.To] = append(adjacency[edge.To], edge.From)
		}
		queue := make([]string, 0, len(seeds))
		for _, seed := range seeds {
			if _, visited := ring[seed]; visited {
				continue
			}
			ring[seed] = 0
			queue = append(queue, seed)
		}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			if allowed(current) {
				keep[current] = true
			}
			if ring[current] >= hops {
				continue
			}
			for _, next := range adjacency[current] {
				if _, visited := ring[next]; visited {
					continue
				}
				ring[next] = ring[current] + 1
				queue = append(queue, next)
			}
		}
	}
	nodes := make([]ViewNode, 0, len(keep))
	for id := range keep {
		node := o.Nodes[id]
		nodes = append(nodes, ViewNode{
			ID: id, Kind: node.Kind, Label: node.Label, Status: node.Status,
			Source: node.Source, Aliases: cloneStrings(node.Aliases),
			HSCodes: cloneStrings(node.HSCodes), Note: node.ExpertNote,
			TransportProfile: node.TransportProfile, MerchantLiquidity: node.MerchantLiquidity,
			TraversalPolicy: node.TraversalPolicy,
			Ring:            ring[id],
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	edges := make([]ViewEdge, 0, len(all))
	for _, edge := range all {
		if edgeTypes != nil && !edgeTypes[edge.Type] {
			continue
		}
		if !keep[edge.From] || !keep[edge.To] {
			continue
		}
		edges = append(edges, edge)
	}
	hidden := 0
	for id := range o.Nodes {
		if !keep[id] {
			hidden++
		}
	}
	return nodes, edges, hidden
}

// nodeEdges lists a node's outgoing edges in the canonical rendering order.
func nodeEdges(id string, node *Node) []ViewEdge {
	edges := make([]ViewEdge, 0, 6)
	appendAll := func(edgeType string, targets []string) {
		for _, target := range targets {
			target = strings.TrimSpace(target)
			if target != "" && target != id {
				edges = append(edges, ViewEdge{From: id, To: target, Type: edgeType})
			}
		}
	}
	if node.Parent != "" {
		appendAll("parent", []string{node.Parent})
	}
	appendAll("category", node.Categories)
	appendAll("derived_to", node.DerivedTo)
	appendAll("consumed_in", node.ConsumedIn)
	appendAll("operates_on", node.OperatesOn)
	appendAll("produces", node.Produces)
	appendAll("consumes", node.Consumes)
	appendAll("hypernym", node.Hyponyms)
	return edges
}
