package models

import (
	"encoding/json"
	"testing"
)

// TestDemandChainReviewUnmarshalFlexibleShapes pins the QC judge's tolerance: models return
// rejected names as objects or bare strings and follow-ups as strings or query objects.
func TestDemandChainReviewUnmarshalFlexibleShapes(t *testing.T) {
	var review DemandChainReview
	if err := json.Unmarshal([]byte(`{
		"verdict":"revise","score":0.55,
		"rejected":[{"name":"ACS Publications","reason":"publisher"},"August Wilhelm von Hofmann"],
		"follow_up_queries":["biodiesel production buyers India",{"query":"distributor used cooking oil"}],
		"missing_stages":["biodiesel production"]
	}`), &review); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(review.Rejected) != 2 || review.Rejected[0].Reason != "publisher" || review.Rejected[1].Reason == "" {
		t.Errorf("rejected = %+v, want object and bare-name forms", review.Rejected)
	}
	if len(review.FollowUpQueries) != 2 || review.FollowUpQueries[1] != "distributor used cooking oil" {
		t.Errorf("follow-ups = %+v, want string and object forms", review.FollowUpQueries)
	}
}
