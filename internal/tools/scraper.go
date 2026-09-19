package tools

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

const (
	defaultScraperTimeout = 15 * time.Second
	maxRedirects          = 5
	// maxBodyBytes caps the downloaded HTML size before parsing to avoid
	// memory blowups on huge or hostile pages.
	maxBodyBytes = 2 << 20 // 2 MiB
	// maxContentBytes caps extracted text sent to the LLM (~40KB per page).
	maxContentBytes = 40000
	// minTextRunes filters tiny fragments and navigation noise.
	minTextRunes = 25
)

// excessiveBlankLines collapses 3+ consecutive newlines left behind by
// removed elements into a single blank line.
var excessiveBlankLines = regexp.MustCompile(`\n{3,}`)

// Scraper fetches and extracts clean text from webpages.
type Scraper struct {
	client *http.Client
}

// NewScraper initializes a high-performance HTTP scraper with cookie support.
func NewScraper(jar *cookiejar.Jar, timeout time.Duration) *Scraper {
	if timeout <= 0 {
		timeout = defaultScraperTimeout
	}

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	if jar == nil {
		// An empty jar keeps cookie handling consistent (e.g. across redirects);
		// a typed-nil *cookiejar.Jar assigned to http.Client.Jar would panic
		// on the first request.
		jar, _ = cookiejar.New(nil)
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
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

	// Only parse HTML documents; skip PDFs, images, and other binaries.
	if ct := resp.Header.Get("Content-Type"); ct != "" && !isHTMLContentType(ct) {
		return nil, fmt.Errorf("unsupported content type %q for scraping", ct)
	}

	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML document: %w", err)
	}

	title := strings.TrimSpace(doc.Find("title").Text())

	// Strip noise and non-content elements
	doc.Find("script, style, noscript, nav, footer, header, svg, form, iframe, button, [role='navigation'], [role='banner']").Remove()

	var builder strings.Builder
	// Select only "leaf" content blocks. Excluding any element that itself
	// contains one of the content tags prevents nested duplicates (e.g. an
	// <article> whose text was previously re-emitted for every child <p>),
	// which inflated the context sent to the LLM by 2-10x.
	doc.Find("h1, h2, h3, h4, p, li, article, section").
		Not(":has(h1, h2, h3, h4, p, li, article, section)").
		Each(func(_ int, sel *goquery.Selection) {
			text := strings.TrimSpace(sel.Text())
			// Filter out tiny snippets / noise (rune-aware)
			if utf8.RuneCountInString(text) > minTextRunes {
				builder.WriteString(text)
				builder.WriteString("\n\n")
			}
		})

	content := strings.TrimSpace(builder.String())
	if len(content) == 0 {
		// Fallback to body text if headings/paragraphs were sparse
		content = strings.TrimSpace(doc.Find("body").Text())
	}

	content = excessiveBlankLines.ReplaceAllString(content, "\n\n")
	content = runeSafeTruncate(content, maxContentBytes)

	return &models.ScrapeResult{
		URL:       targetURL,
		Title:     title,
		Content:   content,
		ByteSize:  len(content),
		FetchedAt: time.Now(),
	}, nil
}

// runeSafeTruncate limits s to maxBytes without ever splitting a UTF-8 rune.
func runeSafeTruncate(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := s[:maxBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "\n\n...[content truncated]..."
}

// isHTMLContentType reports whether the Content-Type header indicates an
// HTML (or XHTML) document.
func isHTMLContentType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return strings.Contains(ct, "html")
	}
	return mt == "text/html" || mt == "application/xhtml+xml"
}
