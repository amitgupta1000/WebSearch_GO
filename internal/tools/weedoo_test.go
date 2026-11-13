package tools

import (
	"net/http"
	"testing"
)

// TestWeeDooProxyRoutingScopesToWeeDooHosts pins the per-host proxy contract:
// weedoo.in (and its subdomains) route through WEEDOO_PROXY_URL with the
// credentials surviving the parse, while every other host keeps the standard
// environment behavior: the residential proxy's bandwidth is spent only on
// the one host that needs it.
func TestWeeDooProxyRoutingScopesToWeeDooHosts(t *testing.T) {
	original := weedooProxyURL
	defer func() { weedooProxyURL = original }()
	weedooProxyURL = "http://user:secret@resi-ipv4-6.gw.tprx.io:8080"

	weedooReq, err := http.NewRequest(http.MethodGet, "https://weedoo.in/company/KLJ-PLASTICIZERS-LTD/L24100GJ1985PLC036203", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := weedooProxyForRequest(weedooReq)
	if err != nil {
		t.Fatalf("weedoo.in proxy resolution: %v", err)
	}
	if got == nil || got.Host != "resi-ipv4-6.gw.tprx.io:8080" {
		t.Fatalf("weedoo.in must route through the configured proxy, got %+v", got)
	}
	if got.User == nil || got.User.Username() != "user" {
		t.Fatalf("proxy credentials must survive the parse, got %v", got)
	}
	if password, _ := got.User.Password(); password != "secret" {
		t.Fatalf("proxy password must survive the parse")
	}

	subReq, err := http.NewRequest(http.MethodGet, "https://api.weedoo.in/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	subGot, err := weedooProxyForRequest(subReq)
	if err != nil {
		t.Fatal(err)
	}
	if subGot == nil || subGot.Host != "resi-ipv4-6.gw.tprx.io:8080" {
		t.Fatalf("weedoo.in subdomains must inherit the routing, got %+v", subGot)
	}

	otherReq, err := http.NewRequest(http.MethodGet, "https://www.zaubacorp.com/company/L24100GJ1985PLC036203", nil)
	if err != nil {
		t.Fatal(err)
	}
	otherGot, err := weedooProxyForRequest(otherReq)
	if err != nil {
		t.Fatal(err)
	}
	if otherGot != nil && otherGot.Host == "resi-ipv4-6.gw.tprx.io:8080" {
		t.Fatal("non-weedoo hosts must never use the weedoo proxy")
	}
}

// TestWeeDooProxyDisabledKeepsDefaultBehavior pins the default: with
// WEEDOO_PROXY_URL unset the hook degrades to the environment proxy behavior,
// so deployments without the knob are byte-for-byte unchanged, and an operator
// setting HTTPS_PROXY globally still gets it honored.
func TestWeeDooProxyDisabledKeepsDefaultBehavior(t *testing.T) {
	original := weedooProxyURL
	defer func() { weedooProxyURL = original }()
	weedooProxyURL = ""

	req, err := http.NewRequest(http.MethodGet, "https://weedoo.in/", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := weedooProxyForRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil && got.Host == "resi-ipv4-6.gw.tprx.io:8080" {
		t.Fatal("without WEEDOO_PROXY_URL the weedoo proxy must stay unused")
	}
}

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
