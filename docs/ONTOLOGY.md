# Chemical value-chain ontology

The landscape modes expand an operator activity ("oil blending", "n-butanol") through a
curated graph instead of hand-maintained Go tables. The graph is data, the reviewer curates it,
and the code only reads it.

## The document

`internal/ontology/data/Chem_KG.json` is the single source of truth (schema version 1). It is
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
go run ./cmd/ontology review -file internal/ontology/data/Chem_KG.json -out internal/ontology/data/REVIEW.md

# 2. what came back (whole file, or a corrections-only block)
go run ./cmd/ontology diff   -before internal/ontology/data/Chem_KG.json -after <returned file>
go run ./cmd/ontology merge  -base   internal/ontology/data/Chem_KG.json -patch <returned file> -out internal/ontology/data/Chem_KG.json

# 3. validate + refresh the artifacts
go run ./cmd/ontology lint   -file internal/ontology/data/Chem_KG.json -out internal/ontology/data/LINT.md
go run ./cmd/ontology review -file internal/ontology/data/Chem_KG.json -out internal/ontology/data/REVIEW.md

# 4. sign off: mark every proposed node approved and record who did it
go run ./cmd/ontology approve -file internal/ontology/data/Chem_KG.json -by "domain expert"

# 5. add the trade DB's canonical product names as proposed substances
go run ./cmd/ontology harvest -out internal/ontology/data/harvest.json   # needs TRADE_DB_API_URL
go run ./cmd/ontology merge -base internal/ontology/data/Chem_KG.json -patch internal/ontology/data/harvest.json -out internal/ontology/data/Chem_KG.json
```

Two read-only commands help the roundtrip: `go run ./cmd/ontology explain -activity "n-butanol" [-mode seller|buyer] [-json]` prints the resolution a run would take (deterministic, no model call), and `go run ./cmd/ontology graph [-activity X | -seed ID] [-out kg.html|.svg|.json]` draws the knowledge graph.

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

Ambiguous families ("oil", "oleochemicals") are **data**: the graph marks the hub
`hypernym: true` with ranked `hyponyms`, and nothing in the ontology package knows what
an oil is. At runtime the hub is resolved in two steps:

1. **Qualifier first** — the activity's own words pick among the hub's variants
   (`soybean oil` → Oleochemical feedstocks, `white oil` → Lubricants, `fuel oil
   blending` → Fuel oils). With no qualifier the hub's rank stands ("oil blending" →
   lubricants) and the seller mode may ask the model to choose among the **listed**
   variants using the requested geography; an out-of-list answer keeps the rank order.
2. **Vocabulary isolation** — a shared process indexes every variant's phrasing
   ("blending" carries lubricant, fuel and crude phrases), so `searchTerms()` drops any
   phrase naming a **sibling variant** of the resolved family
   (`Ontology.SiblingVariants`, derived from the hub's hyponyms). A fuel-oil run never
   buys "lubricant blending", and an oil-blending run never buys "fuel oil blending".

The one piece of oil knowledge left in code is `demandOilFamilies`: a qualifier table
for oil names the graph has no node for ("soybean oil", "white oil", "transformer
oil"). It ranks variants and answers the reviewer anchor only when the graph is silent;
extend the graph, never the table.

## Seeing the graph (on demand)

Three surfaces, one renderer (`internal/ontology/view.go` + `render.go`), so a link, a
saved file and an API response always agree:

```bash
# the console page (also linked from the console header)
open http://localhost:8080/ontology
http://localhost:8080/ontology?seed=oil%20blending&hops=2
http://localhost:8080/ontology?seed=cat:lubricants&kind=substance,category&edge=consumed_in

# the same selection, machine-readable
curl -s 'localhost:8080/api/ontology/graph?seed=n-butanol&hops=2'

# a file, without a server (curation, review notes, offsite)
go run ./cmd/ontology graph -activity "oil blending" -hops 2 -out kg.html
go run ./cmd/ontology graph -seed cat:lubricants -out kg.svg
go run ./cmd/ontology graph -kind category,process -json -out kg.json
```

- **Seed** accepts a node id, a label, an alias, or an *activity*: `-activity "oil
  blending"` seeds the nodes the resolver names (the `oils` hub and the `blending`
  process), which is what makes "show me what the graph knows about this query" a
  useful question. An unplaceable seed draws the whole graph and says so.
- **Layout is deterministic**: rings are taxonomy depth for the whole graph (root → 5
  top categories → 33 → 61 → 62 nodes) or hop distance around a seed, sorted by label,
  with odd rings staggered. The same document always draws the same picture, so two
  reviews can be compared.
- **No scripts, no CDN**: the SVG is built server-side and every node is an `<a>`, so a
  click recentres the view and the entire filtered state lives in the URL. A saved page
  keeps the drawing and drops the form and the links (there is no server to submit to).
- Colours are by kind; edge families are coloured and, for `hypernym`, dashed. Proposed
  nodes are outlined in pink, so an unapproved row is visible before it is trusted.

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

Specificity is read from **the phrase that matched**, never from the node's longest entry name: a
one-word alias is the least specific reading of an activity. `crude palm oil` names palm oil
(through its own alias of that spelling), so the `crude` alias on `Crude oil` is absorbed by the
longer span rather than reported as a second product — letting it win resolves a palm-oil grade to
the crude-oil family, exactly the drift the graph exists to prevent. The same rule absorbs a
one-word match whenever a multi-word match shares the activity (`crude glycerin` is a glycerin
grade). `lint` warns when a one-word substance entry opens another substance's entry name (the
data-side form of the hazard), and `TestEveryEntryNameResolvesToItsOwnNode` sweeps every entry name
in the shipped document, so a generic alias added anywhere is caught by the graph itself rather
than by whoever remembers to write the case.

## Operator confirmation

Resolution is suggestion-first, like the trade DB's product and entity pickers.
The API answer carries ranked `candidates`: the current match first, then the
alternative nodes by shared content words, then the ranked variants of an
ambiguous hub (before the alternatives when the activity is ambiguous, because
there the variants are the useful choice). Every candidate names the family it
would drive, so the choice is about the research path, not just a word.

Sending `node_id` back confirms one. `Ontology.ResolveNode` anchors on that node
directly -- the activity text is never re-read, because the text is what produced
the wrong node -- the resolution reports `path: "operator"` with a warning naming
the confirmation, and the pick is remembered for the activity for the life of the
process, so the run that follows reuses it without resending the field. An
unknown or unapproved id is reported and ignored; the text path resolves instead.
A confirmed node also skips the model's family-choice call: the operator already
chose. Wiring: `POST /api/ontology/match` (chips under the activity field) and
`node_id` on `POST /api/ontology/resolve`, plus `ontology_node` on both landscape
queries.

## Where it is wired

- **Seller Landscape** (`market_landscape.go`): `resolveActivity` (hub ranking included) →
  `chooseOntologyFamily` when the family is still ambiguous → `marketCategoryFromResolution`
  (graph category, or the model's inference for an unmodelled activity) →
  `marketDiscoverySearchesWithTerms` (the graph's family-safe phrases join the discovery
  lanes; a phrase carrying the lane's own role word is not compounded with it). The
  adversarial reviewer's independent anchor (`marketQueryFamilyAnchor`) is resolved from the
  same graph.
- **Buyer Landscape** (`demand_chain.go`): `resolveActivity` → `demandStagesFromOntology` →
  `demandOntologyGuidance` (the stages are injected into the inference prompt) → the graph's
  stages and consuming family stand in when the model returns nothing or the inference fails.
- Still table-driven: the trade-DB product/HS scoping, and the legacy tables
  (`demandFallbackCategory`, `validateDemandCategory`, `demandStageCategoryRules`,
  `demandCategoryConsumerFamilies`, `demandKnownDirectInputStages`, `knownStageProducts`) —
  they are the unmodelled branch's fallbacks, and `demandOilFamilies` now only ranks hub
  variants for oil names the graph does not model.

