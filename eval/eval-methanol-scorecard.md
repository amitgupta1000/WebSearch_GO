# Demand eval — Methanol

_Run eval-methanol · 20 discovery queries (188 hits) · 36 verification queries · 24 pages enriched · 0 leads rejected · completed 2026-11-09 15:17_

## Inferred chain

| Stage | Steps from the product | Rationale |
| :--- | :--- | :--- |
| Formaldehyde Production | 1 | Consumes Methanol as a feedstock. |
| Biodiesel Production | 1 | Consumes Methanol in transesterification. |
| Methyl Tertiary Butyl Ether (MTBE) Production | 1 | Consumes Methanol as a reactant. |
| Acetic Acid Production | 1 | Consumes Methanol via carbonylation. |
| Dimethyl Ether (DME) Production | 1 | Consumes Methanol as a feedstock. |
| Methyl Methacrylate (MMA) Production | 1 | Consumes Methanol as a feedstock. |
| Methylamine Production | 1 | Consumes Methanol as a feedstock. |

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

- **AFDC** — stage: Biodiesel Production · country:  · status: corroborated · confidence: 0.98
- **Alfa Laval** — stage: Biodiesel Production · country:  · status: verified · confidence: 0.98
- **Anguil Environmental Systems, Inc.** — stage: Formaldehyde Production · country:  · status: corroborated · confidence: 0.98
- **HTA** — stage: Biodiesel Production · country: Italy · status: corroborated · confidence: 0.98
- **Methanex** — stage: Formaldehyde Production · country:  · status: corroborated · confidence: 0.98
- **Reichhold** — stage: Formaldehyde Production · country: USA · status: corroborated · confidence: 0.98
- **SL Chemtech** — stage: Formaldehyde Production · country:  · status: corroborated · confidence: 0.98
- **mdpi.com** — stage: Formaldehyde Production · country:  · status: corroborated · confidence: 0.98
- **pubs.acs.org** — stage: Biodiesel Production · country:  · status: corroborated · confidence: 0.98
- **elchemy.com** — stage: Formaldehyde Production · country:  · status: corroborated · confidence: 0.86
- **DME: AN EMERGING GLOBAL FUEL** — stage: Dimethyl Ether (DME) Production · country:  · status: verified · confidence: 0.75
- **operachem.com** — stage: Biodiesel Production · country:  · status: verified · confidence: 0.70

## Warnings

- Private procurement is invisible to web research: this run only sees demand that surfaced publicly.
- Buyer scope: manufacturers whose process consumes the product directly (step 1) or via one intermediate transformation (step 2); deeper value-chain stages are out of scope.
- Model-inferred stages are hypotheses; each result names the consuming stage it was tied to.
- Traders, brokers and trading houses are excluded; rerun with intermediaries to include them.
