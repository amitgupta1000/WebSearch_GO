package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"
)

// RawCookie matches the standard JSON export format from browser extensions (Cookie-Editor, EditThisCookie).
type RawCookie struct {
	Name           string  `json:"name"`
	Value          string  `json:"value"`
	Domain         string  `json:"domain"`
	Path           string  `json:"path"`
	Secure         bool    `json:"secure"`
	HttpOnly       bool    `json:"httpOnly"`
	SameSite       string  `json:"sameSite"`
	HostOnly       bool    `json:"hostOnly"`
	Expires        string  `json:"expires,omitempty"`        // RFC1123 date string (some exporters)
	ExpirationDate float64 `json:"expirationDate,omitempty"` // unix seconds (EditThisCookie)
}

// LoadCookieJarFromFile parses an exported JSON cookie file and populates a CookieJar.
//
// Leading dots in cookie domains (the standard export form, e.g. ".google.com")
// are stripped so the jar treats them as domain cookies that also cover
// subdomains. Expired and empty cookies are skipped.
func LoadCookieJarFromFile(filePath string) (*cookiejar.Jar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	if filePath == "" {
		return jar, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return jar, fmt.Errorf("cookie file could not be read: %w", err)
	}

	var rawCookies []RawCookie
	if err := json.Unmarshal(data, &rawCookies); err != nil {
		return jar, fmt.Errorf("failed to parse cookie JSON: %w", err)
	}

	now := time.Now()
	cookiesByDomain := make(map[string][]*http.Cookie)
	for _, rc := range rawCookies {
		if rc.Name == "" || rc.Value == "" {
			continue
		}
		if isCookieExpired(rc, now) {
			continue
		}
		domain := strings.TrimPrefix(strings.TrimSpace(rc.Domain), ".")
		if domain == "" {
			continue
		}
		cookiesByDomain[domain] = append(cookiesByDomain[domain], &http.Cookie{
			Name:     rc.Name,
			Value:    rc.Value,
			Path:     rc.Path,
			Domain:   domain,
			Secure:   rc.Secure,
			HttpOnly: rc.HttpOnly,
		})
	}

	failed := 0
	var firstErr error
	for domain, cookies := range cookiesByDomain {
		parsedURL, err := url.Parse("https://" + domain)
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		jar.SetCookies(parsedURL, cookies)
	}

	if failed > 0 {
		return jar, fmt.Errorf("failed to parse url for %d domain(s): %v", failed, firstErr)
	}
	return jar, nil
}

// isCookieExpired reports whether an exported cookie has already expired,
// honoring both the unix-based expirationDate and the RFC1123 expires fields.
func isCookieExpired(rc RawCookie, now time.Time) bool {
	if rc.ExpirationDate > 0 {
		return now.Unix() > int64(rc.ExpirationDate)
	}
	if rc.Expires != "" {
		t, err := http.ParseTime(rc.Expires)
		if err != nil {
			// http.ParseTime's layouts only accept the literal "GMT" zone;
			// fall back to RFC1123, which accepts any zone abbreviation
			// (e.g. the "UTC" some exporters emit).
			t, err = time.Parse(time.RFC1123, rc.Expires)
		}
		if err == nil {
			return now.After(t)
		}
	}
	return false
}
