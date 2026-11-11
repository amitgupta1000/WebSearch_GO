package models

import "time"

// RelatedEntityQuery starts a bounded search from a confirmed company identity.
type RelatedEntityQuery struct {
	CompanyName    string
	Domain         string
	Aliases        []string
	RegistrationID string
	Country        string
	SourceProfile  string
	PersonName     string
	PersonDomain   string
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
	Identity   *PersonIdentity         `json:"identity,omitempty"`
}

type PersonIdentity struct {
	CanonicalName string                  `json:"canonical_name,omitempty"`
	DIN           string                  `json:"din,omitempty"`
	Confidence    float64                 `json:"confidence"`
	Attributes    []string                `json:"attributes,omitempty"`
	Evidence      []RelatedEntityEvidence `json:"evidence,omitempty"`
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

type RelatedEntityGraph struct {
	Nodes []RelatedEntityNode `json:"nodes,omitempty"`
	Edges []RelatedEntityEdge `json:"edges,omitempty"`
}

type RelatedEntityNode struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	LabelY int    `json:"label_y"`
}

type RelatedEntityEdge struct {
	From         string                  `json:"from"`
	FromLabel    string                  `json:"from_label"`
	To           string                  `json:"to"`
	ToLabel      string                  `json:"to_label"`
	Relationship string                  `json:"relationship"`
	Status       string                  `json:"status,omitempty"`
	Confidence   float64                 `json:"confidence"`
	Evidence     []RelatedEntityEvidence `json:"evidence,omitempty"`
	X1           int                     `json:"x1"`
	Y1           int                     `json:"y1"`
	X2           int                     `json:"x2"`
	Y2           int                     `json:"y2"`
}

// RelatedEntityReport keeps direct company relationships separate from
// relationships discovered through leadership.
type RelatedEntityReport struct {
	CompanyName          string                  `json:"company_name"`
	Domain               string                  `json:"domain,omitempty"`
	RegistrationID       string                  `json:"registration_id,omitempty"`
	EntityConfirmed      bool                    `json:"entity_confirmed"`
	DirectRelationships  []RelatedCompany        `json:"direct_relationships,omitempty"`
	Leadership           []LeadershipSubject     `json:"leadership,omitempty"`
	LeadershipRelations  []RelatedCompany        `json:"leadership_relationships,omitempty"`
	Graph                RelatedEntityGraph      `json:"graph"`
	QueriesUsed          int                     `json:"queries_used"`
	QueriesPlanned       int                     `json:"queries_planned"`
	CompanyQueriesUsed   int                     `json:"company_queries_used"`
	PeopleQueriesUsed    int                     `json:"people_queries_used"`
	SearchTrace          *SearchTrace            `json:"search_trace,omitempty"`
	Warnings             []string                `json:"warnings,omitempty"`
	Cache                RelatedEntityCache      `json:"cache"`
	SearchHits           int                     `json:"search_hits"`
	ReviewEvidence       []RelatedEntityEvidence `json:"review_evidence,omitempty"`
	RocketReachDiscovery []RocketReachDiscovery  `json:"rocketreach_discovery,omitempty"`
	Exports              []ExportArtifact        `json:"exports,omitempty"`
	CompletedAt          time.Time               `json:"completed_at,omitempty"`
}

type RocketReachDiscovery struct {
	ProfileID   int    `json:"profile_id,omitempty"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Employer    string `json:"employer,omitempty"`
	LinkedInURL string `json:"linkedin_url,omitempty"`
	Query       string `json:"query"`
}

// RelatedEntityCache records the mandatory cache preflight for a run.
type RelatedEntityCache struct {
	Checked int `json:"checked"`
	Hits    int `json:"hits"`
	Misses  int `json:"misses"`
}
