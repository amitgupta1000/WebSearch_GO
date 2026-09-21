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

## Prerequisites

- Go 1.23+
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

One test that drives real Playwright/Chromium (verifying that a clearance cookie
from a JavaScript interstitial is reused across renders) is skipped unless
`PLAYWRIGHT_INTEGRATION=1` is set, so the default suite needs no browser.

For Windows development, run the auto-reloading server instead:

```powershell
.\scripts\dev.ps1
```

It watches Go and embedded template files under `cmd` and `internal`, then
restarts `go run ./cmd/server` after changes. The same watcher is available in
VS Code as the `Go: Watch Server` task.

Research events are also printed in the terminal with `[AGENT]` prefixes. The
browser at `http://localhost:8080` remains the full results view, including the
organization chart and post-run questions; the terminal shows the live trace
and final result summary.

Company result ranking uses a hybrid path: an in-memory BM25 pass ranks all
search snippets immediately, then Gemini semantically reranks only the top
eight candidates with an eight-second deadline. If Gemini times out or returns
invalid URLs, the BM25 ranking continues directly to scraping.

## How a run works

1. **Plan** — Gemini proposes prioritized search queries (with a deterministic
   fallback plan if planning fails or times out).
2. **Search** — every query runs through Serper; results are deduplicated across
   queries and cached for 48 h.
3. **Rank** — BM25 scores every snippet immediately, then Gemini reranks the top
   candidates. Only results the ranking returns as relevant are carried forward.
4. **Scrape** — ranked pages are fetched, cleaned and cached for 7 days. Failures
   are cached briefly too, so a repeat question does not re-fetch a known dead end
   (see [Fetching policy](#fetching-policy)).
5. **Synthesize** — Gemini writes the org chart or dossier from the supplied
   evidence only, and is instructed not to invent people or titles.
6. **Review** — an independent QC pass scores the draft and may request focused
   follow-up iterations (at most two). A follow-up is only started if enough of the
   research budget remains, so a long run returns partial results rather than
   failing on the deadline. If the reviewer call itself fails, the review is
   reported as `unavailable` rather than silently counting as a pass.

## Environment variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `PORT` | `8080` | HTTP listen port |
| `GEMINI_MODEL` | `gemini-2.5-flash-lite` | Gemini model name |
| `SERPER_API_KEY` | — (required for search) | Serper.dev API key |
| `GOOGLE_API_KEY` | — | Gemini API key (AI Studio) |
| `GOOGLE_GENAI_USE_VERTEXAI` | unset | Set to `true` to use Vertex AI backend |
| `GOOGLE_CLOUD_PROJECT` / `GOOGLE_CLOUD_LOCATION` | — | Vertex AI project & region |
| `COOKIE_FILE_PATH` | — | Optional JSON cookie export (Cookie-Editor / EditThisCookie format) |
| `ENABLE_BROWSER_FALLBACK` | `false` | Enable bounded Playwright rendering after HTTP errors, challenges, or low-quality JavaScript shells |
| `CACHE_MAX_ENTRIES` | `2048` | Max entries in the in-memory TTL cache |
| `RESEARCH_TIMEOUT_SECONDS` | `180` | Per-request research budget |
| `BROWSER_RESEARCH_TIMEOUT_SECONDS` | `300` | Per-request research budget when a run renders pages in a real browser (clamped to at least `RESEARCH_TIMEOUT_SECONDS`) |
| `MAX_SCRAPES_PER_RUN` | `10` | Max web pages scraped per research session |
| `MAX_SEARCH_RESULTS` | `12` | Max search results fetched per search query |
| `MAX_AGENT_STEPS` | `10` | Max agent/tool iterations per research session |
| `ALLOW_BROWSER_TOGGLE` | `true` | Offer a per-run "render pages in a real browser" option in the console. Only shown when Chromium can actually start |

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
  visible. The Images gallery, the Scraping Report and the Sources list sit behind
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

## Fetching policy

The scraper is deliberately conservative, and several of these rules surprise people:

- **Only `http` and `https` are fetched.** Other schemes are refused before the
  browser fallback sees them — Playwright would otherwise happily follow `file://`.
- **Loopback, private and link-local targets are refused.** Scrape targets come from
  search results and model output, so `127.0.0.1`, RFC1918 ranges and cloud metadata
  endpoints (`169.254.169.254`) are blocked. There is no server option to relax
  this, so the app cannot research an intranet.
- **Bot challenges are reported, never bypassed.** A CAPTCHA or press-and-hold page
  is rendered like any other; if the challenge is still present afterwards the
  result is marked `blocked` with a reason rather than circumvented.
- **Non-HTML content is rejected.** A PDF response fails the scrape with
  `unsupported content type`, which matters for registries that publish officer
  lists as PDFs.
- **Bodies are capped at 2 MiB and extracted text at ~40 KB.** A body that hits the
  cap is parsed from an incomplete document and flagged as `content_truncated`.
- **Legacy encodings are decoded.** The `Content-Type` charset (or a `<meta>`
  declaration) is honoured, so ISO-8859-1 and windows-1252 pages are not mangled.
- **Transient failures are retried** up to three times with linear backoff, and
  failed scrapes are remembered for 15 minutes so a follow-up question does not
  re-fetch a known dead end. The retry lengthens worst-case per-URL time, which is
  part of why rendered runs get a larger budget.

## Endpoints

| Method | Path | Description |
| :--- | :--- | :--- |
| `GET` | `/` | Research console (HTMX UI) |
| `POST` | `/api/research` | Form fields: `mode` (`company`/`person`), `query`, `company_name`, `location`, plus optional company scope fields `company_country`, `company_city`, `company_domain`, `company_industry`, `areas_of_interest`, `desired_output`, `time_scope`, and `research_notes`. Optional `use_browser` (`1`/`0`) opts a single run into browser rendering. Company research performs up to 3 QC reviews. Returns the live workflow console; add `?sync=1` to run synchronously and return the rendered chart or dossier fragment instead (used by CLI and automated checks). |
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
img-src     'self' data: https:
```

`img-src` allows remote `https:` images because the result panel shows a gallery of
images discovered on scraped sources; without it the browser blocks every one of
them. Images are passive content, and `no-referrer` keeps the browsing host out of
the image request, which limits third-party tracking. Note that **plain `http:`
image URLs stay blocked** — a scraped page that references `http://…/logo.png`
will show a caption with no image.

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
per-run toggle when you need rendering in a container. See `DESIGN.md` for the
system design.