package models

import (
	"encoding/json"
	"testing"
)

// The payloads below replay the drift the models actually emit: a number
// where a string is declared, a lone string where an array is declared, a
// numeric string where an int or float is declared. Each decode must succeed
// with the value coerced, because one drifted field used to discard the whole
// synthesis.

func TestTolerantPersonProfileShapes(t *testing.T) {
	payload := `{
		"full_name": "Jane Doe",
		"confidence_score": "0.82",
		"key_skills": "Procurement",
		"online_presence": {"linkedin": 42, "homepage": "https://example.com"},
		"experiences": [{"role": "MD", "company": "Acme", "period": 2019, "highlights": "led growth"}],
		"education": [{"institution": "IIT", "degree": "B.Tech", "year": 2005}]
	}`
	var profile PersonProfile
	if err := json.Unmarshal([]byte(payload), &profile); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if profile.ConfidenceScore != 0.82 {
		t.Errorf("confidence = %v, want 0.82", profile.ConfidenceScore)
	}
	if len(profile.KeySkills) != 1 || profile.KeySkills[0] != "Procurement" {
		t.Errorf("key skills = %v, want the lone string as a list", profile.KeySkills)
	}
	if profile.OnlinePresence["linkedin"] != "42" {
		t.Errorf("online presence = %v, want the number coerced", profile.OnlinePresence)
	}
	if len(profile.Experiences) != 1 || profile.Experiences[0].Period != "2019" || len(profile.Experiences[0].Highlights) != 1 {
		t.Errorf("experience = %+v, want coerced period and highlights", profile.Experiences)
	}
	if len(profile.Education) != 1 || profile.Education[0].Year != "2005" {
		t.Errorf("education = %+v, want the numeric year coerced", profile.Education)
	}
}

func TestTolerantQueryPlanAndRankedResult(t *testing.T) {
	var plan QueryPlan
	if err := json.Unmarshal([]byte(`{"queries":[{"query":"a","purpose":"b","target_area":"c","priority":"1"},{"query":"d","priority":2.0}]}`), &plan); err != nil {
		t.Fatalf("plan decode: %v", err)
	}
	if len(plan.Queries) != 2 || plan.Queries[0].Priority != 1 || plan.Queries[1].Priority != 2 {
		t.Errorf("plan = %+v, want numeric-string and float priorities coerced", plan.Queries)
	}
	var ranked RankedResult
	if err := json.Unmarshal([]byte(`{"url":"https://x","relevant":"true","score":"0.9"}`), &ranked); err != nil {
		t.Fatalf("ranked decode: %v", err)
	}
	if !ranked.Relevant || ranked.Score != 0.9 {
		t.Errorf("ranked = %+v, want string bool and numeric-string score", ranked)
	}
}

func TestTolerantLegalReportShapes(t *testing.T) {
	payload := `{"company_name":"Acme","findings":[{"case_title":"Acme v X","court":123,"date":2020,"url":"https://x"}]}`
	var report LegalReport
	if err := json.Unmarshal([]byte(payload), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Court != "123" || report.Findings[0].Date != "2020" {
		t.Errorf("findings = %+v, want numeric court/date coerced", report.Findings)
	}
}

func TestTolerantNewsSynthesisShapes(t *testing.T) {
	payload := `{
		"headline": "Acme news",
		"key_developments": "One development",
		"sentiment": {"url":"https://x","label":"positive","score":"0.6","confidence":0.7,"rationale":"r"},
		"citations": [{"claim":"c","urls":"https://x"}],
		"uncertainty": []
	}`
	var synthesis NewsSynthesis
	if err := json.Unmarshal([]byte(payload), &synthesis); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(synthesis.KeyDevelopments) != 1 {
		t.Errorf("developments = %v, want the lone string as a list", synthesis.KeyDevelopments)
	}
	if synthesis.Sentiment.Score != 0.6 {
		t.Errorf("score = %v, want the numeric string coerced", synthesis.Sentiment.Score)
	}
	if len(synthesis.Citations) != 1 || len(synthesis.Citations[0].URLs) != 1 {
		t.Errorf("citations = %+v, want the lone URL as a list", synthesis.Citations)
	}
}

func TestTolerantDeepSearchShapes(t *testing.T) {
	var plan DeepSearchPlan
	if err := json.Unmarshal([]byte(`{"understanding": 42, "sub_questions": "What changed?"}`), &plan); err != nil {
		t.Fatalf("plan decode: %v", err)
	}
	if plan.Understanding != "42" || len(plan.SubQuestions) != 1 {
		t.Errorf("plan = %+v, want coerced understanding and lone string list", plan)
	}
	var review DeepSearchReview
	if err := json.Unmarshal([]byte(`{"complete":"true","missing":"gap"}`), &review); err != nil {
		t.Fatalf("review decode: %v", err)
	}
	if !review.Complete || len(review.Missing) != 1 {
		t.Errorf("review = %+v, want string bool and lone string list", review)
	}
}

func TestTolerantValueChainStageAndReviewShapes(t *testing.T) {
	var stage ValueChainStage
	if err := json.Unmarshal([]byte(`{"name":"Esterification","operators":"ester plant","distance":"2"}`), &stage); err != nil {
		t.Fatalf("stage decode: %v", err)
	}
	if stage.Distance != 2 || len(stage.Operators) != 1 {
		t.Errorf("stage = %+v, want numeric-string distance and lone string operators", stage)
	}
	var review DemandChainReview
	if err := json.Unmarshal([]byte(`{"verdict":"pass","score":"0.7","missing_stages":"","rejected":["Bare Name"]}`), &review); err != nil {
		t.Fatalf("review decode: %v", err)
	}
	if review.Score != 0.7 || len(review.MissingStages) != 0 || len(review.Rejected) != 1 || review.Rejected[0].Name != "Bare Name" {
		t.Errorf("review = %+v, want coerced score, empty missing stages and the bare-name rejection", review)
	}
}
