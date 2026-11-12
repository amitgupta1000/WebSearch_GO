# Handover Notes

## Current state

- Repository: `amitgupta1000/WebSearch_GO`
- Latest commit: `1677dae` — `Narrow company research focus defaults`
- Worktree was clean after the commit.
- Full Go test suite passed: `472/472` (now larger: WeeDoo browser search + DIN attribution tests).

## WeeDoo integration (related-entity + company modes), live-verified

- **Search-bar discovery** (`tools/weedoo.go`, `WeeDooBrowserClient`): WeeDoo is a
  client-rendered SPA the search provider barely indexes, so a CIN-less run resolves the
  company through WeeDoo's own search bar in the shared Playwright browser — the typeahead
  pairs each suggestion with the company's CIN (element-scoped extraction, so the homepage's
  showcase CINs are never attributed). Live: KLJ Resources Limited -> L67120WB1986PLC041487.
  Enabled via `agent.WithWeeDooSearch` in `cmd/server/main.go` (off by default so unit tests
  stay hermetic; `WEEDOO_ENRICHMENT=0` disables).
- **CIN attribution hardened** (`company.go`): a CIN is taken only when it sits in the URL as
  the company's slug page (`/company/klj-resources-ltd/<CIN>`) or is paired with the name
  inside a 100-char snippet window. This replaced token-anywhere matching, which had grabbed a
  subsidiary's CIN (U24110MH2005PTC152375) from a KLJ-named page.
- **Browser-rendered pages** (`FetchCompanyPageText`): WeeDoo's server-rendered pages are
  trimmed to ~1.5KB of navigation by the goquery extractor, so the registry enrichment reads
  them in the browser whenever the render yields more text (7560 vs 1522 chars live). The
  rendered registry page carries the full directors/DIN table.
- **Identity gates**: `containsCompanyIdentity` now uses the entity matcher (spelling variants,
  collapsed slugs like Klj-Resources-Ltd) instead of exact substring.
- **DIN resolution**: the directors table prints Appointment/Name/DIN/Designation rows, so
  `weeDooRegistryDIN` pairs a leader with the eight-digit run on their row (name must sit
  within 60 chars before the digits — flattened plain-fetch extractions otherwise pair every
  director with the first table DIN); `labelledDINFor` makes the labelled DIN extraction
  name-adjacent for the same reason. Masked rows resolve to nothing.
- **Per-leader director pages**: constructed /director/<NAME>/<DIN>, rendered in the browser,
  and moved to the FRONT of the per-leader evidence payload (`reorderWeeDooFirst`) so the
  bounded window always carries the other-directorship lists. Live: 8 director pages fetched
  with correct DINs; Dilip Kumar Karn's other companies flow into leadership-derived relations
  citing the WeeDoo page.
- Registry hits are also PREPENDED to the direct/leader extraction payloads (they were
  previously appended and could fall beyond the bounded-evidence count caps), and WeeDoo
  registry/director hits keep a 6000-rune snippet budget in `boundedRelatedEvidence` (the
  ordinary 1200-rune cap cut the directors table off).
- Known remaining tuning item: the affiliation extraction under-reports (emits a subset of the
  director-page companies; common-name leaders pick up namesake noise despite the prompt
  nudge).
- Probe scripts: `scripts/weedoo-browser-probe.py`, `weedoo-search-bar-probe.py`,
  `weedoo-page-text-probe.py`, `weedoo-director-probe.py`.
- **Deployed**: `deploy-gcp.ps1` built and shipped this to Cloud Run
  (`websearch-go`, asia-south1). Health-check the service on `/api/health` — Cloud Run's
  Knative queue-proxy reserves `/healthz` at the edge, so the container never sees it
  (fixed in `d49a27a` by adding the unreserved route).

## Company mode behavior

CompanyAgent now defaults to leadership-focused research:

- Leadership queries remain enabled by default.
- Location and company-context queries remain available for identity and background.
- Department/function searches are generated only when the user explicitly enters focus areas.
- The UI provides examples such as procurement, supply chain, and sales/marketing.
- The planner no longer adds default procurement, marketing, finance, engineering, or generic department searches.
- Indexed LinkedIn role searches are also limited to explicitly requested focus areas.
- Focus areas still steer which departments get dedicated searches, but people the collected evidence names (for example LinkedIn profiles) are included in the roster even when their function was not requested.

Relevant files:

- `internal/agent/company.go`
- `internal/agent/company_test.go`
- `internal/agent/sources_test.go`
- `internal/web/templates/index.html`

## Important finding

Search scope and output scope are separate.

The search planner can be narrowed while the final synthesis model still receives broad company evidence and may populate a full `departments` array. A deterministic post-synthesis department filter was tested, but rolled back at the user's request. Therefore, a full org chart can still appear even when the query plan is leadership-focused.

If output narrowing is revisited, decide explicitly whether to:

1. Keep the full org-chart schema but populate departments only from explicit focus areas; or
2. Return leadership plus company context by default and omit department output unless requested.

Any future output-scope change should be covered by synthesis-level regression tests, not only fallback-query tests.

## RocketReach and workflow context

- RocketReach discovery is opt-in in Company and Producer/Seller Landscape modes.
- Company RR discovery starts before web research.
- Company RR uses `current_employer` and `current_title`.
- Producer/Seller Landscape remains independent from CompanyAgent and is intended for broad company discovery and potential commercial contacts.
- CompanyAgent is reserved for deep, specific-company research.
- Company SSE terminal-session protection prevents completed logs from replaying on reconnect.

## Validation

The latest full validation command was the repository Go test suite, with all 472 tests passing. The focused CompanyAgent and web tests also passed before the full suite.

## Suggested next steps

1. Run a live Company-mode test with an empty Focus areas field and inspect:
   - planned queries,
   - selected evidence,
   - final department output.
2. Decide the desired output contract for default Company mode.
3. If departments must be absent by default, implement an explicit synthesis/output contract and add regression coverage around the final `OrgChart`, rather than filtering only after synthesis.
4. Preserve explicit focus-area behavior and confirmation-form propagation for RocketReach.
