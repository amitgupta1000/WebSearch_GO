# Buyer landscape review — is it working to the company-mode benchmark?

Status: implemented. The P0/P1 items below that this review identified were applied on 2026-11-14;
the historical review text is kept so the failure modes and their fixes remain auditable. Sections
below the implementation summary describe the pre-fix state.

## Implemented in the 2026-11-14 pass

- **F0 — research/news/directory pages are now read for buyer names.** `verifyDemandLeads` dedupes
  leads, orders them with `marketLeadReadPriority`, reads up to 30 readable pages (including
  research, news, directory and trade pages), uses the roster excerpt for list-shaped pages, and
  tells the extractor that an article/report is a valid source for the companies it names.
- **F1 — buyer verification source ladder.** Only operator-owned/regulatory evidence verifies;
  directory, trade, news, research, publisher and social sources are corroboration only. An
  `official` source must also look like the company's own domain (brand token or acronym, e.g.
  `Indian Oil Corporation` → `iocl.com`), except aggregator-hosted tender records.
- **F2 — name gates cover the leaked shapes.** `demandCompanyTitle` now composes the shared
  `isPlausibleMarketLeadName` gate with publisher suffixes, category/process titles, product
  pages, article shapes, and person-name particles (`August Wilhelm von Hofmann`). Serper `date`
  travels into evidence.
- **F3 — verification ladder.** `demandStatus` returns `verified` only with two operator-owned
  sources or one operator-owned source plus a transactional/input claim; one owned source is
  `corroborated`; directory hits never lift a row above `lead`.
- **F5 — budgets raised for stage coverage.** Defaults are 40 discovery searches, 180 verification
  searches, 60 enriched leads, 48 maximum results (caps 80/300/100/80); readability is separately
  capped at 30 pages. Gap/QC searches are reported separately as `gap_queries`.
- **F7 — coverage floor and QC judge.** Every inferred stage and the separate distribution
  category must identify at least **6** companies. Under-covered stages receive bounded fresh
  `SearchFresh` gap queries; the densest list/article/directory pages returned by those queries
  are read and stage-anchored extraction adds the companies they name as roster candidates. An
  LLM judge then reviews names/evidence and proposes rejects and up to three anchored follow-ups.
  The judge's rejects are applied only to names already in the run; follow-ups are executed as
  fresh searches; every rejection and the final stage coverage are recorded on the report.
- **Distributors/wholesalers are a separate category.** They keep their own reserved lane and
  `StageCandidates` section (`Distribution and wholesale`, source `category`) and are counted by
  the same six-company QC floor, instead of being folded into a manufacturing stage.
- **First-pass category with a deterministic guardrail.** The chain inference also returns a
  buyer-side category (the immediate downstream product family, e.g. TDI → polyurethanes,
  acetic acid → solvents, coal → coal products). It is validated before use: one to four words,
  no end-use/generic superclass (`fuel`, `energy`, `chemicals`, `industrial`, `automotive`, …),
  no process suffix (`manufacturing`, `production`, `synthesis`), not the product or a stage name,
  and it must share a real token/product-family relation with the product or inferred stages.
  A rejected category is reported and the stage-derived fallback is used instead. The category is
  never a buyer or a value-chain stage; it only broadens a weak stage search.
- **Adversarial chain review.** After inference, a skeptical reviewer challenges every stage:
  does it actually consume the product or its immediate derivative under the two-step definition,
  is the distance right, and is it a manufacturing process rather than an end-use market? It can
  reject stages and correct distances (for example a mattress plant consuming foam made from TDI is
  distance 2, not a direct TDI consumer); suggestions are reported but never auto-added.
- **Key-person discovery was removed from the landscape modes.** The former web/RocketReach people
  pass consumed search and API budget without improving buyer/company identification. Those budgets
  now go to discovery, verification, category fallback and gap extraction. Company mode's separate
  RocketReach discovery and on-demand person lookup remain.
- **Artifacts and UI** render stage coverage, the QC verdict/rejections, the adversarial chain
  review, the separate channel category, and the validated product category in the console,
  Markdown and HTML/PDF exports.

Regression tests added: research-page reading, category/person/publisher name gates, stage
coverage floor, QC rejection application, and the separate distribution roster. The existing
buyer unit/eval fixtures now list the leaked scorecard names as non-buyer markers.


## Verdict

**The pipeline runs and its mechanics are sound, but its result quality is not at company-mode
benchmark standard.** It is a good *discovery* pipeline; it is not yet a trustworthy *verified
buyer* list. Two independent signals point the same way:

1. The saved eval scorecards list publishers, a person’s name, category phrases, product pages
   and article titles as “verified” or “corroborated” buyers.
2. A probe of the current gates with those exact names shows they are still admitted today:
   `ACS Publications`, `August Wilhelm von Hofmann`, `Biodiesel Production and Distribution`,
   `Biodiesel producers`, `TDI Products`, `Renewable Toluene Diisocyanate (TDI) Production
   Plant`, and `Technology by Covestro` all pass `isPlausibleMarketLeadName` and
   `demandCompanyTitle`; `demandSourceName` rejects none of them.

The unit tests all pass, but the eval harness has **no pass/fail assertions**, so “tests green”
does not mean “buyer quality is benchmark standard”.

## What works

The pipeline has real strengths worth preserving:

- **Two-step buyer scope is inferred and enforced.** The chain inference prompt defines a buyer
  as a manufacturer consuming the product directly (distance 1) or one transformation step
  downstream (distance 2); `sanitizeDemandStages` drops deeper proposals and reports them
  (`demand_chain.go:823`–`941`). The saved scorecards show 0 chain scope leaks.
- **Every search is anchored.** Each lane query is `Activity + lane term + geography`
  (`demand_chain.go:140`), and every verification query is `"Company" Activity purpose
  geography` (`demand_chain.go:1427`), so a query cannot drift to another product.
- **Mandatory demand lanes are reserved.** `selectDemandLanes` keeps the direct
  procurement/purchase and tender/RFP lanes even when the discovery budget is small
  (`demand_chain.go:1073`), and tests pin that.
- **Claim-conditioned verification.** A tender-lane lead is verified tender-first, an import-lane
  lead import-first, everything else own-site-first (`demand_chain.go:1542`); claim types carry a
  directness-weighted confidence ladder (`demand_chain.go:1635`).
- **Several real identity gates exist**: producer self-description demotion
  (`demandIsProducerWithoutDemand`), document/article title rejection, bait titles, intermediary
  exclusion, stage relevance, name normalisation and alias dedupe.
- **Budgeting and bounded concurrency** are explicit: discovery/verification/handled-lead caps,
  three concurrent lane searches, three concurrent extraction batches.
- **Deterministic assembly**: sorted results, stage candidates, appendix, aliases merged, and the
  report carries the important honesty warning that private procurement is invisible.

## Test evidence

### Unit tests — green

`go test ./internal/agent -run 'TestDemand|TestSelectDemand'` passes (28 tests). These cover lane
order, mandatory lanes, inference override/fallback/caps, two-step scope, buyer-fit confidence,
producer signals, verification purposes, name hygiene, candidate filters, budget defaults,
stage candidates, alias dedupe, people discovery, and country matching.

### Eval/live tests — opt-in and non-asserting

- `TestDemandChainEval` skips unless `DEMAND_EVAL=1` (`demand_chain_eval_test.go:348`). When it
  does run, it writes scorecards but **never fails on a quality outcome** — missed stages, scope
  leaks, bait leakage, intermediary leakage and false buyers are only printed
  (`demand_chain_eval_test.go:392`–`413`). A bad run passes.
- `TestDemandChainLive` similarly skips unless `DEMAND_INTEGRATION=1`, and its only hard assertion
  is that at least one buyer was verified (`demand_chain_live_test.go:116`). It does not check
  precision.
- Credentials are present in this environment (`SERPER_API_KEY`, `GOOGLE_API_KEY`,
  `ROCKETREACH_API_KEY` are set), but the paid eval/live runs were not executed for this review.

### Saved eval scorecards — quality failures

From `eval/eval-methanol-scorecard.md` (2026-11-09):

- **“Verified” buyers include** `ACS Publications` (a publisher), `August Wilhelm von Hofmann`
  (a person, not a company), `Biodiesel Production and Distribution` and `Biodiesel producers`
  (category phrases), `DME: AN EMERGING GLOBAL FUEL` and `acetic acid production, from
  low-pressure methanol ...` (article titles).
- 12 “verified” results; 0 bait leakage was reported only because the bait marker list did not
  include these shapes.

From `eval/eval-tdi-scorecard.md`:

- **“Verified” buyers include** `TDI Products` (a product page), `Toluene Diisocyanate
  Manufacturer & Suppliers` (a supplier page), `Renewable Toluene Diisocyanate (TDI) Production
  Plant` (a project/article), `Technology by Covestro` (a technology licensor), and
  `What is the role of TDI in foam production? - Foamor.` (an article title).
- `Behn Meyer` (a distributor/trader) is verified despite intermediaries being off by default.

### Current-gate probe

I ran the current name/source gates against those exact names (diagnostic only, not kept in the
repo). Results:

| Name | `isPlausibleMarketLeadName` | `demandCompanyTitle` | `demandSourceName` |
| :--- | :---: | :---: | :---: |
| ACS Publications | ✅ | ✅ | ❌ |
| August Wilhelm von Hofmann | ✅ | ✅ | ❌ |
| Biodiesel Production and Distribution | ✅ | ✅ | ❌ |
| Biodiesel producers | ✅ | ✅ | ❌ |
| DME: AN EMERGING GLOBAL FUEL | ✅ | ✅ | ❌ |
| TDI Products | ✅ | ✅ | ❌ |
| Renewable Toluene Diisocyanate (TDI) Production Plant | ✅ | ✅ | ❌ |
| Technology by Covestro | ✅ | ✅ | ❌ |
| Behn Meyer | ✅ | ✅ | ❌ |

`demandSourceName` did **not** reject a single one. (It did reject the supplier-page name
`Toluene Diisocyanate Manufacturer & Suppliers`, and `marketDocumentTitle` rejected the
question-style article title — some gates work, but the coverage is narrow.)

Source-tier probe for the same review:

| Domain | `marketSourceTier` | `demandSourceOverride` | Qualifying? |
| :--- | :--- | :--- | :---: |
| `pubs.acs.org` | official | publisher | ✅ (tier says official) |
| `acs.org` | official | — | ✅ |
| `linkedin.com` | directory | — | ✅ |
| `wikipedia.org` | directory | — | ✅ |

A company with **one directory evidence item** gets `marketStatus = verified`.

## F0. Research articles are snippet-only in buyer mode

This is the biggest difference between buyer mode and the seller-side market mode.

**What buyer mode does today:**

1. `demandCandidate` reads the Serper hit's `Title`, `Link` and `Snippet`
   (`demand_chain.go:1144`). A research/publisher/news hit is classified as a
   `discovery-lead` (`evidenceType`), not a company page.
2. `verifyDemandLeads` builds scrape targets **only from qualifying source tiers**
   (`isQualifyingMarketTier`, `demand_chain.go:1287`–`1293`). Research, publisher, news,
   consultancy and social tiers do **not** qualify, so the article page is never fetched.
3. The extraction prompt receives the lead's **snippet only** for those pages
   (`demand_chain.go:1343`–`1347`). Company names that appear in the article body — the
   competitive-landscape table in a market report, the buyer list in a trade article — are
   invisible unless the snippet happens to name them.
4. Verification is also snippet-only: `demandVerificationEvidence` reads
   `hit.Title + hit.Snippet` (`demand_chain.go:1580`–`1600`) and never fetches the page.

**What market mode already does — and buyer mode should copy:**

`verifyMarketLeads` deliberately separates *reading* a page from *promoting* it:

- orders leads by `marketLeadReadPriority` and caps at `marketMaxLeadPages`
  (`market_landscape.go:266`–`279`);
- reads every `marketLeadReadable` lead, including research, news, directory and trade pages
  (`market_landscape.go:280`–`294`);
- uses `marketRosterPageExcerpt` (6000 chars) for report/directory pages so the competitive
  landscape table is included, instead of a 3000-char prefix (`market_landscape.go:305`–`310`);
- tells the extractor explicitly: “A report or an article is a valid source for the companies it
  names or lists … name the operator, never the page”
  (`market_landscape.go:343`).

Buyer mode has none of that. This is why the saved scorecards contain article titles and
publisher names as “buyers”: the article was not read, so its title was the only thing the
extractor saw, and the names inside it were never offered.

**Fix:** reuse the market-mode read policy in `verifyDemandLeads`:

- order and cap demand leads with `marketLeadReadPriority` / `marketMaxLeadPages`;
- read `marketLeadReadable` pages, using the 6000-char roster excerpt for
  research/news/directory/trade pages;
- add the market prompt's “a report/article is a valid source for the companies it names”
  instruction to the buyer extraction prompt, while keeping the “never return the page title or
  publisher as a company” rule;
- keep the source tier on the extracted lead for provenance, so a company sourced from a
  research report is still independently verified.

**Also add Snippet-aware admission.** Serper returns `title`, `link`, `snippet` and `date`
(`tools/serper.go:47`–`54`, mapped into `models.SearchResult`). Buyer mode uses the title to
name a candidate, the snippet for demand language, and ignores `Date`; it should:

- reject a URL as a `company-page` when the title or snippet is article/report/publisher shaped,
  even if the domain looks official;
- use the snippet (and display URL) to recover a company name when the title is decorated;
- carry `Date` into evidence so the QC/freshness layer can judge recency.

## Benchmark comparison

| Company-mode mechanism | Buyer-mode counterpart | Assessment |
| :--- | :--- | :--- |
| Grounded plan + deterministic anchor enforcement | One structured stage-inference call, two-step sanitisation, lane queries that always include the activity | **Good** — scope is the strongest part of the mode |
| Reserved source slots (`site:` per registry, publisher, domain) | Two mandatory demand lanes (procurement, tender) plus stage/product lanes; no `site:` slots for customs/tender/compliance sources | **Partial** — lane budget is reserved, source coverage is generic |
| BM25 → semantic rerank → entity penalty → relevance floor | No ranking stage; every discovery hit becomes a candidate; source tier is the only filter | **Missing** |
| Tiered scrape selection, one page per domain | All resolveable leads up to `MaxLeadsEnriched`, no source priority or per-domain cap | **Weak** |
| Typed evidence with source dates, OCR, quality | Typed evidence exists, but only `RetrievedAt` (now), no source date/freshness | **Partial** |
| Gap-filling lookups before synthesis | No stage-coverage pass; an empty stage stays empty | **Missing** |
| Entity gate at five points | Producer/bait/intermediary/document/identity gates exist, but name and source holes (below) let junk through | **Partial** |
| QC review with bounded follow-ups | No reviewer and no second pass | **Missing** |
| Freshness validation + reverse lookups | None | **Missing** |
| Search trace, scrape report, timings, gate counters | Lane list, appendix, counts; no per-query kept/excluded trace, scrape report or timings | **Partial** |

### F1. Source qualification is too permissive

`marketSourceTier` returns `"official"` for any domain it does not recognise
(`market_landscape.go:676`–`708`), and `isQualifyingMarketTier` accepts `official`, `regulatory`,
`directory` and `trade` (`market_landscape.go:710`). As a result:

- an unknown blog/content site is “official”;
- `linkedin.com` and `wikipedia.org` are “directory” and qualify;
- `marketStatus` calls a company “verified” with **one** qualifying evidence item
  (`market_landscape.go:651`), so a single directory hit is enough;
- `marketSourceTier` uses `isResearchPublisherDomain` (consultancy markers only), not the shared
  publisher markers; `pubs.acs.org` is “official” at tier level, and `acs.org` has no publisher
  override at all.

`demandVerificationEvidence` does override publisher/consultancy/institution tiers and rejects
them (`demand_chain.go:1580`–`1596`), but the tier bug above and directory acceptance are enough
to verify publishers and people through other domains.

**Fix:** introduce a non-qualifying `external`/`unknown` tier for unrecognised domains, require
the company's own domain (brand match) for “official”, exclude `directory` from *verifying*
evidence (allow it only as corroboration), and route `marketSourceTier` through the shared
`sharedSourceOverride` markers so publisher names/domains are known.

### F2. Name hygiene misses the shapes that actually leak

`demandCompanyTitle` is weaker than `isPlausibleMarketLeadName`, and the latter still accepts:

- publisher names (`ACS Publications`) — the publisher brand list lacks ACS;
- person names (`August Wilhelm von Hofmann`) — no person-name gate;
- category/process phrases (`Biodiesel Production and Distribution`, `Biodiesel producers`) —
  two-word roster phrases are not rejected;
- product/project/article titles (`TDI Products`, `Renewable Toluene Diisocyanate (TDI) Production
  Plant`, `Technology by Covestro`);
- `demandSourceName` knows tender portals and consultancies but not publishers, people or
  categories; it rejected none of the probe names.

**Fix:** make the extraction gate use one shared `marketLeadName`-based predicate, reject names
ending in roster nouns (`producers`, `products`, `suppliers`, `manufacturers`, `plant`,
`distribution`), publisher brands and person-shaped names, and require a company identity signal
(domain brand match, legal suffix, or a company-owned page) before a candidate becomes
`company-page` evidence.

### F3. “Verified” is too easy to earn

A candidate starts at confidence 0.62; one own-site/operator claim adds 0.08 → 0.70, and
`marketStatus` returns `verified` with a single qualifying evidence item. The Methanol scorecard
shows 12 “verified” rows, many at 0.98 from multiple generic pages. A directory hit can satisfy
the same rule.

**Fix:** define a buyer verification ladder, e.g. `verified` requires two independent qualifying
sources (or one company-owned source plus a transaction/input claim); `corroborated` requires two
source tiers; directory/trade evidence only corroborates. Show the evidence count/claim mix on
the row.

### F4. Producer and intermediary detection are name/label based

`demandProducerSignal` embeds the activity string, so “TDI Manufacturer” is not recognised when
the activity is the full chemical name; `isIntermediarySignal` only scans the company name and
domain, so distributors like `Behn Meyer` and technology licensors pass.

**Fix:** add activity aliases (TDI ↔ Toluene Diisocyanate, etc.), scan the verification page for
producer/distributor/trader language (“distributor of”, “authorised distributor”, “technology
licensor”), and demote a candidate whose own page self-describes as a supplier/trader unless a
transactional claim exists.

### F5. Lead budget can starve later stages

`leads` are appended in lane order; `verifyDemandLeads` enriches the first
`MaxLeadsEnriched` entries (`demand_chain.go:1279`–`1282`) without deduping URLs or reserving
slots per stage. Because stage lanes run first, early stages can consume the enrichment budget
and later stages end with no verified companies — even though `StageCandidates` is meant to show
each stage.

**Fix:** canonical-URL dedupe before enrichment and a round-robin per-stage lead queue (reserve
at least one enrichment slot per stage), as company mode reserves source/query slots.

### F6. Geography is advisory only

`Country` is copied from the extraction candidate but never parsed from verification evidence, so
most scorecard rows have a blank country. Countries are only used for a warning when a company's
stated country is outside the request (`demand_chain.go:257`–`270`).

**Fix:** parse the country from the company page/registry evidence, require a country match for
`verified` when the operator scoped the run (or move out-of-scope results to a clearly labelled
section), and keep the city-vs-country warning behaviour.

### F7. No gap fill, QC pass, or freshness pass

Unlike company mode, the buyer pipeline has no `fillEntityGaps` equivalent for a stage with no
leads, no reviewer/follow-up loop, and no source-date/freshness validation. A tender from 2018
can verify a buyer today; a stage can be empty with no second attempt.

**Fix:** add a bounded stage-gap pass (one or two stage-operator queries for empty stages), a
buyer QC review with follow-ups (missing buyers, supplier-only rows, stale evidence), and source
date annotation (`retrieved_at` is not a publication date).

## Minor code issues

- `buildStageCandidates` has a dead branch: discovery leads are already `continue`d at line 535,
  so the later `company.EvidenceType == "discovery-lead"` check at line 551 can never run.
- `DemandChainReport.Iterations` is hardcoded to `2` (`demand_chain.go:87`), although there is no
  second pass; it should reflect actual phases or be removed.
- `demandCompanyTitle` and `isPlausibleMarketLeadName` are two different name gates; only the
  latter runs at extraction, while the former still admits rejected shapes later in the assembly
  path. Unify them.
- `selectDemandLanes` keeps mandatory lanes even when `budget < reserved`, so a budget of 1 still
  runs two queries; either document the floor or clamp the reported `QueriesUsed` to the actual
  budget.

### F8. The eval cannot fail

`TestDemandChainEval` writes scorecards but has no thresholds; `TestDemandChainLive` only fails
when the result list is empty. The latest saved summary contains one product, so even the
informational coverage is thin.

**Fix:** make the eval assert, e.g. no publisher/person/category/bait names in `Results`,
precision ≥ 0.8 against a labelled buyer set, ≥ 60% direct-stage capture, 0 scope leaks, and run
at least Methanol, TDI and one non-India geography. Add the leaking names above as permanent
regression cases.

## Proposed QC layer (LLM judge)

Yes — a judge layer is the right next step, but it should **propose**, not dispose: deterministic
gates must still decide what enters the report.

### Where it runs

One pass after deterministic assembly (and optionally one more after the gap-fill pass), before
the final `MaxCompanies` cap and exports. It sees:

- the inferred chain and each result's `Name`, `Stage`, `Country`, `Status`, `Confidence`, and
  every evidence item's `Title`, `Snippet`, `URL`, `SourceTier`, `ClaimType`, `Status`;
- the stage candidates and the count/shape of the appendix (so it knows what was demoted);
- the run warnings and the buyer-scope definition.

Give it snippets, not just names. A judge that sees only `ACS Publications` cannot know it is a
publisher; one that sees the page title, snippet and URL can.

### Output schema

```json
{
  "verdict": "pass | revise",
  "score": 0.0,
  "rejected": [{"name": "...", "reason": "publisher | person | category | article-title | supplier | wrong-stage | stale | no-demand-evidence"}],
  "missing_stages": ["..."],
  "follow_up_queries": ["..."],
  "reason": "..."
}
```

### Guardrails

1. **No new companies.** The judge may reject, re-stage, or ask for a follow-up search; it may
   never introduce a name that is not in the supplied evidence.
2. **Deterministic re-check.** A rejection is accepted only when a code gate agrees (publisher /
   person / article / supplier / wrong-stage / stale). Otherwise the row is demoted to a stage
   lead or kept with a warning, not silently deleted.
3. **Bounded follow-ups.** At most 3 queries, anchored on the activity and a stage/company, run
   through `SearchFresh` for the “fresh” intent (or `SearchCached` when cost matters), and the
   new evidence goes through the same claim/verification path. One revision pass, budget-gated.
4. **Record the review.** Add `Reviews []QualityReview` (or a buyer-specific struct) to
   `DemandChainReport`, render it in the console and artifacts, and count rejected/re-staged
   rows in the report metrics.
5. **Never use the judge as evidence.** Its score is not confidence; the claim ladder and source
   tiers remain the deterministic score.

### Why it will help

It addresses three different failures at once:

- **junk rows** (`ACS Publications`, person names, category/page titles) that deterministic name
  lists will always miss;
- **stage mistakes** (a real buyer tied to the wrong consuming stage);
- **coverage gaps** (an empty or thin stage, which should trigger a bounded fresh search rather
  than an empty section).

It should not replace the P0 source-tier and name-gate fixes: a judge is a second pair of eyes,
not a substitute for refusing a publisher page as a company.

## Prioritised roadmap

### P0 — restore trust in “verified”

1. Non-qualifying default tier + company-owned-domain requirement; directory/trade as
   corroboration only; publisher marker coverage.
2. One shared name/identity gate covering publisher, person, category, product and article
   shapes; require a company identity signal before verification.
3. Verification ladder: no `verified` from a single directory/external hit; require
   independent sources or a company-owned transactional claim.
4. Eval assertions with the leaking names as regression cases.

Expected effect: the Methanol/TDI scorecards would lose most junk rows; precision becomes
measurable for the first time.

### P1 — coverage and attribution

5. Lead dedupe + per-stage enrichment reserve/round-robin.
6. Country extraction and scope enforcement/demotion.
7. Activity aliases for producer detection; distributor/trader page-language gate.
8. Stage gap-fill queries and a buyer QC review pass.

### P2 — benchmark parity and observability

9. Source-date/freshness annotation and a validation pass.
10. Per-query search trace, scrape report, phase timings and gate counters.
11. Optional source-slot queries (`site:` customs/tender/regulatory sources) and source-priority
    scraping.

## Suggested tests

- Table test the leaking names above against the unified gate; every one must be rejected.
- Source-tier matrix: unknown blog, directory, publisher, trade portal, company domain,
  regulatory; assert which can produce `verified` vs only `corroborated`.
- `marketStatus`: one directory hit ≠ verified; two independent company-owned sources + a
  tender claim = verified/corroborated.
- Lead fairness: 10 leads across 3 stages with `MaxLeadsEnriched=3`; each stage gets one, and
  duplicate URLs are enriched once.
- Eval hard assertions for Methanol/TDI: no publisher/person/category/article names in
  `Results`; direct-stage capture and bait/intermediary leakage thresholds.

## Appendix — anchors

| Concern | Anchor |
| :--- | :--- |
| Buyer pipeline | `internal/agent/demand_chain.go:64` (Research), `:90` (infer), `:106` (discover), `:177` (verify), `:217` (assemble) |
| Lanes and budget | `demand_chain.go:954`, `:1073`, `:784` |
| Candidate gate | `demand_chain.go:1144`, `:1248`, `:1260`, `:1400` |
| Extraction | `demand_chain.go:1350`, `:1372` |
| Verification | `demand_chain.go:1275`, `:1427`, `:1542`, `:1580`, `:1607`, `:1635` |
| Stage candidates | `demand_chain.go:524`, `:550` |
| Market source/status gates | `internal/agent/market_landscape.go:651`, `:676`, `:710`, `:765`, `:800` |
| Name hygiene | `internal/agent/market_names.go:466` (`marketLeadName`), `:327` (`marketHeadlineTitle`), `:370` (`marketPublisherSuffix`) |
| Eval harness | `internal/agent/demand_chain_eval_test.go:348`, `:392`–`413` |
| Live harness | `internal/agent/demand_chain_live_test.go:27`, `:116` |
| Saved scorecards | `eval/eval-methanol-scorecard.md`, `eval/eval-tdi-scorecard.md`, `eval/demand-eval-summary.md` |
