package tools

import "testing"

// TestWeedooCINForCompanyPairsTheNameWithTheCIN pins the suggestion parser: the
// row's visible text pairs the company name with its CIN, and a row for another
// company must never be attributed.
func TestWeedooCINForCompanyPairsTheNameWithTheCIN(t *testing.T) {
	row := "Klj Resources Ltd\nL67120WB1986PLC041487"
	cin, err := weedooCINForCompany(row, "KLJ Resources Limited")
	if err != nil || cin != "L67120WB1986PLC041487" {
		t.Fatalf("expected the suggestion's CIN, got %q (err %v)", cin, err)
	}
	other := "Hindustan Unilever Limited\nL15140MH1933PLC002030"
	if _, err := weedooCINForCompany(other, "KLJ Resources Limited"); err == nil {
		t.Fatal("expected no attribution for a company the typeahead did not suggest")
	}
	// Empty or suffix-only names have nothing to match.
	if _, err := weedooCINForCompany(row, "Private Limited"); err == nil {
		t.Fatal("expected an error for a name without significant tokens")
	}
}

// TestWeedooNameTokensDropsSuffixesAndStops pins the token reduction the
// suggestion matcher relies on: significant words kept, legal suffixes dropped.
func TestWeedooNameTokensDropsSuffixesAndStops(t *testing.T) {
	tokens := weedooNameTokens("KLJ Resources Ltd.")
	if len(tokens) != 2 || tokens[0] != "klj" || tokens[1] != "resources" {
		t.Fatalf("unexpected tokens: %+v", tokens)
	}
	if got := weedooNameTokens("Goyum Screw-Press"); len(got) != 2 || got[0] != "goyum" || got[1] != "screw" || got[2] != "press" {
		if len(got) != 3 {
			t.Fatalf("unexpected tokens: %+v", got)
		}
	}
	if got := weedooNameTokens("  Private Limited  "); len(got) != 0 {
		t.Fatalf("expected no tokens, got %+v", got)
	}
}