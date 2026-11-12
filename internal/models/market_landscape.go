package models

import "time"

// MarketLandscapeQuery defines one bounded, one-run landscape investigation.
type MarketLandscapeQuery struct {
	Objective               string   `json:"objective"`
	Activity                string   `json:"activity"`
	Regions                 []string `json:"regions,omitempty"`
	Countries               []string `json:"countries,omitempty"`
	Exclusions              []string `json:"exclusions,omitempty"`
	MaxCompanies            int      `json:"max_companies,omitempty"`
	IncludeContacts         bool     `json:"include_contacts,omitempty"`
	IncludePeople           bool     `json:"include_people,omitempty"`
	UseRocketReachDiscovery bool     `json:"use_rocketreach_discovery,omitempty"`
}

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
	// Stage names the consuming stage a buyer-side (demand chain) run tied the
	// company to; empty on supplier-side runs.
	Stage string `json:"stage,omitempty"`
	// ProducerSignal marks a buyer-side run's determination that the company
	// describes itself as a producer of the product itself — the supply side.
	// Such a company is a buyer only with transactional or input-language
	// proof; false on supplier-side runs.
	ProducerSignal   bool                      `json:"producer_signal,omitempty"`
	EvidenceType     string                    `json:"evidence_type,omitempty"`
	EvidenceSummary  string                    `json:"evidence_summary,omitempty"`
	BusinessContact  []string                  `json:"business_contact,omitempty"`
	KeyPeople        []string                  `json:"key_people,omitempty"`
	KeyPeopleSources map[string]string         `json:"key_people_sources,omitempty"`
	Evidence         []MarketLandscapeEvidence `json:"evidence,omitempty"`
}

type MarketLandscapePersonCandidate struct {
	Name string `json:"name"`
}

// MarketLandscapeReport separates verified results from the auditable corpus.
type MarketLandscapeReport struct {
	ResearchID                    string                    `json:"research_id"`
	Objective                     string                    `json:"objective"`
	Activity                      string                    `json:"activity"`
	Results                       []MarketLandscapeCompany  `json:"results,omitempty"`
	Appendix                      []MarketLandscapeEvidence `json:"appendix,omitempty"`
	Lanes                         []string                  `json:"lanes,omitempty"`
	Iterations                    int                       `json:"iterations"`
	QueriesUsed                   int                       `json:"queries_used"`
	CandidatesFound               int                       `json:"candidates_found"`
	LeadsReviewed                 int                       `json:"leads_reviewed"`
	LeadsResolved                 int                       `json:"leads_resolved"`
	SearchHits                    int                       `json:"search_hits"`
	LeadsExtracted                int                       `json:"leads_extracted"`
	LeadsRejected                 int                       `json:"leads_rejected"`
	PagesEnriched                 int                       `json:"pages_enriched"`
	RocketReachDiscoveryCalls     int                       `json:"rocketreach_discovery_calls,omitempty"`
	RocketReachProfilesFound      int                       `json:"rocketreach_profiles_found,omitempty"`
	RocketReachCandidatesPromoted int                       `json:"rocketreach_candidates_promoted,omitempty"`
	RocketReachDiscoveryEnabled   bool                      `json:"rocketreach_discovery_enabled,omitempty"`
	Warnings                      []string                  `json:"warnings,omitempty"`
	StartedAt                     time.Time                 `json:"started_at"`
	CompletedAt                   time.Time                 `json:"completed_at"`
	Exports                       []ExportArtifact          `json:"exports,omitempty"`
}
