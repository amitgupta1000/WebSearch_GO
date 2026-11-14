package agent

import (
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

func TestBuildCompanyResearchRosterPreservesClassifiedRows(t *testing.T) {
	chart := &models.OrgChart{
		Leadership: []models.Employee{{
			Name:         "Asha Rao",
			Title:        "Chief Executive Officer",
			PublicSource: "https://www.linkedin.com/in/asha-rao",
			Verification: models.VerificationRecent,
		}},
		Departments: []models.Department{{
			Name: "Sales",
			Members: []models.Employee{{
				Name:  "Asha Rao",
				Title: "Chief Executive Officer",
			}},
		}},
		Validation: &models.ValidationReport{ExcludedRows: []models.OffEntityRow{{
			Name:   "Former Leader",
			Title:  "Director",
			Source: "https://example.com/former-leader",
			Reason: "current employer conflict",
		}}},
		EntityGate: &models.EntityGate{OffEntity: []models.OffEntityRow{{
			Name:   "Wrong Company",
			Title:  "Managing Director",
			Source: "https://example.com/wrong-company",
			Reason: "company name not present in the source",
		}}},
	}
	state := &models.CompanyResearchContext{RocketReachDiscovery: []models.RocketReachDiscovery{
		{Name: "Asha Rao", LinkedInURL: "https://www.linkedin.com/in/asha-rao", Employer: "Acme", Query: "Asha Rao Acme"},
		{Name: "New Candidate", Title: "VP Sales", Employer: "Acme", Query: "New Candidate Acme"},
	}}

	rows := buildCompanyResearchRoster(chart, state)
	if len(rows) != 4 {
		t.Fatalf("expected current, excluded, and discovered rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].Status != "verified" || rows[0].LinkedInURL == "" {
		t.Fatalf("expected verified LinkedIn-backed leader, got %+v", rows[0])
	}
	if rows[1].Name != "Former Leader" || rows[1].Status != "unverified" {
		t.Fatalf("expected excluded row to remain unverified, got %+v", rows[1])
	}
	if rows[2].Name != "Wrong Company" || rows[2].Classification != "excluded by entity gate" {
		t.Fatalf("expected entity-gate row to remain classified, got %+v", rows[2])
	}
	if rows[3].Name != "New Candidate" || rows[3].Classification != "RocketReach discovery" {
		t.Fatalf("expected unmatched RocketReach discovery to remain classified, got %+v", rows[2])
	}
}

// TestApplyRocketReachProfileToRosterMergesFullProfile pins the persistence contract: the paid
// Fetch RR result is written into the same roster row (matched by profile ID) and survives the
// turn, instead of living only in the transient row fragment.
func TestApplyRocketReachProfileToRosterMergesFullProfile(t *testing.T) {
	chart := &models.OrgChart{ResearchRoster: []models.CompanyResearchRow{
		{Name: "Hemant Jain", RocketReachID: 77, Source: "rocketreach.co"},
		{Name: "Other Person", RocketReachID: 88, Source: "example.com"},
	}}
	profile := &tools.RocketReachProfile{
		ID: 77, Name: "Hemant Jain", CurrentTitle: "Managing Director", Location: "Mumbai",
		LinkedInURL: "https://www.linkedin.com/in/hemant-jain",
		JobHistory: []tools.RocketReachJob{{
			Title: "Managing Director", Company: "KLJ Resources Limited", StartDate: "2016", EndDate: "2020",
		}},
		Education: []tools.RocketReachEducation{{Institution: "IIT Bombay", Degree: "B.Tech"}},
		Skills:    []string{"Procurement", "Operations"},
		Emails: []tools.RocketReachContact{
			{Email: "pattern@example.com", Grade: "F"},
			{Email: "verified@example.com", Grade: "B"},
		},
		Phones: []tools.RocketReachPhone{{Number: "+91 99999 00000", Type: "mobile"}},
	}

	if !ApplyRocketReachProfileToRoster(chart, profile) {
		t.Fatal("expected the profile to be merged into the matching roster row")
	}
	row := chart.ResearchRoster[0]
	if len(row.CareerHistory) != 1 || row.CareerHistory[0].Company != "KLJ Resources Limited" {
		t.Fatalf("career history was not persisted: %+v", row.CareerHistory)
	}
	if len(row.Education) != 1 || row.Education[0].Provider != models.ProviderRocketReach {
		t.Fatalf("education was not persisted with its provider: %+v", row.Education)
	}
	if len(row.Skills) != 2 || len(row.Emails) != 1 || row.Emails[0].Email != "verified@example.com" {
		t.Fatalf("skills or verified contacts were not persisted: %+v / %+v", row.Skills, row.Emails)
	}
	if len(row.Phones) != 1 || row.EnrichedAt == "" || row.LinkedInURL == "" {
		t.Fatalf("phone, timestamp, or LinkedIn URL missing: %+v", row)
	}
	if chart.ResearchRoster[1].Name != "Other Person" || chart.ResearchRoster[1].EnrichedAt != "" {
		t.Fatalf("a different roster row must not be touched: %+v", chart.ResearchRoster[1])
	}

	// Name-only matching is the fallback for a row the discovery search did not give an ID.
	nameChart := &models.OrgChart{ResearchRoster: []models.CompanyResearchRow{{Name: "Hemant Jain"}}}
	if !ApplyRocketReachProfileToRoster(nameChart, profile) || nameChart.ResearchRoster[0].RocketReachID != 77 {
		t.Fatalf("expected a unique name match to enrich the row, got %+v", nameChart.ResearchRoster)
	}
}

// TestSessionUpdateCompanyChartAppliesPostRunEnrichment pins the session half: a completed
// company chart can be mutated under the session lock, while a person result or a nil result is
// refused rather than panicking.
func TestSessionUpdateCompanyChartAppliesPostRunEnrichment(t *testing.T) {
	chart := &models.OrgChart{CompanyName: "KLJ Resources Limited"}
	session := &Session{Result: chart}
	called := false
	if !session.UpdateCompanyChart(func(target *models.OrgChart) bool {
		called = true
		target.CompanyName = "KLJ Resources Limited (enriched)"
		return true
	}) || !called {
		t.Fatal("expected the completed company chart to be updated")
	}
	updated, ok := session.Result.(*models.OrgChart)
	if !ok || updated.CompanyName != "KLJ Resources Limited (enriched)" {
		t.Fatalf("expected the session to publish the updated chart, got %+v", session.Result)
	}
	if chart.CompanyName != "KLJ Resources Limited" {
		t.Fatal("the previously published chart must remain an unchanged snapshot")
	}

	personSession := &Session{Result: &models.PersonProfile{FullName: "Hemant Jain"}}
	if personSession.UpdateCompanyChart(func(*models.OrgChart) bool { return true }) {
		t.Fatal("a person session must not accept a company chart update")
	}
	if (&Session{}).UpdateCompanyChart(func(*models.OrgChart) bool { return true }) {
		t.Fatal("a session without a result must not accept a company chart update")
	}
}
