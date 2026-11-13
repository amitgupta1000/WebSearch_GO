package agent

import (
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
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
