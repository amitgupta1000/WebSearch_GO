package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// nfra.go is the client for nfra.gov.in — the National Financial Reporting
// Authority, the audit-enforcement layer of the research corpus.
//
// Measured (October 2026):
//
//   - The site is a NIC-hosted WordPress with order documents published as
//     PDFs on a S3WaaS CDN (cdnbbsr.s3waas.gov.in). The orders archive lives
//     at /document-category/orders/ (WordPress document-category pattern,
//     /page/2/ for continuations), and robots.txt disallows only /wp-admin/.
//   - The archive page lists document links (order pages and direct PDFs)
//     alongside the site navigation, which itself carries document links —
//     so the parser distinguishes listing entries from navigation by scope.
//
// The client fetches and parses the archive; it does not fetch the PDFs (the
// engine's document pipeline already extracts PDF text when a memo needs the
// full order). One request a second: it is a government publication site, and
// nothing here justifies faster.

const (
	defaultNFRAURL        = "https://nfra.gov.in"
	defaultNFRAOrdersPath = "/document-category/orders/"
)

// NFRAClient reads the NFRA orders archive.
type NFRAClient struct {
	baseURL   string // overridable in tests
	fetchHTML func(ctx context.Context, targetURL string) (string, error)
	limiter   *hostLimiter
	page      int // how many archive pages to walk; overridable in tests
}

// NewNFRAClient builds the client on a scraper's plain-HTML fetch.
func NewNFRAClient(scraper *Scraper, pages int) *NFRAClient {
	if pages <= 0 {
		pages = 3
	}
	if scraper == nil {
		return &NFRAClient{baseURL: defaultNFRAURL, limiter: newHostLimiter(1.0, 1, 1), page: pages}
	}
	return &NFRAClient{
		baseURL:   defaultNFRAURL,
		fetchHTML: scraper.FetchHTML,
		limiter:   newHostLimiter(1.0, 1, 1),
		page:      pages,
	}
}

// Orders walks the orders archive and returns every document entry it finds:
// direct PDF links (most orders) and /document/ pages, with the title text the
// listing carries. Navigation links are excluded — the archive page's menus
// also link PDFs, and an enforcement index must not list a valuation-methodology
// report because the menu does.
func (c *NFRAClient) Orders(ctx context.Context) ([]SourceDoc, error) {
	var docs []SourceDoc
	seen := map[string]bool{}

	for page := 1; page <= c.page; page++ {
		archiveURL := c.baseURL + defaultNFRAOrdersPath
		if page > 1 {
			archiveURL = fmt.Sprintf("%s%s%d/", c.baseURL, defaultNFRAOrdersPath, page)
		}
		if _, err := c.limiter.acquire(ctx, hostOf(archiveURL)); err != nil {
			return nil, err
		}
		body, err := c.fetchHTML(ctx, archiveURL)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			// A later archive page failing does not undo earlier pages.
			break
		}
		pageDocs := parseNFRAOrders(c.baseURL, body)
		added := 0
		for _, doc := range pageDocs {
			// Empty strings are never keys: a PDF-only entry legitimately
			// carries no URL, and treating "" as a key would drop every entry
			// after the first one.
			if doc.URL != "" && seen[doc.URL] {
				continue
			}
			if doc.PDFURL != "" && seen[doc.PDFURL] {
				continue
			}
			if doc.URL != "" {
				seen[doc.URL] = true
			}
			if doc.PDFURL != "" {
				seen[doc.PDFURL] = true
			}
			docs = append(docs, doc)
			added++
		}
		if added == 0 {
			// An empty archive page is the last page.
			break
		}
	}
	return docs, nil
}

// parseNFRAOrders extracts document entries from one archive page. WordPress
// document listings link the order PDFs (S3WaaS CDN) and /document/ pages with
// the title as the anchor text; entries are taken from the listing content
// area, and any link also present in the navigation is dropped.
func parseNFRAOrders(baseURL string, body string) []SourceDoc {
	parsed, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil
	}

	// Navigation URLs live in the menus; remembering them lets the entry scan
	// below skip links that are menu items rather than listing entries.
	navURLs := map[string]bool{}
	parsed.Find("nav a, .menu-item a, ul.nav a").Each(func(_ int, a *goquery.Selection) {
		if href, exists := a.Attr("href"); exists {
			navURLs[normalizeDocURL(baseURL, href)] = true
		}
	})

	var docs []SourceDoc
	seen := map[string]bool{}
	parsed.Find("a").Each(func(_ int, a *goquery.Selection) {
		href, exists := a.Attr("href")
		if !exists {
			return
		}
		absolute := normalizeDocURL(baseURL, href)
		title := collapseIndianKanoonSpace(a.Text())
		if title == "" {
			return
		}

		isPDF := strings.Contains(strings.ToLower(absolute), ".pdf")
		isDocPage := strings.Contains(absolute, "/document/")
		if !isPDF && !isDocPage {
			return
		}
		if navURLs[absolute] || seen[absolute] {
			return
		}
		seen[absolute] = true

		doc := SourceDoc{Title: title, Source: "nfra", Category: "orders"}
		if isPDF {
			doc.PDFURL = absolute
		} else {
			doc.URL = absolute
		}
		docs = append(docs, doc)
	})
	return docs
}

// normalizeDocURL resolves a listing href against the site base, so entries
// compare consistently whether the archive wrote relative or absolute links.
func normalizeDocURL(baseURL, href string) string {
	trimmed := strings.TrimSpace(href)
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	return strings.TrimSuffix(baseURL, "/") + "/" + strings.TrimPrefix(trimmed, "/")
}
