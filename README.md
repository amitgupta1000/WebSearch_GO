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
| `MAX_SEARCH_RESULTS` | `8` | Max search results fetched per search query |
| `MAX_AGENT_STEPS` | `10` | Max agent/tool iterations per research session |
| `ALLOW_BROWSER_TOGGLE` | `true` | Offer a per-run "render pages in a real browser" option in the console. Only shown when Chromium can actually start |

When browser fallback is enabled, install the matching Playwright browser once with
`go run github.com/mxschmitt/playwright-go/cmd/playwright install chromium`.
The renderer is HTTP-first, does not interact with challenges, and records a
blocked result when a CAPTCHA or press-and-hold page remains after rendering.
The default `Dockerfile` remains distroless and HTTP-only; use
`Dockerfile.browser` when enabling browser fallback in a container.

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

## Endpoints

| Method | Path | Description |
| :--- | :--- | :--- |
| `GET` | `/` | Research console (HTMX UI) |
| `POST` | `/api/research` | Form fields: `mode` (`company`/`person`), `query`, `company_name`, `location`, plus optional company scope fields `company_country`, `company_city`, `company_domain`, `company_industry`, `areas_of_interest`, `desired_output`, `time_scope`, and `research_notes`. Optional `use_browser` (`1`/`0`) opts a single run into browser rendering. Company research performs up to 3 QC reviews. Returns the live workflow console. |
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
docker build -t websearch-go .
docker run --rm -p 8080:8080 -e SERPER_API_KEY=... -e GOOGLE_API_KEY=... websearch-go
```

The runtime image is distroless (`nonroot`); templates are embedded so the
image contains only the static binary. See `DESIGN.md` for the system design.