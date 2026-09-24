package models

import (
	"math"
	"time"
)

// PersonProfile represents the structured research dossier of an individual.
type PersonProfile struct {
	FullName         string            `json:"full_name"`
	CurrentTitle     string            `json:"current_title"`
	CurrentCompany   string            `json:"current_company"`
	Location         string            `json:"location"`
	ExecutiveSummary string            `json:"executive_summary"`
	Experiences      []Experience      `json:"experiences"`
	Education        []Education       `json:"education"`
	KeySkills        []string          `json:"key_skills"`
	NotableProjects  []string          `json:"notable_projects,omitempty"`
	OnlinePresence   map[string]string `json:"online_presence,omitempty"` // e.g. "github", "twitter", "homepage"
	Sources          []string          `json:"sources"`
	ConfidenceScore  float64           `json:"confidence_score"`
	LastUpdated      time.Time         `json:"last_updated"`
	// Exports lists the output files written for this run (Markdown and PDF).
	Exports []ExportArtifact `json:"exports,omitempty"`
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

// Experience details a past or current professional role.
type Experience struct {
	Role       string   `json:"role"`
	Company    string   `json:"company"`
	Location   string   `json:"location,omitempty"`
	Period     string   `json:"period,omitempty"`
	Highlights []string `json:"highlights,omitempty"`
}

// Education details an academic degree or qualification.
type Education struct {
	Institution string `json:"institution"`
	Degree      string `json:"degree"`
	Field       string `json:"field,omitempty"`
	Year        string `json:"year,omitempty"`
}
