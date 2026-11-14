package models

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// Buyer-side claim types. The confidence ladder weights them by how directly
// they evidence consumption: an import or tender record names an actual
// transaction; input language is the buyer's own words; generic verification
// evidence is the weakest of the qualifying kinds.
const (
	// DemandClaimImport marks customs/trade-data evidence of an actual import.
	DemandClaimImport = "import-record"
	// DemandClaimTender marks a tender, RFP or e-procurement record.
	DemandClaimTender = "demand-tender"
	// DemandClaimUsage marks the buyer's own input language ("raw material",
	// "feedstock", "we use") tying the product to their process.
	DemandClaimUsage = "input-usage"
	// DemandClaimHiring marks procurement/purchasing job postings.
	DemandClaimHiring = "procurement-hiring"
	// DemandClaimRFQ marks a marketplace requirement posting.
	DemandClaimRFQ = "marketplace-rfq"
	// DemandClaimOperator marks evidence that ties the candidate to the
	// consuming stage without naming a transaction (the weakest qualifying kind).
	DemandClaimOperator = "operator-verification"
	// DemandClaimSignal marks an unclassified demand-shaped discovery hit.
	DemandClaimSignal = "demand-signal"
)

// Stage distance values. The buyer definition scopes buyers to manufacturers
// one or two transformation steps downstream of the product: direct consumers
// of the product as an input, and manufacturers of a direct consumer's output.
const (
	// StageDistanceDirect marks a process that consumes the product itself as
	// an input or raw material — the strongest buyer hypothesis.
	StageDistanceDirect = 1
	// StageDistanceDerived marks a process that consumes a direct consumer's
	// output — one transformation step beyond the product, still in scope.
	StageDistanceDerived = 2
)

// ValueChainStage is one consuming stage the buyer-side search is organised
// around: buyers of a product are, almost by definition, operators of the
// processes that consume it.
type ValueChainStage struct {
	// Name is the consuming process, e.g. "biodiesel production".
	Name string `json:"name"`
	// Rationale is one line on why the stage consumes the product.
	Rationale string `json:"rationale,omitempty"`
	// Operators is the search vocabulary for the stage's operators, e.g.
	// "biodiesel plant", "oil refinery".
	Operators []string `json:"operators,omitempty"`
	// Products are concrete product families within a broad stage. Discovery
	// searches these before falling back to the stage label.
	Products []string `json:"products,omitempty"`
	// Source records where the stage came from: "model" (inferred),
	// "operator" (supplied on the form), or "fallback" (inference failed).
	Source string `json:"source"`
	// Distance is how many transformation steps the stage sits from the
	// product: 1 consumes the product directly as an input or raw material,
	// 2 consumes a step-1 output. Stages further than two steps are out of
	// buyer scope and are dropped at inference; zero is treated as 1.
	Distance int `json:"distance,omitempty"`
}

// DemandStageRejection is one inferred stage the adversarial value-chain reviewer rejected.
type DemandStageRejection struct {
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}

// DemandDistanceCorrection fixes a stage's step distance after review (for example, a mattress
// manufacturer that consumes foam made from TDI is step 2, not a direct TDI consumer).
type DemandDistanceCorrection struct {
	Name     string `json:"name"`
	Distance int    `json:"distance"`
}

// ValueChainReview is the adversarial first-pass review: it challenges whether each proposed
// stage really consumes the product under the two-step buyer definition. The pipeline applies
// only rejections and distance corrections to stages already present; it never accepts an
// invented replacement stage from the judge.
type ValueChainReview struct {
	Verdict             string                     `json:"verdict"`
	Score               float64                    `json:"score"`
	RejectedStages      []DemandStageRejection     `json:"rejected_stages,omitempty"`
	DistanceCorrections []DemandDistanceCorrection `json:"distance_corrections,omitempty"`
	MissingStages       []string                   `json:"missing_stages,omitempty"`
	Reason              string                     `json:"reason,omitempty"`
}

// DemandChainInference is the first-pass value-chain result. Category is deliberately separate
// from the stages because it is a fallback search dimension, not a buyer and not a stage.
type DemandChainInference struct {
	Category          string            `json:"category,omitempty"`
	CategoryRationale string            `json:"category_rationale,omitempty"`
	Stages            []ValueChainStage `json:"stages"`
}

// UnmarshalJSON accepts both the new object shape and the old bare-array shape, so a saved or
// older model response still decodes.
func (inference *DemandChainInference) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return json.Unmarshal(trimmed, &inference.Stages)
	}
	type inferenceAlias DemandChainInference
	var decoded inferenceAlias
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return err
	}
	*inference = DemandChainInference(decoded)
	return nil
}

// DemandChainQuery defines one bounded buyer-side investigation: the product or
// commodity to research, where to look, and how much to return.
type DemandChainQuery struct {
	Activity     string   `json:"activity"`
	Objective    string   `json:"objective,omitempty"`
	Regions      []string `json:"regions,omitempty"`
	Countries    []string `json:"countries,omitempty"`
	Exclusions   []string `json:"exclusions,omitempty"`
	MaxCompanies int      `json:"max_companies,omitempty"`
	// StagesOverride supplies the consuming stages directly; when set, the
	// value-chain inference call is skipped entirely.
	StagesOverride []string `json:"stages_override,omitempty"`
	// IncludeIntermediaries keeps traders, brokers and trading houses — who are
	// simultaneously buyers and sellers — in the results. They are excluded by
	// default because a buyer list dominated by middlemen is rarely what the
	// operator asked for.
	IncludeIntermediaries bool `json:"include_intermediaries,omitempty"`
	// Budget overrides. Zero keeps the mode defaults; a run spends at most
	// DiscoveryQueries discovery searches and VerificationQueries verification
	// searches, and enriches at most MaxLeadsEnriched discovery leads before
	// extraction — the levers that make a deep run deep.
	DiscoveryQueries    int `json:"discovery_queries,omitempty"`
	VerificationQueries int `json:"verification_queries,omitempty"`
	MaxLeadsEnriched    int `json:"max_leads_enriched,omitempty"`
}

// StageCandidates groups the companies a run tied to one consuming stage —
// operators the stage lanes surfaced, verified or not. This is the stage-
// oriented view of "potential buyers": a company running a consuming stage is
// a potential buyer of the product by construction, even before any tender or
// import evidence reaches the verification threshold.
type StageCandidates struct {
	Stage     ValueChainStage          `json:"stage"`
	Companies []MarketLandscapeCompany `json:"companies,omitempty"`
}

// DemandStageCoverage is the deterministic QC check for one consuming stage (or the separate
// distribution channel category): how many identity-passed companies the run found for it and
// whether it clears the required minimum. The rule is deliberately about companies identified,
// not companies verified: a stage with no operators is not a useful buyer landscape even when
// every result it does have is well evidenced.
type DemandStageCoverage struct {
	Stage   string `json:"stage"`
	Count   int    `json:"count"`
	Minimum int    `json:"minimum"`
	Pass    bool   `json:"pass"`
}

// DemandRejectedName is one company the QC judge or the deterministic gates removed, with the
// reason retained for audit. The name is never deleted from the appendix's evidence.
type DemandRejectedName struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// DemandChainReview is the post-assembly quality review: a deterministic stage-coverage check
// plus an optional LLM judge. The judge proposes; the pipeline applies only names that already
// exist in the report and records every rejection.
type DemandChainReview struct {
	Verdict         string                `json:"verdict"`
	Score           float64               `json:"score"`
	Rejected        []DemandRejectedName  `json:"rejected,omitempty"`
	MissingStages   []string              `json:"missing_stages,omitempty"`
	FollowUpQueries []string              `json:"follow_up_queries,omitempty"`
	StageCoverage   []DemandStageCoverage `json:"stage_coverage,omitempty"`
	Reason          string                `json:"reason,omitempty"`
}

// UnmarshalJSON accepts the shapes models actually return for the QC fields: rejected entries as
// objects or bare names, and follow-up queries as strings or query objects. A decode failure here
// would silently disable the judge, so the flexible decoder is deliberate.
func (review *DemandChainReview) UnmarshalJSON(data []byte) error {
	type reviewAlias DemandChainReview
	aux := struct {
		Rejected        []json.RawMessage `json:"rejected"`
		FollowUpQueries []json.RawMessage `json:"follow_up_queries"`
		*reviewAlias
	}{reviewAlias: (*reviewAlias)(review)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	review.Rejected = nil
	for _, raw := range aux.Rejected {
		var rejected DemandRejectedName
		if err := json.Unmarshal(raw, &rejected); err == nil && strings.TrimSpace(rejected.Name) != "" {
			review.Rejected = append(review.Rejected, rejected)
			continue
		}
		var name string
		if err := json.Unmarshal(raw, &name); err == nil && strings.TrimSpace(name) != "" {
			review.Rejected = append(review.Rejected, DemandRejectedName{Name: name, Reason: "judge rejection"})
		}
	}
	review.FollowUpQueries = nil
	for _, raw := range aux.FollowUpQueries {
		var query string
		if err := json.Unmarshal(raw, &query); err == nil && strings.TrimSpace(query) != "" {
			review.FollowUpQueries = append(review.FollowUpQueries, query)
			continue
		}
		var object struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(raw, &object); err == nil && strings.TrimSpace(object.Query) != "" {
			review.FollowUpQueries = append(review.FollowUpQueries, object.Query)
		}
	}
	return nil
}

// DemandChainReport is the buyer-side counterpart of MarketLandscapeReport: the
// inferred value chain, the verified consumers, and the auditable corpus.
type DemandChainReport struct {
	ResearchID string `json:"research_id"`
	Activity   string `json:"activity"`
	Objective  string `json:"objective"`
	// Geography is the resolved regional scope every search ran under —
	// countries win over regions, empty means global — so the reader can see
	// what the execution was scoped to.
	Geography string `json:"geography,omitempty"`
	// ProductCategory is the validated first-pass broad category. It is a search fallback only:
	// it never enters Results or the value chain as a buyer stage.
	ProductCategory   string `json:"product_category,omitempty"`
	CategoryRationale string `json:"category_rationale,omitempty"`
	CategorySource    string `json:"category_source,omitempty"`
	// Chain is the consuming stages the search was organised around, in the
	// order the lanes were built from them.
	Chain []ValueChainStage `json:"chain,omitempty"`
	// ChainReview is the adversarial review of the inferred chain. It records rejected stages,
	// distance corrections and missing hypotheses for audit.
	ChainReview *ValueChainReview `json:"chain_review,omitempty"`
	// ChainInferred reports whether the chain came from the model. False means
	// the operator supplied it or the inference fell back to a generic stage.
	ChainInferred bool                     `json:"chain_inferred"`
	Results       []MarketLandscapeCompany `json:"results,omitempty"`
	// StageCandidates is the stage-oriented view: the companies each consuming
	// stage's lanes surfaced, whatever their verification status. Results above
	// is the cross-cutting verified list; this is the value chain's own roster.
	StageCandidates     []StageCandidates         `json:"stage_candidates,omitempty"`
	StageCoverage       []DemandStageCoverage     `json:"stage_coverage,omitempty"`
	Reviews             []DemandChainReview       `json:"reviews,omitempty"`
	Appendix            []MarketLandscapeEvidence `json:"appendix,omitempty"`
	Lanes               []string                  `json:"lanes,omitempty"`
	Iterations          int                       `json:"iterations"`
	QueriesUsed         int                       `json:"queries_used"`
	GapQueries          int                       `json:"gap_queries,omitempty"`
	SearchHits          int                       `json:"search_hits"`
	LeadsReviewed       int                       `json:"leads_reviewed"`
	LeadsExtracted      int                       `json:"leads_extracted"`
	LeadsRejected       int                       `json:"leads_rejected"`
	PagesEnriched       int                       `json:"pages_enriched"`
	VerificationQueries int                       `json:"verification_queries"`
	CandidatesFound     int                       `json:"candidates_found"`
	Warnings            []string                  `json:"warnings,omitempty"`
	StartedAt           time.Time                 `json:"started_at"`
	CompletedAt         time.Time                 `json:"completed_at"`
	Exports             []ExportArtifact          `json:"exports,omitempty"`
}
