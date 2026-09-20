# How WebSearch_GO Works — A Complete Project Guide

`WebSearch_GO` is an **agentic web-research server written in Go**. You give it a company
name or a person's name, and it autonomously plans web searches, runs them, ranks the hits,
scrapes the most promising pages, and uses **Google Gemini** to synthesize the raw text into
a structured report — an org chart for companies, a professional dossier for people. All of
this is streamed live to a browser UI over Server-Sent Events (SSE).

This document explains the whole system: the request lifecycle, the role of every module and
file, and the design decisions that hold it together.

---

## 1. The Big Picture

```
Browser (HTMX UI)
      │  POST /api/research          GET /api/events (SSE)
      ▼
cmd/server/main.go ── HTTP gateway, session lifecycle, SSE streaming
      │
      ├──► internal/agent  ── orchestration brain
      │      ├── CompanyAgent  (plan → search → rank → scrape → synthesize → QC loop)
      │      ├── PersonAgent   (search → scrape → synthesize dossier)
      │      └── Engine        (shared tool-calling + caching + event plumbing)
      │
      ├──► internal/tools  ── the agent's "hands"
      │      ├── serper.go    (Google search via serper.dev API)
      │      ├── scraper.go   (fetch + clean webpage text)
      │      └── cookies.go   (optional session cookies for scraping)
      │
      ├──► internal/llm    ── the agent's "brain interface" (Gemini via genai SDK)
      │
      ├──► internal/cache  ── TTL memory cache (search results / scraped pages)
      │
      ├──► internal/models ── shared data structures (OrgChart, PersonProfile, events…)
      │
      └──► internal/web    ── HTML templates embedded into the binary
```

The architecture follows the standard Go project layout: `cmd/` holds the entry point,
`internal/` holds everything that is private to this module (unimportable by other Go
modules), and each sub-package owns one responsibility.

---

## 2. The Request Lifecycle, End to End

1. **User submits a form** on the index page → `POST /api/research` with `mode`
   (`company` or `person`), `query`, and optional scoping fields.
2. **The server creates a session**: a random ID (`sess_` + 16 hex chars), an event channel
   (buffered, 100 events), and a `Done` channel, all tracked by the `SessionHub`.
3. **Research runs in a background goroutine** with a timeout (`RESEARCH_TIMEOUT_SECONDS`,
   default 180 s). The HTTP response returns *immediately* with the "workflow console"
   fragment — the UI never blocks.
4. **The browser opens `GET /api/events?session_id=…`** (SSE). Every agent step — thinking,
   tool call, tool result — is rendered server-side into an HTML fragment and pushed as an
   SSE `log` event; HTMX swaps it into the console live.
5. **When research finishes**, the SSE handler drains remaining events, renders the final
   result fragment (org chart or dossier), sends a status pill (`✓ Complete` / `✗ Failed`),
   and closes the stream.
6. **After a company run**, the user can ask follow-up questions via `POST /api/question`.
   The agent answers from *retained evidence*; if that is insufficient, it asks Gemini to
   emit `needs_fresh_search: true` plus a query, then runs a fresh uncached search and
   re-synthesizes.

There is also a synchronous mode (`?sync=1`) for CLI/testing that blocks until the result is
ready — useful for automated testing without SSE.

---

## 3. Module-by-Module Reference

### `cmd/server/main.go` — HTTP Gateway (entry point)

The composition root. Responsibilities:

- **Wires all dependencies** in a strict order: cookie jar (optional) → memory cache →
  scraper → Serper client → Gemini client (fail-fast: the server refuses to start without
  working Gemini credentials) → `Engine` → the two agents → `SessionHub` → templates.
- **Routes** (Go 1.22+ method-pattern syntax):
  | Route | Handler |
  |---|---|
  | `GET /{$}` | index page (embedded template) |
  | `POST /api/research` | start company/person research |
  | `POST /api/question` | ask a follow-up on a completed *company* session |
  | `GET /api/events` | SSE stream for one session |
  | `GET /healthz` | liveness probe |
- **Hardening**: 1 MiB request-body cap (`http.MaxBytesReader`), server timeouts
  (read/write/idle), security headers on every response (`nosniff`, `X-Frame-Options: DENY`,
  referrer policy, a CSP that whitelists only Tailwind/HTMX CDNs), and graceful shutdown on
  `SIGINT`/`SIGTERM` (waits up to 30 s for in-flight research — important on Cloud Run).
- **Mode dispatch**: person mode passes `query, company, location`; company mode builds a
  `CompanyBrief` from many optional form fields (domain, industry, country, city, areas of
  interest, desired output, time scope, free-form notes).

### `internal/agent/engine.go` — the ReAct Core

The shared orchestration engine both agents delegate to. Key elements:

- **Interface-based decoupling**: `Searcher`, `PageFetcher`, `LLMClient` — the engine only
  depends on these interfaces, so the real HTTP clients can be swapped for test doubles.
- **Cached tool calls**: `SearchCached` (48 h TTL) and `ScrapeCached` (7-day TTL) hash the
  query/URL into a SHA-256 cache key (`cache.HashKey`). `SearchFresh` bypasses the cache —
  used by the Q&A path when the LLM explicitly asks for new evidence.
- **Non-blocking event emission**: `Emit` writes `AgentEvent`s to the session channel with a
  `select`/`default` — a slow SSE consumer can never stall the research goroutine (events
  are dropped with a warning instead).
- **Structured LLM delegation**: `SynthesizeJSON` / `GenerateStructured` forward prompts to
  the Gemini client.
- **Bounds**: max scrapes per run (default 3), max search results per query (default 8),
  max agent steps (default 10) — read from config, with safe defaults if misconfigured.

### `internal/agent/company.go` — Company Research Agent (~17 KB, the largest file)

Runs a **plan → search → rank → scrape → synthesize → review** pipeline, optionally looping:

1. **`planQueries`** — asks Gemini for a `QueryPlan`: up to 6 prioritized queries covering
   leadership, departments, locations, and any user-requested areas. If the LLM fails, a
   **deterministic fallback plan** (hardcoded query templates) keeps the run alive.
2. **`searchPlan`** — executes each planned query via `SearchCached`, deduplicating results
   by URL across queries; emits `tool_call`/`tool_result` events as it goes.
3. **`rankResults`** — asks Gemini to score result relevance (0–1). Falls back to
   **`deterministicRank`** (keyword/term heuristics) if the LLM ranking fails. LLM-ranked
   results are then filtered to URLs that actually exist in the input (`normalizeRankedResults`)
   — a defensive guard against the model hallucinating URLs.
4. **`scrapeRanked`** — scrapes the top-ranked URLs up to `MaxScrapes`, validating each URL
   with `url.ParseRequestURI` first, skipping tiny/useless pages (<80 bytes of text).
5. **`synthesizeChart`** — one Gemini call with all evidence → `models.OrgChart`.
6. **`reviewChart`** — a *self-critique* step: a second Gemini call acting as a strict QC
   reviewer returns a `QualityReview` (verdict pass/revise, score, gaps, contradictions,
   unsupported claims, follow-up queries). If the verdict is `revise`, the loop re-plans with
   at most 4 follow-up queries — **up to 3 iterations total** (`maxCompanyReviewIterations`).
   A failed/empty review is treated as "pass" so QC breakage can't wedge the run.
7. **`AnswerQuestion`** — post-run Q&A: first tries to answer from the retained
   `CompanyResearchContext`; if the LLM signals `needs_fresh_search`, it runs `SearchFresh`,
   scrapes the top hits, merges with old evidence, and answers again. All accumulated
   evidence is retained for transparency and Q&A.

### `internal/agent/person.go` — Person Research Agent

A leaner, single-pass flow:

1. Builds one disambiguated query (`personName + companyName + location`).
2. Searches (cached), then scrapes up to `MaxScrapes` pages, **prioritizing** LinkedIn,
   Wikipedia, "about" pages, GitHub (or the first result regardless).
3. Runs a second career-focused query (`"… speaker interview articles career education"`).
4. Sends the accumulated context to Gemini with a large inline JSON schema prompt →
   `models.PersonProfile` (summary, experiences, education, skills, online presence,
   confidence score).

### `internal/agent/hub.go` — Session State

- **`Session`**: one research run — ID, mode, query, buffered `EventCh`, event history,
  result, error, retained `CompanyContext`, and a `Done` channel closed exactly once
  (`sync.Once`). All access is mutex-guarded.
- **`SessionHub`**: a concurrent map of sessions plus a janitor goroutine that deletes
  sessions older than 15 minutes (checked every 5 minutes) so memory stays bounded.

### `internal/llm/gemini.go` — Gemini Client

Wraps the official `google.golang.org/genai` SDK:

- **Dual backend auth**: `GOOGLE_API_KEY` (AI Studio) *or* Vertex AI (`GOOGLE_GENAI_USE_VERTEXAI=true`
  + project + location, with Application Default Credentials). The model defaults to
  `gemini-2.5-flash-lite`, configurable via `GEMINI_MODEL`.
- **Structured output discipline**: `GenerateStructured` appends a "respond ONLY with valid
  JSON" instruction, then **cleans** the response (`cleanJSONString` strips ``` fences and
  extracts the outermost `{...}` if the model wrapped JSON in prose), and **retries once**
  with a stricter reminder before giving up.
- Temperature 0.2 for determinism; explicit empty/safety-blocked response detection.

### `internal/tools/serper.go` — Search Tool

HTTP client for `google.serper.dev/search`:

- POSTs `{"q": query, "num": maxResults}` with the `X-API-KEY` header; parses the `organic`
  array into `models.SearchResult`.
- **Retry with backoff**: up to 3 attempts with linear backoff (500 ms base) — but only for
  *transient* failures, modeled by a `retriableError` type (network errors, 429, 5xx).
  Non-retriable errors (e.g., 401) fail immediately.
- The error body of failed calls is truncated to 512 chars and included in the error for
  debuggability. The base URL is a struct field so tests can point it at a stub server.

### `internal/tools/scraper.go` — Web Scraper

Fetches a URL and returns clean text:

- **Guards**: 2 MiB body cap before parsing, 40 KB cap on extracted text, 15 s timeout,
  max 5 redirects, HTML-only content types (skips PDFs/images), browser-like headers.
- **Extraction with goquery**: removes scripts/styles/nav/footer/forms/etc., then selects
  only **leaf content blocks** (`h1–h4, p, li, article, section` with
  `:has(...)` exclusion) — this prevents nested duplicates that previously inflated LLM
  context 2–10×. Rune-aware length filtering (>25 runes) drops nav noise; a body-text
  fallback covers sparse pages; truncation never splits a UTF-8 rune.
- A nil cookie jar is replaced with an empty one — avoids a real panic (`http.Client.Jar`
  with a typed-nil jar panics on first use).

### `internal/tools/cookies.go` — Cookie Jar Loader

Optionally loads cookies exported from browser extensions (Cookie-Editor / EditThisCookie
JSON) via `COOKIE_FILE_PATH`, letting the scraper carry authenticated sessions. It handles
both expiration formats (unix `expirationDate` and RFC1123/`expires`), strips leading dots
from domains, skips expired/empty cookies, and tolerates partial failures.

### `internal/cache/cache.go` — TTL Memory Cache

A thread-safe in-memory cache: `sync.RWMutex`-guarded map + FIFO eviction queue. Entries
carry per-key TTLs; a janitor goroutine sweeps expired entries every 10 minutes; the entry
count is bounded (`CACHE_MAX_ENTRIES`, default 2048) so long-running processes can't leak
memory. `HashKey` produces deterministic `prefix:sha256` keys. `Close()` (deferred in
`main`) stops the janitor via a closed channel.

### `internal/models/` — Shared Data Types

| File | Defines |
|---|---|
| `agent.go` | `AgentEvent` (session event stream payload: thinking / tool_call / tool_result / complete / error) and `SearchResult` / `ScrapeResult` (tool outputs) |
| `company.go` | `OrgChart`, `Department`, `Employee`, `Location` — the company research output |
| `person.go` | `PersonProfile`, `Experience`, `Education` — the person dossier output (plus a `ScorePercent` display helper) |
| `research.go` | The research "state machine" types: `CompanyBrief` + `ResearchPreferences` (user input), `PlannedQuery`/`QueryPlan` (search planning), `RankedResult` (LLM relevance judgments), `Evidence`, `QualityReview` (self-critique), `CompanyResearchContext` (retained per-run state powering QC + Q&A), `QuestionAnswer` |

These structs are the contract between the LLM and the rest of the program: every Gemini
call unmarshals its JSON into one of them.

### `internal/web/` — Embedded UI

- `web.go` — `go:embed`s `templates/*.html` into the binary and parses them once. The
  container image therefore needs *only* the static binary.
- `templates/index.html` — the research console page (HTMX + Tailwind from CDNs).
- `templates/fragments.html` — server-rendered HTMX fragments: `workflow_console`,
  `log_entry`, `company_chart`, `company_result`, `person_dossier`, `question_answer`,
  `error_box`. Rendered with `html/template` (auto-escaped against XSS).

### Support Files

| File | Role |
|---|---|
| `go.mod` / `go.sum` | Module `github.com/amitgupta1000/WebSearch_GO`, Go 1.23; deps: `goquery` (HTML parsing), `google.golang.org/genai` (Gemini) |
| `Dockerfile` | Multi-stage build → distroless `nonroot` runtime image; templates are embedded so the image is just the binary |
| `.github/workflows/go.yml` | CI: `go mod tidy`, enforce gofmt, `go vet`, build, test |
| `scripts/dev.ps1` | Windows dev loop: fingerprints all `.go`/`.html` files under `cmd`+`internal` every 750 ms and restarts `go run ./cmd/server` on change (also wired as the VS Code task `Go: Watch Server`) |
| `.vscode/tasks.json` | Registers that watcher task |
| `react_engine.py` | A standalone **Python replica** of the ReAct engine (same architecture, CLI-driven) — useful as a readable reference implementation |
| `DESIGN.md` | The original high-level design doc and implementation plan |
| `PROJECT_GUIDE.md`, `*.pptx`, `generate_*_deck.py` | Generated presentation materials about the project |
| `LLMreasoning.md` | Notes from a prior conversation about LLM reasoning |

### Test Files (mirroring each module)

`engine_test.go`, `company_test.go`, `cache_test.go`, `config_test.go`, `gemini_test.go`,
`cookies_test.go`, `scraper_test.go`, `serper_test.go`, `web_test.go` — covering tool
behavior (via injectable base URLs / stub servers), cache eviction, config defaults, JSON
cleaning, template escaping, and the deterministic ranking fallback.

---

## 4. Data Flow Summary (Company Mode)

```
CompanyBrief (form)
   │
   ▼
planQueries ──────────► QueryPlan (≤6 queries)          [Gemini]
   │
   ▼
searchPlan ───────────► []SearchResult (deduped)        [Serper + cache]
   │
   ▼
rankResults ──────────► []RankedResult (scored)         [Gemini, or heuristics]
   │
   ▼
scrapeRanked ─────────► []Evidence (≤3 pages)           [Scraper + cache]
   │
   ▼
synthesizeChart ──────► OrgChart                        [Gemini]
   │
   ▼
reviewChart ──────────► QualityReview (pass/revise)     [Gemini]
   │
   ├── revise? ──► loop back to searchPlan (≤3 total iterations)
   ▼
chart + CompanyResearchContext ──► SSE result ──► HTMX UI
                                  └─► retained for /api/question
```

Person mode is the same skeleton without planning/QC: two searches → scrape top bio pages →
single synthesis → `PersonProfile`.

---

## 5. Notable Engineering Choices

- **Graceful degradation everywhere**: LLM planning, ranking, and QC all have deterministic
  fallbacks; search retries transient failures; the UI still renders if QC is broken.
- **Bounded resources**: cache entries, scrapes per run, search results, agent steps, request
  body size, scrape body/text size, and session lifetime are all explicitly capped — the
  server can't be runaway-by-design.
- **Concurrency discipline**: mutex-guarded sessions, `sync.Once` completion, buffered
  non-blocking event channels, background goroutines with timeouts and cleanup.
- **Security posture**: body caps, security headers + CSP, `nonroot` distroless container,
  auto-escaped templates, and hallucination guards (URLs filtered against real results).
- **Everything embedded**: templates ship inside the binary, so deployment is one static
  executable per OS/arch.

## 6. Running It

```bash
go mod tidy
go build ./... && go test ./...
go run ./cmd/server        # then open http://localhost:8080
```

Required environment: `SERPER_API_KEY` and Gemini credentials (`GOOGLE_API_KEY`, or the
Vertex AI trio). Optional tuning knobs: `PORT`, `GEMINI_MODEL`, `CACHE_MAX_ENTRIES`,
`RESEARCH_TIMEOUT_SECONDS`, `MAX_SCRAPES_PER_RUN`, `MAX_SEARCH_RESULTS`, `MAX_AGENT_STEPS`,
`COOKIE_FILE_PATH` (see the README table for defaults). On Windows, `.\scripts\dev.ps1`
gives you the auto-reloading dev server.