# WebSearch_GO

Agentic web research server in Go. It runs autonomous research flows over two
tracks and synthesizes structured results with Gemini:

- **Company mode** — discovers leadership pages, builds an org chart with
  departments and locations.
- **Person mode** — disambiguates a person via optional company/location
  constraints and compiles a professional dossier.

The HTTP layer serves an HTMX UI from templates embedded in the binary
(`internal/web`); the agents stream-step through search (Serper API) → scrape →
LLM synthesis, with TTL caching and bounded memory.

> **Documentation:** this file is the user-facing guide (setup, configuration,
> endpoints, policies). **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** is the
> internals companion — request lifecycle, module-by-module deep dive, testing, and
> known limitations — and **[SCRAPER_WALKTHROUGH.md](SCRAPER_WALKTHROUGH.md)** walks the
> fetch path (policy, rendering ladder, extraction) end to end.

## Prerequisites

- Go 1.24+
- A Serper API key ([serper.dev](https://serper.dev))
- Gemini credentials, one of:
  - `GOOGLE_API_KEY` (Gemini API / AI Studio key), **or**
  - Vertex AI with Application Default Credentials:
    `GOOGLE_GENAI_USE_VERTEXAI=true`, `GOOGLE_CLOUD_PROJECT`, `GOOGLE_CLOUD_LOCATION`

## Quickstart

```bash
go mod tidy    # generates go.sum — required before building or using Docker
go build ./...
go test ./...
go run ./cmd/server
# open http://localhost:8080
```

Tests that need external resources are skipped unless their gate is set, so the default
suite runs offline: `PLAYWRIGHT_INTEGRATION=1` enables the real-Chromium tests
(interstitial cookie reuse, and parsing a real generated PDF) and
`GEMINI_INTEGRATION=1` enables the live inline-PDF check. See
[docs/ARCHITECTURE.md § Testing](docs/ARCHITECTURE.md#testing).

For Windows development, run the auto-reloading server instead:

```powershell
.\scripts\dev.ps1
```

It watches Go and embedded template files under `cmd` and `internal`, then
restarts `go run ./cmd/server` after changes. The same watcher is available in
VS Code as the `Go: Watch Server` task. Because it restarts on *every* change, a
running research or question is cut off when you save a file — let a run finish,
or expect it to start over.

If the server exits immediately after printing its startup line, the port is
almost always taken by a previous instance:

```
Server failed: listen tcp :8080: bind: Only one usage of each socket address ...
[FATAL] :8080 is already in use: another process (often a previous instance) is listening there.
```

`dev.ps1` now clears a stale server off the port before each start and reports a
dead child instead of staying silently down; to run a second instance side by
side, give it a free `PORT`.

Research events are also printed in the terminal with `[AGENT]` prefixes. The
browser at `http://localhost:8080` remains the full results view, including the
organization chart and post-run questions; the terminal shows the live trace
and final result summary.

## How a run works

1. **Plan** — Gemini proposes prioritized search queries (with a deterministic
   fallback plan if planning fails or times out).
2. **Search** — every query runs through Serper; results are deduplicated across
   queries and cached for 48 h.
3. **Rank** — BM25 scores every snippet immediately, then Gemini semantically reranks
   the top eight candidates within an eight-second deadline; if that call times out or
   returns invalid URLs, the BM25 order continues straight to scraping. Candidates
   showing no connection to the target company — no official-domain match and no
   distinctive company-name token in the title, snippet or URL — are dropped, so other
   companies' profiles and off-domain pages no longer consume scrape budget. The filter
   never empties the candidate list.
4. **Scrape** — ranked pages are fetched, cleaned and cached for 7 days. Pages are fetched
   several at a time (`SCRAPE_CONCURRENCY`), but politely per host: at most
   `SCRAPE_HOST_CONCURRENCY` in flight and `SCRAPE_HOST_RATE_PER_SEC` per second, with a
   server's `Retry-After` hint honoured on a `429`/`503`. Identical content is collected
   once — pages are deduplicated by URL, by canonical URL, and by a fingerprint of the
   extracted text — and a `404`/`410` is reported without paying for a browser render.
   A blocked page gets one bounded **hard-URL escalation** (plain render → cookie-bridged
   render → extended settle) before degrading to snippet evidence; set
   `HARD_URL_HANDLER=0` to turn that off. Escalations are offered to the shapes the
   ladder can actually read first — a public LinkedIn post or company page renders on
   the first step, while a person profile is an auth-wall without a session and gets
   one cheap attempt instead of two. Failures are cached briefly too, so a repeat
   question does not re-fetch a known dead end
   (see [Fetching policy](#fetching-policy)).
5. **Synthesize** — Gemini writes the org chart or dossier from the supplied
   evidence only, and is instructed not to invent people or titles.
6. **Review** — an independent QC pass scores the draft and may request focused
   follow-up iterations (at most two). A follow-up is only started if enough of the
   research budget remains, so a long run returns partial results rather than
   failing on the deadline. If the reviewer call itself fails, the review is
   reported as `unavailable` rather than silently counting as a pass.
7. **Save** — the finished chart or dossier is written to `OUTPUT_BUCKET` as a Markdown
   document and a PDF, and the console lists both under **Saved files** (see
   [Saved output files](#saved-output-files)).

## Environment variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `PORT` | `8080` | HTTP listen port |
| `GEMINI_MODEL` | `gemini-2.5-flash-lite` | Gemini model name |
| `SERPER_API_KEY` | — (required for search) | Serper.dev API key |
| `GOOGLE_API_KEY` | — | Gemini API key (AI Studio) |
| `GOOGLE_GENAI_USE_VERTEXAI` | unset | Set to `true` to use Vertex AI backend |
| `GOOGLE_CLOUD_PROJECT` / `GOOGLE_CLOUD_LOCATION` | — | Vertex AI project & region |
| `GCP_REGION` | `us-central1` | Region recorded in config (informational; Vertex AI uses `GOOGLE_CLOUD_LOCATION`) |
| `COOKIE_FILE_PATH` | — | Optional JSON cookie export (Cookie-Editor / EditThisCookie format) for the cookie-bridged ladder step. A cookie scoped to `www.example.com` is also registered for `example.com`, so `in.linkedin.com` receives a session exported for `www.linkedin.com`; startup logs what loaded (`Cookie jar: 7 cookies from …`) |
| `ENABLE_BROWSER_FALLBACK` | `false` | Enable bounded Playwright rendering after HTTP errors, challenges, or low-quality JavaScript shells |
| `CACHE_MAX_ENTRIES` | `2048` | Max entries in the in-memory TTL cache |
| `RESEARCH_TIMEOUT_SECONDS` | `180` | Per-request research budget |
| `BROWSER_RESEARCH_TIMEOUT_SECONDS` | `300` | Per-request research budget when a run renders pages in a real browser (clamped to at least `RESEARCH_TIMEOUT_SECONDS`) |
| `MAX_SCRAPES_PER_RUN` | `10` | Max web pages scraped per research session |
| `MAX_SEARCH_RESULTS` | `12` | Max search results fetched per search query |
| `MAX_AGENT_STEPS` | `10` | Max agent/tool iterations per research session |
| `ALLOW_BROWSER_TOGGLE` | `true` | Offer a per-run "render pages in a real browser" option in the console. Only shown when Chromium can actually start |
| `ALLOW_PROFILE_SCRAPING` | `false` | Fetch professional-profile pages (e.g. `linkedin.com`) directly instead of keeping only their public search snippet. Opt-in, because that is a policy decision |
| `HARD_URL_HANDLER` | `true` | Escalate blocked pages and professional profiles to the bounded hard-URL handler (plain render → cookie-bridged render → extended settle) |
| `HARD_URLS_PER_RUN` | `2` | Hard cap on how many URLs one run may escalate. Raise it for profile-heavy briefs, but remember each escalation can take up to `HARD_URL_BUDGET_SECONDS` |
| `HARD_URL_BUDGET_SECONDS` | derived | Budget for the whole ladder on one URL. `0` derives it from the ladder's own step caps (15 s + 15 s + 20 s + margin), so the extended-settle step can never be clipped |
| `VALIDATE_EVIDENCE` | `true` | Date and label every person row from the sources behind it (`verified-recent` / `dated` / `stale` / `undated` / `conflicting`) and allow bounded reverse lookups. The offline half costs nothing; `0` keeps the labels and spends no searches |
| `ENTITY_GATE_MODE` | `enforce` | How strictly sources are held to the confirmed company: `enforce` rejects and removes people no source connects to the company, `report` labels them but keeps them (for measuring the gate's effect), `off` disables the gate entirely for comparison runs |
| `VALIDATION_LOOKUPS_PER_RUN` | `4` | Cap on reverse lookups — `"Name" "Company"`, snippet-only, never fetched — for rows the pass could not date or corroborate. `0` disables the lookups but keeps the labels |
| `VALIDATION_STALE_AFTER_MONTHS` | `36` | Age at which a row's newest source is reported as `stale` |
| `VALIDATION_RECENT_WITHIN_MONTHS` | `18` | Age inside which a dated source from the company's own domain, or from two independent domains, counts as `verified-recent` |
| `CACHE_BACKEND` | `memory` | Where cached searches and scraped pages live: `memory`, `redis` or `gcs` |
| `REDIS_URL` | — | Redis connection URL (`redis://` / `rediss://`). Takes precedence over the split settings below |
| `REDIS_HOST` | — | Redis host; with `REDIS_PORT` / `REDIS_PASSWORD` it is assembled into a connection URL when `REDIS_URL` is unset |
| `REDIS_PORT` | `6379` | Redis port |
| `REDIS_PASSWORD` | — | Redis password (URL-escaped when the connection URL is assembled) |
| `REDIS_KEY_PREFIX` | `websearch-go:` | Key namespace inside a shared Redis database |
| `GCS_CACHE_BUCKET` | `crystal-crm-store` | Bucket used when `CACHE_BACKEND=gcs` |
| `GCS_CACHE_PREFIX` | `WebSearch_GO/cache/` | Object-name prefix inside that bucket |
| `GOOGLE_APPLICATION_CREDENTIALS` | — | Service-account file for Cloud Storage (standard Google ADC variable; an ambient identity is used when it is unset) |
| `OUTPUT_BUCKET` | `crystal-crm-store` | Bucket finished runs are saved to as Markdown + PDF. Empty disables cloud output |
| `OUTPUT_PREFIX` | `WebSearch_GO/` | Folder inside that bucket; artifacts go to `company/` or `person/` beneath it |
| `OUTPUT_LOCAL_DIR` | — | Write artifacts to a local directory instead of the bucket (takes precedence; useful in development) |
| `OUTPUT_FORMATS` | `md,pdf` | Which artifacts to save (`md`, `pdf`) |
| `SCRAPE_CONCURRENCY` | `4` | Pages fetched in parallel per run |
| `SCRAPE_HOST_RATE_PER_SEC` | `2` | Sustained requests per second to any one host (`0` disables) |
| `SCRAPE_HOST_BURST` | `3` | Requests that may start back-to-back per host |
| `SCRAPE_HOST_CONCURRENCY` | `2` | Simultaneous in-flight requests per host (`0` disables) |
| `SCRAPE_REVALIDATE_AFTER_SECONDS` | `86400` | Age at which a cached page is revalidated with `If-None-Match` / `If-Modified-Since` (`0` disables) |
| `SCRAPE_MAX_RETRY_AFTER_SECONDS` | `5` | Cap on how long a server's `Retry-After` hint may delay a retry |

When browser fallback is enabled, install the matching Playwright browser once with
`go run github.com/mxschmitt/playwright-go/cmd/playwright install chromium`.
The renderer is HTTP-first, does not interact with challenges, and records a
blocked result when a CAPTCHA or press-and-hold page remains after rendering.
Rendering launches the full Chromium build (`channel: "chromium"`) rather than
Playwright's headless shell, which crashes ("Target crashed" on the first page) on
some hosts. The renderer keeps one shared browser context so cookies earned by
passing a JavaScript interstitial are reused, and waits for network idle before
extracting.

With `ALLOW_BROWSER_TOGGLE` enabled, the console offers the choice per run: a
"Render pages in a real browser" box on the research form, and a "Re-run with
browser rendering" action on any finished company result so a thin result can be
escalated without re-entering the brief. The re-run reuses the retained brief and
starts a fresh session. Scrape results are cached per rendering mode, so an
HTTP-only result never masks a rendered re-run of the same URL, and asking for
rendering where Chromium is unavailable is reported as an error instead of
silently degrading. Chromium is probed once at startup; when it cannot start the
option is hidden entirely and a warning is logged.

Rendered runs get a larger budget (`BROWSER_RESEARCH_TIMEOUT_SECONDS`, 300s by
default) because each scraped page costs several extra seconds; the live console
shows the budget it is running under. The HTTP write timeout is sized from the
largest configured budget so a long stream is never cut off mid-run.

Troubleshooting: if `playwright install` fails with
`fork/exec .../ms-playwright-go/<version>/node.exe: The system cannot find the file
specified`, the driver directory is half-populated (the playwright-core package
downloaded but the bundled Node.js binary did not). The installer cannot repair it
because it aborts before downloading when the existing `package/` cannot be
executed. Delete that driver directory and re-run the install:

```bash
# Windows
rm -rf "$LOCALAPPDATA/ms-playwright-go/1.62.1"
# Linux/macOS
rm -rf "${XDG_CACHE_HOME:-$HOME/.cache}/ms-playwright-go/1.62.1"
go run github.com/mxschmitt/playwright-go/cmd/playwright install chromium
```

Set `PLAYWRIGHT_NODEJS_PATH` to a preinstalled `node` to skip the bundled Node.js
download entirely.

## Research console

The UI is a single page that streams each agent step over SSE:

- **Live workflow console** — per-step tool calls, results and reasoning, plus a
  badge showing which research budget the run is under.
- **Result panel** — the org chart, with Executive Leadership and Departments always
  visible. The Images gallery, the Scraping Report, the Sources list, the Phase
  Timings table and the Saved files list sit behind
  disclosure buttons (each labelled with its item count) so the answer stays
  readable. Collapsing is visual only: the content remains in the page, so it is
  still found by `Ctrl+F` and in print.
- **Per-run browser rendering** — a "Render pages in a real browser" checkbox on the
  research form, shown only when Chromium can actually start.
- **Escalating a thin result** — a finished company result offers "Re-run with
  browser rendering", which reuses the retained brief in a fresh session so you do
  not have to re-enter it.
- **Ask about this research** — post-run Q&A that answers from retained evidence
  first and only performs a fresh search when the evidence is insufficient.

## Saved output files

Every finished run is written to `OUTPUT_BUCKET` (default `crystal-crm-store`) under
`OUTPUT_PREFIX` (default `WebSearch_GO/`) as two files, so a result outlives the browser
session it was produced in:

```
gs://crystal-crm-store/WebSearch_GO/company/20260923-085002-klj-resources-limited.md
gs://crystal-crm-store/WebSearch_GO/company/20260923-085002-klj-resources-limited.pdf
```

- **Markdown** is rendered from the research models — org chart, departments and members,
  locations, sources, and the run's phase timings. Person runs produce the dossier equivalent
  under `person/`.
- **PDF** is printed from the same content by the Chromium the scraper already runs, so no
  PDF library is involved. The document is self-contained (inline styling, no remote assets).
- **Names are `<timestamp>-<subject>.md`**, which sorts chronologically and keeps an
  accented or non-Latin subject name from producing an unusable file name.
- **One failure never hides the other.** If Chromium is unavailable the PDF artifact reports
  why while the Markdown is still saved; a failed export is recorded in the run's scrape
  report and never fails the research itself. Exports run on a detached context with their
  own budget, so a run that used its whole research deadline still saves its results.
- The console lists what was written in a **Saved files** panel on the result, with a link to
  each object.

Set `OUTPUT_LOCAL_DIR` to write the same files to a local directory instead of the bucket, or
`OUTPUT_FORMATS=md` to skip the PDF.

## Caching

Search results are cached for 48 h and scraped pages for 7 days; failed scrapes are remembered
for 15 minutes so a follow-up question does not re-fetch a known dead end. Scrape entries are
keyed by URL *and* rendering mode, so an HTTP-only result never masks a rendered re-run of the
same page.

`CACHE_BACKEND` decides where those entries live:

| Backend | Survives restarts | Shared between instances | Notes |
| :--- | :--- | :--- | :--- |
| `memory` (default) | no | no | Bounded FIFO cache (2048 entries by default); zero setup |
| `redis` | yes | yes | Native per-key TTL. Connect with `REDIS_URL`, or with `REDIS_HOST` / `REDIS_PORT` / `REDIS_PASSWORD` — the URL is assembled for you and the password is URL-escaped |
| `gcs` | yes | yes | One object per entry with its expiry embedded; credentials from `GOOGLE_APPLICATION_CREDENTIALS` or an ambient identity. Set `GCS_CACHE_BUCKET`, and pair it with a bucket lifecycle rule to reclaim expired objects |

A shared backend is what makes the cache real: a restarted container, or a second Cloud Run
instance, reuses the searches and pages the first one already paid for. If the configured backend
cannot be reached at startup, the server logs a warning and falls back to the in-memory cache — a
cache is an optimization, not a dependency — and the backend actually in use is printed at
startup.

Cached pages are revalidated instead of re-downloaded. The scraper keeps the `ETag` /
`Last-Modified` a page returned, and any entry older than `SCRAPE_REVALIDATE_AFTER_SECONDS`
(`86400`) is refreshed with a conditional request: an unchanged page answers `304 Not Modified`
in a single round trip, and a revalidation that fails for any reason keeps the cached copy rather
than losing evidence.

## Entity gate — what the chart is held to

"Relevant to the brief" is not "about the company". A run for a company called **CJ Shah**
(a name that reads as initials plus a surname) assembled 204 people from 24 sources —
none of them on the company's own domain: a LinkedIn name directory, a textbook by an
author named Shah, a 2016 conference archive, papers on Malaysian procurement, and a
global wealth-management roundup. Every one of those is topically relevant to a
procurement brief; none of them is about CJ Shah.

**Confirm the entity first.** The console offers **Find the exact company first**: it runs
one or two searches and shows candidate identities — domain, evidence, and a registration
number (a CIN read out of a registry page, shown with the page it came from). Pick one,
edit the domain, add other names the entity answers to ("CJ Shah & Co", "KLJ Group"), or
run on the name alone. Candidate domains that do not carry the company's name are flagged,
because gating a run on a rating agency's domain would be worse than gating on none.

**Then every stage is held to that identity** (`internal/agent/entity.go`):

1. **Query plan** — a planned query that names neither the company, an alias, the domain
   nor the registration id is dropped, and the plan is refilled with anchored queries
   (`site:<domain> …`). It is the cheapest gate: an unanchored query is never fetched.
2. **Ranker** — every candidate records *how* it is tied to the company (`domain`,
   `registration-id`, `name`, `alias`), and one that names it nowhere is pushed to the
   back of the queue.
3. **Snippets** — only snippets naming the company reach synthesis. A snippet is a name
   and a title; this is the widest hole, and closing it removed the wealth-management
   roundup and the 2016 summit in one step.
4. **Rows** — with `ENTITY_GATE_MODE=enforce`, a person no source connects to the company
   is removed from the chart and listed under **Unverified / other entities** with the
   title, the department and the source that failed to name the company. A row whose name
   *is* the company name (a shareholding page labelled "CJ SHAH") is excluded as the
   entity, not a person. Rows matched by the name alone carry a visible
   `matched by company name only` warning, because a same-named company matches the same
   way.
5. **QC** — the reviewer is told the exact identity and must report anyone the evidence
   does not connect to it.

The artifact states the identity and what the gate did (`## Entity gate`), so a short
roster is explainable rather than mysterious. `report` mode records everything without
removing anyone, which is how the gate's effect is measured; `off` is the pre-gate
behaviour.

## Fetching policy

The scraper is deliberately conservative, and several of these rules surprise people:

- **Only `http` and `https` are fetched.** Other schemes are refused before the
  browser fallback sees them — Playwright would otherwise happily follow `file://`.
- **Loopback, private and link-local targets are refused.** Scrape targets come from
  search results and model output, so `127.0.0.1`, RFC1918 ranges and cloud metadata
  endpoints (`169.254.169.254`) are blocked. There is no server option to relax
  this, so the app cannot research an intranet.
- **Bot challenges are reported, never bypassed.** A CAPTCHA or press-and-hold page
  is rendered like any other, and a blocked result is escalated once to the hard-URL
  handler — a plain render, a cookie-bridged render, and an extended-settle render —
  but nothing is ever clicked, held, or solved. If the challenge survives every step
  the result is marked `blocked` with a reason and the run degrades to snippet evidence.
- **Professional-profile pages are escalated, not fetched by default.** A `linkedin.*`
  URL contributes its public search snippet, and the ladder gets one bounded attempt
  (at most two URLs per run, 30 s each, and only while at least 45 s of the research
  budget remains). The handler bridges the cookie jar into the browser context, so a
  session exported via `COOKIE_FILE_PATH` can authenticate a rendered page. Set
  `ALLOW_PROFILE_SCRAPING=true` to fetch such pages through the normal scrape path too,
  or `HARD_URL_HANDLER=0` to disable escalation entirely — both are policy decisions, so
  check the terms of the sites involved first.
  - **The shapes are not equally recoverable, and the ladder now says so.** Measured
    across live runs: LinkedIn posts and company pages came back on the first render
    (1–2 s, 160–2,919 words), while every logged-out `/in/…` person profile exhausted
    both steps (15–25 s) and fell back to its snippet. Escalation slots therefore go to
    posts and company pages first, and a logged-out person profile or `/pub/dir/` page
    gets only the cheap first attempt instead of also waiting 20 s of extended settling
    for a sign-in page. A blocked page (no session involved) still gets all three steps.
  - **A failure now says which session was missing:** the reason carries
    `(no cookies for in.linkedin.com: set COOKIE_FILE_PATH …)` whenever the cookie step
    was skipped for lack of a session, so an unreachable profile is no longer ambiguous.
  - **Raising `HARD_URLS_PER_RUN` buys attempts, not results.** With the cap at 2 vs 6 in
    an A/B on the same brief, more escalations added prose and two office locations but
    no new people, at +67 % scrape time. Keep it low and spend the budget on the run.
- **PDFs are read; every other non-HTML response is rejected.** A PDF is sent to
  Gemini, which reads PDFs natively, and falls back to a bundled pure-Go parser when
  that call fails — so officer lists published as registry filings become evidence. The
  scrape report shows which path ran (`pdf-gemini` / `pdf-local`). Images, archives and
  other binary payloads still fail with `unsupported content type`, and text that comes
  back as machine noise rather than words is discarded instead of being quoted.
- **Bodies are capped at 2 MiB and extracted text at ~40 KB.** A body that hits the
  cap is parsed from an incomplete document and flagged as `content_truncated`.
- **Legacy encodings are decoded.** The `Content-Type` charset (or a `<meta>`
  declaration) is honoured, so ISO-8859-1 and windows-1252 pages are not mangled.
- **Transient failures are retried** up to three times with linear backoff, and
  failed scrapes are remembered for 15 minutes so a follow-up question does not
  re-fetch a known dead end. The retry lengthens worst-case per-URL time, which is
  part of why rendered runs get a larger budget.
- **Parallel fetches stay polite.** A run fetches several pages at once, but each host
  is limited independently (`SCRAPE_HOST_CONCURRENCY` at a time, `SCRAPE_HOST_RATE_PER_SEC`
  per second), and a `429`/`503` carrying `Retry-After` is honoured — capped by
  `SCRAPE_MAX_RETRY_AFTER_SECONDS` — instead of burning three quick retries.
- **Missing pages are not rendered.** A `404` or `410` is reported as a failure rather than
  costing several seconds of browser time; `403`s still render, because a browser sometimes
  gets through where a plain HTTP client cannot.
- **Rendered pages load no images, fonts or CSS,** because the renderer only reads the DOM
  text: those requests are aborted, and a page that already has its content when the DOM is
  ready skips the network-idle and settle waits entirely. A render therefore costs seconds
  rather than tens of seconds.
- **Identical content is collected once.** Pages are deduplicated by URL (ignoring `www.`,
  tracking parameters, fragments and parameter order), by the page's own canonical URL, and
  by a fingerprint of the extracted text, so a site's `/about`, `/about-us` and `/company`
  pages do not each consume a scrape.

## Endpoints

| Method | Path | Description |
| :--- | :--- | :--- |
| `GET` | `/` | Research console (HTMX UI) |
| `POST` | `/api/research` | Start research: `mode` (`company`/`person`), `query`, `company_name`, `location`, optional company-brief scope fields, and optional `use_browser` (`1`/`0`) to render this run's pages in a real browser. Returns the live workflow console, or the final result fragment with `?sync=1` (used by the CLI and the org-chart drill-down). Full field list: [HTTP API reference](docs/ARCHITECTURE.md#http-api-reference). |
| `POST` | `/api/rerun` | Form fields: `session_id`, optional `use_browser`. Re-runs a completed company session's retained brief in a new session, which is how a thin result is escalated with browser rendering. |
| `POST` | `/api/question` | Form fields: `session_id`, `question`. Answers a question from retained evidence or runs a fresh focused search when needed. |
| `GET` | `/api/events` | Server-Sent Events (SSE) stream for live step logs & final synthesis. |
| `GET` | `/healthz` | Liveness probe |

## Security headers

Every response sets `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`,
and a restrictive Content-Security-Policy:

```
default-src 'self';
script-src  'self' 'unsafe-inline' https://cdn.tailwindcss.com https://unpkg.com;
style-src   'self' 'unsafe-inline' https://cdn.tailwindcss.com;
connect-src 'self';
img-src     'self' data: https: http:
```

`img-src` allows remote images because the result panel shows a gallery of images
discovered on scraped sources; without it the browser blocks every one of them. Both
`https:` and `http:` are permitted, because the kind of corporate site this tool
researches still serves images over plain http. Images are passive content and
`no-referrer` keeps the browsing host out of the image request, which limits
third-party tracking. (On an HTTPS deployment browsers block mixed-content `http:`
images anyway, so the extra permission only matters for local/http origins.)

The CDN allowances exist because `index.html` loads Tailwind and HTMX from
`cdn.tailwindcss.com` and `unpkg.com`. To serve the UI without any third-party
origins, vendor those two scripts into `internal/web` and drop the hosts from
`script-src`/`style-src`.

## Docker

```bash
go mod tidy   # ensure go.sum exists first
docker build -t websearch-go .                                 # HTTP-only, distroless
docker build -f Dockerfile.browser -t websearch-go-browser .   # includes Chromium
docker run --rm -p 8080:8080 -e SERPER_API_KEY=... -e GOOGLE_API_KEY=... websearch-go
```

The default runtime image is distroless (`nonroot`); templates are embedded, so the
image contains only the static binary. Use `Dockerfile.browser` (based on the
matching Playwright image) together with `ENABLE_BROWSER_FALLBACK=true` or the
per-run toggle when you need rendering in a container. See
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the system design.