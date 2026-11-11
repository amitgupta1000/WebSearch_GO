# WebSearch_GO — Architecture & Internals

This document explains how the project works end-to-end and the role played by every
module and file: the request lifecycle, the design decisions that hold it together, the
tests that pin that behaviour, and the known limitations. It is the companion to
[`README.md`](README.md), which covers prerequisites, quickstart, configuration,
endpoints and deployment.

> **In one sentence:** WebSearch_GO is a self-contained Go web server that runs
> autonomous, LLM-driven web research agents — building company org charts and person
> dossiers from live Google searches and web scraping — and streams every agent step to
> the browser in real time over Server-Sent Events.

## Contents

1. [The research modes](#the-research-modes)
2. [System architecture](#system-architecture)
3. [Component map](#component-map)
4. [Engineering principles at a glance](#engineering-principles-at-a-glance)
5. [Request lifecycle](#request-lifecycle)
6. [Architecture assessment](#architecture-assessment)
7. [Data flow (company mode)](#data-flow-company-mode)
8. [Module deep dive](#module-deep-dive)
9. [Repository layout & file roles](#repository-layout--file-roles)
10. [Cross-cutting mechanisms](#cross-cutting-mechanisms)
11. [Configuration](#configuration)
12. [HTTP API reference](#http-api-reference)
13. [Testing](#testing)
14. [Building & deploying](#building--deploying)
15. [Files that are not part of the Go service](#files-that-are-not-part-of-the-go-service)
16. [Project health & known issues](#project-health--known-issues)
17. [Design history & roadmap](#design-history--roadmap)

## The research modes

WebSearch_GO is an **agentic AI research server** written in idiomatic Go (module
`github.com/amitgupta1000/WebSearch_GO`, Go 1.24). It performs autonomous web research in
six modes:

| Mode | What it produces | How it works |
| :--- | :--- | :--- |
| **Company** (`mode=company`) | A structured `OrgChart`: overview, HQ, leadership, departments, locations, sources, and optional RR discovery provenance | LLM plans a set of Google queries → results are ranked for relevance → bounded RocketReach title-group searches nominate strictly employer-matched key persons → Google/registry entity filtering and Gemini validate/synthesize the org chart from the combined evidence |
| **Person** (`mode=person`) | A structured `PersonProfile` dossier: title, company, bio, experiences, education, skills, confidence score, and optional RocketReach contact enrichment | Automatic priority RocketReach pipeline first — direct lookup, and on a miss the free search endpoint disambiguates same-named profiles against the confirmed employer before one pinned lookup fetches the full record — then disambiguated search (name + company + location) → bio-oriented pages are scraped → a secondary career search supplements snippets → Gemini synthesizes/QCs the dossier |
| **Legal** (`mode=legal`) | A structured `LegalReport`: docketed litigation findings on the confirmed company and named persons, deterministic risk position, plus same-named hits kept for review | The company identity is confirmed as in company mode → bounded case-law and regulatory searches run → every hit is tied to the identity by CIN/name/alias → Gemini classifies category/status → deterministic dockets and risk position are assembled |
| **Related entities** (`mode=related_entity`) | A graph-first `RelatedEntityReport`: evidence-backed company and person nodes connected by labelled relationships, with raw snippets retained for review and optional RocketReach discovery provenance | Separate company search (32-query budget) from people discovery (8-query budget) → run up to four bounded RocketReach title-group searches when configured → merge strictly employer-matched candidates → launch a six-query identity/affiliation trajectory for each resolved leader, concurrently up to eight people → resolve DIN, appointment, domain, and registry signals → search other-company involvement; Gemini labels bounded evidence slices, but the report preserves the graph and raw evidence rather than depending on one monolithic extraction. Extraction is citation-checked: every claim must carry a hit URL, an uncited claim is grounded from the run's own hits (a hit naming both the claim and the primary company wins) or dropped when no hit mentions it, and evidence tolerant decoding accepts object arrays, plain strings or missing evidence |
| **Market landscape** (`mode=market_landscape`) | A verified producer/seller landscape with evidence and optional contact discovery | Bounded activity/geography lanes → discovery leads → company extraction and verification → deterministic confidence/status assembly |
| **Buyer landscape** (`mode=demand_chain`) | A verified demand-chain report identifying public consumers and buying signals | Two-step value-chain inference (direct consumers of the product as an input, plus one derived step) → search operator and demand lanes → scrape and verify buyer evidence with claim-conditioned search ordering, a producer gate (supply-side self-descriptions earn no confidence) and aggregator/publisher identity gates → deterministic assembly with stage attribution and buyer-fit distance |
| **News brief** (`mode=news`) | A current-news article, summary, chronology, conflict register, citations, and structured sentiment | Search configured whitelist hosts → bypass search cache for freshness → scrape and date articles from search metadata/JSON-LD → keep only verifiable articles from the last 24 hours → chunk and label evidence → Gemini structured synthesis with temporal conflict handling |

The system is built from six cooperating layers:

- **HTTP layer** (`cmd/server`, `internal/web`) — an HTMX-powered single-page console,
  with templates embedded into the binary via `go:embed`.
- **Agentic orchestration** (`internal/agent`) — a shared *Engine* plus domain
  specialists (company / person / legal / related entity / market / demand / news /
  deep search) that run bounded research pipelines. The modes share acquisition and
  evidence plumbing, but retain domain-specific plans, schemas and quality rules.
- **Tooling** (`internal/tools`) — Serper.dev Google-search client, a sanitized HTML
  scraper with an optional Playwright renderer and PDF text extraction,
  browser-cookie import, and the IndianKanoon case-law client.
- **LLM layer** (`internal/llm`) — a Gemini client (`gemini-2.5-flash-lite` by default)
  that returns *structured JSON* with cleaning + retry.
- **State & caching** (`internal/cache`, `internal/agent/hub.go`) — a bounded TTL cache
  for searches/scrapes and a thread-safe session hub for live event streams.
- **Shared vocabulary** (`internal/models`) — all structs that cross package boundaries.

The binary is fully self-contained (templates embedded), runs on Cloud Run-style
environments (distroless non-root Docker image, graceful SIGTERM shutdown, `/healthz`
probe), and enforces strict budgets on cost and runtime (max scrapes, max search
results, max agent steps, per-request timeout).

## System architecture

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

## Architecture assessment

### Verdict

The structure is robust for the current workload. The strongest decisions are the
dependency-inverted `Engine`, the typed models at package boundaries, bounded budgets,
deterministic fallbacks, explicit evidence provenance, and domain-owned final schemas.
Those choices make a failed search, scraper, model call or browser render degrade to a
reported partial result instead of taking down the process or silently inventing data.

The main scaling risk is not the HTTP or tool layer; it is workflow duplication inside
`internal/agent`. Company, person, legal, related-entity, market, demand-chain, news and
deep-search modes each implement variations of query planning, bounded search execution,
URL/result deduplication, scraping, evidence assembly, source tracing, identity checks and
review. The duplication is currently understandable because the modes were developed from
different evidence problems, but it will make policy fixes drift between modes as more
research models are added.

### What should become reusable

The right abstraction is a set of small, synchronous Go services or pipeline stages, not
an independent LLM agent for every recurring task. A generic LLM agent would make budgets,
provenance and failure semantics harder to reason about. The following boundaries are
worth extracting behind interfaces while keeping the existing domain agents as
orchestrators:

| Reusable boundary | Shared responsibility | Domain-owned responsibility |
| :--- | :--- | :--- |
| `IdentityResolver` / `IdentityMatcher` | Compile confirmed names, aliases, domains and registration IDs; classify source/entity matches; retain excluded material | Company, person and legal acceptance rules, including legal same-named retention and person employer rules |
| `SearchExecutor` | Run planned searches with cache, concurrency, cancellation, result normalization, URL dedupe and `SearchTrace` | Query vocabulary, source choice, pagination and whether a failed source is fatal |
| `EvidenceCollector` | Select candidates, fetch pages through the shared scraper, retain snippet fallback, dedupe content, and emit `ScrapeReport` | Candidate priority, profile/hard-URL policy, evidence type and the minimum evidence needed for synthesis |
| `Provenance` helpers | Normalize canonical URLs, attach source/date/quality metadata, and enforce that output citations came from collected evidence | The meaning of a citation and the evidence threshold for each output field |
| `ReviewPolicy` | Bounded review attempts, remaining-budget checks, unavailable/error status and follow-up accounting | Review prompt, verdict schema, deterministic fallback and whether revision is safe for the domain |
| `RunContext` | Shared deadline, phase timings, budget reservations, event emission and trace identity | Mode-specific counters and output assembly |

These should be introduced incrementally. `Engine.SearchCached`,
`ScrapeCachedBatch`, `Emit`, the phase timer and the existing trace/report models are
already the beginnings of these services; extraction should move their policy-neutral
parts out of `company.go` rather than create a second framework beside `Engine`.

### What should remain domain-specific

Do not generalize the final synthesis or classification layer yet. An org chart, a person
dossier, a legal docket, a market landscape, a demand chain, a news chronology and a deep
research report have different evidence semantics and failure policies. In particular:

- Legal matching must preserve ambiguous findings and derive risk deterministically.
- Person research needs employer/location disambiguation, same-named grouping and timeline QC.
- Company research needs entity gating, freshness validation and bounded revision loops.
- Market and demand modes need lane-specific discovery and deterministic company merging.
- News needs freshness-first acquisition and citation/date constraints.

Those are agent policies, not shared mechanics. Keeping them local prevents a generic
pipeline from weakening the strongest safety rule of one mode to accommodate another.

### Recommended target shape

Keep `CompanyAgent`, `PersonAgent`, `LegalAgent`, and the other domain agents as thin
orchestrators over a shared run context:

```text
domain request
  -> domain planner
  -> shared SearchExecutor
  -> domain candidate policy
  -> shared EvidenceCollector
  -> domain synthesis/classification
  -> shared ReviewPolicy or validation helpers
  -> domain report/export
```

The shared stages should return typed outcomes containing successes, per-item failures,
budgets spent, trace entries and provenance. They must never hide an unavailable source or
turn a cache/tool failure into an empty successful result. This preserves the current
honesty contract while making cross-mode behaviour testable once.

### Migration order and guardrails

1. Extract `RunContext` and `SearchExecutor` from the company and person paths first;
  these have the clearest overlap and already use compatible `Searcher`/cache contracts.
2. Extract evidence/citation normalization next, starting with canonical URL and content
  dedupe. Keep hard-URL, profile and legal-document ladders as injected policies.
3. Move identity matching behind a small interface only after company, person and legal
  tests cover their intentionally different verdicts. Share matching primitives, not one
  universal gate.
4. Add a reusable review/validation budget helper after the stage contracts are stable.
  Its first job is accounting and status reporting, not prompt generation.
5. Migrate news, market, demand-chain, related-entity and deep-search incrementally, one
  mode at a time, with golden trace/report fixtures and race-detector coverage.

Every extraction should preserve the current public agent entry points, event ordering where
the UI depends on it, cache-key semantics, deterministic result ordering, and explicit
partial/unavailable states. A small contract test suite for search traces, scrape reports,
identity exclusions, cancellation and budget exhaustion is more valuable than a broad
generic agent abstraction.

### Assessment summary

The architecture is sound and can scale further without a rewrite. The next optimization
is consolidation of recurring mechanics into injectable stages, not adding more autonomous
agents. The current domain agents should remain responsible for the questions they ask,
the evidence they trust and the shape of the answer; shared services should make the
mechanics consistent, bounded and observable.

---

## Component map

| Module / Package | Path | Functional Contribution |
| :--- | :--- | :--- |
| **HTTP API Entry** | `cmd/server/main.go` | Loads configuration, initializes dependencies with fail-fast checks, routes company/person research modes, serves the HTMX UI from embedded templates (`internal/web`), renders auto-escaped HTML fragments, probes Chromium once for the per-run rendering toggle, serves `POST /api/research`, `POST /api/rerun`, `POST /api/question` and the `/api/events` SSE stream, and exposes `/healthz` with graceful shutdown. |
| **Shared Agent Engine** | `internal/agent/engine.go` | Provides dependency-inverted search, scrape, cache, structured-LLM, event, budget and synthesis plumbing used by the bounded domain pipelines. The domain agents decide the workflow; this package does not run a free-form ReAct loop. |
| **Company Agent** | `internal/agent/company.go` | Orchestrates multi-phase company discovery (domain search, leadership page extraction, executive hierarchy building, and departmental/regional drill-down formatting). |
| **Person Agent** | `internal/agent/person.go` | Accepts optional company/location disambiguation constraints, queries career footprints across public sources, and aggregates data into an end-to-end dossier. |
| **Search Tool** | `internal/tools/serper.go` | Executes structured web queries via Serper API (regular, news, and site-restricted queries) and returns sanitized snippets and links. |
| **Scraper Tool** | `internal/tools/scraper.go` | Enforces the fetch policy (http/https only; loopback, private and link-local hosts refused), fetches webpage bodies with content-type and size guards, decodes the declared charset, retries transient failures, detects bot-challenge interstitials, strips HTML boilerplate (scripts, styles, ads), deduplicates nested content blocks, and extracts rune-safe truncated text for LLM ingestion. |
| **Browser Renderer** | `internal/tools/scraper_browser.go` | Optional bounded Playwright fallback for HTTP errors, bot challenges and JavaScript shells: launches the full Chromium build, keeps one shared browser context so interstitial clearance cookies survive across renders, waits for network idle, and caps concurrency with a semaphore. Enabled per process or per run; challenges are reported, never solved. |
| **PDF Extraction** | `internal/tools/pdf.go`, `internal/llm/gemini.go` | Reads PDFs instead of rejecting them — officer lists and filings are often published only as PDFs. The scraper hands a PDF body to an injected `DocumentTextExtractor`: Gemini reads it inline (handling compressed streams, subsetted fonts and scans), with a local `ledongthuc/pdf` parser as the fallback. That parser is panic-guarded, capped at 200 pages, and its output must clear a 60 % printable-ratio gate before it becomes evidence; provenance is recorded as `pdf-gemini` / `pdf-local`. |
| **Cache Store** | `internal/cache/` | A context-aware `Store` surface with three backends — Redis (default), Cloud Storage through the JSON API + Application Default Credentials, and in-process memory. Search results (48 h), scraped pages (7 days) and negative scrape results (15 min) all flow through it, so the shared default survives restarts; cached pages are revalidated with conditional requests. |
| **Phase Timings** | `internal/agent/timing.go` | A per-phase stopwatch over plan/search/rank/scrape/synthesize/review. The result is attached to the chart, emitted as a `timing` event, and shown in the console, so "the scraper is slow" becomes a measured claim. |
| **Output Exporter** | `internal/export/` | Turns a finished run into durable files: a Markdown document rendered from the research models, and a PDF printed from a self-contained HTML document by the Chromium the scraper already runs (no PDF library). Both are written to Cloud Storage or a local directory, one artifact per attempt, so a failed PDF never hides the Markdown. |
| **Cloud Storage Client** | `internal/gcs/` | A thin JSON API client (upload, download, delete) shared by the cache backend and the output exporter. It uses Application Default Credentials, avoiding the gRPC/protobuf dependency tree the official library would add. |
| **Gemini Client** | `internal/llm/gemini.go` | Wraps the Google GenAI SDK to interface with `gemini-2.5-flash-lite`, configuring system instructions, tool declarations, token budgets, and structured JSON decoding. |
| **Embedded UI Templates** | `internal/web/` | `go:embed`'ed HTML templates (index page + HTMX result fragments) rendered with `html/template` auto-escaping. |
| **Data Models** | `internal/models/` | Defines Go structs for `OrgChart`, `Department`, `Employee`, `PersonProfile`, `Experience`, `Education`, and agent event payloads. |

---

## Engineering principles at a glance

- **Graceful degradation everywhere**: LLM planning and ranking have deterministic fallbacks;
  search and scraping retry transient failures; a failed QC review is reported as
  `unavailable` rather than quietly counting as a success.
- **Bounded resources**: cache entries, scrapes per run, search results, agent steps, request
  body size, scrape body/text size, and session lifetime are all explicitly capped — the
  server can't be runaway-by-design.
- **Concurrency discipline**: mutex-guarded sessions, `sync.Once` completion, buffered
  non-blocking event channels, background goroutines with timeouts and cleanup.
- **Security posture**: body caps, security headers + CSP, `nonroot` distroless container,
  auto-escaped templates, and hallucination guards (URLs filtered against real results).
- **Everything embedded**: templates ship inside the binary, so deployment is one static
  executable per OS/arch.

## Request lifecycle

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
4. **The agent runs its pipeline** (details in [Module deep dive](#module-deep-dive)). As it
   works, it emits
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

## Data flow (company mode)

```
CompanyBrief (form)
   │
   ▼
planQueries ──────────► QueryPlan (≤24 queries)         [Gemini]
   │
   ▼
searchPlan ───────────► []SearchResult (deduped)        [Serper + cache]
   │
   ▼
rankResults ──────────► []RankedResult (scored)         [Gemini, or heuristics]
   │
   ▼
scrapeRanked ─────────► []Evidence (≤10 pages)          [Scraper + cache]
   │
   ▼
synthesizeChart ──────► OrgChart                        [Gemini]
   │
   ▼
reviewChart ──────────► QualityReview (pass/revise)     [Gemini]
   │
   ├── revise? ──► loop back to searchPlan (≤3 iterations, ≥60 s budget left)
   ▼
chart + CompanyResearchContext ──► SSE result ──► HTMX UI
                                  └─► retained for /api/question
```

Person mode runs a bounded identity-aware plan: professional and regional/global directory
searches feed employment evidence, while a separate X/Twitter search
feed optional public personalization context and deterministic public-account candidates →
scrape top bio pages → synthesis/QC → `PersonProfile`.

Both modes finish by saving their result: `Engine.ExportCompany` / `ExportPerson` write a
Markdown document and a PDF (printed by the same Chromium) to the configured destination, and
the artifacts are attached to the chart or profile so the console can list them. Exports run
on a detached context, so they succeed even when the research budget is exhausted.

---

## Module deep dive

### `cmd/server/main.go` — HTTP gateway & composition root

The only `package main` in the repo. Responsibilities:

**Startup sequence (`main`)** — builds the object graph in dependency order:

1. `config.Load()` — read env vars; warn (but don't exit) if `SERPER_API_KEY` is missing.
2. `tools.LoadCookieJarFromFile(cfg.CookieFile)` — optional browser cookies; warn on failure.
3. `llm.NewGeminiClient(ctx, cfg.GeminiModel)` — **fail-fast**: `log.Fatalf` if Gemini
   credentials are missing, since no research can run without it. It is built before the
   scraper because the scraper borrows it to read PDFs.
4. `cache.NewMemoryCache(cfg.CacheMaxEntries)` — bounded TTL cache (`defer Close()`).
5. `tools.NewScraperWithBrowser(cookieJar, 15s, cfg.EnableBrowserFallback,
   tools.WithDocumentTextExtractor(documentTextExtractor(gemini)))` and
   `tools.NewSerperClient(cfg.SerperAPIKey)`. The extractor closure is the whole PDF
   policy: Gemini first, local parser on failure.
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
Tailwind/HTMX CDNs plus remote images (`https:` and `http:`) so the scraped-image
gallery can render). The `http.Server` sets `ReadHeaderTimeout` (10s), `ReadTimeout` (30s),
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

### `internal/agent` — the agentic core

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
3. **Rank (`rankResults`)** — BM25 scores every result, source signals adjust the
   scores, and one LLM call reranks the top candidates (falling back to
   **`deterministicRank`**: a keyword heuristic — base 0.35, +0.30 if the company
   name matches, +0.05 per matching interest term or leadership/department/location
   keyword, +0.20 if the official domain matches, relevant when score ≥ 0.45).
   `normalizeRankedResults` then keeps only URLs that actually appeared in the
   search input and are marked relevant, sorted by score.
   Finally **`pruneUnconnected`** drops candidates with no connection to the target
   company: no official-domain match and no distinctive company-name token
   (`companyTokens`, which ignores corporate/generic words like "Limited" or "Group")
   in the title, snippet or URL. BM25 marks anything sharing the query's tokens
   relevant, so without this every result the search engine returned — including
   other companies' profiles — reached the scrape stage. The unfiltered list is
   returned when filtering would empty it, so an unusual name cannot starve a run.
4. **Triage and scrape (`scrapeRankedWithBrief`)** — PDF-looking candidates first pass
   through a two-stage conservative Gemini document-triage gate: fast metadata triage from
   search signals, followed by a bounded preview fetch (512 KiB / 3 pages) for uncertain
   candidates. Only a `relevant` result with confidence ≥ 0.80 and matching entity/topic
   signals is allowed to reach full scraping; failed, not relevant or persistently uncertain
   triage is fail-closed and recorded in `ScrapeReportEntry`. Non-document candidates retain
   the normal path. Approved candidates are then fetched in bounded parallel batches
   (`SCRAPE_CONCURRENCY`) through `Engine.ScrapeCachedBatch`, validating each URL with
   `url.ParseRequestURI` (absolute URL with scheme+host required), and keeps at most
   `MaxScrapes` pages. Pages whose extracted text is ≤ 80 bytes are discarded as noise,
   text already collected from another URL is dropped, and survivors become
   `models.Evidence` (URL, title, content, score, evidence type) plus the `sources` list.
   *(Steps 8+: "Scraping selected relevant source…")*
   - **Professional profiles are policy-driven.** A `linkedin.*` host is classified
     `professional_profile`. When the hard-URL handler is available it gets one bounded
     escalation attempt, and its outcome is recorded on the scrape report
     (`HandlerStatus: recovered | failed | deferred | unavailable`); without a handler the
     policy is enforced in two places — the snippet-evidence loop in `scrapeRanked` and
     `selectScrapeCandidates` via `excludedScrapeCandidate` — so only the public search
     snippet is kept. Setting `ALLOW_PROFILE_SCRAPING=true`
     (`agent.WithProfileScraping`) also sends profiles through the normal scrape path.
     Department-level results for a company whose functional leadership is visible only on
     such profiles depend on these settings.
   - **WeeDoo registry pages are fetched first, as a bounded priority source**
     (`company_registry`, up to three pages): an Indian company's directory page lists directors
     with DINs, addresses and group links that its Google snippet truncates — the enrichment the
     related-entity segment already scrapes by hand. Because the search provider barely indexes
    WeeDoo, the pass also **constructs** the directory URL from the company name and its CIN
    (the operator's registration id, or a CIN a registry snippet published) and verifies the
    fetched page names the company before admitting it. The pass is entity-gated (a directory page
     about another company is excluded like any other source) and spends the ordinary scrape
     budget, on the same deadline that protects the lookup passes. Registry candidates also
     sort above general `leadership` pages in the scrape ladder, so a page the enrichment pass
     could not fetch still gets the next budget slot.
   - **Blocked pages get the same bounded escalation** before falling back to snippet
     evidence, so a challenge interstitial is never mistaken for page content.
   - Every attempt (success, failure, skip) lands in `chart.ScrapeReport`, which the
     console renders as the collapsible Scraping Report and summarises through
     `OrgChart.UnverifiedSourceCount`.
5. **Synthesize (`synthesizeChart`)** — a strict "corporate intelligence analyst"
   system prompt tells Gemini to use **only** the supplied evidence (no invented
   employees or reporting lines) and emit an `OrgChart` JSON with an explicit
   `departments[].members` schema. Evidence originating from `professional_profile`
   snippets is re-presented as a labelled **profile candidates** block, so a headline
   that pairs a name with a function and a company is not lost among the page text.
   The roster's scope is evidence, not the brief's Areas: every person the supplied
   evidence explicitly names in a role at the company is placed in the department their
   title implies, with the naming page as their public source — Areas drive which
   functions get dedicated searches, they do not suppress people the evidence already
   names.
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
7. **Validate (`validateChart`)** — the freshness pass described under
   [Evidence freshness & validation](#evidence-freshness--validation). It runs on every
   return path (success, budget exhaustion, and a partial chart) just before the
   artifacts are written, so the labels and the reverse lookups are part of the saved
   report. Its own budget check means an exhausted run still gets the offline half.

`formatEvidence` renders the scraped pages as `--- Evidence N: title (url), score, as
of YYYY-MM-DD ---` blocks for the synthesis prompt, and `formatProfileCandidates`
renders the snippet-sourced candidates.

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

#### `legal.go` — LegalAgent: identity → case-law search → classify → report

`ResearchLegal` runs the legal mode pipeline, deliberately smaller than the other
two modes (no planning model call, no scrape ladder, no QC loop):

1. **Plan (`planLegalQueries`)** — deterministic, strongest first, capped at
   `LEGAL_QUERIES_PER_RUN`: the company name, the CIN (a registration id cannot
   belong to a same-named company), confirmed aliases, a Casemine snippet track
   (`site:casemine.com "Company"` through `SearchCached`), then each named person
   as `"Name" "Company"` co-occurrence. Juristway receives a reserved slot when
   enabled so aliases and person searches cannot starve that complementary
   source. *(Step 40)*
2. **Search (`executeLegalQuery`)** — IndianKanoon searches go through the native
   client (`legal_search:<sha256(query)>`, TTL 48 h) and always carry
   `doctype=judgments`; statute sections land in the trace's excluded list rather
   than vanishing. Casemine goes through its native client when one is configured —
   its own fetch ladder, measured: the site 403s every non-browser User-Agent
   (headless Chromium's included), but its server-rendered results page answers the
   scraper's Chrome UA, so `Scraper.FetchHTML` (plain, jar session) runs first and
   `Scraper.RenderHTML` (cookie-bridged shared-browser render) is the wall
   fallback. Without the client the track falls back to search-engine snippets.
   *(Steps 41–42)*
3. **Classify (`legalFindingFromHit`)** — every judgment is tied to the confirmed
   identity by the same `entityMatcher` keys a company run is held to: CIN in the
   record (`cin`, decisive), the full legal name (`exact-name`), a confirmed alias
   (`alias`), and a person's name in a person-and-company search (`exact-name`).
   Anything weaker is the **same-named bucket**: kept and labelled
   `matched by name only`, never silently dropped — a legal check's risk profile
   inverts the org chart's, so a false positive must be visible and a false
   negative must not happen. The case title's `X v. Y` structure is parsed
   deterministically into the party's side (`legalPartyRole`): the left side is
   `initiated` (petitioner/complainant/appellant), the right side `facing`
   (respondent/defendant/accused), a match outside the sides is `mentioned` — the
   distinction that changes a risk reading, and it survives model outages. A
   Gemini pass then adds category and status keyed by index;
   on failure the findings stay deterministic. *(Step 44)*
4. **Fold iterations (`foldCaseIterations`)** — records are grouped by case key
   (the title with its trailing `on <date>` stripped, so the trial order and the
   appeal judgment fold together) and the **latest dated record wins the row**:
   the matter's current state. The group's identity class upgrades to its
   strongest member, fields are backfilled, and the survivor reports
   `Iterations` so the artifact shows how many stages folded. *(Step 43 pre-
   classification; runs before the confirm pass so documents confirm only live
   rows)*
5. **Confirm (`confirmWithDocuments`)** — up to `LEGAL_DOCS_PER_RUN` IndianKanoon
   documents are fetched in full (TTL 7 d), weakest identity first; a CIN or full
   name in a judgment body upgrades a name-only match and re-grades it out of the
   same-named bucket.
6. **Group and assess risk (`buildDockets`, `buildRiskPosition`)** — classified
   findings are grouped deterministically into company-law, criminal, tax and
   other dockets. Only recent company-law matters where the company is clearly
   responding contribute to the risk level; the dockets retain all matched
   findings and expose pending/respondent counts. No model narrative can
   override this evidence-based position. *(Step 45)*
7. **Report (`assembleLegalReport`)** — dockets, matched findings, same-named hits,
   per-person results in request order (an unbudgeted person is named as
   *not searched* rather than silently missing), and per-source honesty
   (`LegalSourceReport.Unavailable`), so "no findings" is never ambiguous between
   "none exist" and "we could not look". Exports run on a detached context like
   the other modes. *(Steps 45–46)*

#### Bounded concurrency (`runBoundedLegal`)

The two search tracks and the document-confirm pass run through one bounded
worker pool, mirroring the engine's `ScrapeCachedBatch` shape:

- **The pool** (`runBoundedLegal`, `LEGAL_CONCURRENCY`, default 4): workers take
  a semaphore, and a panic in a worker fails that one unit — not the run or the
  process — because these goroutines sit outside net/http's per-request
  recovery, exactly as the scrape batch's do.
- **What parallelism buys here** is honest about the limiters: IndianKanoon's
  client allows one request a second and Casemine's the same, so parallel
  workers on one source queue at its limiter; the win is *cross-source*
  (an IndianKanoon wait overlapping a Casemine fetch) and overlapping response
  handling with the next request's wait.
- **Determinism under concurrency:** workers write outcomes into slices indexed
  by plan position, and the fold into the findings buckets and the search trace
  happens serially in plan order after the pool drains — so the report and the
  report appendix are identical on every run however the workers
  interleaved. Live `tool_call`/`tool_result` events do interleave; each is
  self-describing.
- **Budget under concurrency:** the shared query allowance is a mutex-guarded
  `legalBudget` — continuation pages and person-search slots are claimed
  atomically, first claim wins, and a person worker that cannot claim is
  recorded as *not searched* rather than racing the count.
- The race detector (`go test -race ./internal/agent`) is part of the story: it
  is what caught the test doubles' call logs and the company-mode mock searcher
  when the pools landed.

#### Directors discovery (`discoverDirectors`, toggle-driven)

With the form's directors toggle on, a discovery pass runs between the company
track and the person track:

1. It spends up to `legalDiscoveryQueries = 2` of the remaining query budget on
   registry snippets — a generic `"Company" directors list` and a
   `site:zaubacorp.com` query through the engine's cached search, recorded in the
   trace under their own purpose.
2. **The company-linked gate:** a snippet can nominate a person only when the
   entity matcher ties it to the confirmed company, so an unrelated company's
   board never enters the run. Excluded snippets sit in the trace's excluded
   list.
3. `extractDirectors` reads person names out of the company-linked snippets
   through one model call, row-keyed so the source host travels
   deterministically; the names then pass the deterministic gates (multi-token,
   not the company itself, deduplicated, capped at
   `legalMaxDiscoveredDirectors = 4`, operator-named persons skipped) before any
   search is spent on one.
4. Discovered persons join the person track as `"Name" "Company"` searches within
   the remaining budget; their `LegalPersonResult.DiscoveredVia` records how they
   entered the run, and the pass reports its own queries/yield as the
   `directors discovery` source — which appears in the report only when the
   toggle was on.

### `internal/tools` — external-world adapters

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
- **Safety guards:** HTML/XHTML `Content-Type`s are parsed as markup, `application/pdf`
  is extracted as text through the injected document extractor (Gemini inline PDF, with a
  local `ledongthuc/pdf` fallback), and images and every other binary type are rejected
  with `unsupported content type`. The body is read through a **2 MiB**
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

#### `pdf.go` — PDF text extraction

Officer lists and registry filings are often published only as PDFs, so a
`DocumentTextExtractor` can be injected into the scraper (`WithDocumentTextExtractor`);
`cmd/server` wires it to Gemini, with the local parser in `pdf.go` as its fallback:

- **Content-type policy.** `IsPDFContentType` accepts `application/pdf` and
  `application/x-pdf`. A generic binary type (`application/octet-stream`, `binary/…`) is
  accepted only when the body carries a `%PDF-` header within its first 1024 bytes — the
  format's own rule — and a declared PDF type is never parsed as HTML.
- **Local extraction** (`ExtractPDFText`) walks up to 200 pages with `ledongthuc/pdf`,
  skipping single unreadable pages instead of discarding the document, behind a
  `recover()` because the library can panic on malformed input (a truncated file must
  produce an error, never take down the request goroutine).
- **Output gate.** Text is normalised, capped at the same 40 KB as HTML content (with
  `ContentTruncated` set when the cap bites), and rejected when it is empty or below
  `minPrintableRatio` — 60 % printable runes — so a broken font mapping or a mislabeled
  binary payload never reaches the model as evidence.
- **Provenance.** `ScrapeResult.Extractor` is `pdf-gemini` or `pdf-local` (shown in the
  scrape report) and `Title` is the document's file name.

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
  Three policies now shape the work:
  - **Shape ordering** (`hardshape.go`). The agent offers profile candidates in
    `HardURLShape.EscalationPriority()` order — post, company page, other, person
    profile, `/pub/dir/` — so the small `HARD_URLS_PER_RUN` cap is spent on shapes that
    render on the first step.
  - **Auth-wall skip.** With an empty jar for the host, a person profile or directory
    page (`.AuthWalled()`) runs `plain-render` only: time cannot clear an authwall, so
    the 20 s extended settle is kept for the synthesis that follows. The step list in the
    failure message shows the skip, and `noCookieHint` adds why the cookie step was missing.
  - **Cookie widening.** `LoadCookieJarFromFileDetailed` registers a host-scoped cookie
    for its registrable parent as well, because a session exported for `www.linkedin.com`
    is otherwise absent on `in.linkedin.com` where the profiles are served — the reason
    the cookie step never fired in practice. Widening stops at the registrable domain
    (`www.example.co.uk` → `example.co.uk`, never `co.uk`).

#### `cookies.go` — browser session import

`LoadCookieJarFromFile` populates a standard `http.CookieJar` from either shape a session is
exported in, decided by the first byte of the file:

- **A bare array** — Cookie-Editor / EditThisCookie.
- **Playwright's `storage_state.json`** — `{"cookies": […], "origins": […]}`. Only the cookies
  are imported; `origins`/localStorage has no counterpart in an HTTP client.
- **`expires` in either shape** — a unix timestamp (Playwright writes a number, EditThisCookie
  `expirationDate`) or an RFC1123 string. `RawCookie.UnmarshalJSON` folds a number into
  `ExpirationDate` and ignores an expiry it cannot read, because a decoder error would fail
  the whole file and empty the jar over one field. An object without a `cookies` array is
  refused with that wording rather than silently yielding nothing.

Also:

- Skips empty and already-expired cookies (understands both unix
  `expirationDate` and `expires` date strings, with an RFC1123 fallback for
  non-GMT zone abbreviations).
- Strips the leading `.` from domains (`.example.com` → `example.com`) so cookies
  cover subdomains, groups them per domain, and installs them under
  `https://<domain>`. Reports how many domains could not be parsed.
- **Widens a host-scoped cookie to its registrable parent** (a second registration,
  never a move: `www.linkedin.com` also registers `linkedin.com`), unless the parent
  already carries a cookie of the *same name*. The rule is per name, not per domain: a
  whole-domain skip dropped a session cookie scoped to `www.linkedin.com` whenever an
  unrelated cookie happened to be scoped to `linkedin.com`, and the profile host is
  `in.linkedin.com`, which only ever sees the parent. Child domains are visited in sorted
  order, so a name declared by two children widens deterministically. Widening stops at the
  registrable domain, so `www.example.co.uk` widens to `example.co.uk` and never to `co.uk`.
- `LoadCookieJarFromFileDetailed` returns a `CookieJarReport` (count, skipped, widened,
  per-domain counts) that startup logs, so "the session never loaded" and "the site has
  no cookies" are distinguishable — they are indistinguishable from a ladder failure.
- With no path configured it returns an empty (but valid) jar, and startup names the export
  formats and the capture commands instead of only stating the omission.

### Scraper performance & politeness

*A deep, standalone walkthrough of the fetch path — policy, hardening, the rendering
ladder and extraction — lives in [SCRAPER_WALKTHROUGH.md](SCRAPER_WALKTHROUGH.md).*

Four mechanisms keep the scrape phase fast without hammering anyone:

- **Batched parallelism.** `Engine.ScrapeCachedBatch` fetches candidates in batches bounded
  by `SCRAPE_CONCURRENCY` (default 4) and returns outcomes in request order, so roughly
  10 × 1.5 s of serial waiting becomes about a quarter of that. The batch is sized to the
  remaining scrape budget plus one worker's worth of slack, and `scrapeRanked` still applies
  its per-item rules in rank order afterwards: a page fetched beyond the budget is cached
  for the next QC iteration or follow-up question rather than wasted.
- **Per-host limits.** `hostlimit.go` gives every host its own token bucket
  (`SCRAPE_HOST_RATE_PER_SEC` / `SCRAPE_HOST_BURST`) and concurrency cap
  (`SCRAPE_HOST_CONCURRENCY`, default 2), keyed on hostname, with idle-host pruning so a run
  that touches thousands of hosts cannot grow the map without limit. A host slot is held
  across the whole retry sequence, so three retries count as one polite request, and waiting
  is context-aware: a cancelled run stops queueing immediately.
- **Response-driven backoff.** A `429`/`503` carrying `Retry-After` (seconds *or* an HTTP
  date) delays the retry by exactly that long, capped by `SCRAPE_MAX_RETRY_AFTER_SECONDS`; a
  malformed or past value falls back to the normal linear backoff. That turns a rate limit
  into one short pause instead of three wasted attempts.
- **Conditional requests.** `tools.WithConditionalRequest` makes the next fetch conditional;
  a `304` comes back as a result flagged `NotModified` with no body, which
  `Engine.revalidateScrape` uses to restamp a cached page for the cost of one round trip.

Renders are cheaper in three ways: `404`/`410` skip the browser entirely (a render cannot
recover an absent page), the shared browser context aborts `image`, `media`, `font` and
`stylesheet` requests (the render only reads DOM text), and a page that already holds
`renderReadyRunes` of visible text at `DOMContentLoaded` skips both the network-idle wait
and the settle pause.

### `internal/llm/gemini.go` — Gemini client

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

### `internal/tools/indiankanoon.go` — the case-law client

The IndianKanoon client (`indiankanoon.org`, India's free case-law index) is the
native source of legal mode. Its shape was pinned against the live site, not
guessed, and the integration test (`LEGAL_INTEGRATION=1`) re-checks the live
markup:

- **Search** — `GET /search/?formInput=<query>&doctype=judgments`; the
  `doctype=judgments` filter is the statute guard, because a bare party search
  otherwise answers with IPC and Income-Tax-Act sections before any case. Query
  operators (`sortby:mostrecent`, `year:<yyyy>`, `cites:`/`citedby:`) pass through
  unmodified, so the agent composes them.
- **Parse** — `<article class="result">` items: the title link carries a
  `/docfragment/<id>/` (judgment) or `/doc/<id>/` (statute) id; the headline
  snippet often carries the judgment's structured `PETITIONER:` / `RESPONDENT:` /
  `CASE NO.:` / `DATE OF JUDGMENT:` block, which is read into
  `LegalSearchHit` and is what makes party matching stronger than title text;
  `<span class="docsource">` names the court; `Cited by N` tags are read as a
  weak relevance signal.
- **Documents** — a `/docfragment/` URL is normalised to `/doc/<id>/`; the
  judgment body lives in `div.judgments`, with `doc_title` / `docsource_main`
  for the title and court. Bodies are capped at 3 MiB.
- **Politeness** — one request per second, burst one, one in flight (the free
  public index is not owed the scraper's burst budget), with a single retry on
  transient failures.
- **`robots_guard.go`** — the site's robots.txt carries a long list of
  individually disallowed `/doc/` paths: case-removal orders. The guard loads it
  once, honours the `User-Agent: *` group, and refuses those documents by name
  (`indiankanoon document N is excluded by robots.txt`) rather than fetching a
  taken-down judgment. An unreadable robots.txt fails open, so the mode is never
  silently blinded.

### `internal/tools/casemine.go` — the second case-law client

Casemine's access policy was measured, and the client encodes the measurement as
a two-step fetch ladder:

- **Measured (October 2026):** every non-browser User-Agent is answered with a
  flat nginx 403 — 117 bytes, no challenge to solve — and that includes headless
  Chromium's own `HeadlessChrome` UA. With the scraper's Chrome UA the same
  request returns the full server-rendered page (366 KB homepage, 478 KB
  search results). The wall is User-Agent-only.
- **The ladder:** `Scraper.FetchHTML` (plain HTTP, the scraper's browser-like
  headers and jar session) runs first; when it fails or the body reads as a wall
  (`casemineLooksBlocked`: the 403 title, or suspiciously few bytes),
  `Scraper.RenderHTML` retries through the shared browser with the jar's cookies
  bridged in — the session a user exported via `COOKIE_FILE_PATH` travels with
  the render. Both failures are named in one error; neither stage is retried
  silently.
- **Parse:** `div.jdlist` cards — `a.jdlink` carries `/judgement/in/<id>` with
  the `jd-title`, `p.text-snippet` (with `<em>` highlights stripped), and
  `jd-court` / `jd-date` / `jd-cite` metadata spans. The first page carries no
  pagination markup; a bounded legal check reads the first page only.
- **Scraper additions:** `FetchHTML` and `RenderHTML` are general capabilities
  (validated target, jar session, render semaphore), not Casemine-specific
  hooks — any client that needs raw or rendered markup can use them.

### `internal/export` — saved output files

A finished run becomes files: `ExportCompany` / `ExportPerson` render Markdown from the
research models and a self-contained HTML document that Chromium prints to PDF. Both are
written through a `Destination`, so the same exporter writes to Cloud Storage or a local
directory.

- **Deterministic rendering.** The Markdown and HTML come from `OrgChart` / `PersonProfile`
  — no second model call — so an artifact can be reproduced from the run's own data.
  Values that would break a Markdown table (`|`, newlines) are escaped, and the HTML is
  rendered through `html/template`, so scraped text cannot inject markup into the PDF.
- **The PDF needs no PDF library.** `tools.Scraper.RenderHTMLToPDF` prints the document with
  the browser the scraper already manages, taking one of the same render slots. The
  document carries inline styling and no remote assets, because the shared browser context
  aborts image/font/stylesheet requests.
- **Branded reports.** `brand.go` carries the Crystal AI identity — the palette, serif/sans
  type stacks, the scope kickers and the cover photograph, embedded in the binary
  (`go:embed`) and inlined as a `template.URL` data URI so html/template cannot percent-encode
  the payload. Every PDF opens with the deck's cover — teal band, wordmark, kicker, serif
  title, summary, checked stamp, photograph — and prints a running footer through
  `RenderHTMLToPDFFootered`, an optional renderer capability the exporter degrades gracefully
  without (the document's own end footer is the fallback branding). Page geometry lives in the
  print call rather than the document's `@page` rule: Chromium draws the running footer inside
  the print margins only when `@page` leaves them unset.
- **One artifact per attempt.** A run reports a Markdown artifact and a PDF artifact
  separately: if printing fails, the PDF artifact carries the reason while the stored
  Markdown stands, and neither failure fails the research.
- **Naming.** `<prefix><scope>/<UTC timestamp>-<slug>.<ext>`, so objects sort
  chronologically and a subject name with accents or non-Latin characters still yields a
  portable file name.
- **Detached budget.** Exports run on `context.WithoutCancel` with their own timeout, so a
  run that consumed its whole research deadline still saves its results.

### `internal/agent/demand_chain.go` — buyer landscape

The buyer-side mode answers "who consumes or buys this product" — an evidence
problem with a different shape than the supplier side, where sellers advertise
themselves. Its phases: **infer** (one structured call proposes the consuming
stages of the product; the operator's `StagesOverride` wins outright, and a
failed inference degrades to a generic stage rather than failing the run),
**discover** (stage-operator lanes built from the chain's own vocabulary, plus
product-shaped demand lanes: tenders, importers, input language), **verify**
(consumption evidence through consumption-shaped purposes), and **assemble**
(deterministically). Demand claims carry a directness-weighted confidence ladder
(import/tender record > the buyer's own input language > stage verification),
buyer bait ("buyers list" listicles, market-research vendors) stays in the
appendix as leads, and intermediaries (traders, brokers) are excluded unless the
operator asks for them. The report carries the chain itself plus the house-style
honesty warning that private procurement is invisible to web research. It shares
the market mode's evidence vocabulary (`MarketLandscapeCompany`/`Evidence`),
tier classification and name hygiene, so a later supplier∩buyer matching pass
needs no schema change.

### `internal/gcs` — Cloud Storage JSON API client

`Upload`, `Download` (with `found=false` for a missing object, which is a normal cache
miss), `Delete`, plus `URI` / `HTTPSURL` helpers for `gs://` references and console links.
Authentication is Application Default Credentials, so `GOOGLE_APPLICATION_CREDENTIALS` or an
ambient identity is all it needs. Keeping it thin is deliberate: the cache and the exporter
need three operations on small objects, and the official client library would add gRPC,
protobuf and the googleapis tree for them.

### `internal/cache` — the cache store and its backends

`cache.Store` is the byte-oriented, context-aware surface the engine caches against, which
is what lets the same engine code run against three backends:

| Backend | File | Expiry | Notes |
| :--- | :--- | :--- | :--- |
| `MemoryCache` | `cache.go` | In-process TTL check | Default. Bounded FIFO eviction at `CACHE_MAX_ENTRIES` |
| `RedisCache` | `redis.go` | Redis `SET … EX` | Shared, survives restarts; connect with `REDIS_URL` or with the split `REDIS_HOST` / `REDIS_PORT` / `REDIS_PASSWORD` (the URL is assembled, credentials escaped); keys namespaced by `REDIS_KEY_PREFIX` |
| `GCSCache` | `gcs.go` | Expiry embedded in each object | Shared, survives restarts; Cloud Storage JSON API with Application Default Credentials (`GOOGLE_APPLICATION_CREDENTIALS` or an ambient identity), so it needs no key material |

- **Interface:** `Get` / `Set` / `Delete` / `Close` / `Name`, each taking a `context.Context`
  and raw `[]byte`. Implementations never return errors: a lookup or write that fails
  degrades to a miss (logged), because a cache is an optimization rather than a dependency.
- **Typed helpers:** `cache.GetJSON` / `cache.SetJSON` perform the JSON round trip, which is
  what keeps the interface honest for an object store.
- **Keys:** `cache.HashKey(prefix, input)` = prefix + SHA-256 hex of the input —
  deterministic, collision-safe, and fixed-length.
- **Envelope:** Cloud Storage has no per-object TTL, so `gcs.go` wraps each payload in an
  `envelope` carrying an absolute expiry that is decoded in the same single read. Redis and
  the in-process cache use their native expiry instead.
- **Bounded memory:** for `MemoryCache` an insertion-order queue drives **FIFO eviction** at
  `maxEntries` (default `2048`, from `CACHE_MAX_ENTRIES`), a background goroutine sweeps
  expired entries every 10 minutes, and `Delete` keeps the queue in step so a stale queue
  position cannot shadow a later insert. `Close()` is idempotent and called via `defer` in
  `main`; non-positive size limits fall back to `DefaultMaxEntries`.
- **Backend selection** lives in `cmd/server/cache.go`: it connects to (and pings) the
  configured backend, logs which one is in use, and falls back to the in-memory cache with a
  loud warning when a shared backend is unreachable.

### `internal/models` — shared vocabulary

Pure data structures (plus tiny display helpers); no business logic. Every package
exchanges data through these types:

| File | Contents | Role |
| :--- | :--- | :--- |
| `agent.go` | `EventType` (`thinking`, `tool_call`, `tool_result`, `complete`, `error`), `AgentEvent` (+`FormattedTime` for the UI clock), `SearchResult`, `ScrapeResult` (incl. `Blocked`/`BlockReason` and `ContentTruncated`) | The agent's observability contract and raw tool outputs |
| `company.go` | `OrgChart` (+`UnverifiedSourceCount()`), `Department`, `Employee`, `Location` | Final company-mode output shape (also the synthesis JSON schema Gemini must fill). Custom `UnmarshalJSON` tolerates a string-or-object `headquarters`, bare-string `Location` entries (the shape the model drifts to for `brief.key_locations`), and numeric `headcount_est`; `UnverifiedSourceCount` drives the "not enough detail?" hint |
| `person.go` | `PersonProfile` (+`ScorePercent()`), `Experience`, `Education` | Final person dossier shape; `ScorePercent` rounds the 0–1 confidence to a whole percent for the UI badge |
| `research.go` | `ResearchPreferences`, `CompanyBrief` (embeds preferences), `PlannedQuery` / `QueryPlan`, `RankedResult`, `Evidence`, `ScrapeReportEntry`, `QualityReview`, `CompanyResearchContext`, `QuestionAnswer` | Inputs and intermediate artifacts of the company pipeline, plus the retained context used to answer post-run questions. `QualityReview` has a custom `UnmarshalJSON` because the model returns `follow_up_queries` as either strings or objects |

### `internal/web` — embedded UI layer

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
    tool_result, teal complete, red error, amber timing).
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

## Repository layout & file roles

```
WebSearch_GO/
├── cmd/
│   └── server/
│       ├── main.go              # Composition root: config, DI, routes, SSE, shutdown
│       ├── cache.go             # Cache-backend selection (memory/redis/gcs) + fallback
│       ├── output.go            # Output-artifact destination + exporter wiring
│       ├── main_test.go         # SSE termination, browser-choice policy, security headers
│       └── sse_test.go          # SSE reconnect behaviour
├── internal/
│   ├── agent/
│   │   ├── engine.go            # ReAct core: tool interfaces, cached search/scrape, Emit, synthesis plumbing
│   │   ├── company.go           # Company agent: plan → search → rank → scrape → synthesize + QC loop
│   │   ├── person.go            # Person agent: disambiguation → bio scraping → dossier synthesis
│   │   ├── legal.go             # Legal agent: identity → IndianKanoon/Casemine search → classify → report
│   │   ├── market_landscape.go  # Market agent: lanes → discovery → verification → deterministic report
│   │   ├── demand_chain.go      # Buyer agent: infer value chain → stage lanes → consumption verification
│   │   ├── hub.go               # Session + SessionHub: thread-safe per-research event buses
│   │   ├── timing.go            # Per-phase timer/recorder for the run's timing report
│   │   ├── legal_test.go        # Query plan, identity classification, buckets, budget honesty
│   │   ├── engine_test.go       # Engine limits, negative scrape cache, render-mode cache keys
│   │   └── company_test.go      # Fallback query plan, ranking, QC loop, profile candidates
│   ├── cache/
│   │   ├── cache.go             # MemoryCache: bounded in-process TTL cache (FIFO eviction)
│   │   ├── store.go             # Store interface, JSON helpers, expiry envelope
│   │   ├── redis.go             # RedisCache: shared backend with native per-key TTL
│   │   ├── gcs.go               # GCSCache: object backend with an embedded expiry
│   │   └── cache_test.go / backends_test.go / redis_integration_test.go / gcs_integration_test.go
│   ├── config/
│   │   ├── config.go            # Environment loading with defaults + positive-int validation
│   │   └── config_test.go
│   ├── export/
│   │   ├── export.go            # Artifact/Destination types, exporter wiring, Cloud Storage
│   │   │                        #   and filesystem destinations, format selection
│   │   ├── brand.go             # Crystal AI identity: palette, type stacks, embedded cover photo
│   │   ├── run.go               # Export per mode (company/person/legal/market/demand-chain), naming, slugify
│   │   ├── markdown.go          # Markdown documents for all research modes
│   │   ├── html.go              # Self-contained branded printable documents for the PDF
│   │   ├── demand_chain.go      # Buyer-side Markdown + HTML renderers (value chain, claims)
│   │   ├── util.go              # Small rendering helpers
│   │   └── export_test.go / render_test.go / brand_test.go / export_integration_test.go
│   ├── gcs/
│   │   ├── client.go            # Cloud Storage JSON API client (upload/download/delete)
│   │   └── client_integration_test.go
│   ├── llm/
│   │   ├── gemini.go            # Gemini client: structured JSON, cleaning, JSON + transient retry
│   │   └── gemini_test.go       # cleanJSONString + transient-error classification tests
│   ├── models/
│   │   ├── agent.go             # AgentEvent (5 event types), SearchResult, ScrapeResult
│   │   ├── company.go           # OrgChart (+UnverifiedSourceCount), Department, Employee, Location
│   │   ├── person.go            # PersonProfile (+ScorePercent), Experience, Education
│   │   ├── legal.go             # LegalQuery, LegalReport, LegalFinding, LegalSourceReport
│   │   ├── market_landscape.go  # MarketLandscapeQuery/Report/Company/Evidence (shared by both landscape modes)
│   │   ├── demand_chain.go      # DemandChainQuery/Report, ValueChainStage, buyer claim types
│   │   └── research.go          # Briefs, QueryPlan, RankedResult, Evidence, ScrapeReportEntry,
│   │                            #   QualityReview (tolerant follow_up_queries), QuestionAnswer
│   ├── tools/
│   │   ├── serper.go            # Serper.dev search client with retry + linear backoff
│   │   ├── scraper.go           # Fetch policy, charset decode, noise-stripping text extractor, retries,
│   │   │                        #   FetchHTML/RenderHTML (raw & rendered markup with jar sessions)
│   │   ├── indiankanoon.go      # IndianKanoon case-law client: search parse, doc fetch, robots guard
│   │   ├── casemine.go          # Casemine client: plain-fetch → cookie-bridged render ladder, jdlist parse
│   │   ├── robots_guard.go      # robots.txt User-Agent:* group honouring, fail-open, load-once
│   │   ├── hostlimit.go         # Per-host politeness: token bucket + concurrency cap
│   │   ├── pdf.go               # PDF content-type policy, local text extraction, printable-ratio gate
│   │   ├── scraper_browser.go   # Optional Playwright renderer (full Chromium, shared context)
│   │   ├── scraper_browser_integration_test.go  # Opt-in real-Chromium test (PLAYWRIGHT_INTEGRATION=1)
│   │   ├── pdf_integration_test.go              # Opt-in real-world PDF test (same gate)
│   │   ├── cookies.go           # Browser cookie-export JSON → http.CookieJar
│   │   └── serper_test.go / scraper_test.go / pdf_test.go / cookies_test.go / indiankanoon_test.go
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
├── Dockerfile                   # Multi-stage: golang:1.24-alpine → distroless nonroot (HTTP-only)
├── Dockerfile.browser           # Playwright base image variant, for browser rendering
├── .dockerignore                # Excludes credentials, cookies, docs, and local artifacts
├── requirements.txt             # Python companion dependencies only
├── go.mod / go.sum              # Module: goquery + genai + playwright-go + golang.org/x/net + ledongthuc/pdf
├── docs/
│   ├── README.md                # Usage: prerequisites, env vars, endpoints, Docker, policies
│   ├── ARCHITECTURE.md          # Architecture, module deep dive, testing, known issues
│   ├── SCRAPER_WALKTHROUGH.md   # End-to-end scraper behavior and limits
│   └── GO_FOR_PYTHON_READERS.md # Go concepts explained for Python readers
├── react_engine.py              # Standalone Python replica of the ReAct engine (CLI)
└── websearch.exe                # Build artifact of cmd/server (gitignored via *.exe)
```

---

## Cross-cutting mechanisms

### Caching strategy

| Cached artifact | Key | TTL | Purpose |
| :--- | :--- | :--- | :--- |
| Serper search results | `search:<sha256(query)>` | 48 h | Repeat queries (common during iterative research and drill-downs) don't cost API credits |
| Scraped page text | `scrape:<sha256(url, render mode)>` | 7 days | Pages rarely change; re-scraping is the slowest tool call |
| Failed scrape (negative) | same shape as above | 15 min | Stops a follow-up question re-fetching a known dead end (403, empty page, unreadable document). Cancellations are never cached |
| Hard-URL escalation | `scrape:<sha256(url, mode=hard)>` | 7 d / 15 min | A recovered page is reused by QC follow-up iterations, and a ladder that ended still blocked is not repeated within the same run |

All four share one `cache.Store`. The backend is chosen by `CACHE_BACKEND`: `redis`
(process-local, the default), `redis`, or `gcs` — the two shared backends are what make the
cache survive a restart and serve every instance of the server. A shared backend that
cannot be reached at startup falls back to the in-memory cache with a warning, so a cache
outage costs speed rather than availability, and the backend in use is logged at startup.

The scrape key includes the **render mode** (`default` / `http` / `browser` / `hard`,
derived from any per-request browser override or the hard-URL ladder). Without it, a
thin HTTP-only result cached by an earlier run would be served to a browser-rendered
re-run of the same URL — and a cached failure would suppress the rendered attempt that
might have succeeded.

Cached pages are **revalidated rather than re-downloaded**. A scrape result carries the
`ETag` / `Last-Modified` the server returned, and once an entry is older than
`SCRAPE_REVALIDATE_AFTER_SECONDS` the engine issues a conditional request: `304 Not
Modified` restamps the entry for one round trip, a changed page replaces it, and a failed
revalidation keeps the cached copy (losing good evidence is worse than serving slightly
stale evidence). A page that sends no validator can only be refreshed by a full download,
so it is served from the cache until it expires.

### Agent observability (event contract)

`models.AgentEvent` is the single observability contract: `SessionID`, `Type`
(one of five `EventType`s), `Step` (1-based pipeline position), `Content`,
optional `ToolName`/`ToolInput`, `Timestamp`. Agents only *emit*; the SSE handler
*consumes*, persists history, and renders. This separation keeps agents UI-agnostic
and lets tests pass `nil` channels.

### Concurrency model

- One goroutine per research run (spawned in `handleResearch`); the HTTP handler
  returns immediately.
- Communication is channel-based: buffered `EventCh` (non-blocking sends via
  `Emit`) and `Done` (closed once via `sync.Once`).
- Shared state is small and locked: `Session.Events` (mutex), `SessionHub` (RWMutex),
  `MemoryCache` (RWMutex).
- `Engine` itself is stateless per run (only immutable config), so concurrent
  research sessions are safe.

### Budgets & guards (cost/runaway protection)

| Guard | Where | Default |
| :--- | :--- | :--- |
| Max agent steps per run | `MAX_AGENT_STEPS` → `Engine.MaxSteps` | 10 |
| Max pages scraped per run | `MAX_SCRAPES_PER_RUN` → `Engine.MaxScrapes` | 10 |
| Max search results per query | `MAX_SEARCH_RESULTS` → `Engine.MaxSearchResults` | 12 |
| Company query plan size | `maxCompanySearchQueries` / `limitQueryPlan` | 24 queries |
| Person query plan size | `personPlanMaxQueries` | 12 queries |
| Person search concurrency | `personSearchConcurrency` | 4 bounded workers |
| QC review passes | `maxCompanyReviewIterations` | 3 (≤ 2 follow-ups) |
| Min budget to start a QC follow-up | `minQCIterationBudget` | 60 s |
| Per-run wall clock | `RESEARCH_TIMEOUT_SECONDS` → context timeout | 180 s |
| Per-run wall clock, browser rendering | `BROWSER_RESEARCH_TIMEOUT_SECONDS` | 300 s |
| Form body size | `maxFormBytes` in `main.go` | 1 MiB |
| Scraped HTML body | `maxBodyBytes` in `scraper.go` | 2 MiB |
| Text sent to LLM per page | `maxContentBytes` (rune-safe) | 40 KB |
| Scrape attempts per URL | `scraperMaxAttempts` | 3 (linear backoff) |

Search-result caps are per Serper query. The normal company/person pipeline requests the
first bounded result set returned by Serper; it does not automatically paginate Google results
to positions 13–24, nor crawl the first two pages of every source domain. The separate legal
search workflow is the exception: `LEGAL_PAGES_PER_QUERY` defaults to 2 and controls its
source-specific result-page walk. Legal pagination has a configurable ceiling of 5 pages by
default, but the walk stops earlier when a page is empty, repeats prior hits, or adds no new
entity-relevant hits.
| LLM attempts per call | `geminiMaxAttempts` | 3 (linear backoff) |
| Concurrent browser renders | `maxConcurrentRenders` (semaphore) | 2 |
| Failed scrape retention | `negativeScrapeTTL` | 15 min |
| Reverse lookups per company run | `VALIDATION_LOOKUPS_PER_RUN` → `Engine.ValidationLookups` | 4 |
| Min remaining budget for a reverse lookup | `minValidationBudget` | 15 s |
| Per-lookup search timeout | `validationLookupTimeout` | 12 s |
| Freshness windows | `VALIDATION_STALE_AFTER_MONTHS` / `VALIDATION_RECENT_WITHIN_MONTHS` | 36 / 18 months |
| LLM temperature | `gemini.go` | 0.2 |
| HTTP server timeouts | `main.go` (header/read/idle/write) | 10/30/90s / max(research, browser research)+30s |

### Evidence freshness & validation

Snippets and aggregator listings arrive undated, so a director appointed in 2020 and one appointed last month look identical to the synthesiser — which is how a five-year-old appointment notice became a "current" CFO. The pass that fixes this runs at the end of a company run, before the artifacts are written, in three parts:

1. **Dating** (`internal/agent/evidence_dates.go`, no requests). Every source is stamped with the newest date it proves about its own content: the LinkedIn activity id's timestamp (`id >> 22` is epoch ms, which is what turns a profile URL into a dated citation), JSON-LD `datePublished`/`dateModified`, a date written into the URL or file name (`…1july2020.pdf`, `…_October 24_ 2025_…`, `/nov-dec_2023/`), and finally the response's `Last-Modified`. Content signals beat `Last-Modified`, because a re-uploaded 2020 filing is still a 2020 filing. Each stamp reaches the synthesiser as `as of YYYY-MM-DD` on the evidence item itself.
2. **Assessment** (`internal/agent/validate.go`, no requests). Every leadership and department row is matched to the sources that support it — its recorded citation plus any evidence naming that person — and labelled `verified-recent` (a dated source inside the recent window from the company's own domain or two independent domains), `dated`, `stale`, `undated`, or `conflicting` when two people claim one title. Row metadata (`AsOf`, `Verification`, `VerifiedBy`, `VerificationNote`) is attached by the agent, never by the model.
3. **Bounded reverse lookups.** Only the rows the assessment could not resolve get one snippet-only search — `"Name" "Company"` — capped by `VALIDATION_LOOKUPS_PER_RUN`, skipped when under `minValidationBudget` remains, and never fetched: the point is to find a *date* or establish current employment. A dated recent mention promotes the row; a positive current-employer statement keeps it current; nothing is ever added to the roster from a lookup.

Current-employment status is conservative and three-way:

- **Current** requires positive evidence tying the person to the researched company.
- **Historical** covers explicit former, retired, deceased, obituary, or passed-away evidence.
- **Current-unverified** covers an old or undated company association where no positive current
  employment evidence was found. Absence of a newer employer is not evidence of continued
  employment.

Verified `historical` rows are removed from the current roster in `enforce` mode (labelled
in `report` mode) and preserved in `ValidationReport.ExcludedRows`, rendered as **Excluded
from the current org chart** in the web UI, Markdown, and PDF reports. A
`current-unverified` row is **not** removed: the lookup's silence is not evidence the
person left, so the row stays in the chart carrying an "unable to verify" remark — visible
in the row's `As of` cell in the console, Markdown and PDF — and is counted separately as
`ValidationReport.RowsUnverified` in the Data freshness section. This shows the
unconfirmed results instead of hiding them, without presenting uncertain historical
affiliations as current leadership. `off` disables the entity gate comparison.

The result renders as an `As of` column and a "Data freshness" section in the console, the Markdown and the PDF, including the conflicts and what each lookup changed. `OrgChart.UnverifiedSourceCount()` remains the separate measure of sources that could not be used at all.

### Entity gate

Every stage of a run is held to one confirmed company identity. The gate exists because
"relevant to the brief" is not "about the company": a live run for a company named **CJ
Shah** assembled 204 people from 24 sources with **none** of them on the company's own
domain — a LinkedIn name directory, a textbook by an author named Shah, a 2016 conference
archive, papers on Malaysian construction procurement, and a global wealth-management
roundup. All of them were topically on-brief.

**The identity** (`CompanyBrief.CompanyName`, `Domain`, `Aliases`, `RegistrationID`,
`EntityConfirmed`) is compiled once per run into an `entityMatcher` (`entity.go`) with keys
tried strongest first:

| Key | How it matches | Confidence |
| :--- | :--- | :--- |
| `domain` | registrable host equal to or a subdomain of the confirmed domain | 0.95 |
| `registration-id` | normalised (case- and separator-insensitive) containment — registry publishers format a CIN differently | 0.9 |
| `alias` / `name` | **every** token of the name appears as a whole word; legal suffixes are dropped, one-character tokens ignored, and a single short token refuses to be an identity | 0.8 / 0.7 |

`name` is the weakest key on purpose: a same-named company matches it exactly the same way,
so rows kept by it are labelled `matched by company name only` in every artifact rather than
presented as equivalent to a domain match.

**Where the gate applies**, earliest first:

1. **Query plan** (`planQueries`) — the planner is instructed to anchor every query, and
   `enforceEntityQueries` then *drops* any query that names neither the company, an alias,
   the domain nor the registration id, refilling the plan from `entityAnchoredQueries`
   (which always contributes `site:<domain>` when a domain is known). The drop count is
   reported in the plan's timing detail.
2. **Ranker** (`applySourceSignals`) — each `RankedResult` records its `EntityKey`; an
   unmatched candidate takes a −0.35 score penalty and the reason `entity=none`, so it
   cannot outrank a page that carries the company's name, domain or registration id, but
   can still be fetched if scrape budget remains.
3. **Snippets** (`filterEntitySnippets`) — only snippets naming the company reach
   `synthesizeChart`, and `snippetNamesEntity` gates the per-candidate snippet fallback in
   `scrapeRanked` and `searchSnippetEvidence`. Name matching tolerates segmentation
   variants: a name or alias with at least two significant tokens whose spacing-free form
   (`collapseText`) is eight or more characters also matches any segmentation of the same
   words joined whole (`textContainsNameRun` over `wordTokens`) — "Goyum Screwpress" and
   the legal "Goyum Screw Press" both produce the run "goyumscrewpress", while a longer
   run-together word that merely begins with the name ("sanjaychemicalsllp.com" vs
   "sanjay chemicals") never matches, because a hit must consume whole words on both
   sides. Short or initialism names ("CJ Shah") keep the strict whole-word rule entirely.
   A multi-word name whose distinctive first token (five-plus characters, not a generic
   corporate word) is spelled out by a confirmed domain as the whole name run together
   also gains a single-token brand key — goyumscrewpress.in for "Goyum Screw Press" matches
   "at GOYUM" — while a domain that merely contains the token (sanjaychemindia.com contains
   "sanjay"), a name that is a person's name over generic industry words ("Sanjay
   Chemicals", where same-first-word firms like "Sanjay Chlorides" abound), and initialisms
   ("CJ Shah") never do.
   `mentionsCompanyNear` and `namesTheEntity` apply the same fallback.
4. **Rows** (`applyEntityVerdicts`, inside the validation pass) — a row supported by no
   entity-naming source is recorded in `EntityGate.OffEntity` with its title, department and
   failing source, and removed when the mode is `enforce`. A row whose name *is* the company
   name or an alias (`entityMatcher.namesTheEntity`) is excluded as the entity rather than a
   person — a shareholding page labelled "CJ SHAH" produced exactly that row in a live run.
   An excluded department head is cleared from `Department.Head`.
5. **Reverse lookup before exclusion** — the lookup policy (`unresolvedRows` +
   `lookupWorthiness`) is deliberately aggressive, because a lookup *establishes* facts the
   run could not collect rather than only dating rows it already has. Ranked candidates:
   a row with no entity key at all (the lookup can rescue it) → a title two people claim →
   a name-only match → stale → undated → merely dated. The current-status tests
   (`hasCurrentCompanyEmployment` and friends) run through the entity matcher, so results
   that spell the company differently — operating name vs registered name, or the bare
   brand — no longer read as "unknown" and cost a live row its place. A row already `verified-recent` *and*
   tied by a domain, alias or registration id is not looked up. `reverseLookup` reports
   `MatchedCompany`/`MatchedKey` when a result names both the person and the company, and
   `applyEntityRescue` upgrades the row (rescuing it from exclusion, or strengthening a
   name-only match), counting it in `EntityGate.RescuedByLookup`; the cap-truncated pass
   reports `EntityGate.LookupsDeferred`. The proactive counterpart lives in
   `entity_lookup.go`: `fillEntityGaps` spends up to `defaultGapLookups` anchored searches
   (`"<Company>" <function> head …`) on the functions the evidence does not cover, before
   synthesis, keeping only results the matcher accepts (`EntityGate.GapLookups`/
   `GapEvidence`). Fetching runs on `scrapeDeadlineContext`, i.e. on a context that stops
   `scrapeReserve` = `validationReserve` + `synthesisReserve` + `gapLookupReserve` (55 s)
   short of the deadline, and `gapLookupTimeoutFor` bounds one gap search by the headroom
   left. This is what keeps the lookups from being starved: live, scrape 155.0 s of a 180 s
   budget produced "0 reverse lookup(s)", 15 rows unchecked and no gap lookups; with the
   reserve in place the same brief ran 10 lookups and kept 12 people who had no
   company-named source, two of them directors previously excluded with no source at all.
6. **Person-scoped mentions** — `mentionsCompanyNear` requires a name/alias mention within
   `companyMentionWindow` (300) bytes of the person's name before a source can tie that
   person to the company. Domain and registration-id matches are unaffected: they are
   facts about the URL. Without this, a peer-company table in an unrelated IPO story tied
   whoever else the page named.
7. **Host deny-list** — `lowQualityHosts` (job boards, content farms, company directories,
   document dumps, social networks) is classified as `aggregator`/quality 0.1, below the
   0.25 relevance floor, so those pages are never fetched.
8. **Prompts** — synthesis and QC both receive `entityMatcher.promptLine()` and are told
   that a page never naming the company is not evidence about it.
9. **Output** — `renderEntityGateMarkdown` / the HTML template / the console panel report
   the keys, the confirmation state, the mode, the counts (queries dropped, candidates
   tagged/unmatched, snippets dropped, rows excluded, people rescued by lookup) and the
   excluded people. The company report's *Excluded search results* appendix (Markdown and
   HTML) also lists the search hits the gate excluded, per query and capped, pointing at
   the report appendix for the selected evidence — so a spelling-variant miss is
   reviewable rather than silent, and the console's Search results tab labels the same
   hits inline.

**Modes** (`ENTITY_GATE_MODE`): `enforce` (default) removes and lists; `report` labels and
lists without removing, which is how the gate's effect is measured against `off`, where no
stage filters at all. A nil `entityMatcher` means "no gate" everywhere, so `off` is a true
baseline rather than a subtly different pipeline.

**The confirmation step** (`POST /api/resolve-entity` → `entity_candidates` fragment) is
where a company run starts: the console form's own submit targets it (`hx-target=
"#entity-candidates"`), so both the button and Enter go through it, and only a card's
`Confirm` button posts to `/api/research`. `CompanyAgent.ResolveEntityCandidates` performs
one or two searches, drops
directory/social hosts (registry pages are kept as *evidence* for the CIN, never as a
candidate domain), scores candidates by how much of the company name the domain carries
(`domainEchoStrength`: the name itself 2, contains it 1), and prefers name-matching domains
when any exist. The operator picks or edits one and the console posts a normal research form
carrying `entity_domain`, `entity_aliases`, `entity_reg_id` and
`entity_confirmed=1` — so the gate's key set comes from a human, not from
`inferOfficialDomain`'s guess at the ranked results.

A card always renders, and the first one is pre-selected with `autofocus`, so the step is one
click. Three properties are worth keeping:

- **A typed domain short-circuits the search.** `handleResolveEntity` normalizes what was
  typed (`agent.NormalizeDomain`, so a pasted URL becomes its host) and renders it as the
  single candidate without touching the agent. A value that is not a domain at all yields
  `""`, which falls back to the lookup rather than gating a run on nonsense.
- **Two names, one company.** Each card lists the other candidates as `entity_extra`
  checkboxes whose *value* carries the whole identity (`domain|name|registration` — an
  unticked checkbox submits nothing, so parallel hidden fields could not stay in step).
  `handleResearch` reads them with `parseEntityExtras`; `confirmEntityIdentity` refuses a
  ticked identity whose registration id contradicts the card's (two CINs are two legal
  entities) and otherwise `mergeEntityExtras` folds the domains into `CompanyBrief.Domains`
  and the names into `Aliases`. `entityMatcher` compiles a key per domain, all at 0.95, with
  the primary domain first; query anchoring still uses the primary only, so extra domains
  widen what the gate accepts without multiplying searches.
- **Person mode opts out** with its own `hx-post="/api/research"`, and `handleRerun` never
  re-asks: both would otherwise re-confirm an identity that is already fixed.

`rootHost` (in `company.go`) reduces a host to its registrable name, keeping multi-part
suffixes: before that, `sanjaychem.co.in` reduced to `co.in`, and a domain key of `co.in`
matches *every* `.co.in` host on the web — a gate that accepts everything.

**The person step** (`POST /api/resolve-person` → `person_candidates`) mirrors it, because a
name is ambiguous about *which human* the way a company name is about *which organisation*.
`PersonAgent.ResolvePersonCandidates` spends at most `personResolveQueryLimit` searches and
reads candidates out of the hit titles:

- `splitPersonHeadline` splits `<name> - <role> - <employer>` (also `|`, `–`, `—`, `·`),
  `containsAllTokens` requires every token of the typed name as a whole word, and
  `splitPersonNameSegment` keeps an honorific while dropping trailing prose — and returns the
  role when it shares the name's segment ("Jane Doe, Managing Director").
- `splitRoleAtEmployer` reads the "<role> at <employer>" shape, and `personTitleNoise` keeps
  the platform out of the employer ("| LinkedIn" is not a company).
- `scorePersonCandidate` ranks what an operator would use: a hit that already names the typed
  company (+2) and place (+1) leads, a stated role/employer (+1 each) beats a bare name, a
  people-directory host counts `-1` and is flagged on the card, and a real profile
  (`linkedin.com/in/`, a personal site, Wikipedia) gets the tiebreak.

The pinned identity travels as `models.PersonQuery`: `ResearchPerson(ctx, sessionID, query,
eventCh)` searches `"<name> <company> <location>"`, reads the confirmed profile first
(`SeedURL`) and always keeps its snippet as a `=== CONFIRMED IDENTITY ===` block, crediting
the URL as a source even when the fetch is walled. Employer and place are pre-filled from the
candidate, so the operator confirms a context read off a source instead of recalling one. If
company *and* place were both typed, the step spends no search at all — the same cheap path
the typed-domain case uses.

**The person identity gate** (`internal/agent/person_gate.go`). Company mode holds a run to an
entity at five points; person mode used to hold it to nothing, so every snippet the search
engine loosely matched — including other people with the same name — reached the synthesiser,
and a dossier could describe a same-named person at a different company in a different
location. The gate applies what the confirmation step implies: the company and location on the
card are acceptance rules, not just search terms. `personGate.classify` decides per source:

- names the person (whole-word) — without it, the source is unrelated;
- the confirmed profile URL — always accepted;
- the confirmed company or the pinned location present — matched;
- a **different employer stated in the headline** (`statedHeadlineEmployer`, read only in a
  role context: "<role> at <X>" or the segment after a recognised role) — excluded as a
  different person, with the stated employer in the reason;
- otherwise — *name-only*: kept for a fetched page (a career history legitimately lists past
  employers in its body), counted and labelled, but a **snippet** that names the person and
  states no employer is dropped, because a snippet is a free-floating assertion.

A court-case headline ("Jane Doe vs Bharat Enterprises on 3 March") and a past employer in the
body of a fetched page are deliberately *not* read as a stated employer, so real evidence
survives. With no company confirmed there is nothing to hold sources to, so a name-alone run
excludes nothing rather than returning an empty dossier. Every exclusion is listed in the
dossier's **Identity check** (`models.PersonGate.Excluded`) next to the run's operating
results — the queries issued, the phase timings, and every fetch attempt with its outcome
(`ScrapeReport`, `Timings`, `SearchQueries`), the counterpart of the company chart's scrape
report and timings.

**The multilateral view** (`PersonProfile.SameNamed`). The realistic learning behind the gate:
a person search is answered by several people who share the name, so pretending otherwise is
what produced blended dossiers. The gate keeps the dossier about the confirmed person, and
`sameNamedGroups` regroups the excluded evidence for everyone else — one group per employer a
same-named person's source states (each is another identity), plus one group for material that
names the person but states no employer. Groups are ordered strongest-first (most evidence
leads) with the unattributed material last, capped at 8 groups × 4 snippets, and each group's
own headline leads it. The section is rendered in the console, the Markdown and the PDF; in
the console each employer group carries a **Research this person instead** action that re-runs
the research with that employer prefilled.

**Overlapping timelines** (`Experience.Flag`). Two "possible posts" whose periods overlap are
the last blend signature QC can catch from the dossier alone: a Managing Director row at the
confirmed employer and a CTO row at another employer both claiming 2019-2021 is either genuine
concurrency or the same-named person's role. `flagOverlappingTimelines` reads every kept row's
period (`parsePeriodYears` handles "2019-present", "Jan 2015 – Dec 2019", "Present" with no
year, reversed ranges), pairs up rows at *different* employers whose ranges share **more than
one year** — a single shared year is a normal handover — and sets a flag on both rows naming
the other post and its period. Rows at the same employer are never flagged (a board seat
alongside an executive one overlaps legitimately), nothing is dropped, and the flag renders on
the row in the console, the Markdown and the PDF, so the resolution is the operator's: check
the two cited sources.

### Error handling & resilience

- **Aggregator sources** (`internal/agent/sources.go`). LinkedIn person profiles cannot be read
  without a session, so `alternativeProfileSources` names the people-data aggregators that
  publish the same title-and-employer fact: crunchbase, zoominfo, clearbit, apollo.io, hunter.io.
  Company mode gives one company-name-only Google-index slot each to LinkedIn, Economic
  Times, ZaubaCorp, TheCompanyCheck, and Tracxn. Each slot covers common
  legal-name variants such as `Limited`/`LTD`; when available, registry slots also get a
  CIN-based fallback. Separate LinkedIn role-specific slots use the same name variants.
  Naukri has no dedicated slot and remains below the company evidence floor when it appears
  in general search results. Crunchbase is retained only as a low-confidence corroborating
  source, not a dedicated company search slot.
  The planned global source profile includes LinkedIn, ZoomInfo, Apollo, RocketReach, The Org,
  Forbes, Fortune, Reuters, and Craft.co; these are kept separate from the India-focused
  source profile and selected through the company form's **Auto / India-focused / Global**
  source-profile toggle. Auto preserves the established India-focused default unless the
  operator explicitly selects Global.
  RocketReach is wired into the live pipeline as a separate provider: related-entity discovery
  uses at most four bounded title-group searches and retains query provenance, while company
  discovery adds functional seniority terms (Manager, General Manager, Deputy General Manager,
  Vice-President, Additional Director, and Executive Director) without increasing the call
  count, and explicit
  person research performs one structured profile/contact lookup before web research and
  synthesis; the lookup is the first workflow operation and RR facts are additive,
  provenance-tagged, and do not replace indexed
  evidence already present.
  Company mode also exposes default-on Leadership, Marketing, and Procurement / Supply Chain
  interests, plus an optional person-of-interest field. A supplied person receives three
  reserved company-anchored searches, including a LinkedIn-index query, before general
  planner queries are admitted.
  Two roles, kept distinct: for **person** material they are claims about a named individual,
  which `personDirectoryHosts` already flags; for **company** material, the selected source
  profile controls which indexed source slots are reserved. India mode uses promoted registry
  and business sources; Global mode uses its global publisher and directory list, with
  profile-aware classification so those results are not rejected by the general low-quality
  filter. The company plan gains one anchored query per selected host
  (`site:<host> ("<Company>" OR "<Company LTD>")`, which also passes `matcher.covers`),
  plus role-specific LinkedIn searches and optional CIN fallbacks; person research gains one
  more search whose snippets are labelled
  `DIRECTORY CLAIMS`, the confirmation step spends an aggregator query only when its two core
  passes come back thin, and `isProfileSourceURL` keeps the fetch priority in step with the query
  builder so a searched domain is not ignored at fetch time.
  RocketReach discovery uses `POST /api/v2/person/search` without requesting contact details.
  Returned profiles are filtered by strict company-token matching before they can branch into
  person-affiliation research. Explicit person dossier enrichment uses
  `GET /api/v2/person/lookup` once, preferring the confirmed LinkedIn URL and otherwise using
  name plus current employer. The lookup can contribute missing role, employer, location,
  career-history, education, skills, and LinkedIn fields; career rows are then included in
  the normal identity QC. Only this explicit person path can populate email or phone fields.

- **Related-entity mode uses a mandatory cache-first preflight.** It checks every planned
  direct-relationship and leadership query before search, reuses valid cached results through
  the shared engine, and reports checked/hit/miss counts and returned-hit counts in the result.
  The direct branch uses the selected India or Global source-profile hosts rather than relying
  only on generic company queries. Returned snippets are retained as review evidence even when
  structured extraction is too conservative, so an empty relationship classification does not
  hide the material the search actually returned.

- **Legal risk is derived after retrieval.** Legal mode still retains and displays every
  identity-matched finding, groups findings into deterministic dockets, and derives the risk
  position only from recent company-law matters where the company is the responding-side party
  (`facing`). Criminal, tax, initiated, stale, undated, and person-party findings remain visible
  in their appropriate review sections without inflating the company risk position.

- **A refused client is reported, not retried.** `isBotDenialStatus` recognizes the non-standard
  code a host uses to refuse a client (LinkedIn answers 999 to a plain client for a person
  profile: 1.5 KB, no content, measured), and `fetchHTTPOnce` returns it immediately with the
  reason `the site refused the request (HTTP 999)` — no retries, no body read. It stays
  **non-terminal** on purpose, because a render can still win a public page on the same host;
  `isRetriableStatus` keeps handling 408/429/5xx as transient.

- **Serper:** retries transient failures (network, 429, 5xx) with linear backoff;
  fatal errors surface immediately.
- **Documents (PDFs):** every ingest is bounded three ways, because a workflow that stalls is
  worse than one that reports a failure. `documentSkipBytes` (40 MiB) refuses an oversized
  document from its declared `Content-Length` before the transfer — as a `terminalFetchError`
  that `ScrapeURL` reports rather than handing to `tryBrowser`, since a render cannot turn a
  large filing into HTML. `maxDocumentDownloadBytes` (8 MiB) caps the body download before extraction,
  aligned with Gemini's inline limit (contrasting with HTML's 2 MiB `maxBodyBytes`). `WithDocumentTimeout` (env `DOCUMENT_EXTRACT_TIMEOUT_SECONDS`,
  default 60 s) bounds `scrapeDocument`'s extraction: the extractor is called on its own
  context, so a model call or a parser walk that does not return is abandoned with
  "context deadline exceeded" in the scrape report and the run continues. And
  `ExtractPDFTextPartial` checks the deadline between pages (the parser library offers no
  cancellation) and returns what it read with page markers (`--- [Page N] ---`), which `documentTextExtractor` keeps while logging
  the shortfall; `ExtractPDFTextContext` stays all-or-nothing for callers that cannot use
  partial text. The model side adds `documentRequestTimeout` (60 s) per extraction request.
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
- **Panic containment:** a panic while fetching a page fails that one URL (logged
  with a stack, reported in the scrape report) instead of ending the process, and a
  panic inside a background run completes that session with an error so the console
  renders an error box. Both sit outside `net/http`'s per-request recovery, which is
  why they need their own boundary; the test suite pins both with panicking doubles.
- **Port collisions:** binding a busy port is fatal *by design* (`log.Fatalf`), but the
  message names the cause (`... is already in use: another process ... is listening
  there`) because the process otherwise prints its startup line and exits, which reads
  like a crash. `scripts/dev.ps1` clears a stale instance off the port before every start.
- **Shutdown:** in-flight research gets up to 30 s to finish on SIGTERM/SIGINT.

### Security posture

- `html/template` auto-escaping everywhere; strict CSP (`img-src` additionally allows
  remote images, `https:` and `http:`, so the scraped-image gallery renders, with
  `no-referrer` limiting third-party tracking); `nosniff` / `DENY` / `no-referrer`
  headers; request-body cap; distroless **nonroot** runtime image; session IDs from
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

## Configuration

All configuration comes from environment variables, loaded once by `internal/config/config.go`.
The canonical table of variables, defaults and meanings lives in
[the README's environment-variable table](README.md#environment-variables); what follows is how
those values are loaded and validated.

- `config.Load()` is the only reader of the environment, so a deployment's configuration is
  fully described by one set of variables (plus the optional cookie file).
- Numeric values are parsed by `envInt`, which silently falls back to the default for unset,
  invalid, or non-positive values.
- `BROWSER_RESEARCH_TIMEOUT_SECONDS` is clamped to at least `RESEARCH_TIMEOUT_SECONDS`, and
  `http.Server.WriteTimeout` is sized from the largest configured budget so a long stream is
  never cut off mid-run.
- Gemini credentials are validated at startup: the server exits rather than accepting
  requests it cannot serve.
- `HARD_URL_HANDLER` (default `true`) gates the bounded hard-URL ladder. `HARD_URLS_PER_RUN`
  caps how many URLs one run may escalate, and the ladder's per-URL budget is *derived* from
  its own step caps (15 s + 15 s + 20 s, plus a margin) rather than written by hand, so the
  extended-settle step cannot be clipped by an outside timeout before it runs; override it
  with `HARD_URL_BUDGET_SECONDS` if you want a shorter leash. Escalation only starts while the
  whole ladder **plus** the synthesis that follows it still fits inside the run's deadline
  (currently budget + 20 s), so a late-run escalation defers instead of consuming the tail of
  a run and returning nothing. A ladder that runs out of its own budget is reported as
  `deferred`, not `failed` — the distinction is what says whether the wall or the clock won.

---

## HTTP API reference

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

## Testing

Run everything with `go test ./...`. The suite uses table-free, dependency-injected
unit tests — no network access required:

| Package | What's covered |
| :--- | :--- |
| `cmd/server` | SSE termination on a failed session; browser-choice policy (toggle disabled by the operator / Chromium unavailable / explicit opt-out / absent field); security headers incl. the CSP `img-src` allowance; re-run validation paths; the PDF extractor policy (Gemini preferred, local parser on failure, both-fail reporting) |
| `internal/agent` | Engine limit enforcement + default fallbacks (`mockSearcher`/`mockScraper`/`mockLLM`); negative scrape caching and render-mode cache keys; deterministic fallback query plan, ranking, QC loop termination, profile-candidate plumbing, QC budget guard |
| `internal/cache` | TTL expiry, FIFO eviction bounds |
| `internal/config` | Defaults, env-var overrides / invalid-value fallback, browser-research timeout selection and clamping |
| `internal/llm` | `cleanJSONString` fence/prose stripping; transient-vs-fatal error classification; opt-in inline-PDF extraction against the live API |
| `internal/models` | Tolerant unmarshalling (`headquarters`, `headcount_est`, string-or-object `follow_up_queries`) and `UnverifiedSourceCount` |
| `internal/tools` | Serper retry/response handling and the scraper's fetch policy, extraction and block detection against `httptest` servers — incl. charset decoding, non-HTTP/private-host refusal, per-request rendering override, idempotent `Close`, truncation flagging — plus cookie parsing & expiry and the PDF path (content-type policy, generic-binary sniffing, local parsing, printable-ratio rejection, extractor-failure surfacing) |
| `internal/web` | Template parsing, HTML auto-escaping, disclosure-panel structure, capability-gated browser toggle, run-budget badge |

Two tests drive **real Playwright/Chromium**: one proves a clearance cookie earned by
passing a JavaScript interstitial is reused across renders, the other renders a page to a
real PDF (Flate-compressed content streams, subsetted fonts) and checks the local parser
reads it — the synthetic fixture in the default suite is deliberately trivial. Both are
skipped unless `PLAYWRIGHT_INTEGRATION=1` is set, so the default suite needs no browser:

```bash
PLAYWRIGHT_INTEGRATION=1 go test ./internal/tools/ -run 'TestBrowserRendererReusesInterstitialClearance|TestLocalPDFParserReadsRealWorldPDF'
```

A further test proves Gemini reads a PDF sent as **inline data**, which is the primary
extraction path. It is skipped unless `GEMINI_INTEGRATION=1` is set and needs live
credentials:

```bash
GEMINI_INTEGRATION=1 go test ./internal/llm/ -run TestGeminiExtractsTextFromInlinePDF
```

The shared cache backends have their own opt-in tests, so a real Redis or bucket is only
touched when it is asked for:

```bash
# redis: needs REDIS_URL, or REDIS_HOST / REDIS_PORT / REDIS_PASSWORD
REDIS_INTEGRATION=1 go test ./internal/cache/ -run Live -v

# gcs: needs GCS_CACHE_BUCKET and Application Default Credentials
GCS_INTEGRATION=1 GCS_CACHE_BUCKET=my-cache go test ./internal/cache/ -run Live -v
```

Both verify a real round trip, that expiry is enforced by the backend, and that deletion
works. The GCS test deliberately stores one entry with an already-elapsed expiry to prove a
stale object reads as a miss.

The artifact exporter has a gate of its own, which is the only test that exercises the
whole saving path — a real Chromium printing a real PDF, uploaded to a real bucket and read
back — and it cleans up the objects it writes:

```bash
# needs GCS_OUTPUT_BUCKET (default crystal-crm-store), credentials, and Chromium
OUTPUT_INTEGRATION=1 go test ./internal/export/ -run Live -v
```


CI (`.github/workflows/go.yml`) additionally enforces `gofmt` cleanliness, `go vet`,
and a full build on every push to `main` and every pull request, using the Go
version pinned by `go.mod`.

---

## Building & deploying

### Local development

On Windows, `scripts/dev.ps1` (also the VS Code task **Go: Watch Server**) watches
every `.go`/`.html` file under `cmd/` and `internal/`, computes a fingerprint every
750 ms, and restarts `go run ./cmd/server` on change — hot reload without extra
tooling.

### Docker (production shape)

The `Dockerfile` is a two-stage build: `golang:1.24-alpine` compiles a static binary
(`CGO_ENABLED=0`, `-trimpath -ldflags="-w -s"`); the runtime stage is
`gcr.io/distroless/static-debian12:nonroot` — minimal attack surface, fast cold
start, and no shell/package manager in the image. Templates are embedded, so the
image contains only the single binary.

That image deliberately **cannot** render pages. `Dockerfile.browser` builds on
`mcr.microsoft.com/playwright:v1.62.1-noble` instead (browser version kept in step
with the `playwright-go` dependency) and is the one to deploy with
`ENABLE_BROWSER_FALLBACK=true` or the per-run toggle.

### Cloud Run deployment

Cloud Run runs the Go HTTP server and supplies the `PORT` environment variable
(the images default it to `8080` for local runs). Cloud Storage is an object store,
not an application host: it is used for exported Markdown/PDF artifacts and the
optional GCS cache backend. Redis is the default shared cache backend when the
service can reach the configured Redis endpoint. The repository includes
[`deploy/cloud-run.env.yaml`](../deploy/cloud-run.env.yaml) as a loadable
non-secret baseline for the service configuration.
If a managed Redis endpoint is non-TLS but its stored URL uses `rediss://`,
set `REDIS_USE_SSL=false`; when unset, the URL scheme is honored unchanged.

```bash
gcloud builds submit --tag REGION-docker.pkg.dev/PROJECT/REPOSITORY/websearch-go:TAG .
gcloud run deploy websearch-go \
  --image REGION-docker.pkg.dev/PROJECT/REPOSITORY/websearch-go:TAG \
  --region REGION \
  --env-vars-file deploy/cloud-run.env.yaml \
  --set-secrets GOOGLE_API_KEY=google-api-key:latest,SERPER_API_KEY=serper-api-key:latest,REDIS_URL=redis-url:latest
```

The command above builds the default image, which intentionally has no Chromium.
For browser rendering, build with [`Dockerfile.browser`](../Dockerfile.browser)
through the checked-in Cloud Build config, then deploy that image:

```bash
IMAGE="REGION-docker.pkg.dev/PROJECT/REPOSITORY/websearch-go:TAG"
gcloud builds submit \
  --config cloudbuild.browser.yaml \
  --substitutions "_IMAGE=${IMAGE}" \
  .
gcloud run deploy websearch-go \
  --image "${IMAGE}" \
  --region REGION \
  --env-vars-file deploy/cloud-run.env.yaml \
  --set-env-vars ENABLE_BROWSER_FALLBACK=true,ALLOW_BROWSER_TOGGLE=true \
  --set-secrets GOOGLE_API_KEY=google-api-key:latest,SERPER_API_KEY=serper-api-key:latest,REDIS_URL=redis-url:latest
```

On Windows, [`deploy-gcp.ps1`](../deploy-gcp.ps1) wraps
these browser-image build and deploy steps:

```powershell
.\deploy-gcp.ps1
```

Grant the Cloud Run runtime service account the minimum required permissions:
Secret Manager access for configured secrets, Storage object access for output/cache
prefixes, and any permissions required by the selected Vertex/Google API path.
Replace the `REPLACE_WITH_*` values in the env file before deployment. Create the
referenced secrets in Secret Manager and grant the runtime service account
`roles/secretmanager.secretAccessor`; never put secret values in the env file.
Keep `REDIS_URL`, API keys, service-account files, and cookie exports out of the
image; `.dockerignore` explicitly excludes local credentials and cookie files.

The existing UI is embedded Go HTML/HTMX and should normally remain on the same
Cloud Run service. GCS artifacts are stored privately and downloaded through the
authenticated `/api/exports` Cloud Run route; the application identity reads the
object, so the browser never needs GCS credentials. A Firebase Hosting split is
optional: deploy a separate static frontend, rewrite `/api/**` to Cloud Run,
configure CORS and authentication, and keep the artifact route on Cloud Run.

### Environment note for Windows dev

`.vscode/tasks.json` exposes the watcher. `go build ./cmd/server` produces
`websearch.exe` at the repository root, which `.gitignore` excludes through `*.exe`.

---

## Files that are not part of the Go service

These files live in the repository but are **not** compiled into or required by the
server:

| File | Role |
| :--- | :--- |
| `react_engine.py` | A standalone **Python replica** of the Go ReAct engine (its docstring maps each function to the corresponding Go file). It is a CLI learning/demo companion — `python react_engine.py --task "..."` or `--demo` for a fully offline scripted run. Not part of the Go build. |
| `websearch.exe` | *Absent until built.* `go build ./cmd/server` produces it at the repository root, and `.gitignore` excludes it through `*.exe`. |
| `docs/README.md` | Usage: prerequisites, quickstart, environment variables, endpoints, Docker commands, plus the security-header and fetching-policy summaries. |
| `docs/ARCHITECTURE.md` | This document — architecture, the module deep dive, testing, and the known-issues list. |
| `.gitignore`, `.vscode/`, `.github/`, `scripts/` | Repo hygiene, editor task, CI pipeline, dev watcher. |

---

## Project health & known issues

**Verified:**

- `go build ./...`, `go vet ./...` and `go test ./...` pass across all Go packages, and
  `gofmt -l` reports no formatting issues (CI enforces the same gates).
- Code quality highlights worth keeping: interface-driven agent tools (mockable),
  deterministic fallbacks for every intermediate LLM call, bounded memory everywhere
  (cache, session hub, event channels, scrape sizes), and disciplined HTTP hardening
  (fetch-target policy, content-type/size caps, charset decoding, CSP).

**Known limitations (deliberate behaviour, not bugs):**

- **Professional-profile handling is policy-driven.** With the hard-URL handler enabled (the
  default) a `linkedin.*` URL gets one bounded escalation per run — browser render,
  cookie-bridged render, extended settle — and falls back to its public snippet when the wall
  survives. With the handler disabled (`HARD_URL_HANDLER=0`) or Chromium unavailable,
  profiles are never scraped, which is when department-level results stay thin for a company
  whose functional leadership is visible only there. `ALLOW_PROFILE_SCRAPING=true` also puts
  them through the normal scrape path — a policy decision, so check the sites' terms first.
  - **With a session, the cheap path comes first.** `Engine.SessionCookiesFor` reports whether
    the jar covers a URL's host; when it does, a profile URL gets one cookie-authenticated
    `ScrapeCached` attempt before the ladder is considered — in person mode too, which
    previously sent every profile URL straight to the ladder. The live measurement supports
    this shape: a cookie-authenticated plain fetch returned a real profile (949 KB, no
    authwall), while the render sequence reached the sign-in page and then tripped the
    redirect loop. With no session the rule is unchanged: no unauthenticated authwall fetch,
    straight to the ladder.
  - **The ladder leads with the session for auth-walled shapes.** `hardLadderSteps` drops
    `plain-render` and starts at `cookie-render` when `HardURLShapeOf(...).AuthWalled()` and
    the jar has cookies. A public shape (a post or company page) keeps `plain-render` first,
    since it often needs no session at all.
  - **Cache keys carry a session fingerprint.** `Engine.sessionCacheSuffix` appends a hash of
    the cookies a URL would be sent (`Scraper.SessionFingerprint`, empty when none apply), so a
    logged-out authwall failure — negative-cached for 15 minutes — is never served to the run
    for which a session was just captured. Ordinary pages keep sharing entries.
  - **A refused session is named.** `tools.IsRedirectLoop` recognizes both spellings
    (`ERR_TOO_MANY_REDIRECTS` and "stopped after N redirects"), and
    `Scraper.sessionRejectedError` wraps the failure as `tools.ErrSessionRejected` only when a
    session was actually sent. The handler reports that as `session-rejected` with "re-capture
    it", instead of a generic ladder failure that sends the operator looking at the site.
    Without a session, a redirect loop stays an ordinary site failure.
- **Ranking is guarded but still heuristic.** `pruneUnconnected` drops candidates with no
  domain or company-name connection, which removed the off-company profiles that used to
  reach the scrape stage — yet in practice the ranker still marks nearly every candidate
  relevant, and a company whose name contains a common word, or that legitimately appears on
  generic directory or news pages, can be over- or under-filtered. Stricter pre-filters
  (domain or company-name must match) would tighten this further.
- **PDF extraction is model-first, and the fallback is weaker.** A PDF is read by
  Gemini, which handles compressed streams, subsetted fonts and scanned pages; a pure-Go
  parser (`ledongthuc/pdf`) takes over when the model call fails, so extraction survives a
  rate limit but cannot recover a scanned filing that has no text layer. Adopting that
  parser raised this module's minimum Go version to `1.24.1`, so both Dockerfiles build on
  `golang:1.24-alpine`.
- **Browser rendering is off by default for ordinary scrapes** and requires a Playwright
  install plus the per-run toggle (or `ENABLE_BROWSER_FALLBACK=true`). The hard-URL ladder
  uses the same renderer whenever Chromium can start, independent of that toggle.
- **Playwright's `chrome-headless-shell` crashes on some hosts**, so the renderer uses
  `channel: "chromium"` (the full browser) — correct, but heavier.
- **Runs are long** (roughly 90 s–5 min). Rendered runs draw the larger budget, and the
  budget-aware QC pass returns partial results rather than failing when time runs out.

**Possible future improvements** (none blocking):

- The cache uses Redis by default; `CACHE_BACKEND` selects Redis, Cloud Storage, or in-process memory to
  share it. A remote backend costs one lookup round trip per read, which is why `memory`
  stays the default, and a GCS bucket needs a lifecycle rule to reclaim objects that have
  expired logically but still occupy space.
- Renders are skipped for `404`/`410` and made cheap by asset blocking, but the render
  budget (`BROWSER_RESEARCH_TIMEOUT_SECONDS`) is still sized for the worst case rather than
  measured per site.
- `SerperClient` parses `knowledgeGraph` but never uses it — it could enrich company
  mode's evidence.
- `MAX_AGENT_STEPS` is enforced via budgets/bounds rather than a live ReAct
  loop counter; the domain agents are currently fixed pipelines rather than
  free-form ReAct loops (the original design vocabulary, preserved in
  [Design history & roadmap](#design-history--roadmap)) reflects the project's
  conceptual model).
- Negative scrape caching is time-based only; distinguishing "definitely gone" (404)
  from "try again" (timeout) would waste fewer retries.

---

*See [`README.md`](README.md) for prerequisites, the canonical configuration table, and
the deployment commands.*
## Design history & roadmap

### Phase 1: Workspace Initialization & Environment Scaffolding
- Initialize the Go module (`go.mod`) and declare core dependencies (`google.golang.org/genai`, `net/http`, HTML parser).
- Establish the folder structure (`cmd/`, `internal/agent/`, `internal/llm/`, `internal/tools/`, `internal/models/`).
- Set up configuration loading for GCP project credentials and `SERPER_API_KEY`.

### Phase 2: Tooling & Extraction Layer
- Implement `internal/tools/serper.go` for web search execution.
- Implement `internal/tools/scraper.go` with HTTP client timeouts, content-type and size guards, and HTML text sanitization.
- *(Added later)* `internal/tools/scraper_browser.go` — an opt-in Playwright renderer for pages that require JavaScript — plus the fetch-target policy that keeps scraping scoped to public HTTP(S) hosts.
- *(Added later)* `internal/tools/pdf.go` — PDF text extraction, so registry filings and officer lists published as documents become evidence: Gemini reads the PDF inline (verified against the live API) and a local pure-Go parser covers a failed model call. Adopting that parser raised the module's minimum Go version to `1.24.1` (both Dockerfiles build on `golang:1.24-alpine`).

### Phase 3: Gemini Integration & ReAct Engine
- Implement `internal/llm/gemini.go` with Gemini 2.5 Flash Lite connectivity, function calling declarations, and structured output parsing.
- Implement `internal/agent/engine.go` to handle iterative thought-action-observation cycles.

### Phase 4: Domain Agents Implementation
- Implement `internal/agent/company.go` for org chart discovery and drill-down navigation.
- Implement `internal/agent/person.go` for disambiguation and candidate dossier compilation.

### Phase 5: CLI / API Interface & Verification
- Implement `cmd/server/main.go` as the HTTP gateway with embedded HTMX templates (`internal/web`), auto-escaped fragment rendering, health endpoint, and graceful shutdown.
- Add unit tests across tooling (scraper, serper, cookies), caching, LLM JSON cleaning, and template escaping.
- Containerize with a multi-stage `Dockerfile` ready for GCP Cloud Run deployment.

## Appendix: scraping trajectory (company vs person)

Written against the code at this revision; the function names are the anchors, so if a row
stops matching the code the code won that argument. Legend for **Bound**: *filter* = dropped,
never fetched · *budget* = time or count cap · *fallback* = what runs next on failure.

### A1. Company mode — `CompanyAgent.ResearchCompanyRun` → `scrapeRanked`

| # | Stage | What happens | Bound | On failure |
| :--- | :--- | :--- | :--- | :--- |
| 1 | Plan | Gemini plans queries → `enforceEntityQueries` drops unanchored/duplicate → `entityAnchoredQueries` refill | *filter*: a query must name the company, an alias, its domain or its registration id; cap 24; dedicated indexed-profile slots are reserved | `fallbackQueryPlan` |
| 1a | ↳ anchored set | `site:<domain>` ×3, name-anchored leadership/procurement, source-profile slots, role-specific LinkedIn slots, and three reserved person-of-interest queries when requested | Naukri has no dedicated company slot; Global mode uses its own source list | — |
| 2 | Search | Serper per query | `MaxSearchResults` per query; 48 h cache; URL dedupe | per-query failure tolerated |
| 3 | Rank | BM25 → top **12** semantically reranked by Gemini, 8 s deadline → `pruneUnconnected` | *filter*: no shared company token with the snippet ⇒ dropped (kept when there is no snippet); quality: aggregator 0.1, `linkedin.` 0.6, external 0.55, leadership 0.7, official 0.9–1.0 | BM25 order continues, marked `semantic skip` |
| 3a | ↳ relevance floor | `Score += 0.18·(quality−0.5)`; quality < 0.25 ⇒ `Relevant=false` | ***filter at fetch level***: deny-listed hosts are never downloaded; promoted aggregator snippets remain retained evidence | — |
| 4 | Gate tag | `EntityKey` per candidate (domain 0.95 / registration 0.9 / alias 0.8 / name 0.7) | counts reported | — |
| 5 | Select | ordered official_leadership → official_report → official_locations → official → leadership → industry_event → external; one page per domain unless official | `MaxScrapes × 2` candidates | — |
| 6 | Session fetch, then ladder pass (first) | `fetchProfileWithSession` tries one cookie-authenticated fetch when the jar covers the host (company mode included, `ALLOW_PROFILE_SCRAPING` regardless, and the flag is not consulted); otherwise `orderProfileEscalations` by shape priority post 0 / company 1 / other 2 / person 3 / directory 4 | *budget*: `HARD_URLS_PER_RUN` 2; 55 s per URL; needs budget + 20 s | session fetch ⇒ evidence; else recovered by the ladder ⇒ evidence; else `skipped` + public snippet retained (with `ALLOW_PROFILE_SCRAPING=true`, falls through to the batch instead) |
| 6a | ↳ steps | auth-walled + session ⇒ `cookie-render → extended-settle`; auth-walled, no session ⇒ `plain-render` only; public shape ⇒ `plain-render → [cookie-render] → extended-settle` | *filter*: the futile logged-out render is skipped for auth-walled shapes | exhausts ⇒ `blocked`; a redirect loop **with** a session ⇒ `session-rejected` |
| 7 | Batch scrape | batches of `MaxScrapes − usable + concurrency − 1` | *budget*: `scrapeCtx` = deadline − 55 s (`validationReserve` 25 + `synthesisReserve` 15 + `gapLookupReserve` 15); host limits | the batch loop stops at the reserve, keeping what it already fetched |
| 8 | Per page | usable = no error ∧ not `Blocked` ∧ > 80 chars ⇒ evidence (+ image OCR) | dedupe by URL, canonical URL, content hash | `Blocked` ⇒ one ladder escalation (`AlreadyRendered` when the per-run browser already tried); else `failed` + snippet |
| 9 | Snippet fallback | if evidence is empty, keep search snippets as evidence | capped at `MaxScrapes` | — |
| 10 | Gap lookups | `fillEntityGaps`: `"<Company>" <area> head director manager` for areas the evidence misses | *budget*: ≤4; needs > 25 s + 15 s; each bounded by headroom; only entity-matching results | nothing added, reported as 0 |
| 11 | Snippet filter | `filterEntitySnippets` — the company must appear inside the person's ~300-byte mention window | *filter* | dropped snippets counted |
| 12 | Synthesis | Gemini → `OrgChart` from evidence + filtered snippets + profile candidates + entity `promptLine` + directory claims | — | run fails (no fallback) |
| 13 | QC review | verdict + optional follow-ups | ≤2 follow-ups (3 iterations total); needs ≥60 s | recorded `unavailable` |
| 14 | Freshness pass | newest source per row ⇒ verified-recent / dated / stale / undated / conflicting; then ≤10 reverse lookups ranked no-key → conflicting → name-only → stale → undated → dated | windows 18 / 36 months; *budget* 25 s; cache keyed by **session fingerprint** | row excluded (`matched by company name only`) unless a lookup rescues it |
| 15 | Report + export | entity-gate table, scrape report, md + pdf, with planned-query evidence linked from the **Search results** tab | — | artifact written on a detached context after the deadline |


### A2. Person mode — `PersonAgent.ResearchPerson`

| # | Stage | What happens | Bound | On failure |
| :--- | :--- | :--- | :--- | :--- |
| 1 | Confirmation step | 2 core searches, candidates parsed from headlines, top match pre-selected | *budget*: 2 queries + **1 aggregator pass only when < 2 candidates**; company **and** place typed ⇒ 0 searches; no pre-selection when nothing names the typed company | 0 candidates ⇒ the step still renders the name-alone card |
| 2 | Plan | 6–9 anchored searches — profile, role and position, appointments and news, career and education, regional/global aggregator `site:`, plus one X/Twitter personalization search — deduped, capped 12 | one failed search is skipped | all planned searches fail ⇒ run fails |
| 3 | Search phase | every planned query through `SearchCached` with four bounded workers; results are processed back in plan order; employment snippets are identity-gated, while social snippets are kept in a separate public-personalization context | concurrency 4; cache, cancellation, and provider limits still apply | employment exclusions listed in the **Identity check**; social exclusions remain in the trace |
| 4 | Fetch targets | confirmed seed first; profile URLs from *every* planned search, then any gate-matched page, up to `MaxScrapes` 10; directory results are snippets-only | cap `MaxScrapes` 10 | non-matching hits stay snippets |
| 5 | Profile URL | **session for the host** ⇒ one cookie-authenticated `ScrapeCached`; otherwise ⇒ the ladder | usable = unblocked ∧ > 80 chars; ladder: 2/run, 55 s, needs budget + 20 s | cheap fetch ⇒ ladder ⇒ snippet; a redirect loop with a session ⇒ `session-rejected` |
| 6 | Other URLs | parallel `ScrapeCachedBatch`; a fetched body that names the person but states no employer is counted **name-only** | content-hash dedupe; host limits | `Blocked` ⇒ one ladder escalation |
| 7 | Identity check event | kept / name-only / excluded reported before synthesis | — | — |
| 8 | Synthesis | dossier from gated employment snippets/pages plus labelled public-personalization snippets, deterministic public-account candidates, and the confirmed-profile block; social context cannot establish identity or employment | — | run fails |
| 9 | **Dossier QC** | every synthesised experience row held to the confirmed employer; a non-matching row survives only when a bounded reverse lookup (`"Name" "That employer"`) ties them; the headline employer is corrected to the confirmed one; identical repeated rows collapse to one; two rows at different employers overlapping by more than a handover year are **both flagged** on the row (concurrent roles or a blended row) | lookups ≤ `VALIDATION_LOOKUPS_PER_RUN`, gated by `VALIDATE_EVIDENCE`, ≥ 15 s of run budget each; overlap threshold 2 shared years | dropped / corrected rows are listed in the **Identity check**; flags render on the rows |
| 10 | **Same-named people** | the excluded evidence is regrouped multilaterally — one group per employer a same-named person's source states, plus unattributed name-only material — ordered strongest-first, most relevant snippets per group | caps: 8 groups × 4 snippets (`person_gate.go`) | rendered in the dossier (console, md, pdf); the console offers a re-run per employer |
| 1 | RocketReach enrichment | When the operator explicitly runs a person dossier, use the confirmed LinkedIn URL when available, otherwise name + current employer; retrieve the structured profile, LinkedIn URL, and contact fields before web research and feed them into synthesis | one lookup per explicit person run; no lookup from company/related-entity discovery | provider failure is a warning; the dossier remains usable |
| 12 | Export | md + pdf, with **Identity check**, **Same-named people**, optional **Contact enrichment**, and **Research trace** sections; experience rows carry their source; planned-query evidence remains linked from the **Search results** tab | — | written on a detached context |

Not present in person mode: the freshness pass and image OCR, and the 55 s scrape reserve (its
bound is `RESEARCH_TIMEOUT_SECONDS` plus per-request timeouts). The entity gate's counterpart —
the person identity gate — runs at snippets, fetch selection, and the fetched bodies, and
**dossier QC** runs on the synthesised rows. Person mode is also where the session rules apply,
since profile URLs are its bread and butter.

### A3. Inside one fetch — `Scraper.ScrapeURL` (shared)

| Step | What happens | Bound |
| :--- | :--- | :--- |
| validate | http/https only; loopback, RFC1918 and metadata addresses refused; blocked-source list | hard |
| cache | `scrapeCacheKey(ctx,url) + sessionCacheSuffix` — mode `default`/`http`/`browser`, plus the session fingerprint when cookies apply | 7 d hit; negative 15 min; revalidate after 24 h (6 s conditional request) |
| politeness | per-host rate, burst, in-flight cap | 2/s, 3, 2 |
| fetch | retries with linear backoff, honouring `Retry-After` | attempts; 5 s cap on the hint |
| status | 304 ⇒ cached copy; 404/410 ⇒ terminal; **999 (bot-denial) ⇒ one request, no retries, reason `the site refused the request (HTTP 999)`, and the browser fallback is still allowed**; other ⇒ browser fallback | — |
| non-HTML | skip when `Content-Length` > 40 MiB (**terminal**, no render); extract ≤60 s; PDF ⇒ Gemini (≤8 MiB inline) ⇒ local parser (deadline between pages, partial text kept) | `DOCUMENT_EXTRACT_TIMEOUT_SECONDS` |
| HTML | extract; 2 MiB body cap; `content_truncated`; ~40 KB text cap | — |
| block detection | `Blocked` ⇒ per-run browser fallback when the toggle is on, else the blocked result is returned | — |
| ladder | **not** here: the agent calls `ScrapeHard` through the bounded handler | as A1 row 6 |

### A4. What an anonymous LinkedIn profile fetch actually produces

Measured with no cookies and no session, on `https://in.linkedin.com/in/<profile>`:

```
raw: status=999 final=…/in/<profile> bytes=1530
  wall markers: authwall=1, "sign in"=0, "join now"=0
  profile markers: parmar=0, experience=0
scraper: error: scraping failed after 3 attempts: scraping returned status code: 999
```

**999 is LinkedIn's bot-denial**, not a sign-in page a login would clear: 1.5 KB, no profile
content, served to a plain client whatever its intent. That is why a profile URL is worth
exactly one bounded ladder attempt (a render can win a *public post or company page*) and
otherwise its snippet — and why `ALLOW_PROFILE_SCRAPING=false` stays the default: opened up,
each profile would spend a batch slot on a single refused request and can consume a ladder
escalation a recoverable shape could have used, in exchange for nothing.

`fetchProfileWithSession` is the flip side of the same measurement: when the operator *does*
supply a session covering the host, both modes read the profile with one cookie-authenticated
fetch first, because that is the request that actually returned content (949 KB, no authwall).
The refusal above is no longer retried — one request, then the reason — which is why the
package reports `isBotDenialStatus` separately from `isRetriableStatus`.
