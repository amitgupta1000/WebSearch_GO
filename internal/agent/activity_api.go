package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/ontology"
)

// activity_api.go is the public face of the resolution flow: the Seller and Buyer
// Landscape modes call the same resolveActivity underneath, and the HTTP API and
// the ontology CLI call this view to explain — before spending a run — which
// branch an activity takes and what research path it produces.
//
//	(1)   ontology match  → the graph supplies the categories, terms and stages
//	(2.1) alias match     → the model maps the wording onto an approved node
//	(2.2) unmodelled      → the model plans the path; the activity is flagged
//
// The view is read-only: it never starts a research run and never writes to the
// graph. Alias mappings are reported for curation, not persisted.

// ActivityMode selects the side of the value chain the resolution describes.
type ActivityMode string

const (
	// ActivityModeSeller is the Seller Landscape: who makes the product family.
	ActivityModeSeller ActivityMode = "seller"
	// ActivityModeBuyer is the Buyer Landscape: who consumes the product.
	ActivityModeBuyer ActivityMode = "buyer"
)

// NormalizeActivityMode defaults to the seller mode and accepts the words the
// console and the CLI use.
func NormalizeActivityMode(mode string) ActivityMode {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "buyer", "demand", "demand-chain", "demand_chain":
		return ActivityModeBuyer
	default:
		return ActivityModeSeller
	}
}

// Exported branch names, for consumers that switch on the resolution path (the
// HTTP API and the ontology CLI do).
const (
	ActivityPathOntology   = string(pathOntology)
	ActivityPathAlias      = string(pathAlias)
	ActivityPathOperator   = string(pathOperator)
	ActivityPathUnmodelled = string(pathUnmodelled)
)

// ActivityQuery is the resolution request.
type ActivityQuery struct {
	Activity  string       `json:"activity"`
	Geography string       `json:"geography,omitempty"`
	Mode      ActivityMode `json:"mode,omitempty"`
	// TradeProduct is the operator's pick from the trade-DB product matcher.
	// When set it anchors the graph resolution before the typed activity does,
	// so the preview matches what a trade-DB run actually uses.
	TradeProduct string `json:"trade_product,omitempty"`
	// NodeID is the operator's ontology confirmation: the graph node this
	// activity means. It is applied before the activity text is resolved (the
	// text is what produced the wrong node), remembered for the activity for the
	// life of the process, and echoed back on the resolution.
	NodeID string `json:"node_id,omitempty"`
}

// ActivityFamilyView is one ranked product family the graph offers.
type ActivityFamilyView struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Rank   int    `json:"rank"`
	Reason string `json:"reason,omitempty"`
	Via    string `json:"via,omitempty"`
}

// ActivityResolution is the API's answer: which branch resolved the activity and
// the research path it produced.
type ActivityResolution struct {
	Activity   string       `json:"activity"`
	Mode       ActivityMode `json:"mode"`
	Path       string       `json:"path"`
	Matched    bool         `json:"matched"`
	NodeLabel  string       `json:"node_label,omitempty"`
	Confidence string       `json:"alias_confidence,omitempty"`
	// TradeProduct echoes the picked trade-DB product that anchored the
	// resolution, so a preview can say which name the graph followed.
	TradeProduct string `json:"trade_product,omitempty"`
	// NodeID echoes the ontology node the resolution is anchored on (the text's
	// own match, or the operator's confirmation); Picked says which.
	NodeID string `json:"node_id,omitempty"`
	Picked bool   `json:"picked,omitempty"`
	// Candidates are the nodes worth confirming, ranked: the match first, then
	// its alternatives with the family each would drive. They are offered even
	// on a clean match -- "check the match" is the point of the picker.
	Candidates []ActivityCandidate `json:"candidates,omitempty"`

	Families    []ActivityFamilyView `json:"families,omitempty"`
	Processes   []string             `json:"processes,omitempty"`
	SearchTerms []string             `json:"search_terms,omitempty"`

	Category          string   `json:"category,omitempty"`
	CategorySource    string   `json:"category_source,omitempty"`
	CategoryRationale string   `json:"category_rationale,omitempty"`
	CategoryLanes     []string `json:"category_lanes,omitempty"`

	Stages   []models.ValueChainStage `json:"stages,omitempty"`
	Warnings []string                 `json:"warnings,omitempty"`
}

// ResolveActivity runs the resolution flow without starting a run. A nil engine
// keeps it deterministic: the alias branch is simply not attempted, which is what
// the CLI and offline tools use.
func ResolveActivity(ctx context.Context, engine *Engine, query ActivityQuery) (ActivityResolution, error) {
	return ResolveActivityAgainst(ctx, engine, nil, query)
}

// ResolveActivityAgainst resolves against a specific document; nil means the
// embedded, approved seed. The CLI uses a candidate file this way, so curation can
// check a node's research path before it is approved.
func ResolveActivityAgainst(ctx context.Context, engine *Engine, document *ontology.Ontology, query ActivityQuery) (ActivityResolution, error) {
	query.Activity = strings.TrimSpace(query.Activity)
	if query.Activity == "" {
		return ActivityResolution{}, fmt.Errorf("activity is required")
	}
	if document == nil {
		shared, err := ontology.Shared()
		if err != nil {
			return ActivityResolution{}, err
		}
		document = shared
	}
	view := ActivityResolution{Activity: query.Activity, Mode: NormalizeActivityMode(string(query.Mode)), TradeProduct: strings.TrimSpace(query.TradeProduct)}
	nodeID := strings.TrimSpace(query.NodeID)
	resolution := resolveActivityWithAnchor(ctx, engine, document, query.Activity, query.TradeProduct, nodeID, nil)
	// An explicit confirmation is remembered for the activity, the way the trade
	// entity picker remembers its pick: the console previews once, and the run
	// that follows reuses it without resending the field.
	if nodeID != "" && resolution.Path == pathOperator {
		SetOntologyPick(query.Activity, nodeID)
	}
	// A confirmed node needs no model arbitration: the operator already chose.
	if resolution.matched() && view.Mode == ActivityModeSeller && resolution.Path != pathOperator {
		resolution = chooseOntologyFamily(ctx, engine, resolution, query.Geography, nil)
	}
	view.Path = string(resolution.Path)
	view.Matched = resolution.matched()
	view.NodeID = resolution.NodeID
	view.NodeLabel = resolution.NodeLabel
	view.Picked = resolution.Path == pathOperator
	view.Confidence = resolution.AliasConfidence
	view.Candidates = activityCandidates(document, resolution.Graph, query.Activity, ontologyCandidateLimit)
	view.Warnings = append(view.Warnings, resolution.Warnings...)
	for _, family := range resolution.Graph.Families {
		view.Families = append(view.Families, ActivityFamilyView{
			ID: family.ID, Label: family.Label, Rank: family.Rank,
			Reason: family.Reason, Via: family.Via,
		})
	}
	for _, process := range resolution.Graph.Processes {
		view.Processes = append(view.Processes, process.ID)
	}
	view.SearchTerms = append(view.SearchTerms, resolution.searchTerms()...)
	category := resolution.primaryCategory()
	if view.Mode == ActivityModeBuyer {
		category = resolution.downstreamCategory()
	}
	if category != "" {
		view.Category = category
		view.CategorySource = string(resolution.Path)
		view.CategoryRationale = resolution.categoryRationale()
		if view.Mode == ActivityModeBuyer && category != resolution.primaryCategory() {
			view.CategoryRationale = fmt.Sprintf("Buyer-side family: %q is consumed by %q manufacturers.", resolution.Activity, category)
		}
		view.CategoryLanes = marketCategoryLanes(category)
	}
	if view.Mode == ActivityModeBuyer {
		view.Stages = demandStagesFromOntology(resolution.document, resolution)
	}
	return view, nil
}
