package models

// RelatedEntityQuery starts a bounded search from a confirmed company identity.
type RelatedEntityQuery struct {
	CompanyName    string
	Domain         string
	Aliases        []string
	RegistrationID string
	Country        string
	SourceProfile  string
	MaxLeaders     int
}

// RelatedEntityEvidence is the source material supporting a relationship.
type RelatedEntityEvidence struct {
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	Source  string `json:"source,omitempty"`
}

// LeadershipSubject is a company-linked director or senior executive.
type LeadershipSubject struct {
	Name       string                  `json:"name"`
	Role       string                  `json:"role,omitempty"`
	Company    string                  `json:"company,omitempty"`
	Confidence float64                 `json:"confidence"`
	Evidence   []RelatedEntityEvidence `json:"evidence,omitempty"`
}

// RelatedCompany is a company connected to the primary company or its leaders.
type RelatedCompany struct {
	Name         string                  `json:"name"`
	Relationship string                  `json:"relationship"`
	Status       string                  `json:"status,omitempty"`
	Person       string                  `json:"person,omitempty"`
	Role         string                  `json:"role,omitempty"`
	Confidence   float64                 `json:"confidence"`
	Evidence     []RelatedEntityEvidence `json:"evidence,omitempty"`
}

// RelatedEntityReport keeps direct company relationships separate from
// relationships discovered through leadership.
type RelatedEntityReport struct {
	CompanyName         string              `json:"company_name"`
	Domain              string              `json:"domain,omitempty"`
	RegistrationID      string              `json:"registration_id,omitempty"`
	EntityConfirmed     bool                `json:"entity_confirmed"`
	DirectRelationships []RelatedCompany    `json:"direct_relationships,omitempty"`
	Leadership          []LeadershipSubject `json:"leadership,omitempty"`
	LeadershipRelations []RelatedCompany    `json:"leadership_relationships,omitempty"`
	QueriesUsed         int                 `json:"queries_used"`
	QueriesPlanned      int                 `json:"queries_planned"`
	SearchTrace         *SearchTrace        `json:"search_trace,omitempty"`
	Warnings            []string            `json:"warnings,omitempty"`
}
