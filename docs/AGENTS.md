# Agent package reference

The repository package is `internal/agent` (singular). The files below contain
the research agents and the shared pipeline helpers; test files are omitted.

## Shared runtime and orchestration

- [engine.go](../internal/agent/engine.go) — Defines the search, scraping, LLM, caching, event, and bounded-execution runtime used by all agents.
- [hub.go](../internal/agent/hub.go) — Tracks research sessions, their status, results, events, and lifecycle.
- [timing.go](../internal/agent/timing.go) — Records elapsed time for research phases and exposes phase timing summaries.
- [hardurl.go](../internal/agent/hardurl.go) — Provides a bounded escalation ladder for URLs that normal scraping cannot read.
- [evidence_dates.go](../internal/agent/evidence_dates.go) — Extracts dates that source content explicitly proves about its evidence.
- [sources.go](../internal/agent/sources.go) — Supplies non-LinkedIn source searches and evidence helpers for person research.

## Activity and entity resolution

- [activity_api.go](../internal/agent/activity_api.go) — Exposes activity normalization and ontology-backed resolution for HTTP and CLI callers.
- [activity_resolution.go](../internal/agent/activity_resolution.go) — Maps an operator’s activity wording to ontology families, categories, search terms, and research paths.
- [entity.go](../internal/agent/entity.go) — Applies deterministic company-identity checks to queries, search results, evidence, and person rows.
- [entity_lookup.go](../internal/agent/entity_lookup.go) — Runs company-anchored lookups to fill or validate thin research rosters.
- [entity_resolve.go](../internal/agent/entity_resolve.go) — Finds and ranks candidate company identities for operator confirmation before a run.
- [entity_source.go](../internal/agent/entity_source.go) — Classifies discovery sources and separates useful discovery evidence from proof of company identity.
- [related_entity.go](../internal/agent/related_entity.go) — Discovers confirmed company relationships and follows leadership into related companies.

## Company and person research

- [company.go](../internal/agent/company.go) — Runs the multi-phase company discovery, evidence collection, hierarchy mapping, and profile synthesis workflow.
- [company_metrics.go](../internal/agent/company_metrics.go) — Extracts evidence-backed employee, turnover, and financial-year metrics without inventing values.
- [company_roster.go](../internal/agent/company_roster.go) — Builds and merges the durable company research roster from organizational-chart results.
- [rocketreach_roster.go](../internal/agent/rocketreach_roster.go) — Merges on-demand full RocketReach profiles into existing company roster rows.
- [profile_fetch.go](../internal/agent/profile_fetch.go) — Reuses an operator-supplied authenticated session for a cheap professional-profile fetch before rendering.
- [person.go](../internal/agent/person.go) — Provides person-specific social evidence and date handling used by the person dossier workflow.
- [person_gate.go](../internal/agent/person_gate.go) — Filters person evidence and rows against the confirmed identity and employer.
- [person_plan.go](../internal/agent/person_plan.go) — Creates a multi-angle, identity-anchored search plan for person research.
- [person_qc.go](../internal/agent/person_qc.go) — Performs post-synthesis quality control on person dossiers and removes unsupported identities or employers.
- [person_resolve.go](../internal/agent/person_resolve.go) — Finds and ranks candidate people for operator confirmation using role, employer, and location context.
- [validate.go](../internal/agent/validate.go) — Performs a freshness and independent-source validation pass over a completed company chart.

## Market and buyer landscape

- [market_landscape.go](../internal/agent/market_landscape.go) — Runs the seller landscape workflow to discover, verify, classify, and report companies by activity and geography.
- [market_classification.go](../internal/agent/market_classification.go) — Assigns company roles and geographic presence from collected evidence.
- [market_geography.go](../internal/agent/market_geography.go) — Detects evidence of an actual operating presence in a requested geography.
- [market_geography_data.go](../internal/agent/market_geography_data.go) — Stores concrete operating-place vocabulary used by geographic classification.
- [market_geography_regions.go](../internal/agent/market_geography_regions.go) — Stores regional operating terms used to distinguish regional from global-only pages.
- [market_groups.go](../internal/agent/market_groups.go) — Groups verified landscape companies into role and geography sections for reporting.
- [market_names.go](../internal/agent/market_names.go) — Cleans and normalizes company names extracted from search titles and pages.
- [market_qc.go](../internal/agent/market_qc.go) — Defines editable QC policies and converts reviewer output into per-company verdicts.
- [market_query_review.go](../internal/agent/market_query_review.go) — Adversarially reviews planned seller and buyer queries before they run.
- [demand_chain.go](../internal/agent/demand_chain.go) — Runs the buyer landscape workflow, inferring value-chain stages and verifying consuming companies and demand signals.
- [deepsearch.go](../internal/agent/deepsearch.go) — Runs open-ended, multi-round research: decomposition, query composition, relevance selection, extraction, review, and cited synthesis.

## News and legal research

- [news.go](../internal/agent/news.go) — Builds a whitelist-first, freshness-bounded current-news brief with citations and structured sentiment.
- [legal.go](../internal/agent/legal.go) — Runs bounded case-law research for a confirmed company and selected people.
- [legal_dockets.go](../internal/agent/legal_dockets.go) — Groups related legal matters into dockets and derives deterministic risk summaries.

## Trade research

- [trade_product.go](../internal/agent/trade_product.go) — Resolves operator activity wording to the trade database’s standardized product vocabulary.
- [trade_data.go](../internal/agent/trade_data.go) — Defines trade-source custody levels and first-party customs-record evidence semantics.
- [trade_profile.go](../internal/agent/trade_profile.go) — Builds an aggregated Crystal customs profile for a company.
- [trade_background.go](../internal/agent/trade_background.go) — Adds the company’s aggregated customs profile and product mix to research output.
- [trade_records.go](../internal/agent/trade_records.go) — Queries and reports first-party Crystal trade records for product-ranked partners.
