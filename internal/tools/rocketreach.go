package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	rocketReachLookupURL       = "https://api.rocketreach.co/api/v2/person/lookup"
	rocketReachSearchURL       = "https://api.rocketreach.co/api/v2/person/search"
	rocketReachMaxPages        = 3
	rocketReachCompanyPageSize = 100
	// rocketReachTimeout is one request's budget. The search endpoint answers
	// 100-result pages in tens of seconds under load; 20s cut healthy responses.
	rocketReachTimeout = 45 * time.Second
	// rocketReachMaxAttempts is one logical call's attempt count: exactly one
	// retry for the transport cuts and blips, never for stable rejections.
	rocketReachMaxAttempts = 2
	// rocketReachRetryBackoff is the pause between attempts of a transport cut.
	rocketReachRetryBackoff = 750 * time.Millisecond
	// rocketReachMaxRetryAfter caps a 429 Retry-After the client is willing to
	// wait out. Longer windows (minutes, hours) are the provider saying the
	// quota is done for the period — retrying would just extend the wait, so
	// the error surfaces instead.
	rocketReachMaxRetryAfter = 10 * time.Second
	// rocketReachMaxErrorBody caps how much of a non-2xx body is preserved.
	// The body is where RocketReach explains itself (quota exhausted, invalid
	// facet, plan limit) — discarding it made every failure undiagnosable.
	rocketReachMaxErrorBody = 256
)

// rocketReachTransport pins the client to the machine's own egress. The
// RocketReach API is an authenticated JSON endpoint with no IP-reputation gate,
// and routing it through the shared HTTPS_PROXY — a residential proxy on the
// Cloud Run deployment — produced intermittent mid-response cuts ("unexpected
// EOF") on the search endpoint. The scraper's per-host proxy scoping does not
// apply here: this client owns its transport.
var rocketReachTransport = &http.Transport{Proxy: nil}

// RocketReachClient performs explicitly requested contact lookups. Search
// discovery is intentionally not part of this client because the application
// uses the existing indexed-search pipeline for discovery.
type RocketReachClient struct {
	apiKey    string
	client    *http.Client
	baseURL   string
	searchURL string
}

// RocketReachHTTPError preserves provider status metadata so callers can
// distinguish authentication failures from rate limiting and report a useful
// retry hint. Detail carries the provider's own explanation (quota exhausted,
// invalid facet, plan limit), truncated, because that text is what makes a
// failure diagnosable instead of a bare status code.
type RocketReachHTTPError struct {
	Operation  string
	StatusCode int
	RetryAfter string
	Detail     string
}

func (e *RocketReachHTTPError) Error() string {
	message := fmt.Sprintf("RocketReach %s returned HTTP %d", e.Operation, e.StatusCode)
	if e.RetryAfter != "" {
		message += " (retry after " + e.RetryAfter + ")"
	}
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message
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
		client:    &http.Client{Timeout: rocketReachTimeout, Transport: rocketReachTransport},
		baseURL:   rocketReachLookupURL,
		searchURL: rocketReachSearchURL,
	}
}

// Search discovers profiles without requesting contact details. Search calls
// do not perform paid contact enrichment.
func (c *RocketReachClient) Search(ctx context.Context, query map[string][]string) ([]RocketReachProfile, error) {
	return c.search(ctx, query, 0)
}

// SearchCompany uses RocketReach's company-first people search: the caller
// supplies company and structured facets such as department or experience.
// It returns profile summaries only; contact enrichment remains a separate
// explicit lookup.
func (c *RocketReachClient) SearchCompany(ctx context.Context, query map[string][]string) ([]RocketReachProfile, error) {
	return c.search(ctx, query, rocketReachCompanyPageSize)
}

func (c *RocketReachClient) search(ctx context.Context, query map[string][]string, pageSize int) ([]RocketReachProfile, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("RocketReach is not configured")
	}
	start := 1
	var profiles []RocketReachProfile
	for page := 0; page < rocketReachMaxPages; page++ {
		var result *rocketReachSearchResponse
		var err error
		for attempt := 1; attempt <= rocketReachMaxAttempts; attempt++ {
			result, err = c.searchPage(ctx, query, start, pageSize)
			if err == nil {
				break
			}
			if attempt >= rocketReachMaxAttempts {
				return nil, err
			}
			wait, retriable := rocketReachRetryWait(err)
			if !retriable {
				return nil, err
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
		profiles = append(profiles, result.Profiles...)
		if result.Pagination.Next <= start || result.Pagination.Next == 0 {
			break
		}
		start = result.Pagination.Next
	}
	return profiles, nil
}

func (c *RocketReachClient) searchPage(ctx context.Context, query map[string][]string, start, pageSize int) (*rocketReachSearchResponse, error) {
	body, err := json.Marshal(struct {
		Query    map[string][]string `json:"query"`
		Start    int                 `json:"start,omitempty"`
		PageSize int                 `json:"page_size,omitempty"`
		OrderBy  string              `json:"order_by,omitempty"`
	}{Query: query, Start: start, PageSize: pageSize, OrderBy: "relevance"})
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, rocketReachMaxErrorBody))
		return nil, &RocketReachHTTPError{
			Operation:  "search",
			StatusCode: resp.StatusCode,
			RetryAfter: strings.TrimSpace(resp.Header.Get("Retry-After")),
			Detail:     strings.TrimSpace(string(body)),
		}
	}
	var result rocketReachSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode RocketReach search response: %w", err)
	}
	return &result, nil
}

// rocketReachRetryWait classifies a failure and returns the pause one retry
// should take. Transport-level cuts (unexpected EOF, resets, timeouts) are the
// flaky-egress signature and get a short backoff; 5xx gets the same; a 429 is
// retried only when its Retry-After fits inside the wait cap — a longer window
// means the quota is done for the period and the error should surface. Stable
// rejections (401, 403, 400, decode errors) never retry.
func rocketReachRetryWait(err error) (time.Duration, bool) {
	var httpErr *RocketReachHTTPError
	if errors.As(err, &httpErr) {
		switch {
		case httpErr.StatusCode >= 500:
			return rocketReachRetryBackoff, true
		case httpErr.StatusCode == http.StatusTooManyRequests:
			after, parseErr := strconv.Atoi(strings.TrimSpace(httpErr.RetryAfter))
			if parseErr != nil || after <= 0 {
				return 0, false
			}
			wait := time.Duration(after) * time.Second
			if wait > rocketReachMaxRetryAfter {
				return 0, false
			}
			return wait, true
		default:
			return 0, false
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return rocketReachRetryBackoff, true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return rocketReachRetryBackoff, true
	}
	return 0, false
}

type rocketReachSearchResponse struct {
	Profiles   []RocketReachProfile `json:"profiles"`
	Pagination struct {
		Start int `json:"start"`
		Next  int `json:"next"`
		Total int `json:"total"`
	} `json:"pagination"`
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
	return c.lookupWithParams(ctx, params)
}

// LookupByID retrieves the exact profile returned by person_search. Using the
// profile ID avoids ambiguity when a display name or employer has aliases.
func (c *RocketReachClient) LookupByID(ctx context.Context, id int) (*RocketReachProfile, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("RocketReach is not configured")
	}
	if id <= 0 {
		return nil, fmt.Errorf("RocketReach lookup requires a positive profile ID")
	}
	return c.lookupWithParams(ctx, url.Values{"id": []string{strconv.Itoa(id)}})
}

// lookupWithParams runs one lookup with the same bounded retry the search
// path gets: a paid call should not fail on a transport cut that a second
// attempt would survive.
func (c *RocketReachClient) lookupWithParams(ctx context.Context, params url.Values) (*RocketReachProfile, error) {
	var lastErr error
	for attempt := 1; attempt <= rocketReachMaxAttempts; attempt++ {
		profile, err := c.lookupAttempt(ctx, params)
		if err == nil {
			return profile, nil
		}
		lastErr = err
		if attempt >= rocketReachMaxAttempts {
			break
		}
		wait, retriable := rocketReachRetryWait(err)
		if !retriable {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

func (c *RocketReachClient) lookupAttempt(ctx context.Context, params url.Values) (*RocketReachProfile, error) {
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, rocketReachMaxErrorBody))
		return nil, &RocketReachHTTPError{
			Operation:  "lookup",
			StatusCode: resp.StatusCode,
			RetryAfter: strings.TrimSpace(resp.Header.Get("Retry-After")),
			Detail:     strings.TrimSpace(string(body)),
		}
	}
	var profile RocketReachProfile
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("decode RocketReach lookup response: %w", err)
	}
	return &profile, nil
}
