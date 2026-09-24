package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"sort"
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

// CookieJarReport summarises what a cookie export actually contributed, so
// startup can state whether a session loaded instead of leaving a later failure
// to explain it. Domains is sorted, with counts: "linkedin.com (7)".
type CookieJarReport struct {
	Path string
	// Cookies is how many cookies were registered, Skipped how many were dropped
	// (empty name or value, expired, or no domain).
	Cookies int
	Skipped int
	// WidenedToParent counts cookies registered for their registrable parent in
	// addition to the exported host.
	WidenedToParent int
	Domains         []string
}

// LoadCookieJarFromFile parses an exported JSON cookie file and populates a CookieJar.
//
// Leading dots in cookie domains (the standard export form, e.g. ".google.com")
// are stripped so the jar treats them as domain cookies that also cover
// subdomains. Expired and empty cookies are skipped.
func LoadCookieJarFromFile(filePath string) (*cookiejar.Jar, error) {
	jar, _, err := LoadCookieJarFromFileDetailed(filePath)
	return jar, err
}

// LoadCookieJarFromFileDetailed is LoadCookieJarFromFile plus the summary startup
// logs. It also registers a host-scoped cookie for its registrable parent.
//
// That widening is what makes an exported session usable: browsers and exporters
// commonly scope a session to the host the user was browsing (www.linkedin.com),
// and an authwall is served on a *different* subdomain (in.linkedin.com for
// profiles), so without the parent the jar looks empty exactly where the ladder
// needs it and the cookie-render step is skipped without explanation.
func LoadCookieJarFromFileDetailed(filePath string) (*cookiejar.Jar, CookieJarReport, error) {
	report := CookieJarReport{Path: filePath}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, report, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	if filePath == "" {
		return jar, report, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return jar, report, fmt.Errorf("cookie file could not be read: %w", err)
	}
	// Editors on Windows save JSON with a UTF-8 BOM (VS Code's "UTF-8 with BOM",
	// Notepad), and json.Unmarshal rejects the leading bytes. Tolerating the BOM
	// matters here because the failure is otherwise silent: the jar stays empty
	// and the ladder simply never bridges a session.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	data = bytes.TrimSpace(data)

	var rawCookies []RawCookie
	if err := json.Unmarshal(data, &rawCookies); err != nil {
		return jar, report, fmt.Errorf("failed to parse cookie JSON: %w", err)
	}

	now := time.Now()
	cookiesByDomain := make(map[string][]*http.Cookie)
	for _, rc := range rawCookies {
		if rc.Name == "" || rc.Value == "" {
			report.Skipped++
			continue
		}
		if isCookieExpired(rc, now) {
			report.Skipped++
			continue
		}
		domain := strings.TrimPrefix(strings.TrimSpace(rc.Domain), ".")
		if domain == "" {
			report.Skipped++
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

	// Widening pass: a host-scoped cookie is also registered for its registrable
	// parent, unless the export already covers that domain itself. Additions are
	// collected first so the map is not mutated while it is being read.
	declared := make(map[string]bool, len(cookiesByDomain))
	for domain := range cookiesByDomain {
		declared[domain] = true
	}
	widened := make(map[string][]*http.Cookie)
	for domain, cookies := range cookiesByDomain {
		parent := registrableParent(domain)
		if parent == "" || parent == domain || declared[parent] {
			continue
		}
		// Each cookie is copied with the parent domain: reusing the same value
		// would carry the child's Domain into a request for the parent, which the
		// jar rejects as a non-matching domain.
		for _, cookie := range cookies {
			clone := *cookie
			clone.Domain = parent
			widened[parent] = append(widened[parent], &clone)
		}
		report.WidenedToParent += len(cookies)
	}
	for parent, cookies := range widened {
		cookiesByDomain[parent] = append(cookiesByDomain[parent], cookies...)
	}

	failed := 0
	var firstErr error
	domains := make([]string, 0, len(cookiesByDomain))
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
		report.Cookies += len(cookies)
		domains = append(domains, fmt.Sprintf("%s (%d)", domain, len(cookies)))
	}
	sort.Strings(domains)
	report.Domains = domains

	if failed > 0 {
		return jar, report, fmt.Errorf("failed to parse url for %d domain(s): %v", failed, firstErr)
	}
	return jar, report, nil
}

// multiPartSuffixes are the public suffixes that are themselves two labels, so a
// cookie scoped to www.example.co.uk must be widened to example.co.uk rather than
// to co.uk.
var multiPartSuffixes = map[string]bool{
	"co.uk": true, "org.uk": true, "ac.uk": true, "gov.uk": true,
	"com.au": true, "net.au": true, "org.au": true,
	"co.in": true, "net.in": true, "org.in": true, "co.nz": true,
	"co.jp": true, "co.za": true, "co.kr": true, "co.id": true, "co.th": true,
	"com.br": true, "com.mx": true, "com.sg": true, "com.hk": true, "com.tr": true,
	"com.my": true, "com.ph": true, "com.vn": true, "com.tw": true, "com.cn": true,
	"com.pk": true, "com.bd": true, "com.sa": true, "com.eg": true, "com.ng": true,
	"co.ke": true,
}

// registrableParent returns the domain worth widening a host-scoped cookie to, or
// "" when there is none: an IP address, a single-label host, a registrable domain
// itself, or a public suffix.
func registrableParent(host string) string {
	if net.ParseIP(host) != nil {
		return ""
	}
	labels := strings.Split(host, ".")
	if len(labels) < 3 {
		return ""
	}
	parent := strings.Join(labels[len(labels)-2:], ".")
	if multiPartSuffixes[parent] {
		if len(labels) < 4 {
			return ""
		}
		parent = strings.Join(labels[len(labels)-3:], ".")
	}
	if multiPartSuffixes[parent] || parent == host {
		return ""
	}
	return parent
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
