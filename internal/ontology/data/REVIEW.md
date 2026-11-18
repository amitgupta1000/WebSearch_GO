# Chemical value-chain review — seed-2026-11-18

Notes: Seed value chain authored from the in-repo chemistry vocabulary, the petrochemical family skeleton and customs nomenclature. Every node is status=proposed with source=seed: no relation here has been confirmed by a domain expert yet. Return the edited file or a corrections block.

## How to review this

The JSON beside this file is the single source of truth; this sheet is its readable view.

1. Every row is `status: proposed` until someone confirms it. Flip it to `approved` (or `rejected`) with a note.
2. `source` records where a relation came from: `seed` (authored, unverified), `hs` (customs nomenclature), `echa`, `trade-db`, `operator` (you pinned it).
3. Return **either** the whole edited JSON **or** a `corrections` block keyed by node ID — a present field replaces that field wholesale, so send the list you want to end up with.
4. `derived_to` is downstream chemistry (feedstock → derivative). `consumed_in` is the value-chain edge to the process or industrial product that uses it. The leaves there drive company discovery.

## Summary

- substances: **69** · categories: **39** · processes: **13** · end-products: **13**
- status: approved **0**, proposed **134**, rejected **0**

## Category tree

- `cat:chemicals` Chemicals _(intermediate)_ _(proposed)_
  - `cat:coal-products` Coal products _(proposed)_
  - `cat:inorganics` Inorganics _(intermediate)_ _(proposed)_
    - `cat:acids` Inorganic acids _(proposed)_
    - `cat:alkalis` Alkalis _(proposed)_
  - `cat:oils` Oils — **ambiguous**, ranked: Lubricants > Fuel oils > Crude oil > Oleochemical feedstocks _(proposed)_
  - `cat:oleochemicals` Oleochemicals _(intermediate)_ _(proposed)_
    - `cat:biodiesel` Biodiesel _(intermediate)_ _(proposed)_
    - `cat:fatty-acids` Fatty acids _(intermediate)_ _(proposed)_
    - `cat:oleochemical-feedstocks` Oleochemical feedstocks _(intermediate)_ _(proposed)_
    - `cat:soap` Soap _(proposed)_
  - `cat:petrochemicals` Petrochemicals _(intermediate)_ _(proposed)_
    - `cat:amines` Amines _(intermediate)_ _(proposed)_
    - `cat:aromatics` Aromatics _(intermediate)_ _(proposed)_
    - `cat:esters` Esters _(intermediate)_ _(proposed)_
      - `cat:acetates` Acetates _(intermediate)_ _(proposed)_
      - `cat:acrylates` Acrylates _(intermediate)_ _(proposed)_
      - `cat:plasticizers` Plasticizers _(intermediate)_ _(proposed)_
    - `cat:fuel-oils` Fuel oils _(proposed)_
    - `cat:glycols` Glycols _(intermediate)_ _(proposed)_
    - `cat:isocyanates` Isocyanates _(intermediate)_ _(proposed)_
    - `cat:lubricants` Lubricants _(intermediate)_ _(proposed)_
      - `cat:base-oils` Base oils _(intermediate)_ _(proposed)_
      - `cat:finished-lubricants` Finished lubricants _(proposed)_
    - `cat:olefins` Olefins _(intermediate)_ _(proposed)_
    - `cat:polymers` Polymers and resins _(intermediate)_ _(proposed)_
      - `cat:epoxies` Epoxy resins _(proposed)_
      - `cat:polyolefins` Polyolefins _(proposed)_
      - `cat:polyurethanes` Polyurethanes _(intermediate)_ _(proposed)_
      - `cat:pvc` PVC _(proposed)_
    - `cat:solvents` Solvents _(intermediate)_ _(proposed)_
      - `cat:alcohol-solvents` Alcohol solvents _(proposed)_
      - `cat:aromatic-solvents` Aromatic solvents _(proposed)_
      - `cat:ester-solvents` Ester solvents _(proposed)_
      - `cat:glycol-ethers` Glycol ethers _(intermediate)_ _(proposed)_
      - `cat:ketones` Ketones _(proposed)_
    - `cat:surfactants` Surfactants _(intermediate)_ _(proposed)_
      - `cat:anionic-surfactants` Anionic surfactants _(proposed)_
      - `cat:nonionic-surfactants` Nonionic surfactants _(proposed)_

## Substances

### `sub:acetone` — Acetone
- aliases: propanone
- hs: 2914.11
- families: cat:ketones
- status: **proposed**, source: seed, confidence: high

### `sub:acrylic-acid` — Acrylic acid
- aliases: prop-2-enoic acid
- hs: 2916.11
- families: cat:acrylates
- derived to: sub:butyl-acrylate
- consumed in: proc:polymerization
- status: **proposed**, source: seed, confidence: high

### `sub:ammonia` — Ammonia
- hs: 2814.10
- families: cat:inorganics
- status: **proposed**, source: seed, confidence: high

### `sub:aniline` — Aniline
- aliases: aminobenzene
- hs: 2921.41
- families: cat:amines
- status: **proposed**, source: seed, confidence: high

### `sub:base-oil` — Base oil
- aliases: base stock; group i base oil; group ii base oil
- hs: 2710.19
- families: cat:base-oils
- consumed in: proc:blending; end:greases; end:metalworking-fluids
- status: **proposed**, source: seed, confidence: high

### `sub:benzene` — Benzene
- aliases: benzol
- hs: 2902.20
- families: cat:aromatics; cat:aromatic-solvents
- derived to: sub:ethylbenzene; sub:cumene; sub:linear-alkylbenzene
- status: **proposed**, source: seed, confidence: high

### `sub:butadiene` — Butadiene
- aliases: 1 3 butadiene
- hs: 2901.24
- families: cat:olefins
- status: **proposed**, source: seed, confidence: high

### `sub:butyl-acetate` — Butyl acetate
- hs: 2915.33
- families: cat:acetates; cat:ester-solvents
- consumed in: end:paints-coatings
- status: **proposed**, source: seed, confidence: high

### `sub:butyl-acrylate` — Butyl acrylate
- hs: 2916.12
- families: cat:acrylates
- consumed in: end:adhesives-sealants; end:paints-coatings
- status: **proposed**, source: seed, confidence: high

### `sub:butyl-glycol-ether` — Butyl glycol ether
- aliases: ebutyl glycol; ethylene glycol monobutyl ether
- hs: 2909.43
- families: cat:glycol-ethers
- consumed in: end:paints-coatings
- status: **proposed**, source: seed, confidence: medium

### `sub:carbon-black` — Carbon black
- hs: 2803.00
- families: cat:inorganics
- status: **proposed**, source: seed, confidence: high

### `sub:caustic-soda` — Caustic soda
- aliases: sodium hydroxide; lye; caustic
- hs: 2815.11
- families: cat:alkalis
- consumed in: end:soap
- status: **proposed**, source: seed, confidence: high

### `sub:chlorine` — Chlorine
- hs: 2801.10
- families: cat:inorganics
- derived to: sub:vinyl-chloride
- status: **proposed**, source: seed, confidence: high

### `sub:coal` — Coal
- aliases: thermal coal; coking coal
- hs: 2701.19
- families: cat:coal-products
- status: **proposed**, source: seed, confidence: high

### `sub:coconut-oil` — Coconut oil
- aliases: copra oil
- hs: 1513.11
- families: cat:oleochemical-feedstocks
- derived to: cat:fatty-acids; sub:glycerin
- status: **proposed**, source: seed, confidence: high

### `sub:crude-oil` — Crude oil
- aliases: petroleum crude; crude
- hs: 2709.00
- families: cat:petrochemicals
- derived to: sub:naphtha; sub:fuel-oil; sub:base-oil
- consumed in: proc:refining
- status: **proposed**, source: seed, confidence: high

### `sub:cumene` — Cumene
- aliases: isopropylbenzene
- hs: 2902.70
- families: cat:aromatics
- derived to: sub:phenol; sub:acetone
- status: **proposed**, source: seed, confidence: high

### `sub:dibutyl-phthalate` — Dibutyl phthalate
- aliases: dbp
- hs: 2917.34
- families: cat:plasticizers
- consumed in: end:pvc-compound
- status: **proposed**, source: seed, confidence: high

### `sub:diesel` — Diesel
- aliases: gasoil; gas oil
- hs: 2710.19
- families: cat:fuel-oils
- status: **proposed**, source: seed, confidence: medium

### `sub:dinp` — Diisononyl phthalate
- aliases: dinp
- hs: 2917.34
- families: cat:plasticizers
- consumed in: end:pvc-compound
- status: **proposed**, source: seed, confidence: medium

### `sub:dop` — Dioctyl phthalate
- aliases: dop; dehp; bis(2-ethylhexyl) phthalate
- hs: 2917.33
- families: cat:plasticizers
- consumed in: end:pvc-compound
- status: **proposed**, source: seed, confidence: high

### `sub:epoxy-resin` — Epoxy resin
- aliases: epoxy
- hs: 3907.30
- families: cat:epoxies
- consumed in: end:paints-coatings
- status: **proposed**, source: seed, confidence: high

### `sub:ethanol` — Ethanol
- aliases: ethyl alcohol; industrial ethanol
- hs: 2207.10
- families: cat:alcohol-solvents
- consumed in: proc:esterification
- status: **proposed**, source: seed, confidence: medium

### `sub:ethanolamines` — Ethanolamines
- aliases: mea; dea; tea; monoethanolamine
- hs: 2922.11
- families: cat:amines
- status: **proposed**, source: seed, confidence: high

### `sub:ethyl-acetate` — Ethyl acetate
- hs: 2915.31
- families: cat:acetates; cat:ester-solvents
- consumed in: end:paints-coatings
- status: **proposed**, source: seed, confidence: high

### `sub:ethylbenzene` — Ethylbenzene
- hs: 2902.60
- families: cat:aromatics
- derived to: sub:styrene
- status: **proposed**, source: seed, confidence: high

### `sub:ethylene` — Ethylene
- aliases: ethene
- hs: 2901.21
- families: cat:olefins
- derived to: sub:ethylene-glycol; sub:polyethylene; sub:vinyl-chloride
- status: **proposed**, source: seed, confidence: high

### `sub:ethylene-glycol` — Ethylene glycol
- aliases: monoethylene glycol; meg
- hs: 2905.31
- families: cat:glycols
- status: **proposed**, source: seed, confidence: high

### `sub:formaldehyde` — Formaldehyde
- aliases: methanal; formalin
- hs: 2912.11
- families: cat:petrochemicals
- status: **proposed**, source: seed, confidence: high

### `sub:fuel-oil` — Fuel oil
- aliases: heavy fuel oil; hfo; bunker fuel; vlsfo
- hs: 2710.19
- families: cat:fuel-oils
- consumed in: proc:blending
- status: **proposed**, source: seed, confidence: high

### `sub:glycerin` — Glycerin
- aliases: glycerine; glycerol; crude glycerin
- hs: 2905.45
- families: cat:oleochemicals
- consumed in: end:personal-care
- status: **proposed**, source: seed, confidence: high

### `sub:hydrochloric-acid` — Hydrochloric acid
- aliases: hcl; muriatic acid
- hs: 2806.10
- families: cat:acids
- status: **proposed**, source: seed, confidence: high

### `sub:hydrogen-peroxide` — Hydrogen peroxide
- aliases: h2o2
- hs: 2847.00
- families: cat:inorganics
- status: **proposed**, source: seed, confidence: high

### `sub:isopropanol` — Isopropanol
- aliases: isopropyl alcohol; ipa
- hs: 2905.12
- families: cat:alcohol-solvents
- status: **proposed**, source: seed, confidence: high

### `sub:labsa` — Linear alkylbenzene sulphonic acid
- aliases: labsa; ddbsa
- hs: 3402.11
- families: cat:anionic-surfactants
- consumed in: end:detergents
- status: **proposed**, source: seed, confidence: high

### `sub:linear-alkylbenzene` — Linear alkylbenzene
- aliases: lab; linear alkyl benzene
- hs: 3817.00
- families: cat:aromatics
- derived to: sub:labsa
- status: **proposed**, source: seed, confidence: high

### `sub:lubricant-additives` — Lubricant additives
- aliases: lube additives; additive packages
- families: cat:lubricants
- consumed in: proc:blending
- status: **proposed**, source: seed, confidence: medium

### `sub:mdi` — Methylene diphenyl diisocyanate
- aliases: mdi; methylene diphenyl diisocyanate
- hs: 2929.10
- families: cat:isocyanates
- consumed in: end:rigid-pu-foam; end:flexible-pu-foam
- status: **proposed**, source: seed, confidence: high

### `sub:mek` — Methyl ethyl ketone
- aliases: mek; butanone
- hs: 2914.12
- families: cat:ketones
- status: **proposed**, source: seed, confidence: high

### `sub:methanol` — Methanol
- aliases: methyl alcohol; wood alcohol
- hs: 2905.11
- families: cat:alcohol-solvents
- derived to: sub:formaldehyde
- consumed in: proc:transesterification; proc:esterification
- status: **proposed**, source: seed, confidence: high

### `sub:methyl-methacrylate` — Methyl methacrylate
- aliases: mma
- hs: 2916.14
- families: cat:acrylates
- consumed in: proc:polymerization
- status: **proposed**, source: seed, confidence: high

### `sub:n-butanol` — n-Butanol
- aliases: butanol; butyl alcohol; nba
- hs: 2905.13
- families: cat:alcohol-solvents
- derived to: sub:butyl-acrylate; sub:butyl-acetate; sub:dibutyl-phthalate
- consumed in: proc:esterification
- status: **proposed**, source: seed, confidence: high

### `sub:naphtha` — Naphtha
- aliases: petroleum naphtha
- hs: 2710.12
- families: cat:petrochemicals
- derived to: sub:ethylene; sub:propylene
- consumed in: proc:cracking
- status: **proposed**, source: seed, confidence: high

### `sub:nitric-acid` — Nitric acid
- hs: 2808.00
- families: cat:acids
- status: **proposed**, source: seed, confidence: high

### `sub:nonylphenol-ethoxylate` — Nonylphenol ethoxylate
- aliases: npe; nonylphenol ethoxylates
- families: cat:nonionic-surfactants
- consumed in: end:detergents
- status: **proposed**, source: seed, confidence: medium

### `sub:palm-kernel-oil` — Palm kernel oil
- aliases: pko
- hs: 1513.21
- families: cat:oleochemical-feedstocks
- derived to: cat:fatty-acids; sub:glycerin
- status: **proposed**, source: seed, confidence: high

### `sub:palm-oil` — Palm oil
- aliases: crude palm oil; cpo
- hs: 1511.10
- families: cat:oleochemical-feedstocks
- derived to: sub:pfad; cat:fatty-acids; sub:glycerin; cat:biodiesel
- consumed in: proc:transesterification
- status: **proposed**, source: seed, confidence: high

### `sub:pfad` — Palm fatty acid distillate
- aliases: pfad
- hs: 3823.19
- families: cat:fatty-acids
- derived to: cat:fatty-acids; sub:glycerin
- status: **proposed**, source: seed, confidence: medium

### `sub:phenol` — Phenol
- aliases: carbolic acid
- hs: 2907.11
- families: cat:aromatics
- status: **proposed**, source: seed, confidence: high

### `sub:phosphoric-acid` — Phosphoric acid
- hs: 2809.20
- families: cat:acids
- status: **proposed**, source: seed, confidence: high

### `sub:phthalic-anhydride` — Phthalic anhydride
- aliases: pa; pan
- hs: 2917.35
- families: cat:aromatics; cat:plasticizers
- derived to: sub:dibutyl-phthalate; sub:dop; sub:dinp
- consumed in: proc:esterification
- status: **proposed**, source: seed, confidence: high

### `sub:polyether-polyol` — Polyether polyol
- aliases: polyol; polyether polyols
- families: cat:polyurethanes
- consumed in: end:flexible-pu-foam; end:rigid-pu-foam
- status: **proposed**, source: seed, confidence: high

### `sub:polyethylene` — Polyethylene
- aliases: pe; hdpe; ldpe
- hs: 3901.10
- families: cat:polyolefins
- status: **proposed**, source: seed, confidence: high

### `sub:polypropylene` — Polypropylene
- aliases: pp
- hs: 3902.10
- families: cat:polyolefins
- status: **proposed**, source: seed, confidence: high

### `sub:propylene` — Propylene
- aliases: propene
- hs: 2901.22
- families: cat:olefins
- derived to: sub:acrylic-acid; sub:polypropylene; sub:propylene-glycol
- status: **proposed**, source: seed, confidence: high

### `sub:propylene-glycol` — Propylene glycol
- aliases: monopropylene glycol; mpg
- hs: 2905.32
- families: cat:glycols
- status: **proposed**, source: seed, confidence: high

### `sub:pvc-resin` — PVC resin
- aliases: polyvinyl chloride resin
- hs: 3904.10
- families: cat:pvc
- consumed in: proc:compounding; end:cable-wire
- status: **proposed**, source: seed, confidence: high

### `sub:salt` — Salt
- aliases: sodium chloride; industrial salt
- hs: 2501.00
- families: cat:inorganics
- consumed in: proc:chlor-alkali
- status: **proposed**, source: seed, confidence: high

### `sub:soda-ash` — Soda ash
- aliases: sodium carbonate
- hs: 2836.20
- families: cat:alkalis
- status: **proposed**, source: seed, confidence: high

### `sub:styrene` — Styrene
- aliases: styrene monomer; vinylbenzene
- hs: 2902.50
- families: cat:aromatics
- status: **proposed**, source: seed, confidence: high

### `sub:sulphuric-acid` — Sulphuric acid
- aliases: sulfuric acid
- hs: 2807.00
- families: cat:acids
- status: **proposed**, source: seed, confidence: high

### `sub:tallow` — Tallow
- aliases: animal tallow; beef tallow
- hs: 1502.00
- families: cat:oleochemical-feedstocks
- derived to: cat:biodiesel; cat:fatty-acids
- status: **proposed**, source: seed, confidence: medium

### `sub:titanium-dioxide` — Titanium dioxide
- aliases: tio2; titanium white
- hs: 2823.00
- families: cat:inorganics
- consumed in: end:paints-coatings
- status: **proposed**, source: seed, confidence: high

### `sub:toluene` — Toluene
- aliases: toluol; methylbenzene
- hs: 2902.30
- families: cat:aromatics; cat:aromatic-solvents
- derived to: sub:benzene
- status: **proposed**, source: seed, confidence: medium

### `sub:toluene-diisocyanate` — Toluene diisocyanate
- aliases: tdi; toluene diisocyanate
- hs: 2929.10
- families: cat:isocyanates
- consumed in: end:flexible-pu-foam
- status: **proposed**, source: seed, confidence: high

### `sub:urea` — Urea
- hs: 3102.10
- families: cat:inorganics
- status: **proposed**, source: seed, confidence: high

### `sub:used-cooking-oil` — Used cooking oil
- aliases: uco; waste cooking oil
- hs: 1518.00
- families: cat:oleochemical-feedstocks
- derived to: cat:biodiesel; cat:fatty-acids
- consumed in: proc:transesterification
- status: **proposed**, source: seed, confidence: high

### `sub:vinyl-chloride` — Vinyl chloride monomer
- aliases: vcm; vinyl chloride
- hs: 2903.21
- families: cat:olefins
- derived to: sub:pvc-resin
- consumed in: proc:polymerization
- status: **proposed**, source: seed, confidence: high

### `sub:xylene` — Xylene
- aliases: xylenes; mixed xylene
- hs: 2902.44
- families: cat:aromatics; cat:aromatic-solvents
- derived to: sub:phthalic-anhydride
- status: **proposed**, source: seed, confidence: medium — note: Phthalic anhydride is the o-xylene route; mixed xylene mostly goes to solvents and gasoline blending.


## Processes

### `proc:blending` — Blending
- aliases: blend
- operates on: cat:oils
- produces: cat:lubricants; cat:fuel-oils
- search terms: oil blending; lubricant blending; fuel oil blending; crude oil blending; blending plant
- status: **proposed**, source: seed, confidence: high

### `proc:chlor-alkali` — Chlor-alkali electrolysis
- aliases: chlor alkali; membrane cell
- operates on: sub:salt
- produces: sub:chlorine; sub:caustic-soda
- search terms: chlor alkali plant; caustic soda plant; chlorine producer
- status: **proposed**, source: seed, confidence: high

### `proc:compounding` — Compounding
- aliases: compound
- operates on: sub:pvc-resin; cat:plasticizers
- produces: end:pvc-compound
- search terms: pvc compounding; compound plant; masterbatch producer
- status: **proposed**, source: seed, confidence: high

### `proc:cracking` — Steam cracking
- aliases: steam cracker; cracking
- operates on: sub:naphtha
- produces: sub:ethylene; sub:propylene
- search terms: steam cracker; cracker plant; olefin plant
- status: **proposed**, source: seed, confidence: high

### `proc:distillation` — Distillation
- aliases: fractionation
- operates on: cat:petrochemicals
- search terms: distillation plant; fractionation plant
- status: **proposed**, source: seed, confidence: medium

### `proc:esterification` — Esterification
- aliases: esterify
- operates on: sub:phthalic-anhydride; sub:n-butanol; sub:methanol
- produces: cat:esters
- search terms: esterification plant; ester plant; esterification unit
- status: **proposed**, source: seed, confidence: high

### `proc:formulation` — Formulation and blending
- aliases: formulate
- operates on: cat:solvents; cat:surfactants
- produces: end:paints-coatings; end:detergents
- search terms: formulation plant; blending and filling plant; contract manufacturer
- status: **proposed**, source: seed, confidence: medium

### `proc:hydrodealkylation` — Hydrodealkylation
- aliases: hda
- operates on: sub:toluene
- produces: sub:benzene
- search terms: hda unit; benzene plant
- status: **proposed**, source: seed, confidence: medium

### `proc:hydrogenation` — Hydrogenation
- operates on: cat:oleochemical-feedstocks
- produces: cat:fatty-acids
- search terms: hydrogenation plant; fatty alcohol plant
- status: **proposed**, source: seed, confidence: medium

### `proc:polymerization` — Polymerization
- aliases: polymerisation; polymerize
- operates on: sub:ethylene; sub:propylene; sub:acrylic-acid; sub:vinyl-chloride
- produces: cat:polymers
- search terms: polymer plant; polymerization plant; resin plant
- status: **proposed**, source: seed, confidence: high

### `proc:refining` — Refining
- aliases: refine; oil refining
- operates on: sub:crude-oil
- produces: cat:fuel-oils; cat:base-oils; sub:naphtha
- search terms: oil refinery; crude oil refining; refinery operator
- status: **proposed**, source: seed, confidence: high

### `proc:sulphonation` — Sulphonation
- aliases: sulfonation
- operates on: sub:linear-alkylbenzene
- produces: sub:labsa
- search terms: sulphonation plant; labsa plant
- status: **proposed**, source: seed, confidence: high

### `proc:transesterification` — Transesterification
- aliases: transesterify
- operates on: sub:palm-oil; sub:used-cooking-oil; sub:tallow
- produces: cat:biodiesel; sub:glycerin
- search terms: biodiesel plant; transesterification plant; biodiesel refinery
- status: **proposed**, source: seed, confidence: high


## End products (value-chain leaves → company discovery)

### `end:adhesives-sealants` — Adhesives and sealants
- aliases: adhesive; sealant
- consumes: cat:solvents; sub:butyl-acrylate; sub:mdi
- operator phrases: adhesive manufacturer; sealant manufacturer; adhesive plant
- status: **proposed**, source: seed, confidence: high

### `end:biodiesel-blend` — Biodiesel blending
- aliases: biodiesel blend; fuel blending
- consumes: cat:biodiesel
- operator phrases: biodiesel blender; fuel blending terminal; biodiesel supplier
- status: **proposed**, source: seed, confidence: high

### `end:cable-wire` — Cable and wire
- aliases: cable; wire and cable
- consumes: sub:pvc-resin; cat:plasticizers
- operator phrases: cable manufacturer; wire and cable plant; cable compound producer
- status: **proposed**, source: seed, confidence: high

### `end:detergents` — Detergents
- aliases: detergent; home care
- consumes: sub:labsa; cat:surfactants
- operator phrases: detergent manufacturer; detergent plant; home care manufacturer
- status: **proposed**, source: seed, confidence: high

### `end:flexible-pu-foam` — Flexible PU foam
- aliases: flexible foam; pu foam
- consumes: sub:toluene-diisocyanate; sub:polyether-polyol
- operator phrases: flexible foam manufacturer; pu foam plant; mattress foam producer
- status: **proposed**, source: seed, confidence: high

### `end:greases` — Greases
- aliases: grease
- consumes: sub:base-oil; cat:fatty-acids
- operator phrases: grease manufacturer; grease plant; lubricating grease producer
- status: **proposed**, source: seed, confidence: high

### `end:metalworking-fluids` — Metalworking fluids
- aliases: cutting fluids; coolants
- consumes: sub:base-oil; cat:esters
- operator phrases: metalworking fluid manufacturer; cutting fluid plant; coolant manufacturer
- status: **proposed**, source: seed, confidence: high

### `end:paints-coatings` — Paints and coatings
- aliases: paint; coatings
- consumes: cat:solvents; sub:titanium-dioxide; sub:epoxy-resin; sub:butyl-acrylate
- operator phrases: paint manufacturer; coatings manufacturer; paint factory; industrial coatings plant
- status: **proposed**, source: seed, confidence: high

### `end:personal-care` — Personal care products
- aliases: personal care; cosmetics
- consumes: sub:glycerin; cat:surfactants
- operator phrases: personal care manufacturer; cosmetic ingredient supplier; toiletries plant
- status: **proposed**, source: seed, confidence: medium

### `end:pvc-compound` — PVC compounding
- aliases: pvc compound; pvc compounds
- consumes: sub:pvc-resin; cat:plasticizers
- operator phrases: pvc compound manufacturer; pvc compounder; masterbatch plant; cable compound manufacturer
- status: **proposed**, source: seed, confidence: high

### `end:rigid-pu-foam` — Rigid PU foam
- aliases: rigid foam; insulation foam
- consumes: sub:mdi; sub:polyether-polyol
- operator phrases: rigid foam manufacturer; insulation panel manufacturer; pu insulation plant
- status: **proposed**, source: seed, confidence: high

### `end:soap` — Soap manufacturing
- aliases: soap producer; soap plant
- consumes: cat:fatty-acids; sub:caustic-soda
- operator phrases: soap manufacturer; soap plant; toilet soap producer
- status: **proposed**, source: seed, confidence: high

### `end:superabsorbent-polymer` — Superabsorbent polymer
- aliases: sap
- consumes: sub:acrylic-acid
- operator phrases: superabsorbent polymer manufacturer; sap plant; hygiene absorbent manufacturer
- status: **proposed**, source: seed, confidence: high


## Lint findings

- `warning` warning: 134 of 134 node(s) are proposed and will not resolve until reviewed
- `warning` warning: proc:distillation.produces: process names no output
- `warning` warning: sub:diesel.hs_codes: HS code 2710.19 is also used by sub:base-oil
- `warning` warning: sub:dinp.hs_codes: HS code 2917.34 is also used by sub:dibutyl-phthalate
- `warning` warning: sub:fuel-oil.hs_codes: HS code 2710.19 is also used by sub:base-oil
- `warning` warning: sub:lubricant-additives.hs_codes: no HS code; the trade-DB match cannot use it
- `warning` warning: sub:nonylphenol-ethoxylate.hs_codes: no HS code; the trade-DB match cannot use it
- `warning` warning: sub:polyether-polyol.hs_codes: no HS code; the trade-DB match cannot use it
- `warning` warning: sub:toluene-diisocyanate.hs_codes: HS code 2929.10 is also used by sub:mdi
