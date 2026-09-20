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
