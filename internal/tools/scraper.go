package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
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
	// documentExtractTimeout bounds one document's text extraction, and is the guardrail a
	// stalled ingest needs: the extractor is model-backed and the local fallback walks a PDF
	// page by page, so without this a single large filing can hold the entire run while
	// nothing else progresses. Past the deadline the document degrades to the failure the
	// scrape report already knows how to describe.
	documentExtractTimeout = 60 * time.Second
	// documentSkipBytes is the size above which a document is not downloaded at all. The
	// report then says so, which is more useful than a truncated body that no extractor can
	// read: a filing larger than this is not something a run can cite with confidence.
	documentSkipBytes = 40 << 20
	// maxBodyBytes caps the downloaded HTML size before parsing to avoid
	// memory blowups on huge or hostile pages.
	maxBodyBytes = 2 << 20 // 2 MiB
	// maxDocumentDownloadBytes caps downloaded non-HTML documents (PDFs) before extraction,
	// aligned with the LLM's 8 MiB inline document limit.
	maxDocumentDownloadBytes = 8 << 20 // 8 MiB
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
	// maxConcurrentRenders bounds how many browser pages may be rendered at once.
	maxConcurrentRenders = 2
	// defaultHostRatePerSecond, defaultHostBurst and defaultHostConcurrency are
	// the default per-host politeness budget: parallel fetching is what makes a
	// run fast, and these keep it from concentrating on a single site.
	defaultHostRatePerSecond = 2
	defaultHostBurst         = 3
	defaultHostConcurrency   = 2
	// defaultMaxRetryAfter caps how long a server's Retry-After hint may delay a
	// retry, so one unfriendly server cannot consume the run's whole budget.
	defaultMaxRetryAfter = 5 * time.Second
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
	// ErrSessionRejected reports that the site answered with a redirect loop while a session
	// was being sent, which is how a flagged or expired export behaves rather than how a
	// broken site behaves. It exists so an operator sees "re-capture the session" instead of
	// debugging a site that is working fine.
	ErrSessionRejected = errors.New("the exported session appears rejected or flagged")
)

// IsRedirectLoop reports whether an error is the redirect-storm signature. Both clients
// produce it: Go's http.Client says "stopped after 10 redirects", and Chromium says
// "net::ERR_TOO_MANY_REDIRECTS".
func IsRedirectLoop(err error) bool {
	if err == nil {
		return false
	}
	// Chromium reports net::ERR_TOO_MANY_REDIRECTS (underscores), Go says "stopped after N
	// redirects" (spaces), so both spellings have to be matched.
	lowered := strings.ToLower(err.Error())
	if strings.Contains(lowered, "too many redirects") || strings.Contains(lowered, "too_many_redirects") {
		return true
	}
	return strings.Contains(lowered, "stopped after") && strings.Contains(lowered, "redirect")
}

// sessionRejectedError annotates a redirect loop that happened while a session was in play.
// It returns the original error unchanged when no session was sent for that host, because a
// loop without a session is a site problem, not a credential problem.
func (s *Scraper) sessionRejectedError(targetURL string, err error) error {
	if err == nil || !IsRedirectLoop(err) || !s.HasSessionCookies(targetURL) {
		return err
	}
	return fmt.Errorf("%w (re-capture it before the next run): %v", ErrSessionRejected, err)
}

// HasSessionCookies reports whether the jar holds any cookie for the target's host. It is how
// a caller decides whether a page is worth fetching the cheap way — a logged-in-only page
// answers an unauthenticated fetch with the same wall a browser would, so there is no point
// paying for that request unless a session exists.
func (s *Scraper) HasSessionCookies(targetURL string) bool {
	return len(s.jarCookies(targetURL)) > 0
}

// SessionFingerprint identifies the session a URL would be fetched with: a short hash of the
// cookies the jar would send, or "" when there are none. Cache keys carry it, because a page
// that failed unauthenticated must not be served as a cached failure to a run that now holds
// a session — the same URL genuinely answers differently.
func (s *Scraper) SessionFingerprint(targetURL string) string {
	cookies := s.jarCookies(targetURL)
	if len(cookies) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "; ")))
	return hex.EncodeToString(sum[:8])
}

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
	// hosts enforces the per-host request budget for HTTP fetches.
	hosts *hostLimiter
	// maxRetryAfter caps how long a Retry-After hint may delay a retry.
	maxRetryAfter time.Duration
	// extractDocument, when set, turns a non-HTML document (a PDF) into text.
	// Without it, non-HTML responses are rejected as unsupported.
	extractDocument DocumentTextExtractor
	// documentTimeout bounds one document's extraction; zero means documentExtractTimeout.
	documentTimeout time.Duration
}

// documentBudget is the deadline one document's extraction gets: the configured value, or
// the default when none is configured.
func (s *Scraper) documentBudget() time.Duration {
	if s != nil && s.documentTimeout > 0 {
		return s.documentTimeout
	}
	return documentExtractTimeout
}

// WithDocumentTimeout bounds how long one document's text extraction may take. It is the
// difference between a run that degrades (one filing becomes "failed: extraction exceeded
// 60s" in the report) and a run that stalls on it until its own deadline.
func WithDocumentTimeout(timeout time.Duration) Option {
	return func(s *Scraper) { s.documentTimeout = timeout }
}

// DocumentTextExtractor converts an already-fetched non-HTML document body into plain
// text. It returns the text plus the name of the extractor that produced it (one of
// ExtractorPDFGemini / ExtractorPDFLocal), which is reported in the scrape result.
type DocumentTextExtractor func(ctx context.Context, contentType string, body []byte) (text string, extractor string, err error)

// DocumentTarget describes the research scope used to extract relevant findings
// from a document. It is immutable for the lifetime of one scrape context.
type DocumentTarget struct {
	CompanyName     string
	Aliases         []string
	AreasOfInterest []string
	TimeScope       string
}

type documentTargetContextKey struct{}

// WithDocumentTarget attaches the per-run document target to a scrape context.
// The target is carried explicitly through the scrape call without changing the
// PageFetcher interface used by other callers.
func WithDocumentTarget(ctx context.Context, target DocumentTarget) context.Context {
	return context.WithValue(ctx, documentTargetContextKey{}, target)
}

// DocumentTargetFromContext returns the target attached to ctx, if any.
func DocumentTargetFromContext(ctx context.Context) (DocumentTarget, bool) {
	target, ok := ctx.Value(documentTargetContextKey{}).(DocumentTarget)
	return target, ok
}

// DocumentTargetFingerprint identifies the extraction scope for cache keys.
func DocumentTargetFingerprint(target DocumentTarget) string {
	value := strings.Join([]string{
		target.CompanyName,
		strings.Join(target.Aliases, "\x1f"),
		strings.Join(target.AreasOfInterest, "\x1f"),
		target.TimeScope,
	}, "\x1e")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Option customizes a Scraper created by NewScraper / NewScraperWithBrowser.
type Option func(*Scraper)

// WithDocumentTextExtractor enables text extraction for non-HTML documents (PDFs).
// Extraction is opt-in because it may forward document bytes to the model provider,
// and because the default policy is to reject anything that is not HTML.
func WithDocumentTextExtractor(extract DocumentTextExtractor) Option {
	return func(s *Scraper) { s.extractDocument = extract }
}

// WithPrivateHostAccess allows scraping loopback, private and link-local
// addresses, which the scraper refuses by default. Scrape targets are derived
// from untrusted search results and LLM output, so the default policy blocks the
// SSRF/link-local-metadata class of bugs. Enable it for intranet deployments and
// for tests that target local httptest servers.
func WithPrivateHostAccess() Option {
	return func(s *Scraper) { s.allowPrivate = true }
}

// WithHostLimits overrides the per-host politeness budget: at most
// ratePerSecond requests per second (burst may start back-to-back) and at most
// perHostConcurrency requests in flight at once. Non-positive values disable the
// corresponding limit.
func WithHostLimits(ratePerSecond, burst float64, perHostConcurrency int) Option {
	return func(s *Scraper) { s.hosts = newHostLimiter(ratePerSecond, burst, perHostConcurrency) }
}

// WithMaxRetryAfter caps how long a server's Retry-After hint may delay a retry.
// A non-positive value leaves the hint uncapped.
func WithMaxRetryAfter(delay time.Duration) Option {
	return func(s *Scraper) { s.maxRetryAfter = delay }
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
		// weedoo.in traffic routes through WEEDOO_PROXY_URL when configured
		// (Cloudflare challenges datacenter egress on sight); every other host
		// keeps the standard environment proxy behavior.
		Proxy: weedooProxyForRequest,
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
	if weedooProxyURL != "" {
		log.Printf("[SCRAPER] weedoo.in requests route through WEEDOO_PROXY_URL")
	}
	scraper := &Scraper{
		client:         client,
		browserEnabled: enableBrowser,
		browserTimeout: browserTimeout,
		fetchAttempts:  defaultFetchAttempts,
		fetchBackoff:   defaultFetchBackoff,
		maxRetryAfter:  defaultMaxRetryAfter,
		hosts:          newHostLimiter(defaultHostRatePerSecond, defaultHostBurst, defaultHostConcurrency),
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

// conditionalRequestKey carries response validators for one fetch.
type conditionalRequestKey struct{}

// validators are the HTTP response validators a conditional request needs.
type validators struct {
	etag         string
	lastModified string
}

// WithConditionalRequest returns a context that makes the next fetch conditional:
// the scraper sends If-None-Match / If-Modified-Since, and a page the server
// still considers current comes back as a 304 with no body. This is how a cached
// page is refreshed for one round trip instead of a full download.
//
// A context without validators (or with only blank ones) is returned unchanged,
// so callers can pass them through unconditionally.
func WithConditionalRequest(ctx context.Context, etag, lastModified string) context.Context {
	etag = strings.TrimSpace(etag)
	lastModified = strings.TrimSpace(lastModified)
	if etag == "" && lastModified == "" {
		return ctx
	}
	return context.WithValue(ctx, conditionalRequestKey{}, validators{etag: etag, lastModified: lastModified})
}

// conditionalRequestFor reports the validators carried by ctx, if any.
func conditionalRequestFor(ctx context.Context) (validators, bool) {
	carried, ok := ctx.Value(conditionalRequestKey{}).(validators)
	return carried, ok
}

// ConditionalRequestValidators reports the response validators carried by ctx,
// which a caller set with WithConditionalRequest. It exists so a caller can tell
// whether a fetch it is about to make is a revalidation.
func ConditionalRequestValidators(ctx context.Context) (etag, lastModified string, ok bool) {
	carried, found := conditionalRequestFor(ctx)
	return carried.etag, carried.lastModified, found
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

// HardLadderBudget is the worst-case wall-clock allowance of one run of the
// hard-URL ladder: every step in sequence, each using its full navigation budget.
//
// Callers size their own per-URL budget from this rather than from a hand-written
// constant, so the last step — the extended-settle render that exists precisely for
// slow interstitials — can never be clipped by an outside timeout before it has had
// its chance.
func HardLadderBudget() time.Duration {
	return hardRenderTimeout + hardRenderTimeout + extendedRenderTimeout
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
	steps := hardLadderSteps(targetURL, cookies, opts.AlreadyRendered)
	if len(steps) == 0 {
		// Nothing left to try: the caller already rendered this page and there is
		// no session to unlock the shape, so an attempt would only spend the run's
		// budget reaching the same wall.
		return nil, fmt.Errorf("%w: no applicable ladder step for %s", ErrHardLadderUnavailable, targetURL)
	}

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
	hint := noCookieHint(targetURL, cookies)
	if lastBlocked != nil {
		return nil, s.sessionRejectedError(targetURL, fmt.Errorf("%w (%s): %s%s", ErrHardLadderExhausted, names, lastBlocked.BlockReason, hint))
	}
	if lastErr != nil {
		return nil, s.sessionRejectedError(targetURL, fmt.Errorf("%w (%s): %v%s", ErrHardLadderExhausted, names, lastErr, hint))
	}
	return nil, fmt.Errorf("%w (%s)%s", ErrHardLadderExhausted, names, hint)
}

// ladderStep is one attempt in the hard-URL ladder.
type ladderStep struct {
	name string
	opts renderOptions
}

// hardLadderSteps returns the steps that apply to this URL, in order. It is the
// single place the ladder's policy lives, so tests can pin it without a browser.
//
// The order follows what can actually clear the page. An auth-walled shape needs the session
// and nothing else, so with a session the cookie-bridged render leads and the plain render is
// dropped: measured on a live profile, the logged-out render spent its full budget reaching
// the sign-in page and was the step that triggered LinkedIn's redirect loop. Without a
// session there is nothing to bridge, so the plain render is all there is.
func hardLadderSteps(targetURL string, cookies []*http.Cookie, alreadyRendered bool) []ladderStep {
	steps := make([]ladderStep, 0, 3)
	authWalled := HardURLShapeOf(targetURL).AuthWalled()
	hasCookies := len(cookies) > 0

	if hasCookies && authWalled {
		steps = append(steps, ladderStep{name: "cookie-render", opts: renderOptions{cookies: cookies, timeout: hardRenderTimeout}})
		steps = append(steps, ladderStep{
			name: "extended-settle",
			opts: renderOptions{cookies: cookies, networkIdle: extendedNetworkIdle, settle: extendedSettle, timeout: extendedRenderTimeout},
		})
		return steps
	}

	if !alreadyRendered {
		steps = append(steps, ladderStep{name: "plain-render", opts: renderOptions{timeout: hardRenderTimeout}})
	}
	if hasCookies {
		steps = append(steps, ladderStep{name: "cookie-render", opts: renderOptions{cookies: cookies, timeout: hardRenderTimeout}})
	}
	if !hasCookies && authWalled {
		// Logged out, an auth-walled shape does not clear with time: every
		// observed attempt spent the full extended settle and reached the same
		// sign-in page. Skipping the step keeps that budget for the synthesis
		// that follows, and the step list in the failure message shows the skip.
		return steps
	}
	steps = append(steps, ladderStep{
		name: "extended-settle",
		opts: renderOptions{cookies: cookies, networkIdle: extendedNetworkIdle, settle: extendedSettle, timeout: extendedRenderTimeout},
	})
	return steps
}

// noCookieHint explains an escalation failure that had no session to bridge. The
// step list already shows cookie-render was skipped; this says why, which is the
// difference between "the wall won" and "we never brought the session".
func noCookieHint(targetURL string, cookies []*http.Cookie) string {
	if len(cookies) > 0 {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return fmt.Sprintf(" (no cookies for %s: set COOKIE_FILE_PATH with an exported session to enable the cookie-render step)", parsed.Hostname())
}

// BrowserAvailable reports whether Chromium can actually be started. The console
// uses it to decide whether offering the per-request toggle would be honest. The
// result is cached, and a successful probe leaves the browser warm for the first
// request.
func (s *Scraper) BrowserAvailable() bool {
	browser, err := s.getBrowser()
	return err == nil && browser != nil
}

// RenderHTMLToPDF prints a self-contained HTML document to PDF using the browser,
// which is what turns a finished run into a shareable file.
//
// It takes one of the same render slots the scrape fallback uses, so printing can
// never spawn more Chromium pages than scraping already allows, and it returns an
// error when the browser is unavailable — the caller is expected to store the
// Markdown anyway rather than fail the run.
func (s *Scraper) RenderHTMLToPDF(ctx context.Context, html string) ([]byte, error) {
	return s.printThroughBrowser(ctx, html, "")
}

// RenderHTMLToPDFFootered prints a self-contained HTML document with a running
// footer on every page, the branded counterpart of RenderHTMLToPDF. It is
// forwarded so the exporter can detect the capability on the scraper rather
// than on the browser behind it; a browser that cannot print running footers
// degrades to the plain document, whose own end footer is the only branding the
// pages carry.
func (s *Scraper) RenderHTMLToPDFFootered(ctx context.Context, html, footerHTML string) ([]byte, error) {
	return s.printThroughBrowser(ctx, html, footerHTML)
}

// printThroughBrowser resolves the browser's printing capability — running
// footer when the renderer has one, plain document otherwise — and prints under
// the render semaphore and print budget every browser job is bounded by.
func (s *Scraper) printThroughBrowser(ctx context.Context, html, footerHTML string) ([]byte, error) {
	if strings.TrimSpace(html) == "" {
		return nil, fmt.Errorf("nothing to print")
	}
	if s.isClosed() {
		return nil, fmt.Errorf("scraper is closed")
	}

	browser, err := s.getBrowser()
	if err != nil {
		return nil, fmt.Errorf("browser rendering is unavailable: %w", err)
	}
	print := func(printCtx context.Context) ([]byte, error) {
		if footered, ok := browser.(footeredDocumentPrinter); ok {
			return footered.RenderHTMLToPDFFootered(printCtx, html, footerHTML)
		}
		plain, ok := browser.(documentPrinter)
		if !ok {
			return nil, fmt.Errorf("the configured browser renderer cannot print documents")
		}
		return plain.RenderHTMLToPDF(printCtx, html)
	}

	if s.renderSem != nil {
		select {
		case s.renderSem <- struct{}{}:
			defer func() { <-s.renderSem }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	printCtx, cancel := context.WithTimeout(ctx, browserPrintTimeout)
	defer cancel()
	return print(printCtx)
}

// DocumentPrinter reports whether the scraper can print documents to PDF, which is
// what the exporter checks before advertising a PDF artifact.
func (s *Scraper) DocumentPrinter() bool {
	if s == nil || s.isClosed() {
		return false
	}
	browser, err := s.getBrowser()
	if err != nil {
		return false
	}
	_, ok := browser.(documentPrinter)
	return ok
}

// RenderHTML renders a page in the shared browser and returns the full HTML, for
// clients that parse rendered markup rather than extracted text.
//
// It exists for the Casemine case-law client: the site answers plain HTTP with
// 403 (measured, including for robots.txt), its results are JavaScript-rendered,
// and its document links are the payload a parser needs — which the scraper's
// text extraction throws away. The jar's cookies for the target host are bridged
// into the browser context first, so a user-exported session (COOKIE_FILE_PATH)
// authenticates the render the same way the hard-URL ladder's cookie step does.
// The render semaphore, the shared context and the browser's clearance cookies
// are all shared with every other render, so this is bounded like any other
// browser work.
func (s *Scraper) RenderHTML(ctx context.Context, targetURL string) (string, error) {
	if s == nil || s.isClosed() {
		return "", fmt.Errorf("scraper is closed")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := s.validateTarget(targetURL); err != nil {
		return "", err
	}
	browser, err := s.getBrowser()
	if err != nil {
		return "", fmt.Errorf("browser rendering is unavailable: %w", err)
	}
	return s.render(ctx, browser, targetURL, renderOptions{cookies: s.jarCookies(targetURL)})
}

// FetchHTML fetches a page over plain HTTP — the scraper's browser-like headers,
// jar session and retry policy — and returns the raw HTML without extracting
// text, for clients that parse markup themselves.
//
// It exists beside RenderHTML for the same client: the measured reality is that
// Casemine's 403 wall is User-Agent-only (a plain fetch with the scraper's Chrome
// UA returns the full server-rendered page), so the cheap path runs first and the
// cookie-bridged render is the fallback when a wall ever tightens. The body is
// capped at maxBodyBytes like every other fetch.
func (s *Scraper) FetchHTML(ctx context.Context, targetURL string) (string, error) {
	if s == nil || s.isClosed() {
		return "", fmt.Errorf("scraper is closed")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := s.validateTarget(targetURL); err != nil {
		return "", err
	}
	response, err := s.fetchHTTP(ctx, targetURL)
	if err != nil {
		return "", err
	}
	if response.NotModified {
		// A conditional request has no body here; the caller never sends
		// validators on this path, but a cached ETag elsewhere could produce
		// one, and empty HTML must not be mistaken for content.
		return "", fmt.Errorf("the page has not changed since the cached copy (304)")
	}
	if response.Truncated {
		// Truncation is acceptable for a search page: the results live in the
		// first part of the document, and the caller reports byte size.
		return string(response.Body), nil
	}
	return string(response.Body), nil
}

// ScrapeURL fetches a target URL, strips non-content tags, and returns sanitized text.
func (s *Scraper) ScrapeURL(ctx context.Context, targetURL string) (*models.ScrapeResult, error) {
	if err := s.validateTarget(targetURL); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, fmt.Errorf("scraper is closed")
	}

	response, err := s.fetchHTTP(ctx, targetURL)
	if err != nil {
		// A terminal failure is one a render cannot fix, so it is reported rather than
		// handed to the browser: an oversized document has no HTML to recover.
		var terminal *terminalFetchError
		if errors.As(err, &terminal) {
			return nil, terminal
		}
		// A redirect storm is different: a session in play makes it the signature of a
		// rejected export, and it is named as such so nobody debugs the site instead.
		return s.tryBrowser(ctx, targetURL, nil, "HTTP request failed", s.sessionRejectedError(targetURL, err))
	}
	if response.NotModified {
		// The caller's cached copy is still current, so there is no body to
		// extract; returning the validators again keeps the next revalidation
		// conditional as well.
		return &models.ScrapeResult{
			URL:          targetURL,
			CanonicalURL: targetURL,
			ContentType:  response.ContentType,
			ETag:         response.ETag,
			LastModified: response.LastModified,
			NotModified:  true,
			FetchedAt:    time.Now(),
		}, nil
	}
	if response.Status != http.StatusOK {
		if isHopelessStatus(response.Status) {
			// A browser cannot turn an absent or explicitly gone page into
			// evidence, so report it instead of paying seconds for a render.
			return nil, fmt.Errorf("scraping returned status code: %d", response.Status)
		}
		return s.tryBrowser(ctx, targetURL, nil, fmt.Sprintf("HTTP status %d", response.Status), fmt.Errorf("scraping returned status code: %d", response.Status))
	}
	if response.ContentType != "" && !isHTMLContentType(response.ContentType) {
		return s.scrapeDocument(ctx, targetURL, response.ContentType, response.Body, response.Truncated)
	}

	result, err := extractHTML(targetURL, response.ContentType, response.Body, "goquery-content-root", response.Truncated)
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
	// The validators come from the HTTP response; carrying them on the result is
	// what lets a later fetch ask whether this content is still current instead
	// of downloading it again.
	result.ETag = response.ETag
	result.LastModified = response.LastModified
	return result, nil
}

// FetchDocumentPreview retrieves up to maxBytes of a document (e.g. PDF) for cheap relevance triage.
// It respects the scraper's target validation, host concurrency, and politeness limits.
func (s *Scraper) FetchDocumentPreview(ctx context.Context, targetURL string, maxBytes int) ([]byte, string, error) {
	if s.isClosed() {
		return nil, "", fmt.Errorf("scraper is closed")
	}
	if err := s.validateTarget(targetURL); err != nil {
		return nil, "", err
	}
	if maxBytes <= 0 {
		maxBytes = 512 << 10 // 512 KiB default
	}

	release, err := s.hosts.acquire(ctx, hostOf(targetURL))
	if err != nil {
		return nil, "", err
	}
	defer release()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to construct request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/pdf,application/x-pdf,*/*;q=0.8")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("document preview request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("document preview returned HTTP status %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)))
	if err != nil {
		return nil, "", fmt.Errorf("failed to read document preview body: %w", err)
	}
	return body, contentType, nil
}

// isHopelessStatus reports statuses that a browser render cannot improve on: the
// page is absent or explicitly gone, so rendering it would only cost seconds.
func isHopelessStatus(status int) bool {
	return status == http.StatusNotFound || status == http.StatusGone
}

// scrapeDocument extracts text from a non-HTML PDF. Only PDFs are supported, and only
// when a document extractor was configured — every other non-HTML response is rejected
// exactly as it was before, so the default policy is unchanged.
func (s *Scraper) scrapeDocument(ctx context.Context, targetURL, contentType string, body []byte, truncated bool) (*models.ScrapeResult, error) {
	if s.extractDocument == nil || !isExtractableDocument(contentType, body) {
		return nil, fmt.Errorf("unsupported content type %q for scraping", contentType)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("document scrape aborted before extraction: %w", err)
	}
	// The extraction gets its own deadline: a large filing must not hold the run. The
	// timeout is the smaller of the configured document budget and whatever the run has
	// left, so a run near its own deadline never starts an extraction it cannot finish.
	extractCtx, cancelExtract := context.WithTimeout(ctx, s.documentBudget())
	defer cancelExtract()

	text, extractor, err := s.extractDocument(extractCtx, contentType, body)
	if err != nil {
		return nil, fmt.Errorf("document text extraction failed for %q: %w", contentType, err)
	}
	content := normalizeDocumentText(text)
	if content == "" {
		return nil, fmt.Errorf("document %s contained no extractable text", targetURL)
	}
	if ratio := printableRatio(content); ratio < minPrintableRatio {
		return nil, fmt.Errorf("document %s produced unreadable text (%.0f%% printable)", targetURL, ratio*100)
	}
	if len(content) > maxContentBytes {
		truncated = true
	}
	content = runeSafeTruncate(content, maxContentBytes)

	return &models.ScrapeResult{
		URL:              targetURL,
		CanonicalURL:     targetURL,
		Title:            documentTitle(targetURL),
		Content:          content,
		WordCount:        len(strings.Fields(content)),
		ContentType:      contentType,
		Extractor:        extractor,
		QualityScore:     scraperQualityScore(content, nil, nil),
		ByteSize:         len(content),
		FetchedAt:        time.Now(),
		ContentTruncated: truncated,
	}, nil
}

// documentTitle derives a title for a document URL, which has no <title> element:
// the final path segment (the file name) when there is one, otherwise the host.
func documentTitle(targetURL string) string {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return targetURL
	}
	trimmed := strings.Trim(parsed.Path, "/")
	if index := strings.LastIndex(trimmed, "/"); index >= 0 {
		trimmed = trimmed[index+1:]
	}
	if trimmed != "" {
		if unescaped, unescapeErr := url.PathUnescape(trimmed); unescapeErr == nil {
			return unescaped
		}
		return trimmed
	}
	return parsed.Host
}

// normalizeDocumentText tidies extractor output, which arrives as a flat run of text
// fragments: trailing spaces are dropped per line and run-on blank lines collapse.
func normalizeDocumentText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(excessiveBlankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
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

// fetchResponse is the outcome of one HTTP attempt: the body plus the response
// metadata the rest of the scraper reacts to.
type fetchResponse struct {
	Body         []byte
	ContentType  string
	Status       int
	Truncated    bool
	ETag         string
	LastModified string
	NotModified  bool
	RetryAfter   time.Duration
}

// terminalFetchError marks a fetch failure that no fallback can fix — an oversized document
// is not made readable by rendering it — so the scrape reports it instead of paying seconds
// for a browser attempt that cannot succeed.
type terminalFetchError struct{ err error }

func (e *terminalFetchError) Error() string { return e.err.Error() }

func (e *terminalFetchError) Unwrap() error { return e.err }

// fetchHTTP retrieves targetURL, retrying transient failures with linear backoff
// that defers to the server's Retry-After hint when it sends one. Non-transient
// statuses (e.g. 404) are returned to the caller so the browser fallback can
// decide what to do with them.
//
// The host slot is held across the whole retry sequence, so a retried page
// counts as one polite request against its host's budget rather than three.
func (s *Scraper) fetchHTTP(ctx context.Context, targetURL string) (*fetchResponse, error) {
	attempts := s.fetchAttempts
	if attempts <= 0 {
		attempts = defaultFetchAttempts
	}

	release, err := s.hosts.acquire(ctx, hostOf(targetURL))
	if err != nil {
		return nil, err
	}
	defer release()

	var lastErr error
	var retryAfter time.Duration
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			delay := s.fetchBackoff * time.Duration(attempt-1)
			if retryAfter > delay {
				delay = retryAfter
			}
			if limit := s.maxRetryAfter; limit > 0 && delay > limit {
				delay = limit
			}
			if err := sleepWithContext(ctx, delay); err != nil {
				return nil, err
			}
		}
		retryAfter = 0

		response, err := s.fetchHTTPOnce(ctx, targetURL)
		if err == nil {
			if response.Status == http.StatusOK || response.NotModified || !isRetriableStatus(response.Status) {
				return response, nil
			}
			// A 429 or 503 that carries Retry-After says exactly how long to
			// wait; honouring it turns a rate limit into one short pause instead
			// of three wasted attempts.
			retryAfter = response.RetryAfter
			lastErr = &retriableError{fmt.Errorf("scraping returned status code: %d", response.Status)}
			continue
		}

		var retriable *retriableError
		if !errors.As(err, &retriable) {
			return nil, err
		}
		lastErr = err
	}

	return nil, fmt.Errorf("scraping failed after %d attempts: %w", attempts, lastErr)
}

// parseRetryAfter reads a Retry-After header, which is either a number of
// seconds or an HTTP date. A value that is malformed or already in the past
// yields zero, so it never shortens the normal backoff.
func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := when.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}

// isRetriableStatus reports whether an HTTP status is worth retrying.
func isRetriableStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= 500
}

// botDenialStatus is LinkedIn's non-standard "we refuse this client" code. It is not a
// transient failure and not a login wall: measured with no session, a person profile answers
// 999 with 1.5 KB and no content, so retrying it is both impolite and pointless.
const botDenialStatus = 999

// isBotDenialStatus reports whether a status is a deliberate refusal of the client rather than
// a server problem. Checking it before isRetriableStatus is what stops a refusal from being
// retried three times.
func isBotDenialStatus(statusCode int) bool {
	return statusCode == botDenialStatus
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

// fetchHTTPOnce performs a single GET, marking network failures as retriable and
// recording the response validators and Retry-After hint. Conditional headers are
// sent when the caller supplied validators, so a page that has not changed comes
// back as a bodiless 304 instead of being transferred again.
func (s *Scraper) fetchHTTPOnce(ctx context.Context, targetURL string) (*fetchResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to construct request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if carried, ok := conditionalRequestFor(ctx); ok {
		if carried.etag != "" {
			req.Header.Set("If-None-Match", carried.etag)
		}
		if carried.lastModified != "" {
			req.Header.Set("If-Modified-Since", carried.lastModified)
		}
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, &retriableError{fmt.Errorf("scraping request failed: %w", err)}
	}
	defer resp.Body.Close()

	response := &fetchResponse{
		ContentType:  resp.Header.Get("Content-Type"),
		Status:       resp.StatusCode,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		RetryAfter:   parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
	}
	if resp.StatusCode == http.StatusNotModified {
		response.NotModified = true
		return response, nil
	}

	// A refusal is returned at once: the retry loop exists for transient failures, and a
	// deliberate bot-denial is not one. The error is not terminal for the caller, because a
	// render *can* win a public page on a host that refuses plain clients — it only loses the
	// retries. The body is never read, since a refusal carries nothing worth keeping.
	if isBotDenialStatus(resp.StatusCode) {
		return nil, fmt.Errorf("the site refused the request (HTTP %d)", resp.StatusCode)
	}

	// A document far larger than any run could use is not downloaded at all. The check uses
	// the declared length, which is enough to stop the transfer before it starts; the body
	// cap below still guards a server that understates or omits it.
	if response.ContentType != "" && !isHTMLContentType(response.ContentType) && resp.ContentLength > documentSkipBytes {
		return nil, &terminalFetchError{fmt.Errorf("document is %d bytes, above the %d byte download limit", resp.ContentLength, documentSkipBytes)}
	}

	maxBytes := maxBodyBytes
	if !isHTMLContentType(response.ContentType) {
		maxBytes = maxDocumentDownloadBytes
	}

	// Read one byte past the cap so a truncated document can be reported.
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes+1)))
	if err != nil {
		return nil, &retriableError{fmt.Errorf("failed to read document response: %w", err)}
	}
	response.Truncated = len(body) > maxBytes
	if response.Truncated {
		body = body[:maxBytes]
	}
	response.Body = body
	return response, nil
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
	// <article> whose text was previously re-emitted for every child <p>), which
	// inflated the context sent to the LLM by 2-10x. Leaf <table> elements are
	// included so tabular registry data — directors with DINs, GST and ISIN
	// tables, financial rows — reaches the LLM: registry pages carry their most
	// valuable facts in <td> cells that p/li selectors never see.
	contentRoot.Find("h1, h2, h3, h4, p, li, article, section, table").
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
