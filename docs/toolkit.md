# Toolkit Reference (`internal/tools`)

The tools live in `internal/tools` (a Go package). The package splits into **core fetching infrastructure**, **legal-source clients**, and **data-enrichment clients**.

## Core fetching infrastructure

| File | Purpose |
|---|---|
| **serper.go** | Client for the Serper.dev API — the Google-search entry point all research agents query first. POSTs the query, parses organic results + knowledge-graph entries into `models.SearchResult`, and retries transient failures (network errors, 429, 5xx) with linear backoff. |
| **scraper.go** | The central HTTP scraper. Fetches pages with browser-like Chrome UA, retries transient failures, honors `Retry-After`, enforces body-size caps, detects bot-walls/challenge pages (Cloudflare etc.), extracts readable text, and handles non-HTML documents (PDFs). Also enforces safety: HTTP(S)-only and blocks private/loopback hosts (SSRF guard). Everything else builds on it. |
| **scraper_browser.go** | Headless-browser (Playwright/Chromium) layer. Renders JavaScript-heavy or bot-walled pages plain HTTP can't read (with cookie-bridged user sessions), blocks heavy resources (images/fonts/CSS) for speed, and doubles as an HTML→PDF printer (with optional page footers) used by report export. |
| **cookies.go** | Loads a user-exported browser session (`COOKIE_FILE_PATH`) into a Go cookie jar. Understands both export shapes — browser-extension arrays (Cookie-Editor / EditThisCookie) and Playwright storage state — handles divergent expiry formats, skips expired cookies, and widens host-scoped cookies to their registrable parent domain. Reports what was loaded. |
| **egress_proxy.go** | Per-host egress proxy routing. Some hosts block datacenter IPs on sight (weedoo.in, linkedin.com, indiankanoon.org), so those route through residential proxies configured via env knobs (`WEEDOO_PROXY_URL`, `LINKEDIN_PROXY_URL`, …) while everything else keeps the normal `HTTPS_PROXY` behavior — protecting the proxy's bandwidth budget. |
| **hostlimit.go** | Per-host politeness limiter: token-bucket rate limit plus a concurrency cap per target host, so parallel fetching never concentrates on one site (which earns 429s). Bounded memory with idle-host pruning. |
| **robots_guard.go** | Fetches and caches one site's robots.txt and refuses to fetch disallowed paths (e.g., IndianKanoon's court-ordered removal `/doc/` paths). Fails open if robots.txt can't be read. |
| **hardshape.go** | Classifies LinkedIn URL shapes (person profile / directory / post / company / other). Shape predicts whether escalation can pay off: public shapes (posts, company pages) are prioritized in the hard-URL ladder; auth-walled shapes (person profiles) are deprioritized since only a real session clears them. |
| **pdf.go** | PDF text extraction. Primary path is Gemini (handles scanned/compressed PDFs), with a pure-Go local parser fallback that caps pages/bytes and survives panics on malformed files. Also does PDF sniffing by content-type + magic bytes, since registry portals serve filings as generic binaries. |
| **source_doc.go** | Small shared type (`SourceDoc`) normalizing document/listing entries from the official-register sources (SEBI, ICAI) plus a relative→absolute URL normalizer. |

## Legal-source clients (the "legal mode" corpus)

| File | Purpose |
|---|---|
| **indiankanoon.go** | Client for indiankanoon.org — the *primary* Indian case-law source. Server-rendered search parsing (title, snippet, court, petitioner/respondent/case-number metadata, cited-by counts), judgment document fetching, and its own extra-polite throttle (1 req/sec) plus the robots guard. |
| **casemine.go** | Client for casemine.com — the *second* legal source. Two-step fetch ladder: plain HTTP with a Chrome UA (the site's wall is UA-only), falling back to the cookie-bridged browser render if blocked. Parses the result cards into the same `LegalSearchHit` shape. |
| **juristway.go** | Client for juristway.com — a *commercial* case-law DB. Rides its LoopBack REST API on paths robots.txt explicitly allows; extracts the free-tier layer (parties, court, bench, dates, editorial headnotes) and public detail pages; full text is paywalled. Optional best-effort login token caching. |
| **icai.go** | Client for icai.org (Institute of Chartered Accountants of India) — the professional-body layer: guidance notes, accounting standards, disciplinary matters. Reads `/category/<slug>` archive pages and extracts document/PDF links, excluding navigation links. |
| **sebi.go** | Client for sebi.gov.in — securities-enforcement orders (which name audit firms/CAs as parties). Two discovery paths: the official orders search POST endpoint, and the site's sitemap.xml filtered to the `/enforcement/orders/` family (titles/dates reconstructed from URL slugs). |

## Data-enrichment clients

| File | Purpose |
|---|---|
| **rocketreach.go** | Client for the RocketReach API — paid, explicitly-requested contact lookups (email/phone by name+company or LinkedIn URL). Own transport pinned to direct egress (proxying caused mid-response cuts), bounded retries, and rich HTTP-error preservation (quota/plan details) so failures are diagnosable. |
| **tradedb.go** | Client for the Crystal import-export analytics API — cleaned Indian customs trade records behind a FastAPI/Postgres read layer. Fail-soft by design (trade research downgrades to web-only when unreachable), bounded concurrency, token-matching helpers for region/foreign-port name matching, and the entity-profile family: aggregated per-company profiles (rank within the profiled top set, shipments, value) and per-entity product mixes for any name the data carries. Its `/names` typeahead matches *substrings of the stored spelling* and is punctuation-blind, so callers must not treat a raw typeahead hit as an identity — `internal/agent/trade_entity_resolve.go` owns that judgement (see below). |
| **weedoo.go** | Client for weedoo.in (Indian company registry) driven through a dedicated headless browser, since it's fully client-rendered. Resolves company names → CINs via the search bar's typeahead (proximity-based name/CIN attribution, cached including misses), and fetches rendered company pages for the directors/DIN tables. Routes through the residential proxy when configured. |

### Company-entity resolution (one resolver, every surface)

A researched company name is almost never the DB's stored spelling ("Sasol Chemicals" vs
`SASOL CHEMICALS (PTY) LTD`, "KLJ Resources Limited" vs `KLJ RESOURCES LTD`), so resolution is a
first-class step and it is **shared, never per-mode**. `internal/agent/trade_entity_resolve.go`
owns it; `BuildTradeProfile` composes it with the profile and product-mix lookups, and three
surfaces consume that one function: the company dossier's Trade DB background, the verified rows
of both landscape models, and the on-demand Fetch Trade Profile button. Landscape trade
*sections* (importers/suppliers per product) need no resolution at all — those names come **from**
the DB.

Landscape trade sections are budgeted report-wide: `TRADE_DB_MAX_PARTNERS` (default 20, max 40)
caps both sections *together* - importers take the head, suppliers the remainder, and the report
warns when the supplier section was trimmed. The region-scoped supplier ranking reads the 1,000
most recent declarations at the region's ports (one bounded page), and the report names that
window whenever the page came back full, so a recency-limited ranking is never read as the whole
history.

The contract:

1. **Normalize** — case, `&`↔`and`, dots/commas/dashes/slashes/brackets, then drop trailing legal
   forms across the forms the data actually carries (`ltd`, `pvt`, `pty`, `pte`, `bhd`, `gmbh`,
   `ag`, `se`, `sa`, `bv`, `nv`, `srl`, `spa`, `kgaa`, ...; dotted forms split by normalization,
   `N.V.` -> `n v`, go as a pair).
2. **Walk a query ladder** — the typed name, its suffix-stripped core, then its first two
   significant tokens, because the typeahead only matches substrings of the stored spelling.
3. **Score every hit** — normalized equal (1.0) and core equal (0.98) are the *confident* tiers
   and resolve automatically; token containment (0.85) and a character-similarity band down to
   0.45 are suggestions only.
4. **Suggest, don't guess** — a miss returns ranked candidates (semantic re-rank by one bounded
   model call, validated against the candidate list, when a model is available; the deterministic
   order stands otherwise). The operator picks from chips on the dossier card or in an unmatched
   Fetch Trade Profile result (`POST /api/trade/entity-profile`); the pick is remembered per
   company for the process, invalidates the cached answer, and skips the typeahead. Company
   Research also carries a pre-run matcher (`POST /api/trade/entity-match`) whose chips fill
   `trade_entity`/`trade_entity_direction`, and its customs background is opt-in
   (`company_trade_records`), so a run never queries the DB unseen. Headless runs never pick for
   the operator: a miss stays a miss, with the closest spellings named in the note.
5. **Launch the DB itself** - the console's *Explore Crystal TradeDB* button (header and both
   landscape sections) posts a password to `POST /api/trade/explore`; only a match redirects
   into the Firebase frontend (`CRYSTAL_DB_URL`), so the URL is never in the page. The password
   (`CRYSTAL_DB_PASSWORD`) protects the link, not the app: the Firebase app's own sign-in is the
   real boundary, and the redirect opens in a new tab so the console keeps its run.

## How they fit together

The agents (`internal/agent`) orchestrate these — Serper for search, Scraper + browser for page reads, the five legal clients for Indian legal research, and RocketReach/TradeDB/WeeDoo as gated enrichment sources for person, trade, and company-registry work — with hostlimit/robots_guard/egress_proxy/cookies as the shared plumbing that keeps all of it polite, unblocked, and compliant.

