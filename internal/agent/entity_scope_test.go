package agent

import (
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestCompanyMentionMustBeNearThePerson is the fix for pages that merely mention the
// company. Verified live: a MoneyControl IPO story about Chemcon Speciality Chemicals
// contains "Sanjay Chemicals" twice, so a person named elsewhere on that page passed the
// old rule and became company evidence.
func TestCompanyMentionMustBeNearThePerson(t *testing.T) {
	matcher := newEntityMatcher(models.CompanyBrief{CompanyName: "Sanjay Chemicals", Domain: "sanjaychemindia.com"})

	near := "Chetan Jain, Chief Financial Officer at Sanjay Chemicals (India) Pvt Ltd, Mumbai."
	if !mentionsCompanyNear(near, "Chetan Jain", matcher) {
		t.Error("a mention beside the person's name must count as evidence about them")
	}

	far := strings.Repeat("The market for speciality chemicals grew again this year. ", 12) +
		"Chetan Jain is a finance professional who has worked in several industries. " +
		strings.Repeat("Analysts expect further consolidation in the sector. ", 8) +
		"Peer companies include Sanjay Chemicals, Chemcon Speciality Chemicals and others."
	if mentionsCompanyNear(far, "Chetan Jain", matcher) {
		t.Error("a company mention hundreds of characters away must not count")
	}
}

// TestGatherRowSourcesIgnoresDistantCompanyMentions covers the same rule where it decides a
// row: a source whose only company mention is far from the person cannot tie them to the
// company, so the row stays off-entity (and it is then the lookup's job to rescue them).
func TestGatherRowSourcesIgnoresDistantCompanyMentions(t *testing.T) {
	matcher := newEntityMatcher(models.CompanyBrief{CompanyName: "Sanjay Chemicals", Domain: "sanjaychemindia.com"})
	url := "https://www.moneycontrol.com/news/business/ipo/chemcon-speciality-chemicals-ipo.html"
	content := "Chetan Jain has been appointed to the audit committee. " +
		strings.Repeat("Chemcon Speciality Chemicals raised capital in its initial public offering. ", 40) +
		"Peer set: Sanjay Chemicals, Chemcon and others."

	views := buildEvidenceViews([]models.Evidence{{URL: url, Title: "IPO story", Content: content}})
	employee := models.Employee{Name: "Chetan Jain", PublicSource: url}

	if key := strongestEntityKey(gatherRowSources(employee, views, matcher)); key != "" {
		t.Fatalf("a distant mention must not tie the person to the company, got key %q", key)
	}

	// The same page with the two beside each other does tie them.
	closeUp := "Chetan Jain is the Chief Financial Officer of Sanjay Chemicals (India) Pvt Ltd."
	closeViews := buildEvidenceViews([]models.Evidence{{URL: url, Title: "Profile", Content: closeUp}})
	if key := strongestEntityKey(gatherRowSources(employee, closeViews, matcher)); key == "" {
		t.Fatal("a nearby mention must tie the person to the company")
	}
}

// TestLookupWorthinessRanksTheEntityCases pins the aggressive lookup policy: anything that
// is not already current *and* tied by a strong key is worth one snippet-only search, with
// the person a lookup could rescue first.
func TestLookupWorthinessRanksTheEntityCases(t *testing.T) {
	cases := []struct {
		name       string
		person     models.Employee
		gateActive bool
		wantRank   int
		wantLookup bool
	}{
		{name: "no key at all comes first", person: models.Employee{Verification: models.VerificationUndated}, gateActive: true, wantRank: 0, wantLookup: true},
		{name: "a title two people claim", person: models.Employee{EntityKey: models.EntityKeyDomain, Verification: models.VerificationConflicting}, gateActive: true, wantRank: 1, wantLookup: true},
		{name: "a name-only match", person: models.Employee{EntityKey: models.EntityKeyName, Verification: models.VerificationDated}, gateActive: true, wantRank: 2, wantLookup: true},
		{name: "a stale row", person: models.Employee{EntityKey: models.EntityKeyDomain, Verification: models.VerificationStale}, gateActive: true, wantRank: 3, wantLookup: true},
		{name: "an undated row", person: models.Employee{EntityKey: models.EntityKeyDomain, Verification: models.VerificationUndated}, gateActive: true, wantRank: 4, wantLookup: true},
		{name: "a merely dated row", person: models.Employee{EntityKey: models.EntityKeyDomain, Verification: models.VerificationDated}, gateActive: true, wantRank: 5, wantLookup: true},
		{name: "already current and tied by domain", person: models.Employee{EntityKey: models.EntityKeyDomain, Verification: models.VerificationRecent}, gateActive: true, wantLookup: false},
		{name: "current by name alone still gets one", person: models.Employee{EntityKey: models.EntityKeyName, Verification: models.VerificationRecent}, gateActive: true, wantRank: 2, wantLookup: true},
		{name: "gate off: an empty key is not off-entity", person: models.Employee{Verification: models.VerificationRecent}, gateActive: false, wantLookup: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rank, worth := lookupWorthiness(testCase.person, testCase.gateActive)
			if worth != testCase.wantLookup {
				t.Fatalf("lookup worth = %t, want %t", worth, testCase.wantLookup)
			}
			if worth && rank != testCase.wantRank {
				t.Fatalf("rank = %d, want %d", rank, testCase.wantRank)
			}
		})
	}
}

// TestClassifySourceDropsJobBoardsAndContentFarms covers the deny-list: these hosts are
// full of people and titles, which is why they look relevant, and they rank with the
// aggregators so they are never fetched at all.
func TestClassifySourceDropsJobBoardsAndContentFarms(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "Sanjay Chemicals", Domain: "sanjaychemindia.com"}
	for _, target := range []string{
		"https://www.naukri.com/cash-flow-management-jobs-in-india-24",
		"https://www.shine.com/job-search/industrial-markets-jobs",
		"https://www.scribd.com/document/544917725/rwedfds",
		"https://iipmr.com/education/placements.php",
		"https://www.instagram.com/spgleaders/reel/Db8GkB0tlbR/",
	} {
		profile := classifySource(brief, target)
		if profile.quality >= 0.25 {
			t.Errorf("%s should be below the relevance floor, got quality %.2f (%s)", target, profile.quality, profile.sourceType)
		}
	}

	// The company's own site is untouched, and still classified as leadership material.
	profile := classifySource(brief, "https://www.sanjaychemindia.com/team.php")
	if profile.sourceType != "official_leadership" || profile.quality < 0.9 {
		t.Fatalf("the company's own team page must stay the best source, got %+v", profile)
	}
}
