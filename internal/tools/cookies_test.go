package tools

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
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
