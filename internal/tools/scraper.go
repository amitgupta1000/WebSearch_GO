package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// Scraper fetches and extracts clean text from webpages.
type Scraper struct {
	client *http.Client
}

// NewScraper initializes a high-performance HTTP scraper with cookie support.
func NewScraper(jar *cookiejar.Jar, timeout time.Duration) *Scraper {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	return &Scraper{client: client}
}

// ScrapeURL fetches a target URL, strips non-content tags, and returns sanitized text.
func (s *Scraper) ScrapeURL(ctx context.Context, targetURL string) (*models.ScrapeResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to construct request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scraping request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scraping returned status code: %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML document: %w", err)
	}

	title := strings.TrimSpace(doc.Find("title").Text())

	// Strip noise and non-content elements
	doc.Find("script, style, noscript, nav, footer, header, svg, form, iframe, button, [role='navigation'], [role='banner']").Remove()

	var builder strings.Builder
	doc.Find("h1, h2, h3, h4, p, li, article, section").Each(func(_ int, sel *goquery.Selection) {
		text := strings.TrimSpace(sel.Text())
		// Filter out tiny snippets / noise
		if len(text) > 25 {
			builder.WriteString(text)
			builder.WriteString("\n\n")
		}
	})

	content := strings.TrimSpace(builder.String())
	if len(content) == 0 {
		// Fallback to body text if headings/paragraphs were sparse
		content = strings.TrimSpace(doc.Find("body").Text())
	}

	// Trim overly massive content to avoid token blowups (e.g. max ~40KB per page)
	if len(content) > 40000 {
		content = content[:40000] + "\n\n...[content truncated]..."
	}

	return &models.ScrapeResult{
		URL:       targetURL,
		Title:     title,
		Content:   content,
		ByteSize:  len(content),
		FetchedAt: time.Now(),
	}, nil
}
