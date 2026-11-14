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

// TestDemandChainInferenceAcceptsObjectAndArray pins backward compatibility: the new first-pass
// object carries the category, while older bare-array responses still decode into stages.
func TestDemandChainInferenceAcceptsObjectAndArray(t *testing.T) {
	var object DemandChainInference
	if err := json.Unmarshal([]byte(`{"category":"solvents","category_rationale":"Acetates consume acetic acid","stages":[{"name":"solvent production"}]}`), &object); err != nil {
		t.Fatalf("object Unmarshal: %v", err)
	}
	if object.Category != "solvents" || len(object.Stages) != 1 || object.Stages[0].Name != "solvent production" {
		t.Errorf("object = %+v", object)
	}
	var array DemandChainInference
	if err := json.Unmarshal([]byte(`[{"name":"solvent production"}]`), &array); err != nil {
		t.Fatalf("array Unmarshal: %v", err)
	}
	if len(array.Stages) != 1 || array.Category != "" {
		t.Errorf("array = %+v", array)
	}
}
