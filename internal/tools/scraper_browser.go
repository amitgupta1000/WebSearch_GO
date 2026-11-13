package tools

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mxschmitt/playwright-go"
)

const browserFallbackTimeout = 10 * time.Second

// browserNetworkIdleTimeout bounds how long a render waits for in-flight
// requests (analytics, hydration, JavaScript interstitials) to settle.
const browserNetworkIdleTimeout = 4 * time.Second

// browserSettleTimeout is a short fixed pause after network idle so late
// hydration effects land before the DOM is read.
const browserSettleTimeout = 750 * time.Millisecond

// browserReadReserve keeps part of the render budget free for reading the
// rendered HTML, so a render that spent its whole settle window still returns
// content instead of failing the post-wait context check.
const browserReadReserve = 250 * time.Millisecond

// renderReadyRunes is how much visible text means the page already has its
// content at DOMContentLoaded, which makes the network-idle wait and the settle
// pause unnecessary.
const renderReadyRunes = 2000

// blockedRenderResources lists the resource types the renderer refuses to load.
//
// A rendered page is only read for its DOM text, so images, media, fonts and
// stylesheets are overhead: aborting them removes most of a page's bytes and
// much of its settling time, while the document, its scripts and its XHR/fetch
// calls still run — which is exactly what a JavaScript interstitial needs in
// order to clear.
var blockedRenderResources = map[string]bool{
	"image":      true,
	"media":      true,
	"font":       true,
	"stylesheet": true,
}

// browserPrintSettleTimeout is how long a document is allowed to settle before it
// is printed. Exported documents carry inline CSS and no remote assets, so layout
// is ready almost immediately.
const browserPrintSettleTimeout = 400 * time.Millisecond

// browserPrintTimeout bounds one print job. It is separate from the scrape render
// budget because printing a document is a different amount of work from rendering
// a hostile page.
const browserPrintTimeout = 20 * time.Second

// blockHeavyResources installs the resource router on a context. It is applied
// once per context rather than per page, so every render in a run benefits.
func blockHeavyResources(browserContext playwright.BrowserContext) {
	handler := func(route playwright.Route) {
		if blockedRenderResources[route.Request().ResourceType()] {
			_ = route.Abort()
			return
		}
		_ = route.Continue()
	}
	if err := browserContext.Route("**/*", handler); err != nil {
		log.Printf("[WARN] browser: could not install the resource-blocking router: %v", err)
	}
}

// renderOptions customizes a single browser render.
type renderOptions struct {
	// cookies are added to the shared browser context before navigation, so a
	// user-exported session (COOKIE_FILE_PATH) can authenticate rendered pages.
	cookies []*http.Cookie
	// networkIdle overrides the bounded network-idle wait; zero keeps the default.
	networkIdle time.Duration
	// settle overrides the fixed post-navigation wait; zero keeps the default.
	settle time.Duration
	// timeout bounds this single render (navigation + settle); zero keeps the
	// scraper's default browser timeout.
	timeout time.Duration
}

type browserRenderer interface {
	Render(ctx context.Context, targetURL string, opts renderOptions) (string, error)
	Close() error
}

// documentPrinter is an optional capability of a browser renderer: printing a
// self-contained HTML document to PDF. It is type-asserted rather than required, so
// renderers that only scrape (and the test doubles for them) stay unchanged.
type documentPrinter interface {
	RenderHTMLToPDF(ctx context.Context, html string) ([]byte, error)
}

// footeredDocumentPrinter is the optional running-footer counterpart of
// documentPrinter: printing a document with a footer on every page (the branded
// page numbers a document's own footer element cannot provide). Also
// type-asserted, so a renderer without it degrades to the plain print.
type footeredDocumentPrinter interface {
	RenderHTMLToPDFFootered(ctx context.Context, html, footerHTML string) ([]byte, error)
}

// playwrightRenderer keeps one shared browser context so cookies earned by
// passing a JavaScript interstitial (for example a clearance cookie) are reused
// by later renders instead of being discarded after every page. A fresh page is
// created per render, which is safe to do concurrently.
type playwrightRenderer struct {
	playwright *playwright.Playwright
	browser    playwright.Browser
	timeout    time.Duration

	mu      sync.Mutex
	context playwright.BrowserContext
}

func newPlaywrightRenderer(timeout time.Duration) (browserRenderer, error) {
	return newPlaywrightRendererWithProxy(timeout, "")
}

// newPlaywrightRendererWithProxy launches the shared browser optionally through
// an HTTP or SOCKS proxy. Playwright accepts the credentials inline in the
// server URL ("http://user:pass@host:port"), matching WEEDOO_PROXY_URL's
// format. Only callers whose browser visits a single host (the WeeDoo client)
// pass a proxy — the general scraper's browser serves every host and stays
// direct.
func newPlaywrightRendererWithProxy(timeout time.Duration, proxyServer string) (browserRenderer, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("start Playwright: %w", err)
	}

	launchOptions := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		// Use the full Chromium build rather than Playwright's default headless
		// shell: the shell crashes on some hosts (the page target dies with
		// "Target crashed" as soon as a page is opened), and the full browser
		// also presents a more realistic fingerprint, which matters when a page
		// gates its content behind a JavaScript interstitial.
		Channel: playwright.String("chromium"),
		Args:    []string{"--disable-dev-shm-usage"},
	}
	if proxyServer != "" {
		launchOptions.Proxy = &playwright.Proxy{Server: proxyServer}
	}
	browser, err := pw.Chromium.Launch(launchOptions)
	if err != nil {
		_ = pw.Stop()
		return nil, fmt.Errorf("launch Chromium: %w", err)
	}
	return &playwrightRenderer{playwright: pw, browser: browser, timeout: timeout}, nil
}

// sharedContext returns the reused browser context, creating it on first use.
func (r *playwrightRenderer) sharedContext() (playwright.BrowserContext, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.context != nil {
		return r.context, nil
	}
	browserContext, err := r.browser.NewContext(playwright.BrowserNewContextOptions{
		Locale:   playwright.String("en-US"),
		Viewport: &playwright.Size{Width: 1440, Height: 900},
	})
	if err != nil {
		return nil, fmt.Errorf("create browser context: %w", err)
	}
	blockHeavyResources(browserContext)
	r.context = browserContext
	return r.context, nil
}

// settleBudget returns how long a settle wait may run: requested, capped to the
// remaining render budget minus the reserve kept for reading the rendered HTML.
// It returns 0 when that leaves nothing, so the caller skips the wait entirely.
func settleBudget(remaining, requested time.Duration) time.Duration {
	remaining -= browserReadReserve
	if remaining <= 0 {
		return 0
	}
	if remaining < requested {
		return remaining
	}
	return requested
}

// remainingMillis reports how long a settle wait may run for ctx, in
// milliseconds. Capping to the *remaining* budget (rather than the full render
// timeout) is what stops the settle waits from pushing a render past its context
// deadline, which would otherwise report an already-loaded page as a failure.
func remainingMillis(ctx context.Context, requested time.Duration) float64 {
	if requested <= 0 {
		return 0
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return float64(requested.Milliseconds())
	}
	return float64(settleBudget(time.Until(deadline), requested).Milliseconds())
}

// addCookies bridges the scraper's HTTP cookie jar into the shared browser
// context. The jar belongs to the Go HTTP client and Chromium keeps its own
// store, so without this step a user-exported session (COOKIE_FILE_PATH)
// could never authenticate a rendered page. Cookies are merged under the
// render mutex (AddCookies is not documented as concurrency-safe) and persist
// in the context, matching how interstitial clearance cookies are reused.
func (r *playwrightRenderer) addCookies(browserContext playwright.BrowserContext, targetURL string, cookies []*http.Cookie) error {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("parse target URL: %w", err)
	}
	converted := make([]playwright.OptionalCookie, 0, len(cookies))
	host := parsed.Hostname()
	for _, c := range cookies {
		if c == nil || c.Name == "" || c.Value == "" {
			continue
		}
		domain := strings.TrimPrefix(c.Domain, ".")
		if domain == "" {
			domain = host
		}
		path := c.Path
		if path == "" {
			path = "/"
		}
		cookie := playwright.OptionalCookie{Name: c.Name, Value: c.Value, Domain: &domain, Path: &path}
		secure := c.Secure
		httpOnly := c.HttpOnly
		cookie.Secure = &secure
		cookie.HttpOnly = &httpOnly
		switch c.SameSite {
		case http.SameSiteStrictMode:
			cookie.SameSite = playwright.SameSiteAttributeStrict
		case http.SameSiteNoneMode:
			cookie.SameSite = playwright.SameSiteAttributeNone
		default:
			cookie.SameSite = playwright.SameSiteAttributeLax
		}
		converted = append(converted, cookie)
	}
	if len(converted) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := browserContext.AddCookies(converted); err != nil {
		return err
	}
	return nil
}

func (r *playwrightRenderer) Render(ctx context.Context, targetURL string, opts renderOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	browserContext, err := r.sharedContext()
	if err != nil {
		return "", err
	}
	if len(opts.cookies) > 0 {
		if err := r.addCookies(browserContext, targetURL, opts.cookies); err != nil {
			return "", fmt.Errorf("bridge session cookies: %w", err)
		}
	}

	page, err := browserContext.NewPage()
	if err != nil {
		return "", fmt.Errorf("create browser page: %w", err)
	}
	defer page.Close()

	// A render must never overrun its attempt budget: cap navigation at the
	// context deadline when it is shorter than the renderer default.
	gotoTimeout := r.timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < gotoTimeout {
			gotoTimeout = remaining
		}
	}
	if _, err = page.Goto(targetURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(float64(gotoTimeout.Milliseconds())),
	}); err != nil {
		return "", fmt.Errorf("navigate browser page: %w", err)
	}

	// Let the page settle. This is what gives a JavaScript interstitial time to
	// run to completion and set its clearance cookie, and hydration effects time
	// to finish. Both waits are best-effort and capped to what is left of the
	// render budget, so settling can never push the render past its deadline and
	// report an already-loaded page as a failure.
	//
	// A page that already holds its content needs neither wait: measuring once and
	// skipping saves the better part of five seconds on the many corporate pages
	// that render server-side but still ship a JavaScript shell. The hard-URL
	// ladder's extended-settle step asks for longer waits explicitly, so an
	// explicit request always waits.
	networkIdle := opts.networkIdle
	if networkIdle <= 0 {
		networkIdle = browserNetworkIdleTimeout
	}
	settle := opts.settle
	if settle <= 0 {
		settle = browserSettleTimeout
	}
	if visibleTextLength(page) < renderReadyRunes || opts.networkIdle > 0 || opts.settle > 0 {
		if wait := remainingMillis(ctx, networkIdle); wait > 0 {
			_ = page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
				State:   playwright.LoadStateNetworkidle,
				Timeout: playwright.Float(wait),
			})
		}
		if wait := remainingMillis(ctx, settle); wait > 0 {
			page.WaitForTimeout(wait)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	html, err := page.Content()
	if err != nil {
		return "", fmt.Errorf("read rendered HTML: %w", err)
	}
	return html, nil
}

// visibleTextLength reports how much text the page currently shows, or 0 when it
// cannot be measured. A failed measurement must never fail a render: the caller
// simply falls back to waiting, which is what it would have done anyway.
func visibleTextLength(page playwright.Page) int {
	value, err := page.Evaluate("() => (document.body ? document.body.innerText : '').length")
	if err != nil {
		return 0
	}
	switch length := value.(type) {
	case float64:
		return int(length)
	case int:
		return length
	}
	return 0
}

// RenderHTMLToPDF prints a self-contained HTML document to PDF, which is how a
// finished run becomes a shareable file without adding a PDF library: the browser
// the server already ships does the typesetting.
//
// The document must be self-contained (inline CSS, no remote assets): the shared
// context blocks image/font/stylesheet requests, so anything linked would be
// missing from the printed page.
func (r *playwrightRenderer) RenderHTMLToPDF(ctx context.Context, html string) ([]byte, error) {
	return r.printDocument(ctx, html, "")
}

// RenderHTMLToPDFFootered prints a self-contained HTML document with a running
// footer on every page. Chromium renders the footer template in the bottom page
// margin — outside the document's own @page box — which is how a report gets
// its brand line and page numbers on every page rather than only at the end.
func (r *playwrightRenderer) RenderHTMLToPDFFootered(ctx context.Context, html, footerHTML string) ([]byte, error) {
	return r.printDocument(ctx, html, footerHTML)
}

// printDocument is the shared print path: load, settle, print.
func (r *playwrightRenderer) printDocument(ctx context.Context, html, footerHTML string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(html) == "" {
		return nil, fmt.Errorf("nothing to print")
	}

	browserContext, err := r.sharedContext()
	if err != nil {
		return nil, err
	}
	page, err := browserContext.NewPage()
	if err != nil {
		return nil, fmt.Errorf("create browser page: %w", err)
	}
	defer page.Close()

	if err := page.SetContent(html, playwright.PageSetContentOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(float64(browserPrintTimeout.Milliseconds())),
	}); err != nil {
		return nil, fmt.Errorf("load document for printing: %w", err)
	}
	if wait := remainingMillis(ctx, browserPrintSettleTimeout); wait > 0 {
		page.WaitForTimeout(wait)
	}

	// Page geometry lives here rather than in the document's @page rule:
	// Chromium draws the running footer inside these print margins, and a CSS
	// @page margin would override them and drop the footer. The same margins
	// apply without a footer, so both paths print identical pages.
	options := playwright.PagePdfOptions{
		Format:          playwright.String("A4"),
		PrintBackground: playwright.Bool(true),
		Margin: &playwright.Margin{
			Top:    playwright.String("16mm"),
			Bottom: playwright.String("18mm"),
			Left:   playwright.String("14mm"),
			Right:  playwright.String("14mm"),
		},
	}
	if strings.TrimSpace(footerHTML) != "" {
		options.DisplayHeaderFooter = playwright.Bool(true)
		options.FooterTemplate = playwright.String(footerHTML)
		// DisplayHeaderFooter turns on Chromium's stock header too (date and
		// document title); an empty header template keeps the pages clean.
		options.HeaderTemplate = playwright.String(`<div></div>`)
	}

	pdf, err := page.PDF(options)
	if err != nil {
		return nil, fmt.Errorf("print document to PDF: %w", err)
	}
	if len(pdf) == 0 {
		return nil, fmt.Errorf("printing produced an empty document")
	}
	return pdf, nil
}

func (r *playwrightRenderer) Close() error {
	r.mu.Lock()
	browserContext := r.context
	r.context = nil
	r.mu.Unlock()

	var contextErr error
	if browserContext != nil {
		contextErr = browserContext.Close()
	}
	browserErr := r.browser.Close()
	playwrightErr := r.playwright.Stop()
	if contextErr != nil {
		return contextErr
	}
	if browserErr != nil {
		return browserErr
	}
	return playwrightErr
}
