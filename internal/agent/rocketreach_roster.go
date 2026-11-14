package agent

import (
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// ApplyRocketReachProfileToRoster merges an on-demand full RocketReach profile into the
// company's research roster. The free discovery search normally returns a summary, so the paid
// "Fetch RR" lookup is what supplies career history, education, skills and contact details; this
// function makes that lookup durable instead of leaving it in one transient row fragment.
//
// It matches on the RocketReach profile ID first, then the LinkedIn URL, then a unique
// normalised name with no conflicting URL. A non-matching profile is left alone rather than
// guessed into the wrong row. Only fields the provider actually returned are replaced, so a
// partial lookup cannot erase evidence the run already had.
func ApplyRocketReachProfileToRoster(chart *models.OrgChart, profile *tools.RocketReachProfile) bool {
	if chart == nil || profile == nil {
		return false
	}
	index := rocketReachRosterRowIndex(chart.ResearchRoster, profile)
	if index < 0 {
		return false
	}
	row := &chart.ResearchRoster[index]
	if history := rocketReachCareerHistory(profile.JobHistory); len(history) > 0 {
		row.CareerHistory = history
	}
	if education := rocketReachEducationRows(profile); len(education) > 0 {
		row.Education = education
	}
	if len(profile.Skills) > 0 {
		row.Skills = appendUniqueStrings(append([]string(nil), row.Skills...), profile.Skills...)
	}
	if emails, phones := rocketReachContactDetails(profile); len(emails) > 0 || len(phones) > 0 {
		if len(emails) > 0 {
			row.Emails = emails
		}
		if len(phones) > 0 {
			row.Phones = phones
		}
	}
	if row.LinkedInURL == "" {
		row.LinkedInURL = strings.TrimSpace(profile.LinkedInURL)
	}
	if row.Designation == "" {
		row.Designation = strings.TrimSpace(profile.CurrentTitle)
	}
	if row.Location == "" {
		row.Location = firstNonEmpty(profile.Location, joinNonEmpty(", ", profile.City, profile.Region, profile.Country))
	}
	if profile.ID > 0 {
		row.RocketReachID = profile.ID
	}
	if row.Source == "" {
		row.Source = "rocketreach.co"
	}
	if !strings.Contains(row.Classification, "RocketReach profile enrichment") {
		if strings.TrimSpace(row.Classification) == "" {
			row.Classification = "RocketReach profile enrichment"
		} else {
			row.Classification += "; RocketReach profile enrichment"
		}
	}
	row.EnrichedAt = time.Now().UTC().Format(time.RFC3339)
	return true
}

// rocketReachRosterRowIndex finds the roster row a profile belongs to. The ID and LinkedIn URL
// are exact; the name is only used when it is unique and does not contradict a known ID or URL.
func rocketReachRosterRowIndex(rows []models.CompanyResearchRow, profile *tools.RocketReachProfile) int {
	if profile.ID > 0 {
		for index := range rows {
			if rows[index].RocketReachID == profile.ID {
				return index
			}
		}
	}
	linkedIn := strings.TrimSpace(profile.LinkedInURL)
	if linkedIn != "" {
		for index := range rows {
			if strings.EqualFold(strings.TrimSpace(rows[index].LinkedInURL), linkedIn) {
				return index
			}
		}
	}
	name := normalizePersonName(profile.Name)
	if name == "" {
		return -1
	}
	matched := -1
	count := 0
	for index := range rows {
		row := rows[index]
		if row.RocketReachID != 0 && profile.ID != 0 && row.RocketReachID != profile.ID {
			continue
		}
		if row.LinkedInURL != "" && linkedIn != "" && !strings.EqualFold(strings.TrimSpace(row.LinkedInURL), linkedIn) {
			continue
		}
		if normalizePersonName(row.Name) != name {
			continue
		}
		matched = index
		count++
	}
	if count == 1 {
		return matched
	}
	return -1
}

// rocketReachEducationRows converts provider education records into dossier rows. The provider
// is recorded so an education line is not mistaken for a scraped page, even when the provider
// supplies a LinkedIn URL as its source link.
func rocketReachEducationRows(profile *tools.RocketReachProfile) []models.Education {
	if profile == nil || len(profile.Education) == 0 {
		return nil
	}
	source := strings.TrimSpace(profile.LinkedInURL)
	if source == "" {
		source = models.ProviderRocketReach
	}
	rows := make([]models.Education, 0, len(profile.Education))
	for _, education := range profile.Education {
		institution := firstNonEmpty(education.Institution, education.School)
		degree := education.Degree
		field := firstNonEmpty(education.Field, education.Major)
		year := firstNonEmpty(education.Year, joinNonEmpty(" - ", education.StartYear, education.EndYear))
		if institution == "" && degree == "" && field == "" {
			continue
		}
		rows = append(rows, models.Education{
			Institution: institution, Degree: degree, Field: field, Year: year,
			Source: source, Provider: models.ProviderRocketReach,
		})
	}
	return rows
}
