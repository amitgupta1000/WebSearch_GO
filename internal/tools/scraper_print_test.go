package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// printingRenderer is a browserRenderer that also implements the optional
// documentPrinting capability, standing in for real Chromium.
type printingRenderer struct {
	mu        sync.Mutex
	documents []string
	pdf       []byte
	err       error
}

func (*printingRenderer) Render(context.Context, string) (string, error) {
	return "", fmt.Errorf("scrape rendering is not used by this test")
}

func (*printingRenderer) Close() error { return nil }

func (r *printingRenderer) RenderHTMLToPDF(_ context.Context, html string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.documents = append(r.documents, html)
	if r.err != nil {
		return nil, r.err
	}
	return r.pdf, nil
}

func (r *printingRenderer) printed() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.documents...)
}

func TestScraperRenderHTMLToPDFUsesTheRenderer(t *testing.T) {
	renderer := &printingRenderer{pdf: []byte("%PDF-1.4 test")}
	scraper := newTestScraper()
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return renderer, nil }
	defer scraper.Close()

	if !scraper.DocumentPrinter() {
		t.Fatal("expected the scraper to report document printing support")
	}

	pdf, err := scraper.RenderHTMLToPDF(context.Background(), "<html><body>report</body></html>")
	if err != nil {
		t.Fatalf("RenderHTMLToPDF: %v", err)
	}
	if string(pdf) != "%PDF-1.4 test" {
		t.Errorf("pdf = %q, want the renderer's bytes", pdf)
	}
	if printed := renderer.printed(); len(printed) != 1 || !strings.Contains(printed[0], "report") {
		t.Errorf("printed documents = %v, want the supplied document", printed)
	}
}

func TestScraperRenderHTMLToPDFRejectsEmptyDocument(t *testing.T) {
	scraper := newTestScraper()
	defer scraper.Close()

	if _, err := scraper.RenderHTMLToPDF(context.Background(), "   "); err == nil {
		t.Error("expected an error when there is nothing to print")
	}
}

func TestScraperReportsPrintingUnavailableForScrapeOnlyRenderers(t *testing.T) {
	// A scrape-only renderer must be reported honestly rather than failing later,
	// because the exporter relies on this to describe what it can produce.
	scraper := newTestScraper()
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return &fakeBrowserRenderer{}, nil }
	defer scraper.Close()

	if scraper.DocumentPrinter() {
		t.Error("a scrape-only renderer must not claim printing support")
	}
	if _, err := scraper.RenderHTMLToPDF(context.Background(), "<html></html>"); err == nil {
		t.Error("expected an error when the renderer cannot print")
	}
}

func TestScraperRenderHTMLToPDFReportsPrinterFailure(t *testing.T) {
	renderer := &printingRenderer{err: fmt.Errorf("Target crashed")}
	scraper := newTestScraper()
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return renderer, nil }
	defer scraper.Close()

	_, err := scraper.RenderHTMLToPDF(context.Background(), "<html><body>x</body></html>")
	if err == nil || !strings.Contains(err.Error(), "Target crashed") {
		t.Errorf("err = %v, want the printer's failure surfaced", err)
	}
}
