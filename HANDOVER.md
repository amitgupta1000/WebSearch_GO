# Handover Notes

## Current state

- Repository: `amitgupta1000/WebSearch_GO`, branch `main`, pushed to `origin/main`.
- Latest commits: the "trade based search made explicit" series (`a2a8a5b` and the docs commit
  that carries this file) â€” see `git log --oneline -5` for the exact chain.
- Worktree clean after the docs commit.
- Full Go suite: **634 tests passing, 0 failing** (`go test ./... -count=1`), `go vet` and
  `go build` clean.
- Live-verified this session: three full Buyer Landscape (`demand_chain`) runs on the sample
  query `"n-butanol" "India"` (`TestDemandChainLive`, deep budget). The run logs were removed
  from the repo in the 2026-11-17 artifacts cleanup; artifacts under `%TEMP%\crystal-live-nbutanol*`.

## 2026-11-17 — adversarial query review (Seller Landscape)

The operator reviewed the search strings a "Oil Blending" MEA run issued and the verdict was:
half the discovery spend bought no marginal recall (self-repeating lanes, region-collision lanes,
activity-anchored trade lanes, unnatural role clusters). Instead of hardcoding better lanes, the
pipeline now reviews its own queries adversarially before any search runs:

- **New `internal/agent/market_query_review.go`.** Layered review: deterministic guards first
  (self-repetition, vocabulary-subset redundancy within the same `site:` class, unknown/doubled
  `site:` hosts, word caps), then one bounded LLM call that keeps, rewrites or drops every query.
  Rewrites are sanitized hard: requested-geography tokens only (region vocabulary expands, so a
  Middle East run may narrow to UAE but never widen to India), one `site:` host max from the
  trade-platform + ensun/revenuebase allowlist, no repeated content words, same-lane vocabulary
  shared with the original. Fail-open: on model error the original queries run and a warning is
  recorded. Verdicts land on the report as `QueryReviews` (rewrites/drops only — unchanged
  queries stay auditable through `SearchQueries`) and render in the Markdown export.
- **Wired into all three query sources**: discovery lanes (phase `discovery`), the shared
  category-lane runner (phase `category`, also exercised by the buyer landscape through
  `DemandChainAgent.researchCategoryLane`), and the per-company verification purposes (phase
  `verification`, reviewed once before any lane runs; `verifyMarketLeads` now takes its purposes
  as a parameter). A rewrite of "registry trade source"-style purposes replaces the template for
  both verification stages.
- **Category lanes run once per requested geography** (`marketSearchGeographies`), killing the
  `oleochemicals manufacturers Middle East Africa` concatenated-region soup; the orphaned
  `marketGeography` helper is gone.
- **Process activities expand to natural indexed phrases** (`marketActivitySearchTerms`): an
  activity containing "blend" also searches as `lubricant blending` — the phrase the trade
  actually indexes — and `fuel oil blending` joins only when the activity names fuel. This
  implements the intent of `TestMarketDiscoverySearchesUseNaturalProcessTerms`, which had been
  failing at HEAD since the `e5136b4` composition rework.
- Tests: `internal/agent/market_query_review_test.go` (merge rules, refused unsafe rewrites,
  fail-open, mechanical redundancy + site-class separation, per-region category lanes).

**Buyer alignment (same day).** The buyer landscape now runs the same discipline:

- The engine-neutral `reviewQueryBatch` serves both agents. Buyer discovery expands its lanes
  across the requested geographies (`demandLaneRuns`, lane-major) *before* the budget selects
  among the runs, so `DiscoveryQueries` still bounds the spend; a region-scoped lane
  ("Middle East importer tender procurement") runs only under the region it names and never gets
  its own region appended again (`demandLaneSearchQuery`). The expanded runs pass through the
  review, and surviving runs map back to their lanes by original query text — stage attribution
  and `laneStages` are untouched by a rewrite.
- `demandGeography` — the tail on gap and verification queries — alternates a multi-region scope
  with OR (`"Middle East" OR Africa`) instead of concatenating it; the report header shows the
  comma-joined scope via the new `demandGeographyDisplay`.
- The buyer's verification template (tender / import / own-site phrases) is reviewed once per run:
  a rewrite replaces the phrase everywhere via `applyDemandPurposeRewrites`; a drop is recorded on
  the report but the claim-conditioned ordering keeps all three kinds. The shared sanitizers are
  connector-aware so compound phrases ("import data OR importer of OR customs shipment") can be
  rewritten without tripping the repeat-word guard.
- The category-lane delegation no longer discards verdicts: `DemandChainReport` gained
  `QueryReviews`, populated from the buyer's own lane review, the purpose review and the shared
  category runner, and rendered in the buyer Markdown export.
- Tests: `internal/agent/demand_query_review_test.go` (region-collision composition, geography
  plumbing, purpose rewrite contract, and a full-Research pin: per-region execution, a reviewed-
  away run that never executes, the rewritten importer phrase in verification, no region soup).

**Verification before ship (same day).** Two layers, both green:

- Deterministic: `internal/agent/query_plan_verification_test.go` composes the real plans for
  representative samples in both modes ("Oil Blending" MEA, "lubricant blending" India, "linear
  alkyl benzene" global/MEA, "fuel oil blending" Middle East, "used cooking oil" Malaysia,
  "n-butanol" India) and asserts the semantic invariants at plan level: no region soup, no
  self-repetition, no role-word stacks, bounded length, natural phrases present
  (`"lubricant blending"` variants), one query per geography.
- Live: `QUERY_PROBE=1 go test ./internal/agent -run TestQueryProbeLive` composes the real
  plans, runs the actual model review, then spends ~20 real Serper searches on the survivors.
  The 2026-11-17 run: the reviewer re-anchored all four seller trade lanes on the inferred
  category ("lubricants"), dropped the region tail from the buyer's site-scoped directory lanes,
  re-ordered a region lane for natural phrasing, and refused a duplicate rewrite; every probed
  query returned 9-10 hits naming exactly the right pages (lubricant blending plant and storage-
  terminal rosters, UCO suppliers and import-trade pages). Purposes survived unchanged.

**Repo hygiene (same day).** Removed 84 one-off artifact dumps, the stale buyer live-run logs
and the 38MB `server.exe~` snapshot; `/artifacts` and `/eval` are gitignored (the three
scorecards cited by `docs/BUYER_LANDSCAPE_REVIEW.md` stay tracked).

## Discovery landscape â€” current stage (buyer + seller)

The research modes are the product's core: Buyer Landscape (`mode=demand_chain`), Seller
Landscape (`mode=market_landscape`), plus company/person/legal/related/deep-search modes. This
session closed the category-lane gaps and fixed the discovery failures the live n-butanol run
exposed. Details in `docs/BUYER_LANDSCAPE_REVIEW.md` (2026-11-16 pass section) and
`docs/ARCHITECTURE.md` (buyer landscape section).

**What is in place now**

- **Trade-data lanes are explicit and tiered.** Seller side: exporter/shipment/customs lanes;
  buyer side: importer/customs/shipment/bill-of-lading lanes plus claim-conditioned
  verification (`import data OR importer of OR customs shipment`). Public trade platforms
  (Volza, Trademo, Seair, Eximpedia, Zauba) are tiered as `trade`: a trade import record can
  corroborate demand but never owns company identity â€” a trade record alone keeps a buyer at
  `lead`; only an owned-page buyer upgrades to `verified`. (`internal/agent/trade_data.go`)
- **Category lanes are stored and auditable end to end.** Both agents resolve a first-pass
  product category (`n-butanol â†’ plasticizers` / `acrylates and acetates`), run independent
  category discovery lanes, and store `CategoryLanes` + `CategoryResults` on both reports. The
  lane names, category and query counts render in the console (`fragments.html`), Markdown and
  HTML/PDF exports; the category section is emitted only when a category/results exist, and its
  rows carry Employees/Turnover like the main tables. Category findings never enter
  exact-product results.
- **Company metrics.** Evidence-backed Employees/Turnover (plus financial year and a
  metrics-source link) flow through discovery, cards and exports for both landscapes.
- **2026-11-16 discovery-composition fixes** (the KLJ failure):
  1. Stage-anchored lanes no longer carry the product prefix â€” `Plasticizer Manufacturers
     India` instead of `N-Butanol Plasticizer Manufacturers India`; only product-shaped lanes
     (tender, importer, trade-data, procurement, distribution) keep it
     (`demandLane.productAnchored`, `demandLaneSearchQuery`). Gap searches likewise search the
     stage vocabulary clean.
  2. The buyer-side category relation gate is morphology-tolerant (`demandTokenRelates`:
     singular/plural/`-ies` variants), so `acrylates and acetates` is no longer rejected against
     singular stage names; `acrylates`, `acetates`, `esters`, `glycol ethers` joined the
     product-family token map.
  3. `demandKnownDirectInputStages` + `demandKnownDirectConsumption` guard: the adversarial
     chain review can no longer drop (or demote) a chemically established direct input on
     "consumes derivatives" wording â€” n-butanol is esterified with phthalic anhydride directly
     into dibutyl phthalate, so Plasticizer Production is distance 1. The override is recorded
     as a warning; unrelated rejections still apply.
- **Live outcome (KLJ regression test):** the run resolves `Category: plasticizers (model)`;
  the `category level results` section lists **KLJ** as a corroborated plasticizer manufacturer
  in India (98%); the Plasticizer Production stage roster carries KLJ (QC `passed`); HS 29051300
  (butan-1-ol) customs records in the run's evidence name **KLJ PETROPLAST LIMITED** as a top
  butanol importer. Indian buyers (India Glycols 0.98 with trade import records, Deepak Nitrite,
  Nayakem, Bharti group) now surface with real country attribution.

## Standing subsystem notes (still accurate)

- **Registry tiering (India):** WeeDoo browser search-bar discovery + CIN attribution rules,
  zaubacorp at the company-registry tier, WeeDoo/zaubacorp director pages and the frozen
  (name, DIN) discipline before any person search (`related_entity.go`, `company.go`). WeeDoo's
  search bar does not serve the datacenter-IP browser on Cloud Run; the CIN-from-hits path and
  evidence-frozen DINs keep working there. Probe scripts under `scripts/weedoo-*.py`.
- **Cloud Run:** deploy via `deploy-gcp.ps1` (2Gi/2cpu for Chromium); health-check on
  `/api/health` (Knative reserves `/healthz`).
- **Person identity hygiene:** singular-office title conflicts, initials-vs-full-name positional
  matching (`personNamesMatch`), masked DIN rows resolve to nothing.
- **RocketReach:** opt-in in company mode; person candidate selection ranks seed URL â†’ current
  employer â†’ most recent past role â†’ lone exact name; paid lookups ID-pinned and cached 24h;
  provider labels flow through console/exports. Key-person discovery is deliberately NOT run in
  the landscape modes â€” the budget stays on company/demand discovery.
- **Person social section** is attached by the agent, never by the model, and carries a fixed
  disclaimer; contact lookups remain on-demand.
- **Company mode** defaults to leadership-focused research; department searches require explicit
  focus areas (the planner does not add default department queries).
- **Standing finding:** search scope and output scope are separate â€” a narrowed query plan can
  still yield a full org chart from broad evidence; a deterministic post-synthesis department
  filter was rolled back at the user's request. Any output-scope change needs synthesis-level
  regression tests.

## Validation

- `go build ./...`, `go vet ./...`, `go test ./... -count=1` â€” all green (634 tests).
- `gofmt` clean on touched files (repo-wide `gofmt -l` shows pre-existing CRLF drift in
  committed files; do not bulk-reformat).
- Live buyer-pipeline harness: `DEMAND_INTEGRATION=1 DEMAND_LIVE_DIR=<dir> go test
  ./internal/agent/ -run TestDemandChainLive -v -timeout 45m` (needs SERPER_API_KEY + Vertex
  ADC; ~5â€“10 min at the deep budget). Eval harness: `DEMAND_EVAL=1 go test ./internal/agent/
  -run TestDemandChainEval` writes scorecards to `eval/`.
- Caution: env vars set in a terminal session persist across commands in that session â€” clear
  `DEMAND_INTEGRATION` before package-wide test runs or the live test will run and spend budget.

## Trade records (Crystal import-export DB) — built this session

The Crystal trade DB (`crystalimportanalytics-1` Cloud Run, same project) is now a first-class,
opt-in data source:

- **Client** (`internal/tools/tradedb.go`): `TopImporters` / `TopSuppliers` (ranked by shipment
  value), `QueryRecords`, `EntityNames`, `FilterOptions`, `Health`. Bounded concurrency (semaphore,
  `TRADE_DB_CONCURRENCY`, default 3), one retry for transient blips, and a dedicated
  `TradeUnavailableError` for the stable disconnected-database answer (never retried). The ranking
  endpoints duplicate every row — the client dedupes. Live latencies: rankings ~20-45s, records ~8s.
- **Opt-in** (`IncludeTradeRecords` on both landscape queries; console checkboxes
  `demand_trade_records` / `market_trade_records`): default OFF, zero DB calls.
- **Geography gates** (`internal/agent/trade_records.go`): importer records are Indian importers —
  only India-scoped runs query them ("West Indies" is token-safe). Supplier records are foreign
  exporters into India: single-region runs resolve the region's foreign ports from the DB's own
  vocabulary (`tools.ResolveForeignPorts`) and drop every record whose `origin_country` does not
  match (`tools.RegionMatches` — origin_country is authoritative; port names are messy and the
  port→country pairs can be wrong, e.g. GENEVA→GERMANY). India seller runs skip suppliers with a
  warning; global runs take ranked exporters labelled unscoped.
- **Policy**: new `custody` source tier (first-party) distinct from public `trade`. `demandStatus`
  treats a custody import record as both identity and transaction → verifies alone. Public trade
  sites keep the lead-only rule. Rows are published as separate sections
  (`TradeImporters` / `TradeSuppliers`) that never feed verified results or coverage; importer rows
  carry `Role: importer` plus reseller tags (trading/distributor/wholesaler vocabulary) so the
  operator decides buyer vs reseller.
- **Rendering**: console (`fragments.html` trade record importers/suppliers sections), markdown +
  HTML exports (`internal/export/trade.go`, `html.go`, `demand_chain.go`, `market.go`).
- **Config**: `TRADE_DB_API_URL` (empty = capability off), `TRADE_DB_TIMEOUT_SECONDS` (60),
  `TRADE_DB_CONCURRENCY` (3), `TRADE_DB_MAX_PARTNERS` (**20**, clamped 1..40 — the report-wide cap on
  trade-DB names across the sections, so a high-volume chemical cannot swamp the report; each
  section carries at most the cap, the ranking head by shipment value); startup summary reports
  `trade-db=`. Deploy values in `deploy/cloud-run.env.yaml`.
- **Tests**: `internal/tools/tradedb_test.go` (11 hermetic), `tradedb_integration_test.go`
  (opt-in live smoke: `TRADE_DB_INTEGRATION=1 TRADE_DB_API_URL=<url> go test ./internal/tools/ -run
  TestTradeDBLiveSmoke -v`), `internal/agent/trade_records_test.go` (gates, mapping, policy,
  wiring, origin scoping). Full suite green.
- **Follow-ups**: API is `--allow-unauthenticated` — lock down with IAM + ID-token client; harvest
  the 5,186-entry foreign-port vocabulary into a majority-vote port→country index; ISO-2 port codes
  (`JEBEL ALI, AE`) unmatched in v1; `/api/query`'s fuzzy product search matches non-chemical rows
  (prefer the standardized ranking endpoints or pin HS codes).

## IndianKanoon Cloudflare wall — residential-proxy route + browser ladder (this session)

Live legal runs showed `indiankanoon: 0 kept of 0 hits ... 403 "Just a moment..."`. Two-stage fix,
both verified against the live wall:

- **Stage 1 — request shape** (`internal/tools/indiankanoon.go`, `egress_proxy.go`): the client used
  a bare `http.Client` with a self-declaring bot UA — and since `HTTPS_PROXY` (the residential proxy)
  is already bound at runtime, its default transport was *already* routing through it and still got
  challenged: the wall is triggered by the request shape, not just the IP. The client now uses the
  scraper's proven edge (per-host proxy transport via `scraperProxyForRequest`, cookie jar, browser
  UA + Accept/Accept-Language from the shared `browserUserAgent` const) plus a dedicated
  `INDIAN_KANOON_PROXY_URL` knob (subdomains inherit, shared HTTPS_PROXY fallback) bound in
  `deploy-gcp.ps1` to the same `residential_proxy_url` secret. **This alone did not pass the live
  wall** (re-verified post-deploy: still 403) — the challenge is a JavaScript interstitial.
- **Stage 2 — browser ladder** (the casemine/weedoo pattern): a challenge response is a typed
  `indianKanoonWallError` that falls back to a **dedicated Playwright Chromium routed through the
  same proxy** (`newPlaywrightRendererWithProxy`, started lazily on the first wall so wall-free runs
  pay nothing), bridging the plain client's cookie jar into the shared context so the clearance
  cookie the challenge grants serves later fetches. A wall-shaped render is refused, never parsed.
  `Close()` wired in `main.go`.
- Tests: `TestIndianKanoonSendsWallPassingHeaders`, `TestIndianKanoonRoutesThroughItsOwnProxy`,
  `TestIndianKanoonWallFallsBackToRender` (403 → injected render → hits parsed; challenged render →
  error); the 403 test injects a failing `startRenderer` so unit tests never launch a browser.
  Full suite green.

## Cache ops detached from run lifetime (this session)

`[WARN] redis cache get failed for search:<hash>: context deadline exceeded` at the tail of a
landscape run was the cache **inheriting the run's research deadline**: `RedisCache.Get/Set/Delete`
(and the GCS backend) passed the caller's context straight to the client with no timeout of their
own, so (a) every cache op near the run deadline failed with `context deadline exceeded`, (b) a hung
Redis or VPC-connector blip could stall a run until the run's own deadline, and worst (c) **a
finished or disconnected run lost its cache writes** — the exact results the next run reuses.

Fix (`internal/cache/redis.go`, `internal/cache/gcs.go`): every cache op now runs on
`context.WithoutCancel(ctx)` — values kept, cancellation and deadlines dropped — bounded per-op
(2s Redis, 5s GCS). Cache ops can never ride the caller's deadline again, degrade to a miss within
seconds during an outage, and writes always land. Pinned by `TestGCSCacheSurvivesCancelledCallerContext`
(GCS stub, runs in the suite) and `TestRedisCacheDetachedFromCallerCancel` (opt-in
`REDIS_INTEGRATION=1`). Full suite + race green.

## Landscape run concurrency raise (this session)

Both landscape modes were wall-clock-bound by serialized network stages. Now:

- **Seller landscape** (`market_landscape.go`) had **zero** concurrency: lane discovery, LLM
  extraction batches and per-candidate verification searches all ran one call at a time. All three
  are now bounded-concurrent (`marketLaneSearchConcurrency=4`, `marketExtractionConcurrency=3`,
  `marketVerificationConcurrency=4`, `marketExtractionBatchSize=4`): every stage lands results in
  pre-assigned slots and applies them in candidate/lane order after the wait, so rosters, warnings,
  rejection counts and evidence summaries never depend on completion order.
- **Buyer landscape** (`demand_chain.go`): lane search + extraction concurrency raised 3→6
  (`demandLaneSearchConcurrency`, `demandExtractionConcurrency`); the **verification searches**
  (previously fully serial per candidate × purposes — the dominant phase) now run bounded-concurrent
  (`demandVerificationConcurrency=4`); per-stage gap/broaden searches (previously serial) run
  bounded-concurrent (`demandGapSearchConcurrency=3`).
- **Budget semantics preserved exactly**: verification budget is an atomic spend counter — the cap
  holds and a small budget is partially spendable (a candidate's purposes stop mid-list when the
  budget runs out), matching the serial loop it replaced (`TestDemandChainBudgetOverride` pins this).
- Scraper env raised in `deploy/cloud-run.env.yaml`: `SCRAPE_CONCURRENCY` 4→6,
  `SCRAPE_HOST_CONCURRENCY` 2→3 (directory-heavy reads — ensun/revenuebase — benefit; per-host rate
  limit unchanged).
- Test-stub fix surfaced by `-race`: `failingScraper.calls` now mutex-guarded (batch scraper calls it
  from worker goroutines). `internal/agent` + `internal/tools` pass under `-race`.

## RR discovery pivot — Company research button (this session)

The per-card "RR discovery" button on Buyer/Seller Landscape results (which fired 3-4 RocketReach
searches × up to 3 pages = up to 12 metered calls per click, uncached) is **replaced by a
"Company research" button** (`POST /api/market-landscape/company-research`):

- The handler (`handleCompanyResearchLaunch`) maps the card onto the existing identity-confirmation
  flow (`handleResolveEntity`): name → query, verified location → brief country, landscape product →
  industry, run provenance → notes; RocketReach discovery stays **off** for the run.
- One Enter confirms the identity and the dossier runs with the entity gate; its research roster
  already offers per-person **Fetch RR** (cached by profile ID), so provider quota is spent only on
  the people the operator picks. The dead RR-discovery handler, route and template were removed.
- **RocketReach client hardening** (needed because Fetch RR lookups and person-mode RR discovery
  still call the provider): the client now uses a **direct-egress transport (`Proxy: nil`)** — the
  shared `HTTPS_PROXY` (residential proxy) was routing RR API traffic and produced intermittent
  mid-response cuts (live: `unexpected EOF` on `/person/search`); timeout 20s→45s; one bounded retry
  for transport cuts/5xx (429 only when `Retry-After` ≤ 10s); and `RocketReachHTTPError.Detail` now
  carries the provider's own error text, which was previously discarded.
- Tests: `internal/tools/rocketreach_retry_test.go` (transport-cut retry, no-retry on long windows,
  provider detail, direct egress) + web/launch mapping tests. Full suite green.

## Intermediate-category downstep (this session)

The buyer chain's category ("acrylates and acetates" for n-butanol) is an **intermediate product
family** — category-shaped search dead-ends on the intermediate's producers, while the real buyers
one step further (polymers/copolymers/plasticizers made from those esters) only entered the chain
when the model happened to infer them. The **category downstep** resolves this:

- After category validation, when the category is an intermediate (`demandCategoryIsIntermediate`)
  **and names what the distance-1 stages produce** (`demandCategoryMatchesStageOutput`), one bounded
  structured call resolves the families that consume the intermediate; they enter the chain as
  **distance-2 stages** (`Source: "category"`), deduped against the inferred stages (shared role
  words like "production" do not count as overlap), capped at 4 and by `demandMaxStages`.
- Deterministic consumer families (`demandCategoryConsumerFamilies`) anchor the model's proposal and
  are the fallback when it fails or proposes nothing usable; a consumer that is a known DIRECT
  consumer of the product is upgraded to distance 1 (chemistry outranks chain position).
- The chemistry guard matters: the downstep fires only when the category matches the step-1 **output**
  ("used cooking oil -> oleochemicals" with a biodiesel step 1 must not pull in soap/surfactant
  siblings — their buyers do not buy biodiesel).
- Downstep stages then flow through the existing machinery unchanged: lanes, discovery,
  verification, buyer-fit confidence, stage coverage, QC. Audit: a warning names the category and
  the added stages.
- Tests: `internal/agent/demand_downstep_test.go` (downstep append, deterministic fallback, dedupe,
  non-intermediate skip, known-direct upgrade) + updated `TestDemandInferenceCategoryIsValidated`.

## Market-intelligence directory promotion — ensun + revenuebase (this session)

The operator promoted two market-intelligence directories for buyer/seller landscape discovery:
**ensun.io** and **revenuebase.ai**. Previously unclassified (they would land in the "official" tier
— able to present as the operating company itself), and revenuebase-sourced rows were being dropped
by the buyer QC for the source's sake. Now:

- **Classification** (`entity_source.go` `sourceMarketIntelMarkers`): both domains are
  **directory-tier** everywhere — `classifyDiscoverySource` → `sourceDirectory` (never the operating
  company), `marketSourceTier`/`marketDiscoveryTier` → "directory" (qualifying for corroboration but
  never identity-owning), buyer `demandSourceTierForHit` → "directory". Directory tier is already the
  read-first class (`marketLeadReadPriority` 1 for roster-shaped titles) and a roster source, so
  their ranked manufacturer tables are scraped ahead of company homepages and their names extracted
  and verified like any other lead.
- **Deterministic site-scoped lanes** (`trade_data.go`): the seller landscape gains
  `site:ensun.io <activity> manufacturers` + `site:revenuebase.ai <activity> manufacturers`;
  the buyer landscape gains product-anchored `site:ensun.io` / `site:revenuebase.ai` lanes (a
  generic product-scoped query is deliberate — any directory page mentioning the product is a lead
  worth reading; the producer gate and consumption verification sort buyers from sellers).
- **QC policy promotion**: both default QC policies gained a source rule — a company found on ensun
  or RevenueBase is judged on its own evidence like any other row, never rejected/accepted for its
  source, and the directory itself is never credited as the company.
- Tests: `internal/agent/market_intel_test.go` (tier + read priority, lanes in both landscapes, QC
  policy promotion + default-policy identity).

## Future goals / improvement areas

Prioritized from the live runs and the code:

1. **Group-entity identity linking (top).** Discovery extracts "KLJ" while the customs record
   names "KLJ PETROPLAST LIMITED" â€” group entities (KLJ / KLJ Group / KLJ Plasticizers / KLJ
   Petroplast / KLJ Resources) must resolve to one corporate family so a trade import record
   naming a group entity corroborates the parent lead and can promote it past `lead`. Add an
   alias/corporate-family resolution step to verification (seeded by registry group data and
   shared name prefixes + registered-office matches).
2. **Own-site consumption-language promotion.** Category-manufacturer leads whose own pages
   show consuming-stage vocabulary (e.g. KLJ's DBP/phthalate pages) should earn
   operator-verification evidence instead of staying `candidate` â€” currently the KLJ page was
   read but did not upgrade the lead.
3. **QC judge calibration for known-direct stages.** The live run QC-removed several listed
   plasticizer manufacturers (Baerlocher, Payal, Kushal, Dhanraj) for "no direct evidence of
   N-Butanol consumption" even though plasticizer production consumes n-butanol directly. Feed
   the known-direct relation into the buyer-QC policy prompt (or accept stage-membership +
   producer status for known-direct stages deterministically).
4. **Presence/country attribution.** Rows with `Country: India` still land in the
   global/cross-border audit bucket, and many corroborated rows have an empty country. Presence
   should be promoted when the evidence names an Indian address/city (Mumbai, Hyderabad, Delhi)
   even without an explicit country token.
5. **Trade-site name extraction.** Volza/Trademo/Seair serve 403/captcha walls (proxy does not
   help), so importer names come only from search snippets. Options: browser-rendered reads for
   the trade tier, or expanding which snippets are mined for buyer names (the HS-code
   "top importers" lists already carry names like KLJ PETROPLAST).
6. **Stage coverage QC floor.** The six-company minimum is regularly unmet (honest gaps are
   reported, never invented). Widen operator vocabulary per stage, add deterministic
   `largest manufacturers of <stage-product>` lanes for stage products, and/or tune the
   minimum per stage distance.
7. **Name hygiene leaks seen live.** Roster names like "Pickle Mixing Machine Exporter &",
   "CGSpace", "Himadri Speciality Chemical Ltd: Home" passed the gates; tighten the title-fold
   and non-company-shape rejection (trailing "&", ": Home" suffixes, repository/aggregator
   hosts).
8. **Ship to Cloud Run.** The current fixes are local-only; redeploy with `deploy-gcp.ps1` and
   re-run the n-butanol sample against the service.
