package agent

import (
	"net/url"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

const (
	researchRosterVerified   = "verified"
	researchRosterUnverified = "unverified"
)

func buildCompanyResearchRoster(chart *models.OrgChart, state *models.CompanyResearchContext) []models.CompanyResearchRow {
	if chart == nil {
		return nil
	}
	rows := make([]models.CompanyResearchRow, 0, len(chart.Leadership)+len(chart.Departments))
	byPerson := make(map[string]int)
	add := func(row models.CompanyResearchRow) {
		row.Name = strings.TrimSpace(row.Name)
		if row.Name == "" {
			return
		}
		key := normalizePersonName(row.Name)
		if key == "" {
			return
		}
		if index, exists := byPerson[key]; exists {
			mergeResearchRosterRow(&rows[index], row)
			return
		}
		if row.Status == "" {
			row.Status = researchRosterUnverified
		}
		byPerson[key] = len(rows)
		rows = append(rows, row)
	}

	for _, employee := range chart.Leadership {
		add(researchRosterEmployee(employee, "leadership"))
	}
	for _, department := range chart.Departments {
		for _, employee := range department.Members {
			row := researchRosterEmployee(employee, "department: "+department.Name)
			if row.Designation == "" {
				row.Designation = department.Name
			}
			add(row)
		}
	}

	if chart.Validation != nil {
		for _, excluded := range chart.Validation.ExcludedRows {
			add(models.CompanyResearchRow{
				Name:             excluded.Name,
				Designation:      excluded.Title,
				Source:           excluded.Source,
				Classification:   "excluded by validation",
				Status:           researchRosterUnverified,
				VerificationNote: excluded.Reason,
			})
		}
	}
	if chart.EntityGate != nil {
		for _, excluded := range chart.EntityGate.OffEntity {
			add(models.CompanyResearchRow{
				Name:             excluded.Name,
				Designation:      excluded.Title,
				Source:           excluded.Source,
				Classification:   "excluded by entity gate",
				Status:           researchRosterUnverified,
				VerificationNote: excluded.Reason,
			})
		}
	}
	if state != nil {
		for _, discovery := range state.RocketReachDiscovery {
			add(models.CompanyResearchRow{
				Name:             discovery.Name,
				LinkedInURL:      discovery.LinkedInURL,
				Designation:      discovery.Title,
				Location:         discovery.Location,
				CareerHistory:    discovery.CareerHistory,
				Source:           "rocketreach.co",
				Classification:   "RocketReach discovery",
				Status:           researchRosterUnverified,
				VerificationNote: discovery.Employer,
				ResearchQuery:    discovery.Query,
				RocketReachID:    discovery.ProfileID,
			})
		}
	}
	return rows
}

func researchRosterEmployee(employee models.Employee, classification string) models.CompanyResearchRow {
	return models.CompanyResearchRow{
		Name:             employee.Name,
		LinkedInURL:      linkedinURLFromSource(employee.PublicSource),
		Designation:      employee.Title,
		Location:         employee.Location,
		Source:           employee.PublicSource,
		Classification:   classification,
		Status:           researchRosterStatus(employee.Verification),
		Verification:     employee.Verification,
		VerificationNote: employee.VerificationNote,
	}
}

func researchRosterStatus(verification string) string {
	if strings.EqualFold(strings.TrimSpace(verification), models.VerificationRecent) {
		return researchRosterVerified
	}
	return researchRosterUnverified
}

func linkedinURLFromSource(source string) string {
	parsed, err := url.Parse(strings.TrimSpace(source))
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	if !strings.Contains(host, "linkedin.") || !strings.Contains(strings.ToLower(parsed.Path), "/in/") {
		return ""
	}
	return parsed.String()
}

func mergeResearchRosterRow(primary *models.CompanyResearchRow, duplicate models.CompanyResearchRow) {
	if primary.LinkedInURL == "" {
		primary.LinkedInURL = duplicate.LinkedInURL
	}
	if primary.Designation == "" {
		primary.Designation = duplicate.Designation
	}
	if primary.Location == "" {
		primary.Location = duplicate.Location
	}
	if len(primary.CareerHistory) == 0 {
		primary.CareerHistory = duplicate.CareerHistory
	}
	if primary.Source == "" {
		primary.Source = duplicate.Source
	}

	if primary.Verification == "" {
		primary.Verification = duplicate.Verification
	}
	if primary.VerificationNote == "" {
		primary.VerificationNote = duplicate.VerificationNote
	}
	if primary.ResearchQuery == "" {
		primary.ResearchQuery = duplicate.ResearchQuery
	}
	if primary.RocketReachID == 0 {
		primary.RocketReachID = duplicate.RocketReachID
	}
	if primary.Status != researchRosterVerified && duplicate.Status == researchRosterVerified {
		primary.Status = researchRosterVerified
	}
	if primary.Classification == "" {
		primary.Classification = duplicate.Classification
	} else if duplicate.Classification != "" && !strings.Contains(primary.Classification, duplicate.Classification) {
		primary.Classification += "; " + duplicate.Classification
	}
}
