# The Scraper — A Detailed Walkthrough

This is a code-level walkthrough of the **scraper**, the tool layer that turns a URL into the
clean text, metadata and images the research agents actually reason over. It covers every file
in the scraping stack:

| File | Role |
| :--- | :--- |
| `internal/tools/scraper.go` | Fetch policy, retries, charset decoding, challenge detection, noise-stripping text extraction, image extraction, quality scoring |
| `internal/tools/scraper_browser.go` | Optional bounded Playwright/Chromium renderer used as a fallback |
| `internal/tools/scraper_test.go` | 29 unit tests pinning the behaviours above, incl. the hard-URL ladder |
| `internal/tools/scraper_browser_integration_test.go` | Opt-in real-Chromium test (`PLAYWRIGHT_INTEGRATION=1`) |
| `internal/tools/cookies.go` | Browser cookie-export JSON → `http.CookieJar` (optional authenticated sessions) |

The scraper is deliberately *not* an agent itself — it never calls the LLM and makes no
decisions about *where* to go. The agents (company/person, via `internal/agent`) decide which
URLs are worth visiting; the scraper is the disciplined "hands" that fetch, sanitize and report
back. Every judgement it makes (blocked? thin? truncated?) is recorded as data on
`models.ScrapeResult` so the calling agent — and the UI's scraping report — can act on it.

---

## 1. Where the scraper sits

```text
internal/agent                     (CompanyAgent / PersonAgent)
      │   PageFetcher interface
      │   ScrapeURL(ctx, targetURL) (*models.ScrapeResult, error)
      ▼
internal/agent/engine.go           Engine.ScrapeCached
      │   · cache key scrape:sha256(url|mode=…)     (success TTL 7 days)
      │   · failures cached 15 min (negative cache)
      ▼
internal/tools/scraper.go          *tools.Scraper
      ├── validateTarget   → HTTP(S)-only, public-host-only fetch policy (SSRF defence)
      ├── fetchHTTP        → GET with retries, linear backoff, 2 MiB body cap, ≤5 redirects
      ├── extractHTML      → charset decode → goquery → noise strip → text/images/metadata
      └── tryBrowser       → scraper_browser.go (Playwright Chromium render, opt-in)
            ▲                     │
cookies.go ─┘ shared cookie jar    └── full Chromium, shared browser context, render semaphore
```

The rest of the program only knows the scraper through one interface (in
`internal/agent/engine.go`):

```go
// PageFetcher extracts readable page content. *tools.Scraper satisfies this interface.
type PageFetcher interface {
	ScrapeURL(ctx context.Context, targetURL string) (*models.ScrapeResult, error)
}
```

That indirection is what keeps the scraper unit-testable: the tests inject fake browser
renderers via the `newBrowser func(time.Duration) (browserRenderer, error)` field and target
`httptest` servers instead of the live web.

**Wiring** happens once in `cmd/server/main.go` (in this order):

1. `tools.LoadCookieJarFromFile(cfg.CookieFile)` — optional cookies; failures only warn.
2. `tools.NewScraperWithBrowser(cookieJar, 15*time.Second, cfg.EnableBrowserFallback)`.
3. `defer scraper.Close()` — releases the Chromium process on shutdown.
4. When `ALLOW_BROWSER_TOGGLE` or `ENABLE_BROWSER_FALLBACK` is set,
   `scraper.BrowserAvailable()` probes Chromium once so the UI never offers a render toggle
   that cannot work. A successful probe also leaves the browser **warm** for the first run.

---

## 2. Types and constants at a glance

### The tuning constants (scraper.go:25–47)

| Constant | Value | Why it exists |
| :--- | :--- | :--- |
| `defaultScraperTimeout` | 15 s | Whole-request HTTP timeout (`main.go` passes exactly this) |
| `maxRedirects` | 5 | Beyond this, `http.ErrUseLastResponse` returns the last response instead of erroring |
| `maxBodyBytes` | 2 MiB (`2<<20`) | Download cap — avoids memory blowups on huge or hostile pages |
| `maxContentBytes` | 40 000 | Cap on extracted text handed to the LLM (~40 KB per page) |
| `minTextRunes` | 25 | Filters tiny fragments and navigation noise (rune-aware, not bytes) |
| `minQualityRunes` | 160 | Below this a page is a "shell" → browser fallback candidate |
| `blockPageMaxRunes` | 1200 | Challenge detection only inspects pages with little visible text |
| `defaultFetchAttempts` / `defaultFetchBackoff` | 3 / 400 ms | Retry budget with linear backoff (`400 ms × (attempt−1)`) |
| `maxConcurrentRenders` | 2 | Semaphore bounding live browser contexts |
| `browserFallbackTimeout` | 10 s | Per-render Chromium budget (scraper_browser.go:12) |
| `browserNetworkIdleTimeout` | 4 s | Bounded wait for in-flight requests to settle (scraper_browser.go:16) |
| `maxExtractedImages` | 24 | Capacity pre-allocation for the image slice (scraper.go:581) |

Plus `noiseSelector` (scraper.go:52) — the long CSS selector listing everything that never
carries primary content, and `bodyBlockSelector` (scraper.go:452) used by the text fallback.

### `Scraper` (scraper.go:59)

| Field | Purpose |
| :--- | :--- |
| `client *http.Client` | Tuned transport (100 idle conns, 10 per host, 90 s idle timeout), timeout, shared cookie jar, redirect cap of 5 |
| `browserEnabled bool` | Process-wide default for the render fallback (`ENABLE_BROWSER_FALLBACK`) |
| `browserTimeout` | Per-render budget — `min(10 s, HTTP timeout)` |
| `allowPrivate bool` | Opt-in escape hatch for the private-host policy (`WithPrivateHostAccess`) |
| `fetchAttempts` / `fetchBackoff` | Retry budget (see constants) |
| `browser`, `browserInit`, `browserInitErr` | Lazy, once-only browser init — the *result including a failure* is cached, so a missing Playwright install is never re-attempted per scrape |
| `browserMu`, `closed` | State guard; makes `Close()` idempotent |
| `renderSem chan struct{}` | Counting semaphore capping concurrent renders at 2 |
| `newBrowser` | Renderer constructor — swapped for fakes in tests |

Two constructors exist: `NewScraper` (HTTP only) simply delegates to `NewScraperWithBrowser` with
`enableBrowser=false`. A nil cookie jar is safe — an empty jar is created, because a typed-nil
`*cookiejar.Jar` on `http.Client.Jar` would panic on the first request (scraper.go:107).

### `models.ScrapeResult` (models/agent.go:45) — the output contract

Every field is consumed downstream (evidence pipeline, scrape report, UI):

| Field | Meaning |
| :--- | :--- |
| `URL`, `CanonicalURL`, `Title` | Page identity (`<link rel="canonical">` is honoured) |
| `Content` | Clean extracted text, rune-safe truncated at 40 000 bytes |
| `Headings`, `JSONLD`, `Images` | Structure metadata that boosts synthesis quality |
| `WordCount`, `ByteSize`, `QualityScore` | Size + heuristic quality in `[0, 1]` |
| `ContentType`, `Extractor` | Provenance — `goquery-content-root`, `playwright-content-root`, or `blocked-page` |
| `Blocked` + `BlockReason` | Challenge/consent interstitial detected (e.g. `"captcha challenge"`) |
| `FallbackAttempted/Status/Reason/Error` | Fallback trace: `attempted` → `succeeded` / `blocked` / `failed` |
| `ContentTruncated` | Body hit the 2 MiB cap; text came from an incomplete document |
| `FetchedAt` | Timestamp consumed by the evidence pipeline |

---

## 3. Anatomy of a scrape — `ScrapeURL` end to end

`ScrapeURL` (scraper.go:182) is the single entry point. The whole flow in one diagram:

```text
ScrapeURL(ctx, targetURL)
   │
   ├─1─ validateTarget ────── reject non-HTTP(S), hostless or private targets (hard error)
   ├─2─ isClosed? ─────────── error (scraper already shut down)
   │
   ├─3─ fetchHTTP  (≤3 attempts, 400 ms × (attempt−1) linear backoff, cancellation-aware)
   │      ├─ transport error ────────────────► tryBrowser("HTTP request failed", err)
   │      ├─ status ≠ 200 ───────────────────► tryBrowser("HTTP status NNN", err)
   │      └─ status 200:
   │            ├─ PDF ───────────────────── document extractor (Gemini → local parser)
   │            ├─ other non-HTML ─────────── hard error (no fallback)
   │            └─ extractHTML(...)
   │                 ├─ challenge detected → blocked result
   │                 │      └─► tryBrowser("HTTP response was marked as blocked")
   │                 └─ needsBrowserFallback() true (thin page / JS shell)
   │                        └─► tryBrowser("low-quality or JavaScript-shell page")
   │
   └─4─ return *models.ScrapeResult (with full provenance trail)
```

The key property: **the browser fallback is available at every failure point of the HTTP
path**, but it is *bounded* — unsupported non-HTML responses and a closed scraper fail hard,
while PDFs use the document extractor; a failed render never destroys a usable HTTP result (§7).

Happy path, step by step:

1. **Validate** — `validateTarget` parses the URL and enforces the fetch policy (§4).
2. **Fetch** — `fetchHTTP` loops up to 3 attempts. Each `fetchHTTPOnce` sends a GET with a
   browser-like `User-Agent` (`Chrome/124`), `Accept: text/html…` and
   `Accept-Language: en-US,en;q=0.9`, reads at most 2 MiB + 1 byte, and returns
   `(body, contentType, statusCode, truncated)`. Network errors and retriable statuses
   (408, 429, 5xx) are wrapped in `retriableError` and retried; 404/403 etc. are returned
   as-is so the fallback can judge them (§5).
3. **Extract** — `extractHTML` decodes the charset, strips noise, detects challenges, and
   extracts text, headings, JSON-LD and images (§6).
4. **Quality gate** — `needsBrowserFallback` re-checks the result: blocked pages or content
   under 160 runes / quality under 0.45 are re-rendered in Chromium when enabled (§7–8).
5. **Return** — `*models.ScrapeResult` or an error; `Engine.ScrapeCached` adds caching (§10).

---

## 4. The fetch-target policy (`validateTarget`, scraper.go:216)

Scrape targets come from *untrusted* search results and model output, so the policy is
deliberately strict:

```go
if parsed.Scheme != "http" && parsed.Scheme != "https" {
    return fmt.Errorf("refusing to scrape non-HTTP(S) URL %q", targetURL)
}
```

- **Scheme check happens before anything else** because the HTTP client would reject other
  schemes anyway — but the browser renderer would happily follow them (`file://` reads local
  files). Rejecting up front means the fallback never even sees them.
- **`isPrivateHost` (scraper.go:245)** refuses literal IPs that are loopback, private (RFC1918),
  link-local unicast/multicast, unspecified or multicast — which includes cloud metadata
  endpoints such as `169.254.169.254` — plus the hostnames `localhost`, `*.localhost`,
  `*.local` and `*.internal`.
- **Known limitation (documented in the code):** hostnames that merely *resolve* to a private
  address are not caught here — that would require DNS resolution, which the dialer then
  repeats. Hostname literal checks only.
- **Escape hatch:** `WithPrivateHostAccess()` is an `Option` used by intranet deployments and
  the test suite (local `httptest` servers resolve to loopback). There is **no environment
  variable** for this — production keeps the default.

## 5. HTTP fetching: retries, caps and headers

### `fetchHTTP` (scraper.go:263) — the retry loop

- Up to `fetchAttempts` (3) attempts. Between attempts it sleeps `fetchBackoff × (attempt−1)`
  → 0 ms, 400 ms, 800 ms — via `sleepWithContext`, which wakes early if the research context is
  cancelled (a cancelled run never waits out a backoff).
- **What is retriable:** network/transport errors and body-read errors (wrapped in
  `retriableError` by `fetchHTTPOnce`), plus statuses `408 Request Timeout`, `429 Too Many
  Requests`, and everything ≥ 500 (`isRetriableStatus`, scraper.go:297).
- **What is *not* retried:** 404/403/401-style client errors. They're returned to `ScrapeURL`,
  which hands them to `tryBrowser` — rendering a genuinely missing page in Chromium would just
  be slower 404.
- Exhausting attempts yields `scraping failed after 3 attempts: …` with the last error.

### `fetchHTTPOnce` (scraper.go:316) — one GET

- `http.NewRequestWithContext` — every fetch honours the run's research deadline.
- Browser-like headers (see §3) — many sites serve interstitials to empty UAs.
- `io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))` — reads **one byte past the cap** so
  the scraper can tell "exactly 2 MiB" apart from "truncated", setting `ContentTruncated`
  honestly. A truncated body is still parsed (half a page usually beats no page).

### Redirects and cookies

- `CheckRedirect` allows 5 hops, then `http.ErrUseLastResponse` — the last response is
  processed rather than the chain aborted as an error.
- The shared cookie jar (§9) persists cookies across redirects automatically, and across
  every scrape in the process — which is what lets an authenticated session survive a
  login-redirect chain.

---

## 6. The extraction pipeline (`extractHTML`, scraper.go:344)

This is where raw bytes become LLM-ready text. Nine ordered steps:

**1. Charset decoding.** `charset.NewReader(bytes.NewReader(body), contentType)`
(golang.org/x/net) honours the `Content-Type` charset *or* a `<meta>` declaration instead of
assuming UTF-8 — ISO-8859-1 and windows-1252 pages are not mangled
(`TestScrapeURLDecodesNonUTF8Document`).

**2. Title + canonical URL.** `<title>` text, and `<link rel="canonical">` resolved against the
target via `resolveURL` (which returns the base rather than a dangling reference when parsing
fails).

**3. JSON-LD *before* noise removal.** `script[type='application/ld+json']` payloads are
collected first (scraper.go:362) — the next step discards all `<script>` nodes. JSON-LD is pure
gold for the agents (person/organization schemas) and later feeds the OCR/evidence path.

**4. Noise removal.** `doc.Find(noiseSelector).Remove()` strips `script, style, noscript, nav,
footer, header, aside, svg, form, iframe, button, dialog`, ARIA navigation/banner/dialog roles,
and class/id contains matches for `cookie`, `consent`, `sidebar`, `advert`, `related`,
`social-share`. Removal happens **before any text is inspected**, so inline `<script>` source
can never influence challenge detection or leak into the content handed to the LLM.

**5. Content root selection.** `main, article, [role='main']` — first match wins; otherwise
`body`. This keeps page chrome (menus, promo rails) out of the extracted text.

**6. Challenge detection.** `detectBlockPage(title, visibleText)` (scraper.go:616) runs on the
*visible* text only. If the visible text exceeds `blockPageMaxRunes` (1200 runes) it returns
`""` immediately — a substantive article that merely mentions "captcha" is never
misclassified. Otherwise it scans the lowercased `title + "\n" + text` for the marker table:

| Phrase | Reported reason |
| :--- | :--- |
| press and hold | press-and-hold challenge |
| verify you are human | human-verification challenge |
| are you a robot | robot-verification challenge |
| captcha | captcha challenge |
| unusual traffic | unusual-traffic challenge |
| checking your browser | browser-check challenge |
| access denied | access denied |

A hit produces a result with `Blocked: true`, `BlockReason`, `Extractor: "blocked-page"`,
`QualityScore: 0`, and the (short) visible text as `Content` — evidence of *what* blocked us,
not the page content.

**7. Headings.** `h1…h4` inside the content root, trimmed and deduplicated by emptiness.

**8. Images** (`extractImages`, scraper.go:583). Per `<img>`: first present attribute among
`src`, `data-src`, `data-lazy-src` (lazy-load patterns), resolved against the page URL, accepted
only for absolute `http(s)`, deduplicated, enriched with `alt`, `title` and the closest
`<figure><figcaption>` caption (`maxExtractedImages` only pre-sizes the slice, it is not a hard
cap). Images feed the Gemini OCR path (§11).

**9. Leaf-block text extraction** — the trick that keeps context small:

```go
contentRoot.Find("h1, h2, h3, h4, p, li, article, section").
    Not(":has(h1, h2, h3, h4, p, li, article, section)").
    Each(...)
```

Selecting only elements that **do not contain** another content tag avoids the nested-duplicate
explosion (an `<article>` whose text would otherwise be re-emitted for every child `<p>` —
historically a 2–10× context bloat). Blocks under `minTextRunes` (25 runes) are dropped.

**Fallbacks and cleanup:**

- If the extracted text is under `minQualityRunes` (160 runes), `bodyText` (scraper.go:457)
  re-renders the body from *block-level leaf elements*
  (`h1–h6, p, li, dt, dd, td, th, blockquote, figcaption, pre`), joining blocks with blank
  lines. goquery's `Text()` concatenates every descendant with no separator — this is what
  keeps `"Short pageSmall shell."` from happening (`TestScrapeURLKeepsWordBoundariesInBodyFallback`).
- `excessiveBlankLines` collapses 3+ newlines into one blank line.
- `runeSafeTruncate(content, 40000)` caps the text **without ever splitting a UTF-8 rune**
  (it back-trims until `utf8.ValidString` holds) and appends `"\n\n...[content truncated]..."`.
- Finally `scraperQualityScore` computes the quality heuristic (§7).

---

## 7. The quality gate

`scraperQualityScore` (scraper.go:661) is a cheap, explainable heuristic in `[0, 1]`:

| Signal | Effect |
| :--- | :--- |
| base | `+0.25` |
| ≥ 40 words | `+0.35` |
| ≥ 15 words | `+0.15` |
| any headings | `+0.20` |
| any JSON-LD | `+0.15` |
| content mentions `accept cookies` / `cookie policy` | `−0.2` (consent-wall smell) |
| final clamp | `[0, 1]` |

`needsBrowserFallback` (scraper.go:474) turns the score into a decision:

```go
func needsBrowserFallback(result *models.ScrapeResult) bool {
    if result == nil || result.Blocked {
        return true
    }
    return utf8.RuneCountInString(result.Content) < minQualityRunes || result.QualityScore < 0.45
}
```

I.e. blocked, under 160 runes, or scoring below 0.45 → try Chromium. In practice: 40+ words
score 0.60 and always pass; shorter pages need headings and/or JSON-LD — a bare 15-word shell
scores 0.40 and falls to the renderer, while <15 words with no structure tops out at 0.25.

## 8. The browser fallback (`tryBrowser` + `scraper_browser.go`)

### When it triggers

`tryBrowser(ctx, targetURL, base, reason, originalErr)` (scraper.go:481) is called with:

| Trigger | `reason` recorded | `base` passed |
| :--- | :--- | :--- |
| Transport error after retries | `"HTTP request failed"` | nil |
| Non-200 status | `"HTTP status NNN"` | nil |
| HTML parse failure | `"HTTP HTML parsing failed"` | nil |
| `Blocked` result | `"HTTP response was marked as blocked"` | the blocked result |
| Thin/JS-shell page | `"low-quality or JavaScript-shell page"` | the thin result |

### Enable/disable — two layers

1. **Process-wide** `ENABLE_BROWSER_FALLBACK` → `browserEnabled`.
2. **Per-request override** via context: `tools.WithBrowserFallback(ctx, enabled)` /
   `BrowserFallbackOverride(ctx)` (scraper.go:145–170). `browserFallbackFor` prefers the
   override; this is how the console's per-run checkbox and `POST /api/rerun` opt a single run
   into rendering without mutating shared state.
3. With rendering disabled: a non-nil `base` (blocked/thin page) is returned **as-is** — the
   run continues with degraded evidence rather than an error; with no base, the original error
   propagates.

### What the renderer does (scraper_browser.go)

`playwrightRenderer` launches **full Chromium** (`Channel: "chromium"`, headless,
`--disable-dev-shm-usage`) rather than Playwright's headless shell — the shell crashes with
"Target crashed" on some hosts, and the full build presents a more realistic fingerprint.
`Render(ctx, url)` then:

1. Creates a fresh `page` in the **shared browser context** (locale `en-US`, viewport
   1440×900). One shared context means a clearance cookie earned by passing a JavaScript
   interstitial is **reused by later renders** instead of being discarded (this exact behaviour
   is what the integration test pins).
2. `page.Goto(url, WaitUntil: domcontentloaded)` under the render timeout.
3. Lets the page settle: bounded `WaitForLoadState(networkidle)` (≤ 4 s) — analytics,
   hydration and JavaScript interstitials get time to finish — plus a 750 ms settle wait, both
   clamped by `settleMillis` to fit the render budget.
4. Re-checks `ctx.Err()` (the run may have expired mid-render), then `page.Content()` → HTML.

The rendered HTML goes back through the *same* `extractHTML` with extractor
`"playwright-content-root"` — so a rendered page gets identical cleaning, challenge detection
and quality scoring.

### Bounding the blast radius

- **Concurrency:** `render` (scraper.go:527) acquires one of 2 `renderSem` slots (selecting on
  `ctx.Done()` so a cancelled run never queues), then wraps the render in
  `context.WithTimeout(ctx, s.browserTimeout)` — 10 s, or less if the HTTP timeout is smaller.
- **Laziness:** `getBrowser` (scraper.go:542) starts Playwright on first use, exactly once;
  even a *failure* is cached (`browserInitErr`) so a missing install doesn't cost a launch per
  scrape.
- **Graceful degradation:** if the render fails but a `base` result exists, the base is
  returned with `FallbackStatus: "failed"` and `FallbackError` filled in. Only when there is no
  base does the caller see an error (`…; browser fallback failed: …`).
- **Challenges are reported, never solved:** if the rendered page is *still* blocked,
  `FallbackStatus` is `"blocked"` — the scraper never clicks, holds or solves anything.
- **Shutdown:** `Close()` is idempotent and tears down context → browser → Playwright in order.

### The hard-URL ladder (`ScrapeHard`)

When the agents hit a URL the normal path cannot read — a challenge page or a professional
profile — the bounded hard-URL handler (`internal/agent/hardurl.go`) escalates through
`ScrapeHard`:

1. **`plain-render`** — one render with the default budget (skipped when the caller's normal
   path already rendered and was still blocked, `HardScrapeOptions.AlreadyRendered`).
2. **`cookie-render`** — the shared jar's cookies for the domain are copied into the browser
   context (`jar.Cookies(u)` → `addCookies` → `AddCookies`), so a user-exported session
   (`COOKIE_FILE_PATH`) can authenticate rendered pages. Previously the cookie jar only ever
   reached the Go HTTP client — Chromium never saw it.
3. **`extended-settle`** — longer network-idle (8 s) and settle (2.5 s) waits for slow
   interstitials and late hydration.

Every attempt reuses the shared context, holds one of the 2 render-semaphore slots, and is
re-extracted through the same `extractHTML` pipeline. Success requires unblocked, non-thin
content (`!Blocked && !needsBrowserFallback(result)`); every failure wraps
`ErrHardLadderUnavailable` (no browser) or `ErrHardLadderExhausted` (still blocked / thin),
with the attempted step names and block reason in the message. Challenges are still never
solved — an interstitial that survives all steps keeps the URL blocked.

The handler wraps this with the blast-radius containment: a 30 s per-URL ladder budget, a
per-run cap of 2 escalated URLs, and a gate requiring ≥45 s of the run's deadline to remain.
Attempts run under a dedicated `mode=hard` cache key (`ScrapeHardCached`, 7-day success /
15-min negative TTL), so a failed escalation can never mask — or be masked by — the normal
HTTP or browser-mode entries for the same URL. Recovered URLs become full evidence
(`HandlerStatus: recovered` in the scrape report); failures degrade to the previous
snippet-fallback behaviour.

---

## 9. Cookie support (`cookies.go`)

`LoadCookieJarFromFile` imports the JSON export of the Cookie-Editor / EditThisCookie browser
extensions (via `COOKIE_FILE_PATH`) into a standard `http.CookieJar` that the scraper's HTTP
client then shares:

- Accepts both expiry formats: unix `expirationDate` (float) and RFC1123 `expires` — with a
  fallback because `http.ParseTime` only accepts a literal `GMT` zone while some exporters emit
  `UTC`.
- Strips the leading dot from export-style domains (`.google.com` → `google.com`) so the jar
  treats them as domain cookies covering subdomains.
- Skips empty and expired cookies; tolerates partial failures (returns the usable jar plus an
  error naming how many domains failed).
- One subtlety worth knowing: the jar belongs to the **Go HTTP client only**. Chromium manages
  its own cookie store inside the shared browser context (that's where interstitial clearance
  cookies live, §8) — the two stores do not mix.

Use case: pages that gate content behind a session (e.g. an intranet or a site where the public
page 302s to a login). With cookies loaded, the redirect chain lands on content.

## 10. The caching contract (`Engine.ScrapeCached`, engine.go:176)

The agent engine never calls `ScrapeURL` directly — it calls `ScrapeCached`, which adds:

- **Key composition** (`scrapeCacheKey`, engine.go:148): `cache.HashKey("scrape", url + "|mode=" + mode)`
  where `mode` is `default` / `browser` / `http` derived from the per-request
  `WithBrowserFallback` override. **An HTTP-only result can never mask a browser-rendered
  re-run of the same URL** (and vice versa), and a negative entry from a blocked HTTP fetch
  cannot suppress a rendered attempt.
- **Success TTL:** 7 days (`scrapeTTL`).
- **Negative TTL:** 15 minutes (`negativeScrapeTTL`, engine.go:164) — failures (paywalls, 403s,
  rejected content types) are remembered briefly so a follow-up question doesn't pay to
  rediscover a dead end. The scraper already retried transient network failures upstream, so
  what reaches this layer is usually deterministic.
- **Cancellations are never cached** — a run that timed out mid-scrape doesn't poison the URL
  for the next run.

---

## 11. How the agents consume the scraper

### Company agent — `scrapeRanked` (company.go:711)

The company agent never scrapes the raw ranked list. `selectScrapeCandidates`
(company.go:894) curates it first:

1. **Source-type priority** — `official_leadership` → `official_report` → `official_locations`
   → `official` → `leadership` → `industry_event` → `external`, then by score within a tier.
2. **Hard-URL routing** — `professional_profile` (LinkedIn & co.) and anything already
   `blocked` used to be hard-skipped; when the hard-URL handler is available they get one
   bounded escalation instead, with the snippet kept as fallback evidence either way (below).
3. **Domain diversity** — one page per root domain, except `official` sources may take up to 2.
4. Cap at `engine.MaxScrapes()` (default 10, `MAX_SCRAPES_PER_RUN`).

Per-URL then, via `ScrapeCached`:

- URLs are pre-validated with `url.ParseRequestURI`; failures are recorded, not fatal.
- Results under `minScrapeContentLen` (80 bytes of text) are skipped as useless.
- A **blocked** result is routed to the hard-URL handler for one bounded escalation; if the
  ladder fails, the URL's *search snippet* is kept as fallback evidence
  (`snippetEvidenceForURL`, company.go:876) at 65 % of its rank score with quality 0.25 —
  degraded, clearly labelled `search_snippet_fallback` evidence instead of nothing.
- Every attempt lands in a `models.ScrapeReportEntry` (status, reason, word count, quality
  score, extractor, fallback status/reason/error, OCR status) that surfaces in the UI's
  "Scraping Report" disclosure — so a thin synthesis is *explainable*, not mysterious.

### Person agent

Leaner single-pass flow: one disambiguated query, then scraping prioritised toward LinkedIn,
Wikipedia, "about" pages and GitHub, plus a second career-focused query. Same scraper, same
guarantees.

### Image OCR

For a company scrape with images, `scrapeRanked` gives OCR a 15-second budget per page
(company.go:785): `Engine.ExtractImageTextDetailed` (engine.go:239) checks whether the LLM
client implements `imageTextExtractor`; `GeminiClient` does (gemini.go:142), downloads bounded
image inputs itself, and transcribes visible text. Results land on `Image.ExtractedText`, are
counted in `ScrapeReportEntry.OCRImagesWithText` (statuses `attempted`/`success`/`failed`/
`not_applicable`), and are formatted into the evidence block as `OCR: …`
(`TestFormatEvidenceIncludesImageOCR`). Failures never abort the scrape.

## 12. Configuration knobs

| Variable | Default | Scraper-relevant effect |
| :--- | :--- | :--- |
| `ENABLE_BROWSER_FALLBACK` | `false` | Process-wide render fallback |
| `ALLOW_BROWSER_TOGGLE` | `true` | Per-run checkbox; only shown if `BrowserAvailable()` passes |
| `HARD_URL_HANDLER` | `true` | Escalate blocked pages and professional profiles (2 URLs/run, 30 s each, ≥45 s deadline remaining — code constants) |
| `COOKIE_FILE_PATH` | — | Cookie jar import (§9) |
| `RESEARCH_TIMEOUT_SECONDS` | 180 | Run budget the scraper's context inherits |
| `BROWSER_RESEARCH_TIMEOUT_SECONDS` | 300 | Larger budget for rendered runs (clamped ≥ HTTP budget) |
| `MAX_SCRAPES_PER_RUN` | 10 | Scrape cap consumed by the agents |

One prerequisite when rendering is enabled:
`go run github.com/mxschmitt/playwright-go/cmd/playwright install chromium`.
`Dockerfile.browser` builds an image that already contains it.

---

## 13. Observability — reading a scrape outcome

When a result looks thin, the provenance fields answer "why" without re-running:

- `Extractor` — `goquery-content-root` (normal), `playwright-content-root` (rendered),
  `blocked-page` (challenge detected).
- `Blocked` + `BlockReason` — which interstitial fired.
- `FallbackAttempted` / `FallbackStatus` (`attempted` → `succeeded` | `blocked` | `failed`) /
  `FallbackReason` / `FallbackError` — the full fallback trace.
- `ContentTruncated` + `ByteSize` — whether the 2 MiB cap cut the document.
- `QualityScore` + `WordCount` — why the quality gate judged it thin (see §7 table).

The same fields surface twice: in `models.ScrapeReportEntry` (the UI's per-URL scraping
report) and in the terminal's `[AGENT]` trace lines emitted by `Emit`.

## 14. Test coverage (`scraper_test.go`)

Each test pins one behaviour; they use `httptest` servers and injected fake renderers, so the
default suite needs no network and no browser:

| Test | Pins |
| :--- | :--- |
| `TestScrapeURLExtractsContentWithoutDuplicates` | Leaf-block dedup (no nested re-emission) |
| `TestScrapeURLExtractsMetadataAndRemovesBoilerplate` | Title/headings/JSON-LD kept, boilerplate gone |
| `TestScrapeURLClassifiesPressAndHoldChallenge` / `TestScrapeURLIgnoresChallengeKeywordsInScript` / `TestDetectBlockPageIgnoresSubstantivePages` | Challenge detection: visible text only, script source ignored, substantive pages never misclassified |
| `TestScrapeURLRejectsNonHTMLContent` | Unsupported non-PDF non-HTML responses fail with `unsupported content type` |
| `TestScrapeURLPDFUsesDocumentExtractor` | PDF responses use the injected document extractor |
| `TestScrapeURLUsesBrowserForJavaScriptShell` | Thin page → render → real content |
| `TestScrapeURLKeepsHTTPResultWhenBrowserFails` | Graceful degradation (`FallbackStatus: failed`) |
| `TestScrapeURLDoesNotBypassRenderedChallenge` | Rendered challenge stays `blocked` |
| `TestScrapeURLReturnsErrorWhenHTTPAndBrowserFail` | Error wrapping when both paths fail |
| `TestRuneSafeTruncate` / `TestIsHTMLContentType` | Rune-safe truncation; content-type matching |
| `TestScrapeURLDecodesNonUTF8Document` | Legacy charset handling |
| `TestScrapeURLKeepsWordBoundariesInBodyFallback` | `bodyText` block separation |
| `TestScrapeURLRejectsNonHTTPScheme` / `TestScrapeURLRefusesPrivateHostsUnlessOptedIn` | Fetch policy |
| `TestScrapeURLRetriesTransientStatus` / `TestScrapeURLDoesNotRetryClientError` | Retry semantics |
| `TestScrapeURLFlagsTruncatedBody` | 2 MiB cap + `ContentTruncated` |
| `TestScraperCloseIsIdempotent` / `TestScraperInitializesBrowserOnce` | Lifecycle |
| `TestScraperBoundsConcurrentRenders` | Semaphore cap of 2 |
| `TestScrapeURLHonoursBrowserFallbackOverride` / `TestBrowserFallbackOverrideReporting` | Per-request override (opt-in and opt-out) |

The engine side adds `TestScrapeCacheKeySeparatesRenderModes` (engine_test.go:119), and the
hard-URL stack adds its own layers: `scraper_test.go` pins the ladder itself
(`TestScrapeHardLadderRecoversAfterBlockedRender` asserts the step order and per-step render
options, `TestScrapeHardBridgesJarCookiesIntoRender` proves the jar's cookies reach the
cookie-bridged step, `TestScrapeHardSkipsPlainRenderWhenAlreadyRendered`,
`TestScrapeHardStillBlockedAfterLadder`, `TestScrapeHardReportsUnavailableWithoutBrowser`),
while `internal/agent/hardurl_test.go` pins the blast-radius containment (per-run cap,
low-budget deferral, disable toggle, `mode=hard` cache separation, and the company/person
routing including the byte-for-byte legacy fallback when no ladder is available).

**Integration test** (real Chromium, verifying clearance-cookie reuse across renders):

```bash
go run github.com/mxschmitt/playwright-go/cmd/playwright install chromium
PLAYWRIGHT_INTEGRATION=1 go test ./internal/tools/ -run TestBrowserRendererReusesInterstitialClearance
```

Everything else: `go test ./...`.

## 15. Known limitations (all deliberate)

- **No JavaScript by default** — the HTTP path sees the pre-hydration DOM; only the opt-in
  fallback renders. Off by default because Chromium is heavy (hence the separate
  `Dockerfile.browser`).
- **Hostname-only private-IP check** — a public hostname resolving to a private address is not
  caught (`isPrivateHost` comment, scraper.go:243). Accepted because DNS resolution would be
  duplicated by the dialer; `allowPrivate` deployments are trusted anyway.
- **Unsupported non-HTML is a hard error** — PDFs are supported through the document
  extractor, while other binary formats fail rather than degrading to browser rendering.
- **Challenges are reported, never bypassed** — the hard-URL ladder may render a challenge
  up to three times, but nothing is ever clicked, held, or solved; a surviving interstitial
  keeps the URL blocked.
- **40 KB text ceiling** — very long pages reach the LLM truncated; the flag makes it visible.
- **Render concurrency 2** — a burst of fallbacks queues rather than multiplying browsers; a
  long render queue competes with the run's deadline, which is why rendered runs get 300 s.

## 16. Extension recipes

| Change | Where |
| :--- | :--- |
| New challenge marker (e.g. a site-specific interstitial) | `detectBlockPage` marker table (scraper.go:622) + a test in the style of `TestDetectBlockPageIgnoresSubstantivePages` |
| New noise element class | `noiseSelector` (scraper.go:52) — remember it must stay a pre-text CSS selector |
| Different caps (body/text/attempts) | constants block (scraper.go:25–47); per-instance overrides (`fetchAttempts`, `fetchBackoff`) are plain fields, so an `Option` is the idiomatic hook |
| Extra request header | `fetchHTTPOnce` (scraper.go:322) — and mirror anything fingerprint-relevant into the Playwright context if needed |
| Another render engine | implement `browserRenderer` (`Render`/`Close`) and inject via `newBrowser` — no other file changes |
| Extra ladder step (e.g. an archived-copy tactic) | add a `ladderStep` in `ScrapeHard` (scraper.go:219) and assert the order in `TestScrapeHardLadderRecoversAfterBlockedRender`; outcome classification is via `errors.Is` sentinels |
| New private-host pattern | `isPrivateHost` (scraper.go:245), plus the suffix list for hostname forms |

When touching the extractor, run the focused suite first — it's fast and precise:
`go test ./internal/tools/ -run "TestScrapeURL|TestDetectBlockPage|TestRuneSafe|TestIsHTML|TestScraper" -v`.