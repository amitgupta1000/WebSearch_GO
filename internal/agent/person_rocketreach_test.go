package agent

import (
	"context"
	"fmt"
	"strings"
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

// scriptedRocketReach scripts the RocketReach source for the priority pipeline:
// lookups replay a fixed sequence, the search returns a fixed candidate set.
type scriptedRocketReach struct {
	lookupCalls  []string
	searchCalls  int
	lookupQueue  []*tools.RocketReachProfile
	searchResult []tools.RocketReachProfile
}

func (s *scriptedRocketReach) Enabled() bool { return true }

func (s *scriptedRocketReach) Lookup(ctx context.Context, name, company, linkedinURL string) (*tools.RocketReachProfile, error) {
	s.lookupCalls = append(s.lookupCalls, fmt.Sprintf("%s | %s | %s", name, company, linkedinURL))
	if len(s.lookupQueue) == 0 {
		return nil, nil
	}
	next := s.lookupQueue[0]
	s.lookupQueue = s.lookupQueue[1:]
	return next, nil
}

func (s *scriptedRocketReach) Search(ctx context.Context, query map[string][]string) ([]tools.RocketReachProfile, error) {
	s.searchCalls++
	return s.searchResult, nil
}

// TestLookupRocketReachEscalatesThroughSearchWhenTheLookupMisses pins the
// priority pipeline: when the one-shot lookup misses, the free search endpoint
// disambiguates the same-named profiles against the confirmed employer, and one
// pinned lookup fetches the full record with contact details.
func TestLookupRocketReachEscalatesThroughSearchWhenTheLookupMisses(t *testing.T) {
	source := &scriptedRocketReach{
		lookupQueue: []*tools.RocketReachProfile{
			nil, // the direct lookup misses
			{ // the pinned candidate lookup returns the full record
				Name:            "Hemant Jain",
				CurrentTitle:    "Managing Director",
				CurrentEmployer: "KLJ Resources Limited",
				LinkedInURL:     "https://www.linkedin.com/in/hemant-jain-klj",
				JobHistory:      []tools.RocketReachJob{{Title: "Managing Director", Company: "KLJ Resources Limited"}},
				Emails:          []tools.RocketReachContact{{Email: "hemant@example.com", Grade: "B"}},
			},
		},
		searchResult: []tools.RocketReachProfile{
			{Name: "Hemant Jain", CurrentEmployer: "Somebody Else Ltd", LinkedInURL: "https://www.linkedin.com/in/hemant-other"},
			{Name: "Hemant Jain", CurrentEmployer: "KLJ Resources Limited", LinkedInURL: "https://www.linkedin.com/in/hemant-jain-klj"},
		},
	}
	agent := &PersonAgent{rocketReach: source}

	result := agent.lookupRocketReach(context.Background(), "sess_test", models.PersonQuery{
		Name: "Hemant Jain", Company: "KLJ Resources Limited",
	}, nil)

	if result == nil {
		t.Fatal("expected the search fallback to recover the profile")
	}
	if source.searchCalls != 1 {
		t.Fatalf("expected exactly one free search, got %d", source.searchCalls)
	}
	if len(source.lookupCalls) != 2 {
		t.Fatalf("expected the direct lookup plus the pinned candidate lookup, got %v", source.lookupCalls)
	}
	if !strings.Contains(source.lookupCalls[1], "linkedin.com/in/hemant-jain-klj") {
		t.Fatalf("expected the pinned lookup to use the employer-matched LinkedIn URL, got %v", source.lookupCalls)
	}
	if len(result.Emails) != 1 || result.Emails[0].Email != "hemant@example.com" {
		t.Fatalf("expected the full record with contact details, got %+v", result)
	}
}

// TestLookupRocketReachChargesNothingWhenIdentityIsAmbiguous guards the paid
// call: without a company or a seed, a same-named crowd must not cost a lookup.
func TestLookupRocketReachChargesNothingWhenIdentityIsAmbiguous(t *testing.T) {
	source := &scriptedRocketReach{
		lookupQueue: []*tools.RocketReachProfile{nil},
		searchResult: []tools.RocketReachProfile{
			{Name: "Hemant Jain", CurrentEmployer: "Somebody Else Ltd"},
			{Name: "Hemant Jain", CurrentEmployer: "Totally Different Pvt Ltd"},
		},
	}
	agent := &PersonAgent{rocketReach: source}

	result := agent.lookupRocketReach(context.Background(), "sess_test", models.PersonQuery{Name: "Hemant Jain"}, nil)

	if result != nil {
		t.Fatalf("an unconfirmed identity must not produce a profile, got %+v", result)
	}
	if len(source.lookupCalls) != 1 {
		t.Fatalf("no pinned lookup may run on a guess, got %v", source.lookupCalls)
	}
}

// TestSelectRocketReachCandidatePrefersSeedThenEmployer pins the selection
// order: the operator's confirmed seed profile beats everything, then the
// employer-matched profile, and a lone name match is only taken when nothing
// about the identity was confirmed.
func TestSelectRocketReachCandidatePrefersSeedThenEmployer(t *testing.T) {
	profiles := []tools.RocketReachProfile{
		{Name: "Hemant Jain", CurrentEmployer: "KLJ Resources Limited", LinkedInURL: "https://www.linkedin.com/in/hemant-klj"},
		{Name: "Hemant Jain", CurrentEmployer: "OtherCo", LinkedInURL: "https://www.linkedin.com/in/hemant-seed"},
	}
	query := models.PersonQuery{Name: "Hemant Jain", Company: "KLJ Resources Limited", SeedURL: "https://www.linkedin.com/in/hemant-seed"}
	seeded := selectRocketReachCandidate(profiles, query)
	if seeded == nil || seeded.LinkedInURL != "https://www.linkedin.com/in/hemant-seed" {
		t.Fatalf("expected the operator's seed profile to win, got %+v", seeded)
	}
	query.SeedURL = ""
	matched := selectRocketReachCandidate(profiles, query)
	if matched == nil || matched.CurrentEmployer != "KLJ Resources Limited" {
		t.Fatalf("expected the employer match to win once no seed is present, got %+v", matched)
	}
	lone := selectRocketReachCandidate(profiles[:1], models.PersonQuery{Name: "hemant jain"})
	if lone == nil || lone.CurrentEmployer != "KLJ Resources Limited" {
		t.Fatalf("expected the lone name match without a company, got %+v", lone)
	}
}
