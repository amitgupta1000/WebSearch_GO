# WebSearch_GO

Agentic web research server in Go. It runs autonomous research flows over two
tracks and synthesizes structured results with Gemini:

- **Company mode** — discovers leadership pages, builds an org chart with
  departments and locations.
- **Person mode** — disambiguates a person through the same confirmation step a company run
  uses: candidates come from profile headlines and pin the employer and place the run
  searches with (see [How a run works](#how-a-run-works))
  constraints and compiles a professional dossier.
- **Related Entity mode** — confirms a company, checks the usual sources for direct group or
  subsidiary relationships, then follows directors and C-suite leaders into other companies.

The HTTP layer serves an HTMX UI from templates embedded in the binary
(`internal/web`); the agents stream-step through search (Serper API) → scrape →
LLM synthesis, with TTL caching and bounded memory.

> **Documentation:** this file is the user-facing guide (setup, configuration,
> endpoints, policies). **[ARCHITECTURE.md](ARCHITECTURE.md)** is the
> internals companion — request lifecycle, module-by-module deep dive, testing, and
> known limitations — and **[SCRAPER_WALKTHROUGH.md](SCRAPER_WALKTHROUGH.md)** walks the
> fetch path (policy, rendering ladder, extraction) end to end.

## Prerequisites

- Go 1.24+ (local development)
- Docker (recommended for Cloud Run deployment)
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

On Windows, `ROCKETREACH_API_KEY` is read from the process environment first and
then from the User and Machine environment scopes when the process environment
does not contain it. Cloud Run continues to use the Secret Manager-injected
process environment value.

Tests that need external resources are skipped unless their gate is set, so the default
suite runs offline: `PLAYWRIGHT_INTEGRATION=1` enables the real-Chromium tests
(interstitial cookie reuse, and parsing a real generated PDF) and
`GEMINI_INTEGRATION=1` enables the live inline-PDF check. See
[ARCHITECTURE.md § Testing](ARCHITECTURE.md#testing).

For Windows development, run the auto-reloading server instead:

```powershell
.\scripts\dev.ps1
```

It watches Go and embedded template files under `cmd` and `internal`, then
restarts `go run ./cmd/server` after changes. The same watcher is available in
VS Code as the `Go: Watch Server` task. Because it restarts on *every* change, a
running research or question is cut off when you save a file — let a run finish,
or expect it to start over.
The watcher also refreshes `ROCKETREACH_API_KEY` from the Windows User or Machine
environment scope before starting Go, which handles terminals opened before the
key was added.

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

A run starts with the **confirmation step** — who or what is being researched — and only a
confirmed identity starts the pipeline. Either way it always asks, is one click (the top
match is pre-selected and focused), and never dead-ends:

- **Company** (`POST /api/resolve-entity`): candidate identities with domain, evidence and a
  registration id, plus the other candidates as *also part of this company* for a company
  that answers to several names or domains (see [Entity gate](#entity-gate--what-the-chart-is-held-to)).
  With `ROCKETREACH_API_KEY`, the company run makes a bounded set of RR title-group searches
  for key-person candidates across leadership and functional seniority lanes (including
  Manager, General Manager, Deputy General Manager, Vice-President, Additional Director, and
  Executive Director). It keeps only strict employer matches, adds those candidates to
  the existing company evidence gate, and exposes their query provenance; it does not request
  contact details.
- **Person** (`POST /api/resolve-person`): the people who share the typed name, read out of
  the `<name> - <role> - <employer>` headlines professional networks use, with the employer
  and place pre-filled from the profile the operator picks. That pinned context is what the
  run searches with — `"Name" Company Location` — so a dossier is about the right Sanjay
  Gupta rather than whichever one ranked highest, and the confirmed profile is read first as
  a seed source. A people-directory hit (RocketReach, ZoomInfo, SignalHire…) is offered but flagged as a
  claim rather than a profile, and if no profile is found the name-alone path is right there.
  At the start of the dossier workflow, an explicit person run performs one priority
  RocketReach lookup using the confirmed LinkedIn URL or name plus current employer.
  Its structured profile facts (current role, employer, location, career history, education,
  skills, LinkedIn URL, and contact details) are shown in the workflow, supplied to synthesis,
  and then merged through the normal identity QC; verified email/phone fields are rendered
  in the dossier and exports.
  Company and related-entity runs never request those fields.
- **Legal** (`POST /api/resolve-entity`, `mode=legal`): the same company-identity confirmation
  a company run uses — a legal check needs exactly the same "which company is this"
  discipline — plus an optional list of directors or key persons to check alongside it (see
  [Legal mode](#legal-mode)).
- **Related entities** (`POST /api/resolve-entity`, `mode=related_entity`): confirms the
  company, searches direct corporate relationships first, then searches up to eight
  company-linked leaders for external company involvement. It performs a cache-first preflight
  over the full plan and reports cache/result counts. Direct and leadership-derived
  relationships are reported separately and are never folded into the primary company identity;
  returned snippets remain visible for review when classification is inconclusive. This mode
  does not call RocketReach.

RocketReach is a paid enrichment provider and is intentionally limited to company discovery
and explicitly requested person dossiers. Search and lookup failures are non-fatal and are
reported as run warnings.

### Legal mode (Indian case-law check)

A legal run is a bounded litigation-exposure check on the confirmed company and the named
persons. It is deliberately smaller than the other modes: no planning model call, no scrape
ladder, no QC loop.

The run retains every relevant identity-matched finding for review, but its risk opinion applies
a post-retrieval filter: only cases dated within the previous ten years where the company is on
the responding side (defendant/respondent/accused) are sent to the risk assessment. Initiated,
older, undated, and person-party findings remain visible but are explicitly excluded from the
opinion.

1. **Identity** — the confirmation step pins the company (domain, aliases, CIN); the form's
   `Directors / key persons` field names the people to check.
2. **Search** — a deterministic plan spends at most `LEGAL_QUERIES_PER_RUN` searches,
   strongest first: the company name, the CIN (NCLT and ROC orders cite it, and a CIN in a
   document cannot belong to a same-named company), confirmed aliases, Casemine, then each
   person as `"Name" "Company"` co-occurrence.
   IndianKanoon (`indiankanoon.org`) is searched natively — its results are
   server-rendered HTML — with `doctype=judgments` so statute sections never crowd the
   party search, one request a second politeness, a robots.txt guard for its
   case-removal disallows, and a 48 h cache. Casemine (`casemine.com`) is searched
   through its native client, whose access ladder was measured: the site 403s every
   non-browser User-Agent (including headless Chromium's), but its own server-rendered
   results come back to the scraper's Chrome UA, so the plain fetch runs first and a
   cookie-bridged browser render (`COOKIE_FILE_PATH` session) is the fallback.
3. **Classify** — every judgment is tied to the identity deterministically, strongest key
   first: CIN in the record → exact legal name → confirmed alias → name-only. The case
   title's "Petitioner v. Respondent" structure is parsed deterministically too, so a
   **company initiating** an action (petitioner, complainant) is distinguished from one
   **responding** (respondent, defendant, accused) — the distinction that changes the risk
   reading, and it survives model outages because it never depended on the model. A Gemini
   pass then adds category (insolvency, sebi, tax, criminal, civil, arbitration, consumer,
   labour, other) and status (decided/pending); evidence is never lost for want of a
   classification. Reserved searches also scan SEBI, NCLT, NCLAT and IBBI order results.
4. **Fold case iterations** — the same matter travels through stages (trial, appeal,
   revision) and each stage is its own document. Records are grouped by case title with
   the date stripped, and the **latest dated record wins the row** — it is the matter's
   current state — with the identity class upgraded to the group's strongest and the row
   reporting how many earlier records were folded into it.
5. **Confirm** — up to `LEGAL_DOCS_PER_RUN` IndianKanoon documents are fetched in full to
   firm up the weakest matches (a CIN or full name in a judgment body upgrades a
   name-only guess) and to fill in missing court/date.
6. **Group and assess risk** — classified findings are grouped into deterministic
   company-law, criminal, tax and other dockets. Only recent company-law matters where
   the company is clearly responding contribute to the overall risk level; all findings
   remain visible in their dockets, with pending and respondent counts.
7. **Report** — dockets, matched findings, same-named hits *kept and labelled* rather than dropped
   (a legal check must not silently discard a hit that merely shares a name), per-person
   results, and a source line that names what each source actually answered, so "no
   findings" is never ambiguous between "none exist" and "we could not look".

### Directors discovery (legal mode toggle)

The legal form's **"Discover the company's directors & key persons"** checkbox turns on a
discovery pass between the company track and the person track:

1. The run spends up to two of its remaining queries on registry snippets — a generic
   `"Company" directors list` and a `site:zaubacorp.com` query.
2. Only snippets that name the confirmed company (the same identity keys the entity gate
   uses) can nominate a person — the company-linked rule, so a different Acme's board
   never enters the run.
3. Gemini extracts person names from the company-linked snippets; the names then pass
   deterministic gates (multi-token, not the company itself, deduplicated, capped at four)
   before any search is spent on them.
4. Discovered persons are searched exactly like operator-named ones, each as
   `"Name" "Company"`, within the remaining budget; a person the budget never reached is
   listed as *not searched* with how they were discovered. The report marks them
   **discovered**, and the pass reports its own queries and yield as a source line.

Exports write a Markdown document and a PDF under the `legal/` prefix, plus the run's
`-searches.json` trace.

```bash
curl -X POST "http://localhost:8080/api/research?sync=1" \
  -d "mode=legal" -d "query=Acme Robotics Pvt Ltd" \
  -d "legal_persons=N. Chandrasekaran"
```

Then:

1. **Plan** — Gemini proposes prioritized search queries (with a deterministic
   fallback plan if planning fails or times out).
2. **Search** — every query runs through Serper; results are deduplicated across
   queries and cached for 48 h.
3. **Rank** — BM25 scores every snippet immediately, then Gemini semantically reranks
   the top twelve candidates within an eight-second deadline; if that call times out or
   returns invalid URLs, the BM25 order continues straight to scraping. Candidates
   showing no connection to the target company — no official-domain match and no
   distinctive company-name token in the title, snippet or URL — are dropped, so other
   companies' profiles and off-domain pages no longer consume scrape budget. The filter
   never empties the candidate list.
4. **Triage document candidates** — PDF-looking candidates receive a two-stage conservative
   Gemini classification before full download: fast metadata triage from search title, snippet
   and URL, followed by a bounded preview fetch (initial 512 KiB / first 3 pages) for
   uncertain candidates. Only `relevant` decisions with confidence ≥ 0.80 and matching entity/topic
   signals proceed; `not_relevant`, persistently `uncertain`, malformed, and failed classifications
   are skipped and recorded in the scrape report. Non-document pages keep the normal path.
5. **Scrape** — ranked pages are fetched, cleaned and cached for 7 days. Pages are fetched
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

### Scraping trajectory at a glance

The full decision-by-decision version — every filter, cap and fallback, with the function
names — is the [scraping-trajectory appendix](ARCHITECTURE.md#appendix-scraping-trajectory-company-vs-person).
Compactly:

**Company run**

| Stage | Gate that can stop it | What happens when it does |
| :--- | :--- | :--- |
| Plan | unanchored query, cap 24, dedicated indexed-source and person-of-interest slots reserved | deterministic `fallbackQueryPlan` |
| Rank | no shared company token (`pruneUnconnected`); source quality < 0.25 (deny-listed hosts) | candidate dropped — **never fetched** |
| Profile pass | `HARD_URLS_PER_RUN` 2, 55 s per URL, needs budget + 20 s | `skipped` + the public snippet is kept |
| Batch scrape | `MAX_SCRAPES_PER_RUN` 10; fetches stop 55 s before the deadline | pages already fetched are kept |
| Page | `Blocked` ⇒ one ladder escalation | else `failed`, snippet retained |
| Synthesis | — | run fails (no fallback) |
| QC | 2 follow-ups max, needs ≥60 s | result returned as-is |
| Freshness | 10 reverse lookups, 18/36-month windows | historical or current-unverified row excluded from the current chart and listed in **Excluded from the current org chart** |

**Person run**

| Stage | Gate | Fallback |
| :--- | :--- | :--- |
| Confirmation | 2 searches + 1 aggregator pass only when <2 candidates | name-alone card |
| Plan | 6–9 anchored searches — profile, role, appointments, career/education, regional/global aggregator `site:`, plus one X/Twitter personalization search, capped at 12 | a failed search is skipped; the run fails only when every one does |
| Snippets | **identity gate**: a snippet must name the person *and* the confirmed company or location | excluded — listed in the dossier's **Identity check** |
| Fetch targets | identity gate first: profile URLs from *every* planned search, then any gate-matched page, up to `MaxScrapes` 10 | excluded, with the stated employer in the reason |
| Profile URL | session ⇒ **one cookie-authenticated fetch**; otherwise the ladder (2/run, 55 s) | ladder ⇒ snippet |
| Other URLs | content-hash dedupe | `Blocked` ⇒ one ladder escalation |
| Directory snippets | the aggregator `site:` query is snippets-only, never fetched | a directory naming a *different* employer is dropped |
| Synthesis | one-person instruction in the prompt; every experience row must cite the `source` URL that supports it; empty over invented | — |
| **Dossier QC** | a synthesised row whose employer is not the confirmed one survives only when a reverse lookup (`"Name" "That employer"`) ties them; the headline employer is corrected to the confirmed one; identical repeated rows collapse to one; two rows at different employers whose timelines overlap by more than a handover year are both **flagged** (concurrent roles or a blended row — never dropped) | dropped / corrected rows listed in the **Identity check**; overlap flags rendered on the rows; lookups capped by `VALIDATION_LOOKUPS_PER_RUN`, gated by `VALIDATE_EVIDENCE` |
| **Same-named people** | the excluded evidence is regrouped multilaterally — one group per employer a same-named person's source states, plus the unattributed name-only material — and the most relevant snippets per group are shown in the dossier | rendered in console, md and pdf; each employer group in the console carries a **Research this person instead** action |

The identity gate is person mode's counterpart of the company entity gate: the company and
location confirmed on the card are acceptance rules, not just search terms — that is what keeps
a same-named person at another employer out of the dossier. The plan replaced three hardcoded
searches with eight anchored ones (nine cap), including separate public-personalization
searches for X/Twitter, because the old budget bought one broad
question and two afterthoughts. A snippet is held harder than a fetched page (a snippet that names the
person but states no employer is dropped; a fetched page that does is kept but counted as
*name-only*), because a career history legitimately lists past employers in its body while a
snippet is a free-floating assertion. Social snippets are kept in a separate context and cannot
establish identity, employment, title, or sensitive attributes. Dossier QC is the post-synthesis half of the same rule:
the model's rows are held to the confirmed employer, with a bounded reverse lookup rescuing
genuine past employers. The **Same-named people** section is the multilateral half of the same
learning: a person search answers with several people who share the name, so the evidence the
gate excluded is regrouped by the employer each source states — one group per other person,
plus the unattributed material — and shown with its most relevant snippets instead of being
discarded. A name-alone run has nothing to hold sources or rows to and excludes
nothing. Person mode still has no freshness pass and no image OCR; company mode is where the
55 s scrape reserve protects the lookups and synthesis from a slow scrape.

## Environment variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `PORT` | `8080` | HTTP listen port |
| `GEMINI_MODEL` | `gemini-2.5-flash-lite` | Gemini model name |
| `SERPER_API_KEY` | — (required for search) | Serper.dev API key |
| `ROCKETREACH_API_KEY` | — (optional) | Enables bounded company/related-entity profile discovery and explicit person-dossier contact enrichment. Search discovery does not request contact details; lookup failures are non-fatal. |
| `GOOGLE_API_KEY` | — | Gemini API key (AI Studio) |
| `GOOGLE_GENAI_USE_VERTEXAI` | unset | Set to `true` to use Vertex AI backend |
| `GOOGLE_CLOUD_PROJECT` / `GOOGLE_CLOUD_LOCATION` | — | Vertex AI project & region |
| `GCP_REGION` | `us-central1` | Region recorded in config (informational; Vertex AI uses `GOOGLE_CLOUD_LOCATION`) |
| `COOKIE_FILE_PATH` | — | Optional JSON session export for the cookie-bridged ladder step and for plain fetches of logged-in-only pages. Both shapes are accepted: a browser extension's array (Cookie-Editor / EditThisCookie) and Playwright's `storage_state.json` (`{"cookies": […]}`), with `expires` as a unix timestamp or an RFC1123 string. A cookie scoped to `www.example.com` is also registered for `example.com`, so `in.linkedin.com` receives a session exported for `www.linkedin.com`. Startup logs what loaded (`Cookie jar: 7 cookies from …`) |
| `ENABLE_BROWSER_FALLBACK` | `false` | Enable bounded Playwright rendering after HTTP errors, challenges, or low-quality JavaScript shells |
| `CACHE_MAX_ENTRIES` | `2048` | Max entries in the in-memory TTL cache |
| `RESEARCH_TIMEOUT_SECONDS` | `180` | Per-request research budget |
| `BROWSER_RESEARCH_TIMEOUT_SECONDS` | `300` | Per-request research budget when a run renders pages in a real browser (clamped to at least `RESEARCH_TIMEOUT_SECONDS`) |
| `LEGAL_TIMEOUT_SECONDS` | `180` | Per-request budget for a legal mode run (floored at `RESEARCH_TIMEOUT_SECONDS` so the HTTP write timeout can never cut a legal stream off) |
| `LEGAL_QUERIES_PER_RUN` | `16` | Max legal searches per run across all sources and parties (company, CIN, aliases, Casemine, persons) |
| `LEGAL_DOCS_PER_RUN` | `8` | Max IndianKanoon documents fetched in full per legal run; findings otherwise stand on their search-page snippets |
| `LEGAL_PAGES_PER_QUERY` | `8` | Adaptive legal-source ceiling; the walk stops early on empty/repeated pages or pages with no new entity-relevant hits. IndianKanoon paginates natively (`&pagenum=`); Casemine is a measured single-page source. This does not limit normal company/person domain searches |
| `LEGAL_CONCURRENCY` | `6` | How many legal searches and document fetches run at once. Each source's own politeness limiter still governs its request rate — the pool is what lets an IndianKanoon wait overlap a Casemine fetch instead of queueing behind it |
| `MAX_SCRAPES_PER_RUN` | `20` | Max web pages scraped per research session |
| `MAX_SEARCH_RESULTS` | `20` | Max search results fetched per search query |
| `MAX_AGENT_STEPS` | `20` | Max agent/tool iterations per research session |
| `ALLOW_BROWSER_TOGGLE` | `true` | Offer a per-run "render pages in a real browser" option in the console. Only shown when Chromium can actually start |
| `ALLOW_PROFILE_SCRAPING` | `false` | Fetch professional-profile pages (e.g. `linkedin.com`) directly instead of keeping only their public search snippet. Opt-in, because that is a policy decision |
| `HARD_URL_HANDLER` | `true` | Escalate blocked pages and professional profiles to the bounded hard-URL handler (plain render → cookie-bridged render → extended settle) |
| `HARD_URLS_PER_RUN` | `2` | Hard cap on how many URLs one run may escalate. Raise it for profile-heavy briefs, but remember each escalation can take up to `HARD_URL_BUDGET_SECONDS` |
| `HARD_URL_BUDGET_SECONDS` | derived | Budget for the whole ladder on one URL. `0` derives it from the ladder's own step caps (15 s + 15 s + 20 s + margin), so the extended-settle step can never be clipped |
| `VALIDATE_EVIDENCE` | `true` | Date and label every person row from the sources behind it (`verified-recent` / `dated` / `stale` / `undated` / `conflicting`) and allow bounded reverse lookups. The offline half costs nothing; `0` keeps the labels and spends no searches |
| `ENTITY_GATE_MODE` | `enforce` | How strictly sources are held to the confirmed company: `enforce` removes off-entity, historical, retired, deceased, or current-unverified rows and lists them in the report, `report` labels them but keeps them, `off` disables the gate entirely for comparison runs |
| `VALIDATION_LOOKUPS_PER_RUN` | `10` | Cap on reverse lookups — `"Name" "Company"`, snippet-only, never fetched. The policy is deliberately aggressive: anything not already current *and* tied to the company by a domain, alias or registration id gets one, because a lookup is what rescues a person no collected source could tie to the company. `0` disables them but keeps the labels |
| `VALIDATION_STALE_AFTER_MONTHS` | `36` | Age at which a row's newest source is reported as `stale` |
| `VALIDATION_RECENT_WITHIN_MONTHS` | `18` | Age inside which a dated source from the company's own domain, or from two independent domains, counts as `verified-recent` |
| `CACHE_BACKEND` | `redis` | Where cached searches and scraped pages live: `redis`, `gcs` or `memory` |
| `REDIS_URL` | — | Redis connection URL (`redis://` / `rediss://`). Takes precedence over the split settings below |
| `REDIS_HOST` | — | Redis host; with `REDIS_PORT` / `REDIS_PASSWORD` it is assembled into a connection URL when `REDIS_URL` is unset |
| `REDIS_PORT` | `6379` | Redis port |
| `REDIS_PASSWORD` | — | Redis password (URL-escaped when the connection URL is assembled) |
| `REDIS_USE_SSL` | unset | Optional override for the Redis URL scheme: `true` forces TLS (`rediss://`), `false` disables TLS (`redis://`); unset honors the scheme already stored in `REDIS_URL` |
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
| `DOCUMENT_EXTRACT_TIMEOUT_SECONDS` | `60` | How long one document's text extraction may take before it is reported as failed and the run continues |

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
- **Search results** (`<run>-searches.json`) accompany every run: every query it issued, with
  each hit split into what the identity/entity gate kept and what it excluded. The console's
  **Search results** panel previews it per query and links the download; the file is written
  regardless of the configured output formats, because the rendered artifacts quote the
  findings while this one carries the findings themselves.
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
| `redis` (default) | yes | yes | Native per-key TTL. Connect with `REDIS_URL`, or with `REDIS_HOST` / `REDIS_PORT` / `REDIS_PASSWORD` — the URL is assembled for you and the password is URL-escaped |
| `gcs` | yes | yes | One object per entry with its expiry embedded; credentials from `GOOGLE_APPLICATION_CREDENTIALS` or an ambient identity. Set `GCS_CACHE_BUCKET`, and pair it with a bucket lifecycle rule to reclaim expired objects |
| `memory` | no | no | Bounded FIFO fallback (2048 entries by default); useful for local development |

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

**Confirm the entity first, every time.** A company run now goes through a confirmation
step: submit the form (or press Enter) and the console shows candidate identities — domain,
evidence, and a registration number (a CIN read out of a registry page, shown with the page
it came from). The top match is pre-selected and focused, so confirming is one click.
Person-mode research has a separate confirmation step that resolves people who share a name
and pins the selected employer and location before the dossier run starts.

- **Two names, one company.** Each card offers the other candidates as *also part of this
  company*: tick one and its domain joins the identity's domains and its name joins its
  aliases, so the gate accepts a source naming any of them. That is how a corporate site
  plus a brand site, or a registered name plus an operating name, become one entity.
- **A different CIN is a different company.** If a ticked candidate carries a registration
  id that contradicts the card's, the run is refused rather than merged, naming both ids —
  the gate's whole value is that a human confirmed exactly which company this is. Clearing
  the registration id is the deliberate way to say "one identity anyway".
- **It costs nothing when you already know the answer.** Type the domain and the step
  spends no search at all: it renders that domain (normalized, so a pasted URL works) as the
  single pre-filled candidate.
- **A failed lookup is not a dead end.** When no domain can be identified, the step says so
  and still offers *Research by name only* — the run is gated on the name alone, and the
  artifact reports that.
- Candidate domains that do not carry the company's name are flagged, because gating a run
  on a rating agency's domain would be worse than gating on none.
- A confirmed pick is recorded as `entity_source=resolved`, so an artifact never presents a
  pointed-and-clicked identity as a typed one.

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
   Current-employment validation is separate from entity matching: a historical company
   association is not treated as current merely because no newer employer is found. Explicit
   former, retired, or deceased evidence is excluded, and an old association with no positive
   current evidence is marked `current-unverified`. In enforce mode these rows are removed
   from the current chart but listed in the report's **Excluded from the current org chart**
   section with their source and reason. Report mode keeps the rows visible with the same
   labels for tuning and review.
5. **Reverse lookup before exclusion** — every row that is not already current *and* tied
   by a domain, alias or registration id gets one snippet-only `"Name" "Company"` search
   first. If a result names both, the person is **kept** and the rescue is recorded
   (`rescued by lookup`, with the confirming source). Live case: a CFO whose only collected
   source was a Datanyze name-and-title page was dropped by the gate; one lookup naming
   both him and the company keeps him. The lookup order is by how much one search can fix:
   a row with *no* company link at all first (that is the rescue case), then a title two
   people claim, then a name-only match, then stale, then undated, then merely dated.
   `VALIDATION_LOOKUPS_PER_RUN` (default 10) caps them, and when the cap is reached the
   artifact says how many people were **left unchecked**, so a truncated pass is visible
   rather than silent.
6. **Anchored lookups before synthesis** — the same policy applied proactively: for the
   functions the evidence does not cover (leadership, procurement, supply chain, finance,
   marketing, up to four per run) the agent runs `"<Company>" <function> head director
   manager` before the chart is built, and keeps the results that name the company. This is
   what fills a thin chart with company-anchored sources instead of leaving a department
   empty. The artifact reports the count and the sources it added.
   - **Both lookup passes are protected by a reserve.** Fetching runs on a context that
     stops `scrapeReserve` (55 s: 25 s validation + 15 s synthesis + 15 s lookups) before
     the run's deadline, and each pass bounds its own work against the others' reserve. This
     is measured, not theoretical: one live run spent 158 s of a 180 s budget scraping and
     reported `no run budget left for reverse lookups` — 0 lookups, 15 rows unchecked, two
     named directors excluded for lack of a source. After the reserve that run's successor
     performed **10 lookups and kept 12 people who otherwise had no company-named source**,
     including both directors.
7. **Person-scoped mentions** — a company mention only supports a row when it appears
   within ~300 characters of the person's name. A whole-page co-occurrence is not evidence
   about anyone: a MoneyControl IPO story about another company contains "Sanjay Chemicals"
   twice, and an Indian Kanoon judgment four times, and both used to enter the source list.
8. **Job boards and content farms are never sources** — naukri, shine, iipmr, scribd,
   ambitionbox, indiamart, cphi-online, document dumps and social networks rank with the
   aggregators, below the relevance floor, so they are not fetched at all.
9. **QC** — the reviewer is told the exact identity and must report anyone the evidence
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
  - **Making a logged-in-only page readable.** The startup line
    `Cookie jar: no COOKIE_FILE_PATH set …` means no session was supplied, so an authwalled
    page can only ever contribute its snippet. To supply one:
    1. Capture a logged-in session, either way:
       - **Playwright** (no extension needed): `npx playwright open https://www.linkedin.com/login`,
         log in, then close the window — `--save-storage=linkedin.json` on that command writes
         the storage state.
       - **Browser extension**: log in, then Cookie-Editor → *Export* → *JSON* for the domain.
    2. Save the file **outside the repo** (e.g. `%USERPROFILE%\.wsgo\linkedin.json`) and set
       `COOKIE_FILE_PATH` to it.
    3. Restart and check the startup line — it must list the domain and a count
       (`Cookie jar: 12 cookies from … : linkedin.com (7), www.linkedin.com (5)`). A file that
       loads nothing logs `produced no usable cookies` with the skip count.
    Cookies ride the plain HTTP fetch as well as the ladder's browser context, so a valid
    session often makes a profile readable without a render at all, and `ALLOW_PROFILE_SCRAPING`
    is not required for that.
    - **Aggregator domains stand in for a LinkedIn profile.** LinkedIn person profiles cannot be
  read without a session (and a session stops working almost immediately), so the query
  builders now search the people-data aggregators for the title-and-employer fact a dossier
  needs:
  - **Company mode** adds one anchored query, `"<Company>" (site:crunchbase.com OR
    site:clearbit.com OR site:apollo.io OR site:hunter.io)`. **ZoomInfo is deliberately absent**
    from that list: it is on the company deny-list (`lowQualityHosts`), so a query for it would
    return pages the gate discards before fetching. The list is derived from the deny-list at
    runtime, so it cannot drift.
  - **Person mode** searches the people-directory set — a directory page is a claim about a *named individual*,
    which that path already labels as "a claim to check" — as an extra pass that is only spent
    when the two core searches come back thin, plus one such query during the research run
    itself.
  - Those results reach synthesis under **DIRECTORY CLAIMS**, labelled as a stated title and
    employer rather than verified employment, and mostly contribute snippets: these sites
    paywall or JS-wall their data, so a snippet is usually all there is. Treat them as leads
    for the corroboration the pipeline already applies (entity matching, proximity, freshness
    verdicts).
    - **A session is a per-run asset, not a durable credential.** Measured on a live LinkedIn
      export: one profile read returned the real page (949 KB, no authwall), and the next
      request — the same URL a minute later — came back as a redirect loop. Four rules follow
      from that measurement, and all four are implemented:
      1. **The cheap path is tried first.** For a profile URL, a session-bearing run makes one
         cookie-authenticated HTTP fetch before it considers a render — in person mode too,
         which previously only ever rendered profiles even though the cheap fetch is what
         works. With no session, behaviour is unchanged: straight to the ladder, because an
         unauthenticated authwall fetch buys nothing.
      2. **A session leads the ladder when one applies.** For an auth-walled shape the
         cookie-bridged render is the first step and the plain render is dropped, since time
         cannot clear a sign-in page — that futile step was what triggered the redirect loop
         in the live test.
      3. **The cache is scoped to the session.** A logged-out failure is negative-cached for
         15 minutes; that entry is no longer served to the run for which you have just
         captured a session, because the same URL genuinely answers differently.
      4. **A refused session is named as such.** A redirect loop while a session was being
         sent is reported as `session-rejected` — "re-capture it; this page falls back to its
         public snippet" — instead of a generic ladder failure, so you do not debug a site
         that is working. A redirect loop *without* a session stays an ordinary site failure.
      Treat a capture as good for the handful of profile URLs a run actually needs, capture it
      fresh (right before the run that needs it), and expect to re-capture rather than persist.
    - Two honest caveats. **Sessions expire** and are invalidated by logout, password change
      or a security checkpoint, so the file needs re-capturing; an "empty" jar is nearly always
      an expired one. And **authenticated automation is against LinkedIn's terms** and can get
      the account restricted — use an account you can afford to lose, keep volume low, and
      expect a CAPTCHA to end the run's access to that page regardless (nothing here solves a
      challenge). Registry filings (ZaubaCorp/Tofler), company sites, news and conference bios
      remain the sources that need no session at all.
  - **Why `ALLOW_PROFILE_SCRAPING` is off.** Measured without a session,
    `https://in.linkedin.com/in/…` answers **HTTP 999** — LinkedIn's bot-denial — with 1.5 KB
    and no profile content. That refusal is no longer retried: it is reported as
    `the site refused the request (HTTP 999)` after **one** request, while a render can still
    be attempted for a public page on the same host. Opening the flag would spend a batch slot
    per profile on that refused request and can consume a ladder escalation a recoverable shape
    (a public post or company page) could have used, in exchange for nothing. The flag exists
    for the opposite case: fetching profiles through the batch *without* a session. When a
    session **is** supplied, both modes read the profile with one cookie-authenticated fetch and
    the flag is not consulted — a session is explicit consent for that host, and one
    authenticated GET is less intrusive than the logged-out render the default already performs.
  - **What it costs, concretely.** With the flag off, a profile URL costs one bounded ladder
    attempt (~15 s) and otherwise contributes its public snippet. With it on and no session, it
    costs a batch slot for one refused request — for the same snippet.
  - **Raising `HARD_URLS_PER_RUN` buys attempts, not results.** With the cap at 2 vs 6 in
    an A/B on the same brief, more escalations added prose and two office locations but
    no new people, at +67 % scrape time. Keep it low and spend the budget on the run.
- **PDFs are read; every other non-HTML response is rejected.** A PDF is sent to
  Gemini, which reads PDFs natively, and falls back to a bundled pure-Go parser when
  that call fails — so officer lists published as registry filings become evidence. The
  scrape report shows which path ran (`pdf-gemini` / `pdf-local`). Images, archives and
  other binary payloads still fail with `unsupported content type`, and text that comes
  back as machine noise rather than words is discarded instead of being quoted.
- **Ingesting a document is bounded in time, not just in size.** This is the guardrail
  against a workflow that simply stops: one very large filing must never hold a run while
  nothing else progresses. Three limits apply — the declared size (`Content-Length` above
  40 MiB is refused *before* the transfer, and a render cannot fix that, so it is reported
  rather than handed to the browser), the extraction budget
  (`DOCUMENT_EXTRACT_TIMEOUT_SECONDS`, default 60 s, after which the document is reported as
  failed and the run continues), and the parser's own page/text ceilings. The pure-Go parser
  honours the deadline *between pages* and hands back what it read, which the local fallback
  keeps — a filing whose first pages carry the roster is worth more than nothing, and the
  shortfall is logged rather than presented as the whole document. On the model side each
  verbatim-extraction request also carries its own 60 s deadline.
- **HTML bodies are capped at 2 MiB, PDF documents at 8 MiB, and extracted text at ~40 KB.** A body that hits the
  cap is parsed from an incomplete document and flagged as `content_truncated`. Multi-page PDF extractions include page markers (`--- [Page N] ---`) so evidence can cite specific pages.
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
| `POST` | `/api/research` | Start research: `mode` (`company`/`person`), `query`, `company_name`, `location`, optional company-brief scope fields, and optional `use_browser` (`1`/`0`) to render this run's pages in a real browser. Returns the live workflow console, or the final result fragment with `?sync=1` (used by the CLI and the org-chart drill-down). Full field list: [HTTP API reference](ARCHITECTURE.md#http-api-reference). |
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
[ARCHITECTURE.md](ARCHITECTURE.md) for the system design.

### Cloud Run

Cloud Run is the production host for the Go HTTP server; Cloud Storage stores exports
and can optionally store the cache. Cloud Storage does not execute Go binaries.

```bash
export PROJECT_ID="your-gcp-project"
export REGION="us-central1"
export REPOSITORY="websearch"
export IMAGE="${REGION}-docker.pkg.dev/${PROJECT_ID}/${REPOSITORY}/websearch-go:latest"

gcloud auth configure-docker "${REGION}-docker.pkg.dev"
gcloud builds submit --tag "${IMAGE}" .
gcloud run deploy websearch-go \
  --image "${IMAGE}" \
  --region "${REGION}" \
  --platform managed \
  --allow-unauthenticated \
  --env-vars-file deploy/cloud-run.env.yaml \
  --set-secrets "GOOGLE_API_KEY=google-api-key:latest,SERPER_API_KEY=serper-api-key:latest,REDIS_URL=redis-url:latest"
```

`deploy/cloud-run.env.yaml` contains the non-secret runtime configuration and is
safe to keep in source control after replacing the `REPLACE_WITH_*` bucket/project
values. Create the referenced Secret Manager secrets and grant the Cloud Run runtime
service account `roles/secretmanager.secretAccessor` before deploying. The secret
values must not be placed in the YAML file, Dockerfile, or image arguments. The
browser-enabled image can be built with `Dockerfile.browser` when browser fallback
is required, but it is larger and has slower cold starts.

The current UI is server-rendered Go templates and HTMX, so it can be served directly
from Cloud Run. GCS artifacts are downloaded through the authenticated `/api/exports`
Cloud Run route rather than unsigned `storage.googleapis.com` links. Firebase Hosting
is only needed for a separately built static frontend; in that architecture, configure
Firebase rewrites for `/api/**` to the Cloud Run service and keep the authenticated
artifact route on Cloud Run.

### Python companion dependencies

`requirements.txt` applies only to the standalone `react_engine.py` demo/learning
companion. The Go server is built from `go.mod` and `go.sum`; Python dependencies are
not installed into either production Docker image.