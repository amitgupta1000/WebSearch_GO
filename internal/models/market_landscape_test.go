package models

import (
	"encoding/json"
	"testing"
)

// TestMarketLeadCandidateAcceptsNameAliases pins the live extraction fix: models may return
// company_name or company instead of name, and a whole batch must not decode to empty names.
func TestMarketLeadCandidateAcceptsNameAliases(t *testing.T) {
	var candidates []MarketLandscapeLeadCandidate
	if err := json.Unmarshal([]byte(`[
		{"name":"Acme Lubricants","country":"India"},
		{"company_name":"Bharat Lubes","country":"India"},
		{"company":"Chennai Blenders"}
	]`), &candidates); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(candidates) != 3 || candidates[0].Name != "Acme Lubricants" ||
		candidates[1].Name != "Bharat Lubes" || candidates[2].Name != "Chennai Blenders" {
		t.Errorf("candidates = %+v, want all three name aliases normalized", candidates)
	}
}
