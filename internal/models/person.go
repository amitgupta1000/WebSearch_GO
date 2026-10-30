package models

import (
	"math"
	"time"
)

// PersonProfile represents the structured research dossier of an individual.
type PersonProfile struct {
	FullName                string                    `json:"full_name"`
	CurrentTitle            string                    `json:"current_title"`
	CurrentCompany          string                    `json:"current_company"`
	Location                string                    `json:"location"`
	ExecutiveSummary        string                    `json:"executive_summary"`
	Experiences             []Experience              `json:"experiences"`
	Education               []Education               `json:"education"`
	KeySkills               []string                  `json:"key_skills"`
	NotableProjects         []string                  `json:"notable_projects,omitempty"`
	OnlinePresence          map[string]string         `json:"online_presence,omitempty"` // e.g. "github", "twitter", "homepage"
	PublicInterests         []string                  `json:"public_interests,omitempty"`
	RecentThemes            []string                  `json:"recent_themes,omitempty"`
	PublicAccounts          []PublicAccount           `json:"public_accounts,omitempty"`
	PersonalizationEvidence []PersonalizationEvidence `json:"personalization_evidence,omitempty"`
	Sources                 []string                  `json:"sources"`
	ConfidenceScore         float64                   `json:"confidence_score"`
	LastUpdated             time.Time                 `json:"last_updated"`
	// Exports lists the output files written for this run (Markdown and PDF).
	Exports []ExportArtifact `json:"exports,omitempty"`
	// PersonGate records what the identity check kept and excluded, so a dossier can show why
	// a same-named person at another employer is not in it. It is the person-mode counterpart
	// of the company entity gate, attached by the agent and never by the model.
	PersonGate *PersonGate `json:"person_gate,omitempty"`
	// ScrapeReport lists every page-fetch attempt with its outcome, like company mode.
	ScrapeReport []ScrapeReportEntry `json:"scrape_report,omitempty"`
	// Timings lists where the run's time went, like company mode.
	Timings []PhaseTiming `json:"timings,omitempty"`
	// SearchQueries lists the queries the run issued, in order.
	SearchQueries []string `json:"search_queries,omitempty"`
	// SearchTrace is the raw search evidence the run collected — every planned query with
	// its kept and excluded hits — behind the Search results tab and the searches.json
	// artifact. Attached by the agent.
	SearchTrace *SearchTrace `json:"search_trace,omitempty"`
	// ContactEnrichment is populated only for an explicitly requested person
	// dossier/search; company and related-entity discovery never requests it.
	ContactEnrichment *PersonContactEnrichment `json:"contact_enrichment,omitempty"`
	// SameNamed is the multilateral view: the most relevant snippets for the other people who
	// share the confirmed name, grouped by the employer each source states, plus the material
	// that names the person but states no employer. Attached by the agent, never by the
	// model, because a person search answers with several people and the operator should see
	// all of them.
	SameNamed []PersonEvidenceGroup `json:"same_named,omitempty"`
}

type PersonContactEnrichment struct {
	Emails      []PersonContactEmail `json:"emails,omitempty"`
	Phones      []PersonContactPhone `json:"phones,omitempty"`
	LinkedInURL string               `json:"linkedin_url,omitempty"`
	Source      string               `json:"source,omitempty"`
}

type PersonContactEmail struct {
	Email string `json:"email"`
	Type  string `json:"type,omitempty"`
	Grade string `json:"grade,omitempty"`
}

type PersonContactPhone struct {
	Number string `json:"number"`
	Type   string `json:"type,omitempty"`
}

// PublicAccount is a social account discovered from public indexed evidence. It is
// intentionally separate from employment evidence and may be only a possible match.
type PublicAccount struct {
	Platform      string  `json:"platform"`
	URL           string  `json:"url"`
	IdentityBasis string  `json:"identity_basis"`
	Confidence    float64 `json:"confidence"`
}

// PersonalizationEvidence is public, non-sensitive context that may help tailor
// outreach. It is not employment or identity proof.
type PersonalizationEvidence struct {
	Platform      string  `json:"platform"`
	URL           string  `json:"url"`
	Summary       string  `json:"summary"`
	IdentityBasis string  `json:"identity_basis"`
	Confidence    float64 `json:"confidence"`
}

// PersonGate summarises the identity check a person run applies to everything it collects. A
// source reaches the dossier only when it names the person; when a company was confirmed, a
// source that states a *different* employer for that name is excluded, because the same name
// routinely belongs to several people at several employers.
type PersonGate struct {
	Name    string `json:"name"`
	Company string `json:"company,omitempty"`
	// Kept counts sources that name the person and the confirmed employer.
	Kept int `json:"kept"`
	// NameOnly counts sources that name the person but state no employer, which are kept and
	// labelled rather than trusted.
	NameOnly int `json:"name_only,omitempty"`
	// Dropped counts sources excluded by the gate, listed in Excluded.
	Dropped  int            `json:"dropped"`
	Excluded []OffEntityRow `json:"excluded,omitempty"`
}

// PersonEvidenceGroup collects the evidence for one identity other than the confirmed one —
// the same-named people a person search inevitably surfaces. A person query is answered by
// the search engine with several people who share the name; the dossier is about the
// confirmed one, and this is where the rest of the evidence is presented instead of being
// discarded, so the operator can judge the confirmation or spot the person they actually
// meant.
type PersonEvidenceGroup struct {
	// Employer keys the group: the employer a same-named person's source states. Empty means
	// the evidence names the person but states no employer, so it cannot be attributed.
	Employer string `json:"employer"`
	// Snippets are the most relevant hits for this identity, most-specific first.
	Snippets []SearchResult `json:"snippets"`
}

// ScorePercent returns the confidence score as a whole percentage (0-100) for display.
func (p *PersonProfile) ScorePercent() int {
	return int(math.Round(p.ConfidenceScore * 100))
}

// PersonCandidate is one person the typed name could mean, offered before a run starts so
// "Sanjay Gupta" is disambiguated by a human — the journalist, the surgeon or the chemicals
// executive — instead of by whichever profile the model preferred. What makes a person
// findable is context, so the candidate's role and employer are the payload: they become the
// company and location the run searches with.
type PersonCandidate struct {
	// Name is the name as the source spells it, which is often fuller than what was typed
	// ("Dr. Sanjay Kumar Gupta" for "Sanjay Gupta"). It is editable on the card, so a
	// correction can be confirmed rather than re-typed.
	Name string `json:"name"`
	// Title and Company are the role and employer the source states, when it states them.
	Title   string `json:"title,omitempty"`
	Company string `json:"company,omitempty"`
	// URL, EvidenceTitle and EvidenceSnippet are what the operator judges the candidate by,
	// and the URL is the page the run reads first once the identity is confirmed.
	URL             string `json:"url,omitempty"`
	EvidenceTitle   string `json:"evidence_title,omitempty"`
	EvidenceSnippet string `json:"evidence_snippet,omitempty"`
	// MatchesCompany and MatchesLocation report that the context already typed also appears
	// in this hit, which is the strongest hint that this is the right person.
	MatchesCompany  bool `json:"matches_company,omitempty"`
	MatchesLocation bool `json:"matches_location,omitempty"`
	// Directory is set when the hit comes from a people-directory rather than a profile or
	// the person's own site. Those pages are still worth offering — they state role and
	// employer — but they rank below a real profile.
	Directory bool    `json:"directory,omitempty"`
	Score     float64 `json:"score,omitempty"`
}

// PersonQuery is who to research plus the context that disambiguates them. The confirmation
// step fills Company and Location from the profile the operator picked, which replaces a
// context guessed from memory with one read off a source.
type PersonQuery struct {
	Name     string
	Company  string
	Location string
	Country  string
	// Confirmed reports that a human confirmed this identity in the confirmation step, and
	// Source records how it arrived: "resolved" or "typed".
	Confirmed bool
	Source    string
	// SeedURL, SeedTitle and SeedSnippet are the profile the identity was confirmed from.
	// The run reads that page first and always keeps its snippet, because the whole point of
	// confirming is that this is the page describing the right person.
	SeedURL     string
	SeedTitle   string
	SeedSnippet string
}

// Experience details a past or current professional role. Source is the URL from the collected
// evidence that supports the row: the synthesis prompt requires it, and dossier QC uses it to
// show what a dropped row was based on. Flag carries a QC warning — e.g. a timeline that
// overlaps another row at a different employer, which is either genuine concurrency or a
// blended row from a same-named person — set by QC, never by the model.
type Experience struct {
	Role       string   `json:"role"`
	Company    string   `json:"company"`
	Location   string   `json:"location,omitempty"`
	Period     string   `json:"period,omitempty"`
	Highlights []string `json:"highlights,omitempty"`
	Source     string   `json:"source,omitempty"`
	Flag       string   `json:"flag,omitempty"`
}

// Education details an academic degree or qualification.
type Education struct {
	Institution string `json:"institution"`
	Degree      string `json:"degree"`
	Field       string `json:"field,omitempty"`
	Year        string `json:"year,omitempty"`
	Source      string `json:"source,omitempty"`
}
