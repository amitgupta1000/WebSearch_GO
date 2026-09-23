package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"golang.org/x/net/html/charset"
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
	// blockPageMaxRunes bounds challenge detection to pages with little visible
	// text. Real challenge pages are nearly empty once scripts are stripped,
	// whereas a substantive page that merely mentions a marker stays unblocked.
	blockPageMaxRunes = 1200
	// defaultFetchAttempts is how many times a transient fetch failure is retried.
	defaultFetchAttempts = 3
	// defaultFetchBackoff is the linear backoff unit between fetch attempts.
	defaultFetchBackoff = 400 * time.Millisecond
	// maxConcurrentRenders bounds how many browser contexts may exist at once.
	maxConcurrentRenders = 2
	// Hard-URL ladder budgets. Each attempt is capped so the whole ladder stays
	// inside the handler's per-URL slice of the run's research budget.
	hardRenderTimeout     = 15 * time.Second
	extendedRenderTimeout = 20 * time.Second
	// extendedNetworkIdle and extendedSettle lengthen the ladder's final step,
	// giving slow interstitials time to clear and late hydration time to finish.
	extendedNetworkIdle = 8 * time.Second
	extendedSettle      = 2500 * time.Millisecond
)

// noiseSelector lists elements that never carry primary content. It is applied
// before any text is inspected so markup such as inline <script> source can
// never influence challenge detection or the text handed to the LLM.
const noiseSelector = "script, style, noscript, nav, footer, header, aside, svg, form, iframe, button, dialog, [role='navigation'], [role='banner'], [role='dialog'], [aria-modal='true'], [class*='cookie'], [id*='cookie'], [class*='consent'], [id*='consent'], [class*='sidebar'], [id*='sidebar'], [class*='advert'], [id*='advert'], [class*='related'], [id*='related'], [class*='social-share'], [id*='social-share']"

// excessiveBlankLines collapses 3+ consecutive newlines left behind by
// removed elements into a single blank line.
var excessiveBlankLines = regexp.MustCompile(`\n{3,}`)

// Sentinel errors for the hard-URL ladder, so callers can classify outcomes
// without matching error strings.
var (
	// ErrHardLadderUnavailable reports that the ladder cannot run at all,
	// usually because no Playwright/Chromium install is present.
	ErrHardLadderUnavailable = errors.New("hard URL ladder unavailable")
	// ErrHardLadderExhausted reports that every ladder step ran without
	// producing readable content (still blocked, or only a thin shell).
	ErrHardLadderExhausted = errors.New("hard URL ladder exhausted")
)

// Scraper fetches and extracts clean text from webpages.
type Scraper struct {
	client         *http.Client
	browserEnabled bool
	browserTimeout time.Duration
	allowPrivate   bool
	fetchAttempts  int
	fetchBackoff   time.Duration
	browserMu      sync.Mutex
	browser        browserRenderer
	browserInit    bool
	browserInitErr error
	closed         bool
	// renderSem bounds concurrent browser renders. It is a buffered channel
	// used as a counting semaphore.
	renderSem  chan struct{}
	newBrowser func(time.Duration) (browserRenderer, error)
}

// Option customizes a Scraper created by NewScraper / NewScraperWithBrowser.
type Option func(*Scraper)

// WithPrivateHostAccess allows scraping loopback, private and link-local
// addresses, which the scraper refuses by default. Scrape targets are derived
// from untrusted search results and LLM output, so the default policy blocks the
// SSRF/link-local-metadata class of bugs. Enable it for intranet deployments and
// for tests that target local httptest servers.
func WithPrivateHostAccess() Option {
	return func(s *Scraper) { s.allowPrivate = true }
}

// NewScraper initializes a high-performance HTTP scraper with cookie support.
func NewScraper(jar *cookiejar.Jar, timeout time.Duration, opts ...Option) *Scraper {
	return NewScraperWithBrowser(jar, timeout, false, opts...)
}

// NewScraperWithBrowser initializes the scraper and optionally enables the
// bounded JavaScript-rendering fallback.
func NewScraperWithBrowser(jar *cookiejar.Jar, timeout time.Duration, enableBrowser bool, opts ...Option) *Scraper {
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

	browserTimeout := browserFallbackTimeout
	if timeout < browserTimeout {
		browserTimeout = timeout
	}
	scraper := &Scraper{
		client:         client,
		browserEnabled: enableBrowser,
		browserTimeout: browserTimeout,
		fetchAttempts:  defaultFetchAttempts,
		fetchBackoff:   defaultFetchBackoff,
		renderSem:      make(chan struct{}, maxConcurrentRenders),
		newBrowser:     newPlaywrightRenderer,
	}
	for _, opt := range opts {
		opt(scraper)
	}
	return scraper
}

// browserFallbackContextKey carries a per-request override of the scraper's
// configured browser fallback default, so a user can opt into rendering for a
// single research run without changing the process-wide setting.
type browserFallbackContextKey struct{}

// WithBrowserFallback returns a context that overrides the browser fallback
// setting for one request. Absent means "use the configured default".
func WithBrowserFallback(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, browserFallbackContextKey{}, enabled)
}

// BrowserFallbackOverride reports the per-request browser fallback override and
// whether one was set. Callers that cache scrape results key on this so a thin
// HTTP-only result never masks a browser-rendered re-run of the same URL.
func BrowserFallbackOverride(ctx context.Context) (bool, bool) {
	override, ok := ctx.Value(browserFallbackContextKey{}).(bool)
	return override, ok
}

// browserFallbackFor reports whether browser rendering should be used for ctx.
func (s *Scraper) browserFallbackFor(ctx context.Context) bool {
	if override, ok := BrowserFallbackOverride(ctx); ok {
		return override
	}
	return s.browserEnabled
}

// jarCookies returns the shared cookie jar's cookies for the target URL. The
// jar belongs to the Go HTTP client; Chromium keeps its own cookie store, so
// the hard-URL ladder uses these to bridge a user-exported session into the
// browser context (see renderOptions.cookies).
func (s *Scraper) jarCookies(targetURL string) []*http.Cookie {
	parsed, err := url.Parse(targetURL)
	if err != nil || s.client == nil || s.client.Jar == nil {
		return nil
	}
	return s.client.Jar.Cookies(parsed)
}

// HardScrapeOptions tunes one run of the hard-URL ladder.
type HardScrapeOptions struct {
	// AlreadyRendered reports that the caller's normal path already rendered
	// the page in a browser (per-run fallback) and was still blocked, so the
	// ladder's plain-render step would only repeat that work.
	AlreadyRendered bool
}

// ScrapeHard escalates a URL the normal path could not read through the
// bounded hard-URL ladder: a plain browser render, then a cookie-bridged
// render when the jar holds cookies for the domain, then an extended-settle
// render that gives slow interstitials time to clear. It returns a result
// only when a step produces unblocked, non-thin content; every failure wraps
// ErrHardLadderUnavailable or ErrHardLadderExhausted. The ladder never solves
// challenges: an interstitial that survives every step keeps the URL blocked,
// and the calling agent degrades to its snippet-fallback evidence as before.
func (s *Scraper) ScrapeHard(ctx context.Context, targetURL string, opts HardScrapeOptions) (*models.ScrapeResult, error) {
	if err := s.validateTarget(targetURL); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, fmt.Errorf("scraper is closed")
	}
	browser, err := s.getBrowser()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrHardLadderUnavailable, err)
	}

	cookies := s.jarCookies(targetURL)
	type ladderStep struct {
		name string
		opts renderOptions
	}
	steps := make([]ladderStep, 0, 3)
	if !opts.AlreadyRendered {
		steps = append(steps, ladderStep{name: "plain-render", opts: renderOptions{timeout: hardRenderTimeout}})
	}
	if len(cookies) > 0 {
		steps = append(steps, ladderStep{name: "cookie-render", opts: renderOptions{cookies: cookies, timeout: hardRenderTimeout}})
	}
	steps = append(steps, ladderStep{
		name: "extended-settle",
		opts: renderOptions{cookies: cookies, networkIdle: extendedNetworkIdle, settle: extendedSettle, timeout: extendedRenderTimeout},
	})

	attempted := make([]string, 0, len(steps))
	var lastBlocked *models.ScrapeResult
	var lastErr error
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attempted = append(attempted, step.name)
		html, renderErr := s.render(ctx, browser, targetURL, step.opts)
		if renderErr != nil {
			lastErr = renderErr
			continue
		}
		result, extractErr := extractHTML(targetURL, "text/html", []byte(html), "playwright-content-root", false)
		if extractErr != nil {
			lastErr = extractErr
			continue
		}
		result.FallbackAttempted = true
		result.FallbackReason = step.name
		if !result.Blocked && !needsBrowserFallback(result) {
			result.FallbackStatus = "succeeded"
			return result, nil
		}
		if result.Blocked {
			lastBlocked = result
		}
	}
	names := strings.Join(attempted, " → ")
	if lastBlocked != nil {
		return nil, fmt.Errorf("%w (%s): %s", ErrHardLadderExhausted, names, lastBlocked.BlockReason)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("%w (%s): %v", ErrHardLadderExhausted, names, lastErr)
	}
	return nil, fmt.Errorf("%w (%s)", ErrHardLadderExhausted, names)
}

// BrowserAvailable reports whether Chromium can actually be started. The console
// uses it to decide whether offering the per-request toggle would be honest. The
// result is cached, and a successful probe leaves the browser warm for the first
// request.
func (s *Scraper) BrowserAvailable() bool {
	browser, err := s.getBrowser()
	return err == nil && browser != nil
}

// ScrapeURL fetches a target URL, strips non-content tags, and returns sanitized text.
func (s *Scraper) ScrapeURL(ctx context.Context, targetURL string) (*models.ScrapeResult, error) {
	if err := s.validateTarget(targetURL); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, fmt.Errorf("scraper is closed")
	}

	body, contentType, statusCode, truncated, err := s.fetchHTTP(ctx, targetURL)
	if err != nil {
		return s.tryBrowser(ctx, targetURL, nil, "HTTP request failed", err)
	}
	if statusCode != http.StatusOK {
		return s.tryBrowser(ctx, targetURL, nil, fmt.Sprintf("HTTP status %d", statusCode), fmt.Errorf("scraping returned status code: %d", statusCode))
	}
	if contentType != "" && !isHTMLContentType(contentType) {
		return nil, fmt.Errorf("unsupported content type %q for scraping", contentType)
	}

	result, err := extractHTML(targetURL, contentType, body, "goquery-content-root", truncated)
	if err != nil {
		return s.tryBrowser(ctx, targetURL, nil, "HTTP HTML parsing failed", err)
	}
	if needsBrowserFallback(result) {
		reason := "low-quality or JavaScript-shell page"
		if result.Blocked {
			reason = "HTTP response was marked as blocked"
		}
		return s.tryBrowser(ctx, targetURL, result, reason, nil)
	}
	return result, nil
}

// validateTarget enforces the scraper's HTTP(S)-only, public-host policy.
func (s *Scraper) validateTarget(targetURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil {
		return fmt.Errorf("invalid target URL %q: %w", targetURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		// The HTTP client rejects other schemes, but the browser renderer would
		// happily follow them (file:// reads local files), so reject up front.
		return fmt.Errorf("refusing to scrape non-HTTP(S) URL %q", targetURL)
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("refusing to scrape URL without a host %q", targetURL)
	}
	if s.allowPrivate {
		return nil
	}
	if isPrivateHost(host) {
		return fmt.Errorf("refusing to scrape private or non-routable host %q", targetURL)
	}
	return nil
}

// isPrivateHost reports whether host is a literal IP in a loopback, private,
// link-local or otherwise non-routable range (which includes cloud metadata
// endpoints such as 169.254.169.254), or a well-known local hostname.
//
// Hostnames that merely resolve to such an address are not caught here: that
// would require DNS resolution, which the dialer then repeats.
func isPrivateHost(host string) bool {
	lowered := strings.ToLower(strings.TrimSuffix(host, "."))
	if lowered == "localhost" || strings.HasSuffix(lowered, ".localhost") ||
		strings.HasSuffix(lowered, ".local") || strings.HasSuffix(lowered, ".internal") {
		return true
	}
	ip := net.ParseIP(lowered)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// fetchHTTP retrieves targetURL, retrying transient failures with linear
// backoff. Non-transient statuses (e.g. 404) are returned to the caller so the
// browser fallback can decide what to do with them. The returned bool reports
// whether the body hit maxBodyBytes and was therefore parsed incomplete.
func (s *Scraper) fetchHTTP(ctx context.Context, targetURL string) ([]byte, string, int, bool, error) {
	attempts := s.fetchAttempts
	if attempts <= 0 {
		attempts = defaultFetchAttempts
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if err := sleepWithContext(ctx, s.fetchBackoff*time.Duration(attempt-1)); err != nil {
				return nil, "", 0, false, err
			}
		}

		body, contentType, statusCode, truncated, err := s.fetchHTTPOnce(ctx, targetURL)
		if err == nil {
			if statusCode == http.StatusOK || !isRetriableStatus(statusCode) {
				return body, contentType, statusCode, truncated, nil
			}
			lastErr = &retriableError{fmt.Errorf("scraping returned status code: %d", statusCode)}
			continue
		}

		var retriable *retriableError
		if !errors.As(err, &retriable) {
			return nil, "", 0, false, err
		}
		lastErr = err
	}

	return nil, "", 0, false, fmt.Errorf("scraping failed after %d attempts: %w", attempts, lastErr)
}

// isRetriableStatus reports whether an HTTP status is worth retrying.
func isRetriableStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= 500
}

// sleepWithContext waits for d, returning early if ctx is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// fetchHTTPOnce performs a single GET, marking network failures as retriable.
func (s *Scraper) fetchHTTPOnce(ctx context.Context, targetURL string) ([]byte, string, int, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, "", 0, false, fmt.Errorf("failed to construct request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", 0, false, &retriableError{fmt.Errorf("scraping request failed: %w", err)}
	}
	defer resp.Body.Close()

	// Read one byte past the cap so a truncated document can be reported.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, "", 0, false, &retriableError{fmt.Errorf("failed to read HTML document: %w", err)}
	}
	truncated := len(body) > maxBodyBytes
	if truncated {
		body = body[:maxBodyBytes]
	}
	return body, resp.Header.Get("Content-Type"), resp.StatusCode, truncated, nil
}

func extractHTML(targetURL, contentType string, body []byte, extractor string, truncated bool) (*models.ScrapeResult, error) {
	// Decode using the Content-Type charset (or a <meta> declaration) instead of
	// assuming UTF-8, which corrupts legacy encodings such as ISO-8859-1.
	reader, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return nil, fmt.Errorf("failed to decode HTML document: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML document: %w", err)
	}

	title := strings.TrimSpace(doc.Find("title").Text())
	canonicalURL := targetURL
	if canonical, exists := doc.Find("link[rel='canonical']").First().Attr("href"); exists {
		canonicalURL = resolveURL(targetURL, canonical)
	}

	// JSON-LD must be read before noise removal, which discards <script> nodes.
	var jsonLD []string
	doc.Find("script[type='application/ld+json']").Each(func(_ int, sel *goquery.Selection) {
		if value := strings.TrimSpace(sel.Text()); value != "" {
			jsonLD = append(jsonLD, value)
		}
	})

	// Strip noise and non-content elements before inspecting any text, so that
	// inline <script>/<style> source can never influence challenge detection or
	// end up in the content handed to the LLM.
	doc.Find(noiseSelector).Remove()

	contentRoot := doc.Find("main, article, [role='main']").First()
	if contentRoot.Length() == 0 {
		contentRoot = doc.Find("body").First()
	}

	// Challenge detection only ever sees visible text.
	visibleText := strings.TrimSpace(contentRoot.Text())
	if reason := detectBlockPage(title, visibleText); reason != "" {
		return &models.ScrapeResult{
			URL:              targetURL,
			CanonicalURL:     canonicalURL,
			Title:            title,
			Content:          runeSafeTruncate(visibleText, maxContentBytes),
			ContentType:      contentType,
			Extractor:        "blocked-page",
			QualityScore:     0,
			ByteSize:         len(visibleText),
			FetchedAt:        time.Now(),
			Blocked:          true,
			BlockReason:      reason,
			ContentTruncated: truncated,
		}, nil
	}

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
			if utf8.RuneCountInString(text) >= minTextRunes {
				builder.WriteString(text)
				builder.WriteString("\n\n")
			}
		})

	content := strings.TrimSpace(builder.String())
	if utf8.RuneCountInString(content) < minQualityRunes {
		// Fallback to body text if headings/paragraphs were sparse
		content = bodyText(doc)
	}

	content = excessiveBlankLines.ReplaceAllString(content, "\n\n")
	content = runeSafeTruncate(content, maxContentBytes)
	wordCount := len(strings.Fields(content))
	qualityScore := scraperQualityScore(content, headings, jsonLD)

	return &models.ScrapeResult{
		URL:              targetURL,
		CanonicalURL:     canonicalURL,
		Title:            title,
		Content:          content,
		Headings:         headings,
		JSONLD:           jsonLD,
		Images:           images,
		WordCount:        wordCount,
		ContentType:      contentType,
		Extractor:        extractor,
		QualityScore:     qualityScore,
		ByteSize:         len(content),
		FetchedAt:        time.Now(),
		ContentTruncated: truncated,
	}, nil
}

// bodyBlockSelector lists the block elements used by bodyText.
const bodyBlockSelector = "h1, h2, h3, h4, h5, h6, p, li, dt, dd, td, th, blockquote, figcaption, pre"

// bodyText renders the document body as separated text blocks. goquery's
// Selection.Text() concatenates every descendant text node with no separator, so
// adjacent elements would otherwise run together ("Short pageSmall shell.").
func bodyText(doc *goquery.Document) string {
	var builder strings.Builder
	doc.Find("body").Find(bodyBlockSelector).
		Not(":has(" + bodyBlockSelector + ")").
		Each(func(_ int, sel *goquery.Selection) {
			if text := strings.TrimSpace(sel.Text()); text != "" {
				builder.WriteString(text)
				builder.WriteString("\n\n")
			}
		})
	if builder.Len() == 0 {
		// No recognisable blocks (e.g. bare inline text); fall back to raw body.
		return strings.TrimSpace(doc.Find("body").Text())
	}
	return strings.TrimSpace(builder.String())
}

func needsBrowserFallback(result *models.ScrapeResult) bool {
	if result == nil || result.Blocked {
		return true
	}
	return utf8.RuneCountInString(result.Content) < minQualityRunes || result.QualityScore < 0.45
}

func (s *Scraper) tryBrowser(ctx context.Context, targetURL string, base *models.ScrapeResult, reason string, originalErr error) (*models.ScrapeResult, error) {
	if !s.browserFallbackFor(ctx) {
		if base != nil {
			return base, nil
		}
		return nil, originalErr
	}

	if base != nil {
		base.FallbackAttempted = true
		base.FallbackStatus = "attempted"
		base.FallbackReason = reason
	}
	browser, err := s.getBrowser()
	if err == nil {
		rendered, renderErr := s.render(ctx, browser, targetURL, renderOptions{})
		if renderErr == nil {
			result, extractErr := extractHTML(targetURL, "text/html", []byte(rendered), "playwright-content-root", false)
			if extractErr == nil {
				result.FallbackAttempted = true
				result.FallbackReason = reason
				if result.Blocked {
					result.FallbackStatus = "blocked"
				} else {
					result.FallbackStatus = "succeeded"
				}
				return result, nil
			}
			renderErr = extractErr
		}
		err = renderErr
	}

	if base != nil {
		base.FallbackStatus = "failed"
		base.FallbackError = err.Error()
		return base, nil
	}
	if originalErr != nil {
		return nil, fmt.Errorf("%w; browser fallback failed: %v", originalErr, err)
	}
	return nil, fmt.Errorf("browser fallback failed: %w", err)
}

// render renders targetURL while holding one of the bounded render slots, so a
// burst of fallbacks cannot spawn an unbounded number of browser contexts.
func (s *Scraper) render(ctx context.Context, browser browserRenderer, targetURL string, opts renderOptions) (string, error) {
	if s.renderSem != nil {
		select {
		case s.renderSem <- struct{}{}:
			defer func() { <-s.renderSem }()
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	budget := opts.timeout
	if budget <= 0 {
		budget = s.browserTimeout
	}
	renderCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return browser.Render(renderCtx, targetURL, opts)
}

func (s *Scraper) getBrowser() (browserRenderer, error) {
	s.browserMu.Lock()
	defer s.browserMu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("scraper is closed")
	}
	if s.browserInit {
		return s.browser, s.browserInitErr
	}
	// The first initialisation result is cached, including a failure: a missing
	// Playwright install should not be retried on every scrape.
	s.browserInit = true
	s.browser, s.browserInitErr = s.newBrowser(s.browserTimeout)
	return s.browser, s.browserInitErr
}

// isClosed reports whether Close has already run.
func (s *Scraper) isClosed() bool {
	s.browserMu.Lock()
	defer s.browserMu.Unlock()
	return s.closed
}

// Close releases the optional browser process. It is safe to call repeatedly.
func (s *Scraper) Close() error {
	s.browserMu.Lock()
	defer s.browserMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.browser == nil {
		return nil
	}
	browser := s.browser
	s.browser = nil
	return browser.Close()
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

// detectBlockPage classifies challenge/consent interstitials from the page title
// and its visible text. Only pages with little visible content are inspected, so
// an article that merely mentions a marker word is not misclassified as blocked
// (and does not trigger an unnecessary browser render).
func detectBlockPage(title, visibleText string) string {
	visible := strings.TrimSpace(visibleText)
	if utf8.RuneCountInString(visible) > blockPageMaxRunes {
		return ""
	}
	text := strings.ToLower(title + "\n" + visible)
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

// resolveURL resolves reference against base. If either cannot be parsed it
// returns base, which is always preferable to emitting a dangling reference.
func resolveURL(base, reference string) string {
	baseURL, err := url.Parse(base)
	if err != nil {
		return base
	}
	refURL, err := url.Parse(strings.TrimSpace(reference))
	if err != nil {
		return base
	}
	return baseURL.ResolveReference(refURL).String()
}

func scraperQualityScore(content string, headings, jsonLD []string) float64 {
	score := 0.25
	words := len(strings.Fields(content))
	switch {
	case words >= 40:
		score += 0.35
	case words >= 15:
		score += 0.15
	}
	if len(headings) > 0 {
		score += 0.2
	}
	if len(jsonLD) > 0 {
		score += 0.15
	}
	lowered := strings.ToLower(content)
	if strings.Contains(lowered, "accept cookies") || strings.Contains(lowered, "cookie policy") {
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
