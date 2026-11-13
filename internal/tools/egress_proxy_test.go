package tools

import (
	"net/http"
	"testing"
)

// restoreProxyKnobs returns a defer func restoring both proxy knobs, so tests
// can set them without leaking into later tests.
func restoreProxyKnobs(weedoo, linkedin string) func() {
	return func() {
		weedooProxyURL = weedoo
		linkedinProxyURL = linkedin
	}
}

// TestScraperProxyRoutesEachHostToItsOwnProxy pins the per-host proxy contract:
// weedoo.in and linkedin.com (with subdomains) each route through their own
// configured proxy with credentials surviving the parse, while unrelated hosts
// never touch either proxy — the residential bandwidth is spent only on the
// hosts that need it.
func TestScraperProxyRoutesEachHostToItsOwnProxy(t *testing.T) {
	defer restoreProxyKnobs(weedooProxyURL, linkedinProxyURL)()
	weedooProxyURL = "http://weedoo-user:weedoo-secret@resi-ipv4-6.gw.tprx.io:8080"
	linkedinProxyURL = "http://li-user:li-secret@resi-ipv4-2.gw.tprx.io:8080"

	weedooReq, err := http.NewRequest(http.MethodGet, "https://weedoo.in/company/KLJ-PLASTICIZERS-LTD/L24100GJ1985PLC036203", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scraperProxyForRequest(weedooReq)
	if err != nil {
		t.Fatalf("weedoo.in proxy resolution: %v", err)
	}
	if got == nil || got.Host != "resi-ipv4-6.gw.tprx.io:8080" {
		t.Fatalf("weedoo.in must route through its configured proxy, got %+v", got)
	}
	if got.User == nil || got.User.Username() != "weedoo-user" {
		t.Fatalf("weedoo proxy credentials must survive the parse, got %v", got)
	}
	if password, _ := got.User.Password(); password != "weedoo-secret" {
		t.Fatal("weedoo proxy password must survive the parse")
	}

	subReq, err := http.NewRequest(http.MethodGet, "https://api.weedoo.in/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	if subGot, err := scraperProxyForRequest(subReq); err != nil || subGot == nil || subGot.Host != "resi-ipv4-6.gw.tprx.io:8080" {
		t.Fatalf("weedoo.in subdomains must inherit the routing, got %+v (err %v)", subGot, err)
	}

	profileReq, err := http.NewRequest(http.MethodGet, "https://www.linkedin.com/in/kanhaiya-lal-jain-1234/", nil)
	if err != nil {
		t.Fatal(err)
	}
	profileGot, err := scraperProxyForRequest(profileReq)
	if err != nil {
		t.Fatalf("linkedin proxy resolution: %v", err)
	}
	if profileGot == nil || profileGot.Host != "resi-ipv4-2.gw.tprx.io:8080" {
		t.Fatalf("linkedin.com must route through its configured proxy, got %+v", profileGot)
	}
	if profileGot.User == nil || profileGot.User.Username() != "li-user" {
		t.Fatalf("linkedin proxy credentials must survive the parse, got %v", profileGot)
	}

	bareReq, err := http.NewRequest(http.MethodGet, "https://linkedin.com/company/klj-resources/", nil)
	if err != nil {
		t.Fatal(err)
	}
	bareGot, err := scraperProxyForRequest(bareReq)
	if err != nil {
		t.Fatal(err)
	}
	if bareGot == nil || bareGot.Host != "resi-ipv4-2.gw.tprx.io:8080" {
		t.Fatalf("the bare linkedin.com host must inherit the routing, got %+v", bareGot)
	}

	otherReq, err := http.NewRequest(http.MethodGet, "https://www.zaubacorp.com/company/L24100GJ1985PLC036203", nil)
	if err != nil {
		t.Fatal(err)
	}
	otherGot, err := scraperProxyForRequest(otherReq)
	if err != nil {
		t.Fatal(err)
	}
	if otherGot != nil && (otherGot.Host == "resi-ipv4-6.gw.tprx.io:8080" || otherGot.Host == "resi-ipv4-2.gw.tprx.io:8080") {
		t.Fatal("unrelated hosts must never use a per-host proxy")
	}
}

// TestScraperProxyDisabledKeepsDefaultBehavior pins the default: with the knobs
// unset the hook degrades to the environment proxy behavior, so deployments
// without the knobs are byte-for-byte unchanged, and an operator setting
// HTTPS_PROXY globally still gets it honored.
func TestScraperProxyDisabledKeepsDefaultBehavior(t *testing.T) {
	defer restoreProxyKnobs(weedooProxyURL, linkedinProxyURL)()
	weedooProxyURL = ""
	linkedinProxyURL = ""

	for _, target := range []string{
		"https://weedoo.in/",
		"https://www.linkedin.com/in/someone/",
	} {
		req, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := scraperProxyForRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		if got != nil && (got.Host == "resi-ipv4-6.gw.tprx.io:8080" || got.Host == "resi-ipv4-2.gw.tprx.io:8080") {
			t.Fatalf("%s must not use a per-host proxy when its knob is unset", target)
		}
	}

	// One knob unset must not disable the other.
	defer restoreProxyKnobs(weedooProxyURL, linkedinProxyURL)()
	weedooProxyURL = ""
	linkedinProxyURL = "http://li-user:li-secret@resi-ipv4-2.gw.tprx.io:8080"
	linkReq, err := http.NewRequest(http.MethodGet, "https://www.linkedin.com/in/someone/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := scraperProxyForRequest(linkReq); err != nil || got == nil || got.Host != "resi-ipv4-2.gw.tprx.io:8080" {
		t.Fatalf("an unset weedoo knob must not disable the linkedin proxy, got %+v (err %v)", got, err)
	}
	weedReq, err := http.NewRequest(http.MethodGet, "https://weedoo.in/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := scraperProxyForRequest(weedReq); err != nil || (got != nil && got.Host == "resi-ipv4-2.gw.tprx.io:8080") {
		t.Fatalf("an unset weedoo knob must leave weedoo.in unproxied, got %+v (err %v)", got, err)
	}
}

// TestEgressProxySummaryListsOnlyActiveHosts pins the startup-log helper: it
// names exactly the hosts whose proxy is configured, and names none when the
// knobs are unset.
func TestEgressProxySummaryListsOnlyActiveHosts(t *testing.T) {
	defer restoreProxyKnobs(weedooProxyURL, linkedinProxyURL)()
	weedooProxyURL = ""
	linkedinProxyURL = ""
	if summary := egressProxySummary(); summary != "" {
		t.Fatalf("unset knobs must produce an empty summary, got %q", summary)
	}
	weedooProxyURL = "http://user:pass@gw:8080"
	linkedinProxyURL = "http://user:pass@gw2:8080"
	if summary := egressProxySummary(); summary != "weedoo.in, linkedin.com" {
		t.Fatalf("expected both hosts listed, got %q", summary)
	}
}
