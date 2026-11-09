# Demand eval — Methanol

_Run eval-methanol-india · 20 discovery queries (194 hits) · 36 verification queries · 18 pages enriched · 0 leads rejected · completed 2026-11-09 16:03_

## Inferred chain

| Stage | Steps from the product | Rationale |
| :--- | :--- | :--- |
| Formaldehyde Production | 1 | Consumes Methanol as a feedstock |
| Acetic Acid Production | 1 | Consumes Methanol as a feedstock |
| Methyl Tert-Butyl Ether (MTBE) Production | 1 | Consumes Methanol as a feedstock |
| Biodiesel Production | 1 | Consumes Methanol in transesterification |
| Dimethyl Ether (DME) Production | 1 | Consumes Methanol as a feedstock |
| Methyl Methacrylate (MMA) Production | 1 | Consumes Methanol as a feedstock |
| MDI Production | 2 | Consumes Formaldehyde (distance-1 from Methanol) |

## Scorecard

- **Expected direct stages captured: 5/7**
  - acetic acid — labelled step 1
  - biodiesel — labelled step 1
  - dimethyl ether — labelled step 1
  - formaldehyde — labelled step 1
  - mtbe — labelled step 1
  - missed: olefin, methylamine
- **Scope leaks in the chain: 0** — none
- **Proposals dropped by the two-step gate: 0** — none
- **Bait leakage in verified results: 0** — none
- **Intermediary leakage in verified results: 0** — none
- **Expected-buyer fragments surfaced: 1/2**
  - "narmada" — verified: Gujarat Narmada Valley Fertilizers & Chemicals Limited (0.98); stage candidate [Acetic Acid Production]: Gujarat Narmada Valley Fertilizers & Chemicals Limited (corroborated)
  - missed: gnfc
- **Verified buyers: 11** — 11 tied to expected direct stage(s), 0 to a derived stage, 0 unclassified

## Verified buyers

- **Formaldehyde Production Plant Cost, Setup, DPR 2026** — stage: Formaldehyde Production · country: India · status: corroborated · confidence: 0.98
- **Gujarat Narmada Valley Fertilizers & Chemicals Limited** — stage: Acetic Acid Production · country: India · status: corroborated · confidence: 0.98
- **Reta** — stage: Formaldehyde Production · country: India · status: corroborated · confidence: 0.98
- **Indian Institute of Technology Guwahati** — stage: Formaldehyde Production · country: India · status: verified · confidence: 0.98
- **Formaldehyde Production Plant in India** — stage: Formaldehyde Production · country: India · status: corroborated · confidence: 0.90
- **APL** — stage: Acetic Acid Production · country: India · status: corroborated · confidence: 0.88
- **Assam Petrochemicals** — stage: Acetic Acid Production · country: India · status: corroborated · confidence: 0.88
- **Deepak Fertilisers and Petrochemicals Corporation** — stage: Acetic Acid Production · country: India · status: corroborated · confidence: 0.88
- **Guruji Overseas** — stage: Formaldehyde Production · country: India · status: verified · confidence: 0.70
- **acetic acid production, from high-pressure methanol ...** — stage: Acetic Acid Production · country:  · status: verified · confidence: 0.55
- **Methanol Carbonylation - Energyscope** — stage: Acetic Acid Production · country:  · status: verified · confidence: 0.45

## Warnings

- Private procurement is invisible to web research: this run only sees demand that surfaced publicly.
- Buyer scope: manufacturers whose process consumes the product directly (step 1) or via one intermediate transformation (step 2); deeper value-chain stages are out of scope.
- Model-inferred stages are hypotheses; each result names the consuming stage it was tied to.
- Traders, brokers and trading houses are excluded; rerun with intermediaries to include them.
