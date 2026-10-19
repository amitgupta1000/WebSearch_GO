package models

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
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
	// Domain is the entity's primary domain. It is what query anchoring uses
	// (`site:<domain> …`), so extra domains never multiply the searches.
	Domain   string `json:"domain,omitempty"`
	Industry string `json:"industry,omitempty"`
	// Domains are additional domains the same confirmed entity publishes under — a
	// corporate site and a brand site, say. They widen what the gate *accepts* (a
	// source naming any of them is on-entity) without widening what it *searches*.
	// The operator confirms them one by one in the entity dialogue, because adding a
	// domain here is the strongest statement the gate can be given.
	Domains []string `json:"domains,omitempty"`
	// Aliases are other names the confirmed entity answers to ("CJ Shah & Co",
	// "KLJ Group"). They are how a brand or group relationship enters the entity
	// gate explicitly — confirmed by the operator — instead of being inferred by
	// the model, which is what let a "related group entity" clause pull unrelated
	// companies into one chart.
	Aliases []string `json:"aliases,omitempty"`
	// RegistrationID is the registry identifier (an Indian CIN, a company number).
	// It is the one key that cannot collide with a same-named company, and registry
	// sources publish it, so it is worth asking for.
	RegistrationID string `json:"registration_id,omitempty"`
	// EntityConfirmed is true when a human confirmed or edited this identity. The
	// gate runs either way; an unconfirmed identity is reported as such.
	EntityConfirmed bool `json:"entity_confirmed,omitempty"`
	// EntitySource records where the identity came from: user, resolved or typed.
	// "resolved" means the operator picked it in the entity dialogue.
	EntitySource string `json:"entity_source,omitempty"`
	ResearchPreferences
}

// Entity key kinds, strongest first. They are stable strings so the artifacts can
// show *how* a person row (or a candidate) was tied to the company.
const (
	EntityKeyDomain       = "domain"
	EntityKeyRegistration = "registration-id"
	EntityKeyName         = "name"
	EntityKeyAlias        = "alias"
)

// EntityMatch records how a source was tied to the researched entity. Matched is
// false when nothing in the source names the company, which is the case the gate
// exists for.
type EntityMatch struct {
	Matched bool `json:"matched"`
	// Key is domain, registration-id, name or alias; Value is the key that matched.
	Key        string  `json:"key,omitempty"`
	Value      string  `json:"value,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// OffEntityRow is one person the entity gate could not tie to the company. They are
// removed from the roster and listed in the artifact, so a reader sees what was
// excluded and why instead of wondering why a chart is short.
type OffEntityRow struct {
	Name   string `json:"name"`
	Title  string `json:"title,omitempty"`
	Place  string `json:"place,omitempty"`
	Source string `json:"source,omitempty"`
	Reason string `json:"reason"`
}

// EntityCandidate is one possible company identity offered to the operator before a
// run starts, so a name like "CJ Shah" is disambiguated by a human instead of guessed
// from search results.
type EntityCandidate struct {
	Name   string `json:"name"`
	Domain string `json:"domain,omitempty"`
	// RegistrationID is filled when a registry source publishes one (an Indian CIN
	// can be read straight out of a ZaubaCorp/Tofler URL), and RegistrationSource
	// names the page it was read from, so the operator can check it before using it.
	RegistrationID     string `json:"registration_id,omitempty"`
	RegistrationSource string `json:"registration_source,omitempty"`
	// NameMatch reports that the domain carries the company's name, which is the
	// strongest available hint that it is the company's own site. Candidates without
	// it are flagged in the dialogue, because gating a run on a rating agency's domain
	// would be worse than gating on no domain at all.
	NameMatch     bool    `json:"name_match,omitempty"`
	EvidenceTitle string  `json:"evidence_title,omitempty"`
	EvidenceURL   string  `json:"evidence_url,omitempty"`
	Score         float64 `json:"score,omitempty"`
}

// EntityGate summarises what the confirmed identity filtered: the keys in play, what
// was tagged, and what was excluded. It is attached by the agent, never by the
// model, and it is what makes "the roster is short because those people belong to
// another company" a statement the artifact can make.
type EntityGate struct {
	Keys      []string `json:"keys,omitempty"`
	Confirmed bool     `json:"confirmed"`
	// Mode is the gate's strictness for this run: enforce, report or off.
	Mode             string `json:"mode,omitempty"`
	QueriesDropped   int    `json:"queries_dropped,omitempty"`
	CandidatesTagged int    `json:"candidates_tagged,omitempty"`
	CandidatesNone   int    `json:"candidates_unmatched,omitempty"`
	SnippetsDropped  int    `json:"snippets_dropped,omitempty"`
	RowsOffEntity    int    `json:"rows_off_entity,omitempty"`
	// RescuedByLookup counts people the reverse lookup tied back to the company
	// after no collected source could, which is why they are in the chart.
	RescuedByLookup int `json:"rescued_by_lookup,omitempty"`
	// GapLookups and GapEvidence count the company-anchored lookups run *before*
	// synthesis for areas the evidence did not cover, and the sources they added.
	// They are the proactive half of the lookup policy: the rescue above validates
	// rows that exist, these supply the evidence a thin roster would lack.
	GapLookups  int `json:"gap_lookups,omitempty"`
	GapEvidence int `json:"gap_evidence,omitempty"`
	// LookupsDeferred counts people left unchecked because the run's lookup cap or
	// its time reserve ran out, so a reader knows the pass was truncated rather
	// than complete.
	LookupsDeferred int            `json:"lookups_deferred,omitempty"`
	OffEntity       []OffEntityRow `json:"off_entity,omitempty"`
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
	Title        string  `json:"title,omitempty"`
	Snippet      string  `json:"snippet,omitempty"`
	Relevant     bool    `json:"relevant"`
	Score        float64 `json:"score"`
	Reason       string  `json:"reason"`
	EvidenceType string  `json:"evidence_type"`
	SourceType   string  `json:"source_type,omitempty"`
	Official     bool    `json:"official"`
	QualityScore float64 `json:"quality_score"`
	// EntityKey is how this candidate was tied to the researched company: domain,
	// registration-id, name, alias, or empty when nothing in the candidate names it.
	EntityKey string `json:"entity_key,omitempty"`
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
	// AsOf is the newest date this source itself proves about its content, and
	// DateSignal names where that date came from (linkedin-activity, json-ld,
	// last-modified, url, content). A zero AsOf means the source carries no
	// date at all — which for an aggregator listing is the normal case, and is
	// exactly what makes a stale person indistinguishable from a current one
	// unless the date is recorded here.
	// LastModified is the response validator the fetch path recorded. It is kept
	// only so the dating pass can fall back to it when a document carries no
	// date of its own.
	LastModified string    `json:"last_modified,omitempty"`
	AsOf         time.Time `json:"as_of,omitempty"`
	DateSignal   string    `json:"date_signal,omitempty"`
}

// ValidationLookup records one bounded reverse-search check of a person row.
type ValidationLookup struct {
	Person string `json:"person"`
	Query  string `json:"query"`
	// Status is promoted, refreshed, rescued, unchanged, no-results or error.
	Status string `json:"status"`
	// AsOf is the newest dated mention the lookup found, and Source the URL it
	// came from. Both are empty when nothing dated was found.
	AsOf   string `json:"as_of,omitempty"`
	Source string `json:"source,omitempty"`
	// MatchedCompany reports that a result names both the person and the company,
	// which is what rescues a row no collected source could tie to the company.
	// MatchedKey is the strongest key that result matched with.
	MatchedCompany          bool   `json:"matched_company,omitempty"`
	MatchedKey              string `json:"matched_key,omitempty"`
	CurrentEmployerConflict bool   `json:"current_employer_conflict,omitempty"`
	CurrentStatus           string `json:"current_status,omitempty"`
	Detail                  string `json:"detail,omitempty"`
}

// ValidationReport summarises the freshness pass over a finished chart: what
// each person row is backed by, and what the bounded reverse lookups changed.
// It is attached by the agent (never model output), like ScrapeReport.
type ValidationReport struct {
	Lookups     []ValidationLookup `json:"lookups,omitempty"`
	LookupsUsed int                `json:"lookups_used"`
	RowsChecked int                `json:"rows_checked"`
	RowsRecent  int                `json:"rows_recent"`
	RowsDated   int                `json:"rows_dated"`
	RowsStale   int                `json:"rows_stale"`
	RowsUndated int                `json:"rows_undated"`
	Conflicts   []string           `json:"conflicts,omitempty"`
	// ExcludedRows records people removed from the current chart by current-employment checks.
	ExcludedRows []OffEntityRow `json:"excluded_rows,omitempty"`
	// Skipped names why no reverse lookups ran (validation disabled, out of
	// budget, or nothing needed checking).
	Skipped string `json:"skipped,omitempty"`
}

// Verification verdicts attached to a person row.
const (
	// VerificationRecent means a dated source within the recent window from a
	// company-owned site or at least two independent domains.
	VerificationRecent = "verified-recent"
	// VerificationDated means a dated source exists but it is neither recent nor
	// corroborated enough to call current.
	VerificationDated = "dated"
	// VerificationStale means the newest source is older than the stale window.
	VerificationStale = "stale"
	// VerificationConflicting means another person claims the same title.
	VerificationConflicting = "conflicting"
	// VerificationUndated means no source carried a date at all.
	VerificationUndated = "undated"
	// VerificationCurrentUnverified means historical evidence exists but current
	// employment could not be established.
	VerificationCurrentUnverified = "current-unverified"
	// VerificationHistorical means a source explicitly identifies a former,
	// retired or deceased association.
	VerificationHistorical = "historical"
)

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
	// HandlerStatus records the hard-URL handler outcome when the bounded
	// escalation ladder ran for this URL: recovered, failed, deferred or
	// unavailable. HandlerTactics names the ladder step that produced the
	// result (recovered URLs only).
	HandlerStatus    string `json:"handler_status,omitempty"`
	HandlerTactics   string `json:"handler_tactics,omitempty"`
	ContentTruncated bool   `json:"content_truncated,omitempty"`
	// TriageDecision records the bounded pre-scrape document relevance decision.
	// It is set only for document candidates that went through the triage gate.
	TriageDecision   string  `json:"triage_decision,omitempty"`
	TriageConfidence float64 `json:"triage_confidence,omitempty"`
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

// PhaseTiming records how long one phase of a research run took, with a short
// human note about what it covered. Timings are what turn "the scraper feels
// slow" into a measurable claim, and they are reported per run.
type PhaseTiming struct {
	Phase  string `json:"phase"`
	Millis int64  `json:"millis"`
	Detail string `json:"detail,omitempty"`
}

// PhaseTiming is declared in this package's research vocabulary; the console
// formats it through Seconds so templates never do arithmetic.
func (timing PhaseTiming) Seconds() string {
	return strconv.FormatFloat(float64(timing.Millis)/1000, 'f', 1, 64)
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
	// Validation is the freshness pass over the finished chart, and EntityGate is the
	// record of what the confirmed company identity filtered. Both are attached by
	// the agent.
	Validation *ValidationReport
	// EntityGate is the record of what the entity gate filtered out.
	EntityGate *EntityGate
	// SearchTrace is the raw search evidence: every planned query with its kept and
	// excluded hits, per query. Attached by the agent.
	SearchTrace *SearchTrace
	// Timings is the per-phase breakdown of the run, in completion order.
	Timings []PhaseTiming
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
