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
