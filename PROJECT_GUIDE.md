# WebSearch_GO — Project Guide

This document explains how the **WebSearch_GO** project works end-to-end and the role
played by every module and file. It complements `README.md` (usage quickstart) and
`DESIGN.md` (high-level design) by walking through the actual code paths.

> **In one sentence:** WebSearch_GO is a self-contained Go web server that runs
> autonomous, LLM-driven web research agents — building company org charts and person
> dossiers from live Google searches and web scraping — and streams every agent step to
> the browser in real time over Server-Sent Events.

## Table of Contents

1. [What this project is](#1-what-this-project-is)
2. [How a research request flows end-to-end](#2-how-a-research-request-flows-end-to-end)
3. [System architecture](#3-system-architecture)
4. [Repository layout & file roles](#4-repository-layout--file-roles)
5. [Module deep dive](#5-module-deep-dive)
6. [Cross-cutting mechanisms](#6-cross-cutting-mechanisms)
7. [Configuration reference](#7-configuration-reference)
8. [HTTP API reference](#8-http-api-reference)
9. [Testing](#9-testing)
10. [Building, running & deploying](#10-building-running--deploying)
11. [Files that are not part of the Go service](#11-files-that-are-not-part-of-the-go-service)
12. [Project health & known issues](#12-project-health--known-issues)

---

## 1. What this project is

WebSearch_GO is an **agentic AI research server** written in idiomatic Go
(module `github.com/amitgupta1000/WebSearch_GO`, Go 1.23). It performs autonomous
web research in two modes:

| Mode | What it produces | How it works |
| :--- | :--- | :--- |
| **Company** (`mode=company`) | A structured `OrgChart`: overview, HQ, leadership, departments, locations, sources | LLM plans a set of Google queries → results are ranked for relevance → the top pages are scraped → Gemini synthesizes the org chart from the collected evidence |
| **Person** (`mode=person`) | A structured `PersonProfile` dossier: title, company, bio, experiences, education, skills, confidence score | Disambiguated search (name + company + location) → bio-oriented pages are scraped → a secondary career search supplements snippets → Gemini synthesizes the dossier |

The system is built from six cooperating layers:

- **HTTP layer** (`cmd/server`, `internal/web`) — an HTMX-powered single-page console,
  with templates embedded into the binary via `go:embed`.
- **Agentic orchestration** (`internal/agent`) — a shared *Engine* plus two domain
  specialists (company / person) that plan, search, rank, scrape and synthesize.
- **Tooling** (`internal/tools`) — Serper.dev Google-search client, a sanitized HTML
  scraper, and browser-cookie import.
- **LLM layer** (`internal/llm`) — a Gemini client (`gemini-2.5-flash-lite` by default)
  that returns *structured JSON* with cleaning + retry.
- **State & caching** (`internal/cache`, `internal/agent/hub.go`) — a bounded TTL cache
  for searches/scrapes and a thread-safe session hub for live event streams.
- **Shared vocabulary** (`internal/models`) — all structs that cross package boundaries.

The binary is fully self-contained (templates embedded), runs on Cloud Run-style
environments (distroless non-root Docker image, graceful SIGTERM shutdown, `/healthz`
probe), and enforces strict budgets on cost and runtime (max scrapes, max search
results, max agent steps, per-request timeout).

---

## 2. How a research request flows end-to-end

The interactive (default) path, step by step:

1. **User opens `GET /`** — `handleIndex` renders the embedded `index.html` console
   (Tailwind + HTMX loaded from CDNs).
2. **User submits the form → `POST /api/research`** (HTMX posts the form fields:
   `mode`, `query`, `company_name`, `location`, plus optional company-brief fields).
   The body is capped at 1 MiB (`maxFormBytes`) as DoS protection.
3. **`handleResearch` validates the query**, generates a random session ID
   (`sess_` + 16 hex chars from `crypto/rand`), then:
   - **Async (default):** creates a `Session` in the `SessionHub` (with a buffered
     `EventCh` of capacity 100 and a `Done` channel) and starts a **background
     goroutine** that runs the appropriate agent with a `ResearchTimeout` context.
     It immediately returns the `workflow_console` HTML fragment, which the browser
     swaps into the page.
   - **Sync (`?sync=1`):** runs the agent inline on the request goroutine and renders
     the final `company_chart` / `person_dossier` fragment directly. Used for CLI
     tests and for the "Open dossier" drill-down buttons on the org chart.
4. **The agent runs its pipeline** (details in §5.2). As it works, it emits
   `models.AgentEvent` values (thinking / tool_call / tool_result / complete / error)
   into the session's channel via the **non-blocking** `agent.Emit` helper — a slow
   consumer can never stall the research itself.
5. **The browser opens `GET /api/events?session_id=...`** — an HTMX SSE extension
   connection. `handleEvents`:
   - sets `text/event-stream` headers,
   - **replays** any events already recorded in the session history,
   - streams each new event as an SSE `log` event containing rendered `log_entry`
     HTML (color-coded per event type),
   - when the agent finishes (`session.Done` closes), drains remaining buffered
     events, then sends the final result fragment — `company_chart` or
     `person_dossier`, or `error_box` on failure — plus a status pill, and a final
     `close` event that tells HTMX to stop listening.
6. **Drill-down (company mode):** the rendered org chart embeds small forms per
   employee ("Open dossier") that POST `/api/research?sync=1` with `mode=person`,
   the employee's name, the company name and their location — rendering a
   `person_dossier` straight into the chart's drill-down area.

There are exactly two research entry points, both in `cmd/server/main.go`:

- `CompanyAgent.ResearchCompanyWithBrief(ctx, sessionID, CompanyBrief, eventCh)` → `*models.OrgChart`
- `PersonAgent.ResearchPerson(ctx, sessionID, name, company, location, eventCh)` → `*models.PersonProfile`

Passing `nil` as `eventCh` (sync mode / tests) silently disables event emission.

---

## 3. System architecture

```mermaid
flowchart TD
    Browser([Browser — HTMX console]) -->|"GET /"| Index["cmd/server<br/>handleIndex"]
    Browser -->|"POST /api/research"| Research["cmd/server<br/>handleResearch"]
    Browser <-->"SSE GET /api/events" Events["cmd/server<br/>handleEvents"]

    Research --> Hub["agent.SessionHub<br/>(session lifecycle)"]
    Research -->|"mode router"| CA["agent.CompanyAgent"]
    Research -->|"mode router"| PA["agent.PersonAgent"]

    CA --> Eng["agent.Engine<br/>(plan/search/rank/scrape/synthesize plumbing)"]
    PA --> Eng

    Eng -.->|"interfaces:<br/>Searcher / PageFetcher / LLMClient"| Tools["tools.SerperClient<br/>tools.Scraper"]
    Eng -.-> LLM["llm.GeminiClient"]
    Eng --> Cache["cache.MemoryCache<br/>(TTL + FIFO bound)"]

    Tools --> SerperAPI[(Serper.dev<br/>Google Search)]
    Tools --> Web[(Target<br/>websites)]
    LLM --> Gemini[(Gemini API /<br/>Vertex AI)]

    Hub --> Events
    Research --> Frag["internal/web templates<br/>(embedded via go:embed)"]
    Events --> Frag
```

**Key architectural decisions worth knowing:**

- **Dependency inversion at the agent boundary.** `internal/agent` never imports
  `internal/tools` or `internal/llm`. The `Engine` declares three small interfaces —
  `Searcher`, `PageFetcher`, `LLMClient` — and `cmd/server` wires the concrete
  implementations (`*tools.SerperClient`, `*tools.Scraper`, `*llm.GeminiClient`) into
  it at startup. This is why the agent package can be unit-tested with plain mocks.
- **`internal/models` is the shared vocabulary.** Every package boundary crossing
  (search results, scraped pages, events, briefs, plans, final outputs) uses structs
  from `internal/models`, so packages stay decoupled.
- **The HTTP layer is the composition root.** `cmd/server/main.go` constructs the
  object graph in dependency order and injects it; nothing else constructs
  dependencies.
- **Streaming, not polling.** Agent progress reaches the UI through a per-session
  channel → SSE handler → HTMX swap chain, with history replay for (re)connects.

---

## 4. Repository layout & file roles

```
WebSearch_GO/
├── cmd/
│   └── server/
│       ├── main.go              # Composition root: config, DI, routes, SSE, shutdown
│       ├── main_test.go         # SSE termination, browser-choice policy, security headers
│       └── sse_test.go          # SSE reconnect behaviour
├── internal/
│   ├── agent/
│   │   ├── engine.go            # ReAct core: tool interfaces, cached search/scrape, Emit, synthesis plumbing
│   │   ├── company.go           # Company agent: plan → search → rank → scrape → synthesize + QC loop
│   │   ├── person.go            # Person agent: disambiguation → bio scraping → dossier synthesis
│   │   ├── hub.go               # Session + SessionHub: thread-safe per-research event buses
│   │   ├── engine_test.go       # Engine limits, negative scrape cache, render-mode cache keys
│   │   └── company_test.go      # Fallback query plan, ranking, QC loop, profile candidates
│   ├── cache/
│   │   ├── cache.go             # Bounded in-memory TTL cache (FIFO eviction) + SHA-256 keys
│   │   └── cache_test.go
│   ├── config/
│   │   ├── config.go            # Environment loading with defaults + positive-int validation
│   │   └── config_test.go
│   ├── llm/
│   │   ├── gemini.go            # Gemini client: structured JSON, cleaning, JSON + transient retry
│   │   └── gemini_test.go       # cleanJSONString + transient-error classification tests
│   ├── models/
│   │   ├── agent.go             # AgentEvent (5 event types), SearchResult, ScrapeResult
│   │   ├── company.go           # OrgChart (+UnverifiedSourceCount), Department, Employee, Location
│   │   ├── person.go            # PersonProfile (+ScorePercent), Experience, Education
│   │   └── research.go          # Briefs, QueryPlan, RankedResult, Evidence, ScrapeReportEntry,
│   │                            #   QualityReview (tolerant follow_up_queries), QuestionAnswer
│   ├── tools/
│   │   ├── serper.go            # Serper.dev search client with retry + linear backoff
│   │   ├── scraper.go           # Fetch policy, charset decode, noise-stripping text extractor, retries
│   │   ├── scraper_browser.go   # Optional Playwright renderer (full Chromium, shared context)
│   │   ├── scraper_browser_integration_test.go  # Opt-in real-Chromium test (PLAYWRIGHT_INTEGRATION=1)
│   │   ├── cookies.go           # Browser cookie-export JSON → http.CookieJar
│   │   └── serper_test.go / scraper_test.go / cookies_test.go
│   └── web/
│       ├── web.go               # go:embed of templates + template set parsing
│       ├── web_test.go          # Template parsing / escaping / disclosure-panel tests
│       └── templates/
│           ├── index.html       # Full-page console (form, mode toggle, browser toggle)
│           └── fragments.html   # error_box, log_entry, workflow_console, company_chart,
│                                #   company_result, question_answer, person_dossier
├── scripts/
│   └── dev.ps1                  # Windows auto-reload dev server (watches cmd/ + internal/)
├── .github/workflows/go.yml     # CI: tidy → gofmt → vet → build → test
├── .vscode/tasks.json           # "Go: Watch Server" VS Code task (wraps dev.ps1)
├── Dockerfile                   # Multi-stage: golang:1.23-alpine → distroless nonroot (HTTP-only)
├── Dockerfile.browser           # Playwright base image variant, for browser rendering
├── go.mod / go.sum              # Module: goquery + genai + playwright-go + golang.org/x/net
├── README.md                    # Usage: prerequisites, env vars, endpoints, Docker, policies
├── DESIGN.md                    # High-level design (HLD) + implementation plan
├── HOW_IT_WORKS.md              # Narrative walkthrough of the pipeline and modules
├── react_engine.py              # Standalone Python replica of the ReAct engine (CLI)
├── generate_pro_deck.py         # python-pptx generator for the executive guide deck
├── LLMreasoning.md / .pdf / .pptx  # LLM reasoning executive guide artifacts
└── websearch.exe                # Compiled Windows binary (build artifact, gitignored-safe)
```

---

## 5. Module deep dive

### 5.1 `cmd/server/main.go` — HTTP gateway & composition root

The only `package main` in the repo. Responsibilities:

**Startup sequence (`main`)** — builds the object graph in dependency order:

1. `config.Load()` — read env vars; warn (but don't exit) if `SERPER_API_KEY` is missing.
2. `tools.LoadCookieJarFromFile(cfg.CookieFile)` — optional browser cookies; warn on failure.
3. `cache.NewMemoryCache(cfg.CacheMaxEntries)` — bounded TTL cache (`defer Close()`).
4. `tools.NewScraperWithBrowser(cookieJar, 15s, cfg.EnableBrowserFallback)` and
   `tools.NewSerperClient(cfg.SerperAPIKey)`.
5. `llm.NewGeminiClient(ctx, cfg.GeminiModel)` — **fail-fast**: `log.Fatalf` if
   Gemini credentials are missing, since no research can run without it.
6. `agent.NewEngine(...)` with the three budgets, then `NewCompanyAgent`,
   `NewPersonAgent`, `NewSessionHub`.
7. `web.Templates()` — parse the `go:embed`-ed templates (fail-fast on parse errors).
8. **Chromium probe** — when the per-run toggle is enabled (or fallback is on),
   `scraper.BrowserAvailable()` starts the driver/browser once and caches the answer,
   so the console never advertises rendering that cannot actually work.
9. Assemble `Server{cfg, companyAgent, personAgent, hub, tmpl, browserAvailable,
   allowBrowserToggle}`.

**Routing & HTTP hardening:**

| Route | Handler | Notes |
| :--- | :--- | :--- |
| `GET /{$}` | `handleIndex` | Renders the `index.html` console (capability-gated browser toggle) |
| `POST /api/research` | `handleResearch` | Research launcher (async + `?sync=1` modes, optional `use_browser`) |
| `POST /api/rerun` | `handleRerun` | Re-runs a completed company session's retained brief in a new session |
| `POST /api/question` | `handleQuestion` | Post-run Q&A over retained evidence, with fresh-search fallback |
| `GET /api/events` | `handleEvents` | SSE stream of agent events + final result |
| `GET /healthz` | `handleHealth` | Liveness probe returning `ok` |

All responses pass through `securityHeaders` (`X-Content-Type-Options: nosniff`,
`X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, and a CSP that allows the
Tailwind/HTMX CDNs plus remote `https:` images so the scraped-image gallery can
render). The `http.Server` sets `ReadHeaderTimeout` (10s), `ReadTimeout` (30s),
`IdleTimeout` (90s) and `WriteTimeout = max(ResearchTimeout, BrowserResearchTimeout) + 30s`
so a long research can still finish streaming — a rendered run draws on the larger
budget. SIGINT/SIGTERM trigger a graceful `Shutdown` with a 30 s drain (Cloud Run
sends SIGTERM on scale-in).

**Handlers:**

- `handleResearch` — reads and trims form fields into a `models.CompanyBrief`
  (name, domain, industry, country, city, areas of interest, desired output,
  time scope, free-form notes). Rejects empty queries with an `error_box` fragment.
  `browserChoice(r)` resolves the optional `use_browser` field against the
  deployment policy — asking for rendering where Chromium is unavailable is an
  error, not a silent downgrade. The async path delegates to `startRun`; the sync
  path (`?sync=1`) runs the agent inline with a timeout context and renders the
  result fragment directly.
- `startRun` — shared launcher for `handleResearch` and `handleRerun`. Creates the
  session via `hub.Create(...)`, spawns `go func(){ …; session.Complete(result, err) }()`
  with `withBrowserOverride(ctx, …)` applied and the per-run budget taken from
  `cfg.ResearchTimeoutFor(browserEnabled)`, then renders `workflow_console` with the
  session id, display mode, query and budget badge.
- `handleRerun` — looks the session up, reads the retained
  `CompanyResearchContext.Brief`, and starts a fresh run for it. This is how the
  console escalates a thin result with browser rendering without re-entering the brief.
- `handleEvents` — verifies the session exists, requires `http.Flusher` support,
  then loops a `select` over the request context (client disconnect),
  `session.EventCh` (new events → `AddHistory` + SSE `log` frames), and
  `session.Done` (drain remaining events → render final fragment → `status` pill →
  `close` event). The `sendSSE` helper correctly formats multi-line SSE `data:` fields.
- `render` / `renderError` — execute named templates with `html/template`
  auto-escaping; `splitFormList` turns comma-separated form values into slices.
- `newSessionID` — 8 random bytes from `crypto/rand`, hex-encoded with a `sess_` prefix.

### 5.2 `internal/agent` — the agentic core

#### `engine.go` — shared ReAct orchestration plumbing

The `Engine` is the shared "brain stem" both domain agents build on. It owns the
LLM client, the search/scrape tools (as interfaces), the cache, and the three
budget knobs.

- **Tool interfaces (dependency inversion):**
  - `Searcher` — `Search(ctx, query, maxResults) ([]models.SearchResult, error)`
  - `PageFetcher` — `ScrapeURL(ctx, url) (*models.ScrapeResult, error)`
  - `LLMClient` — `GenerateStructured(ctx, sysPrompt, userPrompt, target any) error`
  `*tools.SerperClient`, `*tools.Scraper` and `*llm.GeminiClient` satisfy these
  without the agent package importing them.
- **Budget accessors** (`MaxScrapes`, `MaxSearchResults`, `MaxSteps`) — nil-safe and
  falling back to the package defaults (3 / 8 / 10) when configured ≤ 0.
- **Caching decorators:**
  - `SearchCached` — cache key `search:<sha256(query)>`, TTL **48 h**.
  - `ScrapeCached` — cache key `scrape:<sha256(url)>`, TTL **7 days**.
  Both return the cached value on a hit and store successful results on a miss.
- **`Emit`** — package-level event publisher. Non-blocking `select` with `default`:
  if the 100-slot channel is full the event is dropped and logged, never blocking
  research. A `nil` channel (sync mode, tests) is a no-op. Stamps `Timestamp`.
- **`SynthesizeJSON`** — wraps gathered context data with a standard instruction and
  delegates to `GenerateStructured`; used for all final synthesis steps.
- **`GenerateStructured`** — guarded passthrough to the LLM interface (errors if no
  client is configured). Also used for intermediate planning/ranking calls.
- **`FormatSearchSnippets`** — renders search results as a numbered markdown list
  (`1. [title](link)` + snippet) — the compact format fed to the LLM as context.

#### `hub.go` — session lifecycle (thread-safe)

- **`Session`** — one research run: `ID`, `Mode`, `Query`, buffered `EventCh`
  (capacity 100), `Events` history slice, `Result any` + `Err` (final outcome), a
  `Done chan struct{}` closed exactly once (via `sync.Once`), and `CreatedAt`.
  Methods: `AddHistory` / `GetHistory` (mutex-guarded, returns a copy) and
  `Complete(result, err)` which stores the outcome and closes `Done`.
- **`SessionHub`** — `RWMutex`-protected map of sessions. `Create` registers a new
  session; `Get` looks one up. A background goroutine runs every 5 minutes and
  deletes sessions older than **15 minutes** (`CreatedAt`-based), so finished
  streams and abandoned sessions cannot leak memory.

#### `company.go` — CompanyAgent: plan → search → rank → scrape → synthesize

`ResearchCompanyWithBrief` (and the simpler `ResearchCompany` wrapper) runs a
six-stage pipeline, emitting events as it goes. `ResearchCompanyRun` additionally
retains a `CompanyResearchContext` (plan, results, evidence, scrape report, reviews)
so the result can answer post-run questions:

1. **Plan (`planQueries`)** — one LLM call turns the `CompanyBrief` (name, domain,
   industry, country, city, areas of interest, desired output, time scope, notes)
   into a `QueryPlan`: up to **6 deduplicated queries**, each with a `purpose`,
   `target_area` and `priority` 1–5, covering leadership, departments, locations
   and user-requested areas. If the LLM call fails or returns nothing, the
   deterministic **`fallbackQueryPlan`** builds three standard queries
   (leadership / departments / locations, scoped by country+city) plus one query per
   requested area of interest. `limitQueryPlan` trims, dedupes (case-insensitive)
   and drops empty queries. *(Step 1: "Building a research plan…")*
2. **Search (`searchPlan`)** — executes each planned query through
   `Engine.SearchCached`, emitting a `tool_call` per query and one `tool_result`
   summary. URLs are deduplicated case-insensitively across all queries. Individual
   query failures are tolerated (kept as `lastErr`); the run only fails if **no**
   results were collected at all. *(Steps 3..6/7)*
3. **Rank (`rankResults`)** — one LLM call scores every result 0–1 for relevance to
   the brief and returns `ranked_results` (URL, relevant flag, score, reason,
   evidence type). `normalizeRankedResults` then keeps only URLs that actually
   appeared in the search input and are marked relevant, sorted by score.
   If the LLM call fails, **`deterministicRank`** takes over: a keyword heuristic
   (base 0.35, +0.30 if the company name matches, +0.05 per matching interest term
   or leadership/department/location keyword, +0.20 if the official domain matches,
   relevant when score ≥ 0.45).
4. **Scrape (`scrapeRanked`)** — walks ranked URLs in score order, validating each
   with `url.ParseRequestURI` (absolute URL with scheme+host required), and scrapes
   at most `MaxScrapes` pages via `Engine.ScrapeCached`. Pages whose extracted text
   is ≤ 80 bytes are discarded as noise. Survivors become `models.Evidence`
   (URL, title, content, score, evidence type) plus the `sources` list.
   *(Steps 8+: "Scraping selected relevant source…")*
   - **Professional profiles are never scraped.** A `linkedin.*` host is classified
     `professional_profile`; only its search snippet is kept as evidence and the
     scrape report records `skipped`. This is a deliberate policy, and it is the main
     reason department-level results stay thin for companies whose functional
     leadership is only visible on such profiles.
   - Every attempt (success, failure, skip) lands in `chart.ScrapeReport`, which the
     console renders as the collapsible Scraping Report and summarises through
     `OrgChart.UnverifiedSourceCount`.
5. **Synthesize (`synthesizeChart`)** — a strict "corporate intelligence analyst"
   system prompt tells Gemini to use **only** the supplied evidence (no invented
   employees or reporting lines) and emit an `OrgChart` JSON with an explicit
   `departments[].members` schema. Evidence originating from `professional_profile`
   snippets is re-presented as a labelled **profile candidates** block, so a headline
   that pairs a name with a function and a company is not lost among the page text.
   Missing company name / domain are backfilled from the brief; `Sources` and
   `LastUpdated` are stamped locally. Synthesis requires at least one usable piece of
   evidence ("no usable company evidence was collected").
6. **Review (`reviewChart`)** — an independent LLM pass scores the draft and may
   return `revise` with focused follow-up queries, for up to
   `maxCompanyReviewIterations = 3` passes. Two guards matter:
   - A follow-up iteration only starts when at least `minQCIterationBudget = 60s` of
     the run's deadline remains, so a long run returns partial results instead of
     failing on the deadline.
   - If the reviewer call fails or returns no verdict, the review is recorded as
     `unavailable` (score 0) and logged — never reported as a pass.

`formatEvidence` renders the scraped pages as `--- Evidence N: title (url), score ---`
blocks for the synthesis prompt, and `formatProfileCandidates` renders the
snippet-sourced candidates.

#### `person.go` — PersonAgent: disambiguation → footprint → dossier

`ResearchPerson` runs a more linear pipeline:

1. **Query construction** — `query1 = personName [+ companyName] [+ location]`,
   giving the LLM-free disambiguation constraints from the form. *(Step 1)*
2. **Primary search** — `SearchCached(query1)`. Failure is fatal (emits an `error`
   event). *(Steps 2–3)*
3. **Targeted scraping** — iterates results and scrapes up to `MaxScrapes` pages,
   prioritizing URLs containing `linkedin`, `wikipedia`, `about`, `github` (or the
   very first result). Successful scrapes (> 80 bytes) are appended to the gathered
   context as `--- Source: title (url) ---` blocks. *(Steps 4–6)*
4. **Secondary search** — `"<query1> speaker interview articles career education"`
   adds career-milestone snippets to the context; these URLs go into `sources`
   directly (no scraping). *(Step 6)*
5. **Synthesis** — an "elite talent intelligence researcher" system prompt spells out
   the exact `PersonProfile` JSON schema (name, title, company, location,
   multi-paragraph executive summary, experiences with highlights, education,
   key skills, notable projects, online presence map, confidence score).
   `Sources` and `LastUpdated` are stamped locally. *(Steps 7–8)*

### 5.3 `internal/tools` — external-world adapters

#### `serper.go` — Google search via Serper.dev

`SerperClient` POSTs `{"q": ..., "num": ...}` to `https://google.serper.dev/search`
with the key in the `X-API-KEY` header (10 s HTTP timeout; `baseURL` is overridable
in tests). Robustness features:

- **Retry with linear backoff:** up to 3 attempts; network errors and HTTP
  429 / 5xx are wrapped as a private `retriableError` and retried with
  `500 ms × (attempt−1)` sleeps that respect `ctx` cancellation. Non-retriable
  errors (e.g. 401, 400) return immediately.
- **Debuggable errors:** failed responses include up to 512 bytes of the response
  body; a missing key short-circuits with a clear message.
- **Normalization:** the `organic` array is mapped into `models.SearchResult`
  (title, link, snippet, position, date). The `knowledgeGraph` field is parsed but
  currently unused downstream.

#### `scraper.go` — fetch + sanitize web pages

`Scraper` turns an arbitrary URL into clean text for the LLM under an explicit fetch
policy:

- **Target policy (`validateTarget`).** Only `http`/`https` are accepted — other
  schemes are refused *before* the browser renderer can follow them, since Playwright
  would happily load `file://`. Loopback, private and link-local hosts are refused
  too, so targets derived from search results or model output cannot reach internal
  services or cloud metadata (`169.254.169.254`). Relaxing this is a code change
  (`tools.WithPrivateHostAccess()`), not configuration.
- **Polite, bounded HTTP:** browser-like `User-Agent` / `Accept` / `Accept-Language`
  headers, connection pooling (`MaxIdleConns: 100`, `10` per host), 15 s timeout,
  max **5 redirects**, and a guaranteed non-nil cookie jar (an empty jar is created
  when none is supplied, because a typed-nil `*cookiejar.Jar` on `http.Client` would
  panic).
- **Retry:** network errors and 408/429/5xx are retried up to 3 times with linear
  backoff; a 404-style client error is returned immediately so the browser fallback
  can decide what to do with it.
- **Safety guards:** only HTML/XHTML `Content-Type`s are parsed (PDFs and images are
  rejected with `unsupported content type`). The body is read through a **2 MiB**
  `io.LimitReader` one byte past the cap, so a truncated document is *detected*
  rather than silently parsed and is surfaced as `ContentTruncated`. The final text
  is rune-safe truncated to **40 KB** (never splits a UTF-8 rune, appends a
  `...[content truncated]...` marker).
- **Encoding:** the `Content-Type` charset (or a `<meta>` declaration) is honoured
  through `golang.org/x/net/html/charset`, so ISO-8859-1 / windows-1252 pages are
  decoded rather than mangled into mojibake.
- **Block detection (`detectBlockPage`)** — *visible* text is checked for challenge
  markers (press-and-hold, "verify you are human", captcha, unusual traffic,
  access denied), never raw markup, so inline `<script>` source cannot trip it. The
  check only applies to short pages, so an article that merely mentions a marker is
  not misclassified. A hit sets `Blocked` + `BlockReason`: the scraper reports the
  challenge instead of attempting to defeat it.
- **Content extraction (goquery):** removes `script/style/noscript/nav/footer/
  header/aside/svg/form/iframe/button/[role=navigation]/[role=banner]` plus cookie,
  consent, sidebar, advert and related-content blocks, then selects only **leaf**
  content blocks — `h1-h4, p, li, article, section` that do *not* contain another such
  tag — preventing the nested-duplicate problem that previously inflated LLM context
  2–10×. Blocks under 25 runes are dropped; if nothing survives, `<body>` text is the
  fallback, joined with separators so adjacent blocks do not run together; 3+
  consecutive newlines are collapsed.
- Returns a `models.ScrapeResult` carrying `URL`, `CanonicalURL`, `Title`, `Content`,
  `Headings`, `JSONLD`, `Images`, `WordCount`, `ByteSize`, `QualityScore`,
  `FetchedAt`, `Blocked`/`BlockReason` and `ContentTruncated`.

#### `scraper_browser.go` — optional Playwright rendering

`NewScraperWithBrowser(..., enableBrowser)` adds a bounded renderer used only when the
HTTP path errors, is blocked, or yields a JavaScript shell:

- **Full Chromium, not the headless shell.** Launch uses `channel: "chromium"`
  because Playwright's bundled `chrome-headless-shell` crashes ("Target crashed" as
  soon as a page opens) on some hosts; the full browser is also a more realistic
  fingerprint for interstitial-gated pages.
- **One shared browser context**, created lazily and reused across renders, so a
  clearance cookie earned by passing a JavaScript interstitial survives to the next
  page. A fresh `Page` per render keeps it concurrency-safe.
- **Settle before extracting** — after `DOMContentLoaded` it waits for network idle
  (bounded) and then a short settle, which is what gives an interstitial time to run
  to completion and redirect.
- **Bounded concurrency** — a `renderSem` counting semaphore caps simultaneous
  renders, and rendering is skipped entirely when the effective setting is off.
- **Challenge-safe** — if the rendered page is still a challenge the result stays
  `blocked`; nothing is clicked, held, or solved.
- **Per-request override.** The setting is process-wide
  (`ENABLE_BROWSER_FALLBACK`) with a per-request override carried on the context
  (`tools.WithBrowserFallback` / `BrowserFallbackOverride`). That is how the console's
  per-run toggle and `POST /api/rerun` switch behaviour for a single run without
  mutating shared state — and why `Engine.scrapeCacheKey` includes the render mode, so
  a thin HTTP-only result can never mask a browser-rendered re-run of the same URL.
- **The hard-URL ladder** (`Scraper.ScrapeHard`) escalates a URL the normal path could
  not read with up to three attempts: `plain-render`, `cookie-render` (the shared jar's
  cookies for the domain are copied into the browser context via `AddCookies` —
  previously `COOKIE_FILE_PATH` only ever reached the HTTP client, never Chromium), and
  `extended-settle` (8 s network-idle + 2.5 s settle for slow interstitials). Success
  requires unblocked, non-thin content; failures wrap the sentinel errors
  `ErrHardLadderUnavailable` / `ErrHardLadderExhausted`, which the calling agent classifies.

#### `cookies.go` — browser cookie import

`LoadCookieJarFromFile` reads the JSON export format of Cookie-Editor /
EditThisCookie extensions and populates a standard `http.CookieJar`:

- Skips empty and already-expired cookies (understands both unix
  `expirationDate` and `expires` date strings, with an RFC1123 fallback for
  non-GMT zone abbreviations).
- Strips the leading `.` from domains (`.example.com` → `example.com`) so cookies
  cover subdomains, groups them per domain, and installs them under
  `https://<domain>`. Reports how many domains could not be parsed.
- With no path configured it returns an empty (but valid) jar.

### 5.4 `internal/llm/gemini.go` — Gemini client

Wraps `google.golang.org/genai` (default model `gemini-2.5-flash-lite`):

- **Auth handled by the SDK** — `GOOGLE_API_KEY` for the Gemini API, or
  `GOOGLE_GENAI_USE_VERTEXAI=true` + `GOOGLE_CLOUD_PROJECT` + `GOOGLE_CLOUD_LOCATION`
  for Vertex AI with ADC. Initialization errors name the missing configuration.
- **`GenerateContent`** — system instruction as a separate `Content`, temperature
  **0.2** (low creativity for factual extraction), joins the text parts of the first
  candidate; errors clearly when the response is empty or safety-blocked. Transient
  API failures (`genai.ServerError`, plus 429/408 `ClientError`s) are retried up to
  **3 attempts** with linear backoff, so a single "service unavailable" cannot kill a
  multi-minute research run. Request errors and cancellations are never retried.
- **`GenerateStructured`** — appends a "Respond ONLY with valid JSON…" instruction,
  decodes with `decodeJSON` → `cleanJSONString` (strips markdown fences, extracts
  the outermost `{...}` when prose surrounds it). If parsing still fails, it
  **retries once** with a stricter instruction before giving up. This makes the
  pipeline resilient to the usual LLM formatting drift.
- `cleanJSONString` is unit-tested in `gemini_test.go`.

### 5.5 `internal/cache/cache.go` — bounded TTL cache

`MemoryCache` is the only cache implementation, used by `Engine.SearchCached` /
`ScrapeCached`:

- **Keys:** `cache.HashKey(prefix, input)` = prefix + SHA-256 hex of the input —
  deterministic, collision-safe, avoids unbounded key lengths in memory.
- **Get/Set:** `RWMutex`-guarded. `Get` performs a lazy expiry check; `Set` stores
  `value any` with an absolute expiration time.
- **Bounded memory:** an insertion-order queue drives **FIFO eviction** — when the
  map exceeds `maxEntries` (default `2048`, from `CACHE_MAX_ENTRIES`), the oldest
  entries are evicted, so long-running servers cannot accumulate unbounded state.
- **Hygiene:** a background goroutine sweeps expired entries every 10 minutes;
  `Close()` (idempotent, called via `defer` in `main`) stops it through a `done`
  channel. Non-positive size limits fall back to `DefaultMaxEntries`.

### 5.6 `internal/models` — shared vocabulary

Pure data structures (plus tiny display helpers); no business logic. Every package
exchanges data through these types:

| File | Contents | Role |
| :--- | :--- | :--- |
| `agent.go` | `EventType` (`thinking`, `tool_call`, `tool_result`, `complete`, `error`), `AgentEvent` (+`FormattedTime` for the UI clock), `SearchResult`, `ScrapeResult` (incl. `Blocked`/`BlockReason` and `ContentTruncated`) | The agent's observability contract and raw tool outputs |
| `company.go` | `OrgChart` (+`UnverifiedSourceCount()`), `Department`, `Employee`, `Location` | Final company-mode output shape (also the synthesis JSON schema Gemini must fill). Custom `UnmarshalJSON` tolerates a string-or-object `headquarters` and numeric `headcount_est`; `UnverifiedSourceCount` drives the "not enough detail?" hint |
| `person.go` | `PersonProfile` (+`ScorePercent()`), `Experience`, `Education` | Final person dossier shape; `ScorePercent` rounds the 0–1 confidence to a whole percent for the UI badge |
| `research.go` | `ResearchPreferences`, `CompanyBrief` (embeds preferences), `PlannedQuery` / `QueryPlan`, `RankedResult`, `Evidence`, `ScrapeReportEntry`, `QualityReview`, `CompanyResearchContext`, `QuestionAnswer` | Inputs and intermediate artifacts of the company pipeline, plus the retained context used to answer post-run questions. `QualityReview` has a custom `UnmarshalJSON` because the model returns `follow_up_queries` as either strings or objects |

### 5.7 `internal/web` — embedded UI layer

- **`web.go`** — `//go:embed templates/*.html` plus `Templates()`, which parses all
  embedded templates into one `html/template` set. Embedding makes the binary
  self-contained: it can be started from any working directory and the distroless
  Docker image needs nothing but the executable.
- **`templates/index.html`** — the full page: Tailwind + HTMX (incl. the SSE
  extension) from CDN, header, and the research form (`hx-post="/api/research"`,
  target `#results-container`). A `toggleFields()` inline script swaps visible
  fields between company mode (domain, industry, country, city, areas of interest,
  desired output, time scope, notes) and person mode (company name, location).
- **`templates/fragments.html`** — seven HTMX fragments:
  - `error_box` — red error banner (data auto-escaped).
  - `log_entry` — one terminal-style line per `AgentEvent`, with a timestamp and a
    color-coded badge per event type (purple thinking, sky tool_call, emerald
    tool_result, teal complete, red error).
  - `workflow_console` — the live console: terminal-style header with mode/query and a
    budget badge, an "Autonomous Agent Running..." status pill, plus three `sse-swap`
    targets (`log`, `status`, `result`) wired to
    `sse-connect="/api/events?session_id=..."`, `sse-close="close"`.
  - `company_chart` — org chart card: HQ/domain header, overview, leadership grid, and
    department cards. The Images gallery, Scraping Report and Sources list are wrapped
    in `<details>`/`<summary>` disclosure buttons (each labelled with its item count)
    so the answer stays readable; the content remains in the DOM, so it is still
    matched by `Ctrl+F` and in print. Per-employee "Open dossier" drill-down forms and
    the "N collected source(s) could not be used" note live here too.
  - `company_result` — wraps `company_chart` and adds the **"Not enough detail?"**
    panel whose "Re-run with browser rendering" form posts to `/api/rerun`, plus the
    "Ask about this research" form.
  - `question_answer` — rendered answer with confidence and a collapsible source list.
  - `person_dossier` — dossier card: name/title/company/location, confidence score
    pill (via `ScorePercent`), executive summary, career history, education and
    skill chips.

All interpolation is auto-escaped by `html/template` (verified by `web_test.go`).

---

## 6. Cross-cutting mechanisms

### 6.1 Caching strategy

| Cached artifact | Key | TTL | Purpose |
| :--- | :--- | :--- | :--- |
| Serper search results | `search:<sha256(query)>` | 48 h | Repeat queries (common during iterative research and drill-downs) don't cost API credits |
| Scraped page text | `scrape:<sha256(url, render mode)>` | 7 days | Pages rarely change; re-scraping is the slowest tool call |
| Failed scrape (negative) | same shape as above | 15 min | Stops a follow-up question re-fetching a known dead end (403, PDF, empty page). Cancellations are never cached |
| Hard-URL escalation | `scrape:<sha256(url, mode=hard)>` | 7 d / 15 min | QC follow-up iterations reuse a recovered page and never repeat a ladder that ended still blocked |

All four share one bounded `MemoryCache` (FIFO-evicted at `CACHE_MAX_ENTRIES`). The
cache is process-local: restarting the server clears it (a Redis/external backend
would be the natural extension point).

The scrape key includes the **render mode** (`default` / `http` / `browser` / `hard`,
derived from any per-request browser override or the hard-URL ladder). Without it, a
thin HTTP-only result cached by an earlier run would be served to a browser-rendered
re-run of the same URL — and a cached failure would suppress the rendered attempt that
might have succeeded.

### 6.2 Agent observability (event contract)

`models.AgentEvent` is the single observability contract: `SessionID`, `Type`
(one of five `EventType`s), `Step` (1-based pipeline position), `Content`,
optional `ToolName`/`ToolInput`, `Timestamp`. Agents only *emit*; the SSE handler
*consumes*, persists history, and renders. This separation keeps agents UI-agnostic
and lets tests pass `nil` channels.

### 6.3 Concurrency model

- One goroutine per research run (spawned in `handleResearch`); the HTTP handler
  returns immediately.
- Communication is channel-based: buffered `EventCh` (non-blocking sends via
  `Emit`) and `Done` (closed once via `sync.Once`).
- Shared state is small and locked: `Session.Events` (mutex), `SessionHub` (RWMutex),
  `MemoryCache` (RWMutex).
- `Engine` itself is stateless per run (only immutable config), so concurrent
  research sessions are safe.

### 6.4 Budgets & guards (cost/runaway protection)

| Guard | Where | Default |
| :--- | :--- | :--- |
| Max agent steps per run | `MAX_AGENT_STEPS` → `Engine.MaxSteps` | 10 |
| Max pages scraped per run | `MAX_SCRAPES_PER_RUN` → `Engine.MaxScrapes` | 10 |
| Max search results per query | `MAX_SEARCH_RESULTS` → `Engine.MaxSearchResults` | 12 |
| Company query plan size | `limitQueryPlan` | 6 queries |
| QC review passes | `maxCompanyReviewIterations` | 3 (≤ 2 follow-ups) |
| Min budget to start a QC follow-up | `minQCIterationBudget` | 60 s |
| Per-run wall clock | `RESEARCH_TIMEOUT_SECONDS` → context timeout | 180 s |
| Per-run wall clock, browser rendering | `BROWSER_RESEARCH_TIMEOUT_SECONDS` | 300 s |
| Form body size | `maxFormBytes` in `main.go` | 1 MiB |
| Scraped HTML body | `maxBodyBytes` in `scraper.go` | 2 MiB |
| Text sent to LLM per page | `maxContentBytes` (rune-safe) | 40 KB |
| Scrape attempts per URL | `scraperMaxAttempts` | 3 (linear backoff) |
| LLM attempts per call | `geminiMaxAttempts` | 3 (linear backoff) |
| Concurrent browser renders | `maxConcurrentRenders` (semaphore) | 2 |
| Failed scrape retention | `negativeScrapeTTL` | 15 min |
| LLM temperature | `gemini.go` | 0.2 |
| HTTP server timeouts | `main.go` (header/read/idle/write) | 10/30/90s / max(research, browser research)+30s |

### 6.5 Error handling & resilience

- **Serper:** retries transient failures (network, 429, 5xx) with linear backoff;
  fatal errors surface immediately.
- **Gemini:** one stricter retry when JSON parsing fails, plus up to 3 attempts for
  transient API failures (5xx / 429 / 408). Non-transient request errors and
  cancellations return immediately.
- **Company pipeline:** per-query search failures are tolerated; LLM planning and
  ranking each have deterministic fallbacks, so research still completes if
  intermediate LLM calls misbehave (only the final synthesis has no fallback).
- **QC review:** a failed or empty review is recorded as `unavailable` (score 0,
  logged) — never as a pass. A follow-up iteration is skipped when under 60 s of
  budget remains, so a long run returns what it has instead of failing on the deadline.
- **Scraping:** transient fetch failures are retried up to 3 times; individual page
  failures, blocked pages and empty pages are recorded in the scrape report and
  skipped, and the failures are cached for 15 minutes so a follow-up question does not
  re-attempt them.
- **SSE:** buffered channel + history replay + drain-on-done ensure no event is
  lost to a late or reconnecting viewer (beyond the deliberate drop-on-overflow).
- **Shutdown:** in-flight research gets up to 30 s to finish on SIGTERM/SIGINT.

### 6.6 Security posture

- `html/template` auto-escaping everywhere; strict CSP (`img-src` additionally allows
  remote `https:` images so the scraped-image gallery renders, with `no-referrer`
  limiting third-party tracking); `nosniff` / `DENY` / `no-referrer` headers;
  request-body cap; distroless **nonroot** runtime image; session IDs from
  `crypto/rand`.
- **Scrape targets are constrained:** absolute `http`/`https` URLs only — other
  schemes are rejected before the browser renderer could follow them — and loopback,
  private and link-local hosts are refused, so search-derived or model-derived URLs
  cannot reach internal services or cloud metadata endpoints.
- **Untrusted HTML is never trusted:** HTML/XHTML content types only, 2 MiB body cap,
  charset-aware decoding, and a parse-then-sanitize pass before anything reaches the
  model.
- **Bot challenges are reported, not defeated** — the browser fallback renders a
  challenge page like any other and reports `blocked` if the challenge persists.

---

## 7. Configuration reference

All configuration comes from environment variables (`internal/config/config.go`):

| Variable | Default | Description |
| :--- | :--- | :--- |
| `PORT` | `8080` | HTTP listen port |
| `GEMINI_MODEL` | `gemini-2.5-flash-lite` | Gemini model name |
| `SERPER_API_KEY` | *(none)* | Serper.dev API key — required for search |
| `GOOGLE_API_KEY` | *(none)* | Gemini API (AI Studio) key |
| `GOOGLE_GENAI_USE_VERTEXAI` | unset | `true` switches to Vertex AI backend |
| `GOOGLE_CLOUD_PROJECT` / `GOOGLE_CLOUD_LOCATION` | *(none)* | Vertex AI project & region |
| `GCP_REGION` | `us-central1` | Region stored in config |
| `COOKIE_FILE_PATH` | *(none)* | Optional browser cookie export (JSON) |
| `CACHE_MAX_ENTRIES` | `2048` | Bounded-cache size (FIFO eviction) |
| `RESEARCH_TIMEOUT_SECONDS` | `180` | Per-research context timeout |
| `BROWSER_RESEARCH_TIMEOUT_SECONDS` | `300` | Per-research timeout when the run renders pages in a browser (clamped to ≥ `RESEARCH_TIMEOUT_SECONDS`) |
| `ENABLE_BROWSER_FALLBACK` | `false` | Default browser-rendering setting applied to every run |
| `ALLOW_BROWSER_TOGGLE` | `true` | Offer the per-run rendering toggle in the console (shown only when Chromium can start) |
| `MAX_SCRAPES_PER_RUN` | `10` | Scrape budget per research run |
| `MAX_SEARCH_RESULTS` | `12` | Results per Serper query |
| `MAX_AGENT_STEPS` | `10` | Agent/tool iteration budget per run |
| `HARD_URL_HANDLER` | `true` | Escalate blocked pages and professional profiles to the bounded hard-URL handler (ladder caps are code constants: 2 URLs/run, 30 s per URL, ≥45 s deadline remaining) |

Numeric values are parsed by `envInt`, which silently falls back to the default for
unset, invalid, or non-positive values.

---

## 8. HTTP API reference

| Method | Path | Description |
| :--- | :--- | :--- |
| `GET` | `/` | Research console (full HTML page from `index.html`) |
| `POST` | `/api/research` | Start research. Form fields: `mode` (`company`/`person`), `query`, `company_name`, `location`; company extras: `company_domain`, `company_industry`, `company_country`, `company_city`, `areas_of_interest`, `desired_output`, `time_scope`, `research_notes` (comma-separated lists allowed); `use_browser` (`1`/`0`) opts this run into browser rendering. Returns the `workflow_console` fragment, or the final result fragment when `?sync=1`. |
| `POST` | `/api/rerun` | Form fields: `session_id`, optional `use_browser`. Re-runs that session's retained brief in a **new** session — the console's "Re-run with browser rendering" button. Errors if the session has expired or is not a company session. |
| `POST` | `/api/question` | Form fields: `session_id`, `question` (≤ 500 chars). Answers from retained evidence, or runs a fresh focused search when the evidence is insufficient. |
| `GET` | `/api/events?session_id=...` | SSE stream: `log` frames (rendered `log_entry`), then `result` (final fragment), `status` (badge HTML), and `close`. |
| `GET` | `/healthz` | Liveness probe → `ok` |

Minimal CLI usage of the sync path (no UI):

```bash
curl -X POST "http://localhost:8080/api/research?sync=1" \
  -d "mode=company" -d "query=Acme Robotics"

# opt a single run into browser rendering
curl -X POST "http://localhost:8080/api/research?sync=1" \
  -d "mode=company" -d "query=Acme Robotics" -d "use_browser=1"
```

---

## 9. Testing

Run everything with `go test ./...`. The suite uses table-free, dependency-injected
unit tests — no network access required:

| Package | What's covered |
| :--- | :--- |
| `cmd/server` | SSE termination on a failed session; browser-choice policy (toggle disabled by the operator / Chromium unavailable / explicit opt-out / absent field); security headers incl. the CSP `img-src` allowance; re-run validation paths |
| `internal/agent` | Engine limit enforcement + default fallbacks (`mockSearcher`/`mockScraper`/`mockLLM`); negative scrape caching and render-mode cache keys; deterministic fallback query plan, ranking, QC loop termination, profile-candidate plumbing, QC budget guard |
| `internal/cache` | TTL expiry, FIFO eviction bounds |
| `internal/config` | Defaults, env-var overrides / invalid-value fallback, browser-research timeout selection and clamping |
| `internal/llm` | `cleanJSONString` fence/prose stripping; transient-vs-fatal error classification |
| `internal/models` | Tolerant unmarshalling (`headquarters`, `headcount_est`, string-or-object `follow_up_queries`) and `UnverifiedSourceCount` |
| `internal/tools` | Serper retry/response handling and the scraper's fetch policy, extraction and block detection against `httptest` servers — incl. charset decoding, non-HTTP/private-host refusal, per-request rendering override, idempotent `Close`, truncation flagging — plus cookie parsing & expiry |
| `internal/web` | Template parsing, HTML auto-escaping, disclosure-panel structure, capability-gated browser toggle, run-budget badge |

One test drives **real Playwright/Chromium** to prove that a clearance cookie earned by
passing a JavaScript interstitial is reused across renders. It is skipped unless
`PLAYWRIGHT_INTEGRATION=1` is set, so the default suite needs no browser:

```bash
PLAYWRIGHT_INTEGRATION=1 go test ./internal/tools/ -run TestBrowserRendererReusesInterstitialClearance
```

CI (`.github/workflows/go.yml`) additionally enforces `gofmt` cleanliness, `go vet`,
and a full build on every push to `main` and every pull request, using the Go
version pinned by `go.mod`.

---

## 10. Building, running & deploying

### Local development

```bash
go mod tidy            # generates go.sum (required before build/Docker)
go build ./...
go test ./...
go run ./cmd/server    # http://localhost:8080
```

On Windows, `scripts/dev.ps1` (also the VS Code task **Go: Watch Server**) watches
every `.go`/`.html` file under `cmd/` and `internal/`, computes a fingerprint every
750 ms, and restarts `go run ./cmd/server` on change — hot reload without extra
tooling.

### Docker (production shape)

```bash
go mod tidy
docker build -t websearch-go .                                  # HTTP-only, distroless
docker build -f Dockerfile.browser -t websearch-go-browser .    # includes Chromium
docker run --rm -p 8080:8080 \
  -e SERPER_API_KEY=... -e GOOGLE_API_KEY=... websearch-go
```

The `Dockerfile` is a two-stage build: `golang:1.23-alpine` compiles a static binary
(`CGO_ENABLED=0`, `-trimpath -ldflags="-w -s"`); the runtime stage is
`gcr.io/distroless/static-debian12:nonroot` — minimal attack surface, fast cold
start, and no shell/package manager in the image. Templates are embedded, so the
image contains only the single binary.

That image deliberately **cannot** render pages. `Dockerfile.browser` builds on
`mcr.microsoft.com/playwright:v1.62.1-noble` instead (browser version kept in step
with the `playwright-go` dependency) and is the one to deploy with
`ENABLE_BROWSER_FALLBACK=true` or the per-run toggle.

### Environment note for Windows dev

`.vscode/tasks.json` exposes the watcher; the built `websearch.exe` at the repo root
is a compiled artifact of `cmd/server`.

---

## 11. Files that are not part of the Go service

These files live in the repository but are **not** compiled into or required by the
server:

| File | Role |
| :--- | :--- |
| `react_engine.py` | A standalone **Python replica** of the Go ReAct engine (its docstring maps each function to the corresponding Go file). It is a CLI learning/demo companion — `python react_engine.py --task "..."` or `--demo` for a fully offline scripted run. Not part of the Go build. |
| `generate_pro_deck.py` | A `python-pptx` script that generated the bundled `LLM_Reasoning_Executive_Guide.pptx` / `.pdf` presentation about LLM reasoning. Documentation tooling, not runtime code. |
| `LLMreasoning.md`, `LLM_Reasoning_Executive_Guide.pdf`, `.pptx` | Executive-guide artifacts explaining the LLM reasoning concepts behind the agent design. |
| `websearch.exe` | A compiled Windows binary of `cmd/server` (build artifact; rebuild with `go build ./cmd/server`). |
| `README.md` | Usage quickstart: prerequisites, env vars, endpoints, Docker commands, plus the security-header and fetching-policy summaries. |
| `DESIGN.md` | The original high-level design (HLD), architecture diagram, and phased implementation plan that this codebase implements. |
| `HOW_IT_WORKS.md` | Narrative walkthrough of the request lifecycle and every module, aimed at a newcomer. |
| `.gitignore`, `.vscode/`, `.github/`, `scripts/` | Repo hygiene, editor task, CI pipeline, dev watcher. |

---

## 12. Project health & known issues

**Verified:**

- `go build ./...`, `go vet ./...` and `go test ./...` all pass across the eight
  packages, and `gofmt -l` reports no formatting issues (CI enforces the same gates).
- Code quality highlights worth keeping: interface-driven agent tools (mockable),
  deterministic fallbacks for every intermediate LLM call, bounded memory everywhere
  (cache, session hub, event channels, scrape sizes), and disciplined HTTP hardening
  (fetch-target policy, content-type/size caps, charset decoding, CSP).

**Known limitations (deliberate behaviour, not bugs):**

- **Professional-profile handling is policy-driven.** With the hard-URL handler enabled
  (the default), `linkedin.*` URLs get one bounded escalation per run â€” browser render,
  cookie-bridged render, extended settle â and fall back to snippets when the wall
  survives. With the handler disabled (`HARD_URL_HANDLER=0`) or Chromium unavailable,
  profiles are never scraped, which is when department-level results stay thin for a
  company whose functional leadership is visible only there.
- **The relevance ranker is permissive.** In practice it marks nearly every candidate
  relevant, so off-company profiles and off-domain pages still reach the scrape stage.
  Hard pre-filters (domain / company-name must match) would tighten this.
- **PDFs are rejected outright** with `unsupported content type`, which costs officer
  lists published as registry filings.
- **Browser rendering is off by default** for ordinary scrapes and requires a Playwright
  install plus the per-run toggle (or `ENABLE_BROWSER_FALLBACK=true`). The hard-URL
  handler uses the same renderer whenever Chromium can start, independent of that toggle.
- **Playwright's `chrome-headless-shell` crashes on some hosts**, so the renderer uses
  `channel: "chromium"` (the full browser) — correct, but heavier.
- **Plain `http:` images remain CSP-blocked**, so the gallery only shows `https:` ones.
- **Runs are long** (roughly 90 s–5 min). Rendered runs draw the larger budget, and the
  budget-aware QC pass returns partial results rather than failing when time runs out.

**Possible future improvements** (none blocking):

- The cache is process-local; an optional Redis/disk backend would survive restarts.
- `SerperClient` parses `knowledgeGraph` but never uses it — it could enrich company
  mode's evidence.
- `MAX_AGENT_STEPS` is enforced via budgets/bounds rather than a live ReAct
  loop counter; the domain agents are currently fixed pipelines rather than
  free-form ReAct loops (the naming in `DESIGN.md` reflects the project's
  conceptual model).
- `websearch.exe` at the repo root could be removed or gitignored to keep the
  repository clean.
- Negative scrape caching is time-based only; distinguishing "definitely gone" (404)
  from "try again" (timeout) would waste fewer retries.

---

*Generated as part of a full codebase review of WebSearch_GO — see also `README.md`
and `DESIGN.md`.*