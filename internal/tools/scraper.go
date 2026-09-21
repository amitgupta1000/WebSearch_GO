package tools

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
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
	// minQualityRunes rejects pages that contain only shells or consent text.
	minQualityRunes = 160
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
	canonicalURL := targetURL
	if canonical, exists := doc.Find("link[rel='canonical']").First().Attr("href"); exists {
		canonicalURL = resolveURL(targetURL, canonical)
	}
	if reason := detectBlockPage(title, doc.Find("body").Text()); reason != "" {
		bodyText := strings.TrimSpace(doc.Find("body").Text())
		return &models.ScrapeResult{
			URL:          targetURL,
			CanonicalURL: canonicalURL,
			Title:        title,
			Content:      runeSafeTruncate(bodyText, maxContentBytes),
			ContentType:  resp.Header.Get("Content-Type"),
			Extractor:    "blocked-page",
			QualityScore: 0,
			ByteSize:     len(bodyText),
			FetchedAt:    time.Now(),
			Blocked:      true,
			BlockReason:  reason,
		}, nil
	}
	var jsonLD []string
	doc.Find("script[type='application/ld+json']").Each(func(_ int, sel *goquery.Selection) {
		if value := strings.TrimSpace(sel.Text()); value != "" {
			jsonLD = append(jsonLD, value)
		}
	})

	contentRoot := doc.Find("main, article, [role='main']").First()
	if contentRoot.Length() == 0 {
		contentRoot = doc.Find("body").First()
	}

	// Strip noise and non-content elements
	noise := "script, style, noscript, nav, footer, header, aside, svg, form, iframe, button, dialog, [role='navigation'], [role='banner'], [role='dialog'], [aria-modal='true'], [class*='cookie'], [id*='cookie'], [class*='consent'], [id*='consent'], [class*='sidebar'], [id*='sidebar'], [class*='advert'], [id*='advert'], [class*='related'], [id*='related'], [class*='social-share'], [id*='social-share']"
	doc.Find(noise).Remove()

	var builder strings.Builder
	var headings []string
	contentRoot.Find("h1, h2, h3, h4").Each(func(_ int, sel *goquery.Selection) {
		if heading := strings.TrimSpace(sel.Text()); heading != "" {
			headings = append(headings, heading)
		}
	})
	images := extractImages(contentRoot, targetURL)
	// Select only "leaf" content blocks. Excluding any element that itself
	// contains one of the content tags prevents nested duplicates (e.g. an
	// <article> whose text was previously re-emitted for every child <p>),
	// which inflated the context sent to the LLM by 2-10x.
	contentRoot.Find("h1, h2, h3, h4, p, li, article, section").
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
	if utf8.RuneCountInString(content) < minQualityRunes {
		// Fallback to body text if headings/paragraphs were sparse
		content = strings.TrimSpace(doc.Find("body").Text())
	}

	content = excessiveBlankLines.ReplaceAllString(content, "\n\n")
	content = runeSafeTruncate(content, maxContentBytes)
	wordCount := len(strings.Fields(content))
	qualityScore := scraperQualityScore(content, headings, jsonLD)

	return &models.ScrapeResult{
		URL:          targetURL,
		CanonicalURL: canonicalURL,
		Title:        title,
		Content:      content,
		Headings:     headings,
		JSONLD:       jsonLD,
		Images:       images,
		WordCount:    wordCount,
		ContentType:  resp.Header.Get("Content-Type"),
		Extractor:    "goquery-content-root",
		QualityScore: qualityScore,
		ByteSize:     len(content),
		FetchedAt:    time.Now(),
	}, nil
}

const maxExtractedImages = 24

func extractImages(root *goquery.Selection, baseURL string) []models.Image {
	images := make([]models.Image, 0, maxExtractedImages)
	seen := make(map[string]bool)
	root.Find("img").EachWithBreak(func(_ int, sel *goquery.Selection) bool {
		source := ""
		for _, attribute := range []string{"src", "data-src", "data-lazy-src"} {
			if value, exists := sel.Attr(attribute); exists && strings.TrimSpace(value) != "" {
				source = strings.TrimSpace(value)
				break
			}
		}
		resolved := resolveURL(baseURL, source)
		parsed, err := url.Parse(resolved)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || seen[resolved] {
			return true
		}
		seen[resolved] = true
		caption := strings.TrimSpace(sel.Closest("figure").Find("figcaption").First().Text())
		images = append(images, models.Image{
			URL:     resolved,
			Alt:     strings.TrimSpace(attrValue(sel, "alt")),
			Title:   strings.TrimSpace(attrValue(sel, "title")),
			Caption: caption,
		})
		return true
	})
	return images
}

func detectBlockPage(title, body string) string {
	text := strings.ToLower(strings.TrimSpace(title + "\n" + body))
	markers := []struct {
		phrase string
		reason string
	}{
		{"press and hold", "press-and-hold challenge"},
		{"verify you are human", "human-verification challenge"},
		{"are you a robot", "robot-verification challenge"},
		{"captcha", "captcha challenge"},
		{"unusual traffic", "unusual-traffic challenge"},
		{"checking your browser", "browser-check challenge"},
		{"access denied", "access denied"},
	}
	for _, marker := range markers {
		if strings.Contains(text, marker.phrase) {
			return marker.reason
		}
	}
	return ""
}

func attrValue(selection *goquery.Selection, name string) string {
	value, _ := selection.Attr(name)
	return value
}

func resolveURL(base, reference string) string {
	baseURL, err := url.Parse(base)
	if err != nil {
		return reference
	}
	refURL, err := url.Parse(strings.TrimSpace(reference))
	if err != nil {
		return reference
	}
	return baseURL.ResolveReference(refURL).String()
}

func scraperQualityScore(content string, headings, jsonLD []string) float64 {
	score := 0.25
	words := len(strings.Fields(content))
	if words >= 40 {
		score += 0.35
	} else if words >= 15 {
		score += 0.15
	}
	if len(headings) > 0 {
		score += 0.2
	}
	if len(jsonLD) > 0 {
		score += 0.15
	}
	if strings.Contains(strings.ToLower(content), "accept cookies") || strings.Contains(strings.ToLower(content), "cookie policy") {
		score -= 0.2
	}
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
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
