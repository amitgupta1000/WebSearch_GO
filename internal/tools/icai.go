package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// icai.go is the client for icai.org — the Institute of Chartered Accountants
// of India, the professional-body layer of the research corpus: guidance
// notes, standards, opinions and disciplinary matters.
//
// Measured (October 2026):
//
//   - The site is server-rendered classic pages with no sitemap.xml (404), so
//     the discovery surface is the /category/<slug> archive pages the site's
//     own navigation links: /category/disciplinary,
//     /category/accounting-standards and /category/guidance-notes were
//     verified live (200, 50–93 KB). The full slug list is enumerated from
//     the homepage nav at build-probe time; this client takes slugs as
//     parameters rather than guessing them.
//   - robots.txt is absent — paths are treated as allowed by default, with
//     the client's own politeness (one request a second) doing the restraint.
//
// Category pages mix navigation links with the category's actual document
// links; the parser excludes the known navigation slugs and keeps entries
// with real anchor text, best-effort rather than over-fitted to one page
// generation.

const defaultICAIURL = "https://www.icai.org"

// ICAIClient reads ICAI's category archives.
type ICAIClient struct {
	baseURL   string // overridable in tests
	fetchHTML func(ctx context.Context, targetURL string) (string, error)
	limiter   *hostLimiter
}

// NewICAINoScraper builds a client without a scraper (tests); production uses
// NewICAIClient.
func NewICAIClient(scraper *Scraper) *ICAIClient {
	if scraper == nil {
		return &ICAIClient{baseURL: defaultICAIURL, limiter: newHostLimiter(1.0, 1, 1)}
	}
	return &ICAIClient{
		baseURL:   defaultICAIURL,
		fetchHTML: scraper.FetchHTML,
		limiter:   newHostLimiter(1.0, 1, 1),
	}
}

// icaiNavSlugs are the site's own navigation categories. Links to these are
// menus, not documents of the requested category, and are excluded from
// listings. The live integration test is what keeps this list current.
var icaiNavSlugs = map[string]bool{
	"overview": true, "about-icai": true, "the-council": true, "network": true,
	"key-statistics": true, "right-to-information-act": true, "annual-report": true,
	"year-books-icai": true, "committees": true, "standing": true, "others": true,
	"secretary-to-the-committee": true, "courses-offered-by-icai": true,
	"empanelment": true, "examination": true, "funds-awards": true, "members-faqs": true,
	"placements": true, "regulatory": true, "students-announcements": true,
}

// Category fetches one ICAI category archive and returns the document links it
// lists: pages and PDFs with real anchor text, navigation excluded.
func (c *ICAIClient) Category(ctx context.Context, slug string) ([]SourceDoc, error) {
	trimmed := strings.Trim(strings.TrimSpace(slug), "/")
	if trimmed == "" {
		return nil, fmt.Errorf("icai category slug is empty")
	}
	categoryURL := c.baseURL + "/category/" + trimmed + "/"
	if _, err := c.limiter.acquire(ctx, hostOf(categoryURL)); err != nil {
		return nil, err
	}
	body, err := c.fetchHTML(ctx, categoryURL)
	if err != nil {
		return nil, err
	}
	return parseICAICategory(c.baseURL, trimmed, body), nil
}

// parseICAICategory extracts document entries from one category page. Entries
// are links with real anchor text whose target is not a navigation category;
// the anchor text is the title. Relative and absolute forms normalize to
// absolute so dedupe and memo citations work either way.
func parseICAICategory(baseURL, category string, body string) []SourceDoc {
	parsed, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil
	}

	var docs []SourceDoc
	seen := map[string]bool{}
	parsed.Find("a").Each(func(_ int, a *goquery.Selection) {
		href, exists := a.Attr("href")
		if !exists {
			return
		}
		absolute := normalizeDocURL(baseURL, href)
		title := collapseIndianKanoonSpace(a.Text())
		if title == "" || len([]rune(title)) < 8 {
			return
		}
		// Navigation categories carry their slug in the URL and no document
		// payload; the category's own documents link elsewhere (posts, PDFs,
		// external files).
		if strings.Contains(absolute, "/category/") {
			return
		}
		if seen[absolute] {
			return
		}
		seen[absolute] = true

		doc := SourceDoc{Title: title, URL: absolute, Source: "icai", Category: category}
		if strings.Contains(strings.ToLower(absolute), ".pdf") {
			doc.PDFURL = absolute
			doc.URL = ""
		}
		docs = append(docs, doc)
	})
	return docs
}
