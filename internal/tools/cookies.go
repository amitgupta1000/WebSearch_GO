package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
)

// RawCookie matches the standard JSON export format from browser extensions (Cookie-Editor, EditThisCookie).
type RawCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Secure   bool    `json:"secure"`
	HttpOnly bool    `json:"httpOnly"`
	SameSite string  `json:"sameSite"`
	HostOnly bool    `json:"hostOnly"`
}

// LoadCookieJarFromFile parses an exported JSON cookie file and populates a CookieJar.
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

	cookiesByDomain := make(map[string][]*http.Cookie)
	for _, rc := range rawCookies {
		domain := rc.Domain
		cookie := &http.Cookie{
			Name:     rc.Name,
			Value:    rc.Value,
			Path:     rc.Path,
			Domain:   domain,
			Secure:   rc.Secure,
			HttpOnly: rc.HttpOnly,
		}
		cookiesByDomain[domain] = append(cookiesByDomain[domain], cookie)
	}

	for domain, cookies := range cookiesByDomain {
		parsedURL, err := url.Parse("https://" + domain)
		if err == nil {
			jar.SetCookies(parsedURL, cookies)
		}
	}

	return jar, nil
}
