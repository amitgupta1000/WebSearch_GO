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

Every node carries `status` (`proposed` | `approved` | `rejected`), `source` (`seed`, `hs`, `echa`,
`trade-db`, `operator`), `confidence` and an optional `expert_note`.

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

# 4. add the trade DB's canonical product names as proposed substances
go run ./cmd/ontology harvest -out internal/ontology/data/harvest.json   # needs TRADE_DB_API_URL
go run ./cmd/ontology merge -base internal/ontology/data/chemicals.json -patch internal/ontology/data/harvest.json -out internal/ontology/data/chemicals.json
```

A correction replaces the field it names (slices wholesale). A correction for an unknown ID is an
addition and must carry `kind` and `label`. Errors block the runtime load; warnings are the
review work queue.

## API surface

```go
resolution := ontology.Resolve("oil blending")
// Ambiguous=true, Families[0]=cat:lubricants, then fuel oils, crude oil, oleochemical feedstocks
// Processes=[proc:blending], SearchTerms=["oil blending","lubricant blending",…]
// EndProducts=[end:greases] — the leaves that identify end-user companies

lexicon, _ := document.Subgraph(resolutionSeedIDs, 2).JSON() // bounded graph handed to the LLM
chain := document.Expand([]string{"sub:n-butanol"}, ontology.Downstream, 2)
matches, unmatched := document.CanonicalTradeMatch(dbCanonicalNames)
```

`Options{RequireApproved: true}` is the post-curation gate: only nodes the expert approved resolve.
The seed ships entirely `proposed`, so the gate stays off until the curated file lands.

## Where it will be wired

Once the curated document is in place, these call sites read the graph instead of their tables:
`demandFallbackCategory`, `validateDemandCategory`/`demandCategoryLooksRelated`,
`demandOilFamilies`, `demandStageCategoryRules`, `demandCategoryConsumerFamilies`,
`demandKnownDirectInputStages`, `knownStageProducts`, `marketActivitySearchTerms`,
`marketCategoryLanes`, and the trade-DB product/HS scoping.
