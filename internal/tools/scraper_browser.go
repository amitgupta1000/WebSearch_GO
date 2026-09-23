package tools

import (
	"context"
	"fmt"
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
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("start Playwright: %w", err)
	}

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		// Use the full Chromium build rather than Playwright's default headless
		// shell: the shell crashes on some hosts (the page target dies with
		// "Target crashed" as soon as a page is opened), and the full browser
		// also presents a more realistic fingerprint, which matters when a page
		// gates its content behind a JavaScript interstitial.
		Channel: playwright.String("chromium"),
		Args:    []string{"--disable-dev-shm-usage"},
	})
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
	r.context = browserContext
	return r.context, nil
}

// boundedWaitMillis caps a wait so it always fits inside the render context's
// remaining budget; an already-expired context yields zero.
func boundedWaitMillis(ctx context.Context, requested time.Duration) float64 {
	if requested <= 0 {
		return 0
	}
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < requested {
			requested = remaining
		}
	}
	if requested <= 0 {
		return 0
	}
	return float64(requested.Milliseconds())
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
	// to finish. Both waits are best-effort and capped by the render budget.
	networkIdle := opts.networkIdle
	if networkIdle <= 0 {
		networkIdle = browserNetworkIdleTimeout
	}
	_ = page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State:   playwright.LoadStateNetworkidle,
		Timeout: playwright.Float(boundedWaitMillis(ctx, networkIdle)),
	})
	settle := opts.settle
	if settle <= 0 {
		settle = 750 * time.Millisecond
	}
	if wait := boundedWaitMillis(ctx, settle); wait > 0 {
		page.WaitForTimeout(wait)
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
