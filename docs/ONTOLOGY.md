# Chemical value-chain ontology

The landscape modes expand an operator activity ("oil blending", "n-butanol") through a
curated graph instead of hand-maintained Go tables. The graph is data, the reviewer curates it,
and the code only reads it.

## The document

`internal/ontology/data/chemicals.json` is the single source of truth (schema version 1). It is
embedded in the binary, strictly decoded (unknown fields are an error), linted on load, and
regenerated only by the tooling below.

### Node kinds (the operator's entry tiers + the value-chain leaves)

| kind | meaning | example |
|---|---|---|
| `substance` | canonical chemical/feedstock an operator can type, with aliases and HS codes | `sub:n-butanol` (`butanol`, `butyl alcohol`, `nba`; 2905.13) |
| `category` | petrochemical/oleochemical/inorganic family, nestable through `parent` | `cat:esters → cat:acrylates` |
| `process` | transformation operation | `proc:blending`, `proc:esterification` |
| `end-product` | industrial article whose makers/consumers are the companies we want | `end:greases` ("grease manufacturer", "grease plant") |

### Edges

| field | direction | example |
|---|---|---|
| `parent` | sub-category → family | `cat:acrylates → cat:esters` |
| `categories` | substance → families | `sub:toluene → cat:aromatics, cat:aromatic-solvents` |
| `derived_to` | downstream chemistry | `sub:n-butanol → sub:butyl-acrylate` |
| `consumed_in` | input → process / industrial product | `sub:acrylic-acid → end:superabsorbent-polymer` |
| `operates_on` / `produces` | process inputs / outputs | `proc:blending → cat:lubricants` |
| `consumes` | end-product inputs | `end:pvc-compound → cat:plasticizers` |
| `hypernym` / `hyponyms` | an ambiguous family and its **ranked** variants | `cat:oils → lubricants > fuel oils > crude oil > oleochemical feedstocks` |

Every node carries `status` (`proposed` | `approved` | `rejected`), `source` (`seed`, `llm-review`,
`hs`, `echa`, `trade-db`, `operator`), `confidence` and an optional `expert_note`.

## Review roundtrip

```powershell
# 1. what the domain expert reviews
go run ./cmd/ontology review -file internal/ontology/data/chemicals.json -out internal/ontology/data/REVIEW.md

# 2. what came back (whole file, or a corrections-only block)
go run ./cmd/ontology diff   -before internal/ontology/data/chemicals.json -after <returned file>
go run ./cmd/ontology merge  -base   internal/ontology/data/chemicals.json -patch <returned file> -out internal/ontology/data/chemicals.json

# 3. validate + refresh the artifacts
go run ./cmd/ontology lint   -file internal/ontology/data/chemicals.json -out internal/ontology/data/LINT.md
go run ./cmd/ontology review -file internal/ontology/data/chemicals.json -out internal/ontology/data/REVIEW.md

# 4. sign off: mark every proposed node approved and record who did it
go run ./cmd/ontology approve -file internal/ontology/data/chemicals.json -by "domain expert"

# 5. add the trade DB's canonical product names as proposed substances
go run ./cmd/ontology harvest -out internal/ontology/data/harvest.json   # needs TRADE_DB_API_URL
go run ./cmd/ontology merge -base internal/ontology/data/chemicals.json -patch internal/ontology/data/harvest.json -out internal/ontology/data/chemicals.json
```

A correction replaces the field it names (slices wholesale). A correction for an unknown ID is an
addition and must carry `kind` and `label`. Errors block the runtime load; warnings are the
review work queue.

## Resolution flow (the runtime contract)

Both landscape modes open with the same three-branch resolution
(`internal/agent/activity_resolution.go`). It is the only place an activity becomes a
research path.

1. **Graph match** — `document.Resolve(activity)` returns families, processes and search
   terms. The run is graph-guided: the family supplies the category lane, the graph's
   indexed phrases join the discovery terms, and the buyer mode gets the consuming stages
   as prompt guidance (and as the chain if the model returns nothing). No inference call
   is spent on the category.
2. **Alias match** — when the graph has nothing, one constrained model call may map the
   wording onto an **approved** node (`{match_id, confidence, rationale}`). Only `high` or
   `medium` confidence and an existing approved ID are accepted; anything else falls
   through. On success the run proceeds exactly as branch 1, and the report records
   "mapped X to label (id)" plus the rationale so curation can add the alias.
3. **Unmodelled** — no match, no acceptable alias: the model plans the path as before, and
   the report warns that the activity is unmodelled so it can be curated.

Seller mode reads the resolution as *category* + *category lanes* + *discovery terms*;
buyer mode reads it as *consuming stages*. The buyer's category is the **consuming**
family (`downstreamCategory`) — "n-butanol" broadens on acrylates, not on alcohol
solvents — while the seller's is the product family.

Ambiguous families ("oil", "oleochemicals") are ranked, and the seller mode may ask the
model to choose among the **listed variants** using the requested geography; a failure or
an out-of-list answer keeps the graph's rank order.

## API surface

```go
// Runtime (internal/agent): the same function the modes call.
resolution := document.Resolve("oil blending", ontology.DefaultOptions)
// Ambiguous=true, Families[0]=cat:lubricants, then fuel oils, crude oil, oleochemical feedstocks
// Processes=[proc:blending], SearchTerms=["oil blending","lubricant blending",…]
// EndProducts=[end:greases] — the leaves that identify end-user companies

lexicon, _ := document.Subgraph(resolutionSeedIDs, 2).JSON() // bounded graph handed to the LLM
chain := document.Expand([]string{"sub:n-butanol"}, ontology.Downstream, 2)
matches, unmatched := document.CanonicalTradeMatch(dbCanonicalNames)
```

```go
// Public resolution API (used by the console endpoint and the CLI).
view, err := agent.ResolveActivity(ctx, engine, agent.ActivityQuery{
    Activity: "oil blending", Geography: "Middle East", Mode: agent.ActivityModeSeller,
})
// view.Path        "ontology" | "alias" | "unmodelled"
// view.Category    "Lubricants"; view.CategoryLanes ["Lubricants manufacturers", …]
// view.Families    ranked variants with their reason (hypernym/subject/downstream)
// view.Stages      buyer mode: the consuming stages with operators and products
// view.Warnings    the audit lines the report carries
```

`ResolveActivityAgainst(ctx, engine, document, query)` resolves against a candidate file,
which is how curation checks a node before it is approved. A nil engine keeps the
resolution deterministic (the alias branch is skipped).

Transports:

```bash
# HTTP (JSON in, JSON out; starts no research, spends no search)
curl -s localhost:8080/api/ontology/resolve \
  -d '{"activity":"oil blending","geography":"Middle East","mode":"seller"}'

# CLI (deterministic unless an engine is wired in)
go run ./cmd/ontology explain -activity "oil blending" -geography "Middle East"
go run ./cmd/ontology explain -activity "n-butanol" -mode buyer -json
```

`Options{RequireApproved: true}` is the runtime gate and it is **on by default**: the shipped
document is approved (`revision: …-approved`, `reviewed_by` recorded), so only reviewed chemistry can
resolve. A node that is still `proposed` is excluded, and the resolution says so ("N matching node(s)
are not approved yet") instead of claiming the activity is unmodelled — harvest and curation add
rows as `proposed`, and they resolve only after `approve`.

Matching is exact on labels, aliases and a process's `search_terms` (the indexed phrasing of the
activity — "oil blending" names the blending process). When nothing matches, an activity that is the
**leading token sequence** of a category or process label still resolves (`saponification` →
"Saponification and fatty acid neutralization"), but never for substances, so `hydrogen` cannot drift
to `Hydrogen peroxide`. Downstream families carry a hop distance of 1, so a broad hub family cannot
fan its whole membership into the end-product walk.

## Where it is wired

- **Seller Landscape** (`market_landscape.go`): `resolveActivity` → `chooseOntologyFamily` when the
  family is ambiguous → `marketCategoryFromResolution` (graph category, or the model's inference for
  an unmodelled activity) → `marketDiscoverySearchesWithTerms` (the graph's indexed phrases join the
  discovery lanes).
- **Buyer Landscape** (`demand_chain.go`): `resolveActivity` → `demandStagesFromOntology` →
  `demandOntologyGuidance` (the stages are injected into the inference prompt) → the graph's stages
  and consuming family stand in when the model returns nothing or the inference fails.
- Not yet switched: the trade-DB product/HS scoping and the legacy category tables
  (`demandFallbackCategory`, `validateDemandCategory`, `demandStageCategoryRules`,
  `demandCategoryConsumerFamilies`, `demandKnownDirectInputStages`, `knownStageProducts`). They
  remain as the fallbacks the unmodelled branch uses; `ValidateDemandCategory` still rejects
  over-broad categories before the graph family is consulted.

