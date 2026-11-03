package agent

import (
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

func TestMergeRocketReachProfileAddsStructuredEvidenceWithoutReplacingDossierFacts(t *testing.T) {
	profile := &models.PersonProfile{
		CurrentTitle:   "Chief Executive Officer",
		CurrentCompany: "Acme",
		KeySkills:      []string{"Strategy"},
		Sources:        []string{"https://example.test/source"},
	}

	mergeRocketReachProfile(profile, &tools.RocketReachProfile{
		CurrentTitle:    "Director",
		CurrentEmployer: "Other Acme",
		Location:        "New Delhi",
		LinkedInURL:     "https://linkedin.com/in/example",
		JobHistory: []tools.RocketReachJob{{
			Title: "Director", Company: "Other Acme", StartDate: "2020", EndDate: "2023",
		}},
		Education: []tools.RocketReachEducation{{
			School: "Example University", Degree: "MBA", Major: "Finance", Year: "2019",
		}},
		Skills: []string{"Strategy", "Procurement"},
	})
	if profile.CurrentTitle != "Chief Executive Officer" || profile.CurrentCompany != "Acme" {
		t.Fatalf("RR facts replaced existing dossier facts: %+v", profile)
	}
	if profile.Location != "New Delhi" || len(profile.Experiences) != 1 || len(profile.Education) != 1 {
		t.Fatalf("structured RR facts not merged: %+v", profile)
	}
	if len(profile.KeySkills) != 2 || profile.OnlinePresence["linkedin"] == "" {
		t.Fatalf("RR skills or LinkedIn URL not merged: %+v", profile)
	}
	if len(profile.Sources) != 2 {
		t.Fatalf("RR source not retained: %+v", profile.Sources)
	}
}

func TestApplyRocketReachContactDropsInferredGradeFEmails(t *testing.T) {
	profile := &models.PersonProfile{}
	applyRocketReachContact(profile, &tools.RocketReachProfile{
		Emails: []tools.RocketReachContact{
			{Email: "pattern@example.com", Grade: "F"},
			{Email: "verified@example.com", Grade: "B"},
		},
	})
	if profile.ContactEnrichment == nil || len(profile.ContactEnrichment.Emails) != 1 {
		t.Fatalf("expected only non-inferred email, got %+v", profile.ContactEnrichment)
	}
	if profile.ContactEnrichment.Emails[0].Email != "verified@example.com" {
		t.Fatalf("unexpected retained email: %+v", profile.ContactEnrichment.Emails)
	}
}
