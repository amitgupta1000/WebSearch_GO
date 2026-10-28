package models

import "time"

// LegalQuery is the identity a legal run starts from, plus the optional person
// names to check alongside the company. The company identity fields mirror the
// confirmed entity a company run carries (domain, aliases, registration id),
// because legal search has the same-named company problem in its sharpest form:
// court titles name parties, and a common business name matches dozens of
// unrelated entities. The CIN is the one key that cannot collide.
type LegalQuery struct {
	CompanyName string `json:"company_name"`
	// Domain, Aliases and RegistrationID come from the entity confirmation step,
	// exactly as a company run receives them. RegistrationID (an Indian CIN) is
	// searched as a term too: NCLT and ROC documents cite it, and a CIN in a
	// document is a decisive identity match.
	Domain         string   `json:"domain,omitempty"`
	Aliases        []string `json:"aliases,omitempty"`
	RegistrationID string   `json:"registration_id,omitempty"`
	// EntityConfirmed and EntitySource record how the identity arrived, the same
	// way CompanyBrief records them: confirmed by a human, resolved, or typed.
	EntityConfirmed bool   `json:"entity_confirmed,omitempty"`
	EntitySource    string `json:"entity_source,omitempty"`
	// Persons are the additional individuals to check alongside the company —
	// directors or officers the operator names explicitly. Each is searched as
	// "Name" "Company" co-occurrence, the same discipline as the reverse lookups
	// in company mode, because a bare person name matches unrelated people.
	Persons []string `json:"persons,omitempty"`
	// CaseTypes optionally focuses the run ("insolvency", "tax", "sebi", …).
	// Empty means all categories.
	CaseTypes []string `json:"case_types,omitempty"`
	// IncludeDirectors asks the run to discover the company's directors and key
	// persons from registry snippets first, and extend the person checks to
	// them — toggle-driven, and every discovered person is company-linked by
	// the same identity gate before their searches run.
	IncludeDirectors bool `json:"include_directors,omitempty"`
}

// LegalMatchKey records how a finding was tied to the researched party, the
// legal counterpart of the entity gate's keys, strongest first. "cin" is
// decisive; "name-only" is the ambiguous class a same-named company also
// matches, so those findings are grouped separately and labelled.
const (
	LegalKeyCIN      = "cin"
	LegalKeyName     = "exact-name"
	LegalKeyAlias    = "alias"
	LegalKeyNameOnly = "name-only"
)

// LegalInvolvement states how the researched party appears in a case. A judgment
// that merely cites an earlier case or mentions the company in passing is not
// exposure, so party status is parsed and reported rather than assumed.
const (
	LegalPartyTo    = "party"
	LegalMentioned  = "mentioned"
	LegalReferenced = "referenced"
)

// LegalRole states which side of a case the researched party is on, parsed from
// the case title's "Petitioner v. Respondent" structure. The distinction changes
// the risk reading: a company initiating an action (complainant, petitioner,
// appellant) is pursuing its rights, while a company on the responding side
// (respondent, defendant, accused) is the one defending. Deterministic where the
// title carries the structure; empty when the title offers no sides to read.
const (
	LegalRoleInitiated = "initiated"
	LegalRoleFacing    = "facing"
)

// LegalFinding is one case, order or regulatory action tied to a researched
// party. MatchKey says how strong the identity link is; Involvement, Category
// and Status are classified when a model call succeeds and left with their
// zero values plus a note when it does not — the finding itself is evidence
// collected, never discarded for want of a classification.
type LegalFinding struct {
	// Party is the searched party this finding came from ("company", a person's
	// name, or an alias), and PartyKind is "company" or "person".
	Party     string `json:"party"`
	PartyKind string `json:"party_kind,omitempty"`
	// MatchKey is one of the LegalKey constants; Reason explains the match in
	// the artifact ("CIN L… appears in the document", "matched by name only").
	MatchKey string `json:"match_key,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// Case identity as the source states it.
	CaseTitle string `json:"case_title"`
	Court     string `json:"court,omitempty"`
	Date      string `json:"date,omitempty"`
	Citation  string `json:"citation,omitempty"`
	URL       string `json:"url"`
	Snippet   string `json:"snippet,omitempty"`
	// Source names where the finding came from: "indiankanoon", "casemine", …
	Source string `json:"source,omitempty"`
	// Involvement, Role and Status are model-classified when available:
	// party/mentioned/referenced; initiated/facing (which side the researched
	// party is on — Role is also parsed deterministically from the case title
	// and survives a model outage); decided/pending/unknown.
	Involvement string  `json:"involvement,omitempty"`
	Role        string  `json:"role,omitempty"`
	Category    string  `json:"category,omitempty"`
	Status      string  `json:"status,omitempty"`
	Classified  bool    `json:"classified,omitempty"`
	Confidence  float64 `json:"confidence,omitempty"`
	// Iterations counts how many dated records of the same case were folded
	// into this finding. A case travels through stages — trial, appeal,
	// revision — and each stage is its own document; the latest one is the
	// current state of the matter, so it wins the row and the count says how
	// many earlier records were folded into it.
	Iterations int `json:"iterations,omitempty"`
}

// LegalPersonResult collects one named person's findings, so the report can
// show per-person exposure next to the company's. Findings also appear in the
// report-wide list; this grouping is for the reader.
type LegalPersonResult struct {
	Name     string         `json:"name"`
	Findings []LegalFinding `json:"findings,omitempty"`
	// Checked is false when the person was skipped because the query budget ran
	// out — reported rather than silently missing.
	Checked       bool   `json:"checked"`
	SkippedReason string `json:"skipped_reason,omitempty"`
	// DiscoveredVia names how this person entered the run: empty for a person
	// the operator named, or "registry snippet on zaubacorp.com" for one the
	// run discovered from company-linked snippets under the directors toggle.
	DiscoveredVia string `json:"discovered_via,omitempty"`
}

// LegalSourceReport records what one legal source contributed, including when
// it contributed nothing: a source that was unreachable or bot-walled is named
// with its reason, so "no findings" is never ambiguous between "none exist"
// and "we could not look".
type LegalSourceReport struct {
	Name    string `json:"name"`
	Queries int    `json:"queries"`
	Hits    int    `json:"hits"`
	Kept    int    `json:"kept"`
	// Unavailable explains why a source contributed nothing ("bot-walled,
	// snippet-only via search", "unreachable"), empty when it answered.
	Unavailable string `json:"unavailable,omitempty"`
}

// LegalRiskAssessment is the model's overall read of the litigation exposure the
// collected findings attach to the company, written from the findings' own
// snippets and classifications. Level uses the four-word scale below; Drivers
// name the specific matters that move the level. Attached by the agent; the
// deterministic fallback fills Level and Summary from counts when the model is
// unavailable, so the report always carries an honest baseline.
type LegalRiskAssessment struct {
	// Level is one of: none, low, moderate, high, critical.
	Level   string   `json:"level,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Drivers []string `json:"drivers,omitempty"`
	// Classified reports whether the model wrote this assessment. The fallback
	// assessment is count-derived and labelled as such.
	Classified         bool   `json:"classified,omitempty"`
	Note               string `json:"note,omitempty"`
	OpinionFilter      string `json:"opinion_filter,omitempty"`
	OpinionCutoff      string `json:"opinion_cutoff,omitempty"`
	EligibleFindings   int    `json:"eligible_findings"`
	ExcludedFindings   int    `json:"excluded_findings"`
	ExcludedOld        int    `json:"excluded_old,omitempty"`
	ExcludedInitiated  int    `json:"excluded_initiated,omitempty"`
	ExcludedUndated    int    `json:"excluded_undated,omitempty"`
	ExcludedNonCompany int    `json:"excluded_non_company,omitempty"`
}

// LegalReport is the result of a legal mode run: every finding the bounded
// search collected, grouped into what matched the confirmed party and what
// only shares its name, plus per-person results and per-source honesty.
// Attached by the agent, never the model.
type LegalReport struct {
	// CompanyName is the party the run was about; Domain, Aliases and
	// RegistrationID mirror the confirmed identity so the report is readable
	// without the original form.
	CompanyName     string   `json:"company_name"`
	Domain          string   `json:"domain,omitempty"`
	Aliases         []string `json:"aliases,omitempty"`
	RegistrationID  string   `json:"registration_id,omitempty"`
	EntityConfirmed bool     `json:"entity_confirmed,omitempty"`
	// EntitySource records where the identity came from: user, resolved or typed.
	EntitySource string `json:"entity_source,omitempty"`

	// Findings are the cases tied to the company (or its confirmed aliases/CIN);
	// SameNamed is the ambiguous class — same name, no stronger key — kept and
	// labelled because a legal check must not silently drop a same-named hit.
	Findings  []LegalFinding `json:"findings,omitempty"`
	SameNamed []LegalFinding `json:"same_named,omitempty"`

	// Persons holds one result per requested person, in request order.
	Persons []LegalPersonResult `json:"persons,omitempty"`

	// Summary is the model-written exposure narrative when the classification
	// pass ran; the fallback summary is a deterministic count line.
	Summary string `json:"summary,omitempty"`

	// Risk is the overall legal-risk assessment assembled from the findings'
	// snippets and classifications. Always present on a finished report — the
	// deterministic fallback fills it when the model is unavailable.
	Risk *LegalRiskAssessment `json:"risk,omitempty"`

	// Sources reports what each legal source contributed.
	Sources []LegalSourceReport `json:"sources,omitempty"`

	// SearchTrace carries the legal queries and their hits, so the run's
	// searches artifact shows exactly what was searched and what it returned.
	SearchTrace  *SearchTrace        `json:"search_trace,omitempty"`
	ScrapeReport []ScrapeReportEntry `json:"scrape_report,omitempty"`
	Timings      []PhaseTiming       `json:"timings,omitempty"`
	// Exports lists the output files written for this run (Markdown and PDF).
	Exports     []ExportArtifact `json:"exports,omitempty"`
	LastUpdated time.Time        `json:"last_updated"`
}

// FindingCount returns the total number of collected findings across both
// groups, for summaries and the console badge.
func (r *LegalReport) FindingCount() int {
	return len(r.Findings) + len(r.SameNamed)
}
