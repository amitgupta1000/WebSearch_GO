package models

import (
	"encoding/json"
	"testing"
)

func TestOrgChartAcceptsHeadquartersLocationObject(t *testing.T) {
	var chart OrgChart
	err := json.Unmarshal([]byte(`{"company_name":"KLJ Resources Limited","headquarters":{"city":"New Delhi","country":"India","address":"KLJ House"}}`), &chart)
	if err != nil {
		t.Fatalf("unmarshal OrgChart: %v", err)
	}
	if chart.Headquarters != "New Delhi, India, KLJ House" {
		t.Errorf("unexpected headquarters: %q", chart.Headquarters)
	}
}

func TestOrgChartAcceptsHeadquartersString(t *testing.T) {
	var chart OrgChart
	err := json.Unmarshal([]byte(`{"company_name":"Acme","headquarters":"Berlin, Germany"}`), &chart)
	if err != nil {
		t.Fatalf("unmarshal OrgChart: %v", err)
	}
	if chart.Headquarters != "Berlin, Germany" {
		t.Errorf("unexpected headquarters: %q", chart.Headquarters)
	}
}

func TestOrgChartAcceptsCompanyBrief(t *testing.T) {
	var chart OrgChart
	err := json.Unmarshal([]byte(`{"company_name":"Acme","brief":{"business_description":"Makes pumps.","key_products":["industrial pumps"],"key_locations":[{"city":"Pune","country":"India"}]}}`), &chart)
	if err != nil {
		t.Fatalf("unmarshal company brief: %v", err)
	}
	if chart.Brief == nil || chart.Brief.BusinessDescription != "Makes pumps." || len(chart.Brief.KeyProducts) != 1 || chart.Brief.KeyLocations[0].City != "Pune" {
		t.Fatalf("unexpected company brief: %+v", chart.Brief)
	}
}

func TestOrgChartAcceptsNumericHeadcountAndNestedLeadership(t *testing.T) {
	var chart OrgChart
	err := json.Unmarshal([]byte(`{"company_name":"Chevron","locations":[{"city":"Houston","country":"USA","headcount_est":7000}],"org_chart":{"name":"Chevron Corporation","leadership":[{"name":"Mike Wirth","title":"Chairman and CEO","direct_reports":[{"name":"Mark Nelson","title":"Vice Chairman"}]}]}}`), &chart)
	if err != nil {
		t.Fatalf("unmarshal alternate OrgChart: %v", err)
	}
	if chart.Locations[0].HeadcountEst != "7000" {
		t.Errorf("expected numeric headcount to normalize to string, got %q", chart.Locations[0].HeadcountEst)
	}
	if len(chart.Leadership) != 2 || chart.Leadership[1].ReportsTo != "Mike Wirth" {
		t.Errorf("expected flattened leadership hierarchy, got %+v", chart.Leadership)
	}
}

func TestQualityReviewAcceptsStringFollowUpQueries(t *testing.T) {
	var review QualityReview
	payload := `{"verdict":"revise","score":0.4,"gaps":["no procurement leaders"],"follow_up_queries":["KLJ Resources Limited head of procurement","KLJ Resources Limited marketing head"]}`
	if err := json.Unmarshal([]byte(payload), &review); err != nil {
		t.Fatalf("unmarshal QualityReview: %v", err)
	}
	if review.Verdict != "revise" || len(review.FollowUpQueries) != 2 {
		t.Fatalf("expected a revise review with two follow-up queries, got %+v", review)
	}
	if review.FollowUpQueries[0].Query != "KLJ Resources Limited head of procurement" {
		t.Errorf("unexpected follow-up query: %+v", review.FollowUpQueries[0])
	}
	if len(review.Gaps) != 1 {
		t.Errorf("expected gaps to survive decoding, got %+v", review.Gaps)
	}
}

func TestQualityReviewAcceptsStructuredFollowUpQueries(t *testing.T) {
	var review QualityReview
	payload := `{"verdict":"revise","score":0.4,"follow_up_queries":[{"query":"KLJ procurement head","purpose":"close gap","target_area":"procurement","priority":2}]}`
	if err := json.Unmarshal([]byte(payload), &review); err != nil {
		t.Fatalf("unmarshal QualityReview: %v", err)
	}
	if len(review.FollowUpQueries) != 1 || review.FollowUpQueries[0].TargetArea != "procurement" || review.FollowUpQueries[0].Priority != 2 {
		t.Fatalf("expected the structured follow-up query to be preserved, got %+v", review.FollowUpQueries)
	}
}

func TestOrgChartCountsUnverifiedSources(t *testing.T) {
	chart := OrgChart{ScrapeReport: []ScrapeReportEntry{
		{URL: "https://a.example", Status: "success"},
		{URL: "https://b.example", Status: "skipped"},
		{URL: "https://c.example", Status: "failed"},
	}}
	if got := chart.UnverifiedSourceCount(); got != 2 {
		t.Errorf("UnverifiedSourceCount() = %d, want 2", got)
	}
	if got := (&OrgChart{}).UnverifiedSourceCount(); got != 0 {
		t.Errorf("expected zero for a chart without a scrape report, got %d", got)
	}
}

func TestOrgChartAcceptsStringKeyLocations(t *testing.T) {
	var chart OrgChart
	payload := `{"company_name":"Goyum Screw Press","brief":{"business_description":"Makes oil expellers.","key_products":["Oil Expellers"],"key_locations":["Ludhiana, Punjab, India"]},"headquarters":"Ludhiana, Punjab, India"}`
	if err := json.Unmarshal([]byte(payload), &chart); err != nil {
		t.Fatalf("unmarshal OrgChart with string key_locations: %v", err)
	}
	if chart.Brief == nil || len(chart.Brief.KeyLocations) != 1 {
		t.Fatalf("expected one key location, got %+v", chart.Brief)
	}
	keyLocation := chart.Brief.KeyLocations[0]
	if keyLocation.City != "Ludhiana" || keyLocation.Country != "India" || keyLocation.Address != "Punjab" {
		t.Errorf("unexpected parsed key location: %+v", keyLocation)
	}
	if chart.Headquarters != "Ludhiana, Punjab, India" {
		t.Errorf("unexpected headquarters: %q", chart.Headquarters)
	}
}

func TestLocationAcceptsStringAndNullForms(t *testing.T) {
	var twoParts Location
	if err := json.Unmarshal([]byte(`"Mumbai, India"`), &twoParts); err != nil {
		t.Fatalf("unmarshal two-part location string: %v", err)
	}
	if twoParts.City != "Mumbai" || twoParts.Country != "India" || twoParts.Address != "" {
		t.Errorf("unexpected two-part location: %+v", twoParts)
	}

	var nullLocation Location
	if err := json.Unmarshal([]byte(`null`), &nullLocation); err != nil {
		t.Fatalf("unmarshal null location: %v", err)
	}
	if nullLocation.City != "" || nullLocation.Country != "" {
		t.Errorf("expected zero location for null, got %+v", nullLocation)
	}

	var emptyLocation Location
	if err := json.Unmarshal([]byte(`""`), &emptyLocation); err != nil {
		t.Fatalf("unmarshal empty location string: %v", err)
	}
	if emptyLocation.City != "" || emptyLocation.Country != "" {
		t.Errorf("expected zero location for empty string, got %+v", emptyLocation)
	}
}

// TestOrgChartDecodesGoyumSynthesisPayload pins the exact payload shape that
// broke synthesis: the model emitted brief.key_locations as bare strings while
// headquarters stayed a string and locations stayed objects.
func TestOrgChartDecodesGoyumSynthesisPayload(t *testing.T) {
	var chart OrgChart
	payload := `{
  "company_name": "Goyum Screw Press",
  "domain": "goyumscrewpress.in",
  "overview": "GOYUM is a leading Project Engineering Company established in 1971 and now a prominent name in the Edible & Non-Edible Oil Processing Plants & Allied Industry on Turnkey Basis. They manufacture and export a wide range of machinery and equipment for oil extraction, refining, and processing, as well as animal feed plants and industrial shredders. The company emphasizes quality, efficiency, and cost-effectiveness in its products and services, with a global presence in over 74 countries.",
  "brief": {
    "business_description": "Manufacturer and exporter of edible and non-edible oil processing plants and allied industry equipment, including oil extraction machinery, solvent extraction plants, edible oil refinery plants, animal feed plants, and industrial shredders.",
    "key_products": [ "Oil Extraction Machines", "Solvent Extraction Plants", "Edible Oil Refinery Plants", "Animal Feed Plants", "Industrial Shredders", "Oil Expellers", "Oil Extruder Machines", "Oil Mill Plants", "Screw Oil Presses", "Oil Crushing Machines", "Oil Pressing Machines" ],
    "key_locations": [ "Ludhiana, Punjab, India" ]
  },
  "headquarters": "Ludhiana, Punjab, India",
  "leadership": [
    { "name": "Goyum Jain", "title": "CEO", "public_source": "https://www.oilexpeller.com/chief-executive-officer/" },
    { "name": "Vinod Jain", "title": "Managing Director & Chairman", "public_source": "https://india5000.com/winners/2020/india-500-sme-awards-2020/goyum-screw-press-1578634632570" }
  ],
  "departments": [
    { "name": "sales", "description": "The sales team provides equipment and cost details, and assists with process design, layout, equipment selection, installation, and commissioning.", "members": [] }
  ],
  "locations": [
    { "city": "Ludhiana", "country": "India", "address": "Plot No. 2581, Industrial Area-A, Ludhiana, Punjab, 141003, India" }
  ]
}`
	if err := json.Unmarshal([]byte(payload), &chart); err != nil {
		t.Fatalf("unmarshal synthesis payload: %v", err)
	}
	if chart.CompanyName != "Goyum Screw Press" || chart.Domain != "goyumscrewpress.in" || chart.Headquarters != "Ludhiana, Punjab, India" {
		t.Fatalf("unexpected chart header: %+v", chart)
	}
	if chart.Brief == nil || len(chart.Brief.KeyLocations) != 1 || chart.Brief.KeyLocations[0].City != "Ludhiana" || chart.Brief.KeyLocations[0].Country != "India" {
		t.Fatalf("unexpected brief: %+v", chart.Brief)
	}
	if len(chart.Leadership) != 2 || chart.Leadership[1].Name != "Vinod Jain" || chart.Leadership[1].ReportsTo != "" {
		t.Errorf("unexpected leadership: %+v", chart.Leadership)
	}
	if len(chart.Departments) != 1 || chart.Departments[0].Name != "sales" || len(chart.Departments[0].Members) != 0 {
		t.Errorf("unexpected departments: %+v", chart.Departments)
	}
	if len(chart.Locations) != 1 || chart.Locations[0].City != "Ludhiana" || chart.Locations[0].Address != "Plot No. 2581, Industrial Area-A, Ludhiana, Punjab, 141003, India" {
		t.Errorf("unexpected locations: %+v", chart.Locations)
	}
}
