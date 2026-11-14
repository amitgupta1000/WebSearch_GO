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
	if profile.Experiences[0].Provider != models.ProviderRocketReach || profile.Education[0].Provider != models.ProviderRocketReach {
		t.Fatalf("RR rows must carry their provider, got %+v / %+v", profile.Experiences[0], profile.Education[0])
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
// lookups replay a fixed sequence, the search returns scripted result sets (one
// per call when several passes are expected), and every query is recorded.
type scriptedRocketReach struct {
	lookupCalls     []string
	lookupByIDCalls []string
	searchCalls     int
	searchQueries   []map[string][]string
	lookupQueue     []*tools.RocketReachProfile
	searchResult    []tools.RocketReachProfile
	searchResults   [][]tools.RocketReachProfile
}

func (s *scriptedRocketReach) Enabled() bool { return true }

func (s *scriptedRocketReach) Lookup(ctx context.Context, name, company, linkedinURL string) (*tools.RocketReachProfile, error) {
	s.lookupCalls = append(s.lookupCalls, fmt.Sprintf("%s | %s | %s", name, company, linkedinURL))
	return s.nextLookup(), nil
}

func (s *scriptedRocketReach) LookupByID(ctx context.Context, id int) (*tools.RocketReachProfile, error) {
	s.lookupByIDCalls = append(s.lookupByIDCalls, fmt.Sprintf("%d", id))
	return s.nextLookup(), nil
}

func (s *scriptedRocketReach) nextLookup() *tools.RocketReachProfile {
	if len(s.lookupQueue) == 0 {
		return nil
	}
	next := s.lookupQueue[0]
	s.lookupQueue = s.lookupQueue[1:]
	return next
}

func (s *scriptedRocketReach) Search(ctx context.Context, query map[string][]string) ([]tools.RocketReachProfile, error) {
	s.searchCalls++
	s.searchQueries = append(s.searchQueries, query)
	if len(s.searchResults) > 0 {
		next := s.searchResults[0]
		s.searchResults = s.searchResults[1:]
		return next, nil
	}
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

// TestSelectRocketReachCandidateUsesCareerHistoryWhenCurrentEmployerDiffers pins the
// disambiguation gap: a director or long-serving employee whose RocketReach current employer has
// moved on is still the confirmed identity when their career history names the company.
func TestSelectRocketReachCandidateUsesCareerHistoryWhenCurrentEmployerDiffers(t *testing.T) {
	query := models.PersonQuery{Name: "Hemant Jain", Company: "KLJ Resources Limited"}
	past := tools.RocketReachProfile{
		Name: "Hemant Jain", CurrentEmployer: "Northern Polymers Ltd", LinkedInURL: "https://www.linkedin.com/in/hemant-northern",
		JobHistory: []tools.RocketReachJob{
			{Title: "Director", Company: "KLJ Resources Limited", StartDate: "2010", EndDate: "2016"},
			{Title: "Managing Director", Company: "KLJ Resources Limited", StartDate: "2016", EndDate: "2020"},
		},
	}
	current := tools.RocketReachProfile{Name: "Hemant Jain", CurrentEmployer: "KLJ Resources Limited", LinkedInURL: "https://www.linkedin.com/in/hemant-klj"}

	selected, basis := selectRocketReachCandidateWithBasis([]tools.RocketReachProfile{past, current}, query)
	if selected == nil || selected.CurrentEmployer != "KLJ Resources Limited" || !strings.Contains(basis, "current employer") {
		t.Fatalf("a current employer must outrank a past role, got %+v via %q", selected, basis)
	}

	selected, basis = selectRocketReachCandidateWithBasis([]tools.RocketReachProfile{past}, query)
	if selected == nil || selected.LinkedInURL != past.LinkedInURL || !strings.Contains(basis, "past role") {
		t.Fatalf("expected the past-role match to select the profile, got %+v via %q", selected, basis)
	}

	newer := past
	newer.JobHistory = []tools.RocketReachJob{{Title: "Chairman", Company: "KLJ Resources Limited", StartDate: "2018", EndDate: "2022"}}
	selected, _ = selectRocketReachCandidateWithBasis([]tools.RocketReachProfile{past, newer}, query)
	if selected == nil || selected.JobHistory[0].EndDate != "2022" {
		t.Fatalf("expected the most recent past role to win the tie, got %+v", selected)
	}
}

// TestLookupRocketReachRetriesWithoutCurrentEmployerForPastRole pins the bounded second free
// search: the current-employer filter is dropped once, the past role selects the candidate, and
// the paid lookup is pinned to the profile ID the search returned rather than re-resolving the
// same name.
func TestLookupRocketReachRetriesWithoutCurrentEmployerForPastRole(t *testing.T) {
	source := &scriptedRocketReach{
		lookupQueue: []*tools.RocketReachProfile{
			nil, // the direct lookup misses
			{
				Name: "Hemant Jain", CurrentTitle: "Managing Director", CurrentEmployer: "Northern Polymers Ltd",
				JobHistory: []tools.RocketReachJob{{Title: "Managing Director", Company: "KLJ Resources Limited", StartDate: "2016", EndDate: "2020"}},
				Emails:     []tools.RocketReachContact{{Email: "hemant@example.com", Grade: "B"}},
			},
		},
		searchResults: [][]tools.RocketReachProfile{
			// The current-employer-filtered pass returns a namesake with no tie to KLJ.
			{{Name: "Hemant Jain", CurrentEmployer: "Northern Polymers Ltd"}},
			// The unfiltered pass returns the profile whose career history names KLJ.
			{{
				Name: "Hemant Jain", ID: 77, CurrentEmployer: "Northern Polymers Ltd",
				JobHistory: []tools.RocketReachJob{{Title: "Managing Director", Company: "KLJ Resources Limited", StartDate: "2016", EndDate: "2020"}},
			}},
		},
	}
	agent := &PersonAgent{rocketReach: source}

	result := agent.lookupRocketReach(context.Background(), "sess_test",
		models.PersonQuery{Name: "Hemant Jain", Company: "KLJ Resources Limited"}, nil)
	if result == nil || len(result.Emails) != 1 {
		t.Fatalf("expected the past-role candidate's full record, got %+v", result)
	}
	if source.searchCalls != 2 {
		t.Fatalf("expected the filtered pass and one unfiltered retry, got %d", source.searchCalls)
	}
	if _, filtered := source.searchQueries[0]["current_employer"]; !filtered {
		t.Errorf("the first pass must filter by current employer, got %+v", source.searchQueries[0])
	}
	if _, filtered := source.searchQueries[1]["current_employer"]; filtered {
		t.Errorf("the retry must drop the current-employer filter, got %+v", source.searchQueries[1])
	}
	if len(source.lookupByIDCalls) != 1 || source.lookupByIDCalls[0] != "77" {
		t.Fatalf("expected the paid lookup pinned to the search's profile ID, got %v", source.lookupByIDCalls)
	}
	if len(source.lookupCalls) != 1 {
		t.Fatalf("expected only the direct lookup before the search fallback, got %v", source.lookupCalls)
	}
}

// TestRocketReachPastRoleWarningNamesTheCurrentEmployer keeps the dossier honest when the match
// came from history: a former role is not presented as the current one without saying so.
func TestRocketReachPastRoleWarningNamesTheCurrentEmployer(t *testing.T) {
	warning := rocketReachPastRoleWarning(&tools.RocketReachProfile{
		CurrentEmployer: "Northern Polymers Ltd",
		JobHistory: []tools.RocketReachJob{{
			Title: "Managing Director", Company: "KLJ Resources Limited", StartDate: "2016", EndDate: "2020",
		}},
	}, "KLJ Resources Limited")
	for _, expected := range []string{"past role", "KLJ Resources Limited", "Northern Polymers Ltd", "2016 - 2020", "Managing Director"} {
		if !strings.Contains(warning, expected) {
			t.Errorf("warning is missing %q: %s", expected, warning)
		}
	}
	if warning := rocketReachPastRoleWarning(&tools.RocketReachProfile{
		CurrentEmployer: "KLJ Resources Limited",
		JobHistory:      []tools.RocketReachJob{{Company: "KLJ Resources Limited"}},
	}, "KLJ Resources Limited"); warning != "" {
		t.Errorf("a current-employer match must not warn, got %q", warning)
	}
}

// TestClearModelProviderDropsAHallucinatedProvider keeps the provider marker a trusted,
// agent-only signal: a model that echoes "provider": "RocketReach" cannot bypass QC.
func TestClearModelProviderDropsAHallucinatedProvider(t *testing.T) {
	profile := &models.PersonProfile{
		Experiences: []models.Experience{{Role: "Director", Company: "Invented Co", Provider: models.ProviderRocketReach}},
		Education:   []models.Education{{Institution: "Invented University", Provider: models.ProviderRocketReach}},
	}
	clearModelProvider(profile)
	if profile.Experiences[0].Provider != "" || profile.Education[0].Provider != "" {
		t.Fatalf("model-supplied provider markers must be cleared, got %+v / %+v", profile.Experiences[0], profile.Education[0])
	}
}

// TestQCPersonProfileKeepsRocketReachHistoryWithAFlag pins the provider-aware QC branch: a
// RocketReach career-history row at another employer is not a model guess, so it survives the
// employer gate with an explicit unverified flag instead of being dropped. A web row with no
// source tie is still dropped.
func TestQCPersonProfileKeepsRocketReachHistoryWithAFlag(t *testing.T) {
	gate := newPersonGate(models.PersonQuery{Name: "Hemant Jain", Company: "KLJ Resources Limited"})
	profile := &models.PersonProfile{Experiences: []models.Experience{
		{Role: "Managing Director", Company: "Northern Polymers Ltd", Period: "2016 - 2020", Provider: models.ProviderRocketReach},
		{Role: "Analyst", Company: "Unknown Co", Period: "2021 - 2022"},
	}}
	agent := &PersonAgent{engine: &Engine{}}

	excluded, lookups := agent.qcPersonProfile(context.Background(), profile, gate)
	if len(profile.Experiences) != 1 || profile.Experiences[0].Provider != models.ProviderRocketReach {
		t.Fatalf("expected the RocketReach row to survive QC, got %+v", profile.Experiences)
	}
	if !strings.Contains(profile.Experiences[0].Flag, "RocketReach") || !strings.Contains(profile.Experiences[0].Flag, "not independently verified") {
		t.Fatalf("expected the surviving row to carry its unverified provider flag, got %q", profile.Experiences[0].Flag)
	}
	if len(excluded) != 1 || lookups != 0 {
		t.Fatalf("expected the unsourced web row to be dropped without a lookup, excluded=%+v lookups=%d", excluded, lookups)
	}
}
