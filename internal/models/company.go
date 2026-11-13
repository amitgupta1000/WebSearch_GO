package models

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// OrgChart represents the synthesized organizational structure of a company.
type OrgChart struct {
	CompanyName  string              `json:"company_name"`
	Domain       string              `json:"domain,omitempty"`
	Overview     string              `json:"overview"`
	Brief        *CompanyProfile     `json:"brief,omitempty"`
	Headquarters string              `json:"headquarters,omitempty"`
	Leadership   []Employee          `json:"leadership"`
	Departments  []Department        `json:"departments"`
	Locations    []Location          `json:"locations,omitempty"`
	Images       []Image             `json:"images,omitempty"`
	Sources      []string            `json:"sources"`
	ScrapeReport []ScrapeReportEntry `json:"scrape_report,omitempty"`
	// Timings is attached by the agent after synthesis (like ScrapeReport) so the
	// console can show where the run's time went. It is never model output.
	Timings []PhaseTiming `json:"timings,omitempty"`
	// Exports lists the output files written for this run (Markdown and PDF).
	Exports []ExportArtifact `json:"exports,omitempty"`
	// SearchTrace is the raw search evidence the run collected — every planned query with
	// its kept and excluded hits — behind the Search results tab and the report appendix
	// artifact. Attached by the agent.
	SearchTrace *SearchTrace `json:"search_trace,omitempty"`
	// RocketReachDiscovery records strictly employer-matched RR candidates that
	// were supplied to company leadership validation.
	RocketReachDiscovery []RocketReachDiscovery `json:"rocketreach_discovery,omitempty"`
	// Validation is the freshness pass over the roster: per-row dates and
	// verdicts, plus what the bounded reverse lookups changed. Attached by the
	// agent like ScrapeReport and Timings.
	Validation *ValidationReport `json:"validation,omitempty"`
	// EntityGate is the record of what the confirmed identity filtered out, so a
	// short roster is explainable. Also attached by the agent.
	EntityGate *EntityGate `json:"entity_gate,omitempty"`
	// ResearchRoster preserves every person classified during the run, including
	// discoveries and validation exclusions that are not in the current org chart.
	ResearchRoster []CompanyResearchRow `json:"research_roster,omitempty"`
	LastUpdated    time.Time            `json:"last_updated"`
}

// CompanyProfile is the concise business brief synthesized alongside the org chart.
type CompanyProfile struct {
	BusinessDescription string     `json:"business_description,omitempty"`
	KeyProducts         []string   `json:"key_products,omitempty"`
	KeyLocations        []Location `json:"key_locations,omitempty"`
	Address             string     `json:"address,omitempty"`
	PublicEmail         string     `json:"public_email,omitempty"`
	Telephone           string     `json:"telephone,omitempty"`
}

// CompanyResearchRow is the flat, exportable person record shown by the full
// research roster. Status is deliberately limited to verified or unverified so
// the UI does not imply stronger certainty than the run established.
type CompanyResearchRow struct {
	Name             string                   `json:"name"`
	LinkedInURL      string                   `json:"linkedin_url,omitempty"`
	Designation      string                   `json:"designation,omitempty"`
	Location         string                   `json:"location,omitempty"`
	CareerHistory    []RocketReachCareerEntry `json:"career_history,omitempty"`
	Source           string                   `json:"source,omitempty"`
	Classification   string                   `json:"classification"`
	Status           string                   `json:"status"`
	Verification     string                   `json:"verification,omitempty"`
	VerificationNote string                   `json:"verification_note,omitempty"`
	ResearchQuery    string                   `json:"research_query,omitempty"`
	RocketReachID    int                      `json:"rocketreach_id,omitempty"`
}

// TimingTotalMillis is the summed duration of every recorded phase, used for the
// "total" figure in the console's timings panel.
func (chart OrgChart) TimingTotalMillis() int64 {
	var total int64
	for _, timing := range chart.Timings {
		total += timing.Millis
	}
	return total
}

// UnmarshalJSON accepts both the documented headquarters string and the
// location object sometimes returned by the model.
func (chart *OrgChart) UnmarshalJSON(data []byte) error {
	type orgChartAlias OrgChart
	aux := struct {
		Headquarters json.RawMessage `json:"headquarters"`
		Alternate    json.RawMessage `json:"org_chart"`
		*orgChartAlias
	}{orgChartAlias: (*orgChartAlias)(chart)}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.Headquarters) == 0 || bytes.Equal(aux.Headquarters, []byte("null")) {
		chart.applyAlternateOrgChart(aux.Alternate)
		return nil
	}

	var headquarters string
	if err := json.Unmarshal(aux.Headquarters, &headquarters); err == nil {
		chart.Headquarters = headquarters
		chart.applyAlternateOrgChart(aux.Alternate)
		return nil
	}

	var location Location
	if err := json.Unmarshal(aux.Headquarters, &location); err != nil {
		return fmt.Errorf("headquarters must be a string or location object: %w", err)
	}
	chart.Headquarters = formatLocation(location)
	chart.applyAlternateOrgChart(aux.Alternate)
	return nil
}

func (chart *OrgChart) applyAlternateOrgChart(raw json.RawMessage) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return
	}
	var alternate struct {
		Name       string              `json:"name"`
		Leadership []alternateEmployee `json:"leadership"`
	}
	if json.Unmarshal(raw, &alternate) != nil {
		return
	}
	if chart.CompanyName == "" {
		chart.CompanyName = alternate.Name
	}
	if len(chart.Leadership) == 0 {
		for _, leader := range alternate.Leadership {
			appendAlternateEmployee(&chart.Leadership, leader, "")
		}
	}
}

// EntityWarning explains an entity match that rests on the name alone, which is the
// ambiguous class: a same-named company matches it exactly the same way. It returns an
// empty string for the stronger keys, so healthy rows stay uncluttered.
func (person Employee) EntityWarning() string {
	switch strings.TrimSpace(person.EntityKey) {
	case "":
		return ""
	case EntityKeyName:
		return "matched by company name only"
	default:
		return ""
	}
}

// Freshness renders a person's date and verdict as one cell value, for example
// "2021-02-26 · stale" or "— · undated", followed by the row's verification note
// when it has one — the "unable to verify" remark a current-status lookup
// leaves. It returns an empty string when the row carries no freshness
// information at all, so charts from before the validation pass render exactly
// as they did.
func (person Employee) Freshness() string {
	date := strings.TrimSpace(person.AsOf)
	verdict := strings.TrimSpace(person.Verification)
	shown := ""
	switch {
	case date == "" && verdict == "":
		shown = ""
	case date == "":
		shown = "— · " + verdict
	case verdict == "":
		shown = date
	default:
		shown = date + " · " + verdict
	}
	return appendStatusRemark(shown, person.VerificationNote)
}

// FreshnessNote renders a person's freshness for prose, with the verification
// note attached so remarks like "unable to verify" travel with the row.
func (person Employee) FreshnessNote() string {
	date := strings.TrimSpace(person.AsOf)
	verdict := strings.TrimSpace(person.Verification)
	shown := ""
	switch {
	case date != "" && verdict != "":
		shown = fmt.Sprintf("as of %s · %s", date, verdict)
	case verdict != "":
		shown = verdict
	case date != "":
		shown = "as of " + date
	}
	return appendStatusRemark(shown, person.VerificationNote)
}

// appendStatusRemark attaches the row's verification note (conflict messages,
// the "unable to verify" remark) to a rendered verdict, so the remark travels
// with the row into every artifact instead of living only in the report
// appendix.
func appendStatusRemark(shown, note string) string {
	note = strings.TrimSpace(note)
	switch {
	case note == "":
		return shown
	case shown == "":
		return note
	default:
		return shown + " · " + note
	}
}

// UnverifiedSourceCount reports how many collected sources could not be used as
// evidence: policy-skipped profiles, blocked pages, and failed or empty scrapes.
// It lets the console distinguish "this function has no staff" from "we could not
// verify anyone".
func (chart *OrgChart) UnverifiedSourceCount() int {
	count := 0
	for _, entry := range chart.ScrapeReport {
		if entry.Status != "success" {
			count++
		}
	}
	return count
}

type alternateEmployee struct {
	Name          string              `json:"name"`
	Title         string              `json:"title"`
	DirectReports []alternateEmployee `json:"direct_reports"`
}

func appendAlternateEmployee(employees *[]Employee, source alternateEmployee, reportsTo string) {
	employee := Employee{Name: source.Name, Title: source.Title, ReportsTo: reportsTo}
	*employees = append(*employees, employee)
	for _, report := range source.DirectReports {
		appendAlternateEmployee(employees, report, source.Name)
	}
}

func formatLocation(location Location) string {
	parts := make([]string, 0, 3)
	if location.City != "" {
		parts = append(parts, location.City)
	}
	if location.Country != "" {
		parts = append(parts, location.Country)
	}
	if location.Address != "" {
		parts = append(parts, location.Address)
	}
	return strings.Join(parts, ", ")
}

// Department groups employees by function (Engineering, Sales, etc.).
type Department struct {
	Name        string     `json:"name"`
	Head        string     `json:"head,omitempty"`
	Description string     `json:"description,omitempty"`
	Members     []Employee `json:"members"`
}

// Employee represents a person discovered within an organization.
type Employee struct {
	Name         string   `json:"name"`
	Title        string   `json:"title"`
	PublicEmail  string   `json:"public_email,omitempty"`
	EmailSource  string   `json:"email_source,omitempty"`
	Department   string   `json:"department,omitempty"`
	Location     string   `json:"location,omitempty"`
	Bio          string   `json:"bio,omitempty"`
	PublicSource string   `json:"public_source,omitempty"`
	ReportsTo    string   `json:"reports_to,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	// AsOf is the newest date behind this row (YYYY-MM-DD), Verification is the
	// freshness verdict the validation pass reached, VerifiedBy lists the sources
	// behind it and VerificationNote explains a conflict. All four are attached
	// by the agent, never by the model.
	AsOf             string   `json:"as_of,omitempty"`
	Verification     string   `json:"verification,omitempty"`
	VerifiedBy       []string `json:"verified_by,omitempty"`
	VerificationNote string   `json:"verification_note,omitempty"`
	// EntityKey is how this person was tied to the researched company: domain,
	// registration-id, name or alias. A row matched only by name is the ambiguous
	// class — a same-named company can match the same way — so the artifacts show
	// the key rather than presenting every match as equally strong.
	EntityKey string `json:"entity_key,omitempty"`
}

// Location represents an office or regional operational hub.
type Location struct {
	City         string `json:"city"`
	Country      string `json:"country"`
	Address      string `json:"address,omitempty"`
	HeadcountEst string `json:"headcount_est,omitempty"`
}

// UnmarshalJSON accepts the location object the synthesis schema describes, a
// bare location string ("Ludhiana, Punjab, India") the model sometimes emits
// for brief.key_locations, and numeric or string headcount estimates.
func (location *Location) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		parseLocationText(location, text)
		return nil
	}

	type locationAlias Location
	aux := struct {
		HeadcountEst json.RawMessage `json:"headcount_est"`
		*locationAlias
	}{locationAlias: (*locationAlias)(location)}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.HeadcountEst) == 0 || bytes.Equal(aux.HeadcountEst, []byte("null")) {
		return nil
	}
	value, err := flexibleString(aux.HeadcountEst)
	if err != nil {
		return fmt.Errorf("headcount_est must be a string or number: %w", err)
	}
	location.HeadcountEst = value
	return nil
}

// parseLocationText fills a Location from a bare "City, Region, Country"
// string: the first comma part is the city, the last the country, and anything
// between them lands in Address so no detail is dropped.
func parseLocationText(location *Location, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	parts := strings.Split(text, ",")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	switch len(parts) {
	case 1:
		location.City = parts[0]
	case 2:
		location.City = parts[0]
		location.Country = parts[1]
	default:
		location.City = parts[0]
		location.Country = parts[len(parts)-1]
		location.Address = strings.Join(parts[1:len(parts)-1], ", ")
	}
}

func flexibleString(raw json.RawMessage) (string, error) {
	var stringValue string
	if err := json.Unmarshal(raw, &stringValue); err == nil {
		return stringValue, nil
	}
	var numberValue json.Number
	if err := json.Unmarshal(raw, &numberValue); err == nil {
		return numberValue.String(), nil
	}
	return "", fmt.Errorf("expected JSON string or number")
}
