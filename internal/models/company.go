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
	Exports     []ExportArtifact `json:"exports,omitempty"`
	LastUpdated time.Time        `json:"last_updated"`
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
	Department   string   `json:"department,omitempty"`
	Location     string   `json:"location,omitempty"`
	Bio          string   `json:"bio,omitempty"`
	PublicSource string   `json:"public_source,omitempty"`
	ReportsTo    string   `json:"reports_to,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// Location represents an office or regional operational hub.
type Location struct {
	City         string `json:"city"`
	Country      string `json:"country"`
	Address      string `json:"address,omitempty"`
	HeadcountEst string `json:"headcount_est,omitempty"`
}

// UnmarshalJSON accepts numeric or string headcount estimates from LLM output.
func (location *Location) UnmarshalJSON(data []byte) error {
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
