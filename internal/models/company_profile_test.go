package models

import (
	"encoding/json"
	"testing"
)

// TestCompanyProfileTolerantDecode pins the live Sasol failure fix: the model
// returned employees as a number (46000) where the field is a string, and the
// strict decode discarded the whole synthesis. The payload is the shape from
// that run; decoding must keep the overview and coerce every metric.
func TestCompanyProfileTolerantDecode(t *testing.T) {
	payload := `{
		"company_name": "Sasol",
		"domain": "sasol.com",
		"overview": "Sasol is an international integrated chemicals and energy company.",
		"brief": {
			"business_description": "Integrated chemicals and energy.",
			"employees": 46000,
			"turnover": 14.9,
			"financial_year": 2024,
			"key_products": ["solvents", "polymers", 123],
			"key_locations": [{"city": "Johannesburg", "country": "South Africa"}, "Sasolburg, South Africa", 42],
			"metrics_source": "https://www.sasol.com/investors"
		}
	}`
	var chart OrgChart
	if err := json.Unmarshal([]byte(payload), &chart); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if chart.CompanyName != "Sasol" || chart.Overview == "" {
		t.Errorf("chart header lost: %+v", chart.CompanyName)
	}
	brief := chart.Brief
	if brief == nil {
		t.Fatal("brief is nil, want the decoded profile")
	}
	if brief.Employees != "46000" || brief.Turnover != "14.9" || brief.FinancialYear != "2024" {
		t.Errorf("metrics = (%q, %q, %q), want numeric values coerced to strings", brief.Employees, brief.Turnover, brief.FinancialYear)
	}
	if len(brief.KeyProducts) != 3 || brief.KeyProducts[2] != "123" {
		t.Errorf("key products = %v, want the mixed array coerced", brief.KeyProducts)
	}
	if len(brief.KeyLocations) != 2 {
		t.Errorf("key locations = %+v, want the object and the bare string kept, the number skipped", brief.KeyLocations)
	}
	if brief.MetricsSource != "https://www.sasol.com/investors" {
		t.Errorf("metrics source = %q", brief.MetricsSource)
	}
}

// TestCompanyProfileTolerantNullsAndAbsence pins the quiet paths: nulls and
// absent fields decode to zero values without error, and a lone string for
// key_products becomes a one-entry list.
func TestCompanyProfileTolerantNullsAndAbsence(t *testing.T) {
	payload := `{"brief": {"employees": null, "turnover": "", "key_products": "solvents"}}`
	var chart OrgChart
	if err := json.Unmarshal([]byte(payload), &chart); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if chart.Brief == nil {
		t.Fatal("brief is nil")
	}
	if chart.Brief.Employees != "" || chart.Brief.Turnover != "" {
		t.Errorf("null/empty fields = (%q, %q), want empty strings", chart.Brief.Employees, chart.Brief.Turnover)
	}
	if len(chart.Brief.KeyProducts) != 1 || chart.Brief.KeyProducts[0] != "solvents" {
		t.Errorf("lone-string products = %v, want the one entry", chart.Brief.KeyProducts)
	}
}
