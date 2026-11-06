package models

import "time"

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
	// Source records where the stage came from: "model" (inferred),
	// "operator" (supplied on the form), or "fallback" (inference failed).
	Source string `json:"source"`
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
	// Chain is the consuming stages the search was organised around, in the
	// order the lanes were built from them.
	Chain []ValueChainStage `json:"chain,omitempty"`
	// ChainInferred reports whether the chain came from the model. False means
	// the operator supplied it or the inference fell back to a generic stage.
	ChainInferred       bool                      `json:"chain_inferred"`
	Results             []MarketLandscapeCompany  `json:"results,omitempty"`
	Appendix            []MarketLandscapeEvidence `json:"appendix,omitempty"`
	Lanes               []string                  `json:"lanes,omitempty"`
	Iterations          int                       `json:"iterations"`
	QueriesUsed         int                       `json:"queries_used"`
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
