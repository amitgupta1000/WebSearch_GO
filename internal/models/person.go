package models

import "time"

// PersonProfile represents the structured research dossier of an individual.
type PersonProfile struct {
	FullName        string            `json:"full_name"`
	CurrentTitle    string            `json:"current_title"`
	CurrentCompany  string            `json:"current_company"`
	Location        string            `json:"location"`
	ExecutiveSummary string           `json:"executive_summary"`
	Experiences     []Experience      `json:"experiences"`
	Education       []Education       `json:"education"`
	KeySkills       []string          `json:"key_skills"`
	NotableProjects []string          `json:"notable_projects,omitempty"`
	OnlinePresence  map[string]string `json:"online_presence,omitempty"` // e.g. "github", "twitter", "homepage"
	Sources         []string          `json:"sources"`
	ConfidenceScore float64           `json:"confidence_score"`
	LastUpdated     time.Time         `json:"last_updated"`
}

// Experience details a past or current professional role.
type Experience struct {
	Role        string   `json:"role"`
	Company     string   `json:"company"`
	Location    string   `json:"location,omitempty"`
	Period      string   `json:"period,omitempty"`
	Highlights  []string `json:"highlights,omitempty"`
}

// Education details an academic degree or qualification.
type Education struct {
	Institution string `json:"institution"`
	Degree      string `json:"degree"`
	Field       string `json:"field,omitempty"`
	Year        string `json:"year,omitempty"`
}

// DisambiguationPrompt contains candidate matching candidates when query is ambiguous.
type DisambiguationPrompt struct {
	Name        string              `json:"name"`
	Candidates  []CandidateSummary  `json:"candidates"`
	Question    string              `json:"question"`
}

// CandidateSummary represents one possible person when multiple matches are found.
type CandidateSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CurrentRole string `json:"current_role"`
	Company     string `json:"company"`
	Location    string `json:"location"`
	Snippet     string `json:"snippet"`
}
