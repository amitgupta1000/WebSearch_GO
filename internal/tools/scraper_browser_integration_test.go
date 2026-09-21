package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestBrowserRendererReusesInterstitialClearance drives real Playwright/Chromium
// and is skipped unless PLAYWRIGHT_INTEGRATION=1, so the default suite stays fast
// and does not require a browser download.
//
// It serves a page that behaves like the common JavaScript interstitial: the
// first document load sets a clearance cookie and reloads itself. The shared
// browser context must retain that cookie, so the *second* render is served the
// real page on its first request rather than repeating the interstitial.
func TestBrowserRendererReusesInterstitialClearance(t *testing.T) {
	if os.Getenv("PLAYWRIGHT_INTEGRATION") != "1" {
		t.Skip("set PLAYWRIGHT_INTEGRATION=1 to run the Playwright integration test")
	}

	realContent := `<html><head><title>Settled Page</title></head><body><main><h1>Settled heading</h1><p>` +
		strings.Repeat("Settled body text for the integration check. ", 8) + `</p></main></body></html>`

	var browserRequests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Count only browser-originated document loads: the scraper's own HTTP
		// fetch has a separate cookie jar and would otherwise be counted too.
		// Browsers always send Sec-Fetch-Mode; Go's http.Client does not.
		if r.Header.Get("Sec-Fetch-Mode") != "" {
			atomic.AddInt32(&browserRequests, 1)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if cookie, err := r.Cookie("cleared"); err == nil && cookie.Value == "1" {
			_, _ = io.WriteString(w, realContent)
			return
		}
		_, _ = fmt.Fprint(w, `<html><head><title>Checking your browser</title></head><body>`+
			`<script>document.cookie="cleared=1; path=/"; location.reload();</script>`+
			`<p>Checking your browser before accessing the site.</p></body></html>`)
	}))
	defer srv.Close()

	scraper := NewScraperWithBrowser(nil, 15*time.Second, true, WithPrivateHostAccess())
	defer scraper.Close()

	first, err := scraper.ScrapeURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("first ScrapeURL returned error: %v", err)
	}
	if first.FallbackStatus != "succeeded" {
		t.Fatalf("expected the browser fallback to resolve the interstitial, got %+v", first)
	}
	if !strings.Contains(first.Content, "Settled body text") {
		t.Fatalf("expected settled content after the interstitial cleared, got %q", first.Content)
	}
	// The short <h1> is below minTextRunes, so it is reported as a heading only.
	if len(first.Headings) != 1 || first.Headings[0] != "Settled heading" {
		t.Fatalf("expected the settled page heading, got %+v", first.Headings)
	}
	afterFirst := atomic.LoadInt32(&browserRequests)
	if afterFirst < 2 {
		t.Fatalf("expected the interstitial to reload at least once in the browser, saw %d browser requests", afterFirst)
	}

	second, err := scraper.ScrapeURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("second ScrapeURL returned error: %v", err)
	}
	if !strings.Contains(second.Content, "Settled body text") {
		t.Fatalf("expected settled content on the second render, got %q", second.Content)
	}

	// The clearance cookie earned during the first render must still be present,
	// so the second render is served the real page immediately.
	if got := atomic.LoadInt32(&browserRequests) - afterFirst; got != 1 {
		t.Errorf("second render issued %d browser requests, want 1 (clearance cookie was not reused across renders)", got)
	}
}
