This review audits the seed knowledge base across five dimensions: chemical & process accuracy, customs (HS 2022) nomenclature, taxonomic classification, graph symmetry & referential logic, and missing value-chain bridges.
1. Critical Chemical & Process Accuracy Fixes
proc:hydrogenation (cat:oleochemical-feedstocks 
→
→
 cat:fatty-acids is chemically wrong):
Issue: Hydrogenation of triglyceride feedstocks (cat:oleochemical-feedstocks) saturates double bonds to produce hardened/hydrogenated oils (stearin/hard fats), not fatty acids. Splitting triglycerides into fatty acids + glycerin is fat splitting (hydrolysis). Meanwhile, the node's own search_terms include "fatty alcohol plant"—because high-pressure catalytic hydrogenation/hydrogenolysis of fatty acids or biodiesel (FAME) with hydrogen (sub:hydrogen) produces fatty alcohols.
Fix: Added proc:fat-splitting (cat:oleochemical-feedstocks 
→
→
 cat:fatty-acids, sub:glycerin), added cat:fatty-alcohols and sub:fatty-alcohol, and corrected proc:hydrogenation to operate on ["cat:fatty-acids", "cat:biodiesel", "sub:hydrogen"] to produce ["cat:fatty-alcohols"].
sub:benzene tagged as cat:aromatic-solvents:
Issue: Benzene is a Group 1 carcinogen and is prohibited or strictly phased out as a general industrial solvent (paints, coatings, adhesives) in virtually all jurisdictions. Only toluene, xylene, and higher aromatics (e.g., C9/solvent naphtha) belong in cat:aromatic-solvents.
Fix: Removed cat:aromatic-solvents from sub:benzene (retaining cat:aromatics).
sub:vinyl-chloride tagged as cat:olefins:
Issue: Vinyl chloride monomer (
CH
2
=CHCl
CH 
2
​
 =CHCl
) is a chlorinated alkene/monomer, not a hydrocarbon olefin (ethylene, propylene, butadiene).
Fix: Introduced cat:chlorinated-monomers under cat:petrochemicals and moved sub:vinyl-chloride (along with new node sub:epichlorohydrin) into it.
sub:phthalic-anhydride tagged as cat:plasticizers:
Issue: Phthalic anhydride is an organic acid anhydride intermediate used to synthesize phthalate plasticizers (DOP, DBP, DINP) and alkyd/UPR resins—it is not itself a plasticizer. Leaving it in cat:plasticizers also causes proc:compounding (operates_on: ["sub:pvc-resin", "cat:plasticizers"]) and end:cable-wire to falsely imply compounding PVC directly with phthalic anhydride.
Fix: Introduced cat:chemical-intermediates under cat:petrochemicals and moved sub:phthalic-anhydride out of cat:plasticizers into ["cat:aromatics", "cat:chemical-intermediates"].
sub:pfad deriving sub:glycerin:
Issue: Palm fatty acid distillate (PFAD) is a physical-refining byproduct composed predominantly (~80–90%+) of free fatty acids, with only minor residual glycerides. Unlike crude palm oil, palm kernel oil, coconut oil, or tallow, PFAD is not a commercial source of glycerin.
Fix: Removed sub:glycerin from sub:pfad.derived_to (retaining cat:fatty-acids, and adding cat:biodiesel via acid esterification).
sub:urea tagged as cat:inorganics:
Issue: Chemically, urea (
CO(NH
2
)
2
CO(NH 
2
​
 ) 
2
​
 
) is an organic compound (carbamide) synthesized from ammonia and 
CO
2
CO 
2
​
 
, and customs-wise it sits in Chapter 31 (Fertilizers, 3102.10), not Chapter 28 (Inorganics).
Fix: Reassigned sub:urea to cat:petrochemicals with an expert_note documenting its common trade grouping alongside ammonia/nitrogen fertilizers.
Refining & Cracking Co-product Omissions:
proc:cracking: Steam cracking of naphtha co-produces butadiene (sub:butadiene) alongside ethylene and propylene (as well as pygas aromatics). Linked sub:naphtha 
→
→
 sub:butadiene and added sub:butadiene to proc:cracking.produces, resolving sub:butadiene as a completely orphaned node.
proc:chlor-alkali: Electrolysis of brine obligately co-produces hydrogen (2 NaCl + 2 H2O -> Cl2 + 2 NaOH + H2). Added sub:hydrogen and linked it to proc:chlor-alkali.produces, sub:ammonia, sub:hydrochloric-acid, proc:hydrogenation, and proc:hydrodealkylation.
proc:refining & sub:crude-oil: Added sub:diesel to sub:crude-oil.derived_to and proc:refining.produces so sub:diesel is no longer disconnected.
2. Customs / HS Nomenclature Corrections (HS 2022)
sub:dop & sub:dinp had swapped/wrong HS codes:
sub:dop (Dioctyl orthophthalates) was coded 2917.33, which actually covers dinonyl or didecyl orthophthalates[1]. Corrected sub:dop to 2917.32[1].
sub:dinp (Diisononyl phthalate) was coded 2917.34 (other orthophthalic esters, such as DBP)[1]. Corrected sub:dinp to 2917.33[1].
sub:labsa used an obsolete pre-2022 HS code:
3402.11 was restructured in HS 2022[2][3]; linear alkylbenzene sulphonic acids and their salts now have their own dedicated subheading 3402.31[2][4].
sub:tallow used a superseded HS code:
Heading 1502.00 was split in HS 2012 into 1502.10 (Tallow) and 1502.90 (Other)[5]. Updated sub:tallow to 1502.10.
Incomplete HS coverage relative to existing aliases:
sub:glycerin: Aliases include "crude glycerin", which is classified under 1520.00, whereas refined glycerol is 2905.45[6][7]. Added 1520.00.
sub:polyethylene: Aliases include "hdpe", but 3901.10 only covers polyethylene with specific gravity 
<
0.94
<0.94
 (LDPE/LLDPE)[8][9]. Added 3901.20 (
SG
≥
0.94
SG≥0.94
, HDPE)[9].
sub:ethanolamines: Aliases list MEA, DEA, and TEA, but 2922.11 covers only MEA; DEA is 2922.12 and TEA is 2922.15[10][11]. Added both.
sub:caustic-soda: Aliases include "lye", yet 2815.11 is solid NaOH only; liquid/aqueous caustic soda is 2815.12[12]. Added 2815.12.
cat:fatty-acids: 3823.11 covers stearic acid only; added 3823.12 (oleic acid), 3823.13 (tall oil fatty acids), and 3823.19 (other industrial monocarboxylic fatty acids).
sub:titanium-dioxide: 2823.00 covers unfinished/non-pigmentary titanium oxides; pigment-grade 
TiO
2
TiO 
2
​
 
 (
≥
80
%
≥80%
 
TiO
2
TiO 
2
​
 
) used in paints/coatings trades predominantly under 3206.11[13][14]. Added 3206.11.
sub:coal: Aliases include "coking coal" and "thermal coal". Under WCO Subheading Note 2, bituminous/metallurgical coal is 2701.12 (and anthracite 2701.11), while 2701.19 covers other/sub-bituminous coal (though some national tariffs map coking coal inside 2701.19)[15][16][17]. Added 2701.12 and 2701.11.
sub:xylene: Added isomer codes 2902.41 (o-xylene, the direct PA feedstock), 2902.42 (m-xylene), and 2902.43 (p-xylene) alongside 2902.44 (mixed xylenes).
Added missing HS codes for: cat:biodiesel (3826.00[8]), sub:polyether-polyol (3907.29[18]), sub:nonylphenol-ethoxylate (3402.42 under HS 2022[2][19]), sub:lubricant-additives (3811.21, 3811.29[20]), plus refined grades for sub:palm-oil (1511.90), sub:coconut-oil (1513.19), sub:palm-kernel-oil (1513.29), sub:ethanol (2207.20 denatured), sub:ammonia (2814.20 aqueous), and sub:polypropylene (3902.30 copolymers).
3. Graph Logic, Symmetry & Dead-End Fixes
cat:oils hypernym leakage into proc:blending:
cat:oils has hyponyms: ["cat:lubricants", "cat:fuel-oils", "sub:crude-oil", "cat:oleochemical-feedstocks"]. Because proc:blending operated on cat:oils and produced cat:lubricants and cat:fuel-oils, it implicitly claimed that blending vegetable/animal oleochemical feedstocks yields petroleum lubricants and fuel oils.
Fix: Changed proc:blending.operates_on to ["sub:base-oil", "sub:lubricant-additives", "sub:fuel-oil", "sub:crude-oil"], and changed its output from cat:lubricants to cat:finished-lubricants, which also resolves cat:finished-lubricants being an unused orphan category.
cat:soap vs end:soap orphan/duplication:
cat:soap existed as a category (with aliases "soap noodles", "toilet soap base") with zero member substances, while end:soap represented "Soap manufacturing".
Fix: Added sub:soap-noodles (HS 3401.20[21][22]) in cat:soap, feeding end:soap, and added proc:saponification to connect cat:fatty-acids / cat:oleochemical-feedstocks + sub:caustic-soda 
→
→
 sub:soap-noodles / end:soap.
Bidirectional Edge Asymmetries Repaired:
end:superabsorbent-polymer consumed sub:acrylic-acid 
→
→
 added end:superabsorbent-polymer to sub:acrylic-acid.consumed_in.
end:adhesives-sealants consumed sub:mdi 
→
→
 added end:adhesives-sealants to sub:mdi.consumed_in.
end:pvc-compound consumed sub:pvc-resin 
→
→
 added end:pvc-compound to sub:pvc-resin.consumed_in.
sub:ethanol had consumed_in: ["proc:esterification"] 
→
→
 added sub:ethanol (along with sub:acrylic-acid, sub:acetic-acid, sub:2-ethylhexanol) to proc:esterification.operates_on.
sub:methyl-methacrylate had consumed_in: ["proc:polymerization"] 
→
→
 added sub:methyl-methacrylate (and sub:styrene, sub:butadiene) to proc:polymerization.operates_on.
sub:lubricant-additives had consumed_in: ["proc:blending"] 
→
→
 added to proc:blending.operates_on.
sub:methanol had consumed_in: ["proc:transesterification"] 
→
→
 added sub:methanol (plus sub:coconut-oil, sub:palm-kernel-oil) to proc:transesterification.operates_on, and added proc:transesterification to sub:tallow.consumed_in and sub:glycerin to sub:used-cooking-oil.derived_to.
proc:formulation expanded to reflect end:adhesives-sealants, end:personal-care, end:greases, and end:metalworking-fluids alongside paints and detergents.
Disconnected Existing Nodes Linked Up:
sub:benzene + sub:nitric-acid 
→
→
 sub:aniline + sub:formaldehyde 
→
→
 sub:mdi.
sub:toluene + sub:nitric-acid 
→
→
 sub:toluene-diisocyanate (previously toluene only linked to benzene via HDA).
sub:toluene and sub:xylene linked to end:paints-coatings and end:adhesives-sealants as aromatic solvents.
sub:acetone, sub:mek, sub:isopropanol, sub:ethanol linked to solvent end-uses (end:paints-coatings, end:adhesives-sealants, end:personal-care).
sub:ammonia linked to sub:urea, sub:nitric-acid, sub:ethanolamines, and end:fertilizers.
sub:sulphuric-acid linked to proc:sulphonation (sub:labsa) and end:fertilizers.
sub:soda-ash linked to end:detergents and new end:glass.
4. Typo, Alias & Taxonomy Refinements
Typo fix: sub:butyl-glycol-ether had alias "ebutyl glycol" 
→
→
 corrected to "butyl glycol" and added "2-butoxyethanol", "butyl cellosolve".
Alias precision:
cat:oleochemical-feedstocks: Removed "edible oil" as a direct category synonym because the category contains inedible/animal feedstocks (sub:tallow, sub:used-cooking-oil); added "triglyceride feedstock", "fats and oils", "oleochemical feedstock".
cat:coal-products: Removed "coke" as a direct synonym for the whole category (coke is one product alongside coal, coal tar, and pitch); marked is_intermediate: true.
cat:fatty-acids: Added "lauric acid", "palmitic acid", "distilled fatty acid".
cat:biodiesel: Added "b100", "pme", "ucome", "sme".
sub:polyethylene: Added "lldpe", "polythene".
sub:polyether-polyol: Added "ppg", "polymer polyol".
sub:xylene: Added "o-xylene", "ortho-xylene", "p-xylene", "para-xylene".
sub:hydrogen-peroxide: Added "peroxide" and linked to bleaching/epoxidation/chemical oxidation (proc:oxidation or end:pulp-paper / end:personal-care).
Category structural polish: Created cat:organic-acids / cat:chemical-intermediates so sub:acrylic-acid (chemically an unsaturated carboxylic acid rather than an ester) can carry cat:chemical-intermediates alongside cat:acrylates (preserving its commercial value-chain grouping).
5. New Substances, Categories, Processes & End-Products Added
To eliminate "floating" chains where precursors and products existed in the seed without their mandatory chemical bridges, the following high-leverage nodes were added (tagged "source": "llm-review"):
New Categories (cat:*): cat:chemical-intermediates, cat:chlorinated-monomers, cat:fatty-alcohols, cat:styrenics.
New Intermediates / Substances (sub:*):
sub:ethylene-oxide (2910.10[23]) — bridges sub:ethylene to sub:ethylene-glycol, sub:ethanolamines, sub:butyl-glycol-ether, sub:nonylphenol-ethoxylate, and sub:sles.
sub:propylene-oxide (2910.20[23]) — bridges sub:propylene to sub:propylene-glycol and sub:polyether-polyol.
sub:acetic-acid (2915.21[24]) — bridges sub:methanol to sub:ethyl-acetate and sub:butyl-acetate.
sub:2-ethylhexanol (2905.16[24]) — bridges sub:propylene to sub:dop (with sub:phthalic-anhydride).
sub:bisphenol-a (2907.23[23]) & sub:epichlorohydrin (2910.30[23]) — bridge sub:phenol + sub:acetone and sub:propylene + sub:chlorine to sub:epoxy-resin.
sub:fatty-alcohol (3823.70, 2905.17[25][26]) & sub:sles (3402.39) — complete the oleochemical surfactant chain (cat:fatty-acids / cat:biodiesel 
→
→
 sub:fatty-alcohol 
→
→
 sub:sles 
→
→
 end:detergents / end:personal-care).
sub:soap-noodles (3401.20[21]) — populates cat:soap and bridges cat:fatty-acids + sub:caustic-soda to end:soap.
sub:hydrogen (2804.10[27]) — chlor-alkali co-product feeding sub:ammonia, sub:hydrochloric-acid, and proc:hydrogenation / proc:hydrodealkylation.
sub:polystyrene (3903.11, 3903.19[27]) — gives sub:styrene a primary polymer outlet.
New Processes (proc:*): proc:catalytic-reforming (naphtha 
→
→
 BTX), proc:ethoxylation, proc:fat-splitting, proc:saponification.
New End-Products (end:*): end:coolants-antifreeze (consumes MEG/MPG), end:fertilizers (consumes urea, ammonia, nitric/phosphoric/sulphuric acids), end:glass (consumes soda ash), end:tires-rubber (consumes carbon black, butadiene, styrene).