# Chemical value-chain review — curated-2026-11-18-approved

Reviewer: **Operator sign-off (review accepted 2026-11-18)**

Notes: Merge of edited_ontology.json (source=llm-review) with the review-application fixes: duplicate entry names removed, one-way edges closed, formulation outputs completed, urea re-homed, the fuel-oil -> carbon-black edge dropped (it leaked tire makers into fuel-oil research), and the notes' unimplemented items resolved: added polythene + aqueous ammonia HS codes, sulphur/phosphate-rock feedstocks, phosgene as the chlorine -> isocyanate bridge, and two value-chain leaves (plastics converting, pulp and paper). Declined with reasons: a bare 'peroxide' alias (matches benzoyl/cumene peroxide), cat:organic-acids (would fragment the ester/acid taxonomy; acrylic and acetic acid sit in chemical-intermediates), and proc:oxidation (a generic reaction with no operator vocabulary). Still pending a human domain expert: every node remains status=proposed. Review accepted by Operator sign-off (review accepted 2026-11-18); the runtime gate resolves only approved nodes.

## How to review this

The JSON beside this file is the single source of truth; this sheet is its readable view.

1. Every row is `status: proposed` until someone confirms it. Flip it to `approved` (or `rejected`) with a note.
2. `source` records where a relation came from: `seed` (authored, unverified), `hs` (customs nomenclature), `echa`, `trade-db`, `operator` (you pinned it).
3. Return **either** the whole edited JSON **or** a `corrections` block keyed by node ID — a present field replaces that field wholesale, so send the list you want to end up with.
4. `derived_to` is downstream chemistry (feedstock → derivative). `consumed_in` is the value-chain edge to the process or industrial product that uses it. The leaves there drive company discovery.

## Summary

- substances: **83** · categories: **43** · processes: **17** · end-products: **19**
- status: approved **162**, proposed **0**, rejected **0**

## Category tree

- `cat:chemicals` Chemicals _(intermediate)_
  - `cat:coal-products` Coal products _(intermediate)_
  - `cat:inorganics` Inorganics _(intermediate)_
    - `cat:acids` Inorganic acids
    - `cat:alkalis` Alkalis
  - `cat:oils` Oils — **ambiguous**, ranked: Lubricants > Fuel oils > Crude oil > Oleochemical feedstocks
  - `cat:oleochemicals` Oleochemicals _(intermediate)_
    - `cat:biodiesel` Biodiesel _(intermediate)_
    - `cat:fatty-acids` Fatty acids _(intermediate)_
    - `cat:fatty-alcohols` Fatty alcohols _(intermediate)_
    - `cat:oleochemical-feedstocks` Oleochemical feedstocks _(intermediate)_
    - `cat:soap` Soap bases _(intermediate)_
  - `cat:petrochemicals` Petrochemicals _(intermediate)_
    - `cat:amines` Amines _(intermediate)_
    - `cat:aromatics` Aromatics _(intermediate)_
    - `cat:chemical-intermediates` Organic chemical intermediates _(intermediate)_
    - `cat:chlorinated-monomers` Chlorinated monomers _(intermediate)_
    - `cat:esters` Esters _(intermediate)_
      - `cat:acetates` Acetates _(intermediate)_
      - `cat:acrylates` Acrylates _(intermediate)_
      - `cat:plasticizers` Plasticizers _(intermediate)_
    - `cat:fuel-oils` Fuel oils
    - `cat:glycols` Glycols _(intermediate)_
    - `cat:isocyanates` Isocyanates _(intermediate)_
    - `cat:lubricants` Lubricants _(intermediate)_
      - `cat:base-oils` Base oils _(intermediate)_
      - `cat:finished-lubricants` Finished lubricants
    - `cat:olefins` Olefins _(intermediate)_
    - `cat:polymers` Polymers and resins _(intermediate)_
      - `cat:epoxies` Epoxy resins
      - `cat:polyolefins` Polyolefins
      - `cat:polyurethanes` Polyurethanes _(intermediate)_
      - `cat:pvc` PVC
      - `cat:styrenics` Styrenic polymers
    - `cat:solvents` Solvents _(intermediate)_
      - `cat:alcohol-solvents` Alcohol solvents
      - `cat:aromatic-solvents` Aromatic solvents
      - `cat:ester-solvents` Ester solvents
      - `cat:glycol-ethers` Glycol ethers _(intermediate)_
      - `cat:ketones` Ketones
    - `cat:surfactants` Surfactants _(intermediate)_
      - `cat:anionic-surfactants` Anionic surfactants
      - `cat:nonionic-surfactants` Nonionic surfactants

## Substances

### `sub:2-ethylhexanol` — 2-Ethylhexanol
- aliases: 2-eh; 2eh; isooctyl alcohol
- hs: 2905.16
- families: cat:alcohol-solvents; cat:chemical-intermediates
- derived to: sub:dop
- consumed in: proc:esterification
- status: **approved**, source: llm-review, confidence: high

### `sub:acetic-acid` — Acetic acid
- aliases: glacial acetic acid; ethanoic acid; gaa
- hs: 2915.21
- families: cat:chemical-intermediates
- derived to: sub:ethyl-acetate; sub:butyl-acetate
- consumed in: proc:esterification
- status: **approved**, source: llm-review, confidence: high

### `sub:acetone` — Acetone
- aliases: propanone
- hs: 2914.11
- families: cat:ketones
- derived to: sub:bisphenol-a; sub:methyl-methacrylate; sub:isopropanol
- consumed in: end:paints-coatings; end:adhesives-sealants
- status: **approved**, source: llm-review, confidence: high

### `sub:acrylic-acid` — Acrylic acid
- aliases: prop-2-enoic acid; glacial acrylic acid
- hs: 2916.11
- families: cat:acrylates; cat:chemical-intermediates
- derived to: sub:butyl-acrylate
- consumed in: proc:polymerization; proc:esterification; end:superabsorbent-polymer
- status: **approved**, source: llm-review, confidence: high

### `sub:ammonia` — Ammonia
- aliases: anhydrous ammonia; nh3
- hs: 2814.10; 2814.20
- families: cat:inorganics
- derived to: sub:urea; sub:nitric-acid; sub:ethanolamines
- consumed in: end:fertilizers
- status: **approved**, source: llm-review, confidence: high

### `sub:aniline` — Aniline
- aliases: aminobenzene
- hs: 2921.41
- families: cat:amines
- derived to: sub:mdi
- status: **approved**, source: llm-review, confidence: high

### `sub:base-oil` — Base oil
- aliases: base stock; group i base oil; group ii base oil; group iii base oil
- hs: 2710.19
- families: cat:base-oils
- consumed in: proc:blending; end:greases; end:metalworking-fluids
- status: **approved**, source: seed, confidence: high

### `sub:benzene` — Benzene
- aliases: benzol
- hs: 2902.20
- families: cat:aromatics
- derived to: sub:ethylbenzene; sub:cumene; sub:linear-alkylbenzene; sub:aniline
- status: **approved**, source: llm-review, confidence: high — note: Removed cat:aromatic-solvents: benzene is regulated/banned as an industrial solvent due to carcinogenicity.

### `sub:bisphenol-a` — Bisphenol A
- aliases: bpa; diphenylolpropane
- hs: 2907.23
- families: cat:chemical-intermediates
- derived to: sub:epoxy-resin
- status: **approved**, source: llm-review, confidence: high

### `sub:butadiene` — Butadiene
- aliases: 1 3 butadiene
- hs: 2901.24
- families: cat:olefins
- consumed in: proc:polymerization; end:tires-rubber
- status: **approved**, source: llm-review, confidence: high

### `sub:butyl-acetate` — Butyl acetate
- aliases: n-butyl acetate; nbac
- hs: 2915.33
- families: cat:acetates; cat:ester-solvents
- consumed in: end:paints-coatings
- status: **approved**, source: seed, confidence: high

### `sub:butyl-acrylate` — Butyl acrylate
- aliases: n-butyl acrylate
- hs: 2916.12
- families: cat:acrylates
- consumed in: end:adhesives-sealants; end:paints-coatings
- status: **approved**, source: seed, confidence: high

### `sub:butyl-glycol-ether` — Butyl glycol ether
- aliases: butyl glycol; ethylene glycol monobutyl ether; 2-butoxyethanol; butyl cellosolve
- hs: 2909.43
- families: cat:glycol-ethers
- consumed in: end:paints-coatings
- status: **approved**, source: llm-review, confidence: high

### `sub:carbon-black` — Carbon black
- hs: 2803.00
- families: cat:inorganics
- consumed in: end:tires-rubber
- status: **approved**, source: llm-review, confidence: high

### `sub:caustic-soda` — Caustic soda
- aliases: sodium hydroxide; lye; caustic; caustic soda flakes; caustic soda lye
- hs: 2815.11; 2815.12
- families: cat:alkalis
- derived to: sub:soap-noodles
- consumed in: end:soap; proc:saponification; end:pulp-paper
- status: **approved**, source: llm-review, confidence: high

### `sub:chlorine` — Chlorine
- hs: 2801.10
- families: cat:inorganics
- derived to: sub:vinyl-chloride; sub:epichlorohydrin; sub:hydrochloric-acid; sub:phosgene
- status: **approved**, source: llm-review, confidence: high

### `sub:coal` — Coal
- aliases: thermal coal; coking coal; bituminous coal
- hs: 2701.12; 2701.19; 2701.11
- families: cat:coal-products
- derived to: sub:ammonia; sub:methanol; sub:benzene
- status: **approved**, source: llm-review, confidence: high

### `sub:coconut-oil` — Coconut oil
- aliases: copra oil; cno
- hs: 1513.11; 1513.19
- families: cat:oleochemical-feedstocks
- derived to: cat:fatty-acids; sub:glycerin; cat:biodiesel
- consumed in: proc:fat-splitting; proc:transesterification
- status: **approved**, source: llm-review, confidence: high

### `sub:crude-oil` — Crude oil
- aliases: petroleum crude; crude
- hs: 2709.00
- families: cat:petrochemicals
- derived to: sub:naphtha; sub:fuel-oil; sub:diesel; sub:base-oil
- consumed in: proc:refining; proc:blending
- status: **approved**, source: llm-review, confidence: high

### `sub:cumene` — Cumene
- aliases: isopropylbenzene
- hs: 2902.70
- families: cat:aromatics
- derived to: sub:phenol; sub:acetone
- status: **approved**, source: seed, confidence: high

### `sub:dibutyl-phthalate` — Dibutyl phthalate
- aliases: dbp
- hs: 2917.34
- families: cat:plasticizers
- consumed in: end:pvc-compound
- status: **approved**, source: seed, confidence: high

### `sub:diesel` — Diesel
- aliases: gasoil; gas oil; ulsd
- hs: 2710.19
- families: cat:fuel-oils
- consumed in: end:biodiesel-blend
- status: **approved**, source: llm-review, confidence: high

### `sub:dinp` — Diisononyl phthalate
- aliases: dinp
- hs: 2917.33
- families: cat:plasticizers
- consumed in: end:pvc-compound
- status: **approved**, source: llm-review, confidence: high — note: Corrected HS code from 2917.34 to 2917.33 (Dinonyl or didecyl orthophthalates).

### `sub:dop` — Dioctyl phthalate
- aliases: dop; dehp; bis(2-ethylhexyl) phthalate
- hs: 2917.32
- families: cat:plasticizers
- consumed in: end:pvc-compound
- status: **approved**, source: llm-review, confidence: high — note: Corrected HS code from 2917.33 to 2917.32 (Dioctyl orthophthalates).

### `sub:epichlorohydrin` — Epichlorohydrin
- aliases: ech
- hs: 2910.30
- families: cat:chlorinated-monomers; cat:chemical-intermediates
- derived to: sub:epoxy-resin
- status: **approved**, source: llm-review, confidence: high

### `sub:epoxy-resin` — Epoxy resin
- aliases: epoxy
- hs: 3907.30
- families: cat:epoxies
- consumed in: end:paints-coatings; end:adhesives-sealants
- status: **approved**, source: llm-review, confidence: high

### `sub:ethanol` — Ethanol
- aliases: ethyl alcohol; industrial ethanol; denatured alcohol
- hs: 2207.10; 2207.20
- families: cat:alcohol-solvents
- derived to: sub:ethyl-acetate
- consumed in: proc:esterification; end:personal-care; end:paints-coatings
- status: **approved**, source: llm-review, confidence: high

### `sub:ethanolamines` — Ethanolamines
- aliases: mea; dea; tea; monoethanolamine; diethanolamine; triethanolamine
- hs: 2922.11; 2922.12; 2922.15
- families: cat:amines
- consumed in: end:personal-care; end:metalworking-fluids; end:detergents
- status: **approved**, source: llm-review, confidence: high

### `sub:ethyl-acetate` — Ethyl acetate
- aliases: etac
- hs: 2915.31
- families: cat:acetates; cat:ester-solvents
- consumed in: end:paints-coatings
- status: **approved**, source: seed, confidence: high

### `sub:ethylbenzene` — Ethylbenzene
- hs: 2902.60
- families: cat:aromatics
- derived to: sub:styrene
- status: **approved**, source: seed, confidence: high

### `sub:ethylene` — Ethylene
- aliases: ethene
- hs: 2901.21
- families: cat:olefins
- derived to: sub:ethylene-oxide; sub:ethylene-glycol; sub:polyethylene; sub:vinyl-chloride; sub:ethylbenzene
- consumed in: proc:polymerization
- status: **approved**, source: llm-review, confidence: high

### `sub:ethylene-glycol` — Ethylene glycol
- aliases: monoethylene glycol; meg
- hs: 2905.31
- families: cat:glycols
- consumed in: end:coolants-antifreeze
- status: **approved**, source: llm-review, confidence: high

### `sub:ethylene-oxide` — Ethylene oxide
- aliases: eo; oxirane
- hs: 2910.10
- families: cat:chemical-intermediates
- derived to: sub:ethylene-glycol; sub:ethanolamines; sub:butyl-glycol-ether; sub:nonylphenol-ethoxylate; sub:polyether-polyol; sub:sles
- consumed in: proc:ethoxylation
- status: **approved**, source: llm-review, confidence: high

### `sub:fatty-alcohol` — Fatty alcohol
- aliases: lauryl alcohol; cetyl alcohol; stearyl alcohol; c12-14 fatty alcohol
- hs: 3823.70; 2905.17
- families: cat:fatty-alcohols
- derived to: sub:sles
- consumed in: proc:ethoxylation; proc:sulphonation
- status: **approved**, source: llm-review, confidence: high

### `sub:formaldehyde` — Formaldehyde
- aliases: methanal; formalin
- hs: 2912.11
- families: cat:chemical-intermediates
- derived to: sub:mdi
- status: **approved**, source: llm-review, confidence: high

### `sub:fuel-oil` — Fuel oil
- aliases: heavy fuel oil; hfo; bunker fuel; vlsfo
- hs: 2710.19
- families: cat:fuel-oils
- consumed in: proc:blending
- status: **approved**, source: llm-review, confidence: medium — note: Dropped derived_to sub:carbon-black: carbon-black feedstock is normally FCC slurry or decant oil, not fuel oil, and the edge surfaced tire manufacturers as end users of fuel-oil blending.

### `sub:glycerin` — Glycerin
- aliases: glycerine; glycerol; crude glycerin; refined glycerin
- hs: 2905.45; 1520.00
- families: cat:oleochemicals
- derived to: sub:polyether-polyol; sub:epichlorohydrin
- consumed in: end:personal-care
- status: **approved**, source: llm-review, confidence: high

### `sub:hydrochloric-acid` — Hydrochloric acid
- aliases: hcl; muriatic acid
- hs: 2806.10
- families: cat:acids
- derived to: sub:vinyl-chloride
- status: **approved**, source: llm-review, confidence: high

### `sub:hydrogen` — Hydrogen
- aliases: h2
- hs: 2804.10
- families: cat:inorganics
- derived to: sub:ammonia; sub:hydrochloric-acid
- consumed in: proc:hydrogenation; proc:hydrodealkylation
- status: **approved**, source: llm-review, confidence: high

### `sub:hydrogen-peroxide` — Hydrogen peroxide
- aliases: h2o2
- hs: 2847.00
- families: cat:inorganics
- derived to: sub:propylene-oxide
- consumed in: end:pulp-paper
- status: **approved**, source: llm-review, confidence: high

### `sub:isopropanol` — Isopropanol
- aliases: isopropyl alcohol; ipa
- hs: 2905.12
- families: cat:alcohol-solvents
- consumed in: end:paints-coatings; end:personal-care
- status: **approved**, source: llm-review, confidence: high

### `sub:labsa` — Linear alkylbenzene sulphonic acid
- aliases: labsa; ddbsa
- hs: 3402.31
- families: cat:anionic-surfactants
- consumed in: end:detergents
- status: **approved**, source: llm-review, confidence: high — note: Updated HS code from 3402.11 (pre-2022) to HS 2022 3402.31.

### `sub:linear-alkylbenzene` — Linear alkylbenzene
- aliases: lab; linear alkyl benzene
- hs: 3817.00
- families: cat:aromatics
- derived to: sub:labsa
- consumed in: proc:sulphonation
- status: **approved**, source: llm-review, confidence: high

### `sub:lubricant-additives` — Lubricant additives
- aliases: lube additives; additive packages
- hs: 3811.21; 3811.29
- families: cat:lubricants
- consumed in: proc:blending
- status: **approved**, source: llm-review, confidence: high

### `sub:mdi` — Methylene diphenyl diisocyanate
- aliases: mdi; polymeric mdi; pmdi
- hs: 2929.10
- families: cat:isocyanates
- consumed in: end:rigid-pu-foam; end:flexible-pu-foam; end:adhesives-sealants
- status: **approved**, source: llm-review, confidence: high

### `sub:mek` — Methyl ethyl ketone
- aliases: mek; butanone
- hs: 2914.12
- families: cat:ketones
- consumed in: end:paints-coatings; end:adhesives-sealants
- status: **approved**, source: llm-review, confidence: high

### `sub:methanol` — Methanol
- aliases: methyl alcohol; wood alcohol; meoh
- hs: 2905.11
- families: cat:alcohol-solvents
- derived to: sub:formaldehyde; sub:acetic-acid; sub:methyl-methacrylate
- consumed in: proc:transesterification; proc:esterification
- status: **approved**, source: llm-review, confidence: high

### `sub:methyl-methacrylate` — Methyl methacrylate
- aliases: mma
- hs: 2916.14
- families: cat:acrylates
- consumed in: proc:polymerization
- status: **approved**, source: seed, confidence: high

### `sub:n-butanol` — n-Butanol
- aliases: butanol; butyl alcohol; nba
- hs: 2905.13
- families: cat:alcohol-solvents
- derived to: sub:butyl-acrylate; sub:butyl-acetate; sub:dibutyl-phthalate; sub:butyl-glycol-ether
- consumed in: proc:esterification; proc:ethoxylation
- status: **approved**, source: llm-review, confidence: high

### `sub:naphtha` — Naphtha
- aliases: petroleum naphtha
- hs: 2710.12
- families: cat:petrochemicals
- derived to: sub:ethylene; sub:propylene; sub:butadiene; sub:benzene; sub:toluene; sub:xylene
- consumed in: proc:cracking; proc:catalytic-reforming
- status: **approved**, source: llm-review, confidence: high

### `sub:nitric-acid` — Nitric acid
- hs: 2808.00
- families: cat:acids
- derived to: sub:aniline; sub:toluene-diisocyanate
- consumed in: end:fertilizers
- status: **approved**, source: llm-review, confidence: high

### `sub:nonylphenol-ethoxylate` — Nonylphenol ethoxylate
- aliases: npe; nonylphenol ethoxylates
- hs: 3402.42
- families: cat:nonionic-surfactants
- consumed in: end:detergents
- status: **approved**, source: llm-review, confidence: high

### `sub:palm-kernel-oil` — Palm kernel oil
- aliases: pko; cpko; rbdpko
- hs: 1513.21; 1513.29
- families: cat:oleochemical-feedstocks
- derived to: cat:fatty-acids; sub:glycerin; cat:biodiesel
- consumed in: proc:fat-splitting; proc:transesterification
- status: **approved**, source: llm-review, confidence: high

### `sub:palm-oil` — Palm oil
- aliases: crude palm oil; cpo; rbd palm oil
- hs: 1511.10; 1511.90
- families: cat:oleochemical-feedstocks
- derived to: sub:pfad; cat:fatty-acids; sub:glycerin; cat:biodiesel
- consumed in: proc:transesterification; proc:fat-splitting
- status: **approved**, source: llm-review, confidence: high

### `sub:pfad` — Palm fatty acid distillate
- aliases: pfad
- hs: 3823.19
- families: cat:fatty-acids
- derived to: cat:fatty-acids; cat:biodiesel
- status: **approved**, source: llm-review, confidence: high — note: Removed sub:glycerin from derived_to: PFAD is predominantly free fatty acids, not triglycerides.

### `sub:phenol` — Phenol
- aliases: carbolic acid
- hs: 2907.11
- families: cat:aromatics; cat:chemical-intermediates
- derived to: sub:bisphenol-a
- status: **approved**, source: llm-review, confidence: high

### `sub:phosgene` — Phosgene
- aliases: carbonyl chloride; cocl2
- hs: 2812.10
- families: cat:chemical-intermediates
- derived to: sub:toluene-diisocyanate; sub:mdi
- status: **approved**, source: llm-review, confidence: high — note: Bridge node: chlorine is the feedstock, phosgene the intermediate, isocyanates the product. Without it chlorine appeared to convert straight into TDI/MDI.

### `sub:phosphate-rock` — Phosphate rock
- aliases: rock phosphate; apatite
- hs: 2510.10
- families: cat:inorganics
- derived to: sub:phosphoric-acid
- status: **approved**, source: llm-review, confidence: high

### `sub:phosphoric-acid` — Phosphoric acid
- hs: 2809.20
- families: cat:acids
- consumed in: end:fertilizers
- status: **approved**, source: llm-review, confidence: high

### `sub:phthalic-anhydride` — Phthalic anhydride
- aliases: pa; pan
- hs: 2917.35
- families: cat:aromatics; cat:chemical-intermediates
- derived to: sub:dibutyl-phthalate; sub:dop; sub:dinp
- consumed in: proc:esterification
- status: **approved**, source: llm-review, confidence: high — note: Removed cat:plasticizers: PA is an anhydride precursor to phthalate plasticizers, not a plasticizer itself.

### `sub:polyether-polyol` — Polyether polyol
- aliases: polyol; polyether polyols; ppg; polymer polyol
- hs: 3907.29
- families: cat:polyurethanes
- consumed in: end:flexible-pu-foam; end:rigid-pu-foam; end:adhesives-sealants
- status: **approved**, source: llm-review, confidence: high

### `sub:polyethylene` — Polyethylene
- aliases: pe; hdpe; ldpe; lldpe; polythene
- hs: 3901.10; 3901.20
- families: cat:polyolefins
- consumed in: end:plastics-converting
- status: **approved**, source: llm-review, confidence: high

### `sub:polypropylene` — Polypropylene
- aliases: pp
- hs: 3902.10; 3902.30
- families: cat:polyolefins
- consumed in: end:plastics-converting
- status: **approved**, source: llm-review, confidence: high

### `sub:polystyrene` — Polystyrene
- aliases: ps; gpps; hips; eps
- hs: 3903.11; 3903.19
- families: cat:styrenics
- consumed in: end:plastics-converting
- status: **approved**, source: llm-review, confidence: high

### `sub:propylene` — Propylene
- aliases: propene
- hs: 2901.22
- families: cat:olefins
- derived to: sub:acrylic-acid; sub:polypropylene; sub:propylene-oxide; sub:propylene-glycol; sub:cumene; sub:n-butanol; sub:2-ethylhexanol; sub:isopropanol; sub:epichlorohydrin
- consumed in: proc:polymerization
- status: **approved**, source: llm-review, confidence: high

### `sub:propylene-glycol` — Propylene glycol
- aliases: monopropylene glycol; mpg
- hs: 2905.32
- families: cat:glycols
- consumed in: end:coolants-antifreeze
- status: **approved**, source: llm-review, confidence: high

### `sub:propylene-oxide` — Propylene oxide
- aliases: po; methyloxirane
- hs: 2910.20
- families: cat:chemical-intermediates
- derived to: sub:propylene-glycol; sub:polyether-polyol
- consumed in: proc:ethoxylation
- status: **approved**, source: llm-review, confidence: high

### `sub:pvc-resin` — PVC resin
- aliases: polyvinyl chloride resin
- hs: 3904.10
- families: cat:pvc
- consumed in: proc:compounding; end:pvc-compound; end:cable-wire; end:plastics-converting
- status: **approved**, source: llm-review, confidence: high

### `sub:salt` — Salt
- aliases: sodium chloride; industrial salt
- hs: 2501.00
- families: cat:inorganics
- consumed in: proc:chlor-alkali
- status: **approved**, source: seed, confidence: high

### `sub:sles` — Sodium laureth sulfate
- aliases: sles; sodium lauryl ether sulfate
- hs: 3402.39
- families: cat:anionic-surfactants
- consumed in: end:detergents; end:personal-care
- status: **approved**, source: llm-review, confidence: high

### `sub:soap-noodles` — Soap noodles
- aliases: toilet soap noodles; laundry soap noodles; soap base
- hs: 3401.20
- families: cat:soap
- consumed in: end:soap
- status: **approved**, source: llm-review, confidence: high

### `sub:soda-ash` — Soda ash
- aliases: sodium carbonate; dense soda ash; light soda ash
- hs: 2836.20
- families: cat:alkalis
- consumed in: end:detergents; end:glass
- status: **approved**, source: llm-review, confidence: high

### `sub:styrene` — Styrene
- aliases: styrene monomer; vinylbenzene
- hs: 2902.50
- families: cat:aromatics
- derived to: sub:polystyrene
- consumed in: proc:polymerization; end:tires-rubber
- status: **approved**, source: llm-review, confidence: high

### `sub:sulphur` — Sulphur
- aliases: sulfur; brimstone
- hs: 2503.00
- families: cat:inorganics
- derived to: sub:sulphuric-acid
- status: **approved**, source: llm-review, confidence: high

### `sub:sulphuric-acid` — Sulphuric acid
- aliases: sulfuric acid; oleum
- hs: 2807.00
- families: cat:acids
- derived to: sub:phosphoric-acid
- consumed in: proc:sulphonation; end:fertilizers
- status: **approved**, source: llm-review, confidence: high

### `sub:tallow` — Tallow
- aliases: animal tallow; beef tallow
- hs: 1502.10
- families: cat:oleochemical-feedstocks
- derived to: cat:biodiesel; cat:fatty-acids; sub:glycerin
- consumed in: proc:transesterification; proc:fat-splitting
- status: **approved**, source: llm-review, confidence: high — note: Updated HS code from superseded 1502.00 to 1502.10.

### `sub:titanium-dioxide` — Titanium dioxide
- aliases: tio2; titanium white; rutile; anatase
- hs: 3206.11; 2823.00
- families: cat:inorganics
- consumed in: end:paints-coatings
- status: **approved**, source: llm-review, confidence: high

### `sub:toluene` — Toluene
- aliases: toluol; methylbenzene
- hs: 2902.30
- families: cat:aromatics; cat:aromatic-solvents
- derived to: sub:benzene; sub:toluene-diisocyanate
- consumed in: proc:hydrodealkylation; end:paints-coatings; end:adhesives-sealants
- status: **approved**, source: llm-review, confidence: high

### `sub:toluene-diisocyanate` — Toluene diisocyanate
- aliases: tdi; tdi 80/20
- hs: 2929.10
- families: cat:isocyanates
- consumed in: end:flexible-pu-foam
- status: **approved**, source: seed, confidence: high

### `sub:urea` — Urea
- aliases: carbamide; prilled urea; granular urea
- hs: 3102.10
- families: cat:chemical-intermediates
- consumed in: end:fertilizers
- status: **approved**, source: llm-review, confidence: high — note: Organic carbamide made from ammonia and CO2 (HS chapter 31, not 28). Homed under organic chemical intermediates rather than petrochemicals, which is where the reviewed file put it.

### `sub:used-cooking-oil` — Used cooking oil
- aliases: uco; waste cooking oil
- hs: 1518.00
- families: cat:oleochemical-feedstocks
- derived to: cat:biodiesel; cat:fatty-acids; sub:glycerin
- consumed in: proc:transesterification; proc:fat-splitting
- status: **approved**, source: llm-review, confidence: high

### `sub:vinyl-chloride` — Vinyl chloride monomer
- aliases: vcm; vinyl chloride
- hs: 2903.21
- families: cat:chlorinated-monomers
- derived to: sub:pvc-resin
- consumed in: proc:polymerization
- status: **approved**, source: llm-review, confidence: high — note: Reclassified from cat:olefins to cat:chlorinated-monomers.

### `sub:xylene` — Xylene
- aliases: xylenes; mixed xylene; o-xylene; ortho-xylene; p-xylene; para-xylene
- hs: 2902.44; 2902.41; 2902.42; 2902.43
- families: cat:aromatics; cat:aromatic-solvents
- derived to: sub:phthalic-anhydride
- consumed in: end:paints-coatings; end:adhesives-sealants
- status: **approved**, source: llm-review, confidence: medium — note: Phthalic anhydride is the o-xylene (2902.41) route; p-xylene (2902.43) goes to PTA/PET; mixed xylene (2902.44) mostly goes to solvents and gasoline blending.


## Processes

### `proc:blending` — Oil and lubricant blending
- aliases: blend; lube blending
- operates on: sub:base-oil; sub:lubricant-additives; sub:fuel-oil; sub:crude-oil
- produces: cat:finished-lubricants; cat:fuel-oils
- search terms: oil blending; lubricant blending; fuel oil blending; crude oil blending; blending plant
- status: **approved**, source: llm-review, confidence: high

### `proc:catalytic-reforming` — Catalytic reforming
- aliases: naphtha reforming; btx extraction; reformer
- operates on: sub:naphtha
- produces: sub:benzene; sub:toluene; sub:xylene; sub:hydrogen
- search terms: catalytic reformer; aromatics complex; btx plant
- status: **approved**, source: llm-review, confidence: high

### `proc:chlor-alkali` — Chlor-alkali electrolysis
- aliases: chlor alkali; membrane cell
- operates on: sub:salt
- produces: sub:chlorine; sub:caustic-soda; sub:hydrogen
- search terms: chlor alkali plant; caustic soda plant; chlorine producer
- status: **approved**, source: llm-review, confidence: high

### `proc:compounding` — Compounding
- aliases: compound
- operates on: sub:pvc-resin; cat:plasticizers
- produces: end:pvc-compound
- search terms: pvc compounding; compound plant; masterbatch producer
- status: **approved**, source: seed, confidence: high

### `proc:cracking` — Steam cracking
- aliases: steam cracker; cracking
- operates on: sub:naphtha
- produces: sub:ethylene; sub:propylene; sub:butadiene
- search terms: steam cracker; cracker plant; olefin plant
- status: **approved**, source: llm-review, confidence: high

### `proc:distillation` — Distillation
- aliases: fractionation
- operates on: cat:petrochemicals; cat:fatty-acids
- produces: cat:solvents; cat:aromatics; cat:fatty-acids
- search terms: distillation plant; fractionation plant
- status: **approved**, source: llm-review, confidence: medium

### `proc:esterification` — Esterification
- aliases: esterify
- operates on: sub:phthalic-anhydride; sub:acrylic-acid; sub:acetic-acid; sub:n-butanol; sub:ethanol; sub:methanol; sub:2-ethylhexanol
- produces: cat:esters
- search terms: esterification plant; ester plant; esterification unit
- status: **approved**, source: llm-review, confidence: high

### `proc:ethoxylation` — Ethoxylation and alkoxylation
- aliases: ethoxylate; alkoxylation
- operates on: sub:ethylene-oxide; sub:propylene-oxide; sub:fatty-alcohol; sub:n-butanol
- produces: cat:nonionic-surfactants; cat:glycol-ethers; sub:polyether-polyol
- search terms: ethoxylation plant; alkoxylation unit; surfactant plant
- status: **approved**, source: llm-review, confidence: high

### `proc:fat-splitting` — Fat splitting (hydrolysis)
- aliases: fat splitting; triglyceride hydrolysis
- operates on: cat:oleochemical-feedstocks
- produces: cat:fatty-acids; sub:glycerin
- search terms: fat splitting plant; oleochemical splitting; fatty acid plant
- status: **approved**, source: llm-review, confidence: high

### `proc:formulation` — Formulation and blending
- aliases: formulate
- operates on: cat:solvents; cat:surfactants; sub:base-oil; cat:fatty-acids; cat:esters; sub:ethanolamines
- produces: end:paints-coatings; end:detergents; end:adhesives-sealants; end:personal-care; end:greases; end:metalworking-fluids
- search terms: formulation plant; blending and filling plant; contract manufacturer
- status: **approved**, source: llm-review, confidence: medium

### `proc:hydrodealkylation` — Hydrodealkylation
- aliases: hda
- operates on: sub:toluene; sub:hydrogen
- produces: sub:benzene
- search terms: hda unit; benzene plant
- status: **approved**, source: llm-review, confidence: medium

### `proc:hydrogenation` — Hydrogenation / hydrogenolysis
- aliases: hydrogenolysis; fatty alcohol hydrogenation
- operates on: cat:fatty-acids; cat:biodiesel; sub:hydrogen
- produces: cat:fatty-alcohols
- search terms: hydrogenation plant; fatty alcohol plant
- status: **approved**, source: llm-review, confidence: high — note: Oleochemical hydrogenolysis of fatty acids or FAME yields fatty alcohols. Simple hydrogenation of triglycerides yields hardened/saturated fats (not fatty acids).

### `proc:polymerization` — Polymerization
- aliases: polymerisation; polymerize
- operates on: sub:ethylene; sub:propylene; sub:butadiene; sub:styrene; sub:acrylic-acid; sub:methyl-methacrylate; sub:vinyl-chloride
- produces: cat:polymers
- search terms: polymer plant; polymerization plant; resin plant
- status: **approved**, source: llm-review, confidence: high

### `proc:refining` — Refining
- aliases: refine; oil refining
- operates on: sub:crude-oil
- produces: cat:fuel-oils; cat:base-oils; sub:naphtha; sub:diesel
- search terms: oil refinery; crude oil refining; refinery operator
- status: **approved**, source: llm-review, confidence: high

### `proc:saponification` — Saponification and fatty acid neutralization
- aliases: saponify; soap neutralization
- operates on: cat:fatty-acids; cat:oleochemical-feedstocks; sub:caustic-soda
- produces: sub:soap-noodles; end:soap
- search terms: soap noodle plant; saponification plant; soap base plant
- status: **approved**, source: llm-review, confidence: high

### `proc:sulphonation` — Sulphonation and sulphation
- aliases: sulfonation; sulphation
- operates on: sub:linear-alkylbenzene; sub:fatty-alcohol; sub:sulphuric-acid
- produces: sub:labsa; sub:sles
- search terms: sulphonation plant; labsa plant; sles plant
- status: **approved**, source: llm-review, confidence: high

### `proc:transesterification` — Transesterification
- aliases: transesterify
- operates on: sub:palm-oil; sub:palm-kernel-oil; sub:coconut-oil; sub:used-cooking-oil; sub:tallow; sub:methanol
- produces: cat:biodiesel; sub:glycerin
- search terms: biodiesel plant; transesterification plant; biodiesel refinery
- status: **approved**, source: llm-review, confidence: high


## End products (value-chain leaves → company discovery)

### `end:adhesives-sealants` — Adhesives and sealants
- aliases: adhesive; sealant
- consumes: cat:solvents; sub:butyl-acrylate; sub:mdi; sub:toluene; sub:xylene; sub:acetone; sub:mek
- operator phrases: adhesive manufacturer; sealant manufacturer; adhesive plant
- status: **approved**, source: llm-review, confidence: high

### `end:biodiesel-blend` — Biodiesel blending
- aliases: biodiesel blend; fuel blending
- consumes: cat:biodiesel; sub:diesel
- operator phrases: biodiesel blender; fuel blending terminal; biodiesel supplier
- status: **approved**, source: llm-review, confidence: high

### `end:cable-wire` — Cable and wire
- aliases: cable; wire and cable
- consumes: sub:pvc-resin; cat:plasticizers
- operator phrases: cable manufacturer; wire and cable plant; cable compound producer
- status: **approved**, source: seed, confidence: high

### `end:coolants-antifreeze` — Coolants and antifreeze
- aliases: antifreeze; engine coolant; heat transfer fluid
- consumes: sub:ethylene-glycol; sub:propylene-glycol
- operator phrases: coolant manufacturer; antifreeze blender; antifreeze plant
- status: **approved**, source: llm-review, confidence: high

### `end:detergents` — Detergents
- aliases: detergent; home care
- consumes: sub:labsa; sub:sles; cat:surfactants; sub:nonylphenol-ethoxylate; sub:soda-ash
- operator phrases: detergent manufacturer; detergent plant; home care manufacturer
- status: **approved**, source: llm-review, confidence: high

### `end:fertilizers` — Fertilizers
- aliases: fertilizer; npk; nitrogen fertilizer; phosphate fertilizer
- consumes: sub:urea; sub:ammonia; sub:nitric-acid; sub:phosphoric-acid; sub:sulphuric-acid
- operator phrases: fertilizer manufacturer; fertilizer plant; npk blending plant
- status: **approved**, source: llm-review, confidence: high

### `end:flexible-pu-foam` — Flexible PU foam
- aliases: flexible foam; pu foam
- consumes: sub:toluene-diisocyanate; sub:mdi; sub:polyether-polyol
- operator phrases: flexible foam manufacturer; pu foam plant; mattress foam producer
- status: **approved**, source: seed, confidence: high

### `end:glass` — Glass manufacturing
- aliases: float glass; container glass
- consumes: sub:soda-ash
- operator phrases: glass manufacturer; float glass plant; glass factory
- status: **approved**, source: llm-review, confidence: high

### `end:greases` — Greases
- aliases: grease
- consumes: sub:base-oil; cat:fatty-acids
- operator phrases: grease manufacturer; grease plant; lubricating grease producer
- status: **approved**, source: seed, confidence: high

### `end:metalworking-fluids` — Metalworking fluids
- aliases: cutting fluids; metalworking coolants
- consumes: sub:base-oil; cat:esters; sub:ethanolamines
- operator phrases: metalworking fluid manufacturer; cutting fluid plant; coolant manufacturer
- status: **approved**, source: llm-review, confidence: high — note: Renamed the 'coolants' alias to 'metalworking coolants': end:coolants-antifreeze owns engine/heat-transfer coolants, and the bare word was ambiguous.

### `end:paints-coatings` — Paints and coatings
- aliases: paint; coatings
- consumes: cat:solvents; sub:titanium-dioxide; sub:epoxy-resin; sub:butyl-acrylate; sub:butyl-acetate; sub:ethyl-acetate; sub:butyl-glycol-ether; sub:toluene; sub:xylene; sub:acetone; sub:mek; sub:isopropanol
- operator phrases: paint manufacturer; coatings manufacturer; paint factory; industrial coatings plant
- status: **approved**, source: llm-review, confidence: high

### `end:personal-care` — Personal care products
- aliases: personal care; cosmetics
- consumes: sub:glycerin; cat:surfactants; sub:sles; sub:ethanol; sub:isopropanol; sub:ethanolamines
- operator phrases: personal care manufacturer; cosmetic ingredient supplier; toiletries plant
- status: **approved**, source: llm-review, confidence: medium

### `end:plastics-converting` — Plastics converting
- aliases: plastics processing; film and packaging converting; injection moulding
- consumes: sub:polyethylene; sub:polypropylene; sub:pvc-resin; sub:polystyrene
- operator phrases: plastic converter; packaging manufacturer; film manufacturer; injection moulder; plastic products manufacturer
- status: **approved**, source: llm-review, confidence: medium — note: Closes the polyethylene/polypropylene/polystyrene dead ends: without it those commodities reached no end-user leaf.

### `end:pulp-paper` — Pulp and paper
- aliases: pulp mill; paper mill
- consumes: sub:hydrogen-peroxide; sub:caustic-soda
- operator phrases: pulp mill; paper manufacturer; paper mill; bleached pulp producer
- status: **approved**, source: llm-review, confidence: high

### `end:pvc-compound` — PVC compounding
- aliases: pvc compound; pvc compounds
- consumes: sub:pvc-resin; cat:plasticizers; sub:dop; sub:dinp; sub:dibutyl-phthalate
- operator phrases: pvc compound manufacturer; pvc compounder; masterbatch plant; cable compound manufacturer
- status: **approved**, source: llm-review, confidence: high

### `end:rigid-pu-foam` — Rigid PU foam
- aliases: rigid foam; insulation foam
- consumes: sub:mdi; sub:polyether-polyol
- operator phrases: rigid foam manufacturer; insulation panel manufacturer; pu insulation plant
- status: **approved**, source: seed, confidence: high

### `end:soap` — Soap manufacturing
- aliases: soap producer; soap plant; bar soap
- consumes: cat:fatty-acids; sub:caustic-soda; sub:soap-noodles
- operator phrases: soap manufacturer; soap plant; toilet soap producer
- status: **approved**, source: llm-review, confidence: high

### `end:superabsorbent-polymer` — Superabsorbent polymer
- aliases: sap
- consumes: sub:acrylic-acid
- operator phrases: superabsorbent polymer manufacturer; sap plant; hygiene absorbent manufacturer
- status: **approved**, source: seed, confidence: high

### `end:tires-rubber` — Tires and synthetic rubber
- aliases: tires; rubber goods; synthetic rubber
- consumes: sub:carbon-black; sub:butadiene; sub:styrene
- operator phrases: tire manufacturer; rubber compounder; synthetic rubber plant
- status: **approved**, source: llm-review, confidence: high


## Lint findings

- `warning` warning: sub:diesel.hs_codes: HS code 2710.19 is also used by sub:base-oil
- `warning` warning: sub:fuel-oil.hs_codes: HS code 2710.19 is also used by sub:base-oil
- `warning` warning: sub:pfad.hs_codes: HS code 3823.19 is also used by cat:fatty-acids
- `warning` warning: sub:toluene-diisocyanate.hs_codes: HS code 2929.10 is also used by sub:mdi
