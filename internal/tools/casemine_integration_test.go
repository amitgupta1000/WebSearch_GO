package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestCasemineLiveSearch validates the client against the live site: the plain
// fetch with the scraper's browser-like headers (the measured access path), the
// parser on a real results page, and — as the measurement that justifies the
// fetch ladder — a headless-Chromium render of the homepage, which the site
// answers with its 117-byte nginx 403 because of the HeadlessChrome User-Agent.
// Runs only with CASMINE_INTEGRATION=1, like the other opt-in integration tests.
func TestCasemineLiveSearch(t *testing.T) {
	if os.Getenv("CASMINE_INTEGRATION") != "1" {
		t.Skip("CASMINE_INTEGRATION=1 not set; skipping live Casemine search")
	}

	scraper := NewScraperWithBrowser(nil, 45*time.Second, true)
	defer scraper.Close()

	// 1. The plain path: fetch + parse a real results page.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := NewCasemineClient(scraper)
	hits, err := client.Search(ctx, `"Tata Consultancy Services"`, 1, 10)
	if err != nil {
		t.Fatalf("live search failed: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("live search returned no hits")
	}
	for _, hit := range hits {
		if hit.DocID == "" {
			t.Errorf("hit %q carries no document id", hit.Title)
		}
		if hit.Title == "" {
			t.Error("a parsed hit carries no title")
		}
	}
	t.Logf("live search: %d hits, first = %q (%s, %s)", len(hits), hits[0].Title, hits[0].Court, hits[0].Date)

	// 2. The measurement behind the ladder: headless Chromium's own UA is
	// answered with the flat nginx 403, so the render path alone is not
	// sufficient access — it is the fallback, and the cookie-bridged session
	// (COOKIE_FILE_PATH) is what would upgrade it if the UA wall ever extends
	// to browser fetches.
	if scraper.BrowserAvailable() {
		renderCtx, renderCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer renderCancel()
		rendered, renderErr := scraper.RenderHTML(renderCtx, "https://www.casemine.com/")
		if renderErr != nil {
			t.Logf("homepage render failed (recorded, not asserted): %v", renderErr)
		} else {
			t.Logf("headless-Chromium homepage render: %d bytes — the UA wall measured live", len(rendered))
			if !casemineLooksBlocked(rendered) {
				t.Log("note: the wall no longer holds for headless Chromium; the fetch ladder's order may deserve a re-measure")
			}
		}
	}
}
