package tools

// juristway.go is the client for juristway.com, a commercial Indian case-law
// database behind a Next.js frontend and a LoopBack REST API.
//
// Measured (October 2026), all against the live site:
//
//   - robots.txt publishes an API allow-list — /api/caseLaws, /api/laws,
//     /api/sections, /api/acts and friends are explicitly Allowed before a
//     general "Disallow: /api/". The metadata search therefore rides
//     /api/caseLaws, a surface the site itself grants to programmatic
//     clients; the account-gated paths (/auth/, /login, /profile) are not
//     crawled by this client.
//   - The caseLaws listing returns rich records anonymously: parties,
//     advocates, court, bench, case date and status, document number, a
//     citation shaped like "2017-juristway.com-36-HC(Delhi)-GST", and an
//     editorial headNote (HTML). Roughly 43,000 judgments are indexed and
//     the corpus skews toward high courts, tribunals and tax (GST) matters,
//     which complements IndianKanoon's raw-judgment coverage rather than
//     duplicating it.
//   - Search is a LoopBack filter: where clauses run the regex "like"
//     operator with options "i" (the "ilike" operator answers HTTP 500), the
//     filter travels URL-encoded in a single query parameter, and records
//     sort by caseDate DESC. Listing access needs no credentials.
//   - Full judgment text is subscription-gated: /api/access/status answers
//     the session's plan/active/daysLeft, locked records never carry text in
//     the API payload, and the public /caselaw/<slug> page ships the
//     editorial summary with the raw order behind the paywall. This client
//     therefore extracts the metadata + headnote layer the free tier serves
//     and fetches the public detail page for identity confirmation.
//
// Authentication is optional and best-effort: when credentials are
// configured the client logs in once (POST /api/auth/login — the account
// holder's own credential flow, a LoopBack token with a two-week TTL),
// caches the token, and attaches access_token to model calls. A failed login
// never fails a search — the listing answers anonymously — it only leaves
// the session on the free tier. A token the server rejects is dropped and
// the search retries without it rather than surfacing a 401.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	defaultJuristwayURL = "https://www.juristway.com"
	// juristwayUserAgent is the browser identity the site's own frontend
	// presents; the API answers plain clients with the same payloads.
	juristwayUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	// juristwaySnippetCap bounds the headnote text carried per hit, mirroring
	// the agent's own snippet budget.
	juristwaySnippetCap = 600
	// juristwayMaxDocText bounds a detail page's extracted text so one verbose
	// case page cannot bloat a document payload.
	juristwayMaxDocText = 20000
	// juristwayMaxBodyBytes caps any response body. A judgment page runs to a
	// few hundred KB of HTML; anything beyond is not a page this mode can use
	// within its budget.
	juristwayMaxBodyBytes = 3 << 20
	// juristwayTokenSlack trims the cached token's ttl so it is refreshed
	// before the server would reject it mid-run.
	juristwayTokenSlack = 60 * time.Second
)

// juristwayRecord is one /api/caseLaws record as the API returns it (LoopBack
// camelCase keys). The listing's field projection asks exactly for these.
type juristwayRecord struct {
	ID                    string `json:"id"`
	Citation              string `json:"citation"`
	CitationWithApplicant string `json:"citationWithApplicant"`
	CitationPost          string `json:"citationPost"`
	CaseDate              string `json:"caseDate"`
	CaseStatus            string `json:"caseStatus"`
	PartyAppellant        string `json:"partyAppellant"`
	PartyRespondent       string `json:"partyRespondent"`
	HeadNote              string `json:"headNote"`
	Slug                  string `json:"slug"`
	DocumentNumber        string `json:"documentNumber"`
}

// juristwayToken is the LoopBack access-token envelope POST /api/auth/login
// answers with; ttl is seconds.
type juristwayToken struct {
	ID     string `json:"id"`
	UserID string `json:"userId"`
	TTL    int    `json:"ttl"`
}

// JuristwayClient searches the juristway.com case-law API and fetches its
// public case pages. It satisfies the legal agent's legalSource contract
// (Search + FetchDocument), so the Juristway track plugs into the same
// bounded search walk the other sources use.
type JuristwayClient struct {
	baseURL    string // overridable in tests
	httpClient *http.Client
	limiter    *hostLimiter
	robots     *robotsGuard

	email    string
	password string

	mu           sync.Mutex
	token        string
	tokenExpires time.Time
}

// NewJuristwayClient builds the client on its own HTTP plumbing (JSON
// requests with a cookie jar, which the scraper's FetchHTML cannot carry):
// one request a second, one in flight. Credentials are optional — without
// them the client searches anonymously, which the site's published API
// allow-list permits.
func NewJuristwayClient(email, password string) *JuristwayClient {
	jar, _ := cookiejar.New(nil)
	return &JuristwayClient{
		baseURL:    defaultJuristwayURL,
		httpClient: &http.Client{Timeout: 15 * time.Second, Jar: jar},
		limiter:    newHostLimiter(1.0, 1, 1),
		robots:     newRobotsGuard(defaultJuristwayURL + "/robots.txt"),
		email:      strings.TrimSpace(email),
		password:   password,
	}
}

// Search runs one case-law metadata search against /api/caseLaws. The query
// is the party or subject term the planner composed — quotes are stripped,
// because the API matches a regex term rather than a phrase. page selects the
// LoopBack skip window (the agent asks for page 1 only) and maxResults bounds
// both the request and the answer.
func (c *JuristwayClient) Search(ctx context.Context, query string, page, maxResults int) ([]LegalSearchHit, error) {
	term := strings.Trim(strings.TrimSpace(query), `"`)
	if term == "" {
		return nil, fmt.Errorf("juristway search query is empty")
	}
	if maxResults <= 0 {
		maxResults = 10
	}
	if page <= 0 {
		page = 1
	}

	values := url.Values{}
	values.Set("filter", juristwayFilterJSON(term, "", maxResults, (page-1)*maxResults))
	if token := c.ensureToken(ctx); token != "" {
		values.Set("access_token", token)
	}

	apiURL := c.baseURL + "/api/caseLaws?" + values.Encode()
	body, status, err := c.get(ctx, apiURL)
	if err != nil {
		return nil, err
	}
	if status == http.StatusInternalServerError && values.Get("access_token") == "" {
		// A LoopBack deployment can restrict operators server-side; measured
		// behavior says the compound OR filter is accepted, but a rejection
		// must not blind the track. One fallback attempt searches the
		// citation line alone, which carries parties and court anyway.
		values.Set("filter", juristwayFilterJSON(term, "citationWithApplicant", maxResults, (page-1)*maxResults))
		body, status, err = c.get(ctx, c.baseURL+"/api/caseLaws?"+values.Encode())
		if err != nil {
			return nil, err
		}
	}
	if status == http.StatusUnauthorized && values.Get("access_token") != "" {
		// A stale token degrades to the anonymous tier rather than failing
		// the search: the listing answers without credentials.
		c.dropToken()
		values.Del("access_token")
		body, status, err = c.get(ctx, c.baseURL+"/api/caseLaws?"+values.Encode())
		if err != nil {
			return nil, err
		}
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("juristway: case-law listing answered HTTP %d", status)
	}

	var records []juristwayRecord
	if err := json.Unmarshal([]byte(body), &records); err != nil {
		return nil, fmt.Errorf("juristway: decode listing: %w", err)
	}
	hits := make([]LegalSearchHit, 0, len(records))
	for _, record := range records {
		hits = append(hits, juristwayHit(c.baseURL, record))
	}
	return hits, nil
}

// juristwayFilterJSON builds the LoopBack filter: the regex "like" operator
// with options "i" across the identity fields (measured: "ilike" answers
// HTTP 500), sorted newest first, with a field projection that asks only for
// the metadata the agent consumes. When field is non-empty the where clause
// narrows to that single field — the fallback shape.
func juristwayFilterJSON(term, field string, limit, skip int) string {
	like := map[string]any{"like": term, "options": "i"}
	var where map[string]any
	if field != "" {
		where = map[string]any{field: like}
	} else {
		where = map[string]any{"or": []map[string]any{
			{"citationWithApplicant": like},
			{"headNote": like},
			{"partyAppellant": like},
			{"partyRespondent": like},
		}}
	}
	filter := map[string]any{
		"where": where,
		"fields": map[string]bool{
			"id": true, "citation": true, "citationWithApplicant": true, "citationPost": true,
			"caseDate": true, "caseStatus": true, "partyAppellant": true, "partyRespondent": true,
			"headNote": true, "slug": true, "documentNumber": true,
		},
		"limit": limit,
		"skip":  skip,
		"order": "caseDate DESC",
	}
	encoded, err := json.Marshal(filter)
	if err != nil {
		// A map of strings and bools cannot fail to marshal; the guard keeps
		// the signature honest without an error channel through the search.
		return "{}"
	}
	return string(encoded)
}

// juristwayHit maps one API record into the agent's hit shape. The title is
// the site's own citation-with-applicants line, the court is parsed from the
// citation's court segment, and the headnote's HTML is flattened into the
// snippet — the identity fields are what make party matching stronger than
// title text alone.
func juristwayHit(baseURL string, record juristwayRecord) LegalSearchHit {
	title := record.CitationWithApplicant
	if title == "" {
		title = strings.TrimSpace(record.PartyAppellant + " vs " + record.PartyRespondent)
	}
	return LegalSearchHit{
		Title:      title,
		DocID:      record.ID,
		DocURL:     baseURL + "/caselaw/" + record.Slug,
		DocType:    "judgment",
		Court:      parseJuristwayCourt(record.Citation),
		Date:       record.CaseDate,
		CaseNumber: record.DocumentNumber,
		Petitioner: record.PartyAppellant,
		Respondent: record.PartyRespondent,
		Snippet:    capJuristwayText(htmlToJuristwayText(record.HeadNote), juristwaySnippetCap),
	}
}

// FetchDocument fetches one public case page (/caselaw/<slug>) and returns
// what a logged-out reader sees: the title, the court parsed from the
// citation, and the visible editorial text (headnote and summary). Full
// judgment text is subscription-gated on this site, so the document carries
// the identity layer — who the parties are, which court, what the headnote
// says — not the raw order.
func (c *JuristwayClient) FetchDocument(ctx context.Context, docURL string) (*LegalDocument, error) {
	trimmed := strings.TrimSpace(docURL)
	if trimmed == "" {
		return nil, fmt.Errorf("juristway document url is empty")
	}
	target := trimmed
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		if strings.HasPrefix(trimmed, "/") {
			// An absolute path is already site-resolved (a hit's own URL
			// shape minus the origin).
			target = c.baseURL + trimmed
		} else {
			target = c.baseURL + "/caselaw/" + trimmed
		}
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("juristway: parse url: %w", err)
	}
	if host := parsed.Hostname(); host != "" && host != hostOf(c.baseURL) {
		return nil, fmt.Errorf("juristway: url is not a juristway case page: %s", docURL)
	}
	if !c.robots.allows(parsed.Path) {
		return nil, fmt.Errorf("juristway: robots.txt disallows %s", parsed.Path)
	}
	body, status, err := c.get(ctx, target)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, fmt.Errorf("juristway: case page not found: %s", target)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("juristway: case page answered HTTP %d for %s", status, target)
	}
	return parseJuristwayDocument(target, body), nil
}

// get fetches one URL with the browser identity, the politeness limiter and a
// body cap. It returns the body and the status so callers can distinguish a
// stale-token 401 from an empty answer.
func (c *JuristwayClient) get(ctx context.Context, targetURL string) (string, int, error) {
	release, err := c.limiter.acquire(ctx, hostOf(targetURL))
	if err != nil {
		return "", 0, err
	}
	defer release()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", 0, fmt.Errorf("juristway: build request: %w", err)
	}
	req.Header.Set("User-Agent", juristwayUserAgent)
	req.Header.Set("Accept", "application/json, text/html;q=0.9, */*;q=0.8")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("juristway: fetch %s: %w", targetURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, juristwayMaxBodyBytes))
	if err != nil {
		return "", resp.StatusCode, fmt.Errorf("juristway: read %s: %w", targetURL, err)
	}
	return string(body), resp.StatusCode, nil
}

// ensureToken returns the cached access token, logging in once when the
// operator configured credentials. A login failure degrades to the anonymous
// tier — the metadata search answers without credentials — rather than
// failing the search; the token only raises what the session may read. The
// lock is held through the login so parallel searches neither stampede the
// endpoint nor replay a token this call is refreshing.
func (c *JuristwayClient) ensureToken(ctx context.Context) string {
	if c.email == "" || c.password == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExpires) {
		return c.token
	}
	release, err := c.limiter.acquire(ctx, hostOf(c.baseURL))
	if err != nil {
		return ""
	}
	defer release()
	payload, err := json.Marshal(map[string]string{"email": c.email, "password": c.password})
	if err != nil {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/auth/login", bytes.NewReader(payload))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", juristwayUserAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	var token juristwayToken
	if err := json.Unmarshal(body, &token); err != nil || token.ID == "" {
		return ""
	}
	ttl := time.Duration(token.TTL) * time.Second
	if ttl <= juristwayTokenSlack {
		ttl = 24 * time.Hour
	}
	c.token = token.ID
	c.tokenExpires = time.Now().Add(ttl - juristwayTokenSlack)
	return c.token
}

// dropToken forgets a token the server rejected, so the next search retries
// the login instead of replaying the stale one.
func (c *JuristwayClient) dropToken() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = ""
	c.tokenExpires = time.Time{}
}

// parseJuristwayDocument extracts the case page's identity layer. The page is
// server-rendered around the case metadata with the editorial text in the
// content region; the extraction removes the site chrome defensively and a
// parse that finds nothing still returns the URL-carrying document.
func parseJuristwayDocument(pageURL string, body string) *LegalDocument {
	doc := &LegalDocument{URL: pageURL}
	parsed, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return doc
	}
	title := collapseIndianKanoonSpace(parsed.Find("h1").First().Text())
	if title == "" {
		title = collapseIndianKanoonSpace(parsed.Find("title").First().Text())
	}
	doc.Title = strings.TrimSpace(strings.TrimSuffix(title, "| Juristway"))
	// The citation line carries the court segment the search hits parse too.
	if citation := collapseIndianKanoonSpace(parsed.Find("[class*='citation']").First().Text()); citation != "" {
		doc.Court = parseJuristwayCourt(citation)
	}
	parsed.Find("nav, header, footer, aside, script, style, noscript, form").Remove()
	text := collapseIndianKanoonSpace(parsed.Find("main").First().Text())
	if text == "" {
		text = collapseIndianKanoonSpace(parsed.Find("body").Text())
	}
	doc.Text = capJuristwayText(text, juristwayMaxDocText)
	return doc
}

// juristwayCourtPattern reads the court segment out of a juristway citation:
// "2017-juristway.com-36-HC(Delhi)-GST" names HC(Delhi), the Supreme Court
// cites a bare "SC", tribunals carry their bench in the parentheses.
var juristwayCourtPattern = regexp.MustCompile(`-(SC|HC|ITAT|NCLT|NCLAT|CESTAT|CAT|DRT|AA)(?:\(([^)]+)\))?-.+$`)

// parseJuristwayCourt turns the citation's court segment into a readable
// court name. An unrecognised citation parses to an empty court — the agent
// classifies on title and snippet text anyway.
func parseJuristwayCourt(citation string) string {
	match := juristwayCourtPattern.FindStringSubmatch(strings.TrimSpace(citation))
	if match == nil {
		return ""
	}
	code, place := match[1], strings.TrimSpace(match[2])
	switch code {
	case "SC":
		return "Supreme Court of India"
	case "HC":
		if place == "" {
			return "High Court"
		}
		return strings.Title(strings.ToLower(place)) + " High Court"
	default:
		if place == "" {
			return code
		}
		return code + ", " + place
	}
}

// htmlToJuristwayText flattens the headnote HTML the API returns into visible
// text.
func htmlToJuristwayText(html string) string {
	trimmed := strings.TrimSpace(html)
	if trimmed == "" {
		return ""
	}
	parsed, err := goquery.NewDocumentFromReader(strings.NewReader(trimmed))
	if err != nil {
		return trimmed
	}
	return collapseIndianKanoonSpace(parsed.Text())
}

// capJuristwayText bounds extracted text so one verbose record cannot bloat a
// search page or a document payload.
func capJuristwayText(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}
