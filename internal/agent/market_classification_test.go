package agent

import (
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

func TestMarketRoleClassificationSeparatesManufacturersAndIntermediaries(t *testing.T) {
	cases := []struct {
		name, evidence, wantRole string
		wantTags                 int
	}{
		{"Acme Manufacturing", "Acme operates a blending plant and manufactures lubricants.", models.MarketRoleManufacturer, 1},
		{"Acme Distributors", "Authorized distributor and channel partner for industrial oils.", models.MarketRoleDistributor, 1},
		{"Acme Wholesale", "Wholesale supplier and stockist of lubricants.", models.MarketRoleWholesaler, 1},
		{"Acme Trading", "Importer, exporter and trading house for base oils.", models.MarketRoleTrader, 1},
		{"Acme Manufacturing and Distribution", "Manufacturer with its own plant; also an authorized distributor.", models.MarketRoleManufacturer, 2},
		{"Acme Holdings", "A diversified industrial company.", models.MarketRoleUnknown, 0},
	}
	for _, testCase := range cases {
		company := &models.MarketLandscapeCompany{
			Name: testCase.name,
			Evidence: []models.MarketLandscapeEvidence{{
				Title: testCase.name, Snippet: testCase.evidence,
			}},
		}
		role, tags := marketRoleFor(company)
		if role != testCase.wantRole || len(tags) != testCase.wantTags {
			t.Errorf("%s => role=%q tags=%v, want %q/%d", testCase.name, role, tags, testCase.wantRole, testCase.wantTags)
		}
	}
}

func TestMarketPresenceSeparatesLocalRegionalAndGlobal(t *testing.T) {
	local := &models.MarketLandscapeCompany{
		Name: "Gujarat Blending", Country: "India",
		Evidence: []models.MarketLandscapeEvidence{{Snippet: "Operates a blending plant in Gujarat, India."}},
	}
	if presence, country, _, _ := marketPresenceFor(local, models.MarketLandscapeQuery{Countries: []string{"India"}}); presence != models.MarketPresenceLocal || country != "India" {
		t.Errorf("local presence = %q/%q, want local/India", presence, country)
	}
	if presence, country, _, _ := marketPresenceFor(local, models.MarketLandscapeQuery{Regions: []string{"India"}}); presence != models.MarketPresenceLocal || country != "India" {
		t.Errorf("India scope radio presence = %q/%q, want local/India", presence, country)
	}
	global := &models.MarketLandscapeCompany{
		Name: "Global Additives", Country: "Germany",
		Evidence: []models.MarketLandscapeEvidence{{Snippet: "A global supplier with operations worldwide."}},
	}
	if presence, _, reason, hq := marketPresenceFor(global, models.MarketLandscapeQuery{Countries: []string{"India"}}); presence != models.MarketPresenceGlobal || hq != "Germany" || reason == "" {
		t.Errorf("global presence = %q/%q/%q, want global/Germany/reason", presence, hq, reason)
	}
	regional := &models.MarketLandscapeCompany{
		Name: "Singapore Oils", Country: "Singapore",
		Evidence: []models.MarketLandscapeEvidence{{Snippet: "Regional office in Singapore."}},
	}
	if presence, _, _, _ := marketPresenceFor(regional, models.MarketLandscapeQuery{Regions: []string{"Asia"}}); presence != models.MarketPresenceRegional {
		t.Errorf("regional presence = %q, want regional", presence)
	}
	unknown := &models.MarketLandscapeCompany{Name: "Locationless"}
	if presence, _, reason, _ := marketPresenceFor(unknown, models.MarketLandscapeQuery{Countries: []string{"India"}}); presence != models.MarketPresenceUnknown || reason == "" {
		t.Errorf("unknown presence = %q/%q, want unknown with a reason", presence, reason)
	}
}

func TestMarketCoverageAndGroupsExcludeGlobalOnlyCompanies(t *testing.T) {
	query := models.MarketLandscapeQuery{Countries: []string{"India"}}
	companies := map[string]*models.MarketLandscapeCompany{}
	for index := 0; index < marketCoverageMinimum; index++ {
		name := "Local Manufacturer " + string(rune('A'+index))
		companies[name] = &models.MarketLandscapeCompany{
			Name: name, Country: "India", Role: models.MarketRoleManufacturer,
			Evidence: []models.MarketLandscapeEvidence{{Snippet: "Manufacturing plant in Pune, India."}},
		}
	}
	companies["global"] = &models.MarketLandscapeCompany{
		Name: "Global Additives", Country: "Germany", Role: models.MarketRoleManufacturer,
		Evidence: []models.MarketLandscapeEvidence{{Snippet: "Global supplier of lubricant additives."}},
	}
	classifyMarketCompanies(companies, query)
	coverage := marketCoverageFor(companies, query)
	if coverage.Companies != marketCoverageMinimum || coverage.Global != 1 || !coverage.Pass {
		t.Errorf("coverage = %+v, want %d in-scope + 1 global and PASS", coverage, marketCoverageMinimum)
	}
	results := make([]models.MarketLandscapeCompany, 0, len(companies))
	for _, company := range companies {
		results = append(results, *company)
	}
	groups := marketGroupsFor(results, query)
	if len(groups) != 2 {
		t.Fatalf("groups = %+v, want manufacturer and global groups", groups)
	}
	if groups[0].Label != "Manufacturers / producers" || groups[1].Label != "Global / cross-border" || len(groups[1].Companies) != 1 {
		t.Errorf("groups = %+v, want local manufacturers then one global company", groups)
	}
}

func TestMarketCoverageCountsEverythingWhenNoGeographyIsRequested(t *testing.T) {
	companies := map[string]*models.MarketLandscapeCompany{
		"a": {Name: "Acme", Presence: models.MarketPresenceGlobal},
		"b": {Name: "Beta", Presence: models.MarketPresenceUnknown},
	}
	coverage := marketCoverageFor(companies, models.MarketLandscapeQuery{})
	if coverage.Companies != 2 || coverage.Global != 0 {
		t.Errorf("global-scope coverage = %+v, want both companies counted", coverage)
	}
}
