# Demand eval — Methanol

_Run eval-methanol-india · 20 discovery queries (199 hits) · 36 verification queries · 15 pages enriched · 1 leads rejected · completed 2026-11-09 22:31_

## Inferred chain

| Stage | Steps from the product | Rationale |
| :--- | :--- | :--- |
| Formaldehyde Production | 1 | Consumes Methanol as a feedstock |
| Acetic Acid Production | 1 | Consumes Methanol as a feedstock |
| Methyl Tert-Butyl Ether (MTBE) Production | 1 | Consumes Methanol as a feedstock |
| Dimethyl Ether (DME) Production | 1 | Consumes Methanol as a feedstock |
| Biodiesel Production | 1 | Consumes Methanol in transesterification |
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
- **Expected-buyer fragments surfaced: 2/2**
  - "gnfc" — verified: GNFC Ltd. (0.52); stage candidate [Acetic Acid Production]: GNFC Ltd. (corroborated); stage candidate [Acetic Acid Production]: GNFC to invest Rs 250 cr in acetic acid plant (lead)
  - "narmada" — verified: Gujarat Narmada Valley Fertilizers & Chemicals Limited (0.52); stage candidate [Acetic Acid Production]: Gujarat Narmada Valley Fertilizers & Chemicals Limited (corroborated)
  - missed: none
- **Verified buyers: 12** — 12 tied to expected direct stage(s), 0 to a derived stage, 0 unclassified

## Verified buyers

- **Aldehydes India** — stage: Formaldehyde Production · country: India · status: corroborated · confidence: 0.98
- **GAIL** — stage: Acetic Acid Production · country: India · status: corroborated · confidence: 0.98
- **Kunnathan Polymers** — stage: Formaldehyde Production · country: India · status: verified · confidence: 0.98
- **PTG Advanced Catalysts Co., Ltd.** — stage: Formaldehyde Production · country:  · status: verified · confidence: 0.98
- **Deepak Fertilisers and Petrochemicals Corporation** — stage: Acetic Acid Production · country: India · status: corroborated · confidence: 0.88
- **Methanol Use In Gasoline** — stage: Methyl Tert-Butyl Ether (MTBE) Production · country:  · status: verified · confidence: 0.75
- **Reta Engineering** — stage: Formaldehyde Production · country: India · status: verified · confidence: 0.72
- **Guruji Overseas** — stage: Formaldehyde Production · country: India · status: verified · confidence: 0.70
- **NPTEL IIT Guwahati** — stage: Formaldehyde Production · country: India · status: verified · confidence: 0.70
- **GNFC Ltd.** — stage: Acetic Acid Production · country: India · status: corroborated · confidence: 0.52
- **Gujarat Narmada Valley Fertilizers & Chemicals Limited** — stage: Acetic Acid Production · country: India · status: corroborated · confidence: 0.52
- **Methanol Carbonylation - Energyscope** — stage: Acetic Acid Production · country:  · status: verified · confidence: 0.45

## Warnings

- Private procurement is invisible to web research: this run only sees demand that surfaced publicly.
- Buyer scope: manufacturers whose process consumes the product directly (step 1) or via one intermediate transformation (step 2); deeper value-chain stages are out of scope.
- Model-inferred stages are hypotheses; each result names the consuming stage it was tied to.
- Traders, brokers and trading houses are excluded; rerun with intermediaries to include them.
