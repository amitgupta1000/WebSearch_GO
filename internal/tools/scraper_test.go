package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestScrapeURLExtractsContentWithoutDuplicates(t *testing.T) {
	page := `<!DOCTYPE html><html><head><title>Acme Team</title></head><body>
		<nav>Menu items and navigation links here</nav>
		<article>
			<h1>Acme Team Page</h1>
			<p>First paragraph about Acme Corporation.</p>
			<p>Second paragraph about Acme Corporation.</p>
		</article>
		<footer>Contact Acme at footer@example.com for more info today.</footer>
	</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	s := NewScraper(nil, time.Second)
	res, err := s.ScrapeURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ScrapeURL returned error: %v", err)
	}

	if res.Title != "Acme Team" {
		t.Errorf("title = %q, want %q", res.Title, "Acme Team")
	}
	if n := strings.Count(res.Content, "First paragraph about Acme Corporation."); n != 1 {
		t.Errorf("paragraph emitted %d times, want 1 (nested duplication): %q", n, res.Content)
	}
	if n := strings.Count(res.Content, "Second paragraph about Acme Corporation."); n != 1 {
		t.Errorf("paragraph emitted %d times, want 1: %q", n, res.Content)
	}
	if strings.Contains(res.Content, "footer@example.com") {
		t.Error("footer noise should have been removed")
	}
}

func TestScrapeURLExtractsMetadataAndRemovesBoilerplate(t *testing.T) {
	page := `<!DOCTYPE html><html><head><title>Leadership</title><link rel="canonical" href="/leadership"></head><body>
		<div class="cookie-banner">Accept cookies to continue</div>
		<aside class="sidebar">Related stories and ads</aside>
		<main><h1>Executive Leadership</h1><h2>Chief Executive Officer</h2><figure><img src="/images/ceo.jpg" alt="Acme CEO portrait" title="Chief executive officer"><figcaption>Acme chief executive officer</figcaption></figure><p>Acme's chief executive officer leads the company and its global operations.</p></main>
		<script type="application/ld+json">{"@type":"Organization","name":"Acme"}</script>
	</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	result, err := NewScraper(nil, time.Second).ScrapeURL(context.Background(), srv.URL+"/page")
	if err != nil {
		t.Fatalf("ScrapeURL returned error: %v", err)
	}
	if !strings.HasSuffix(result.CanonicalURL, "/leadership") {
		t.Errorf("canonical URL = %q", result.CanonicalURL)
	}
	if len(result.Headings) != 2 || len(result.JSONLD) != 1 {
		t.Errorf("expected headings and JSON-LD metadata, got headings=%v jsonld=%v", result.Headings, result.JSONLD)
	}
	if len(result.Images) != 1 || !strings.HasSuffix(result.Images[0].URL, "/images/ceo.jpg") || result.Images[0].Caption != "Acme chief executive officer" {
		t.Errorf("expected image metadata, got %+v", result.Images)
	}
	if strings.Contains(result.Content, "Accept cookies") || strings.Contains(result.Content, "Related stories") {
		t.Errorf("boilerplate was not removed: %q", result.Content)
	}
	if result.QualityScore <= 0.5 {
		t.Errorf("expected quality score above 0.5, got %v", result.QualityScore)
	}
}

func TestScrapeURLClassifiesPressAndHoldChallenge(t *testing.T) {
	page := `<!DOCTYPE html><html><head><title>Press and Hold</title></head><body><h1>Press and Hold</h1><p>Press and hold to confirm you are human.</p></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	result, err := NewScraper(nil, time.Second).ScrapeURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ScrapeURL returned error: %v", err)
	}
	if !result.Blocked || result.BlockReason != "press-and-hold challenge" || result.Extractor != "blocked-page" {
		t.Fatalf("expected blocked challenge metadata, got %+v", result)
	}
}

func TestScrapeURLRejectsNonHTMLContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4 not really a pdf"))
	}))
	defer srv.Close()

	s := NewScraper(nil, time.Second)
	if _, err := s.ScrapeURL(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error for non-HTML content type")
	}
}

func TestRuneSafeTruncate(t *testing.T) {
	short := strings.Repeat("é", 100) // 200 bytes, under the limit
	if got := runeSafeTruncate(short, 1000); got != short {
		t.Fatal("strings shorter than the limit must be returned unchanged")
	}

	long := strings.Repeat("é", 30000) // 60000 bytes; each rune is 2 bytes
	cut := runeSafeTruncate(long, 40000)
	if !utf8.ValidString(cut) {
		t.Fatal("truncation produced invalid UTF-8 (split a rune)")
	}
	if max := 40000 + len("\n\n...[content truncated]..."); len(cut) > max {
		t.Fatalf("truncated content too long: %d bytes (max %d)", len(cut), max)
	}
}

func TestIsHTMLContentType(t *testing.T) {
	cases := map[string]bool{
		"text/html; charset=utf-8": true,
		"TEXT/HTML":                true,
		"application/xhtml+xml":    true,
		"application/pdf":          false,
		"application/octet-stream": false,
		"image/png":                false,
	}
	for ct, want := range cases {
		if got := isHTMLContentType(ct); got != want {
			t.Errorf("isHTMLContentType(%q) = %v, want %v", ct, got, want)
		}
	}
}
