package models

import (
	"encoding/json"
	"testing"
)

// TestValueChainReviewTolerantDecode pins the live failure fix: the model
// returned `"missing_stages": ""` (a bare empty string) where the field is
// []string, and the strict decode discarded the whole adversarial chain
// review. The payload below is the exact response from that run; decoding it
// must keep the verdict, the two rejections and the distance correction.
func TestValueChainReviewTolerantDecode(t *testing.T) {
	payload := `{
		"verdict": "revise",
		"score": 0.7,
		"rejected_stages": [
			{ "name": "Esterification", "reason": "Rationale is too general." },
			{ "name": "Ethoxylation and alkoxylation", "reason": "Rationale is too general." }
		],
		"distance_corrections": [
			{ "name": "Adhesives and sealants", "distance": 2 }
		],
		"missing_stages": "",
		"reason": "Two stages were rejected."
	}`
	var review ValueChainReview
	if err := json.Unmarshal([]byte(payload), &review); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if review.Verdict != "revise" || review.Score != 0.7 {
		t.Errorf("verdict = %+v, want revise/0.7", review)
	}
	if len(review.RejectedStages) != 2 || review.RejectedStages[0].Name != "Esterification" {
		t.Errorf("rejected = %+v, want the two stages", review.RejectedStages)
	}
	if len(review.DistanceCorrections) != 1 || review.DistanceCorrections[0].Distance != 2 {
		t.Errorf("corrections = %+v, want the sealsants correction", review.DistanceCorrections)
	}
	if len(review.MissingStages) != 0 {
		t.Errorf("missing stages = %v, want none (empty string means none)", review.MissingStages)
	}
}

// TestValueChainReviewTolerantShapeVariants pins the other shapes the model
// returns for the same fields: missing_stages as a bare string, a mixed
// array of strings and {"name"} objects, and rejected_stages as bare names.
func TestValueChainReviewTolerantShapeVariants(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		wantMiss []string
	}{
		{"bare string", `{"verdict":"pass","missing_stages":"Biodiesel"}`, []string{"Biodiesel"}},
		{"mixed array", `{"verdict":"pass","missing_stages":["Biodiesel",{"name":"Lubricants"}]}`, []string{"Biodiesel", "Lubricants"}},
		{"null", `{"verdict":"pass","missing_stages":null}`, nil},
	}
	for _, tc := range cases {
		var review ValueChainReview
		if err := json.Unmarshal([]byte(tc.payload), &review); err != nil {
			t.Fatalf("%s: decode: %v", tc.name, err)
		}
		if len(review.MissingStages) != len(tc.wantMiss) {
			t.Fatalf("%s: missing = %v, want %v", tc.name, review.MissingStages, tc.wantMiss)
		}
		for i, want := range tc.wantMiss {
			if review.MissingStages[i] != want {
				t.Errorf("%s: missing[%d] = %q, want %q", tc.name, i, review.MissingStages[i], want)
			}
		}
	}

	var review ValueChainReview
	payload := `{"verdict":"revise","rejected_stages":["Esterification",{"name":"Ethoxylation","reason":"general"}]}`
	if err := json.Unmarshal([]byte(payload), &review); err != nil {
		t.Fatalf("rejected shapes: %v", err)
	}
	if len(review.RejectedStages) != 2 || review.RejectedStages[0].Name != "Esterification" || review.RejectedStages[1].Reason != "general" {
		t.Errorf("rejected = %+v, want the bare name and the object", review.RejectedStages)
	}
}
