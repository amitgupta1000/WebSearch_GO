package models

import (
	"encoding/json"
	"strings"
	"time"
)

// MarketLandscapeQuery defines one bounded, one-run landscape investigation.
type MarketLandscapeQuery struct {
	Objective       string   `json:"objective"`
	Activity        string   `json:"activity"`
	Regions         []string `json:"regions,omitempty"`
	Countries       []string `json:"countries,omitempty"`
	Exclusions      []string `json:"exclusions,omitempty"`
	MaxCompanies    int      `json:"max_companies,omitempty"`
	IncludeContacts bool     `json:"include_contacts,omitempty"`
	// QCPrompt is the operator-editable review policy. Empty means the default policy.
	QCPrompt string `json:"qc_prompt,omitempty"`
}

const (
	MarketRoleManufacturer = "manufacturer"
	MarketRoleDistributor  = "distributor"
	MarketRoleWholesaler   = "wholesaler"
	MarketRoleTrader       = "trader"
	MarketRoleUnknown      = "unknown"

	MarketPresenceLocal    = "local"
	MarketPresenceRegional = "regional"
	MarketPresenceGlobal   = "global"
	MarketPresenceUnknown  = "unknown"
)

// MarketLandscapeEvidence is a tagged source record in the vector-free corpus.
type MarketLandscapeEvidence struct {
	URL         string    `json:"url"`
	Title       string    `json:"title,omitempty"`
	Snippet     string    `json:"snippet,omitempty"`
	Content     string    `json:"content,omitempty"`
	Lane        string    `json:"lane"`
	ClaimType   string    `json:"claim_type"`
	SourceTier  string    `json:"source_tier"`
	Country     string    `json:"country,omitempty"`
	Date        string    `json:"date,omitempty"`
	RetrievedAt time.Time `json:"retrieved_at"`
	Status      string    `json:"status"`
}

// MarketLandscapeLeadCandidate is a company name extracted from a discovery
// lead and queued for company-specific verification.
type MarketLandscapeLeadCandidate struct {
	Name        string `json:"name"`
	Country     string `json:"country,omitempty"`
	Reason      string `json:"reason,omitempty"`
	EvidenceURL string `json:"evidence_url,omitempty"`
	SourceType  string `json:"source_type,omitempty"`
	// MentionContext explains why a source named the candidate; it is not
	// treated as proof of operating identity until independently verified.
	MentionContext  string `json:"mention_context,omitempty"`
	SupportingQuote string `json:"supporting_quote,omitempty"`
	// Producer records the extractor's judgement that the page describes the
	// company as a producer, manufacturer or supplier of the target product
	// itself — the supply side. Buyer-side runs consume it through the
	// producer gate; zero-valued (false) on supplier-side runs.
	Producer  bool     `json:"producer,omitempty"`
	KeyPeople []string `json:"key_people,omitempty"`
	// Role is the extractor's page-language hint, later validated against the
	// evidence by the deterministic role classifier.
	Role string `json:"role,omitempty"`
}

// UnmarshalJSON tolerates the field names models use for a company's name: `name`,
// `company_name`, or `company`. Without this, a valid extraction batch whose model chose a
// synonym decoded to empty names and was silently rejected wholesale.
func (candidate *MarketLandscapeLeadCandidate) UnmarshalJSON(data []byte) error {
	type candidateAlias MarketLandscapeLeadCandidate
	aux := struct {
		CompanyName string `json:"company_name"`
		Company     string `json:"company"`
		*candidateAlias
	}{candidateAlias: (*candidateAlias)(candidate)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if strings.TrimSpace(candidate.Name) == "" {
		switch {
		case strings.TrimSpace(aux.CompanyName) != "":
			candidate.Name = aux.CompanyName
		case strings.TrimSpace(aux.Company) != "":
			candidate.Name = aux.Company
		}
	}
	return nil
}

// MarketLandscapeCompany is a normalized company candidate or result.
type MarketLandscapeCompany struct {
	EntityKey  string  `json:"entity_key"`
	Name       string  `json:"name"`
	Domain     string  `json:"domain,omitempty"`
	Country    string  `json:"country,omitempty"`
	Region     string  `json:"region,omitempty"`
	Activity   string  `json:"activity,omitempty"`
	Status     string  `json:"status"`
	Confidence float64 `json:"confidence"`
	// Role and RoleTags separate a manufacturer from a distributor, wholesaler
	// or trader. RoleTags retains secondary roles (a manufacturer that also
	// distributes is still filed under manufacturers).
	Role     string   `json:"role,omitempty"`
	RoleTags []string `json:"role_tags,omitempty"`
	// Presence distinguishes evidenced local/regional operation from a
	// global-only corporate footprint. HQCountry is only a headquarters hint;
	// it is never proof of local operating presence.
	Presence        string `json:"presence,omitempty"`
	PresenceCountry string `json:"presence_country,omitempty"`
	PresenceReason  string `json:"presence_reason,omitempty"`
	HQCountry       string `json:"hq_country,omitempty"`
	// InScope is true when the company counts toward the requested coverage
	// floor. Global/unknown companies remain visible but are false.
	InScope bool `json:"in_scope"`
	// Stage names the consuming stage a buyer-side (demand chain) run tied the
	// company to; empty on supplier-side runs.
	Stage string `json:"stage,omitempty"`
	// ProducerSignal marks a buyer-side run's determination that the company
	// describes itself as a producer of the product itself — the supply side.
	// Such a company is a buyer only with transactional or input-language
	// proof; false on supplier-side runs.
	ProducerSignal  bool   `json:"producer_signal,omitempty"`
	EvidenceType    string `json:"evidence_type,omitempty"`
	EvidenceSummary string `json:"evidence_summary,omitempty"`
	// Description is a short, evidence-backed summary for the result card. It
	// is empty when the collected snippets do not contain descriptive prose.
	Description      string                    `json:"description,omitempty"`
	Employees        string                    `json:"employees,omitempty"`
	Turnover         string                    `json:"turnover,omitempty"`
	FinancialYear    string                    `json:"financial_year,omitempty"`
	MetricsSource    string                    `json:"metrics_source,omitempty"`
	BusinessContact  []string                  `json:"business_contact,omitempty"`
	KeyPeople        []string                  `json:"key_people,omitempty"`
	KeyPeopleSources map[string]string         `json:"key_people_sources,omitempty"`
	Evidence         []MarketLandscapeEvidence `json:"evidence,omitempty"`
}

type MarketLandscapePersonCandidate struct {
	Name string `json:"name"`
}

// MarketLandscapeInference is the producer/seller first-pass category result. It is a fallback
// search dimension only, not a company or a result.
type MarketLandscapeInference struct {
	Category          string `json:"category,omitempty"`
	CategoryRationale string `json:"category_rationale,omitempty"`
}

// MarketCoverage is the producer/seller coverage check: a landscape should identify more than a
// handful of companies before its absence of verified results is treated as a data problem.
type MarketCoverage struct {
	Companies int  `json:"companies"`
	Minimum   int  `json:"minimum"`
	Pass      bool `json:"pass"`
	// Global counts identified companies that are visible in the report but
	// have no local/regional operating evidence for the requested geography.
	// They are deliberately excluded from Companies and the coverage floor.
	Global int `json:"global,omitempty"`
}

// MarketLandscapeGroup is one section of the producer/seller report: a role
// section for in-scope companies, or the separate Global / cross-border bucket.
type MarketLandscapeGroup struct {
	Key       string                   `json:"key"`
	Label     string                   `json:"label"`
	Presence  string                   `json:"presence,omitempty"`
	Companies []MarketLandscapeCompany `json:"companies,omitempty"`
}

// MarketQCVerdict records the QC outcome for every company the reviewer saw,
// including companies it kept. Verdict is "passed", "failed", or "unreviewed";
// Reason is retained even for a pass so the tab is auditable rather than a bare
// badge.
type MarketQCVerdict struct {
	Name           string `json:"name"`
	Verdict        string `json:"verdict"`
	Reason         string `json:"reason"`
	Status         string `json:"status,omitempty"`
	Country        string `json:"country,omitempty"`
	Domain         string `json:"domain,omitempty"`
	EvidenceURL    string `json:"evidence_url,omitempty"`
	Role           string `json:"role,omitempty"`
	Presence       string `json:"presence,omitempty"`
	PresenceReason string `json:"presence_reason,omitempty"`
	Stage          string `json:"stage,omitempty"`
}

// MarketLandscapeReview is the post-assembly producer/seller QC review. It is deterministic on
// coverage and applies only rejections of companies already present in the run.
type MarketLandscapeReview struct {
	Verdict  string               `json:"verdict"`
	Score    float64              `json:"score"`
	Rejected []DemandRejectedName `json:"rejected,omitempty"`
	Verdicts []MarketQCVerdict    `json:"verdicts,omitempty"`
	Passed   int                  `json:"passed"`
	Failed   int                  `json:"failed"`
	Missing  []string             `json:"missing,omitempty"`
	Coverage *MarketCoverage      `json:"coverage,omitempty"`
	Reason   string               `json:"reason,omitempty"`
}

// MarketLandscapeReport separates verified results from the auditable corpus.
type MarketLandscapeReport struct {
	ResearchID        string                    `json:"research_id"`
	Objective         string                    `json:"objective"`
	Activity          string                    `json:"activity"`
	ProductCategory   string                    `json:"product_category,omitempty"`
	CategoryRationale string                    `json:"category_rationale,omitempty"`
	CategorySource    string                    `json:"category_source,omitempty"`
	CategoryLanes     []string                  `json:"category_lanes,omitempty"`
	CategoryResults   []MarketLandscapeCompany  `json:"category_results,omitempty"`
	QCPrompt          string                    `json:"qc_prompt,omitempty"`
	QCPromptSource    string                    `json:"qc_prompt_source,omitempty"`
	Coverage          *MarketCoverage           `json:"coverage,omitempty"`
	Groups            []MarketLandscapeGroup    `json:"groups,omitempty"`
	Reviews           []MarketLandscapeReview   `json:"reviews,omitempty"`
	FallbackQueries   int                       `json:"fallback_queries,omitempty"`
	Results           []MarketLandscapeCompany  `json:"results,omitempty"`
	Appendix          []MarketLandscapeEvidence `json:"appendix,omitempty"`
	Lanes             []string                  `json:"lanes,omitempty"`
	Iterations        int                       `json:"iterations"`
	QueriesUsed       int                       `json:"queries_used"`
	CategoryQueries   int                       `json:"category_queries,omitempty"`
	CandidatesFound   int                       `json:"candidates_found"`
	LeadsReviewed     int                       `json:"leads_reviewed"`
	LeadsResolved     int                       `json:"leads_resolved"`
	SearchHits        int                       `json:"search_hits"`
	LeadsExtracted    int                       `json:"leads_extracted"`
	LeadsRejected     int                       `json:"leads_rejected"`
	PagesEnriched     int                       `json:"pages_enriched"`
	Warnings          []string                  `json:"warnings,omitempty"`
	StartedAt         time.Time                 `json:"started_at"`
	CompletedAt       time.Time                 `json:"completed_at"`
	Exports           []ExportArtifact          `json:"exports,omitempty"`
}
