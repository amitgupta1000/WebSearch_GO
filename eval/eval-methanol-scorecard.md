# Demand eval — Methanol

_Run eval-methanol · 20 discovery queries (186 hits) · 36 verification queries · 18 pages enriched · 0 leads rejected · completed 2026-11-09 21:42_

## Inferred chain

| Stage | Steps from the product | Rationale |
| :--- | :--- | :--- |
| Formaldehyde Production | 1 | Consumes Methanol as a primary feedstock. |
| Biodiesel Production | 1 | Consumes Methanol in transesterification reaction. |
| Methyl Tertiary Butyl Ether (MTBE) Production | 1 | Consumes Methanol as a reactant. |
| Acetic Acid Production | 1 | Consumes Methanol via carbonylation. |
| Dimethyl Ether (DME) Production | 1 | Consumes Methanol as a feedstock. |
| Methyl Methacrylate (MMA) Production | 1 | Consumes Methanol as a feedstock in some processes. |
| Methylamine Production | 1 | Consumes Methanol as a reactant. |

## Scorecard

- **Expected direct stages captured: 6/8**
  - acetic acid — labelled step 1
  - biodiesel — labelled step 1
  - dimethyl ether — labelled step 1
  - formaldehyde — labelled step 1
  - methylamine — labelled step 1
  - mtbe — labelled step 1
  - missed: olefin, methyl chloride
- **Scope leaks in the chain: 0** — none
- **Proposals dropped by the two-step gate: 0** — none
- **Bait leakage in verified results: 0** — none
- **Intermediary leakage in verified results: 0** — none
- **Verified buyers: 12** — 12 tied to expected direct stage(s), 0 to a derived stage, 0 unclassified

## Verified buyers

- **Alfa Laval** — stage: Biodiesel Production · country:  · status: corroborated · confidence: 0.98
- **Anguil Environmental Systems, Inc.** — stage: Formaldehyde Production · country:  · status: corroborated · confidence: 0.98
- **August Wilhelm von Hofmann** — stage: Formaldehyde Production · country:  · status: corroborated · confidence: 0.98
- **Biodiesel Producers** — stage: Biodiesel Production · country:  · status: corroborated · confidence: 0.98
- **Biodiesel Production and Distribution** — stage: Biodiesel Production · country:  · status: corroborated · confidence: 0.98
- **HTA** — stage: Biodiesel Production · country: Italy · status: corroborated · confidence: 0.98
- **Reichhold** — stage: Formaldehyde Production · country: USA · status: corroborated · confidence: 0.98
- **SLchemtech** — stage: Formaldehyde Production · country:  · status: corroborated · confidence: 0.98
- **YS Erchamo** — stage: Biodiesel Production · country:  · status: verified · confidence: 0.98
- **DME: AN EMERGING GLOBAL FUEL** — stage: Dimethyl Ether (DME) Production · country:  · status: verified · confidence: 0.75
- **acetic acid production, from low-pressure methanol ...** — stage: Acetic Acid Production · country:  · status: verified · confidence: 0.65
- **Methanex** — stage: Formaldehyde Production · country:  · status: corroborated · confidence: 0.52

## Warnings

- Private procurement is invisible to web research: this run only sees demand that surfaced publicly.
- Buyer scope: manufacturers whose process consumes the product directly (step 1) or via one intermediate transformation (step 2); deeper value-chain stages are out of scope.
- Model-inferred stages are hypotheses; each result names the consuming stage it was tied to.
- Traders, brokers and trading houses are excluded; rerun with intermediaries to include them.
