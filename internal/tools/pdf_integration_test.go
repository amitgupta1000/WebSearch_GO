package tools

import (
	"os"
	"strings"
	"testing"

	"github.com/mxschmitt/playwright-go"
)

const integrationPageHTML = `<!DOCTYPE html><html><body>
<h1>KLJ Resources Limited</h1>
<h2>Procurement Department</h2>
<ul><li>Rajesh Zaveri - Head of Procurement</li></ul>
</body></html>`

// TestLocalPDFParserReadsRealWorldPDF is an opt-in check that the local parser reads a
// real PDF. Chromium's output has Flate-compressed content streams and embedded font
// subsets, which is what published filings look like; the synthetic fixture used by
// pdf_test.go is deliberately trivial, so this test covers that gap.
// Enable it with PLAYWRIGHT_INTEGRATION=1.
func TestLocalPDFParserReadsRealWorldPDF(t *testing.T) {
	if os.Getenv("PLAYWRIGHT_INTEGRATION") != "1" {
		t.Skip("set PLAYWRIGHT_INTEGRATION=1 to run the Playwright integration test")
	}

	pw, err := playwright.Run()
	if err != nil {
		t.Fatalf("playwright.Run: %v", err)
	}
	defer pw.Stop()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Channel:  playwright.String("chromium"),
	})
	if err != nil {
		t.Fatalf("launch chromium: %v", err)
	}
	defer browser.Close()

	browserContext, err := browser.NewContext()
	if err != nil {
		t.Fatalf("new browser context: %v", err)
	}
	defer browserContext.Close()

	page, err := browserContext.NewPage()
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.SetContent(integrationPageHTML); err != nil {
		t.Fatalf("set content: %v", err)
	}
	document, err := page.PDF(playwright.PagePdfOptions{Format: playwright.String("A4")})
	if err != nil {
		t.Fatalf("render pdf: %v", err)
	}

	text, err := ExtractPDFText(document, 0)
	if err != nil {
		t.Fatalf("ExtractPDFText on a real PDF: %v", err)
	}
	for _, want := range []string{"KLJ Resources Limited", "Procurement Department", "Rajesh Zaveri"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in the parsed text, got %q", want, text)
		}
	}
}
