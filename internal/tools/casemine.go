package tools

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// casemine.go is the client for casemine.com, an Indian case-law aggregator, the
// second source of legal mode.
//
// Its access policy was measured (October 2026), and the measurement is what the
// two-step fetch ladder encodes:
//
//   - Plain HTTP is answered 403 for non-browser User-Agents (PowerShell's
//     default, curl, and even headless Chromium's default "HeadlessChrome" UA).
//     With the scraper's Chrome UA the same request returns the full
//     server-rendered page — 366 KB homepage, 478 KB search results. The wall is
//     User-Agent-only, so the cheap plain fetch runs first.
//   - If that ever tightens, the fallback renders the page through the shared
//     browser with the jar's cookies bridged in (COOKIE_FILE_PATH), the same
//     session mechanism the hard-URL ladder uses. No cookies are required today.
//
// The result markup (verified against a live search):
//
//	<div class="jdlist …">
//	  <a class="jdlink" href="/judgement/in/<id>"><div class="jd-title …">Title</div></a>
//	  <p class="text-snippet …">…<em>highlight</em>…</p>
//	  <span class="jd-court">Supreme Court Of India</span>
//	  <span class="jd-date">Aug 13, 2010</span>
//	  <span class="jd-cite">2857</span>
//	</div>
//
// The first page carries no pagination markup; a bounded legal check reads the
// first page only.

const (
	defaultCasemineURL = "https://www.casemine.com"
	// casemineWallMinBytes is the size under which a fetched page is treated as
	// a wall document rather than results. The measured wall is 117 bytes; the
	// measured results page is ~478 KB. Anything under a few KB is not a result
	// list.
	casemineWallMinBytes = 4096
	// casemineMaxResultsPerPage is how many results one Casemine page carries,
	// measured on a live search.
	casemineMaxResultsPerPage = 20
)

// casemineDocID matches the judgement paths a result links to:
// /judgement/in/<id> (older links may omit the locale segment).
var casemineDocID = regexp.MustCompile(`(?i)/judgement/(?:[a-z]{2}/)?([0-9a-f]{16,})`)

// CasemineClient searches casemine.com. The two fetchers are injected so the
// client is testable and so the fetch ladder lives in one place.
type CasemineClient struct {
	baseURL    string
	fetchHTML  func(ctx context.Context, targetURL string) (string, error)
	renderHTML func(ctx context.Context, targetURL string) (string, error)
	limiter    *hostLimiter
}

// NewCasemineClient builds the client on a scraper: plain fetches carry the
// scraper's browser-like headers and cookie jar; the render fallback is the
// cookie-bridged shared-browser render.
func NewCasemineClient(scraper *Scraper) *CasemineClient {
	if scraper == nil {
		return &CasemineClient{baseURL: defaultCasemineURL, limiter: newHostLimiter(1.0, 1, 1)}
	}
	return &CasemineClient{
		baseURL:    defaultCasemineURL,
		fetchHTML:  scraper.FetchHTML,
		renderHTML: scraper.RenderHTML,
		limiter:    newHostLimiter(1.0, 1, 1),
	}
}

// Search runs one Casemine search and parses its results. The query is sent as
// typed — quoted party names and field operators compose in the caller — and
// maxResults bounds how many parsed results are returned.
//
// page selects the results page. Measured (October 2026): the site ignores the
// page parameter server-side — &page=2 returns page 1's content unchanged, the
// pagination being JavaScript-driven — so page 1 is the only page this client
// returns, and it carries twenty results. A page above 1 answers empty, which
// is how the caller's page walk terminates.
func (c *CasemineClient) Search(ctx context.Context, query string, page, maxResults int) ([]LegalSearchHit, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, fmt.Errorf("casemine search query is empty")
	}
	if maxResults <= 0 {
		maxResults = casemineMaxResultsPerPage
	}
	if page > 1 {
		return nil, nil
	}

	searchURL := c.baseURL + "/search/in?q=" + queryEscape(trimmed)

	if _, err := c.limiter.acquire(ctx, hostOf(searchURL)); err != nil {
		return nil, err
	}

	// Step 1: plain HTTP with the scraper's browser-like headers and jar.
	body, fetchErr := c.fetchHTML(ctx, searchURL)

	// Step 2: a wall (403-shaped body, or suspiciously short) falls back to the
	// cookie-bridged browser render — the session a user exported travels with
	// the render, which is what the plain path cannot always offer.
	if fetchErr != nil || casemineLooksBlocked(body) {
		if c.renderHTML == nil {
			if fetchErr != nil {
				return nil, fetchErr
			}
			return nil, fmt.Errorf("casemine returned a wall and no render fallback is configured")
		}
		rendered, renderErr := c.renderHTML(ctx, searchURL)
		if renderErr != nil {
			if fetchErr != nil {
				return nil, fmt.Errorf("casemine plain fetch failed (%v) and the render fallback failed too (%w)", fetchErr, renderErr)
			}
			return nil, fmt.Errorf("casemine plain fetch returned a wall and the render fallback failed: %w", renderErr)
		}
		if casemineLooksBlocked(rendered) {
			return nil, fmt.Errorf("casemine is bot-walled even for the cookie-bridged render")
		}
		body = rendered
	}

	return parseCasemineResults(body, maxResults), nil
}

// casemineLooksBlocked reports whether a fetched page is a wall document rather
// than a result list. The measured wall is a 117-byte nginx 403; anything under
// casemineWallMinBytes, or carrying its title, is treated the same way.
func casemineLooksBlocked(html string) bool {
	if len(html) < casemineWallMinBytes {
		return true
	}
	return strings.Contains(html, "403 Forbidden")
}

// parseCasemineResults walks the jdlist cards and fills LegalSearchHit rows with
// the verified selectors: the jdlink href and jd-title, the text-snippet, and
// the jd-court / jd-date / jd-cite metadata spans.
func parseCasemineResults(body string, maxResults int) []LegalSearchHit {
	parsed, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil
	}

	hits := make([]LegalSearchHit, 0, maxResults)
	parsed.Find("div.jdlist").EachWithBreak(func(_ int, item *goquery.Selection) bool {
		if len(hits) >= maxResults {
			return false
		}

		link := item.Find("a.jdlink").First()
		href, hasHref := link.Attr("href")
		title := strings.TrimSpace(item.Find(".jd-title").First().Text())
		if !hasHref || title == "" {
			return true
		}

		hit := LegalSearchHit{Title: title}
		if match := casemineDocID.FindStringSubmatch(href); match != nil {
			hit.DocID = match[1]
			hit.DocURL = "/judgement/in/" + match[1]
		} else {
			hit.DocURL = href
		}

		hit.Snippet = collapseIndianKanoonSpace(item.Find(".text-snippet").First().Text())
		hit.Court = collapseIndianKanoonSpace(item.Find(".jd-court").First().Text())
		hit.Date = collapseIndianKanoonSpace(item.Find(".jd-date").First().Text())
		if cited := collapseIndianKanoonSpace(item.Find(".jd-cite").First().Text()); cited != "" {
			if value, err := strconv.Atoi(cited); err == nil {
				hit.CitedBy = value
			}
		}

		hits = append(hits, hit)
		return true
	})
	return hits
}

// queryEscape escapes a query for a query-string parameter, preserving spaces as
// + so a quoted party search stays one parameter value.
func queryEscape(value string) string {
	replacer := strings.NewReplacer(
		"+", "%2B", " ", "+", "\"", "%22", "#", "%23", "&", "%26", "?", "%3F", "%", "%25",
	)
	return replacer.Replace(strings.TrimSpace(value))
}
