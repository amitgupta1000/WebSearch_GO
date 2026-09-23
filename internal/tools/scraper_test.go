package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

type fakeBrowserRenderer struct {
	html  string
	err   error
	calls int
}

func (r *fakeBrowserRenderer) Render(context.Context, string, renderOptions) (string, error) {
	r.calls++
	return r.html, r.err
}

func (*fakeBrowserRenderer) Close() error { return nil }

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

	s := NewScraper(nil, time.Second, WithPrivateHostAccess())
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

	result, err := NewScraper(nil, time.Second, WithPrivateHostAccess()).ScrapeURL(context.Background(), srv.URL+"/page")
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

	result, err := NewScraper(nil, time.Second, WithPrivateHostAccess()).ScrapeURL(context.Background(), srv.URL)
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

	s := NewScraper(nil, time.Second, WithPrivateHostAccess())
	if _, err := s.ScrapeURL(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error for non-HTML content type")
	}
}

func TestScrapeURLUsesBrowserForJavaScriptShell(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><div id="app"></div></body></html>`))
	}))
	defer srv.Close()

	renderer := &fakeBrowserRenderer{html: `<html><head><title>Rendered Team</title></head><body><main><h1>Rendered leadership</h1><p>` + strings.Repeat("JavaScript hydrated leadership content. ", 8) + `</p></main></body></html>`}
	scraper := NewScraperWithBrowser(nil, time.Second, true, WithPrivateHostAccess())
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return renderer, nil }

	result, err := scraper.ScrapeURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ScrapeURL returned error: %v", err)
	}
	if renderer.calls != 1 || result.Extractor != "playwright-content-root" || result.FallbackStatus != "succeeded" {
		t.Fatalf("expected successful browser fallback, calls=%d result=%+v", renderer.calls, result)
	}
	if !strings.Contains(result.Content, "JavaScript hydrated leadership content") {
		t.Errorf("rendered content was not extracted: %q", result.Content)
	}
}

func TestScrapeURLKeepsHTTPResultWhenBrowserFails(t *testing.T) {
	page := `<html><body><main><h1>Short page</h1><p>Small shell.</p></main></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	renderer := &fakeBrowserRenderer{err: context.DeadlineExceeded}
	scraper := NewScraperWithBrowser(nil, time.Second, true, WithPrivateHostAccess())
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return renderer, nil }

	result, err := scraper.ScrapeURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ScrapeURL returned error: %v", err)
	}
	if result.FallbackStatus != "failed" || result.FallbackError == "" || result.Extractor != "goquery-content-root" {
		t.Fatalf("expected HTTP result with fallback failure metadata, got %+v", result)
	}
}

func TestScrapeURLDoesNotBypassRenderedChallenge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><div id="app"></div></body></html>`))
	}))
	defer srv.Close()

	renderer := &fakeBrowserRenderer{html: `<html><head><title>Verification</title></head><body><h1>CAPTCHA</h1><p>Please verify you are human.</p></body></html>`}
	scraper := NewScraperWithBrowser(nil, time.Second, true, WithPrivateHostAccess())
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return renderer, nil }

	result, err := scraper.ScrapeURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ScrapeURL returned error: %v", err)
	}
	if !result.Blocked || result.FallbackStatus != "blocked" || result.BlockReason != "human-verification challenge" {
		t.Fatalf("expected rendered challenge to remain blocked, got %+v", result)
	}
}

func TestScrapeURLReturnsErrorWhenHTTPAndBrowserFail(t *testing.T) {
	renderer := &fakeBrowserRenderer{err: errors.New("browser unavailable")}
	scraper := NewScraperWithBrowser(nil, time.Second, true, WithPrivateHostAccess())
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return renderer, nil }
	scraper.fetchAttempts = 1 // keep this test fast; retries are covered separately

	if _, err := scraper.ScrapeURL(context.Background(), "http://127.0.0.1:1/unavailable"); err == nil {
		t.Fatal("expected combined HTTP and browser error")
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

// newHTMLServer serves page as UTF-8 HTML and tears the server down with the test.
func newHTMLServer(t *testing.T, page string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// waitFor polls condition until it holds or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return condition()
}

// Inline JavaScript must never influence challenge detection, and script source
// must never leak into the extracted content.
func TestScrapeURLIgnoresChallengeKeywordsInScript(t *testing.T) {
	page := `<html><head><title>Quarterly Report</title></head><body>
		<main>
			<h1>Quarterly Report</h1>
			<script>var t="captcha";function check(){return "checking your browser";}</script>
			<style>.access-denied{color:red}</style>
			<p>` + strings.Repeat("Revenue grew across all regions this quarter. ", 8) + `</p>
		</main>
	</body></html>`
	srv := newHTMLServer(t, page)

	res, err := NewScraper(nil, time.Second, WithPrivateHostAccess()).ScrapeURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ScrapeURL returned error: %v", err)
	}
	if res.Blocked {
		t.Fatalf("legitimate page was marked blocked (%q) by script/style text", res.BlockReason)
	}
	if strings.Contains(res.Content, "var t=") || strings.Contains(res.Content, "function check") {
		t.Errorf("script source leaked into content: %q", res.Content)
	}
	if !strings.Contains(res.Content, "Revenue grew across all regions") {
		t.Errorf("expected article text, got %q", res.Content)
	}
}

func TestDetectBlockPageIgnoresSubstantivePages(t *testing.T) {
	long := strings.Repeat("This is a long analytical article about bot mitigation. ", 30) +
		"It discusses captcha widgets, access denied errors and unusual traffic spikes."
	if reason := detectBlockPage("Security analysis", long); reason != "" {
		t.Errorf("substantive page classified as %q, want no block", reason)
	}
	if reason := detectBlockPage("Just a moment", "Please verify you are human"); reason != "human-verification challenge" {
		t.Errorf("challenge page classified as %q, want %q", reason, "human-verification challenge")
	}
}

// Legacy encodings must be transcoded, not decoded as UTF-8.
func TestScrapeURLDecodesNonUTF8Document(t *testing.T) {
	// "Café" written in ISO-8859-1: byte 0xE9 is not valid UTF-8.
	page := "<html><head><title>Caf\xe9 Group</title></head><body><main><h1>Caf\xe9 Group</h1><p>" +
		strings.Repeat("Caf\xe9 society meets every week. ", 10) + "</p></main></body></html>"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	res, err := NewScraper(nil, time.Second, WithPrivateHostAccess()).ScrapeURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ScrapeURL returned error: %v", err)
	}
	if !strings.Contains(res.Title, "Café") {
		t.Errorf("title was not transcoded: %q", res.Title)
	}
	if !strings.Contains(res.Content, "Café society") {
		t.Errorf("ISO-8859-1 content was not transcoded: %q", res.Content)
	}
}

// The body-text fallback must not run adjacent blocks together.
func TestScrapeURLKeepsWordBoundariesInBodyFallback(t *testing.T) {
	srv := newHTMLServer(t, `<html><body><main><h1>Short page</h1><p>Small shell.</p></main></body></html>`)

	res, err := NewScraper(nil, time.Second, WithPrivateHostAccess()).ScrapeURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ScrapeURL returned error: %v", err)
	}
	if !strings.Contains(res.Content, "Short page") || !strings.Contains(res.Content, "Small shell.") {
		t.Fatalf("expected both blocks, got %q", res.Content)
	}
	if strings.Contains(res.Content, "pageSmall") {
		t.Errorf("adjacent blocks were concatenated without a separator: %q", res.Content)
	}
}

// A non-HTTP(S) scheme must be rejected before it can reach the browser
// renderer, which would otherwise follow file:// URLs.
func TestScrapeURLRejectsNonHTTPScheme(t *testing.T) {
	renderer := &fakeBrowserRenderer{html: `<html><body><main><h1>Local file</h1></main></body></html>`}
	scraper := NewScraperWithBrowser(nil, time.Second, true, WithPrivateHostAccess())
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return renderer, nil }

	for _, target := range []string{"file:///C:/Windows/win.ini", "file://host/etc/passwd", "ftp://example.com/x"} {
		if _, err := scraper.ScrapeURL(context.Background(), target); err == nil {
			t.Errorf("expected %q to be rejected", target)
		}
	}
	if renderer.calls != 0 {
		t.Errorf("browser renderer was invoked %d times for non-HTTP targets", renderer.calls)
	}
}

// Loopback, private and link-local targets are refused unless explicitly allowed.
func TestScrapeURLRefusesPrivateHostsUnlessOptedIn(t *testing.T) {
	srv := newHTMLServer(t, "<html><head><title>Local</title></head><body><main><h1>Local</h1><p>"+
		strings.Repeat("Local content here. ", 20)+"</p></main></body></html>")

	strict := NewScraper(nil, time.Second)
	if _, err := strict.ScrapeURL(context.Background(), srv.URL); err == nil {
		t.Error("expected a loopback target to be refused by default")
	}
	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/internal",
		"http://localhost:8080/admin",
	} {
		if _, err := strict.ScrapeURL(context.Background(), target); err == nil {
			t.Errorf("expected %q to be refused", target)
		}
	}

	relaxed := NewScraper(nil, time.Second, WithPrivateHostAccess())
	if _, err := relaxed.ScrapeURL(context.Background(), srv.URL); err != nil {
		t.Fatalf("WithPrivateHostAccess should allow a loopback test server: %v", err)
	}
}

// Transient failures are retried; client errors are not.
func TestScrapeURLRetriesTransientStatus(t *testing.T) {
	var attempts int32
	page := "<html><head><title>Recovered</title></head><body><main><h1>Recovered</h1><p>" +
		strings.Repeat("Content recovered after retry. ", 10) + "</p></main></body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n := atomic.AddInt32(&attempts, 1); n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	scraper := NewScraper(nil, time.Second, WithPrivateHostAccess())
	scraper.fetchBackoff = time.Millisecond

	res, err := scraper.ScrapeURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ScrapeURL returned error: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
	if !strings.Contains(res.Content, "Content recovered after retry") {
		t.Errorf("expected recovered content, got %q", res.Content)
	}
}

func TestScrapeURLDoesNotRetryClientError(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	scraper := NewScraper(nil, time.Second, WithPrivateHostAccess())
	scraper.fetchBackoff = time.Millisecond

	if _, err := scraper.ScrapeURL(context.Background(), srv.URL); err == nil {
		t.Fatal("expected an error for HTTP 404")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("attempts = %d, want 1 (404 must not be retried)", got)
	}
}

// An oversized body is flagged rather than silently parsed while incomplete.
func TestScrapeURLFlagsTruncatedBody(t *testing.T) {
	filler := strings.Repeat("padding padding padding padding padding\n", 60000) // > maxBodyBytes
	page := "<html><head><title>Huge</title></head><body><main><h1>Huge</h1><p>Visible marker text.</p>" +
		filler + "</main></body></html>"
	srv := newHTMLServer(t, page)

	res, err := NewScraper(nil, time.Second, WithPrivateHostAccess()).ScrapeURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ScrapeURL returned error: %v", err)
	}
	if !res.ContentTruncated {
		t.Error("expected ContentTruncated for a body larger than maxBodyBytes")
	}
}

type closeCountingRenderer struct {
	mu     sync.Mutex
	closes int
}

func (*closeCountingRenderer) Render(context.Context, string, renderOptions) (string, error) {
	return "", nil
}

func (r *closeCountingRenderer) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closes++
	return nil
}

func (r *closeCountingRenderer) closeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closes
}

func TestScraperCloseIsIdempotent(t *testing.T) {
	renderer := &closeCountingRenderer{}
	scraper := NewScraperWithBrowser(nil, time.Second, true)
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return renderer, nil }

	if _, err := scraper.getBrowser(); err != nil {
		t.Fatalf("getBrowser returned error: %v", err)
	}

	if err := scraper.Close(); err != nil {
		t.Fatalf("first Close returned error: %v", err)
	}
	if err := scraper.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
	if got := renderer.closeCount(); got != 1 {
		t.Errorf("renderer closed %d times, want 1", got)
	}

	// A closed scraper must not hand out a dead renderer.
	if _, err := scraper.ScrapeURL(context.Background(), "http://example.com/"); err == nil {
		t.Error("expected ScrapeURL after Close to fail")
	}
}

func TestScraperInitializesBrowserOnce(t *testing.T) {
	renderer := &closeCountingRenderer{}
	var calls int32
	scraper := NewScraperWithBrowser(nil, time.Second, true)
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) {
		atomic.AddInt32(&calls, 1)
		return renderer, nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := scraper.getBrowser(); err != nil {
				t.Errorf("getBrowser returned error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("newBrowser called %d times, want 1", got)
	}
}

// concurrencyProbeRenderer blocks inside Render until the test releases it and
// records the highest number of simultaneously active renders.
type concurrencyProbeRenderer struct {
	mu      sync.Mutex
	active  int
	peakN   int
	release chan struct{}
	html    string
}

func (r *concurrencyProbeRenderer) Render(context.Context, string, renderOptions) (string, error) {
	r.mu.Lock()
	r.active++
	if r.active > r.peakN {
		r.peakN = r.active
	}
	r.mu.Unlock()

	<-r.release

	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	return r.html, nil
}

func (*concurrencyProbeRenderer) Close() error { return nil }

func (r *concurrencyProbeRenderer) peak() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peakN
}

func TestScraperBoundsConcurrentRenders(t *testing.T) {
	srv := newHTMLServer(t, `<html><body><div id="app"></div></body></html>`)
	renderer := &concurrencyProbeRenderer{
		release: make(chan struct{}),
		html: `<html><head><title>Rendered</title></head><body><main><h1>Rendered</h1><p>` +
			strings.Repeat("rendered content ", 20) + `</p></main></body></html>`,
	}

	scraper := NewScraperWithBrowser(nil, 5*time.Second, true, WithPrivateHostAccess())
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return renderer, nil }

	var wg sync.WaitGroup
	for i := 0; i < maxConcurrentRenders+3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = scraper.ScrapeURL(context.Background(), srv.URL)
		}()
	}

	// Wait until the semaphore is saturated, then release every renderer.
	if !waitFor(t, 2*time.Second, func() bool { return renderer.peak() >= maxConcurrentRenders }) {
		t.Fatalf("only %d concurrent renders reached the renderer, want %d",
			renderer.peak(), maxConcurrentRenders)
	}
	if got := renderer.peak(); got != maxConcurrentRenders {
		t.Errorf("concurrent renders = %d, want exactly %d", got, maxConcurrentRenders)
	}
	close(renderer.release)
	wg.Wait()
}

func TestScrapeURLHonoursBrowserFallbackOverride(t *testing.T) {
	srv := newHTMLServer(t, `<html><body><div id="app"></div></body></html>`)
	rendered := `<html><head><title>Rendered</title></head><body><main><h1>Rendered</h1><p>` +
		strings.Repeat("rendered content ", 20) + `</p></main></body></html>`

	// The deployment default is off, so an opt-in override must enable rendering.
	optIn := &fakeBrowserRenderer{html: rendered}
	scraper := NewScraperWithBrowser(nil, time.Second, false, WithPrivateHostAccess())
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return optIn, nil }

	result, err := scraper.ScrapeURL(WithBrowserFallback(context.Background(), true), srv.URL)
	if err != nil {
		t.Fatalf("ScrapeURL returned error: %v", err)
	}
	if optIn.calls != 1 || result.Extractor != "playwright-content-root" {
		t.Fatalf("expected an override-driven render, calls=%d result=%+v", optIn.calls, result)
	}

	// An opt-out override must suppress rendering even when the default is on.
	optOut := &fakeBrowserRenderer{html: rendered}
	enabled := NewScraperWithBrowser(nil, time.Second, true, WithPrivateHostAccess())
	enabled.newBrowser = func(time.Duration) (browserRenderer, error) { return optOut, nil }

	httpOnly, err := enabled.ScrapeURL(WithBrowserFallback(context.Background(), false), srv.URL)
	if err != nil {
		t.Fatalf("ScrapeURL returned error: %v", err)
	}
	if optOut.calls != 0 {
		t.Errorf("an opt-out override must skip rendering, got %d call(s)", optOut.calls)
	}
	if httpOnly.Extractor != "goquery-content-root" || httpOnly.FallbackStatus != "" {
		t.Errorf("expected the HTTP-only result to be returned untouched, got %+v", httpOnly)
	}
}

func TestBrowserFallbackOverrideReporting(t *testing.T) {
	if _, ok := BrowserFallbackOverride(context.Background()); ok {
		t.Error("a plain context must not carry an override")
	}
	if enabled, ok := BrowserFallbackOverride(WithBrowserFallback(context.Background(), true)); !ok || !enabled {
		t.Error("expected an opt-in override to be reported")
	}
	if enabled, ok := BrowserFallbackOverride(WithBrowserFallback(context.Background(), false)); !ok || enabled {
		t.Error("expected an opt-out override to be reported")
	}
}

// ladderRenderer scripts one page per render call and records the render
// options each step used, letting tests assert the hard-URL ladder order.
type ladderRenderer struct {
	pages []string
	calls int
	plans []renderOptions
}

func (r *ladderRenderer) Render(_ context.Context, _ string, opts renderOptions) (string, error) {
	index := r.calls
	r.calls++
	r.plans = append(r.plans, opts)
	if index >= len(r.pages) {
		index = len(r.pages) - 1
	}
	return r.pages[index], nil
}

func (*ladderRenderer) Close() error { return nil }

const hardInterstitialHTML = `<html><head><title>Checking your browser</title></head><body><p>Please verify you are human.</p></body></html>`

func hardRenderedHTML() string {
	return `<html><head><title>Rendered</title></head><body><main><h1>Rendered</h1><p>` +
		strings.Repeat("rendered profile content ", 20) + `</p></main></body></html>`
}

func newHardScraper(t *testing.T, renderer browserRenderer, jar *cookiejar.Jar) *Scraper {
	t.Helper()
	scraper := NewScraperWithBrowser(jar, time.Second, false, WithPrivateHostAccess())
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return renderer, nil }
	return scraper
}

func TestScrapeHardLadderRecoversAfterBlockedRender(t *testing.T) {
	srv := newHTMLServer(t, `<html><body><div id="app"></div></body></html>`)
	renderer := &ladderRenderer{pages: []string{hardInterstitialHTML, hardRenderedHTML()}}
	scraper := newHardScraper(t, renderer, nil)

	result, err := scraper.ScrapeHard(context.Background(), srv.URL, HardScrapeOptions{})
	if err != nil {
		t.Fatalf("ScrapeHard returned error: %v", err)
	}
	if result.FallbackStatus != "succeeded" || result.FallbackReason != "extended-settle" || result.Extractor != "playwright-content-root" {
		t.Fatalf("expected extended-settle recovery, got %+v", result)
	}
	if renderer.calls != 2 {
		t.Fatalf("ladder made %d render calls, want 2", renderer.calls)
	}
	if renderer.plans[0].timeout != hardRenderTimeout || len(renderer.plans[0].cookies) != 0 {
		t.Errorf("plain-render step options: %+v", renderer.plans[0])
	}
	if renderer.plans[1].networkIdle != extendedNetworkIdle || renderer.plans[1].settle != extendedSettle || renderer.plans[1].timeout != extendedRenderTimeout {
		t.Errorf("extended-settle step options: %+v", renderer.plans[1])
	}
}

func TestScrapeHardBridgesJarCookiesIntoRender(t *testing.T) {
	srv := newHTMLServer(t, `<html><body><div id="app"></div></body></html>`)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New returned error: %v", err)
	}
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	jar.SetCookies(target, []*http.Cookie{{Name: "session", Value: "abc123"}})

	renderer := &ladderRenderer{pages: []string{hardInterstitialHTML, hardRenderedHTML()}}
	scraper := newHardScraper(t, renderer, jar)

	result, err := scraper.ScrapeHard(context.Background(), srv.URL, HardScrapeOptions{})
	if err != nil {
		t.Fatalf("ScrapeHard returned error: %v", err)
	}
	if result.FallbackReason != "cookie-render" || renderer.calls != 2 {
		t.Fatalf("expected the cookie step to win, reason=%q calls=%d", result.FallbackReason, renderer.calls)
	}
	if len(renderer.plans[1].cookies) != 1 || renderer.plans[1].cookies[0].Name != "session" {
		t.Errorf("cookie step must carry the jar cookies, got %+v", renderer.plans[1].cookies)
	}
	if len(renderer.plans[0].cookies) != 0 {
		t.Errorf("plain-render step must not carry cookies, got %+v", renderer.plans[0].cookies)
	}
}

func TestScrapeHardSkipsPlainRenderWhenAlreadyRendered(t *testing.T) {
	srv := newHTMLServer(t, `<html><body><div id="app"></div></body></html>`)
	renderer := &ladderRenderer{pages: []string{hardRenderedHTML()}}
	scraper := newHardScraper(t, renderer, nil)

	result, err := scraper.ScrapeHard(context.Background(), srv.URL, HardScrapeOptions{AlreadyRendered: true})
	if err != nil {
		t.Fatalf("ScrapeHard returned error: %v", err)
	}
	if renderer.calls != 1 || renderer.plans[0].networkIdle != extendedNetworkIdle {
		t.Fatalf("expected a single extended-settle step, calls=%d plans=%+v", renderer.calls, renderer.plans)
	}
	if result.FallbackReason != "extended-settle" {
		t.Errorf("unexpected winning tactic: %q", result.FallbackReason)
	}
}

func TestScrapeHardStillBlockedAfterLadder(t *testing.T) {
	srv := newHTMLServer(t, `<html><body><div id="app"></div></body></html>`)
	renderer := &ladderRenderer{pages: []string{hardInterstitialHTML}}
	scraper := newHardScraper(t, renderer, nil)

	_, err := scraper.ScrapeHard(context.Background(), srv.URL, HardScrapeOptions{})
	if !errors.Is(err, ErrHardLadderExhausted) {
		t.Fatalf("expected ErrHardLadderExhausted, got %v", err)
	}
	if !strings.Contains(err.Error(), "plain-render → extended-settle") || !strings.Contains(err.Error(), "human-verification challenge") {
		t.Errorf("error must trace the attempted steps and the block reason, got %q", err)
	}
}

func TestScrapeHardReportsUnavailableWithoutBrowser(t *testing.T) {
	scraper := NewScraperWithBrowser(nil, time.Second, false, WithPrivateHostAccess())
	scraper.newBrowser = func(time.Duration) (browserRenderer, error) { return nil, errors.New("no playwright install") }

	if _, err := scraper.ScrapeHard(context.Background(), "https://example.com/page", HardScrapeOptions{}); !errors.Is(err, ErrHardLadderUnavailable) {
		t.Fatalf("expected ErrHardLadderUnavailable, got %v", err)
	}
}
