# Person search review — benchmarking person mode against company mode

Status: review and proposal. Written against commit `dec1b2d` ("enhanced producer search-1"),
the revision where company mode is the working benchmark.

## Implementation status

The separate social section from G9 has landed:

- **Model:** `models.SocialSignal` plus `PersonProfile.Socials` / `SocialNote` and the fixed
  `models.PersonSocialDisclaimer`; `PublicAccounts` is now a projection of the same signals.
- **Plan:** five bounded, snippets-only platform queries (X/Twitter, Facebook, Instagram,
  LinkedIn posts, YouTube), each anchored on the confirmed name and carrying a recency window.
- **Admission:** a social hit must name the person **and** state the confirmed company, the
  confirmed place, or a recognisable role; a headline naming a different employer is refused.
- **Separation:** a social URL is never added to the employment fetch path, is never counted in
  the employment identity check, and a confirmed social seed is presented as a confirmed
  signal rather than an employment source.
- **Rendering:** a distinct **Social signals (unverified)** section with the disclaimer on every
  surface (console, Markdown/PDF, HTML), omitted when empty; the model-derived interests/themes
  block is renamed **Interests and themes (unverified)**.
- **RocketReach disambiguation:** candidate selection now ranks seed → current employer → most
  recent **past employer** in career history → lone exact name; the free search retries once
  without the current-employer filter; the paid lookup is pinned to the search profile ID; a
  past-role match adds a warning naming the profile's current employer.
- **RocketReach provenance and persistence:** provider-sourced experience/education rows carry
  `Provider: RocketReach`; QC keeps them with an unverified flag instead of dropping them; the
  company roster's **Fetch RR** action merges the full profile (history, education, skills,
  verified contacts) into the roster row and caches it by RR profile ID for 24 hours.

Still proposals: the ranker (G2), tiered fetch selection (G3), typed dated evidence (G4), gap
fill (G5), QC revision loop (G6), freshness (G8), and the proxy-aware browser work in §6.

This document answers three questions:

1. What exactly makes company mode the benchmark — which mechanisms, in which functions?
2. Where does person mode stop short of that benchmark, and what should change?
3. Can the residential proxy settings be used to reach Facebook/X for more personal
   flavour, and what would it actually take?

Every claim is anchored to a file and function so it can be checked against the code rather
than believed.

---

## 1. The company benchmark, in mechanisms

Company mode is not one clever prompt; it is mechanisms stacked so that a weak stage is
caught by the next one.

| # | Mechanism | Where | What it buys |
| :-- | :--- | :--- | :--- |
| 1 | **Grounded plan** — an LLM plans queries from a rich brief (name, aliases, domain, registration id, industry, country, city, requested areas, output, time scope, notes) | `company.go:1234` `planQueries` | Recall across directions (leadership, locations, filings, news) without hardcoding every angle |
| 2 | **Deterministic anchor enforcement** — every planned query must name the company, an alias, its domain or its registration id; unanchored and duplicate queries are dropped and the plan is refilled with anchored ones | `company.go:1261` `enforceEntityQueries`, `company.go:1336` `entityAnchoredQueries` | An unanchored query never runs, so its pages are never fetched, ranked or mined |
| 3 | **Reserved source slots** — `site:` slots for the company domain, the registry (`weedoo.in`, `zaubacorp.com`), promoted aggregators, indexed LinkedIn profiles and (global) named publishers | `company.go:1336`–`1401`, `sources.go:26`–`92` | Each valuable source is guaranteed a query even when the planner spends its budget elsewhere |
| 4 | **Multi-stage ranking** — BM25 → source-quality signals (`classifySource`) → entity penalty → Gemini semantic rerank of the top 12 → `pruneUnconnected` → relevance floor (quality < 0.25 ⇒ not fetched) | `company.go:1524` `rankResults`, `1567` `bm25RankResults`, `1724` `classifySource`, `1948` `pruneUnconnected` | The scrape budget goes to the pages most likely to answer the brief, not to the first ten hits |
| 5 | **Tiered scrape selection** — official leadership → official report → official locations → official → registry → leadership → industry event → external; one page per domain unless it is the company's own | `company.go:2891` `selectScrapeCandidates` | Ten fetches buy ten *different* sources instead of three pages of one site |
| 6 | **Typed evidence** — `models.Evidence` carries URL, title, content, source type, quality, images and a source date | `company.go:1179` `allSearchSnippetEvidence`, `evidence_dates.go:296` `annotateEvidenceDates` | Ranking, validation, recency and the prompt can all reason about *what kind of source* a fact came from |
| 7 | **Gap-filling lookups** — after scraping, bounded company-anchored searches for the areas the evidence does not cover | `entity_lookup.go:48` `fillEntityGaps` | A thin roster gets more to be built from before synthesis, instead of being synthesised thin |
| 8 | **Snippet filter** — only snippets that name the company inside the person's mention window reach the synthesiser; the rest are counted as dropped | `entity.go:601` `filterEntitySnippets` | A same-named person at another company cannot be blended into the chart |
| 9 | **Post-synthesis QC and a revision loop** — verdict, score, gaps, contradictions, unsupported claims and up to four follow-up queries; up to three iterations, budget-gated | `company.go:729` `reviewChart`, `company.go:285`–`301` | The model's first draft is treated as a draft, and a material gap triggers another search/scrape/synthesis pass |
| 10 | **Freshness validation** — every row dated and labelled (`verified-recent` / `dated` / `stale` / `undated` / `conflicting`), bounded reverse lookups, singular-title conflict detection | `validate.go:575` `validateChart`, `validate.go:390` `leadershipTitleConflicts`, `validate.go:887` `reverseLookup` | Stale and blended people are surfaced instead of presented as current fact |
| 11 | **Observability** — entity-gate counts, roster rows (kept and excluded), search trace, scrape report with quality/OCR fields, phase timings, exports | `company.go:141`–`188`, `company_roster.go:15` | The operator can see why a source was kept, dropped or never fetched |

The important property is that **the mechanisms do not depend on each other's correctness**.
A bad plan is caught by anchor enforcement; a bad ranking is caught by the quality floor; a bad
synthesis is caught by QC and validation.

---

## 2. Person mode today

Person mode has some of the same ideas — a deterministic plan, an identity gate, a bounded
scrape list, post-synthesis QC — but each is a thinner version of its company counterpart.

| Stage | Person implementation | Where |
| :--- | :--- | :--- |
| Confirmation | 2 searches (+1 aggregator pass when < 2 candidates); candidates parsed from headlines | `person_resolve.go:70` |
| Plan | Ten anchored queries (eight name-alone): four dossier questions, one aggregator `site:` query (snippets-only), five social-platform queries (X/Twitter, Facebook, Instagram, LinkedIn posts, YouTube; snippets-only); cap 12 | `person_plan.go:40`–`90` |
| Search | Every query through `SearchCached` with 4 workers; snippets identity-gated; social snippets held in a separate context | `person.go:145`–`269` |
| Fetch selection | Confirmed seed, then profile URLs from every plan search, then any gate-matched page, up to `MaxScrapes`; plan order decides | `person.go:316`–`422` |
| Fetch | Session fetch for profile hosts, else the hard-URL ladder; the rest in parallel | `person.go:424`–`537` |
| Identity check | kept / name-only / excluded, listed in the dossier | `person_gate.go:109`, `person.go:550`–`561` |
| Synthesis | One Gemini call; no review, no revision, no gap fill | `person.go:580`–`650` |
| QC | Deterministic only: drop non-confirmed employers unless a reverse lookup rescues them; correct the headline employer; flag overlapping timelines | `person_qc.go:27`–`81`, `person_qc.go:179` |
| Same-named people | Excluded evidence regrouped per stated employer | `person_gate.go:407` |
| Export | Markdown + PDF with identity check, same-named, trace | `export/markdown.go:496`, `612`, `631` |

What is missing is exactly what the benchmark table above supplies: a grounded plan, ranking,
tiered fetch selection, typed and dated evidence, gap filling, and a revision loop — plus
symmetric handling of partial and fuller name renderings (G12). The next section turns that
into findings.

---

## 3. Findings — where person mode is weaker

Each finding states the observation, the evidence, the cost, and the improvement. Findings
are ordered by expected impact per unit of work, not by severity alone.

### G1. The plan is a static six-query template

**Observation.** At review time `buildPersonPlan` emitted at most six queries when a company
was confirmed (four topic queries, one aggregator `site:` query, one X/Twitter query) and four
for a name-alone run. The plan ignores everything the confirmation step already learned: the
candidate's stated title, the seed profile's title/snippet, and RocketReach's current title
and full job history. It also has no reserved `site:` slot for the sources that matter most
in India — a LinkedIn profile index, a registry director page, or a news desk.

**Evidence.** `person_plan.go:40`–`90`; `person_plan_test.go` pinned six at review time and now
pins the ten-query plan; `person.go:114` is the only caller. Contrast `company.go:1234`–`1255`
(brief-grounded planner), `company.go:1261` (anchor enforcement) and `company.go:1336`
(reserved slots). `models.PersonQuery` (`models/person.go:160`–`179`) has no title, domain,
source profile or focus field to plan from.

**Cost.** Recall is capped at whatever the four generic phrasings happen to surface. The
registry identity freeze (`harvestPersonDIN`, `person.go:51`) only fires if a plan result
happens to carry a `zaubacorp.com/<slug>-<DIN>` or `weedoo.in/director/...` URL or a labelled
DIN — i.e. the strongest identity anchor in person mode depends on luck. Education, board
seats and past employers are each covered by one broad query with no date window.

**Improvement.** Introduce a `PersonBrief` (name, aliases, company, company domain, confirmed
title, location, country, seed URL/title/snippet, focus areas, time scope, source profile,
social opt-in) and a planner that mirrors company mode:

- `planPersonQueries(ctx, brief) (models.QueryPlan, int)` using `GenerateStructured` and the
  existing `models.QueryPlan`/`PlannedQuery` shape (purpose, target area, priority).
- `enforcePersonQueries` — a query survives only if it names the person (token or initials
  variant) **and** carries the company, the confirmed title, the location or a
  `site:`-restricted authoritative host; duplicates dropped; refill from
  `personAnchoredQueries`.
- `personAnchoredQueries` — deterministic slots: `site:linkedin.com/in/ "Name" Company`,
  `"Name" Company`, `"Name" Company (appointed OR board OR independent director OR DIN)`,
  `"Name" Company (career OR alumni OR university OR education)`,
  `site:zaubacorp.com "Name"` and `site:weedoo.in/director "Name"` (India),
  `site:<company domain> "Name"` when the domain is known,
  `"Name" Company (interview OR keynote OR award OR conference)`,
  a regional news slot, a directory slot (`site:theorg.com`, `site:rocketreach.co`), and the
  personalization slots from G9.
- Raise `personPlanMaxQueries` from 12 to ~16 while keeping the four-worker concurrency and a
  scrape reserve; an extra Serper query is cheap, a missing employer is not.

This is the highest-leverage single change: it converts the DIN freeze from luck into a
planned outcome and gives every later stage better material to rank.

**Status:** the social half has landed — five platform slots and the ten/eight-query plan. The
title/seed/RocketReach grounding and the registry, news and company-site slots remain open.

### G2. There is no ranking stage

**Observation.** Person results are processed in plan order (`person.go:198`), appended to
`planResults` in plan order, and the fetch list is built by walking that order. There is no
BM25, no semantic rerank, no source-quality score and no relevance floor. A page that merely
mentions the name and company outranks a university alumni record or a board appointment that
states the same facts, simply because its query was planned earlier. Snippets reach the
synthesiser in plan order too, so the model's attention follows the template rather than the
evidence.

**Evidence.** `person.go:198`–`268` (snippet processing), `person.go:316`–`422` (fetch
order). Contrast `company.go:1524` (`rankResults`), `1567` (`bm25RankResults`),
`1724`–`1800` (`classifySource` + quality/relevance floor), `1948` (`pruneUnconnected`).

**Cost.** With `MaxScrapes` 10, the budget goes to the first ten gate-passing URLs, which in
practice means several LinkedIn variants, an aggregator page and one press release. The page
that would answer “where did they study” or “which boards do they sit on” stays a snippet or
is never seen.

**Improvement.** Add `person_rank.go`:

- Build a ranking query from the brief: name (all tokens), company, confirmed title, location,
  and the plan's section terms.
- Extract the BM25 core from `bm25RankResults` into a neutral helper
  (`bm25Rank(queryTokens []string, results []models.SearchResult)`), then make the company
  function a thin wrapper. The person ranker reuses it unchanged.
- `applyPersonSourceSignals` with a person source table: confirmed seed 1.0; registry director
  page 0.9; personal site/about page 0.8; university/alumni/association 0.7; conference
  speaker bio 0.65; professional profile 0.6; news/press 0.55; company team page 0.55;
  directory 0.3; social 0.2; deny-listed 0.1 (never fetched). Add the gate verdict as a
  signal: `confirmed` +0.15, `matched` +0.05, `name-only` −0.1.
- Semantic rerank the top 12 with the same `GenerateStructured` pattern as company mode,
  parameterized for “person dossier” and the confirmed identity.
- `prunePersonUnconnected`: drop candidates whose title/snippet does not name the person,
  keeping snippet-less candidates (the company rule). Cap semantic cost with the existing
  8-second deadline.
- Order snippets by score within each section before writing the context.

This is the second-highest-leverage change, and it is mostly a refactor of existing company
code rather than new logic.

### G3. Fetch selection has no source tiers, no per-domain cap and no deny list

**Observation.** `addCandidate` admits anything the gate marks usable; the only ordering is
“profile hosts first, then gate-matched pages”. Nothing prevents three pages from the same
host, and nothing excludes job boards, content farms or social pages from consuming a fetch
slot. Company mode's `selectScrapeCandidates` does both — tier priority plus one page per
domain (two for the company's own domain).

**Evidence.** `person.go:356`–`422`; `company.go:2881`–`2961`; `lowQualityHosts` at
`company.go:1706` includes `facebook.` and `instagram.`; `isBlockedSourceURL`
(`company.go:2811`) blocks Wikipedia. None of these functions is referenced from any
`person*.go` file.

**Cost.** Ten fetches can buy four views of one profile host and one job-board page, while a
different domain that actually documents the person is never read. Social pages are
snippets-only in the plan, but a gate-matched social URL from an ordinary query can still
enter `fetchTargets` via `addCandidate`.

**Improvement.** `person_scrape.go`:

- `selectPersonScrapeCandidates(ranked, limit, seedURL)` with tier order: confirmed seed →
  frozen-DIN registry pages → personal site/about → university/alumni → conference bio →
  professional profile → news/press → company team → directory → external.
- Per-domain cap 1 (allow 2 only for the confirmed profile host when the first is the seed).
- Reuse `isBlockedSourceURL` and a person deny list (derive from `lowQualityHosts`, plus
  `x.com`/`twitter.com` for employment evidence). A deny-listed host may still contribute
  its snippet, but never a fetch.
- Reserve one slot for an education/career source and one for a non-profile domain, so the
  dossier cannot be built entirely from profile snippets.

### G4. Evidence is untyped strings, so dates, quality and documents are unavailable

**Observation.** Person mode accumulates two `strings.Builder`s (`gatheredContext`,
`gatheredScrapes`) plus raw `SearchResult`s. It never constructs `models.Evidence`, so it
cannot use source dates, source quality, canonical-URL dedupe or image OCR — all of which
company mode gets for free. A PDF's text does arrive through the shared scraper, but it is an
untriaged blob rather than a dated, typed evidence item.

**Evidence.** `person.go:122`–`123`, `308`, `438`, `539`–`543`; `models.Evidence` in
`models/research.go`; `evidence_dates.go:296`; `company.go:2735` (`evidenceWithOCR`).

**Cost.** The dossier cannot say when a fact was true. A “current title” may come from a
2019 conference bio. Duplicate republications are caught only by a content hash, not by
canonical URL, and document images are never read.

**Improvement.** `person_evidence.go`:

- Convert both snippets and fetched pages to `[]models.Evidence` with `SourceType`,
  `QualityScore`, `EvidenceType` (`search_snippet`, `profile_page`, `registry_page`,
  `document`) and the originating plan section.
- Call `annotateEvidenceDates(evidence)` before synthesis and render each block with
  `as of YYYY-MM-DD` plus its source type, exactly as `formatEvidence` does for company mode.
- Reuse `contentHash` + canonical URL for dedupe, and `evidenceWithOCR` for PDFs/images when
  the evidence is a document.
- Keep the raw trace unchanged; the typed slice is an addition, not a replacement.

### G5. There is no gap-filling pass

**Observation.** Company mode spends a bounded set of company-anchored lookups on the areas
the evidence does not cover (`fillEntityGaps`), then synthesises. Person mode synthesises from
whatever the plan and scrape produced. If the career section is thin, or education is absent,
or the person's board seats are missing, nothing tries again.

**Evidence.** `entity_lookup.go:48` has no person counterpart; `person.go:563`–`650` goes
straight from the identity-check event to synthesis.

**Cost.** The dossier's weakest section is structural, not evidentiary: the run had budget and
did not spend it on the missing area. This is also the cheapest way to use RocketReach's job
history — the fallback search already returns past employers (`tools.RocketReachJob`), but the
run never corroborates them.

**Improvement.** `person_gaps.go`:

- Compute a coverage map from the typed evidence: per section (experience, education, board
  seats, skills, location, online presence, contact), the number and quality of sources, and
  whether a dated source exists.
- Spend at most 3 queries, strongest gap first, each anchored on the name and confirmed
  company/title, e.g. `"Name" "Company" (alumni OR university OR graduated)`,
  `"Name" "Company" (appointed OR board OR independent director)`,
  `"Name" "Company" (interview OR keynote OR award)`; gate every result, snippets-only unless
  the source tier is high enough to fetch.
- Seed the gaps with RocketReach job history: one query per past employer (bounded to 1–2),
  which both corroborates the row and can surface earlier roles.
- Respect `hasBudgetForIteration`-style reserves so synthesis and QC always have time.

### G6. There is no QC review or revision loop

**Observation.** Company mode asks a reviewer model for a verdict, gaps, contradictions,
unsupported claims and follow-up queries, then re-runs search/scrape/synthesis up to two more
times. Person mode synthesises once and applies deterministic QC to the finished rows. A
person dossier therefore cannot recover from a thin evidence set or a model that dropped a
well-evidenced role.

**Evidence.** `company.go:729`–`763` (`reviewChart`), `company.go:285`–`301` (iteration
loop), `company.go:28`–`34` (iteration cap and minimum budget). Person: `person.go:648`–`677`.

**Cost.** The benchmark's main quality gain — the reviewer noticing that a stated role has no
support, or that the education section is missing — is absent. Deterministic QC can only
remove rows, never ask for the evidence that would keep them.

**Improvement.** `person_review.go`:

- `reviewPersonProfile(ctx, profile, evidence)` returning `models.QualityReview` with person
  checks: unsupported experience/education rows, missing current title/company, contradictory
  employers in two rows, a “current” claim whose only source is old, and social evidence used
  as fact.
- Up to three focused follow-up queries; if the verdict is `revise` and budget allows, run one
  more search/scrape pass, merge the new evidence, and re-synthesise with the reviewer's notes
  appended. Cap at two passes, as company mode does.
- Run `qcPersonProfile` after every synthesis pass, not only the first.
- Record reviews in the profile (a `PersonProfile.Reviews []models.QualityReview` field) so the
  console and export can show why a revision happened.

### G7. Provenance is incomplete after synthesis

**Observation.** Only experience rows are required to cite a source (`person.go:635`). The
`Education` model has a `Source` field, but the prompt does not require it; `key_skills`,
`notable_projects` and `online_presence` have no provenance at all. `confidence_score` is
whatever the model writes, with no deterministic calibration.

**Evidence.** `person.go:600`–`641`; `models/person.go:196`–`202`; the console renders the
dossier sections from these fields.

**Cost.** A reader cannot check the education claim, and the confidence number is not tied to
gate counts or source quality. Company mode's rows all carry sources and validation labels.

**Improvement.**

- Extend the prompt: every education row carries `source`; `notable_projects` and
  `key_skills` are either omitted or carry a source (introduce a `models.SourcedItem`, or a
  parallel sourced-evidence section); `online_presence` URLs must pass `gate.classify`.
- Replace the model-supplied confidence with a deterministic score: weighted by
  confirmed/matched/name-only counts, source quality, recency and how many sections have at
  least one dated source. Keep the model's number only as an internal hint.
- Add a `PersonCoverage` struct (sections, sources, dates, conflicts) to `PersonProfile`, and
  render it beside the identity check.

**Status:** the RocketReach half is implemented — experience and education rows carry a
`Provider`, the console/Markdown/PDF/HTML render it, and QC treats provider rows as
unverified-but-kept. Source discipline for model-written education/skills and a deterministic
confidence score remain open.

### G8. No freshness or current-status discipline

**Observation.** `annotateEvidenceDates` runs only in company mode and the validation pass.
Person mode dates nothing, so “Current title” and “Current company” can rest on a 2018
conference bio. The company validation pass also detects “former/previous/ex” signals and
singular-title conflicts; person QC only checks the employer string.

**Evidence.** `evidence_dates.go:296`, `validate.go:575`, `validate.go:975`
(`hasCurrentEmployerConflict`), `validate.go:1031` (`hasExplicitHistoricalStatus`); no
references from `person*.go`.

**Cost.** The most consequential field in a person dossier — what the person does now — is
the least verified. Stale bios are common among long-serving directors.

**Improvement.**

- Date every experience row from its cited source (reuse `evidenceDate`), store
  `AsOf time.Time` and a status (`verified-recent` / `dated` / `stale` / `undated`).
- Only set `CurrentTitle`/`CurrentCompany` from a source inside the recent window
  (`ValidationRecentWithin`), the confirmed seed, or RocketReach; otherwise keep the row but
  label it as a past role.
- Detect explicit historical signals in the row's source snippet and demote it.
- Run the same singular-title conflict check across the dossier's current roles (a person can
  hold one CEO role, not two).

### G9. Social personalization is thin, and its admission rule is too loose

**Observation.** The only personalization query is one X/Twitter search, snippets-only, and
its results are admitted when the name tokens appear — company/location only adjust the
confidence label. There is no Facebook, Instagram, YouTube, LinkedIn-post or personal-blog
query, no handle discovery from the confirmed profile, and no recency requirement on the
flavour itself.

**Evidence.** `person_plan.go:67`–`74`; `person_gate.go:306`–`315`; confidence assignment at
`person.go:251`–`263`.

**Cost.** “More flavour” is exactly what this section is meant to deliver, and one query on
one platform with a name-only admission rule delivers noise for common names. A common name
plus “interest/opinion” can match thousands of unrelated people.

**Improvement.**

- Plan one bounded query per opted-in platform: X/Twitter, Facebook, Instagram, YouTube
  (channel/about), LinkedIn posts/articles (`site:linkedin.com/posts`, `/pulse/`), and the
  person's own domain/blog if discovered.
- Derive handle candidates from the confirmed profile URL and email/name patterns, then verify
  by requiring the handle's page snippet to name the person **and** the company (or two other
  identity tokens: location + role). Keep the existing `identity_basis`/`confidence` fields.
- Add a recency window per query (`after:`) and surface “recent themes” only from dated
  snippets.
- Never let social evidence set title/employer/sensitive attributes; keep that rule in the
  prompt and enforce it in the QC pass (G6).

**Status:** the snippets-only half is implemented — five bounded platform queries, corroboration
required for admission, and a separate **Social signals (unverified)** section carrying the fixed
disclaimer on every surface. Proxied page-level enrichment remains a proposal (§6).

### G10. No coverage or observability parity

**Observation.** Company mode records source quality per fetch (`ScrapeReportEntry.QualityScore`),
OCR status, entity-gate counts and a roster of kept and excluded people. Person mode's
`scrapeReportEntry` records URL, type, status, reason and word count only; there is no
section-coverage report and no deterministic quality signal in the dossier.

**Evidence.** `person.go:435`–`441`; `models.ScrapeReportEntry` fields at
`models/research.go`; `company.go:2061`–`2400` (`scrapeRankedWithBrief` sets the quality
fields); `company_roster.go:15`.

**Cost.** Tuning person mode is guesswork: the operator cannot see that four of ten fetches
went to one host, or that no fetched page covers education.

**Improvement.**

- Populate `QualityScore`, `SourceType` and `Extractor` on person scrape-report rows.
- Add a `PersonCoverage` report (per section: evidence count, best source type, newest date,
  conflicts) and a one-line run summary event mirroring the entity-gate summary.
- Surface it in the console's research tab and the Markdown/PDF “Research trace”.

### G11. RocketReach is used as a lookup, not as a research input

**Observation.** Company mode's RocketReach discovery turns search results into roster rows
with career history, location and LinkedIn URL, and records the query trace. Person mode's
lookup produces one structured profile and feeds its career history to synthesis, but at
review time the **candidate selection** did not use that history: only the seed, the current
employer or a lone exact name could pick a profile, so a former director whose current
employer had moved on was invisible. The run also filtered its free search by current employer,
which excluded exactly those profiles.

**Evidence.** `company.go:306`–`401` (`discoverRocketReach`), `company.go:402`
(`rocketReachCompanySearchQuery`); `person.go` (`lookupRocketReach`,
`searchRocketReachCandidate`, `selectRocketReachCandidateWithBasis`,
`rocketReachPastEmployerJob`).

**Cost.** The most reliable structured data in the run is used only as prompt text, not as
the skeleton the web evidence should fill. Past employers never become queries, so rows that
RocketReach asserts but the web has not corroborated stay single-sourced or get dropped by QC.

**Improvement.**

- Pass RocketReach's `CurrentTitle`, `Location` and `JobHistory` into the `PersonBrief` and
  let the planner anchor queries on them.
- In `qcPersonProfile`, treat a RocketReach row as corroborated for its employer and dates,
  with the provenance labelled `RocketReach` (not “web”).
- Add one bounded corroboration query per past employer only when the row would otherwise be
  dropped.
- Cache the lookup by `(name, company, seedURL)` so a re-run or a sibling dossier does not
  spend another credit; keep the existing “no guess” rule for ambiguous names.

**Status:** the disambiguation half is implemented — selection ranks the seed, then current
employer, then the most recent **past role** at the confirmed employer, then a lone exact name;
the free search retries once without the `current_employer` filter; the paid lookup is pinned
to the search profile ID; and a past-role match adds a warning naming the profile's current
employer. Company and market discovery intentionally remain current-employer-only, because
their rosters are current people; related-entity discovery already matches past employers
(`rocketReachProfileMatchesCompany`). The planning/corroboration proposals above remain open.

### G12. “Part or all of the name” is not handled symmetrically

**Observation.** Person search accepts a partial typed name at the resolution step, but three
later rules then work against it:

1. **The plan quotes the name as one exact phrase.** `buildPersonPlan` renders `%q` for the
   name (`person_plan.go:41`) and `publicPersonalizationQueries` does the same
   (`person_plan.go:68`). Google treats a quoted string as adjacent words, so `"Sanjay Gupta"`
   does not match a page that renders “Sanjay Kumar Gupta”, and `"Sanjay Kumar Gupta"` does not
   match a profile that renders “Sanjay Gupta”. A partial name in quotes is therefore the
   *narrowest* possible search, which is the opposite of what a partial name needs.
2. **Candidate parsing requires every typed token in the headline.** `parsePersonCandidate`
   calls `containsAllTokens` (`person_resolve.go:161`) and `splitPersonNameSegment` needs the
   same tokens to find the name (`person_resolve.go:336`). A typed middle name that the profile
   omits produces no candidate, even when the company and role match.
3. **The gate requires the surname to be immediately preceded by one token per given name.**
   `personNamedInText` (`person_gate.go:188`) aligns `Sanjay Kumar Gupta` only against another
   two-given-name rendering; “Sanjay Gupta” fails, and a one-token typed name is rejected
   outright (`len(tokens) < 2`). `containsAllTokens` has the same all-or-nothing rule.
4. **`personNamesMatch` requires equal given-name counts.** `personNamesMatch`
   (`validate.go:131`) therefore says “Sanjay Gupta” and “Sanjay Kumar Gupta” are different
   people. That propagates to `harvestPersonDIN` (`person.go:51`, which calls
   `personNamesMatch`), roster dedupe (`company.go:593`) and the same-named grouping — so a
   partial name loses the DIN freeze precisely where the registry spells the fuller name.

**Cost.** Partial names are the normal case in Indian registry records — the MCA name may be
“Sanjay Kumar Gupta” while the web profile is “Sanjay Gupta”. The dossier then gets fewer
sources, loses the strongest identity anchor, and can split one person into two identities.
Conversely, a surname-only run matches every namesake.

**Improvement — model name variants explicitly.**

- `personNameVariants(name)` produces the renderings the plan should search together:
  full quoted; first + last (middles dropped); “Last, First”; initials forms (`F Last`,
  `F. Last`, `F L Last`); the unquoted token form; and, when a company is confirmed,
  surname + company. Use an OR group, e.g.
  `("Sanjay Gupta" OR "Sanjay Kumar Gupta" OR Sanjay Gupta OR "S Gupta") "Acme"` rather than
  one quoted phrase.
- `personNameCovers(typed, candidate)` compares two renderings as an ordered *subsequence*:
  the surname matches exactly; the typed given tokens appear in order among the candidate's
  given tokens; each pair is equal or an initial-prefix match; and the first given token must
  align (no skipping the first name). This accepts partial → fuller and fuller → partial,
  rejects “Sanjay Gupta” vs “Suresh Gupta”, and still refuses a bare surname.
- Use `personNameCovers` instead of the equal-length `personNamesMatch` for candidate parsing,
  DIN slug matching, gate naming when the source carries the fuller name, roster dedupe and
  same-named grouping. Keep `personNamesMatch` only where two *confirmed* full renderings are
  compared and exactness is wanted.
- When the source carries more given names than were typed, require company/title/location
  corroboration before admitting it, and label the row “name variant” in the identity check.
- Prefer the fuller rendering as the canonical display name and as the anchor for every later
  query once a candidate confirms it. Persist both: `PersonQuery.Name` = confirmed/full and
  `PersonQuery.NameTyped` = what the operator typed, so the plan can search both and the
  identity check can say which variant matched.
- For a single-token name, never treat a hit as confirmed identity: require company plus title
  or location, and mark the row unverified; or ask the operator to pick a fuller candidate.
- Record `name_variants` in the dossier and the search trace, so the operator can see which
  renderings were searched.

**Edge cases to pin with tests.** Honorifics (already stripped by `normalizePersonName`),
two-letter initials written as one word (“Kl” → K L), surname-first registry order
(“GUPTA SANJAY”), compound surnames (“Sanjay Gupta Verma” — the current model treats the final
token as the surname; the confirmed full name must win once known), and a typed middle name
against a headline that omits it.

---

## 4. Target person pipeline

The goal is not to copy company mode wholesale (person runs are shorter and one entity, not a
roster), but to reuse its machinery so the same guarantees hold.

```
PersonBrief + personNameVariants ──► planner (LLM) ──► enforcePersonQueries ──► personAnchoredQueries
                    │                                          │
                    └──────────────► SearchCached (trace) ◄────┘
                                              │
                                   identity gate (snippets, name variants)
                                              │
                                   rankPersonResults (BM25 + semantic + source quality)
                                              │
                                   selectPersonScrapeCandidates (tiers, per-domain cap)
                                              │
                                   fetch (session / ladder / batch) ──► typed evidence + dates
                                              │
                                   fillPersonGaps (≤3 anchored lookups)
                                              │
                                   synthesis ──► qcPersonProfile
                                              │
                                   reviewPersonProfile ──► revise? ──► one more pass
                                              │
                                   coverage + identity check + same-named + export
```

New files, each with one responsibility:

| File | Responsibility | Reuses |
| :--- | :--- | :--- |
| `person_brief.go` | assemble the brief from the confirmed candidate, seed and RocketReach profile | `models.PersonQuery`, `tools.RocketReachProfile` |
| `person_names.go` | `personNameVariants` (search renderings) and `personNameCovers` (ordered-subsequence comparison for partial ↔ full names) | `normalizePersonName`, `personNameParts` |
| `person_plan.go` (rewritten) | LLM plan + `enforcePersonQueries` + `personAnchoredQueries` | `models.QueryPlan`, `limitQueryPlan`, `company.go` planner prompt pattern |
| `person_rank.go` | BM25, source signals, semantic rerank, person pruning | `bm25RankResults` core, `GenerateStructured` |
| `person_scrape.go` | tiered selection, per-domain cap, deny list | `company.go` `selectScrapeCandidates`, `isBlockedSourceURL` |
| `person_evidence.go` | typed evidence, dates, formatting, canonical dedupe | `models.Evidence`, `annotateEvidenceDates`, `contentHash` |
| `person_gaps.go` | coverage map + bounded gap lookups | `entity_lookup.go` pattern, `hasBudgetForValidation` |
| `person_review.go` | QC review + revision loop | `reviewChart` pattern, `models.QualityReview` |
| `person_gate.go` | unchanged, plus a `personCoverage` helper | existing gate |

Refactors that make reuse possible:

- `bm25RankResults(brief, results)` → `bm25Rank(queryTokens, results)` + a company wrapper.
- Extract the semantic rerank block (`company.go:1531`–`1564`) into
  `rerankResults(ctx, engine, instruction, queryLabel, candidates)`.
- Extract the quality-floor/penalty application into a small helper that takes a
  `sourceProfile` classifier, so person and company share the arithmetic but not the host
  table.

---

## 5. Roadmap

Estimates are engineering days for one developer familiar with the code, including tests.

### P0 — do first (high impact, low risk)

| Change | Impact | Effort | Risk |
| :--- | :--- | :--- | :--- |
| Add `PersonBrief` fields (title, company domain, seed, focus) and thread them from the confirmation step and RocketReach | Plan quality; every later stage | 0.5 | low |
| `personNameVariants` in the plan and `personNameCovers` for resolution/DIN/gate/dedupe (partial ↔ full names) | Recall for short names; DIN freeze survives middle names | 1 | low–medium |
| Extend `personAnchoredQueries` with LinkedIn-profile, registry/DIN, university, news and company-site slots; raise cap to 16 | Recall; DIN freeze becomes planned | 1 | low |
| Person deny list + per-domain cap + tiered fetch selection | Scrape budget buys distinct sources | 0.5 | low |
| Rank person results (BM25 + source table + semantic top-12) and order snippets | Best evidence reaches the model | 2 | medium (shared refactor) |
| Date and type the evidence; render dates in the prompt | Recency discipline; enables G5/G8 | 1 | low |
| Source on education and deterministic confidence | Checkable dossier | 0.5 | low |

Expected outcome: a noticeable improvement on the same live query without touching the
synthesis prompt or adding model calls beyond one rerank.

### P1 — the structural parity work

| Change | Impact | Effort |
| :--- | :--- | :--- |
| `fillPersonGaps` (coverage map + ≤3 anchored lookups, RocketReach history as seeds) | Fills the weakest section | 1.5 |
| `reviewPersonProfile` + one revision pass, QC after each pass | Catches what deterministic QC cannot | 2 |
| Freshness/current-status checks and row labels | “Current” means current | 1 |
| Coverage report + scrape-report quality fields + run summary | Makes tuning measurable | 1 |
| RocketReach-backed rows and lookup cache | Fewer credits, better corroboration | 0.5 |

### P2 — plan intelligence and social enrichment

| Change | Impact | Effort |
| :--- | :--- | :--- |
| Full LLM planner + `enforcePersonQueries` (replacing the template) | Handles unusual careers, languages, jurisdictions | 3 |
| Social personalization expansion (snippets-only) — see §6 | “Flavour” without scraping risk | 1 |
| Proxied social page enrichment behind a flag — see §6 | Post/activity text where lawful and technically feasible | 3–5 + review |
| Evaluation harness: a small set of known people with expected facts, run in CI weekly | Prevents regressions | 2 |

### Test plan

New/updated tests, in the repository's table-driven style:

- `person_plan_test.go` — every query anchored; new slots present; cap; fallback; title/seed
  context respected; no company invented; name-variant OR group present for partial names.
- `person_names_test.go` — `personNameVariants` covers full/short/initial/reordered forms;
  `personNameCovers` accepts “Sanjay Gupta” ↔ “Sanjay Kumar Gupta” both ways, rejects a
  different given name and a bare surname; two-letter initials align; surname-first ordering is
  handled or explicitly documented.
- `person_rank_test.go` — a university bio outranks an aggregator page; a snippet naming only
  the person is pruned; snippet-less candidates survive; semantic failure falls back to BM25.
- `person_scrape_test.go` — per-domain cap; deny-listed host never selected; seed first;
  registry pages reserved; one education and one non-profile slot.
- `person_evidence_test.go` — dates parsed and annotated; canonical dedupe; source type/quality
  set; document evidence routed through the OCR/extract path.
- `person_gaps_test.go` — only uncovered sections produce queries; budget respected; gate
  applies; RocketReach past employers seed at most two.
- `person_review_test.go` — a revise verdict triggers one more pass; a pass returns; budget
  exhaustion returns the current dossier; QC runs after each pass.
- `person_freshness_test.go` — an old-only “current” row is demoted; a former signal is
  detected; two current singular titles conflict.
- Proxy tests in `egress_proxy_test.go` — social hosts route when configured; unrelated hosts
  never do; browser selection picks the proxied renderer only for social hosts.

---

## 6. Can the proxy reach Facebook/X for more flavour?

Short answer: **the existing proxy settings can carry more traffic, but they cannot by
themselves deliver Facebook or X content.** The proxy changes the caller's IP reputation; it
does not create a session, execute JavaScript, or grant permission. It is currently wired to
the plain HTTP transport only, so it helps exactly the requests that already work over HTTP,
and it does not help browser-driven pages. The useful, low-risk move is to widen the
*snippet-based* social plan first; page-level social enrichment is a separate, flagged
project with a real compliance cost.

### 6.1 What exists today

- `tools/egress_proxy.go` defines two per-host proxies, read once at process start:
  `WEEDOO_PROXY_URL` for `weedoo.in` and its subdomains, `LINKEDIN_PROXY_URL` for
  `linkedin.com` and its subdomains (`egress_proxy.go:24`–`45`).
- `scraperProxyForRequest` is installed as the `http.Transport.Proxy` hook of the shared
  scraper client (`scraper.go:291`–`299`). Every plain fetch — `ScrapeURL`, `FetchHTML`, the
  session fetch in person mode — routes through the matching proxy; redirects inherit the
  transport. Everything else falls back to `HTTPS_PROXY`/`HTTP_PROXY`.
- The **shared Playwright browser is not proxied**: `newPlaywrightRenderer` calls
  `newPlaywrightRendererWithProxy(timeout, "")` (`scraper_browser.go:122`–`123`), and the
  scraper's only browser is created by that function (`getBrowser`, `scraper.go:1370`). The
  one proxied browser in the codebase is the WeeDoo client's, launched with
  `newPlaywrightRendererWithProxy(timeout, weedooProxyURL)` (`weedoo.go:235`–`242`).
- The deployment maps **both** knobs to the same residential-proxy secret
  (`deploy-gcp.ps1:43`), so the per-host scoping is currently about which hosts use the proxy,
  not about different proxies per host.
- The hard-URL ladder (browser renders) is what a blocked page escalates to
  (`scraper.go:449`, `hardLadderSteps`). It uses the unproxied shared browser. `HardURLShapeOf`
  classifies only LinkedIn shapes (`hardshape.go:40`–`61`); Facebook/X fall through to
  `ShapeOther`, so the ladder will attempt a plain render and an extended settle, and a
  cookie render only if the jar carries cookies for that host.
- Social URLs are already recognised for personalization: `isPublicSocialURL` knows
  facebook/instagram/twitter/x (`person_gate.go:317`–`327`), and the plan's single X query is
  snippets-only (`person_plan.go:72`). No social page is fetched today.
- `COOKIE_FILE_PATH` loads one JSON export into the shared jar and widens each cookie to its
  registrable parent (`cookies.go:117`–`130`), so a Facebook/X session exported from a
  browser *would* be available to a plain fetch or a cookie render — if the operator supplies
  it and the session is accepted.
- The scraper sends a desktop Chrome user agent (`scraper.go:810`, `1102`) and the renderer
  blocks images/media/fonts/stylesheets but allows scripts and XHR/fetch
  (`scraper_browser.go:36`–`48`), so SPA rendering is possible in principle.

**The first finding is therefore: the residential proxy is half-wired.** It covers plain HTTP
but not the browser that exists specifically for pages plain HTTP cannot read. That is as true
for LinkedIn profile escalations from Cloud Run as it would be for Facebook/X.

### 6.2 Why a proxy alone does not solve Facebook/X

| Host | Plain GET through the proxy | Proxied browser render | What actually gates it | Realistic outcome |
| :--- | :--- | :--- | :--- | :--- |
| **x.com / twitter.com** | Returns a JavaScript shell; no profile text | Works only if X serves the logged-out profile to a residential IP, and often redirects to login or rate-limits | JS rendering + auth/rate policy; ToS restricts automated collection | Public **snippets** are useful; individual tweets can come from oEmbed/syndication endpoints; profile timelines realistically need the X API |
| **facebook.com** | Login redirect for most pages; occasionally `og:` metadata | Reachable with an exported session + residential IP, otherwise login wall | Login wall; JS; Meta ToS prohibits automated collection | Only with a logged-in session, which is an account-ban and ToS risk; Graph API is the sanctioned route |
| **instagram.com** | Login wall | Same as Facebook | Login wall; Graph API | Not worth scraping; API or skip |
| **linkedin.com/posts, /pulse** | Public HTML; already proxied | Public shape (priority 0 in `hardshape.go:67`) | Authwall applies to profiles, not posts | **Already feasible today** — the cheapest genuine flavour win |
| **youtube.com (channel/about)** | Public HTML with description | Not needed | None for public metadata | Feasible snippets + fetch with no proxy |

Two mechanical points matter:

1. `Playwright` sets the proxy per **browser launch**, not per context or page. Adding social
   hosts to `proxiedHostRule` therefore does nothing for the browser path; a second browser
   launched with the social proxy is required (or a dedicated client like WeeDoo's).
2. A residential IP raises the odds that a *public* page renders. It does nothing for a login
   wall, and it does not change the platform's terms. The codebase's existing hard-URL handler
   already refuses to solve challenges, enter credentials, or press-and-hold; the same line
   should hold for social.

### 6.3 What to do instead — three phases, cheapest first

**Phase 1 — widen the snippets, no proxy, no new fetch path (recommended now).**

Google already indexes public X, Facebook, Instagram, YouTube and LinkedIn pages. A snippet is
free, lawful to read, and enough for flavour (bio, role, interests, recent themes) when it is
attributed correctly. Add bounded personalization slots:

```
site:x.com OR site:twitter.com "<Name>" ("<Company>" OR "<title>")
site:facebook.com "<Name>" ("<Company>" OR "<location>")
site:instagram.com "<Name>" ("<Company>" OR "<title>")
site:youtube.com "<Name>" ("<Company>" OR interview)
site:linkedin.com/posts "<Name>" ("<Company>" OR "<topic>")
site:linkedin.com/pulse "<Name>" "<Company>"
"<Name>" "<Company>" (keynote OR panel OR podcast OR webinar) after:<date>
```

Rules: keep them `snippetsOnly`, cap at 4–6 queries, require name **and** one identity token
(company, title, location) before admission, and never let them set employment or title. This
alone will materially improve “flavour” for common as well as rare names, and it needs no
scraping policy review.

**Phase 2 — proxy the browser for public pages, behind a flag.**

For hosts where a public page exists but the datacenter IP is challenged, route the browser
through the residential proxy. This also fixes the existing half-wiring for LinkedIn posts and
profile escalations. Facebook/X profile pages remain out of scope unless a session is supplied
and the operator accepts the ToS/account risk.

**Phase 3 — official or licensed APIs for X/Facebook.**

Where post text really matters, use the X API (user lookup + recent tweets) or Meta Graph API
(public page fields), or a licensed data provider. This is the only sustainable route; it also
gives structured timestamps and avoids scraping-policy ambiguity.

### 6.4 Implementation sketch for Phase 2 (proxy-aware browser)

1. **Generalise the proxy table** in `egress_proxy.go`:

```go
type proxyRule struct {
    suffix string
    env    string
    url    string
}

var (
    socialProxyURL = strings.TrimSpace(os.Getenv("SOCIAL_PROXY_URL"))
    proxyRules     = []proxyRule{
        {"weedoo.in", "WEEDOO_PROXY_URL", weedooProxyURL},
        {"linkedin.com", "LINKEDIN_PROXY_URL", linkedinProxyURL},
        {"facebook.com", "SOCIAL_PROXY_URL", socialProxyURL},
        {"x.com", "SOCIAL_PROXY_URL", socialProxyURL},
        {"twitter.com", "SOCIAL_PROXY_URL", socialProxyURL},
        {"instagram.com", "SOCIAL_PROXY_URL", socialProxyURL},
    }
)
```

Keep `proxiedHostRule`'s signature so the transport hook and tests are unchanged; add
`proxiedHostList()` for the startup summary and browser selection. Follow the existing
read-once-at-startup pattern (tests already have `restoreProxyKnobs`).

2. **Give the scraper a second, browser-level proxy path.** Because Playwright's proxy is a
   launch option, add a lazily initialised `socialBrowser` created with
   `newPlaywrightRendererWithProxy(s.browserTimeout, socialProxyURL)` and a
   `getBrowserFor(targetURL)` that returns it only when `proxiedHostRule(host) == socialProxyURL`.
   Guard it with the existing `browserMu`, bound renders with the existing `renderSem`, and
   close it in `Close()`. Alternatively (cleaner isolation, and the pattern already proven by
   WeeDoo) add a `tools.SocialBrowserClient` with its own lifecycle and use it from the person
   agent only; this avoids a second browser in every company run.

3. **Teach the ladder about social shapes.** Add `ShapeSocialPublic` (posts/profiles that
   render logged-out) and `ShapeSocialWalled` (login-only), with `AuthWalled()` true for the
   latter. Priority: public social after LinkedIn posts, walled social last. This prevents a
   logged-out Facebook attempt from burning the extended-settle step, exactly as
   `ShapeLinkedInPerson` does today (`hardshape.go:82`–`86`).

4. **Fetch social pages only as personalization, never as evidence.** In the plan, mark a
   social entry `personalization: true` and `fetchable: true` only when the operator opted in
   (`SOCIAL_ENRICHMENT=1`). Fetch at most **one** social page per run, through the social
   browser (not the plain transport), and write the result to `personalizationContext` with a
   label and date. A blocked/failed fetch degrades to the snippet, as everywhere else.

5. **Handle discovery and verification.** Derive candidate handles from the confirmed profile
   URL (`linkedin.com/in/<slug>`), the seed title, and the person's own domain; verify a
   handle by requiring its page or snippet to name the person and the company. Store
   `identity_basis` and `confidence` exactly as `public_accounts` does today.

6. **Config and observability.** `SOCIAL_ENRICHMENT` (default `0`), `SOCIAL_PROXY_URL`,
   `SOCIAL_FETCHES_PER_RUN` (default `1`), `SOCIAL_RENDER_TIMEOUT` (default the ladder's hard
   render timeout). Log the egress summary at startup (extend `egressProxySummary`) and add the
   social fetch outcome to the scrape report with `SourceType: "social"`, `QualityScore`,
   `Extractor` and `FallbackStatus`.

7. **Cache correctly.** The scrape cache key already includes the mode and session fingerprint
   (`scrapeCacheKey` + `sessionCacheSuffix`). Add a social/browser mode component so a direct
   failure is not replayed for a proxied attempt, and vice versa.

### 6.5 Cost, memory and operations

- **Browser memory is the binding constraint.** The project already recorded that Cloud Run's
  default 512 Mi OOM-killed the container when Chromium launched, and deploy now uses
  `--memory 2Gi --cpu 2` (`HANDOVER.md`, deployment notes). A second Chromium adds roughly
  250–450 MB resident. That is affordable at 2 GiB only if social renders are capped to one at
  a time and the second browser is created lazily for person runs, not for every company run.
- **Bandwidth.** Residential proxies are billed per GB. Blocking images/media/fonts/stylesheets
  (`scraper_browser.go:43`) removes most bytes, but a modern social page still transfers
  0.5–3 MB of scripts and JSON. One page per run is the right default; zero is better when the
  snippet already answers the question.
- **Latency.** Social SPAs settle slowly; the default browser fallback timeout is 10 s
  (`scraper_browser.go:16`), while the ladder allows a longer render. A person run is 180 s by
  default (`RESEARCH_TIMEOUT_SECONDS`), so one social render plus synthesis fits, but it must
  be budget-gated like the ladder (`hardURLPostLadderMargin`).
- **Failure modes to instrument.** login redirect, challenge page, empty shell, rate limit
  (HTTP 429), and proxy authentication failure. Each should appear in the scrape report with
  its own reason, not as a generic “no usable content”.
- **Cache/negative cache.** A failed social fetch should be negative-cached briefly (the
  scraper's 15-minute negative entry) so retries do not burn proxy bandwidth.

### 6.6 Compliance and risk

- **Terms of service.** Meta and X restrict automated collection. Public search snippets are
  the low-risk path; page scraping with a logged-in session is the high-risk path and can get
  the operator's account restricted. This is a product decision, not a technical one, and it
  should be explicit in the UI (“public social context for outreach, not verification”) and in
  the run's audit trail.
- **robots.txt.** The repository has a `robotsGuard`, but it is used only by the legal clients
  (`tools/robots_guard.go`); the general scraper does not consult it. Before adding social
  fetches, either extend the guard to those hosts or record the deliberate policy decision.
  (Facebook/X disallow most non-API crawling, so the guard would likely block the fetch
  outright — which is itself a useful answer.)
- **Personal data.** A person dossier is already personal data. Under India's DPDP Act and
  the GDPR where applicable, keep the purpose limitation (professional outreach), minimise
  (no sensitive attributes, no contact harvesting from social), show sources, and support
  deletion/re-run. The existing “public personalization is not identity evidence” rule is the
  right framing; keep it and enforce it in QC.
- **No credential handling.** Never automate login, CAPTCHA or 2FA. If a session is needed,
  the operator supplies an exported cookie file, exactly as the LinkedIn flow already does
  (`COOKIE_FILE_PATH`), and the run reports when the session is rejected.
- **Attribution.** Every flavour item must keep its URL, platform, date, `identity_basis` and
  confidence. An unattributed interest is worse than no interest.

### 6.7 Recommendation

1. **Now:** implement Phase 1 (snippets-only social slots, tighter admission, dates). It is
   cheap, safe, and improves flavour immediately.
2. **Next:** fix the half-wired proxy by giving the *browser* a per-host egress path. This pays
   off for LinkedIn posts and public pages regardless of Facebook/X, and it is the prerequisite
   for any social page fetch.
3. **Only then:** add one proxied social page per run behind `SOCIAL_ENRICHMENT=1`, with the
   shape-aware ladder, the one-fetch cap, the scrape-report fields and the compliance banner.
4. **Prefer APIs** for X/Facebook post content; treat scraping as a fallback for public pages
   with no API, not as the primary route.

---

## 7. Appendix — anchors used in this review

| Concern | Anchor |
| :--- | :--- |
| Person plan | `internal/agent/person_plan.go:40`, `:41`, `:67`, `:68`, `:86` |
| Person research flow | `internal/agent/person.go:76`, `:114`, `:198`, `:316`, `:356`, `:563`, `:648`, `:726` |
| Person gate | `internal/agent/person_gate.go:109`, `:188`, `:291`, `:306`, `:317`, `:407` |
| Person QC | `internal/agent/person_qc.go:27`, `:179`, `:262` |
| Person resolve | `internal/agent/person_resolve.go:70`, `:151`, `:161`, `:282` |
| Name matching (partial ↔ full) | `internal/agent/validate.go:131`, `:155`; `person_gate.go:188`; `person_resolve.go:161`; `person.go:51` |
| Company plan | `internal/agent/company.go:1234`, `:1261`, `:1336`, `:1421`, `:1443` |
| Company ranking | `internal/agent/company.go:1524`, `:1567`, `:1724`, `:1948`, `:2061` |
| Company scrape selection | `internal/agent/company.go:2881`, `:2891` |
| Company QC / validation | `internal/agent/company.go:729`, `validate.go:575`, `:887`, `:975` |
| Company gap fill | `internal/agent/entity_lookup.go:48` |
| Evidence and dates | `internal/models/research.go`, `internal/agent/evidence_dates.go:296` |
| Sources and slots | `internal/agent/sources.go:26`, `:171` |
| Proxy transport | `internal/tools/egress_proxy.go:24`, `:47`; `internal/tools/scraper.go:291`, `:449`, `:1370` |
| Browser launch | `internal/tools/scraper_browser.go:43`, `:122`, `:132`; `internal/tools/weedoo.go:235` |
| URL shapes | `internal/tools/hardshape.go:40`, `:67`, `:82` |
| Deployment proxy wiring | `deploy-gcp.ps1:43` |
| Person UI / export | `internal/web/templates/fragments.html:770`; `internal/export/markdown.go:496`, `:606`, `:628` |

### Document maintenance

Line anchors will drift as the code changes; the function names are the stable references
(`buildPersonPlan`, `planQueries`, `enforceEntityQueries`, `rankResults`,
`selectScrapeCandidates`, `fillEntityGaps`, `reviewChart`, `validateChart`,
`annotateEvidenceDates`, `scraperProxyForRequest`, `hardLadderSteps`). Update this document when
a proposal lands, and keep the “what exists today” facts in §6.1 in step with
`egress_proxy.go` — they are the part most likely to go stale silently.
