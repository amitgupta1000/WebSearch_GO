package tools

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCookieFile(t *testing.T, cookies []RawCookie) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cookies.json")
	data, err := json.Marshal(cookies)
	if err != nil {
		t.Fatalf("marshal cookies: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
	return path
}

func TestLoadCookieJarTrimsLeadingDot(t *testing.T) {
	path := writeCookieFile(t, []RawCookie{{
		Name: "sid", Value: "secret", Domain: ".example.com", Path: "/",
	}})

	jar, err := LoadCookieJarFromFile(path)
	if err != nil {
		t.Fatalf("LoadCookieJarFromFile: %v", err)
	}

	// A domain cookie exported as ".example.com" must also cover subdomains.
	u, err := url.Parse("https://www.example.com/page")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	found := false
	for _, c := range jar.Cookies(u) {
		if c.Name == "sid" && c.Value == "secret" {
			found = true
		}
	}
	if !found {
		t.Fatalf("domain cookie not applied to subdomain; got %v", jar.Cookies(u))
	}
}

func TestLoadCookieJarSkipsExpiredCookies(t *testing.T) {
	path := writeCookieFile(t, []RawCookie{
		{Name: "old", Value: "1", Domain: ".example.com", ExpirationDate: 1000000000},
		{Name: "fresh", Value: "2", Domain: ".example.com", ExpirationDate: float64(time.Now().Add(time.Hour).Unix())},
	})

	jar, err := LoadCookieJarFromFile(path)
	if err != nil {
		t.Fatalf("LoadCookieJarFromFile: %v", err)
	}

	u, _ := url.Parse("https://example.com/")
	cookies := jar.Cookies(u)
	if len(cookies) != 1 || cookies[0].Name != "fresh" {
		t.Fatalf("expected only the fresh cookie to survive, got %v", cookies)
	}
}

func TestLoadCookieJarEmptyPathReturnsEmptyJar(t *testing.T) {
	jar, err := LoadCookieJarFromFile("")
	if err != nil {
		t.Fatalf("empty path should not error, got: %v", err)
	}
	if jar == nil {
		t.Fatal("expected non-nil jar")
	}
}

func TestIsCookieExpired(t *testing.T) {
	now := time.Now()
	future := float64(now.Add(time.Hour).Unix())
	past := float64(now.Add(-time.Hour).Unix())

	if isCookieExpired(RawCookie{ExpirationDate: future}, now) {
		t.Error("future expirationDate must not be expired")
	}
	if !isCookieExpired(RawCookie{ExpirationDate: past}, now) {
		t.Error("past expirationDate must be expired")
	}
	// http.ParseTime only accepts the literal "GMT" zone; exercise the GMT
	// form and the UTC abbreviation (handled by the RFC1123 fallback).
	if isCookieExpired(RawCookie{Expires: now.Add(time.Hour).UTC().Format("Mon, 02 Jan 2006 15:04:05") + " GMT"}, now) {
		t.Error("future expires must not be expired")
	}
	if !isCookieExpired(RawCookie{Expires: now.Add(-time.Hour).UTC().Format("Mon, 02 Jan 2006 15:04:05") + " GMT"}, now) {
		t.Error("past expires must be expired")
	}
	if !isCookieExpired(RawCookie{Expires: now.Add(-time.Hour).UTC().Format("Mon, 02 Jan 2006 15:04:05 MST")}, now) {
		t.Error("past expires with UTC zone must be expired (RFC1123 fallback)")
	}
	if isCookieExpired(RawCookie{}, now) {
		t.Error("session cookie without expiry must not be expired")
	}
}

// TestLoadCookieJarWidensHostScopedCookiesToTheParent is the fix for why the
// ladder's cookie-render step had never fired: exporters scope a session to the
// host the user was browsing (www.linkedin.com), while the authwall is served on a
// sibling subdomain (in.linkedin.com), so the jar looked empty exactly where the
// ladder needed it.
func TestLoadCookieJarWidensHostScopedCookiesToTheParent(t *testing.T) {
	path := writeCookieFile(t, []RawCookie{{
		Name: "li_at", Value: "session", Domain: "www.linkedin.com", Path: "/", HostOnly: true, Secure: true,
	}})

	jar, report, err := LoadCookieJarFromFileDetailed(path)
	if err != nil {
		t.Fatalf("LoadCookieJarFromFileDetailed: %v", err)
	}
	if report.Cookies != 2 {
		t.Errorf("expected the cookie registered for both hosts, got %d", report.Cookies)
	}
	if report.WidenedToParent != 1 {
		t.Errorf("expected 1 widened cookie, got %d", report.WidenedToParent)
	}

	for _, target := range []string{
		"https://www.linkedin.com/feed/",
		"https://in.linkedin.com/in/someone-1234",
		"https://linkedin.com/",
	} {
		parsed, err := url.Parse(target)
		if err != nil {
			t.Fatalf("parse %s: %v", target, err)
		}
		found := false
		for _, cookie := range jar.Cookies(parsed) {
			if cookie.Name == "li_at" && cookie.Value == "session" {
				found = true
			}
		}
		if !found {
			t.Errorf("session cookie not delivered to %s; got %v", target, jar.Cookies(parsed))
		}
	}

	// Widening must not leak the session anywhere else.
	other, err := url.Parse("https://example.com/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cookies := jar.Cookies(other); len(cookies) != 0 {
		t.Fatalf("cookie leaked to an unrelated domain: %v", cookies)
	}
}

// TestLoadCookieJarWidensToTheRegistrableDomainNotTheSuffix covers two-label
// public suffixes, where a naive parent would be co.uk rather than example.co.uk.
func TestLoadCookieJarWidensToTheRegistrableDomainNotTheSuffix(t *testing.T) {
	path := writeCookieFile(t, []RawCookie{{
		Name: "sid", Value: "v", Domain: "shop.example.co.uk", Path: "/",
	}})
	jar, err := LoadCookieJarFromFile(path)
	if err != nil {
		t.Fatalf("LoadCookieJarFromFile: %v", err)
	}

	for _, target := range []string{"https://example.co.uk/", "https://www.example.co.uk/"} {
		parsed, err := url.Parse(target)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(jar.Cookies(parsed)) == 0 {
			t.Errorf("expected the cookie for %s", target)
		}
	}
	for _, target := range []string{"https://other.co.uk/", "https://co.uk/"} {
		parsed, err := url.Parse(target)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if cookies := jar.Cookies(parsed); len(cookies) != 0 {
			t.Errorf("cookie must not be sent to %s, got %v", target, cookies)
		}
	}
}

// TestLoadCookieJarToleratesABOM covers the way Windows editors write these files:
// a UTF-8 BOM makes json.Unmarshal fail, the jar stays empty, and the ladder never
// bridges a session — a silent failure that looked like "the site has no cookies".
func TestLoadCookieJarToleratesABOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies-bom.json")
	body, err := json.Marshal([]RawCookie{{Name: "li_at", Value: "session", Domain: "www.linkedin.com", Path: "/"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, body...)
	if err := os.WriteFile(path, withBOM, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	jar, report, err := LoadCookieJarFromFileDetailed(path)
	if err != nil {
		t.Fatalf("expected a BOM to be tolerated, got %v", err)
	}
	if report.Cookies == 0 {
		t.Fatalf("expected the cookie to load, got report %+v", report)
	}
	parsed, err := url.Parse("https://in.linkedin.com/in/someone-1234")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(jar.Cookies(parsed)) == 0 {
		t.Fatal("expected the widened session cookie to reach the profile subdomain")
	}
}

// TestLoadCookieJarReportsWhatItLoaded backs the startup line that makes a missing
// session obvious instead of leaving it to a later escalation failure.
func TestLoadCookieJarReportsWhatItLoaded(t *testing.T) {
	path := writeCookieFile(t, []RawCookie{
		{Name: "a", Value: "1", Domain: ".example.com"},
		{Name: "b", Value: "2", Domain: ".example.com"},
		{Name: "", Value: "no name", Domain: ".example.com"},
		{Name: "expired", Value: "3", Domain: ".example.com", ExpirationDate: 1000000000},
	})

	_, report, err := LoadCookieJarFromFileDetailed(path)
	if err != nil {
		t.Fatalf("LoadCookieJarFromFileDetailed: %v", err)
	}
	if report.Cookies != 2 {
		t.Errorf("Cookies = %d, want 2", report.Cookies)
	}
	if report.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", report.Skipped)
	}
	if len(report.Domains) != 1 || report.Domains[0] != "example.com (2)" {
		t.Errorf("Domains = %v, want [example.com (2)]", report.Domains)
	}
	if report.Path != path {
		t.Errorf("Path = %q, want %q", report.Path, path)
	}
}

// TestLoadCookieJarAcceptsPlaywrightStorageState covers the shape a LinkedIn session is
// usually captured in. Playwright writes {"cookies": [...], "origins": [...]} with a numeric
// "expires", and either difference used to fail the whole file — which emptied the jar and
// left the ladder reporting "this host simply has no cookies".
func TestLoadCookieJarAcceptsPlaywrightStorageState(t *testing.T) {
	state := `{
	  "cookies": [
	    {"name":"li_at","value":"session-token","domain":".linkedin.com","path":"/","expires":1893456000,"httpOnly":true,"secure":true,"sameSite":"None"},
	    {"name":"lang","value":"v=2&lang=en-us","domain":".www.linkedin.com","path":"/","expires":1893456000,"httpOnly":false,"secure":true,"sameSite":"Lax"}
	  ],
	  "origins": [{"origin":"https://www.linkedin.com","localStorage":[{"name":"k","value":"v"}]}]
	}`
	path := filepath.Join(t.TempDir(), "linkedin-storage.json")
	if err := os.WriteFile(path, []byte(state), 0o600); err != nil {
		t.Fatalf("write storage state: %v", err)
	}

	jar, report, err := LoadCookieJarFromFileDetailed(path)
	if err != nil {
		t.Fatalf("LoadCookieJarFromFileDetailed: %v", err)
	}
	if report.Cookies == 0 {
		t.Fatal("expected the storage-state cookies to load")
	}
	if report.Skipped != 0 {
		t.Errorf("expected no skipped cookies, got %d", report.Skipped)
	}

	// The session must reach a *profile* host, which is a sibling subdomain of the one the
	// export was scoped to: that widening is what makes an exported session usable here.
	for _, target := range []string{
		"https://www.linkedin.com/feed/",
		"https://in.linkedin.com/in/someone-1234",
		"https://linkedin.com/",
	} {
		parsed, err := url.Parse(target)
		if err != nil {
			t.Fatalf("parse %s: %v", target, err)
		}
		found := false
		for _, cookie := range jar.Cookies(parsed) {
			if cookie.Name == "li_at" && cookie.Value == "session-token" {
				found = true
			}
		}
		if !found {
			t.Errorf("session cookie not delivered to %s; got %v", target, jar.Cookies(parsed))
		}
	}
}

// TestLoadCookieJarAcceptsBothExpiryShapes pins the field exporters disagree about: a unix
// timestamp (Playwright) and an RFC1123 string (some extensions). A number used to fail the
// whole file, so a valid session was rejected over one field.
func TestLoadCookieJarAcceptsBothExpiryShapes(t *testing.T) {
	future := time.Now().AddDate(1, 0, 0)
	body := `[
	  {"name":"unix","value":"a","domain":"example.com","path":"/","expires":1893456000},
	  {"name":"rfc","value":"b","domain":"example.com","path":"/","expires":"` + future.UTC().Format(time.RFC1123) + `"},
	  {"name":"unreadable","value":"c","domain":"example.com","path":"/","expires":{"weird":true}}
	]`
	path := filepath.Join(t.TempDir(), "mixed.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}

	jar, report, err := LoadCookieJarFromFileDetailed(path)
	if err != nil {
		t.Fatalf("LoadCookieJarFromFileDetailed: %v", err)
	}
	if report.Cookies != 3 {
		t.Fatalf("expected all three cookies to load, got %d (skipped %d)", report.Cookies, report.Skipped)
	}
	parsed, _ := url.Parse("https://example.com/")
	names := make(map[string]bool)
	for _, cookie := range jar.Cookies(parsed) {
		names[cookie.Name] = true
	}
	for _, want := range []string{"unix", "rfc", "unreadable"} {
		if !names[want] {
			t.Errorf("expected the %q cookie to be loaded, got %v", want, names)
		}
	}
}

// TestLoadCookieJarRejectsAnUnrecognisedObject keeps the failure legible: an object without a
// "cookies" array is not a storage state, and saying so beats an empty jar.
func TestLoadCookieJarRejectsAnUnrecognisedObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wrong.json")
	if err := os.WriteFile(path, []byte(`{"session":"abc"}`), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
	_, _, err := LoadCookieJarFromFileDetailed(path)
	if err == nil {
		t.Fatal("expected an error for a JSON object that is not a cookie export")
	}
	if !strings.Contains(err.Error(), "cookies") {
		t.Errorf("expected the error to name the missing cookies array, got %v", err)
	}
}

// TestLoadCookieJarWidensASessionCookieEvenWhenTheParentIsPartlyCovered is the case a real
// LinkedIn export produces: the session cookie is scoped to www.linkedin.com while other
// cookies are scoped to .linkedin.com. Widening used to skip the whole child domain as soon
// as *any* cookie declared the parent, so the session never reached in.linkedin.com — the one
// host that serves person profiles — and the ladder reported "no cookies for this host".
func TestLoadCookieJarWidensASessionCookieEvenWhenTheParentIsPartlyCovered(t *testing.T) {
	path := writeCookieFile(t, []RawCookie{
		{Name: "lang", Value: "v=2&lang=en-us", Domain: ".linkedin.com", Path: "/"},
		{Name: "li_at", Value: "session", Domain: "www.linkedin.com", Path: "/", HostOnly: true, Secure: true},
	})

	jar, report, err := LoadCookieJarFromFileDetailed(path)
	if err != nil {
		t.Fatalf("LoadCookieJarFromFileDetailed: %v", err)
	}
	if report.WidenedToParent != 1 {
		t.Errorf("expected the session cookie to be widened, got %d", report.WidenedToParent)
	}

	profile, err := url.Parse("https://in.linkedin.com/in/someone-1234")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	found := false
	for _, cookie := range jar.Cookies(profile) {
		if cookie.Name == "li_at" && cookie.Value == "session" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected li_at on the profile host, got %v", jar.Cookies(profile))
	}

	// No name is ever registered twice for one domain, however many children declare it.
	counts := make(map[string]int)
	for _, cookie := range jar.Cookies(profile) {
		counts[cookie.Name]++
	}
	for name, count := range counts {
		if count != 1 {
			t.Errorf("cookie %q delivered %d times to one host", name, count)
		}
	}
}
