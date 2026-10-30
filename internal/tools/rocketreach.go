package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	rocketReachLookupURL = "https://api.rocketreach.co/api/v2/person/lookup"
	rocketReachSearchURL = "https://api.rocketreach.co/api/v2/person/search"
)

// RocketReachClient performs explicitly requested contact lookups. Search
// discovery is intentionally not part of this client because the application
// uses the existing indexed-search pipeline for discovery.
type RocketReachClient struct {
	apiKey    string
	client    *http.Client
	baseURL   string
	searchURL string
}

type RocketReachContact struct {
	Email string `json:"email"`
	Type  string `json:"type,omitempty"`
	Grade string `json:"grade,omitempty"`
}

type RocketReachPhone struct {
	Number string `json:"number"`
	Type   string `json:"type,omitempty"`
}

type RocketReachJob struct {
	Title       string `json:"title,omitempty"`
	Company     string `json:"company,omitempty"`
	Location    string `json:"location,omitempty"`
	StartDate   string `json:"start_date,omitempty"`
	EndDate     string `json:"end_date,omitempty"`
	Description string `json:"description,omitempty"`
}

type RocketReachEducation struct {
	Institution string `json:"institution,omitempty"`
	School      string `json:"school,omitempty"`
	Degree      string `json:"degree,omitempty"`
	Field       string `json:"field,omitempty"`
	Major       string `json:"major,omitempty"`
	StartYear   string `json:"start_year,omitempty"`
	EndYear     string `json:"end_year,omitempty"`
	Year        string `json:"year,omitempty"`
}

type RocketReachProfile struct {
	ID              int                    `json:"id,omitempty"`
	Name            string                 `json:"name,omitempty"`
	CurrentTitle    string                 `json:"current_title,omitempty"`
	CurrentEmployer string                 `json:"current_employer,omitempty"`
	LinkedInURL     string                 `json:"linkedin_url,omitempty"`
	Location        string                 `json:"location,omitempty"`
	City            string                 `json:"city,omitempty"`
	Region          string                 `json:"region,omitempty"`
	Country         string                 `json:"country,omitempty"`
	JobHistory      []RocketReachJob       `json:"job_history,omitempty"`
	Education       []RocketReachEducation `json:"education,omitempty"`
	Skills          []string               `json:"skills,omitempty"`
	Emails          []RocketReachContact   `json:"emails,omitempty"`
	Phones          []RocketReachPhone     `json:"phones,omitempty"`
}

func NewRocketReachClient(apiKey string) *RocketReachClient {
	return &RocketReachClient{
		apiKey:    strings.TrimSpace(apiKey),
		client:    &http.Client{Timeout: 20 * time.Second},
		baseURL:   rocketReachLookupURL,
		searchURL: rocketReachSearchURL,
	}
}

// Search discovers profiles without requesting contact details. Search calls
// do not perform paid contact enrichment.
func (c *RocketReachClient) Search(ctx context.Context, query map[string][]string) ([]RocketReachProfile, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("RocketReach is not configured")
	}
	body, err := json.Marshal(struct {
		Query map[string][]string `json:"query"`
	}{Query: query})
	if err != nil {
		return nil, fmt.Errorf("encode RocketReach search request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.searchURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("create RocketReach search request: %w", err)
	}
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RocketReach search request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("RocketReach search returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Profiles []RocketReachProfile `json:"profiles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode RocketReach search response: %w", err)
	}
	return result.Profiles, nil
}

func (c *RocketReachClient) Enabled() bool {
	return c != nil && c.apiKey != ""
}

// Lookup retrieves contact details only for an explicitly initiated person
// dossier/search. The API does not charge a lookup credit when no match is
// found, but callers still need to treat this as a paid operation.
func (c *RocketReachClient) Lookup(ctx context.Context, name, company, linkedinURL string) (*RocketReachProfile, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("RocketReach is not configured")
	}
	params := url.Values{}
	if strings.TrimSpace(linkedinURL) != "" {
		params.Set("linkedin_url", strings.TrimSpace(linkedinURL))
	} else {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("RocketReach lookup requires a person name or LinkedIn URL")
		}
		params.Set("name", strings.TrimSpace(name))
		if strings.TrimSpace(company) != "" {
			params.Set("current_employer", strings.TrimSpace(company))
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create RocketReach lookup request: %w", err)
	}
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RocketReach lookup request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("RocketReach lookup returned HTTP %d", resp.StatusCode)
	}
	var profile RocketReachProfile
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("decode RocketReach lookup response: %w", err)
	}
	return &profile, nil
}
