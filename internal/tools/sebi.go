package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// sebi.go is the client for sebi.gov.in — the securities-enforcement layer of
// the research corpus. SEBI's adjudication orders routinely name audit firms
// and chartered accountants as parties, which is what makes it a practice
// source and not just a markets source.
//
// Measured (October 2026):
//
//   - Plain HTTP with the scraper's Chrome UA answers 200; robots.txt allows
//     everything but /js and /css.
//   - The orders URL pattern is /enforcement/orders/<mon-yyyy>/<slug>_<id>.html
//     — the slug carries the order title in words, and the URL segment carries
//     the month-year — discovered through the site's own sitemap.xml (364
//     URLs, which also cover recovery proceedings and orders-of-courts).
//   - Order pages are server-rendered; <title> reads "SEBI | <order title>".
//
// The index therefore comes from the sitemap — a complete, site-published
// listing that never breaks when a search page changes — filtered to the
// enforcement/orders family. One request a second.

const (
	defaultSEBIURL        = "https://www.sebi.gov.in"
	defaultSEBISitemapURL = "https://www.sebi.gov.in/sitemap.xml"
	defaultSEBISearchPath = "/sebiweb/ajax/search/section-search.jsp"
)

// SEBIClient reads SEBI's enforcement-order index.
type SEBIClient struct {
	baseURL    string // overridable in tests
	sitemapURL string
	fetchHTML  func(ctx context.Context, targetURL string) (string, error)
	postForm   func(ctx context.Context, targetURL string, form url.Values) (string, error)
	limiter    *hostLimiter
}

// NewSEBIClient builds the client on a scraper's plain-HTML fetch.
func NewSEBIClient(scraper *Scraper) *SEBIClient {
	if scraper == nil {
		return &SEBIClient{baseURL: defaultSEBIURL, sitemapURL: defaultSEBISitemapURL, postForm: postSEBIForm, limiter: newHostLimiter(1.0, 1, 1)}
	}
	return &SEBIClient{
		baseURL:    defaultSEBIURL,
		sitemapURL: defaultSEBISitemapURL,
		fetchHTML:  scraper.FetchHTML,
		postForm:   postSEBIForm,
		limiter:    newHostLimiter(1.0, 1, 1),
	}
}

// SearchOrders uses SEBI's official Orders search endpoint. The browser page
// is only a form; results are returned by this POST endpoint.
func (c *SEBIClient) SearchOrders(ctx context.Context, term, fromDate, toDate string, maxResults int) ([]SourceDoc, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, fmt.Errorf("sebi order search term is empty")
	}
	if maxResults <= 0 {
		maxResults = 20
	}
	if c.postForm == nil {
		return nil, fmt.Errorf("sebi order search is not configured")
	}
	release, err := c.limiter.acquire(ctx, hostOf(c.baseURL))
	if err != nil {
		return nil, err
	}
	defer release()
	body, err := c.postForm(ctx, c.baseURL+defaultSEBISearchPath, url.Values{
		"next_value": {"1"},
		"searchval":  {base64.StdEncoding.EncodeToString([]byte(term))},
		"fromDate":   {strings.TrimSpace(fromDate)},
		"toDate":     {strings.TrimSpace(toDate)},
		"fval":       {"Orders"},
		"typeSearch": {"1"},
		"next":       {"s"},
		"doDirect":   {"-1"},
		"sortby":     {"1"},
	})
	if err != nil {
		return nil, err
	}
	return parseSEBIOrderSearch(body, maxResults), nil
}

// OrderIndex returns the enforcement-order entries from the site's sitemap,
// filtered to the orders family. The title is reconstructed from the URL slug
// (SEBI writes readable slugs); the order page's own <title> is the same text
// and can be confirmed later when a memo fetches the full order.
func (c *SEBIClient) OrderIndex(ctx context.Context) ([]SourceDoc, error) {
	release, err := c.limiter.acquire(ctx, hostOf(c.sitemapURL))
	if err != nil {
		return nil, err
	}
	defer release()
	body, err := c.fetchHTML(ctx, c.sitemapURL)
	if err != nil {
		return nil, err
	}
	return parseSEBISitemap(body), nil
}

// Order fetches one order page and extracts its title, date and document
// links (SEBI serves the order as an HTML page and often a signed PDF).
func (c *SEBIClient) Order(ctx context.Context, orderURL string) (*SourceDoc, string, error) {
	trimmed := strings.TrimSpace(orderURL)
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		trimmed = c.baseURL + "/" + strings.TrimPrefix(trimmed, "/")
	}
	if !strings.Contains(trimmed, "/enforcement/orders/") {
		return nil, "", fmt.Errorf("url is not a sebi enforcement order: %s", orderURL)
	}
	release, err := c.limiter.acquire(ctx, hostOf(trimmed))
	if err != nil {
		return nil, "", err
	}
	defer release()
	body, err := c.fetchHTML(ctx, trimmed)
	if err != nil {
		return nil, "", err
	}

	parsed, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	doc := &SourceDoc{URL: trimmed, Source: "sebi", Category: "orders"}
	doc.Title = collapseIndianKanoonSpace(parsed.Find("h1").First().Text())
	if doc.Title == "" {
		titleText := collapseIndianKanoonSpace(parsed.Find("title").First().Text())
		doc.Title = strings.TrimSpace(strings.TrimPrefix(titleText, "SEBI |"))
	}
	doc.Date = collapseIndianKanoonSpace(parsed.Find(".order-date, .date").First().Text())
	var pdfURL string
	parsed.Find("a[href$='.pdf'], a[href*='.pdf?']").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		if href, exists := a.Attr("href"); exists {
			pdfURL = normalizeDocURL(c.baseURL, href)
			return false
		}
		return true
	})
	doc.PDFURL = pdfURL
	return doc, body, nil
}

// parseSEBISitemap extracts the enforcement-order entries from a sitemap body.
// Sitemaps list absolute URLs; the slug after the month-year segment is the
// order title, and the month-year segment becomes the entry's date.
func parseSEBISitemap(body string) []SourceDoc {
	parsed, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil
	}

	var docs []SourceDoc
	seen := map[string]bool{}
	parsed.Find("loc").Each(func(_ int, loc *goquery.Selection) {
		raw := strings.TrimSpace(loc.Text())
		if !strings.Contains(raw, "/enforcement/orders/") {
			return
		}
		if seen[raw] {
			return
		}
		seen[raw] = true
		docs = append(docs, sebiOrderFromURL(raw))
	})
	return docs
}

// sebiOrderFromURL turns one orders-family URL into an entry: the slug minus
// the trailing _<id> is the title, the mon-yyyy segment the date.
func sebiOrderFromURL(raw string) SourceDoc {
	// .../enforcement/orders/sep-2025/adjudication-order-..._96425.html
	path := raw
	if idx := strings.Index(raw, "/enforcement/orders/"); idx >= 0 {
		path = raw[idx+len("/enforcement/orders/"):]
	}
	path = strings.TrimSuffix(path, ".html")
	parts := strings.Split(path, "/")

	date := ""
	slugParts := parts
	if len(parts) > 1 {
		date = strings.ReplaceAll(parts[0], "-", " ")
		slugParts = parts[1:]
	}
	slug := strings.Join(slugParts, "/")
	if lastDash := strings.LastIndex(slug, "_"); lastDash >= 0 {
		slug = slug[:lastDash]
	}
	title := strings.Title(strings.ReplaceAll(slug, "-", " "))
	return SourceDoc{Title: title, URL: raw, Date: date, Source: "sebi", Category: "orders"}
}

func parseSEBIOrderSearch(body string, maxResults int) []SourceDoc {
	parsed, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil
	}
	docs := make([]SourceDoc, 0, maxResults)
	seen := map[string]bool{}
	parsed.Find("a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href, ok := a.Attr("href")
		if !ok {
			return true
		}
		absolute := normalizeDocURL(defaultSEBIURL, href)
		if !strings.Contains(absolute, "/enforcement/orders/") || seen[absolute] {
			return true
		}
		seen[absolute] = true
		docs = append(docs, SourceDoc{
			Title:    collapseIndianKanoonSpace(a.Text()),
			URL:      absolute,
			Source:   "sebi",
			Category: "orders",
		})
		return len(docs) < maxResults
	})
	return docs
}

func postSEBIForm(ctx context.Context, targetURL string, form url.Values) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html, */*; q=0.01")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sebi order search returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
