package tools

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mxschmitt/playwright-go"
)

const browserFallbackTimeout = 10 * time.Second

// browserNetworkIdleTimeout bounds how long a render waits for in-flight
// requests (analytics, hydration, JavaScript interstitials) to settle.
const browserNetworkIdleTimeout = 4 * time.Second

type browserRenderer interface {
	Render(context.Context, string) (string, error)
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

// settleMillis caps a settle duration so it always fits inside the render budget.
func (r *playwrightRenderer) settleMillis(requested time.Duration) float64 {
	if r.timeout <= 0 {
		return 0
	}
	if requested > r.timeout {
		requested = r.timeout
	}
	return float64(requested.Milliseconds())
}

func (r *playwrightRenderer) Render(ctx context.Context, targetURL string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	browserContext, err := r.sharedContext()
	if err != nil {
		return "", err
	}

	page, err := browserContext.NewPage()
	if err != nil {
		return "", fmt.Errorf("create browser page: %w", err)
	}
	defer page.Close()

	if _, err = page.Goto(targetURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(float64(r.timeout.Milliseconds())),
	}); err != nil {
		return "", fmt.Errorf("navigate browser page: %w", err)
	}

	// Let the page settle. This is what gives a JavaScript interstitial time to
	// run to completion and set its clearance cookie, and hydration effects time
	// to finish. Both waits are best-effort and capped by the render budget.
	_ = page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State:   playwright.LoadStateNetworkidle,
		Timeout: playwright.Float(r.settleMillis(browserNetworkIdleTimeout)),
	})
	if wait := r.settleMillis(750 * time.Millisecond); wait > 0 {
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
