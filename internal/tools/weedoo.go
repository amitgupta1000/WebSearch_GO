package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// weedooBaseURL is the registry site the priority registry enrichment reads.
const weedooBaseURL = "https://weedoo.in"

// weedooProxyURL is the per-host egress proxy for weedoo.in, read once from
// WEEDOO_PROXY_URL (http://user:pass@host:port or socks5://...). Cloudflare
// challenges Cloud Run's datacenter IPs on sight, so weedoo traffic routes
// through a residential proxy while every other host keeps its direct path
// (or the standard HTTP(S)_PROXY environment behavior). Empty by default:
// without the knob nothing changes for any host.
var weedooProxyURL = strings.TrimSpace(os.Getenv("WEEDOO_PROXY_URL"))

// weedooProxyForRequest is the http.Transport.Proxy hook: weedoo.in requests
// go through WEEDOO_PROXY_URL when it is set, everything else falls back to
// the standard environment proxy behavior. Scoping the routing to one host is
// what keeps the residential proxy's bandwidth budget — and its cost — to the
// traffic that actually needs it.
func weedooProxyForRequest(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return http.ProxyFromEnvironment(req)
	}
	if weedooProxyURL != "" {
		host := req.URL.Hostname()
		if host == "weedoo.in" || strings.HasSuffix(host, ".weedoo.in") {
			return url.Parse(weedooProxyURL)
		}
	}
	return http.ProxyFromEnvironment(req)
}

// weedooTypeaheadWait is how long the search bar's typeahead gets to return
// suggestions after the company name is typed.
const weedooTypeaheadWait = 3000 * time.Millisecond

// weedooCINWindow is how much HTML around a CIN occurrence may carry the
// company's name for the CIN to be attributed to it: WeeDoo's typeahead
// suggestion pairs the name and the CIN in one row, while the homepage's
// showcase rows carry other companies' CINs further away.
const weedooCINWindow = 300

// weedooLegalSuffixes are dropped when reducing a company name to the words a
// WeeDoo suggestion must carry.
var weedooLegalSuffixes = map[string]bool{
	"limited": true, "ltd": true, "private": true, "pvt": true, "inc": true,
	"llc": true, "plc": true, "incorporated": true, "corp": true, "corporation": true,
}

// weedooCINPattern matches an Indian CIN — 21 characters: L/U, five digits,
// two letters, four digits, three letters, six digits — the number WeeDoo's
// typeahead pairs with each company suggestion.
var weedooCINPattern = regexp.MustCompile(`(?i)\b[LU]\d{5}[A-Z]{2}\d{4}[A-Z]{3}\d{6}\b`)

// weedooNameTokens reduces a company name to the significant words a WeeDoo
// suggestion must carry: lowercase words of at least two characters, with
// legal suffixes dropped.
func weedooNameTokens(companyName string) []string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return ' '
		}
	}, companyName)
	tokens := make([]string, 0, 4)
	for _, token := range strings.Fields(strings.ToLower(cleaned)) {
		if len(token) < 2 || weedooLegalSuffixes[token] {
			continue
		}
		tokens = append(tokens, token)
	}
	return tokens
}

// weedooCINForCompany extracts the CIN WeeDoo's typeahead paired with the
// company: a CIN occurrence whose surrounding window also carries every
// significant token of the company name. The homepage's showcase rows carry
// other companies' CINs, so attribution is by proximity, never by order.
func weedooCINForCompany(html, companyName string) (string, error) {
	tokens := weedooNameTokens(companyName)
	if len(tokens) == 0 {
		return "", fmt.Errorf("company name %q has no searchable tokens", companyName)
	}
	lowered := strings.ToLower(html)
	for _, match := range weedooCINPattern.FindAllStringIndex(lowered, -1) {
		windowStart := match[0] - weedooCINWindow
		if windowStart < 0 {
			windowStart = 0
		}
		windowEnd := match[1] + weedooCINWindow
		if windowEnd > len(lowered) {
			windowEnd = len(lowered)
		}
		window := lowered[windowStart:windowEnd]
		present := true
		for _, token := range tokens {
			if !strings.Contains(window, token) {
				present = false
				break
			}
		}
		if present {
			return strings.ToUpper(weedooCINPattern.FindString(lowered[match[0]:match[1]])), nil
		}
	}
	return "", fmt.Errorf("no WeeDoo suggestion paired %q with a CIN", companyName)
}

// weedooPage opens a WeeDoo page in the shared browser context and settles it.
// The returned close function releases the page; callers must defer it.
func (r *playwrightRenderer) weedooPage(ctx context.Context, targetURL string) (playwright.Page, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	browserContext, err := r.sharedContext()
	if err != nil {
		return nil, nil, err
	}
	page, err := browserContext.NewPage()
	if err != nil {
		return nil, nil, fmt.Errorf("create browser page: %w", err)
	}
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
		_ = page.Close()
		return nil, nil, fmt.Errorf("navigate to WeeDoo: %w", err)
	}
	if wait := remainingMillis(ctx, browserNetworkIdleTimeout); wait > 0 {
		_ = page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
			State:   playwright.LoadStateNetworkidle,
			Timeout: playwright.Float(wait),
		})
	}
	return page, func() { _ = page.Close() }, nil
}

// weedooSuggestionJS finds the smallest element whose visible text pairs a CIN
// with the company's name tokens — the typeahead suggestion row. Matching at
// element scope is what keeps the homepage's showcase rows (other companies,
// their CINs) from being attributed: they are different elements.
const weedooSuggestionJS = `(() => {
  const cin = /[LU]\d{5}[A-Z]{2}\d{4}[A-Z]{3}\d{6}/i;
  const tokens = %s;
  let best = null;
  for (const el of document.querySelectorAll('div, li, a, span, p, td')) {
    const text = (el.innerText || '').trim();
    if (!text || text.length > 200) continue;
    if (!cin.test(text)) continue;
    const low = text.toLowerCase();
    if (!tokens.every(token => low.includes(token))) continue;
    if (!best || text.length < best.length) best = text;
  }
  return best || '';
})()`

// weedooSuggestionText reads the typeahead suggestion row for the company from
// the rendered page, or "" when no suggestion paired the name with a CIN.
func weedooSuggestionText(page playwright.Page, tokens []string) string {
	encoded, err := json.Marshal(tokens)
	if err != nil {
		return ""
	}
	value, err := page.Evaluate(fmt.Sprintf(weedooSuggestionJS, string(encoded)))
	if err != nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

// weedooCompanyCIN drives weedoo.in's own search bar in the shared browser
// context: type the company name, let the typeahead suggest the company (each
// suggestion pairs the name with its CIN), and read the CIN back. This is the
// discovery the search provider cannot do — Google barely indexes WeeDoo — and
// it feeds the constructed registry URL the enrichment scrapes.
func (r *playwrightRenderer) weedooCompanyCIN(ctx context.Context, companyName string) (string, error) {
	page, closePage, err := r.weedooPage(ctx, weedooBaseURL)
	if err != nil {
		return "", err
	}
	defer closePage()

	input := page.Locator(`input[placeholder*="company name" i]`).First()
	if err := input.Fill(companyName); err != nil {
		return "", fmt.Errorf("fill WeeDoo search bar: %w", err)
	}
	if wait := remainingMillis(ctx, weedooTypeaheadWait); wait > 0 {
		page.WaitForTimeout(wait)
	}
	suggestion := weedooSuggestionText(page, weedooNameTokens(companyName))
	if suggestion == "" {
		return "", fmt.Errorf("no WeeDoo suggestion paired %q with a CIN", companyName)
	}
	cin, err := weedooCINForCompany(suggestion, companyName)
	if err != nil {
		return "", err
	}
	log.Printf("[WEEDOO] typeahead resolved %q -> CIN %s (row %q)", companyName, cin, suggestion)
	return cin, nil
}

// weedooPageText renders a WeeDoo page in the shared browser context and
// returns its visible text. The registry pages are server-rendered, but a plain
// fetch's extraction trims them to the site navigation — the directors and DIN
// table only survives a real render.
func (r *playwrightRenderer) weedooPageText(ctx context.Context, targetURL string) (string, error) {
	page, closePage, err := r.weedooPage(ctx, targetURL)
	if err != nil {
		return "", err
	}
	defer closePage()
	value, err := page.Evaluate("() => (document.body ? document.body.innerText : '')")
	if err != nil {
		return "", fmt.Errorf("read WeeDoo page text: %w", err)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("read WeeDoo page text: unexpected value type")
	}
	return strings.TrimSpace(text), nil
}

// WeeDooBrowserClient resolves Indian company names to their WeeDoo registry
// CIN by driving weedoo.in's own search bar in a headless browser: the site is
// a client-rendered application whose search results never reach a plain HTTP
// fetch, while the typeahead pairs every suggestion with the company's CIN.
type WeeDooBrowserClient struct {
	renderer browserRenderer
	mu       sync.Mutex
	cache    map[string]string
}

// NewWeeDooBrowserClient starts the browser the WeeDoo search runs in. The
// browser is WeeDoo-only, so when WEEDOO_PROXY_URL is configured its whole
// egress routes through the residential proxy — Chromium included.
func NewWeeDooBrowserClient(timeout time.Duration) (*WeeDooBrowserClient, error) {
	renderer, err := newPlaywrightRendererWithProxy(timeout, weedooProxyURL)
	if err != nil {
		return nil, err
	}
	if weedooProxyURL != "" {
		log.Printf("[WEEDOO] browser egress routes through WEEDOO_PROXY_URL")
	}
	return &WeeDooBrowserClient{renderer: renderer, cache: map[string]string{}}, nil
}

// LookupCompanyCIN returns the CIN WeeDoo's search bar pairs with the company
// name. Results are cached per company — including misses — so concurrent runs
// and QC iterations never repeat a browser round trip for the same name.
func (c *WeeDooBrowserClient) LookupCompanyCIN(ctx context.Context, companyName string) (string, bool) {
	name := strings.TrimSpace(companyName)
	if name == "" || !c.Enabled() {
		return "", false
	}
	c.mu.Lock()
	if cin, cached := c.cache[name]; cached {
		c.mu.Unlock()
		return cin, cin != ""
	}
	c.mu.Unlock()

	cin, err := c.lookup(ctx, name)
	c.mu.Lock()
	c.cache[name] = cin
	c.mu.Unlock()
	if err != nil {
		log.Printf("[WEEDOO] search for %q unavailable: %v", name, err)
		return "", false
	}
	return cin, cin != ""
}

func (c *WeeDooBrowserClient) lookup(ctx context.Context, name string) (string, error) {
	if renderer, ok := c.renderer.(*playwrightRenderer); ok {
		return renderer.weedooCompanyCIN(ctx, name)
	}
	return "", fmt.Errorf("browser renderer does not support interactive search")
}

// Enabled reports whether the client carries a working browser.
func (c *WeeDooBrowserClient) Enabled() bool {
	return c != nil && c.renderer != nil
}

// FetchCompanyPageText renders a WeeDoo page in the browser and returns its
// visible text: the plain fetch's extraction trims WeeDoo's server-rendered
// pages to the site navigation, so the directors and DIN table — the reason the
// registry is a priority source — is read from a real render instead.
func (c *WeeDooBrowserClient) FetchCompanyPageText(ctx context.Context, pageURL string) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("WeeDoo browser unavailable")
	}
	if renderer, ok := c.renderer.(*playwrightRenderer); ok {
		return renderer.weedooPageText(ctx, pageURL)
	}
	return "", fmt.Errorf("browser renderer does not support interactive search")
}

// Close shuts the WeeDoo browser down.
func (c *WeeDooBrowserClient) Close() error {
	if c == nil || c.renderer == nil {
		return nil
	}
	return c.renderer.Close()
}
