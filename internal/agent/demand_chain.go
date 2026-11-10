package agent

// demand_chain.go owns the buyer-side search mode: who consumes or buys a
// product. It is the deliberate counterpart of market_landscape.go — a separate
// agent with its own phases, because the buyer problem has an extra first phase
// (the value-chain inference) and different evidence semantics (demand signals,
// recency, bait), but it reuses the market mode's evidence vocabulary, identity
// hygiene and tier classification unchanged.
//
// The phases are: infer (the consuming stages of the product), discover
// (operators of those stages plus product-shaped demand lanes), verify
// (consumption evidence through the leads' own tier rules), and assemble
// (deterministically, like every other mode).

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

const (
	demandMaxDiscoveryQueries    = 20
	demandMinDiscoveryQueries    = 2
	demandMaxVerificationQueries = 70
	demandMaxLeadsEnriched       = 24
	demandMaxStages              = 7
	// Web key-person discovery and the optional RocketReach discovery each get
	// their own 20-search budget, carved out of the verification budget when
	// the people flags are on — the same discipline the market mode uses.
	demandPeopleQueryReserve = 40
	// Bounded concurrency for the pipeline's network-bound stages: lane
	// searches and LLM extraction batches run three at a time, and lead
	// enrichment rides the engine's shared scrape semaphore. Order of results
	// never depends on completion order.
	demandLaneSearchConcurrency = 3
	demandExtractionConcurrency = 3
	demandExtractionBatchSize   = 4
)

// DemandChainAgent owns the buyer-side workflow: infer the value chain, discover
// operators of the consuming stages and public demand signals, verify
// consumption, and assemble the report deterministically.
type DemandChainAgent struct {
	engine      *Engine
	rocketReach *tools.RocketReachClient
}

func NewDemandChainAgent(engine *Engine, rocketReach ...*tools.RocketReachClient) *DemandChainAgent {
	var client *tools.RocketReachClient
	if len(rocketReach) > 0 {
		client = rocketReach[0]
	}
	return &DemandChainAgent{engine: engine, rocketReach: client}
}

// Research runs the buyer pipeline. Existing search modes are not involved.
func (a *DemandChainAgent) Research(ctx context.Context, researchID string, query models.DemandChainQuery, eventCh chan<- models.AgentEvent) (*models.DemandChainReport, error) {
	query.Activity = strings.TrimSpace(query.Activity)
	if query.Activity == "" {
		return nil, fmt.Errorf("market activity is required")
	}
	if strings.TrimSpace(query.Objective) == "" {
		query.Objective = query.Activity
	}
	// Budget normalization: zero keeps the mode defaults (MaxCompanies included),
	// and the caps keep a mistyped budget from running away.
	normalizeDemandBudget(&query)
	// The people phase draws its searches from the same Serper budget as
	// verification, so enabling it carves the reserve out of the verification
	// budget, exactly as the market mode does.
	if query.IncludePeople || query.UseRocketReachDiscovery {
		if query.VerificationQueries > demandPeopleQueryReserve {
			query.VerificationQueries -= demandPeopleQueryReserve
		}
	}

	report := &models.DemandChainReport{
		ResearchID: researchID, Objective: query.Objective, Activity: query.Activity,
		Geography:  demandGeography(query),
		Iterations: 2, StartedAt: time.Now(),
	}

	// Phase 1 — the value chain. The operator's stages win; otherwise one
	// structured call proposes the consuming stages the whole run is organised
	// around. Failure degrades to a generic stage rather than failing the run.
	emitAgentEvent(eventCh, models.EventThinking, "Inferring the demand chain", "", "")
	chain, inferred, warnings := a.inferDemandChain(ctx, query, eventCh)
	report.Chain = chain
	report.ChainInferred = inferred
	report.Warnings = append(report.Warnings, warnings...)
	report.Warnings = append(report.Warnings,
		"Private procurement is invisible to web research: this run only sees demand that surfaced publicly.",
		"Buyer scope: manufacturers whose process consumes the product directly (step 1) or via one intermediate transformation (step 2); deeper value-chain stages are out of scope.",
		"Model-inferred stages are hypotheses; each result names the consuming stage it was tied to.")
	if !query.IncludeIntermediaries {
		report.Warnings = append(report.Warnings, "Traders, brokers and trading houses are excluded; rerun with intermediaries to include them.")
	}

	// Phase 2 — discovery. Stage-operator lanes (the chain's own vocabulary)
	// run first, then product-shaped demand lanes: tenders, importers, input
	// language.
	companies := make(map[string]*models.MarketLandscapeCompany)
	var leads []models.MarketLandscapeEvidence
	lanes := demandChainLanes(query, chain)
	laneStages := make(map[string]string, len(lanes))
	for _, lane := range lanes {
		report.Lanes = append(report.Lanes, lane.term)
		laneStages[strings.ToLower(lane.term)] = lane.stage
	}
	emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Discovering demand across %d lane(s)", len(lanes)), "", "")
	lanesToRun := selectDemandLanes(lanes, query.DiscoveryQueries)
	emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Searching %d lane(s) with bounded concurrency", len(lanesToRun)), "", "")
	// Lane searches are bounded-concurrent (three at a time) and
	// order-preserving: results land in the lane's slot, so the leads, the
	// appendix and the stage attribution never depend on completion order.
	type laneSearch struct {
		hits []models.SearchResult
		err  error
	}
	laneResults := make([]laneSearch, len(lanesToRun))
	searchSemaphore := make(chan struct{}, demandLaneSearchConcurrency)
	var searchGroup sync.WaitGroup
	for index, lane := range lanesToRun {
		searchGroup.Add(1)
		go func(index int, lane demandLane) {
			defer searchGroup.Done()
			searchSemaphore <- struct{}{}
			defer func() { <-searchSemaphore }()
			if err := ctx.Err(); err != nil {
				laneResults[index].err = err
				return
			}
			searchQuery := strings.TrimSpace(query.Activity + " " + lane.term + " " + demandGeography(query))
			emitAgentEvent(eventCh, models.EventToolCall, "Searching "+lane.term, "search", searchQuery)
			hits, err := a.engine.SearchCached(ctx, searchQuery, marketMaxResults)
			laneResults[index].hits, laneResults[index].err = hits, err
		}(index, lane)
	}
	searchGroup.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	report.QueriesUsed = len(lanesToRun)
	for index, lane := range lanesToRun {
		hits, err := laneResults[index].hits, laneResults[index].err
		report.SearchHits += len(hits)
		report.LeadsReviewed += len(hits)
		if err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s search failed: %v", lane.term, err))
			continue
		}
		for _, hit := range hits {
			candidate := demandCandidate(hit, lane, query)
			if candidate == nil {
				continue
			}
			if excludedMarketDomain(candidate.Domain, query.Exclusions) || candidate.Evidence[0].Status == "excluded" {
				candidate.Evidence[0].Status = "excluded"
				report.Appendix = append(report.Appendix, candidate.Evidence...)
				continue
			}
			if candidate.EvidenceType == "discovery-lead" {
				leads = append(leads, candidate.Evidence...)
			}
			mergeMarketCompany(companies, candidate)
		}
	}
	report.CandidatesFound = len(companies)

	// Phase 3 — verification. The discovery leads are enriched and classified
	// against consumption-shaped purposes; the confidence ladder weights claim
	// types by how directly they evidence an actual purchase.
	verified, verificationQueries, extracted, rejected, pagesEnriched, verifyErr := a.verifyDemandLeads(ctx, leads, query, laneStages, eventCh)
	report.VerificationQueries = verificationQueries
	report.LeadsExtracted = extracted
	report.LeadsRejected = rejected
	report.PagesEnriched = pagesEnriched
	if verifyErr != nil {
		report.Warnings = append(report.Warnings, verifyErr.Error())
	}
	if query.UseRocketReachDiscovery && (a.rocketReach == nil || !a.rocketReach.Enabled()) {
		report.Warnings = append(report.Warnings, "RocketReach discovery was requested but no API key is configured (ROCKETREACH_API_KEY); web key-person discovery still runs when selected.")
	}

	// Phase 3b — key people. The buyer-side counterpart of the seller's
	// commercial team: procurement, purchasing and supply-chain roles at the
	// verified buyers, via RocketReach first and web discovery second.
	rrCalls, rrProfiles, rrPromoted := 0, 0, 0
	if query.UseRocketReachDiscovery && (a.rocketReach != nil && a.rocketReach.Enabled()) {
		if len(verified) == 0 {
			emitAgentEvent(eventCh, models.EventToolResult, "RocketReach discovery skipped: no buyers were verified", "rocketreach_search", "")
		} else {
			emitAgentEvent(eventCh, models.EventThinking, "Running RocketReach discovery for procurement contacts", "rocketreach_search", "")
			rrCalls, rrProfiles, rrPromoted = a.discoverDemandRocketReach(ctx, verified, demandPeopleQueryReserve/2, eventCh)
			emitAgentEvent(eventCh, models.EventToolResult,
				fmt.Sprintf("RocketReach discovery complete: %d call(s), %d profile(s) returned, %d promoted", rrCalls, rrProfiles, rrPromoted),
				"rocketreach_search", "")
		}
	}
	if query.IncludePeople {
		if len(verified) == 0 {
			emitAgentEvent(eventCh, models.EventToolResult, "Web key-person discovery skipped: no buyers were verified", "search", "")
		} else {
			peopleQueries := a.discoverDemandPeople(ctx, verified, demandPeopleQueryReserve/2, eventCh)
			report.QueriesUsed += peopleQueries
			emitAgentEvent(eventCh, models.EventToolResult, fmt.Sprintf("Web key-person discovery complete: %d procurement search(es)", peopleQueries), "search", "")
		}
	}

	// Phase 4 — deterministic assembly, in the market mode's own shape. The
	// execution must reflect the requested geography: a verified company whose
	// own evidence names a country outside it moves to the appendix, honestly.
	var demotedEvidence []models.MarketLandscapeEvidence
	pool := make([]*models.MarketLandscapeCompany, 0, len(companies)+len(verified))
	for _, company := range companies {
		pool = append(pool, company)
	}
	pool = append(pool, verified...)
	// Alias-level dedupe: "IndianOil" and "Indian Oil Corporation" are one
	// house; their evidence unions and the strongest verdict represents them.
	merged := demandDedupeCompanies(pool)
	results := make([]*models.MarketLandscapeCompany, 0, len(merged))
	for _, rep := range merged {
		if !demandCompanyTitle(rep.Name) {
			// Document titles are sources, not buyers — they live in the
			// appendix like every other piece of evidence.
			report.Appendix = append(report.Appendix, rep.Evidence...)
			continue
		}
		if rep.EvidenceType == "company-page" {
			// Producer gate: a company whose verification hits describe it as
			// a producer of the product itself is the supply side — it stays
			// a buyer only with transactional or input-language proof.
			if demandIsProducerWithoutDemand(rep) {
				report.Appendix = append(report.Appendix, rep.Evidence...)
				continue
			}
			// An integrated producer-consumer keeps its place but carries a
			// deterministic penalty: its demand is real, but partial.
			if demandHasProducerSignal(rep) {
				rep.Confidence = maxFloat(0.30, rep.Confidence-0.10)
			}
			// Buyer-fit distance: tie the confidence to how many
			// transformation steps the stage sits from the product, so a
			// direct consumer outranks an otherwise identical derived one.
			rep.Confidence = demandBuyerFitConfidence(rep.Confidence, findDemandStage(rep.Stage, report.Chain))
			results = append(results, rep)
		}
	}
	// The requested geography is reflected, not enforced blindly: a company
	// whose own evidence names a country (or city) outside it stays visible
	// but the report says so — a deterministic city-vs-country test has been
	// measured to misfire (e.g. "New Delhi" vs "India").
	if len(query.Countries) > 0 {
		var outside []string
		for _, company := range results {
			if company.Country != "" && !demandCountryMatches(company.Country, query.Countries) {
				outside = append(outside, fmt.Sprintf("%s (%s)", company.Name, strings.TrimSpace(company.Country)))
			}
		}
		if len(outside) > 0 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%d result(s) carry a country outside the requested geography — check the Country column: %s.", len(outside), strings.Join(outside, ", ")))
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Confidence == results[j].Confidence {
			return results[i].Name < results[j].Name
		}
		return results[i].Confidence > results[j].Confidence
	})
	for _, company := range results {
		company.Status = marketStatus(company)
		report.Results = append(report.Results, *company)
	}
	if len(report.Results) > query.MaxCompanies {
		for _, company := range report.Results[query.MaxCompanies:] {
			report.Appendix = append(report.Appendix, company.Evidence...)
		}
		report.Results = report.Results[:query.MaxCompanies]
	}
	if len(report.Results) == 0 {
		report.Warnings = append(report.Warnings, "No company met the consumption-verification threshold; discovery leads remain in the appendix.")
	}
	// The stage-oriented roster: every company the operator lanes tied to a
	// consuming stage — verified or not — grouped the way the value chain
	// inferred them. The verified list above is the cross-cutting view; this
	// keeps stage-active companies visible even when their demand evidence
	// did not reach the verification threshold.
	report.StageCandidates, demotedEvidence = buildStageCandidates(merged, report.Chain)
	report.Appendix = append(report.Appendix, demotedEvidence...)
	report.CompletedAt = time.Now()
	if exports := a.engine.ExportDemandChain(ctx, report); len(exports) > 0 {
		report.Exports = exports
	}
	emitAgentEvent(eventCh, models.EventComplete, fmt.Sprintf("Demand chain complete: %d verified result(s), %d stage(s)", len(report.Results), len(report.Chain)), "", "")
	return report, nil
}

// isDemandDocumentTitle catches titles that name documents rather than
// companies — market reports, patents, studies and analyses rank well on stage
// vocabulary, so the discovery lanes pull in plenty of them. They are evidence,
// and stay in the appendix, never in the company rosters.
func isDemandDocumentTitle(title string) bool {
	lowered := strings.ToLower(strings.TrimSpace(title))
	for _, marker := range []string{
		"market", "report", "forecast", "patent", "cost analysis", "feasibility",
		"simulation", "kinetics", "study", "size and share", "industry analysis",
		"outlook", "process for producing", "production cost", "drama", "grants",
		"procurement system",
	} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// demandCompanyTitle reports whether a title reads like a company at all: not
// a document, not bait, not a generic list headline.
func demandCompanyTitle(name string) bool {
	return !isDemandDocumentTitle(name) && !isGenericMarketTitle(name) && !isDemandBaitTitle(name)
}

// demandProducerPhrases are the phrase shapes that tie a company to
// *producing* the product itself. Each embeds the product name, so a page
// about manufacturing a downstream product never misfires on a buyer.
func demandProducerPhrases(activity string) []string {
	return []string{
		"manufacturer of " + activity, "manufacturers of " + activity,
		"producer of " + activity, "producers of " + activity,
		"supplier of " + activity, "suppliers of " + activity,
		"produces " + activity, "producing " + activity,
		"manufactures " + activity, "manufacturing " + activity,
		activity + " manufacturer", activity + " manufacturers",
		activity + " producer", activity + " producers",
	}
}

// demandProducerSignal reports whether a verification hit describes the
// company as a producer, manufacturer or supplier of the product itself —
// the supply side. Every marker embeds the product name ("manufacturer of
// methanol", "methanol producer"), so "manufacturer of resins" next to a
// methanol mention never misfires on a legitimate downstream buyer.
func demandProducerSignal(hit models.SearchResult, activity string) bool {
	activity = strings.ToLower(strings.TrimSpace(activity))
	if activity == "" {
		return false
	}
	text := strings.ToLower(hit.Title + " " + hit.Snippet)
	for _, phrase := range demandProducerPhrases(activity) {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// demandHasProducerSignal reports whether the company was determined to be a
// producer of the product itself — by the extractor's producer flag or by a
// producer self-description among its verification evidence.
func demandHasProducerSignal(company *models.MarketLandscapeCompany) bool {
	if company.ProducerSignal {
		return true
	}
	for _, evidence := range company.Evidence {
		if evidence.Status == "producer-signal" {
			return true
		}
	}
	return false
}

// demandHasDirectDemandClaim reports whether any evidence names an actual
// transaction or the buyer's own input language — the claim kinds that
// outweigh a producer self-description.
func demandHasDirectDemandClaim(company *models.MarketLandscapeCompany) bool {
	for _, evidence := range company.Evidence {
		switch evidence.ClaimType {
		case models.DemandClaimImport, models.DemandClaimTender, models.DemandClaimUsage:
			return true
		}
	}
	return false
}

// demandIsProducerWithoutDemand reports whether the company's only
// self-description is as a producer of the product — the supply side, demoted
// to the appendix unless transactional or input-language evidence exists.
// Integrated producer-consumers (GNFC produces methanol and tenders for it)
// keep their place, penalized.
func demandIsProducerWithoutDemand(company *models.MarketLandscapeCompany) bool {
	return demandHasProducerSignal(company) && !demandHasDirectDemandClaim(company)
}

// demandAggregatorMarkers are the domain fragments of tender portals, B2B
// marketplaces and trade aggregators: they publish demand, they do not
// exercise it, so their pages neither verify a company nor pass as one.
var demandAggregatorMarkers = []string{
	"tender", "rfp", "rfq", "eprocure", "e-procurement", "bidding",
	"indiamart", "tradeindia", "alibaba", "made-in-china", "exportersindia",
	"tradekey", "ec21", "go4worldbusiness", "exporthub",
}

// demandPublisherMarkers are the domain fragments of academic publishers and
// repositories: bibliography, not buyers.
var demandPublisherMarkers = []string{
	"mdpi", "pubs.acs", "wiley", "sciencedirect", "springer", "researchgate",
	"semanticscholar", "ssrn", "ieee", "journals", "scholar", "ncbi",
	"nature.com", "sciencedaily", "scribd", "slideshare", "amazon.", "books.google",
}

// demandConsultancyMarkers are the domains of market-research vendors and
// consultancies: they write about demand, they do not exercise it.
var demandConsultancyMarkers = []string{
	"imarc", "mordorintelligence", "statista", "credence", "grandviewresearch",
	"marketsandmarkets", "gminsights", "fortunebusinessinsights", "researchandmarkets",
	"intratec", "databridge", "alliedmarket", "persistencemarket", "techsci",
	"stratview", "nextmove", "marketresearch", "transparencymarketresearch",
}

// demandInstitutionMarkers are the domains of conferences, associations and
// bodies: commentary and community, not buyers.
var demandInstitutionMarkers = []string{
	"conference", "confintl", "symposium", "associa", "society", "council",
	"federation", "institute", "iea.", "unep", "oecd",
}

// demandSourceOverride reclassifies aggregator, publisher, consultancy and
// institution domains for the buyer side. The returned tier never qualifies,
// so such pages stay in the run as demand signals and leads while being
// unable to present as companies themselves. Empty when the shared tier
// classifier stands.
func demandSourceOverride(domain string) string {
	domain = strings.ToLower(strings.TrimPrefix(domain, "www."))
	for _, marker := range demandAggregatorMarkers {
		if strings.Contains(domain, marker) {
			return "aggregator"
		}
	}
	for _, marker := range demandPublisherMarkers {
		if strings.Contains(domain, marker) {
			return "publisher"
		}
	}
	for _, marker := range demandConsultancyMarkers {
		if strings.Contains(domain, marker) {
			return "consultancy"
		}
	}
	for _, marker := range demandInstitutionMarkers {
		if strings.Contains(domain, marker) {
			return "institution"
		}
	}
	return ""
}

// demandSourceName catches a candidate whose *name* is an aggregator page,
// consultancy, institution or publication rather than a company — the
// extraction occasionally echoes the page title ("Latest Alcohol Tenders in
// India 2026 - TendersOnTime", "IMARC Group") as a buyer. A "topic - site"
// echo whose topic names the product is likewise a page title, not a buyer.
func demandSourceName(name, activity string) bool {
	lowered := strings.ToLower(name)
	// En/em dashes are headline punctuation too; normalize before matching.
	lowered = strings.ReplaceAll(lowered, "–", "-")
	lowered = strings.ReplaceAll(lowered, "—", "-")
	for _, marker := range []string{
		"tender", "rfp", "rfq", "marketplace", "indiamart", "tradeindia",
		"alibaba", "b2b portal", "eprocure", "wiley", "mdpi", "springer",
		"sciencedirect", "online library", "journal", "books", "researchgate",
		"proceedings", "isbn", "imarc", "mordor", "statista", "credence",
		"research and markets", "marketsandmarkets", "gminsights",
		"grand view", "fortune business", "market report", "market research",
		"consultancy", "consulting", "intratec", "conference", "symposium",
		"association", "society", "council", "exposure to", "handbook",
		"encyclopedia", "guide to", "review of", "study of", "confintl",
		"university", "college", "nptel", "use in",
	} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	// A bare .org domain echoed as the entity is a non-commercial site.
	if strings.HasSuffix(lowered, ".org") && strings.Contains(lowered, ".") && !strings.Contains(lowered, " ") {
		return true
	}
	// "Topic - Site" where the topic names the product is a document title.
	activity = strings.ToLower(strings.TrimSpace(activity))
	if idx := strings.Index(lowered, " - "); idx > 0 && activity != "" {
		if strings.Contains(lowered[:idx], activity) {
			return true
		}
	}
	return false
}

// demandBuyerFitConfidence applies the buyer-definition step penalty: a
// company tied to a derived (step-2) consuming stage sits one transformation
// further from the product than a direct consumer, so an otherwise identical
// derived candidate cannot outrank a direct one. Deterministic, never below
// the discovery floor.
func demandBuyerFitConfidence(confidence float64, stage *models.ValueChainStage) float64 {
	if stage == nil || stage.Distance <= models.StageDistanceDirect {
		return confidence
	}
	penalized := confidence - 0.05*float64(stage.Distance-models.StageDistanceDirect)
	return maxFloat(0.30, penalized)
}

// stageVocabularyTokens extracts the distinctive tokens of a consuming stage —
// its name and operator phrases — minus the generic trade words and the geo, so
// a roster entry can be tested for stage relevance.
func stageVocabularyTokens(stage models.ValueChainStage) []string {
	generic := map[string]bool{
		"plant": true, "manufacturer": true, "manufacturers": true, "producer": true,
		"producers": true, "supplier": true, "suppliers": true, "production": true,
		"india": true, "indian": true,
	}
	tokens := make([]string, 0, 8)
	seen := make(map[string]bool)
	add := func(text string) {
		for _, token := range strings.Fields(strings.ToLower(text)) {
			token = strings.Trim(token, ".,;:()")
			if len(token) >= 4 && !generic[token] && !seen[token] {
				seen[token] = true
				tokens = append(tokens, token)
			}
		}
	}
	add(stage.Name)
	for _, phrase := range stage.Operators {
		add(phrase)
	}
	return tokens
}

// findDemandStage returns the chain stage with the given name, or nil.
func findDemandStage(name string, chain []models.ValueChainStage) *models.ValueChainStage {
	for index := range chain {
		if chain[index].Name == name {
			return &chain[index]
		}
	}
	return nil
}

// stageRelevant reports whether a title speaks about the consuming stage —
// one of its distinctive tokens present — rather than only about the product
// itself. A hit that names the product but no stage vocabulary is the supply
// side ("N-Butanol Manufacturer in Gandhinagar"), not a potential buyer.
func stageRelevant(title string, stage models.ValueChainStage) bool {
	text := strings.ToLower(title)
	for _, token := range stageVocabularyTokens(stage) {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

// buildStageCandidates groups the discovery candidates by their consuming
// stage, in the chain's order, deterministically. Companies the product-shaped
// demand lanes surfaced (no single stage) get their own section rather than
// being dropped, the operator's verified companies join the roster by their
// attributed stage, and entries that only speak about the product (the supply
// side) or name documents are demoted to the appendix. Sorted by confidence,
// then name, like every other view.
func buildStageCandidates(pool []*models.MarketLandscapeCompany, chain []models.ValueChainStage) ([]models.StageCandidates, []models.MarketLandscapeEvidence) {
	byStage := make(map[string][]*models.MarketLandscapeCompany)
	var unclassified []*models.MarketLandscapeCompany
	var demoted []models.MarketLandscapeEvidence
	for _, company := range pool {
		if !demandCompanyTitle(company.Name) {
			// Document titles are evidence, not companies: they move to the
			// appendix where the sources live.
			demoted = append(demoted, company.Evidence...)
			continue
		}
		company.Status = marketStatus(company)
		if strings.TrimSpace(company.Stage) == "" {
			unclassified = append(unclassified, company)
			continue
		}
		// Stage relevance guards the raw-title leads only: a discovery lead
		// that only names the product is the supply side of the market. The
		// verified companies and identity-passed company pages are exempt —
		// they earned their place through extraction, verification and the
		// identity gate, and a company name need not spell its stage.
		stage := findDemandStage(company.Stage, chain)
		if company.EvidenceType == "discovery-lead" && stage != nil && !stageRelevant(company.Name+" "+company.Domain, *stage) {
			demoted = append(demoted, company.Evidence...)
			continue
		}
		byStage[company.Stage] = append(byStage[company.Stage], company)
	}
	sortStageCompanies := func(companies []*models.MarketLandscapeCompany) []models.MarketLandscapeCompany {
		sort.Slice(companies, func(i, j int) bool {
			if companies[i].Confidence == companies[j].Confidence {
				return companies[i].Name < companies[j].Name
			}
			return companies[i].Confidence > companies[j].Confidence
		})
		section := make([]models.MarketLandscapeCompany, 0, len(companies))
		for _, company := range companies {
			section = append(section, *company)
		}
		return section
	}
	sections := make([]models.StageCandidates, 0, len(chain)+1)
	for _, stage := range chain {
		sections = append(sections, models.StageCandidates{Stage: stage, Companies: sortStageCompanies(byStage[stage.Name])})
	}
	if len(unclassified) > 0 {
		sections = append(sections, models.StageCandidates{
			Stage: models.ValueChainStage{
				Name:      "General demand lanes",
				Source:    "lanes",
				Rationale: "Companies the product-shaped demand lanes surfaced, not tied to a single consuming stage.",
			},
			Companies: sortStageCompanies(unclassified),
		})
	}
	return sections, demoted
}

// demandCorporateSuffixes are the legal-form words that carry no identity:
// their presence or absence never makes two names different companies.
var demandCorporateSuffixes = map[string]bool{
	"limited": true, "ltd": true, "pvt": true, "private": true, "inc": true,
	"incorporated": true, "gmbh": true, "bv": true, "nv": true, "sa": true,
	"ag": true, "plc": true, "corp": true, "corporation": true, "company": true,
	"co": true, "llp": true, "llc": true, "lp": true,
}

// demandStopWords are conjunctions and articles that carry no identity —
// "Ishwar Chemicals & Gases" and "Ishwar Chemicals and Gases" are one house.
var demandStopWords = map[string]bool{"and": true, "the": true, "of": true}

// demandNameTokens lowercases a name into alphanumeric tokens, drops stop
// words, and strips trailing corporate suffixes, so "Acme Ltd" and "Acme"
// collapse together and ampersand/and variants unify.
func demandNameTokens(name string) []string {
	tokens := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	kept := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if !demandStopWords[token] {
			kept = append(kept, token)
		}
	}
	tokens = kept
	for len(tokens) > 1 && demandCorporateSuffixes[tokens[len(tokens)-1]] {
		tokens = tokens[:len(tokens)-1]
	}
	return tokens
}

// demandSameCompany reports whether two names are alias-level variants of one
// company, under deliberately conservative rules: space-insensitive equality
// ("IndianOil" / "Indian Oil"), corporate-suffix-insensitive equality
// ("Acme Ltd" / "Acme"), and a long brand key with a corporate-suffix remainder
// ("indianoil" + "indianoilcorporation"). Anything looser would merge
// conglomerate siblings — Adani vs Adani Ports, Tata Motors vs Tata Steel —
// and a buyer list must not do that silently.
func demandSameCompany(a, b string) bool {
	at, bt := demandNameTokens(a), demandNameTokens(b)
	if len(at) == 0 || len(bt) == 0 {
		return false
	}
	aj, bj := strings.Join(at, ""), strings.Join(bt, "")
	if aj == bj {
		return true
	}
	short, long := aj, bj
	if len(short) > len(long) {
		short, long = long, short
	}
	return len(short) >= 8 && strings.HasPrefix(long, short) && demandCorporateSuffixes[long[len(short):]]
}

// demandCanonicalURL folds a URL to host-without-www plus lowercase path, so
// https://greendiesel.com.my/plant and https://www.greendiesel.com.my/plant
// compare equal when two entries cite the same page.
func demandCanonicalURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	return host + strings.ToLower(parsed.Path)
}

// demandSharesEvidence reports whether two entries cite the same source URL —
// the strongest identity signal a raw discovery candidate and the verified
// company extracted from it can share, because they cite the very same page.
func demandSharesEvidence(a, b *models.MarketLandscapeCompany) bool {
	urls := make(map[string]bool)
	for _, evidence := range a.Evidence {
		if evidence.URL != "" {
			urls[demandCanonicalURL(evidence.URL)] = true
		}
	}
	for _, evidence := range b.Evidence {
		if evidence.URL != "" && urls[demandCanonicalURL(evidence.URL)] {
			return true
		}
	}
	return false
}

// demandGeoNames assembles the geography vocabulary a buyer run must never
// accept as a company: the query's own countries and regions, plus the geo
// names extraction models most often echo as entities when a lead mentions a
// market instead of a buyer.
func demandGeoNames(query models.DemandChainQuery) map[string]bool {
	names := map[string]bool{
		"usa": true, "united states": true, "united states of america": true,
		"europe": true, "european union": true, "asia": true, "asia pacific": true,
		"middle east": true, "africa": true, "global": true, "emea": true,
		"apac": true, "north america": true, "south america": true,
	}
	for _, region := range query.Regions {
		if region = strings.ToLower(strings.TrimSpace(region)); region != "" {
			names[region] = true
		}
	}
	for _, country := range query.Countries {
		if country = strings.ToLower(strings.TrimSpace(country)); country != "" {
			names[country] = true
		}
	}
	return names
}

// demandDedupeCompanies merges entries that are alias-level variants of one
// company. The strongest member (confidence, then the more complete name) is
// the representative; evidence is unioned without URL duplicates; country,
// domain and stage fall back to the first member that carries them.
func demandDedupeCompanies(pool []*models.MarketLandscapeCompany) []*models.MarketLandscapeCompany {
	buckets := make([][]*models.MarketLandscapeCompany, 0, len(pool))
	for _, company := range pool {
		found := false
		for index := range buckets {
			if demandSameCompany(buckets[index][0].Name, company.Name) {
				buckets[index] = append(buckets[index], company)
				found = true
				break
			}
			// Shared-evidence identity is admitted only between a verified or
			// identity-passed entry and the raw discovery lead that was
			// extracted from the very same page. Two company-page entries
			// citing one tender portal that lists several bidders are
			// different companies, not one.
			sharedProvenance := demandSharesEvidence(buckets[index][0], company) &&
				((buckets[index][0].EvidenceType == "discovery-lead" && company.EvidenceType != "discovery-lead") ||
					(company.EvidenceType == "discovery-lead" && buckets[index][0].EvidenceType != "discovery-lead"))
			if sharedProvenance {
				buckets[index] = append(buckets[index], company)
				found = true
				break
			}
		}
		if !found {
			buckets = append(buckets, []*models.MarketLandscapeCompany{company})
		}
	}
	merged := make([]*models.MarketLandscapeCompany, 0, len(buckets))
	for _, bucket := range buckets {
		merged = append(merged, demandMergeBucket(bucket))
	}
	return merged
}

// demandMergeBucket builds one representative from an alias bucket.
func demandMergeBucket(members []*models.MarketLandscapeCompany) *models.MarketLandscapeCompany {
	best := members[0]
	for _, member := range members[1:] {
		if member.Confidence > best.Confidence ||
			(member.Confidence == best.Confidence && len(member.Name) > len(best.Name)) {
			best = member
		}
	}
	rep := *best
	rep.Evidence = nil
	seenURLs := make(map[string]bool)
	seenPeople := make(map[string]bool)
	for _, member := range members {
		if strings.TrimSpace(rep.Country) == "" && strings.TrimSpace(member.Country) != "" {
			rep.Country = member.Country
		}
		if strings.TrimSpace(rep.Domain) == "" && strings.TrimSpace(member.Domain) != "" {
			rep.Domain = member.Domain
		}
		if strings.TrimSpace(rep.Stage) == "" && strings.TrimSpace(member.Stage) != "" {
			rep.Stage = member.Stage
		}
		if member.EvidenceType == "company-page" {
			rep.EvidenceType = "company-page"
		}
		for _, evidence := range member.Evidence {
			if evidence.URL == "" || !seenURLs[evidence.URL] {
				if evidence.URL != "" {
					seenURLs[evidence.URL] = true
				}
				rep.Evidence = append(rep.Evidence, evidence)
			}
		}
		for _, person := range member.KeyPeople {
			key := strings.ToLower(strings.TrimSpace(person))
			if person != "" && !seenPeople[key] {
				seenPeople[key] = true
				rep.KeyPeople = append(rep.KeyPeople, person)
			}
		}
	}
	rep.Status = marketStatus(&rep)
	return &rep
}

// normalizeDemandBudget applies the budget defaults and caps: zero keeps the
// mode defaults, and the caps keep a mistyped budget from running away.
func normalizeDemandBudget(query *models.DemandChainQuery) {
	if query.DiscoveryQueries <= 0 {
		query.DiscoveryQueries = demandMaxDiscoveryQueries
	}
	if query.DiscoveryQueries < demandMinDiscoveryQueries {
		query.DiscoveryQueries = demandMinDiscoveryQueries
	}
	if query.DiscoveryQueries > 60 {
		query.DiscoveryQueries = 60
	}
	if query.VerificationQueries <= 0 {
		query.VerificationQueries = demandMaxVerificationQueries
	}
	if query.VerificationQueries > 250 {
		query.VerificationQueries = 250
	}
	if query.MaxLeadsEnriched <= 0 {
		query.MaxLeadsEnriched = demandMaxLeadsEnriched
	}
	if query.MaxLeadsEnriched > 60 {
		query.MaxLeadsEnriched = 60
	}
	if query.MaxCompanies <= 0 || query.MaxCompanies > 50 {
		query.MaxCompanies = 20
	}
}

// demandLane is one discovery lane: the phrase appended to the activity in the
// search query, and the consuming stage it serves (empty for product-shaped
// demand lanes that speak about the product itself).
type demandLane struct {
	term  string
	stage string
}

// inferDemandChain builds the consuming stages the run is organised around.
// The operator's StagesOverride wins outright; otherwise one structured call
// proposes the chain, which is sanitized, capped and guaranteed to carry
// operator vocabulary. Failure degrades to a generic stage, never a failed run.
func (a *DemandChainAgent) inferDemandChain(ctx context.Context, query models.DemandChainQuery, eventCh chan<- models.AgentEvent) ([]models.ValueChainStage, bool, []string) {
	if len(query.StagesOverride) > 0 {
		stages := make([]models.ValueChainStage, 0, len(query.StagesOverride))
		for _, name := range query.StagesOverride {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			stages = append(stages, models.ValueChainStage{
				Name: name,
				// An operator-supplied consuming stage is direct consumption
				// by definition: the operator names the process that takes
				// the product in.
				Distance:  models.StageDistanceDirect,
				Source:    "operator",
				Operators: []string{strings.TrimSpace(name) + " plant operator manufacturer"},
			})
		}
		emitAgentEvent(eventCh, models.EventToolResult, fmt.Sprintf("Using %d operator-supplied consuming stage(s)", len(stages)), "", "")
		return stages, false, nil
	}

	var proposed []models.ValueChainStage
	// The prompt encodes the buyer definition: a buyer is a manufacturer whose
	// process consumes the product as an input, so the chain stops at two
	// transformation steps downstream — deeper steps are context, not buyers.
	prompt := fmt.Sprintf("Identify the industrial manufacturing processes that are potential buyers of %q: processes that consume the product%s. Scope: at most two transformation steps downstream. Return a JSON array where each element has name (the consuming manufacturing process, e.g. \"biodiesel production\"), rationale (one line naming which input the stage consumes), operators (2-3 search phrases for the plants or companies operating that stage, e.g. \"biodiesel plant\"), and distance: 1 when the stage consumes %q directly as an input or raw material, 2 when it consumes the output of a distance-1 process. Exclude stages beyond two steps, end-use sectors that manufacture nothing, retail, logistics, trading and publication stages.", query.Activity, demandGeographyClause(query), query.Activity)
	if err := a.engine.GenerateStructured(ctx,
		"You infer industrial value chains for buyer research. A buyer is a manufacturer whose process consumes the product as an input; never propose stages more than two transformation steps downstream, and keep operator phrases as concrete plant vocabulary.",
		prompt, &proposed); err != nil {
		return a.demandChainFallback(ctx, "Value-chain inference failed", err, query, eventCh)
	}

	var warnings []string
	sanitized := sanitizeDemandStages(proposed)
	if len(sanitized.dropped) > 0 {
		// Audit trail for the buyer definition: what the model proposed
		// beyond the two-step scope is reported, not silently discarded.
		warnings = append(warnings, fmt.Sprintf("%d proposed stage(s) beyond two transformation steps were dropped as out of buyer scope: %s.", len(sanitized.dropped), strings.Join(sanitized.dropped, ", ")))
	}
	stages := sanitized.kept
	if len(stages) == 0 {
		return a.demandChainFallback(ctx, "Value-chain inference returned no usable stages", fmt.Errorf("the model proposed no stages"), query, eventCh)
	}
	for index := range stages {
		if len(stages[index].Operators) == 0 {
			stages[index].Operators = []string{strings.TrimSpace(stages[index].Name) + " plant operator manufacturer"}
		}
	}
	emitAgentEvent(eventCh, models.EventToolResult, fmt.Sprintf("Inferred %d consuming stage(s): %s", len(stages), stageNames(stages)), "", "")
	return stages, true, warnings
}

// demandChainFallback degrades to a product-named generic stage — the buyer
// pipeline always produces a report that still reflects the product the user
// asked about, never a failure, when the chain inference misfires.
func (a *DemandChainAgent) demandChainFallback(ctx context.Context, reason string, err error, query models.DemandChainQuery, eventCh chan<- models.AgentEvent) ([]models.ValueChainStage, bool, []string) {
	warning := fmt.Sprintf("%s; falling back to a generic stage: %v", reason, err)
	emitAgentEvent(eventCh, models.EventError, warning, "", "")
	fallback := []models.ValueChainStage{{
		Name:      query.Activity,
		Source:    "fallback",
		Distance:  models.StageDistanceDirect,
		Rationale: "Generic fallback: the model could not propose consuming stages, so only product-shaped demand lanes ran.",
	}}
	return fallback, false, []string{warning}
}

// sanitizedDemandStages carries what survived the buyer-scope gate and what
// the model proposed beyond it — the dropped names feed the run's audit trail.
type sanitizedDemandStages struct {
	kept    []models.ValueChainStage
	dropped []string
}

// sanitizeDemandStages trims, dedupes and caps the model's proposal so the
// lanes downstream are always buildable from it, and reports every stage the
// two-step buyer scope removed.
func sanitizeDemandStages(proposed []models.ValueChainStage) sanitizedDemandStages {
	stages := make([]models.ValueChainStage, 0, len(proposed))
	var dropped []string
	seen := make(map[string]bool)
	for _, stage := range proposed {
		stage.Name = strings.TrimSpace(stage.Name)
		stage.Rationale = strings.TrimSpace(stage.Rationale)
		if stage.Name == "" || seen[strings.ToLower(stage.Name)] {
			continue
		}
		if stage.Distance > models.StageDistanceDerived {
			// Beyond two transformation steps the stage is out of buyer scope
			// by definition — the full value chain is context, not buyers.
			dropped = append(dropped, stage.Name)
			continue
		}
		if stage.Distance < models.StageDistanceDirect {
			// An unlabelled distance is treated as direct consumption.
			stage.Distance = models.StageDistanceDirect
		}
		seen[strings.ToLower(stage.Name)] = true
		vocabulary := make([]string, 0, len(stage.Operators))
		for _, phrase := range stage.Operators {
			if phrase = strings.TrimSpace(phrase); phrase != "" {
				vocabulary = append(vocabulary, phrase)
			}
		}
		stage.Operators = vocabulary
		stages = append(stages, stage)
		if len(stages) >= demandMaxStages {
			break
		}
	}
	return sanitizedDemandStages{kept: stages, dropped: dropped}
}

func stageNames(stages []models.ValueChainStage) string {
	names := make([]string, 0, len(stages))
	for _, stage := range stages {
		names = append(names, stage.Name)
	}
	return strings.Join(names, ", ")
}

// demandChainLanes orders the discovery lanes: each stage's explicit
// product-plus-sector company query first, then its operator vocabulary, then
// product-shaped demand lanes and geography-scoped importer/tender lanes.
func demandChainLanes(query models.DemandChainQuery, chain []models.ValueChainStage) []demandLane {
	lanes := make([]demandLane, 0, len(chain)*3+8)
	// Deeper discovery budgets earn a wider operator vocabulary per stage, so
	// the extra queries land on new stage lanes instead of thinning the demand
	// lanes.
	vocabularyCap := 2
	if query.DiscoveryQueries >= 40 {
		vocabularyCap = 3
	}
	for _, stage := range chain {
		stageName := strings.TrimSpace(stage.Name)
		if stageName != "" {
			// This lane deliberately combines the requested product with the
			// consuming sector, giving search a direct path to likely operators
			// even when the model's operator vocabulary is weak or equipment-
			// oriented.
			lanes = append(lanes, demandLane{
				term:  stageName + " companies",
				stage: stage.Name,
			})
		}
		vocabularyCount := 0
		stageCap := vocabularyCap - 1
		if stage.Distance > models.StageDistanceDirect && stageCap > 0 {
			// A derived (step-2) stage sits one transformation further from
			// the product than a direct consumer: its operator vocabulary
			// earns one fewer lane, so the budget favours the strongest
			// buyer hypotheses.
			stageCap--
		}
		for _, vocabulary := range stage.Operators {
			if vocabularyCount >= stageCap {
				break
			}
			lanes = append(lanes, demandLane{term: strings.TrimSpace(vocabulary), stage: stage.Name})
			vocabularyCount++
		}
	}
	lanes = append(lanes,
		demandLane{term: "buyer procurement purchase"},
		demandLane{term: "tender RFP e-procurement"},
		demandLane{term: "importer import data"},
		demandLane{term: "raw material feedstock consumption"},
		demandLane{term: "industrial applications manufacturers"},
		// Distributors and wholesalers are retained as buyers: they purchase
		// the product to resell it, which is real demand. Traders and brokers
		// remain gated behind IncludeIntermediaries.
		demandLane{term: "distributor wholesaler of"},
	)
	for _, country := range query.Countries {
		lanes = append(lanes, demandLane{term: strings.TrimSpace(country) + " importer tender procurement"})
	}
	for _, region := range query.Regions {
		lanes = append(lanes, demandLane{term: strings.TrimSpace(region) + " importer tender procurement"})
	}
	unique := make([]demandLane, 0, len(lanes))
	seen := make(map[string]bool)
	for _, lane := range lanes {
		key := strings.ToLower(lane.term)
		if lane.term == "" || seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, lane)
	}
	return unique
}

// selectDemandLanes keeps the two highest-value direct-demand lanes in every
// run: procurement/direct-enquiry and tender/RFP. The remaining budget follows
// the stage-first order produced by demandChainLanes.
func selectDemandLanes(lanes []demandLane, budget int) []demandLane {
	if budget <= 0 {
		return nil
	}
	mandatory := map[string]bool{
		"buyer procurement purchase": true,
		"tender RFP e-procurement":   true,
	}
	reserved := 0
	for _, lane := range lanes {
		if mandatory[lane.term] {
			reserved++
		}
	}
	if reserved == 0 {
		if len(lanes) > budget {
			return lanes[:budget]
		}
		return lanes
	}
	if budget < reserved {
		budget = reserved
	}
	capacity := len(lanes)
	if capacity > budget {
		capacity = budget
	}
	selected := make([]demandLane, 0, capacity)
	optionalBudget := budget - reserved
	for _, lane := range lanes {
		if mandatory[lane.term] {
			continue
		}
		if optionalBudget == 0 {
			break
		}
		selected = append(selected, lane)
		optionalBudget--
	}
	for _, lane := range lanes {
		if mandatory[lane.term] {
			selected = append(selected, lane)
		}
	}
	return selected
}

// demandGeography renders the geo suffix for search queries: countries win over
// regions, and nothing is appended when the run is global.
func demandGeography(query models.DemandChainQuery) string {
	if len(query.Countries) > 0 {
		return strings.Join(query.Countries, " ")
	}
	return strings.Join(query.Regions, " ")
}

// demandGeographyClause renders the geography for LLM prompts: countries and
// regions both travel, so the inference sees the full scope the operator asked
// for; empty when the run is global.
func demandGeographyClause(query models.DemandChainQuery) string {
	parts := append(append([]string(nil), query.Countries...), query.Regions...)
	parts = uniqueStrings(parts)
	if len(parts) == 0 {
		return ""
	}
	return " consumed in " + strings.Join(parts, ", ")
}

// demandCandidate turns one search hit into a candidate company. It reuses the
// producer/seller landscape's identity and tier hygiene, adds buyer-specific bait and
// intermediary filtering, and tags the consuming stage the lane serves.
func demandCandidate(hit models.SearchResult, lane demandLane, query models.DemandChainQuery) *models.MarketLandscapeCompany {
	parsed, err := url.Parse(hit.Link)
	if err != nil || parsed.Hostname() == "" {
		return nil
	}
	domain := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	name := strings.TrimSpace(hit.Title)
	// A name that arrived as a page title ("X | Products | Acme") collapses to
	// its longest segment — the company name is the last meaningful part.
	if strings.Contains(name, "|") {
		best := ""
		for _, segment := range strings.Split(name, "|") {
			if trimmed := strings.TrimSpace(segment); len(trimmed) > len(best) {
				best = trimmed
			}
		}
		name = best
	}
	if name == "" {
		name = domain
	}
	sourceTier := marketSourceTier(domain)
	sourceOverride := demandSourceOverride(domain)
	if sourceOverride != "" {
		// Aggregator and publisher pages remain in the run as demand signals
		// and leads — the extraction may still surface the buyers their
		// listings name — but they can never present as companies.
		sourceTier = sourceOverride
	}
	evidenceType := "company-page"
	if sourceTier == "news" || sourceTier == "social" || sourceOverride != "" || isGenericMarketTitle(name) || isDemandBaitTitle(name) || !hasCompanyIdentitySignal(name, domain) {
		evidenceType = "discovery-lead"
	}
	// Product-shaped demand lanes must carry buyer language on the hit itself;
	// stage-operator lanes need none — the stage vocabulary is the signal.
	if lane.stage == "" && !hasDemandSignalEvidence(hit) {
		evidenceType = "discovery-lead"
	}
	candidate := &models.MarketLandscapeCompany{
		EntityKey: "company:" + domain, Name: name, Domain: domain,
		Activity: query.Activity, Stage: lane.stage, Status: "candidate",
		EvidenceType: evidenceType, Confidence: 0.45,
		Evidence: []models.MarketLandscapeEvidence{{
			URL: hit.Link, Title: hit.Title, Snippet: hit.Snippet, Lane: lane.term,
			ClaimType: demandClaimForLane(lane), SourceTier: sourceTier,
			RetrievedAt: time.Now(), Status: "candidate",
		}},
	}
	// Intermediaries are excluded by default: a buyer list dominated by
	// middlemen is rarely what the operator asked for.
	if !query.IncludeIntermediaries && isIntermediarySignal(name, domain) {
		candidate.Evidence[0].Status = "excluded"
	}
	return candidate
}

// isDemandLane reports whether the lane speaks about the product's demand
// directly (tenders, importers, input language) rather than about the
// consuming stage's operators.
func isDemandLane(lane demandLane) bool {
	term := strings.ToLower(lane.term)
	for _, marker := range []string{"tender", "rfp", "rfq", "importer", "import data", "procurement", "raw material", "feedstock", "buyer"} {
		if strings.Contains(term, marker) {
			return true
		}
	}
	return false
}

// demandClaimForLane names the claim type a lane's discovery evidence carries.
func demandClaimForLane(lane demandLane) string {
	term := strings.ToLower(lane.term)
	switch {
	case strings.Contains(term, "tender") || strings.Contains(term, "rfp") || strings.Contains(term, "rfq"):
		return models.DemandClaimTender
	case strings.Contains(term, "import"):
		return models.DemandClaimImport
	case strings.Contains(term, "raw material") || strings.Contains(term, "feedstock"):
		return models.DemandClaimUsage
	case lane.stage != "":
		return models.DemandClaimOperator
	default:
		return models.DemandClaimSignal
	}
}

// hasDemandSignalEvidence checks whether the hit itself carries buyer language.
func hasDemandSignalEvidence(hit models.SearchResult) bool {
	text := strings.ToLower(hit.Title + " " + hit.Snippet)
	for _, marker := range []string{
		"buyer", "procure", "procurement", "purchasing", "tender", "rfp", "rfq",
		"import", "importer", "consum", "feedstock", "raw material", "ingredient",
		"inputs", "we use",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// isDemandBaitTitle catches the buyer-side noise the supplier side never sees:
// lead-gen listicles and market-research vendors optimising for exactly these
// searches. They stay in the appendix as leads, never as verification.
func isDemandBaitTitle(title string) bool {
	lowered := strings.ToLower(strings.TrimSpace(title))
	for _, marker := range []string{"buyers list", "list of buyers", "buyers of", "buying guide", "market research", "sample report", "download report", "price trend"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// isIntermediarySignal reports whether a candidate looks like a trader or
// broker — simultaneously a buyer and a seller.
func isIntermediarySignal(name, domain string) bool {
	text := strings.ToLower(name + " " + domain)
	for _, marker := range []string{"trading", "traders", "trader", "broker", "commodities", "trading house"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// verifyDemandLeads is the buyer-side sibling of verifyMarketLeads: the same
// skeleton (enrich qualifying tiers, batched LLM extraction, plausibility
// dedupe, per-purpose verification searches), with consumption-shaped prompts,
// claim-typed evidence and a directness-weighted confidence ladder. It reuses
// the market mode's tier, identity and name-hygiene helpers unchanged.
func (a *DemandChainAgent) verifyDemandLeads(ctx context.Context, leads []models.MarketLandscapeEvidence, query models.DemandChainQuery, laneStages map[string]string, eventCh chan<- models.AgentEvent) ([]*models.MarketLandscapeCompany, int, int, int, int, error) {
	if len(leads) == 0 {
		return nil, 0, 0, 0, 0, nil
	}
	enrichable := leads
	if len(enrichable) > query.MaxLeadsEnriched {
		enrichable = enrichable[:query.MaxLeadsEnriched]
	}
	// Enrichment is bounded-concurrent through the engine's batch scraper —
	// the same scrape semaphore every other scraping stage shares — so a deep
	// run's dozens of lead pages do not crawl one by one. Outcomes arrive in
	// submission order, so the lead order is stable.
	targets := make([]string, 0, len(enrichable))
	for _, lead := range enrichable {
		if isQualifyingMarketTier(lead.SourceTier) {
			targets = append(targets, lead.URL)
		}
	}
	outcomes := a.engine.ScrapeCachedBatch(ctx, targets)
	contentByURL := make(map[string]string, len(outcomes))
	pagesEnriched := 0
	for _, outcome := range outcomes {
		if outcome.Err != nil || outcome.Result == nil {
			continue
		}
		if content := strings.TrimSpace(outcome.Result.Content); content != "" {
			contentByURL[demandCanonicalURL(outcome.URL)] = truncateMarketText(content, 3000)
			pagesEnriched++
		}
	}
	enrichedLeads := make([]models.MarketLandscapeEvidence, 0, len(enrichable))
	for _, lead := range enrichable {
		leadCopy := lead
		if content, ok := contentByURL[demandCanonicalURL(lead.URL)]; ok {
			leadCopy.Content = content
		}
		enrichedLeads = append(enrichedLeads, leadCopy)
	}

	var extracted []models.MarketLandscapeLeadCandidate
	rejected := 0
	var extractionErrors []string
	geo := demandGeography(query)
	geoPreference := ""
	if geo = strings.TrimSpace(geo); geo != "" {
		geoPreference = fmt.Sprintf("Prefer companies operating in %s when the lead supports it, and exclude companies whose stated country is outside %s; record the country a lead names (never a city). ", geo, geo)
	}
	// Extraction runs bounded-concurrent: batches of four leads, three batches
	// in flight, results merged in submission order so the verification queue
	// never depends on which LLM call finished first.
	batchCount := (len(enrichedLeads) + demandExtractionBatchSize - 1) / demandExtractionBatchSize
	batchResults := make([][]models.MarketLandscapeLeadCandidate, batchCount)
	batchErrs := make([]string, batchCount)
	extractSemaphore := make(chan struct{}, demandExtractionConcurrency)
	var extractGroup sync.WaitGroup
	for batchIndex := 0; batchIndex < batchCount; batchIndex++ {
		extractGroup.Add(1)
		go func(batchIndex int) {
			defer extractGroup.Done()
			extractSemaphore <- struct{}{}
			defer func() { <-extractSemaphore }()
			start := batchIndex * demandExtractionBatchSize
			end := start + demandExtractionBatchSize
			if end > len(enrichedLeads) {
				end = len(enrichedLeads)
			}
			leadText := make([]string, 0, end-start)
			for index, lead := range enrichedLeads[start:end] {
				content := lead.Snippet
				if lead.Content != "" {
					content += "\nPage content: " + lead.Content
				}
				leadText = append(leadText, fmt.Sprintf("%d. %s — %s (%s)", index+1, lead.Title, content, lead.URL))
			}
			var batch []models.MarketLandscapeLeadCandidate
			prompt := fmt.Sprintf("Extract every actual company or facility operator explicitly supported by these leads as a consumer, buyer, importer, or processor of %q. %sExclude publishers, list titles, market-research vendors, regions, projects, and generic categories. Return the company's legal name — never a news headline, page title, or project name. Set producer to true when the page describes the company as a producer, manufacturer, or supplier of %q itself rather than a consumer of it. If a lead explicitly names a relevant procurement, purchasing, or supply-chain person, include the person's name in key_people; never infer people. Return a JSON array with name, country, reason, evidence_url as a string, producer as a boolean, and key_people as an array of strings.\nActivity: %s\nLeads:\n%s", query.Activity, geoPreference, query.Activity, query.Activity, strings.Join(leadText, "\n"))
			if err := a.engine.GenerateStructured(ctx, "You extract company names for a buyer-research verification queue. Never invent names and do not collapse multiple companies into one.", prompt, &batch); err != nil {
				batchErrs[batchIndex] = err.Error()
			}
			batchResults[batchIndex] = batch
		}(batchIndex)
	}
	extractGroup.Wait()
	for batchIndex := 0; batchIndex < batchCount; batchIndex++ {
		start := batchIndex * demandExtractionBatchSize
		end := start + demandExtractionBatchSize
		if end > len(enrichedLeads) {
			end = len(enrichedLeads)
		}
		if batchErrs[batchIndex] != "" {
			rejected += end - start
			extractionErrors = append(extractionErrors, batchErrs[batchIndex])
			continue
		}
		extracted = append(extracted, batchResults[batchIndex]...)
	}

	uniqueExtracted := make([]models.MarketLandscapeLeadCandidate, 0, len(extracted))
	seenNames := make(map[string]bool)
	geoNames := demandGeoNames(query)
	for _, candidate := range extracted {
		// A name that arrived as a page title or headline ("Butyl Acrylate |
		// Products | X", "INEOS-GNFC's New Plant: Is India's ...") collapses
		// to its company segment.
		candidate.Name = demandNormalizeCandidateName(strings.TrimSpace(candidate.Name))
		// A name that equals the country the lead named is a geography, not a
		// company — the model occasionally echoes a country as an entity.
		if strings.EqualFold(candidate.Name, strings.TrimSpace(candidate.Country)) {
			rejected++
			continue
		}
		// So is a geography the run was never supposed to treat as a buyer:
		// "European Union" or "USA" appear when a lead talks about a market.
		if geoNames[normalizeMarketName(candidate.Name)] {
			rejected++
			continue
		}
		// A name that equals the product itself is the commodity, not a buyer —
		// the model occasionally echoes the product as an entity.
		if normalizeMarketName(candidate.Name) == normalizeMarketName(query.Activity) {
			rejected++
			continue
		}
		// A tender portal, B2B marketplace, consultancy, institution or
		// publication echoed as the entity itself is a source, not a buyer.
		if demandSourceName(candidate.Name, query.Activity) {
			rejected++
			continue
		}
		key := normalizeMarketName(candidate.Name)
		if !isPlausibleMarketLeadName(candidate.Name) || seenNames[key] {
			rejected++
			continue
		}
		seenNames[key] = true
		uniqueExtracted = append(uniqueExtracted, candidate)
	}

	verified := make([]*models.MarketLandscapeCompany, 0, len(uniqueExtracted))
	queriesUsed := 0
	for index, lead := range uniqueExtracted {
		if index >= query.MaxCompanies || queriesUsed >= query.VerificationQueries {
			break
		}
		// Claim-conditioned verification: the searches run cheapest-first for
		// what the discovery lane already claimed, so a tender-lane lead does
		// not burn its budget proving its own website first.
		purposes := demandVerificationPurposes(demandClaimForLead(leads, lead))
		for _, purpose := range purposes {
			if queriesUsed >= query.VerificationQueries {
				break
			}
			searchQuery := strings.TrimSpace(fmt.Sprintf("%q %s %s %s", strings.TrimSpace(lead.Name), query.Activity, purpose.phrase, demandGeography(query)))
			emitAgentEvent(eventCh, models.EventToolCall, "Verifying "+lead.Name+" via "+purpose.kind, "search", searchQuery)
			hits, err := a.engine.SearchCached(ctx, searchQuery, marketMaxResults)
			queriesUsed++
			if err != nil {
				continue
			}
			for _, hit := range hits {
				evidence, ok := demandVerificationEvidence(hit, lead.Name, query.Activity, purpose.kind)
				if !ok {
					continue
				}
				company := findMarketCompany(verified, lead.Name)
				if company == nil {
					company = &models.MarketLandscapeCompany{
						EntityKey: "company:name:" + normalizeMarketName(lead.Name), Name: lead.Name,
						Country: lead.Country, Activity: query.Activity, Stage: demandStageForLead(leads, lead, laneStages),
						EvidenceType: "company-page", Confidence: 0.62,
						KeyPeople: append([]string(nil), lead.KeyPeople...),
					}
					verified = append(verified, company)
				}
				producer := demandProducerSignal(hit, query.Activity) || lead.Producer
				if producer {
					company.ProducerSignal = true
				}
				if producer && demandProducerSignal(hit, query.Activity) {
					// The hit self-describes the company as a producer of the
					// product itself — the supply side. Retained as evidence,
					// never as confidence: the assembly gate decides whether
					// transactional proof makes this producer also a buyer.
					evidence.Status = "producer-signal"
				}
				company.Evidence = append(company.Evidence, evidence)
				if company.EvidenceSummary == "" {
					company.EvidenceSummary = strings.TrimSpace(evidence.Snippet)
				}
				if company.Domain == "" {
					company.Domain = strings.TrimPrefix(strings.ToLower(hostname(hit.Link)), "www.")
				}
				if producer {
					continue
				}
				company.Confidence = minFloat(0.98, company.Confidence+demandClaimWeight(evidence.ClaimType))
			}
		}
	}
	for _, company := range verified {
		company.Status = marketStatus(company)
	}
	var extractionErr error
	if len(extractionErrors) > 0 {
		extractionErr = fmt.Errorf("%d lead extraction batch(es) failed: %s", len(extractionErrors), extractionErrors[0])
	}
	return verified, queriesUsed, len(uniqueExtracted), rejected, pagesEnriched, extractionErr
}

// demandStageForLead recovers the consuming stage a lead was discovered under,
// so a verified company carries the stage it was tied to. The URL match is
// exact; because the model sometimes echoes a lead URL loosely, a lead whose
// snippet or title names the candidate is the fallback. The lane→stage map is
// the one built when the discovery lanes were created.
func demandStageForLead(leads []models.MarketLandscapeEvidence, lead models.MarketLandscapeLeadCandidate, laneStages map[string]string) string {
	for _, evidence := range leads {
		if strings.EqualFold(evidence.URL, lead.EvidenceURL) {
			return laneStages[strings.ToLower(strings.TrimSpace(evidence.Lane))]
		}
	}
	name := strings.ToLower(strings.TrimSpace(lead.Name))
	if name == "" {
		return ""
	}
	for _, evidence := range leads {
		if strings.Contains(strings.ToLower(evidence.Title+" "+evidence.Snippet), name) {
			return laneStages[strings.ToLower(strings.TrimSpace(evidence.Lane))]
		}
	}
	return ""
}

// demandNormalizeCandidateName collapses an echoed page title or headline to
// its company segment: the longest pipe-delimited part ("Butyl Acrylate |
// Products | X"), then everything before a colon (news headlines use
// "Headline: subhead"), then everything before a possessive 's
// ("INEOS-GNFC's New Plant: ..." → "INEOS-GNFC").
func demandNormalizeCandidateName(name string) string {
	name = strings.TrimSpace(name)
	if strings.Contains(name, "|") {
		best := ""
		for _, segment := range strings.Split(name, "|") {
			if trimmed := strings.TrimSpace(segment); len(trimmed) > len(best) {
				best = trimmed
			}
		}
		name = best
	}
	if idx := strings.Index(name, ":"); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}
	if idx := strings.Index(name, "'s"); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}
	return name
}

// demandVerificationPurpose is one templated verification search.
type demandVerificationPurpose struct {
	phrase string
	kind   string
}

// demandVerificationPurposes orders the three verification searches by what
// the discovery lane already claimed: a tender-lane lead confirms cheapest
// through tender evidence, an import-lane lead through trade data, and
// everything else through the buyer's own words first.
func demandVerificationPurposes(discoveryClaim string) []demandVerificationPurpose {
	tender := demandVerificationPurpose{phrase: "tender OR RFP OR procurement", kind: "tender or procurement record"}
	importer := demandVerificationPurpose{phrase: "import data OR importer of", kind: "import record"}
	ownSite := demandVerificationPurpose{phrase: "official site", kind: "own site"}
	switch discoveryClaim {
	case models.DemandClaimTender:
		return []demandVerificationPurpose{tender, importer, ownSite}
	case models.DemandClaimImport:
		return []demandVerificationPurpose{importer, tender, ownSite}
	default:
		return []demandVerificationPurpose{ownSite, tender, importer}
	}
}

// demandClaimForLead recovers the discovery claim a lead carried, matching by
// URL first and by a name mention in the lead's title/snippet second — the
// same discipline as demandStageForLead. Empty when nothing matches.
func demandClaimForLead(leads []models.MarketLandscapeEvidence, lead models.MarketLandscapeLeadCandidate) string {
	for _, evidence := range leads {
		if strings.EqualFold(evidence.URL, lead.EvidenceURL) {
			return evidence.ClaimType
		}
	}
	name := strings.ToLower(strings.TrimSpace(lead.Name))
	if name == "" {
		return ""
	}
	for _, evidence := range leads {
		if strings.Contains(strings.ToLower(evidence.Title+" "+evidence.Snippet), name) {
			return evidence.ClaimType
		}
	}
	return ""
}

// demandVerificationEvidence accepts one verification hit as consumption
// evidence when the tier qualifies, the company is named, and the hit carries
// buyer language or the activity itself. The claim type is read off the text.
func demandVerificationEvidence(hit models.SearchResult, company, activity, purpose string) (models.MarketLandscapeEvidence, bool) {
	domain := hostname(hit.Link)
	tier := marketSourceTier(domain)
	if override := demandSourceOverride(domain); override != "" && override != "aggregator" {
		// Publishers, consultancies and institutions are commentary — never
		// consumption evidence. Tender-portal domains are NOT overridden
		// here: a tender listing hosted on a portal that names the buyer is
		// real claim evidence about that buyer (the aggregators themselves
		// are stopped from masquerading as companies at extraction).
		tier = override
	}
	if !isQualifyingMarketTier(tier) || !mentionsMarketCompany(hit, company) {
		return models.MarketLandscapeEvidence{}, false
	}
	if !hasDemandSignalEvidence(hit) && !hasMarketActivityEvidence(hit, activity) {
		return models.MarketLandscapeEvidence{}, false
	}
	return models.MarketLandscapeEvidence{
		URL: hit.Link, Title: hit.Title, Snippet: hit.Snippet, Lane: "demand verification",
		ClaimType: demandClaimFromText(hit), SourceTier: tier, RetrievedAt: time.Now(), Status: purpose,
	}, true
}

// demandClaimFromText classifies a verification hit into the buyer claim
// taxonomy by the language it uses. Specific role markers are checked before
// generic input words: a hiring post that mentions feedstock is still a hiring
// signal, not consumption evidence.
func demandClaimFromText(hit models.SearchResult) string {
	text := strings.ToLower(hit.Title + " " + hit.Snippet)
	for _, marker := range []string{"tender", "rfp", "rfq", "e-procurement"} {
		if strings.Contains(text, marker) {
			return models.DemandClaimTender
		}
	}
	for _, marker := range []string{"import data", "importer of", "customs", "bill of lading"} {
		if strings.Contains(text, marker) {
			return models.DemandClaimImport
		}
	}
	for _, marker := range []string{"hiring", "job opening", "vacancy", "procurement manager", "purchasing manager"} {
		if strings.Contains(text, marker) {
			return models.DemandClaimHiring
		}
	}
	for _, marker := range []string{"raw material", "feedstock", "we use", "inputs", "consum"} {
		if strings.Contains(text, marker) {
			return models.DemandClaimUsage
		}
	}
	return models.DemandClaimOperator
}

// demandClaimWeight is the directness ladder: an import or tender record names
// an actual transaction; the buyer's own input language is next; generic
// stage verification is the weakest qualifying kind.
func demandClaimWeight(claimType string) float64 {
	switch claimType {
	case models.DemandClaimImport, models.DemandClaimTender:
		return 0.12
	case models.DemandClaimUsage:
		return 0.10
	default:
		return 0.08
	}
}

// demandProcurementTitles are the RocketReach title filters for the buyer side:
// the roles that execute and sign off on purchases.
var demandProcurementTitles = []string{"procurement", "purchasing", "sourcing", "supply chain"}

// discoverDemandRocketReach mirrors the market mode's RocketReach discovery
// with buyer-side title filters: one search per verified company, and profiles
// promoted only when their current employer matches that company — a profile
// at a same-named company elsewhere is exactly the mis-attribution a buyer
// list must not carry.
func (a *DemandChainAgent) discoverDemandRocketReach(ctx context.Context, companies []*models.MarketLandscapeCompany, budget int, eventCh chan<- models.AgentEvent) (int, int, int) {
	if a.rocketReach == nil || !a.rocketReach.Enabled() {
		return 0, 0, 0
	}
	used, profilesFound, promoted := 0, 0, 0
	for _, company := range companies {
		if used >= budget {
			break
		}
		emitAgentEvent(eventCh, models.EventToolCall, "Discovering RocketReach procurement contacts at "+company.Name, "rocketreach_search", company.Name)
		searchFilters := map[string][]string{
			"current_employer": []string{company.Name},
			"current_title":    demandProcurementTitles,
		}
		profiles, err := a.rocketReach.Search(ctx, searchFilters)
		used++
		if err != nil {
			emitAgentEvent(eventCh, models.EventError, "RocketReach search failed for "+company.Name+": "+err.Error(), "rocketreach_search", company.Name)
			continue
		}
		profilesFound += len(profiles)
		seen := make(map[string]bool)
		for _, existing := range company.KeyPeople {
			seen[strings.ToLower(strings.TrimSpace(existing))] = true
		}
		for _, profile := range profiles {
			if !strings.EqualFold(strings.TrimSpace(profile.CurrentEmployer), strings.TrimSpace(company.Name)) {
				emitAgentEvent(eventCh, models.EventThinking,
					fmt.Sprintf("RocketReach candidate %q not promoted: employer %q does not match %q", profile.Name, profile.CurrentEmployer, company.Name),
					"rocketreach_search", "")
				continue
			}
			name := strings.TrimSpace(profile.Name)
			key := strings.ToLower(name)
			if name != "" && !seen[key] && isPlausiblePersonName(name) {
				company.KeyPeople = append(company.KeyPeople, name)
				if company.KeyPeopleSources == nil {
					company.KeyPeopleSources = make(map[string]string)
				}
				company.KeyPeopleSources[name] = "RocketReach"
				seen[key] = true
				promoted++
				emitAgentEvent(eventCh, models.EventToolResult,
					fmt.Sprintf("Promoted procurement contact %s at %s (current employer matched)", name, company.Name),
					"rocketreach_search", "")
			}
		}
	}
	return used, profilesFound, promoted
}

// discoverDemandPeople mirrors the market mode's web key-person discovery with
// buyer-side role vocabulary: procurement, purchasing, sourcing and supply
// chain, at the verified buyers, never inventing people beyond the evidence.
func (a *DemandChainAgent) discoverDemandPeople(ctx context.Context, companies []*models.MarketLandscapeCompany, budget int, eventCh chan<- models.AgentEvent) int {
	used := 0
	for _, company := range companies {
		if used >= budget {
			break
		}
		searchQuery := fmt.Sprintf("%q (procurement OR purchasing OR sourcing OR \"supply chain\") (director OR head OR manager OR VP OR chief OR \"category manager\")", company.Name)
		emitAgentEvent(eventCh, models.EventToolCall, "Discovering procurement contacts at "+company.Name, "search", searchQuery)
		hits, err := a.engine.SearchCached(ctx, searchQuery, marketMaxResults)
		used++
		if err != nil {
			continue
		}
		evidence := make([]string, 0, len(hits))
		companyName := strings.ToLower(company.Name)
		for _, hit := range hits {
			text := strings.ToLower(hit.Title + " " + hit.Snippet)
			if strings.Contains(text, companyName) && strings.TrimSpace(hit.Link) != "" {
				evidence = append(evidence, fmt.Sprintf("%s — %s (%s)", hit.Title, hit.Snippet, hit.Link))
			}
		}
		if len(evidence) == 0 {
			continue
		}
		var people []models.MarketLandscapePersonCandidate
		prompt := fmt.Sprintf("Extract only named people whose procurement, purchasing, sourcing, or supply-chain role is explicitly associated with %q. Exclude job vacancies, generic titles, publishers, and people whose employer is not stated. Return a JSON array with name only. Evidence:\n%s", company.Name, strings.Join(evidence, "\n"))
		if err := a.engine.GenerateStructured(ctx, "You identify explicitly named current procurement and supply-chain employees from anchored company evidence. Never invent names.", prompt, &people); err != nil {
			continue
		}
		seen := make(map[string]bool)
		for _, existing := range company.KeyPeople {
			seen[strings.ToLower(strings.TrimSpace(existing))] = true
		}
		for _, person := range people {
			name := strings.TrimSpace(person.Name)
			key := strings.ToLower(name)
			if name != "" && !seen[key] && isPlausiblePersonName(name) {
				company.KeyPeople = append(company.KeyPeople, name)
				if company.KeyPeopleSources == nil {
					company.KeyPeopleSources = make(map[string]string)
				}
				company.KeyPeopleSources[name] = "web search"
				seen[key] = true
			}
		}
	}
	return used
}

// demandCountryMatches reports whether a company's stated country sits inside
// the requested geography. Both directions are checked, so "Republic of India"
// matches "India" and "India" matches "INDIA". An unknown country is kept: the
// run never claims knowledge it does not have.
func demandCountryMatches(country string, countries []string) bool {
	country = strings.ToLower(strings.TrimSpace(country))
	if country == "" {
		return true
	}
	for _, wanted := range countries {
		wanted = strings.ToLower(strings.TrimSpace(wanted))
		if wanted == "" {
			continue
		}
		if strings.Contains(country, wanted) || strings.Contains(wanted, country) {
			return true
		}
	}
	return false
}
