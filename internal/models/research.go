package models

// ResearchPreferences captures scope that can be reused by company and person research.
type ResearchPreferences struct {
	Country         string   `json:"country,omitempty"`
	City            string   `json:"city,omitempty"`
	AreasOfInterest []string `json:"areas_of_interest,omitempty"`
	DesiredOutput   []string `json:"desired_output,omitempty"`
	TimeScope       string   `json:"time_scope,omitempty"`
	UserNotes       string   `json:"user_notes,omitempty"`
}

// CompanyBrief identifies a company and the user's research scope.
type CompanyBrief struct {
	CompanyName string `json:"company_name"`
	Domain      string `json:"domain,omitempty"`
	Industry    string `json:"industry,omitempty"`
	ResearchPreferences
}

// PlannedQuery is a search query generated for a specific research purpose.
type PlannedQuery struct {
	Query      string `json:"query"`
	Purpose    string `json:"purpose"`
	TargetArea string `json:"target_area"`
	Priority   int    `json:"priority"`
}

// QueryPlan contains the searches selected for a research brief.
type QueryPlan struct {
	Queries []PlannedQuery `json:"queries"`
}

// RankedResult records an LLM relevance judgment for a search result.
type RankedResult struct {
	URL          string  `json:"url"`
	Relevant     bool    `json:"relevant"`
	Score        float64 `json:"score"`
	Reason       string  `json:"reason"`
	EvidenceType string  `json:"evidence_type"`
	SourceType   string  `json:"source_type,omitempty"`
	Official     bool    `json:"official"`
	QualityScore float64 `json:"quality_score"`
}

// Evidence is a selected source prepared for final synthesis.
type Evidence struct {
	URL          string   `json:"url"`
	Title        string   `json:"title"`
	Content      string   `json:"content"`
	Headings     []string `json:"headings,omitempty"`
	JSONLD       []string `json:"json_ld,omitempty"`
	Images       []Image  `json:"images,omitempty"`
	Score        float64  `json:"score"`
	EvidenceType string   `json:"evidence_type"`
	SourceType   string   `json:"source_type,omitempty"`
	QualityScore float64  `json:"quality_score,omitempty"`
}

// QualityReview records a meta-agent evaluation of a completed research pass.
type QualityReview struct {
	Verdict           string         `json:"verdict"`
	Score             float64        `json:"score"`
	Gaps              []string       `json:"gaps,omitempty"`
	Contradictions    []string       `json:"contradictions,omitempty"`
	UnsupportedClaims []string       `json:"unsupported_claims,omitempty"`
	FollowUpQueries   []PlannedQuery `json:"follow_up_queries,omitempty"`
	Reason            string         `json:"reason,omitempty"`
}

// CompanyResearchContext is retained after a run for QC transparency and Q&A.
type CompanyResearchContext struct {
	Brief         CompanyBrief
	QueryPlan     QueryPlan
	SearchResults []SearchResult
	RankedResults []RankedResult
	Evidence      []Evidence
	Reviews       []QualityReview
	Iteration     int
}

// QuestionAnswer is the structured result of a post-research question.
type QuestionAnswer struct {
	Question         string   `json:"question"`
	Answer           string   `json:"answer"`
	Confidence       float64  `json:"confidence"`
	NeedsFreshSearch bool     `json:"needs_fresh_search"`
	SearchQuery      string   `json:"search_query,omitempty"`
	Sources          []string `json:"sources,omitempty"`
	UsedFreshSearch  bool     `json:"used_fresh_search"`
}
