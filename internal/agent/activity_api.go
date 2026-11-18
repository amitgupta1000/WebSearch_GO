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
	ActivityPathUnmodelled = string(pathUnmodelled)
)

// ActivityQuery is the resolution request.
type ActivityQuery struct {
	Activity  string       `json:"activity"`
	Geography string       `json:"geography,omitempty"`
	Mode      ActivityMode `json:"mode,omitempty"`
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
	NodeID     string       `json:"node_id,omitempty"`
	NodeLabel  string       `json:"node_label,omitempty"`
	Confidence string       `json:"alias_confidence,omitempty"`

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
	view := ActivityResolution{Activity: query.Activity, Mode: NormalizeActivityMode(string(query.Mode))}
	resolution := resolveActivityWithDocument(ctx, engine, document, query.Activity, nil)
	if resolution.matched() && view.Mode == ActivityModeSeller {
		resolution = chooseOntologyFamily(ctx, engine, resolution, query.Geography, nil)
	}
	view.Path = string(resolution.Path)
	view.Matched = resolution.matched()
	view.NodeID = resolution.NodeID
	view.NodeLabel = resolution.NodeLabel
	view.Confidence = resolution.AliasConfidence
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
