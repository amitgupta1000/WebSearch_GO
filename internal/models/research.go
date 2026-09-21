package models

import (
	"encoding/json"
	"strings"
)

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

// ScrapeReportEntry records the outcome of attempting to use one target URL.
type ScrapeReportEntry struct {
	URL               string  `json:"url"`
	SourceType        string  `json:"source_type,omitempty"`
	Status            string  `json:"status"`
	Reason            string  `json:"reason,omitempty"`
	WordCount         int     `json:"word_count,omitempty"`
	QualityScore      float64 `json:"quality_score,omitempty"`
	ImagesFound       int     `json:"images_found,omitempty"`
	OCRStatus         string  `json:"ocr_status,omitempty"`
	OCRReason         string  `json:"ocr_reason,omitempty"`
	OCRImagesWithText int     `json:"ocr_images_with_text,omitempty"`
	Extractor         string  `json:"extractor,omitempty"`
	FallbackStatus    string  `json:"fallback_status,omitempty"`
	FallbackReason    string  `json:"fallback_reason,omitempty"`
	FallbackError     string  `json:"fallback_error,omitempty"`
	ContentTruncated  bool    `json:"content_truncated,omitempty"`
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

// UnmarshalJSON accepts follow_up_queries as either a list of query strings or a
// list of PlannedQuery objects. The model returns both shapes, and a decode
// failure here would otherwise discard the entire review, silently disabling the
// QC remediation loop.
func (review *QualityReview) UnmarshalJSON(data []byte) error {
	type reviewAlias QualityReview
	aux := struct {
		FollowUpQueries []json.RawMessage `json:"follow_up_queries"`
		*reviewAlias
	}{reviewAlias: (*reviewAlias)(review)}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	review.FollowUpQueries = nil
	for _, raw := range aux.FollowUpQueries {
		var plan PlannedQuery
		if err := json.Unmarshal(raw, &plan); err == nil && strings.TrimSpace(plan.Query) != "" {
			review.FollowUpQueries = append(review.FollowUpQueries, plan)
			continue
		}
		var query string
		if err := json.Unmarshal(raw, &query); err == nil && strings.TrimSpace(query) != "" {
			review.FollowUpQueries = append(review.FollowUpQueries, PlannedQuery{
				Query:      strings.TrimSpace(query),
				Purpose:    "QC follow-up",
				TargetArea: "general",
				Priority:   1,
			})
		}
	}
	return nil
}

// CompanyResearchContext is retained after a run for QC transparency and Q&A.
type CompanyResearchContext struct {
	Brief         CompanyBrief
	QueryPlan     QueryPlan
	SearchResults []SearchResult
	RankedResults []RankedResult
	Evidence      []Evidence
	ScrapeReport  []ScrapeReportEntry
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
