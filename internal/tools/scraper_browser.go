package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/mxschmitt/playwright-go"
)

const browserFallbackTimeout = 10 * time.Second

type browserRenderer interface {
	Render(context.Context, string) (string, error)
	Close() error
}

type playwrightRenderer struct {
	playwright *playwright.Playwright
	browser    playwright.Browser
	timeout    time.Duration
}

func newPlaywrightRenderer(timeout time.Duration) (browserRenderer, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("start Playwright: %w", err)
	}

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Args:     []string{"--disable-dev-shm-usage"},
	})
	if err != nil {
		_ = pw.Stop()
		return nil, fmt.Errorf("launch Chromium: %w", err)
	}
	return &playwrightRenderer{playwright: pw, browser: browser, timeout: timeout}, nil
}

func (r *playwrightRenderer) Render(ctx context.Context, targetURL string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	browserContext, err := r.browser.NewContext(playwright.BrowserNewContextOptions{
		Locale:   playwright.String("en-US"),
		Viewport: &playwright.Size{Width: 1440, Height: 900},
	})
	if err != nil {
		return "", fmt.Errorf("create browser context: %w", err)
	}
	defer browserContext.Close()

	page, err := browserContext.NewPage()
	if err != nil {
		return "", fmt.Errorf("create browser page: %w", err)
	}
	defer page.Close()

	_, err = page.Goto(targetURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(float64(r.timeout.Milliseconds())),
	})
	if err != nil {
		return "", fmt.Errorf("navigate browser page: %w", err)
	}

	// Allow common hydration effects to settle, without interacting with any
	// challenge, CAPTCHA, login, or press-and-hold controls.
	wait := 750 * time.Millisecond
	if r.timeout < wait {
		wait = r.timeout
	}
	if wait > 0 {
		page.WaitForTimeout(float64(wait.Milliseconds()))
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
	browserErr := r.browser.Close()
	playwrightErr := r.playwright.Stop()
	if browserErr != nil {
		return browserErr
	}
	return playwrightErr
}
