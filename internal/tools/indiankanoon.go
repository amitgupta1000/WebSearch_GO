package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// indiankanoon.go is the client for indiankanoon.org, India's free case-law
// index, and the primary source of legal mode.
//
// Everything here was pinned against the live site (October 2026), not guessed:
//
//   - Search is server-rendered HTML at /search/?formInput=<query>; the results
//     list holds <article class="result"> items with <h4 class="result_title">,
//     a <div class="headline"> snippet and a <div class="hlbottom"> footer whose
//     <span class="docsource"> carries the court (judgments) or a statute label.
//   - Judgment title links point at /docfragment/<id>/, statute sections at
//     /doc/<id>/; both shapes carry the numeric id.
//   - The query string accepts operators inside formInput (sortby:mostrecent,
//     year:<yyyy>, cites:<id>, citedby:<id>) and a doctype=judgments URL filter,
//     which is what keeps statute sections out of a party search.
//   - Document pages render the judgment inside <div class="judgments">, with
//     <div class="doc_title"> and <div class="docsource_main"> around it.
//
// The site is a free public good, so the client is deliberately slower than the
// scraper: one request a second, one in flight, plus a robots.txt guard that
// refuses the specific /doc/ paths the site disallows (case-removal orders).

const (
	defaultIndianKanoonURL = "https://indiankanoon.org"
	// indianKanoonMaxAttempts is one retry: the engine's run budget bounds the
	// run, and a free index is not owed a backoff ladder.
	indianKanoonMaxAttempts  = 2
	indianKanoonBaseRetry    = 500 * time.Millisecond
	indianKanoonMaxErrorBody = 512
	// indianKanoonMaxDocBytes caps a document page's body. A judgment runs to
	// a few hundred KB of HTML; anything beyond is not a document this mode can
	// use within its budget.
	indianKanoonMaxDocBytes = 3 << 20
)

// LegalSearchHit is one parsed result from an IndianKanoon search page. The
// identity fields (Petitioner, Respondent, CaseNumber) are read out of the
// structured metadata the result headline carries for judgments, and they are
// what makes party matching stronger than title text alone.
type LegalSearchHit struct {
	Title  string `json:"title"`
	DocID  string `json:"doc_id,omitempty"`
	DocURL string `json:"doc_url,omitempty"`
	// DocType is "judgment" or "statute". Statutes are dropped by the agent;
	// they are recorded here so the trace shows what the filter removed.
	DocType    string `json:"doc_type,omitempty"`
	Court      string `json:"court,omitempty"`
	Date       string `json:"date,omitempty"`
	CaseNumber string `json:"case_number,omitempty"`
	Petitioner string `json:"petitioner,omitempty"`
	Respondent string `json:"respondent,omitempty"`
	Snippet    string `json:"snippet,omitempty"`
	CitedBy    int    `json:"cited_by,omitempty"`
}

// LegalDocument is one fetched source document: the judgment text the identity
// pass reads when a snippet is not enough to tie a case to the party.
type LegalDocument struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
	Court string `json:"court,omitempty"`
	Text  string `json:"text,omitempty"`
}

// IndianKanoonClient searches indiankanoon.org and fetches its documents.
type IndianKanoonClient struct {
	baseURL    string // overridable in tests
	httpClient *http.Client
	limiter    *hostLimiter
	robots     *robotsGuard
}

// NewIndianKanoonClient creates the client with its politeness budget: one
// request a second, burst of one, one in flight. The robots guard loads on
// first document fetch and fails open (the site's disallows are case removals;
// an unreadable robots.txt must not silently blind the mode).
func NewIndianKanoonClient() *IndianKanoonClient {
	return &IndianKanoonClient{
		baseURL:    defaultIndianKanoonURL,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		limiter:    newHostLimiter(1.0, 1, 1),
		robots:     newRobotsGuard(defaultIndianKanoonURL + "/robots.txt"),
	}
}

var indianKanoonDocID = regexp.MustCompile(`(?i)/doc(?:fragment)?/(\d+)/`)

// Search runs one IndianKanoon search and parses its results page. The query
// is sent as typed — the agent composes operators (sortby, year, quoted party
// names) — and doctype=judgments is always appended, because a party search
// without it answers with statute sections before any case that names the
// party. page selects the results page (the site's own &pagenum= parameter,
// verified live); page 1 is the first page. maxResults bounds how many parsed
// results are returned, not how many the page holds.
func (c *IndianKanoonClient) Search(ctx context.Context, query string, page, maxResults int) ([]LegalSearchHit, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, fmt.Errorf("indiankanoon search query is empty")
	}
	if maxResults <= 0 {
		maxResults = 10
	}
	if page <= 0 {
		page = 1
	}

	searchURL := c.baseURL + "/search/?formInput=" + url.QueryEscape(trimmed) + "&doctype=judgments"
	if page > 1 {
		searchURL += "&pagenum=" + strconv.Itoa(page)
	}
	body, err := c.fetch(ctx, searchURL)
	if err != nil {
		return nil, err
	}
	return parseIndianKanoonResults(body, maxResults), nil
}

// FetchDocument downloads one judgment document and returns its text. The URL
// may be the /docfragment/ link a search page carries; it is normalised to the
// /doc/ form. A path the site's robots.txt disallows is refused by name.
func (c *IndianKanoonClient) FetchDocument(ctx context.Context, docURL string) (*LegalDocument, error) {
	trimmed := strings.TrimSpace(docURL)
	if trimmed == "" {
		return nil, fmt.Errorf("indiankanoon document url is empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Path == "" {
		return nil, fmt.Errorf("indiankanoon document url is not a url: %q", docURL)
	}
	docID := indianKanoonDocID.FindStringSubmatch(parsed.Path)
	if docID == nil {
		return nil, fmt.Errorf("indiankanoon url does not carry a document id: %q", docURL)
	}
	if c.robots != nil && !c.robots.allows("/doc/"+docID[1]+"/") {
		return nil, fmt.Errorf("indiankanoon document %s is excluded by robots.txt", docID[1])
	}

	canonical := c.baseURL + "/doc/" + docID[1] + "/"
	body, err := c.fetch(ctx, canonical)
	if err != nil {
		return nil, err
	}
	return parseIndianKanoonDocument(canonical, body), nil
}

// fetch performs one rate-limited GET with a single retry on transient
// failures, mirroring the Serper client's error discipline.
func (c *IndianKanoonClient) fetch(ctx context.Context, target string) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= indianKanoonMaxAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(indianKanoonBaseRetry * time.Duration(attempt-1)):
			}
		}

		release, err := c.limiter.acquire(ctx, hostOf(target))
		if err != nil {
			return nil, err
		}
		defer release()
		body, err := c.attempt(ctx, target)
		if err == nil {
			return body, nil
		}
		lastErr = err

		var retriable *retriableError
		if !errors.As(err, &retriable) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("indiankanoon request failed after %d attempts: %w", indianKanoonMaxAttempts, lastErr)
}

// attempt performs a single GET, reading at most indianKanoonMaxDocBytes.
func (c *IndianKanoonClient) attempt(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create indiankanoon request: %w", err)
	}
	req.Header.Set("User-Agent", "WebSearch_GO research agent (+https://github.com/amitgupta1000/WebSearch_GO)")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &retriableError{fmt.Errorf("indiankanoon request failed: %w", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, indianKanoonMaxErrorBody))
		err := fmt.Errorf("indiankanoon returned status code %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, &retriableError{err}
		}
		return nil, err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, indianKanoonMaxDocBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read indiankanoon response: %w", err)
	}
	return body, nil
}

// parseIndianKanoonResults parses one search results page into hits. It walks
// <article class="result"> items and reads the verified shape: the title link
// carries the document id, <div class="headline"> the snippet (often with the
// judgment's PETITIONER/RESPONDENT/CASE NO. metadata), and
// <div class="hlbottom"> the court and citation counts.
func parseIndianKanoonResults(body []byte, maxResults int) []LegalSearchHit {
	parsed, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil
	}

	hits := make([]LegalSearchHit, 0, maxResults)
	parsed.Find("article.result").EachWithBreak(func(_ int, item *goquery.Selection) bool {
		if len(hits) >= maxResults {
			return false
		}

		// The first /doc link inside the title is the result itself; a statute
		// entry's second link ("Entire Act") must not take its place.
		var linkHref, linkText string
		item.Find("h4.result_title a").EachWithBreak(func(_ int, a *goquery.Selection) bool {
			href, exists := a.Attr("href")
			if exists && indianKanoonDocID.MatchString(href) {
				linkHref, linkText = href, strings.TrimSpace(a.Text())
				return false
			}
			return true
		})
		if linkHref == "" {
			return true
		}

		hit := LegalSearchHit{Title: linkText}
		if match := indianKanoonDocID.FindStringSubmatch(linkHref); match != nil {
			hit.DocID = match[1]
			hit.DocURL = "/doc/" + match[1] + "/"
		}

		hit.Court = collapseIndianKanoonSpace(item.Find(".hlbottom .docsource").First().Text())
		hit.Snippet = collapseIndianKanoonSpace(item.Find(".headline").First().Text())
		hit.DocType = indianKanoonDocType(hit, item)
		hit.CitedBy = indianKanoonCitedBy(item)
		indianKanoonHeadlineMetadata(hit.Snippet, &hit)
		indianKanoonTitleDate(hit.Title, &hit)

		hits = append(hits, hit)
		return true
	})
	return hits
}

// indianKanoonDocType classifies the result: a statute section announces
// itself ("Section N in The … Act", an "[Entire Act]" companion link, or a
// docsource of "Union of India - Section"); everything else is a judgment.
func indianKanoonDocType(hit LegalSearchHit, item *goquery.Selection) string {
	lowered := strings.ToLower(hit.Title)
	if strings.HasPrefix(lowered, "section ") && strings.Contains(lowered, " in the ") {
		return "statute"
	}
	docsource := strings.ToLower(hit.Court)
	if strings.Contains(docsource, " - section") || strings.Contains(docsource, "constitution") {
		return "statute"
	}
	entireAct := false
	item.Find("h4.result_title a").Each(func(_ int, a *goquery.Selection) {
		if strings.EqualFold(strings.TrimSpace(a.Text()), "entire act") {
			entireAct = true
		}
	})
	if entireAct {
		return "statute"
	}
	return "judgment"
}

var indianKanoonCitedByTag = regexp.MustCompile(`(?i)cited by (\d+)`)

// indianKanoonCitedBy reads the "Cited by N" tag, which is how much case law
// leans on the result — a weak but real relevance signal.
func indianKanoonCitedBy(item *goquery.Selection) int {
	cited := 0
	item.Find(".hlbottom a.cite_tag").Each(func(_ int, tag *goquery.Selection) {
		if match := indianKanoonCitedByTag.FindStringSubmatch(strings.TrimSpace(tag.Text())); match != nil {
			if value, err := strconv.Atoi(match[1]); err == nil {
				cited = value
			}
		}
	})
	return cited
}

var (
	indianKanoonPetitioner   = regexp.MustCompile(`(?is)PETITIONER:\s*(.*?)(?:RESPONDENT:|DATE OF JUDGMENT:|BENCH|$)`)
	indianKanoonRespondent   = regexp.MustCompile(`(?is)RESPONDENT:\s*(.*?)(?:DATE OF JUDGMENT:|BENCH|PETITIONER:|$)`)
	indianKanoonCaseNumber   = regexp.MustCompile(`(?is)CASE NO\.?:?\s*(.{0,120}?)(?:PETITIONER:|RESPONDENT:|DATE OF JUDGMENT:|BENCH|$)`)
	indianKanoonHeadlineDate = regexp.MustCompile(`(?is)DATE OF JUDGMENT:\s*([0-9]{2}/[0-9]{2}/[0-9]{4})`)
	indianKanoonTitleDateOn  = regexp.MustCompile(`(?i)\bon\s+(.+?)\s*$`)
)

// indianKanoonHeadlineMetadata reads the structured block some judgment
// headlines carry (CASE NO., PETITIONER, RESPONDENT, DATE OF JUDGMENT). These
// are the exact party spellings the identity pass prefers over title text.
func indianKanoonHeadlineMetadata(snippet string, hit *LegalSearchHit) {
	if match := indianKanoonPetitioner.FindStringSubmatch(snippet); match != nil {
		hit.Petitioner = collapseIndianKanoonSpace(match[1])
	}
	if match := indianKanoonRespondent.FindStringSubmatch(snippet); match != nil {
		hit.Respondent = collapseIndianKanoonSpace(match[1])
	}
	if match := indianKanoonCaseNumber.FindStringSubmatch(snippet); match != nil {
		hit.CaseNumber = collapseIndianKanoonSpace(match[1])
	}
	if hit.Date == "" {
		if match := indianKanoonHeadlineDate.FindStringSubmatch(snippet); match != nil {
			hit.Date = match[1]
		}
	}
}

// indianKanoonTitleDate reads the "… on 5 November, 2004" suffix a judgment
// title carries.
func indianKanoonTitleDate(title string, hit *LegalSearchHit) {
	if hit.Date != "" {
		return
	}
	if match := indianKanoonTitleDateOn.FindStringSubmatch(title); match != nil {
		date := strings.TrimSpace(match[1])
		if date != "" && len(date) <= 40 {
			hit.Date = date
		}
	}
}

// parseIndianKanoonDocument extracts a judgment document's text: the court
// from <div class="docsource_main">, the title from <div class="doc_title">
// (falling back to the page <title>) and the judgment body from
// <div class="judgments">.
func parseIndianKanoonDocument(docURL string, body []byte) *LegalDocument {
	parsed, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return &LegalDocument{URL: docURL}
	}

	document := &LegalDocument{URL: docURL}
	document.Court = collapseIndianKanoonSpace(parsed.Find(".docsource_main").First().Text())
	document.Title = collapseIndianKanoonSpace(parsed.Find(".doc_title").First().Text())
	if document.Title == "" {
		document.Title = collapseIndianKanoonSpace(parsed.Find("title").First().Text())
	}

	content := parsed.Find("div.judgments").First()
	if content.Length() == 0 {
		content = parsed.Find("body")
	}
	document.Text = collapseIndianKanoonSpace(content.Text())
	return document
}

// collapseIndianKanoonSpace flattens extracted HTML text into single-spaced
// prose, the same readability the scraper's text extractor provides.
func collapseIndianKanoonSpace(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}
