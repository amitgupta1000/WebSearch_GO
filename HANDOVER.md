# Handover Notes

## Current state

- Repository: `amitgupta1000/WebSearch_GO`, branch `main`, pushed to `origin/main`.
- Latest commits: the "trade based search made explicit" series (`a2a8a5b` and the docs commit
  that carries this file) â€” see `git log --oneline -5` for the exact chain.
- Worktree clean after the docs commit.
- Full Go suite: **634 tests passing, 0 failing** (`go test ./... -count=1`), `go vet` and
  `go build` clean.
- Live-verified this session: three full Buyer Landscape (`demand_chain`) runs on the sample
  query `"n-butanol" "India"` (`TestDemandChainLive`, deep budget). Logs:
  `eval/nbutanol-live*.log`; artifacts under `%TEMP%\crystal-live-nbutanol*`.

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
