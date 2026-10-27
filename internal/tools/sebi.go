package tools

import (
	"context"
	"fmt"
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
)

// SEBIClient reads SEBI's enforcement-order index.
type SEBIClient struct {
	baseURL    string // overridable in tests
	sitemapURL string
	fetchHTML  func(ctx context.Context, targetURL string) (string, error)
	limiter    *hostLimiter
}

// NewSEBIClient builds the client on a scraper's plain-HTML fetch.
func NewSEBIClient(scraper *Scraper) *SEBIClient {
	if scraper == nil {
		return &SEBIClient{baseURL: defaultSEBIURL, sitemapURL: defaultSEBISitemapURL, limiter: newHostLimiter(1.0, 1, 1)}
	}
	return &SEBIClient{
		baseURL:    defaultSEBIURL,
		sitemapURL: defaultSEBISitemapURL,
		fetchHTML:  scraper.FetchHTML,
		limiter:    newHostLimiter(1.0, 1, 1),
	}
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
