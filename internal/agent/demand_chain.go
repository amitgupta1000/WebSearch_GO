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
)

const (
	demandMaxDiscoveryQueries    = 48
	demandMinDiscoveryQueries    = 2
	demandMaxVerificationQueries = 200
	demandMaxLeadsEnriched       = 72
	demandMaxLeadPages           = 30
	demandMaxStages              = 7
	// demandMinStageCompanies is the QC floor: the buyer landscape is useful only when each
	// consuming stage (and the separate distribution category) has more than a handful of
	// identity-passed companies to work with.
	demandMinStageCompanies = 6
	// demandStageGapQueries is how many bounded, fresh searches one under-covered stage may
	// spend before the run reports the gap instead of inventing coverage.
	demandStageGapQueries = 3
	// demandGapReadPages is the bounded number of list/article/directory pages read per
	// under-covered stage. The pages are read for the companies they name, never promoted as
	// companies themselves.
	demandGapReadPages = 3
	// demandStageBroadenThreshold is the weak-result trigger: when the operator/product gap
	// searches produce fewer than this many new companies, retry with the stage's category
	// vocabulary (for example "solvents" or "aromatics").
	demandStageBroadenThreshold = 2
	// demandStageBroadenQueries caps the category fallback per stage.
	demandStageBroadenQueries = 2
	// demandDistributionStageName is the separate channel category. Distributors and
	// wholesalers buy the product to resell it; they are buyers, but not manufacturers in the
	// value chain, so they get their own roster rather than polluting a consumption stage.
	demandDistributionStageName = "Distribution and wholesale"
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
	engine *Engine
}

func NewDemandChainAgent(engine *Engine) *DemandChainAgent {
	return &DemandChainAgent{engine: engine}
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

	report := &models.DemandChainReport{
		ResearchID: researchID, Objective: query.Objective, Activity: query.Activity,
		Geography:  demandGeography(query),
		Iterations: 2, StartedAt: time.Now(),
	}

	// Phase 1 — the value chain. The operator's stages win; otherwise one
	// structured call proposes the consuming stages the whole run is organised
	// around. Failure degrades to a generic stage rather than failing the run.
	emitAgentEvent(eventCh, models.EventThinking, "Inferring the demand chain", "", "")
	chain, category, categoryRationale, categorySource, inferred, warnings := a.inferDemandChainWithCategory(ctx, query, eventCh)
	if inferred {
		var chainReview *models.ValueChainReview
		var reviewWarnings []string
		chain, chainReview, reviewWarnings = a.reviewDemandChain(ctx, query, chain, eventCh)
		report.ChainReview = chainReview
		warnings = append(warnings, reviewWarnings...)
	}
	report.Chain = chain
	report.ProductCategory = category
	report.CategoryRationale = categoryRationale
	report.CategorySource = categorySource
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

	// Stage coverage gate: every consuming stage and the distribution category must have more
	// than a handful of identity-passed companies. Spend bounded fresh searches on the gaps
	// before the QC judge sees the report.
	sections, _ := buildStageCandidates(merged, report.Chain)
	coverage := demandStageCoverage(sections)
	if len(demandUndercovered(coverage)) > 0 {
		if added := a.fillDemandStageGaps(ctx, query, coverage, companies, report, eventCh); added > 0 {
			pool = pool[:0]
			for _, company := range companies {
				pool = append(pool, company)
			}
			pool = append(pool, verified...)
			merged = demandDedupeCompanies(pool)
		}
	}
	sections, _ = buildStageCandidates(merged, report.Chain)
	coverage = demandStageCoverage(sections)

	// QC judge: propose rejections and flag under-covered stages. The deterministic coverage
	// criterion is applied even if the judge is unavailable.
	var rejectedNames map[string]string
	review := a.reviewDemandReport(ctx, query, report.Chain, merged, coverage, eventCh)
	rejectedNames = make(map[string]string, len(review.Rejected))
	for _, rejected := range review.Rejected {
		if key := normalizeMarketName(rejected.Name); key != "" {
			rejectedNames[key] = rejected.Reason
		}
	}
	if len(rejectedNames) > 0 {
		kept := make([]*models.MarketLandscapeCompany, 0, len(merged))
		for _, company := range merged {
			if reason, ok := rejectedNames[normalizeMarketName(company.Name)]; ok {
				report.Appendix = append(report.Appendix, company.Evidence...)
				report.Warnings = append(report.Warnings, fmt.Sprintf("Buyer QC removed %s: %s.", company.Name, reason))
				continue
			}
			kept = append(kept, company)
		}
		merged = kept
		// The reviewer may have removed enough rows to reopen a stage; rebuild the pool from
		// the surviving companies and fill the remaining gaps once, still within the budget.
		companies = make(map[string]*models.MarketLandscapeCompany, len(merged))
		for _, company := range merged {
			companies[company.EntityKey] = company
		}
		verified = nil
		sections, _ = buildStageCandidates(merged, report.Chain)
		coverage = demandStageCoverage(sections)
		if len(demandUndercovered(coverage)) > 0 {
			if added := a.fillDemandStageGaps(ctx, query, coverage, companies, report, eventCh); added > 0 {
				pool = pool[:0]
				for _, company := range companies {
					pool = append(pool, company)
				}
				merged = demandDedupeCompanies(pool)
			}
		}
	}

	// The judge's anchored follow-ups are fresh searches, not edits to the report: they can add
	// candidates the initial lanes missed, but every added name still went through the identity
	// and source gates.
	if added := a.runDemandFollowUps(ctx, query, report.Chain, companies, review.FollowUpQueries, report, eventCh); added > 0 {
		pool = pool[:0]
		for _, company := range companies {
			pool = append(pool, company)
		}
		pool = append(pool, verified...)
		merged = demandDedupeCompanies(pool)
	}
	// Gap extraction and follow-ups run after the judge's first pass. Re-apply its rejections
	// so a page cannot reintroduce a company the judge already removed.
	if len(rejectedNames) > 0 {
		kept := make([]*models.MarketLandscapeCompany, 0, len(merged))
		for _, company := range merged {
			if _, ok := rejectedNames[normalizeMarketName(company.Name)]; ok {
				report.Appendix = append(report.Appendix, company.Evidence...)
				continue
			}
			kept = append(kept, company)
		}
		merged = kept
	}

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
		company.Status = demandStatus(company)
		if company.Status == "lead" {
			// The cross-cutting results table is the verification view. Weak rows stay in the
			// stage roster and appendix; they are not presented as verified consumers.
			report.Appendix = append(report.Appendix, company.Evidence...)
			continue
		}
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
	report.StageCoverage = demandStageCoverage(report.StageCandidates)
	review.StageCoverage = report.StageCoverage
	review.MissingStages = nil
	for _, item := range report.StageCoverage {
		if !item.Pass {
			review.MissingStages = append(review.MissingStages, item.Stage)
			report.Warnings = append(report.Warnings, fmt.Sprintf("Stage coverage gap: %s has %d company(ies); QC minimum is %d. The report states the gap rather than inventing companies.", item.Stage, item.Count, item.Minimum))
		}
	}
	if len(review.MissingStages) > 0 {
		review.Verdict = "revise"
	}
	report.Reviews = append(report.Reviews, review)
	report.CompletedAt = time.Now()
	if exports := a.engine.ExportDemandChain(ctx, report); len(exports) > 0 {
		report.Exports = exports
	}
	emitAgentEvent(eventCh, models.EventComplete, fmt.Sprintf("Demand chain complete: %d verified result(s), %d stage(s)", len(report.Results), len(report.Chain)), "", "")
	return report, nil
}

// demandUndercovered returns the stage-coverage rows that did not reach the QC floor.
func demandUndercovered(coverage []models.DemandStageCoverage) []models.DemandStageCoverage {
	var gaps []models.DemandStageCoverage
	for _, item := range coverage {
		if !item.Pass {
			gaps = append(gaps, item)
		}
	}
	return gaps
}

// demandStageForFollowUp maps a judge follow-up query onto an inferred stage (or the separate
// distribution category). A query that names no stage is skipped rather than dumped into the
// general bucket: an unmapped search cannot improve stage coverage.
func demandStageForFollowUp(query string, chain []models.ValueChainStage) string {
	lowered := strings.ToLower(query)
	if strings.Contains(lowered, "distributor") || strings.Contains(lowered, "wholesaler") || strings.Contains(lowered, "wholesale") {
		return demandDistributionStageName
	}
	best := ""
	bestLen := 0
	for _, stage := range chain {
		if strings.Contains(lowered, strings.ToLower(stage.Name)) {
			return stage.Name
		}
		for _, operator := range stage.Operators {
			operator = strings.ToLower(strings.TrimSpace(operator))
			if operator != "" && strings.Contains(lowered, operator) && len(operator) > bestLen {
				best = stage.Name
				bestLen = len(operator)
			}
		}
	}
	if best != "" {
		return best
	}
	if len(chain) == 1 {
		return chain[0].Name
	}
	return ""
}

// runDemandFollowUps executes the QC judge's bounded, anchored follow-up searches. It only adds
// identity-passed candidates; verification remains a separate evidence step.
func (a *DemandChainAgent) runDemandFollowUps(ctx context.Context, query models.DemandChainQuery, chain []models.ValueChainStage, companies map[string]*models.MarketLandscapeCompany, queries []string, report *models.DemandChainReport, eventCh chan<- models.AgentEvent) int {
	added := 0
	seen := make(map[string]bool)
	for _, followUp := range queries {
		followUp = strings.TrimSpace(followUp)
		key := strings.ToLower(followUp)
		if followUp == "" || seen[key] || len(seen) >= 3 {
			continue
		}
		seen[key] = true
		stage := demandStageForFollowUp(followUp, chain)
		if stage == "" {
			report.Warnings = append(report.Warnings, fmt.Sprintf("QC follow-up skipped (no stage anchor): %s", followUp))
			continue
		}
		emitAgentEvent(eventCh, models.EventToolCall, "QC follow-up for "+stage, "search", followUp)
		hits, err := a.engine.SearchFresh(ctx, followUp, marketMaxResults)
		report.GapQueries++
		if err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("QC follow-up failed for %s: %v", stage, err))
			continue
		}
		report.SearchHits += len(hits)
		report.LeadsReviewed += len(hits)
		for _, hit := range hits {
			candidate := demandCandidate(hit, demandLane{term: followUp, stage: stage}, query)
			if candidate == nil {
				continue
			}
			if excludedMarketDomain(candidate.Domain, query.Exclusions) || candidate.Evidence[0].Status == "excluded" {
				report.Appendix = append(report.Appendix, candidate.Evidence...)
				continue
			}
			before := len(companies)
			mergeMarketCompany(companies, candidate)
			if len(companies) > before {
				added++
			}
		}
	}
	return added
}

// demandStageGapTerms builds stage-specific search terms from the chain's own operator
// vocabulary. Searching the full stage name plus "manufacturer" mostly returns directories and
// listicles ("Top Polyurethane Foam Manufacturers"), while operator phrases ("flexible foam
// producers") and the normalized stage label surface actual company pages.
func demandStageGapTerms(query models.DemandChainQuery, stageName string, chain []models.ValueChainStage) []string {
	if stageName == demandDistributionStageName {
		return []string{
			"distributor OR wholesaler of " + query.Activity,
			"authorised distributor OR wholesale supplier of " + query.Activity,
			"channel partner reseller of " + query.Activity,
		}
	}
	var terms []string
	if stage := findDemandStage(stageName, chain); stage != nil {
		for _, operator := range stage.Operators {
			operator = strings.TrimSpace(operator)
			if operator == "" {
				continue
			}
			terms = append(terms, operator)
			lowered := strings.ToLower(operator)
			if !strings.Contains(lowered, "manufactur") && !strings.Contains(lowered, "producer") &&
				!strings.Contains(lowered, "supplier") && !strings.Contains(lowered, "processor") {
				terms = append(terms, operator+" manufacturers")
			}
		}
		for _, product := range stage.Products {
			product = strings.TrimSpace(product)
			if product != "" {
				terms = append(terms, product+" manufacturer")
			}
		}
	}
	label := demandStageSearchLabel(stageName)
	if label == "" {
		label = strings.ToLower(strings.TrimSpace(stageName))
	}
	terms = append(terms, label+" manufacturer", label+" company", label+" plant")
	unique := make([]string, 0, len(terms))
	seen := make(map[string]bool, len(terms))
	for _, term := range terms {
		key := strings.ToLower(strings.TrimSpace(term))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, strings.TrimSpace(term))
	}
	return unique
}

// demandGapSource is a page read to recover stage-company names from a listicle, directory,
// article or report. The page itself is never the company.
type demandGapSource struct {
	URL        string
	Title      string
	Snippet    string
	SourceTier string
}

func demandSourceTierForHit(hit models.SearchResult) string {
	domain := hostname(hit.Link)
	if override := demandSourceOverride(domain); override != "" {
		return override
	}
	return marketSourceTier(domain)
}

// demandStageCategoryRules map stage vocabulary to the broader industry category a buyer
// researcher would use when the exact stage phrase is too narrow. Order matters: elastomer,
// foam and resin rules are tried before the generic polyurethane/chemical fallbacks.
var demandStageCategoryRules = []struct {
	markers  []string
	category string
}{
	{[]string{"elastomer"}, "polyurethane elastomers"},
	{[]string{"foam"}, "polyurethane foam"},
	{[]string{"sealant", "adhesive"}, "adhesives and sealants"},
	{[]string{"resin"}, "polyurethane resins"},
	{[]string{"coating", "paint"}, "coatings"},
	{[]string{"solvent", "acetate", "ester"}, "solvents"},
	{[]string{"aromatic", "benzene", "toluene", "xylene", "styrene"}, "aromatics"},
	{[]string{"oleochemical", "fatty acid"}, "oleochemicals"},
	{[]string{"surfactant"}, "surfactants"},
	{[]string{"plasticizer"}, "plasticizers"},
	{[]string{"lubricant"}, "lubricants"},
	{[]string{"biodiesel"}, "biodiesel"},
	{[]string{"methanol"}, "methanol"},
	{[]string{"polyurethane", "isocyanate", "tdi", "mdi"}, "polyurethanes"},
	{[]string{"polymer"}, "polymers"},
}

// demandStageCategories returns the broad category terms for a stage. They are used only as a
// fallback when the exact stage/operator searches are weak; they are not a replacement for
// demand-anchored verification.
func demandStageCategories(stageName string, stage *models.ValueChainStage, query models.DemandChainQuery) []string {
	text := strings.ToLower(strings.TrimSpace(stageName))
	if stage != nil {
		text += " " + strings.ToLower(strings.Join(stage.Products, " "))
		text += " " + strings.ToLower(strings.Join(stage.Operators, " "))
	}
	text += " " + strings.ToLower(strings.TrimSpace(query.Activity))
	var categories []string
	seen := make(map[string]bool)
	for _, rule := range demandStageCategoryRules {
		matched := false
		for _, marker := range rule.markers {
			if strings.Contains(text, marker) {
				matched = true
				break
			}
		}
		if matched && !seen[rule.category] {
			seen[rule.category] = true
			categories = append(categories, rule.category)
		}
	}
	if len(categories) == 0 {
		label := demandStageSearchLabel(stageName)
		if label == "" {
			label = strings.ToLower(strings.TrimSpace(stageName))
		}
		if label != "" && !seen[label] {
			categories = append(categories, label)
		}
	}
	return categories
}

// demandStageBroadenTerms turns the stage's category into the two broad searches the gap filler
// may run after the exact operator queries return weak results.
func demandStageBroadenTerms(query models.DemandChainQuery, stageName string, chain []models.ValueChainStage, productCategory string) []string {
	if stageName == demandDistributionStageName {
		return []string{
			"chemical distributors " + query.Activity,
			"industrial chemical wholesale distributors",
		}
	}
	stage := findDemandStage(stageName, chain)
	if category := strings.TrimSpace(productCategory); category != "" {
		terms := []string{category + " manufacturers", category + " companies"}
		if stage != nil {
			for _, operator := range stage.Operators {
				operator = strings.TrimSpace(operator)
				if operator != "" {
					terms[0] = category + " " + operator
					break
				}
			}
		}
		return terms
	}
	categories := demandStageCategories(stageName, stage, query)
	terms := make([]string, 0, demandStageBroadenQueries)
	for _, category := range categories {
		category = strings.TrimSpace(category)
		if category == "" {
			continue
		}
		terms = append(terms, category+" manufacturers")
		terms = append(terms, category+" companies")
		if len(terms) >= demandStageBroadenQueries {
			break
		}
	}
	return terms
}

// fillDemandStageGaps spends bounded fresh searches on stages the run has not yet populated to
// the QC floor. It adds identity-passed discovery candidates only; it does not invent verified
// buyers. The caller rebuilds the stage roster after it returns.
func (a *DemandChainAgent) fillDemandStageGaps(ctx context.Context, query models.DemandChainQuery, coverage []models.DemandStageCoverage, companies map[string]*models.MarketLandscapeCompany, report *models.DemandChainReport, eventCh chan<- models.AgentEvent) int {
	gaps := demandUndercovered(coverage)
	if len(gaps) == 0 {
		return 0
	}
	added := 0
	for _, gap := range gaps {
		stage := strings.TrimSpace(gap.Stage)
		if stage == "" {
			continue
		}
		stageAdded := 0
		stageHits := 0
		var gapSources []demandGapSource
		seenSource := make(map[string]bool)
		processHits := func(hits []models.SearchResult, searchQuery string) {
			report.SearchHits += len(hits)
			report.LeadsReviewed += len(hits)
			stageHits += len(hits)
			for _, hit := range hits {
				candidate := demandCandidate(hit, demandLane{term: searchQuery, stage: stage}, query)
				if candidate == nil {
					continue
				}
				if excludedMarketDomain(candidate.Domain, query.Exclusions) || candidate.Evidence[0].Status == "excluded" {
					report.Appendix = append(report.Appendix, candidate.Evidence...)
					continue
				}
				before := len(companies)
				mergeMarketCompany(companies, candidate)
				if len(companies) > before {
					added++
					stageAdded++
				}
				// A roster/report page can name many companies even though its own title is
				// not a company. Collect those pages for bounded reading and extraction.
				tier := demandSourceTierForHit(hit)
				if marketLeadReadable(tier, hit.Title) && tier != "social" {
					key := demandCanonicalURL(hit.Link)
					if key == "" {
						key = strings.ToLower(strings.TrimSpace(hit.Link))
					}
					if !seenSource[key] {
						seenSource[key] = true
						gapSources = append(gapSources, demandGapSource{
							URL: hit.Link, Title: hit.Title, Snippet: hit.Snippet, SourceTier: tier,
						})
					}
				}
			}
		}
		runSearch := func(term string, includeActivity bool) {
			if err := ctx.Err(); err != nil {
				return
			}
			searchQuery := strings.TrimSpace(term + " " + demandGeography(query))
			if includeActivity {
				searchQuery = strings.TrimSpace(query.Activity + " " + term + " " + demandGeography(query))
			}
			emitAgentEvent(eventCh, models.EventToolCall, "Gap search for "+stage, "search", searchQuery)
			hits, err := a.engine.SearchFresh(ctx, searchQuery, marketMaxResults)
			report.GapQueries++
			if err != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("gap search for %s failed: %v", stage, err))
				return
			}
			processHits(hits, searchQuery)
		}
		terms := demandStageGapTerms(query, stage, report.Chain)
		for attempt := 0; attempt < demandStageGapQueries && attempt < len(terms); attempt++ {
			runSearch(terms[attempt], true)
		}
		// The exact stage/operator phrases can be too narrow. If they produced almost no
		// companies, retry with the wider category vocabulary (solvents, aromatics,
		// polyurethane foam, adhesives and sealants, etc.).
		if stageAdded < demandStageBroadenThreshold || gap.Count+stageAdded < demandMinStageCompanies {
			broadTerms := demandStageBroadenTerms(query, stage, report.Chain, report.ProductCategory)
			for attempt := 0; attempt < demandStageBroadenQueries && attempt < len(broadTerms); attempt++ {
				runSearch(broadTerms[attempt], false)
			}
			if len(broadTerms) > 0 {
				emitAgentEvent(eventCh, models.EventThinking,
					fmt.Sprintf("Gap search for %s was weak (%d new company(ies)); broadened to category terms", stage, stageAdded),
					"demand_gap", "")
			}
		}
		// Read the densest roster-shaped pages first; three per stage is enough to recover a
		// stage roster without turning a gap fill into a second full discovery run.
		sort.SliceStable(gapSources, func(i, j int) bool {
			return marketLeadReadPriority(gapSources[i].SourceTier, gapSources[i].Title) <
				marketLeadReadPriority(gapSources[j].SourceTier, gapSources[j].Title)
		})
		if len(gapSources) > demandGapReadPages {
			gapSources = gapSources[:demandGapReadPages]
		}
		for _, company := range a.extractDemandGapCompanies(ctx, query, stage, gapSources, report, eventCh) {
			if company == nil {
				continue
			}
			if excludedMarketDomain(company.Domain, query.Exclusions) {
				report.Appendix = append(report.Appendix, company.Evidence...)
				continue
			}
			before := len(companies)
			mergeMarketCompany(companies, company)
			if len(companies) > before {
				added++
				stageAdded++
			}
		}
		emitAgentEvent(eventCh, models.EventToolResult,
			fmt.Sprintf("Gap search %s: %d hit(s), %d page(s) read, %d new company(ies)", stage, stageHits, len(gapSources), stageAdded),
			"demand_gap", "")
	}
	return added
}

// extractDemandGapCompanies reads the bounded gap pages and asks one stage-anchored extraction
// call to recover the companies they name. The result is a roster candidate, not verification:
// the source may be a listicle or directory, so the company carries its evidence but no
// ownership claim.
func (a *DemandChainAgent) extractDemandGapCompanies(ctx context.Context, query models.DemandChainQuery, stage string, sources []demandGapSource, report *models.DemandChainReport, eventCh chan<- models.AgentEvent) []*models.MarketLandscapeCompany {
	if len(sources) == 0 {
		return nil
	}
	targets := make([]string, 0, len(sources))
	sourceByURL := make(map[string]demandGapSource, len(sources))
	for _, source := range sources {
		key := demandCanonicalURL(source.URL)
		if key == "" {
			continue
		}
		sourceByURL[key] = source
		targets = append(targets, source.URL)
	}
	if len(targets) == 0 {
		return nil
	}
	outcomes := a.engine.ScrapeCachedBatch(ctx, targets)
	contentByURL := make(map[string]string, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.Err != nil || outcome.Result == nil {
			continue
		}
		content := strings.TrimSpace(outcome.Result.Content)
		if content == "" {
			continue
		}
		contentByURL[demandCanonicalURL(outcome.URL)] = marketPageExcerpt(content, marketRosterPageExcerpt)
		report.PagesEnriched++
	}
	var leadText strings.Builder
	index := 1
	for _, source := range sources {
		content := contentByURL[demandCanonicalURL(source.URL)]
		if content == "" {
			content = source.Snippet
		}
		if content == "" {
			continue
		}
		fmt.Fprintf(&leadText, "%d. %s — %s\n%s\n\n", index, source.Title, source.URL, content)
		index++
	}
	if strings.TrimSpace(leadText.String()) == "" {
		return nil
	}
	emitAgentEvent(eventCh, models.EventToolResult, fmt.Sprintf("Read %d page(s) for stage %s", len(sources), stage), "demand_gap", "")
	stageContext := stage
	if found := findDemandStage(stage, report.Chain); found != nil && len(found.Operators) > 0 {
		stageContext = stage + " (" + strings.Join(found.Operators, ", ") + ")"
	}
	prompt := fmt.Sprintf(`Extract every actual company that operates this consuming stage for %q: %s.
These pages may be listicles, directories, reports, articles or company pages. A list, report or article is a valid source for the companies it names, including each row of a key-players or buyer table. Use only names explicitly present in the supplied text. Return legal company names, never page titles, publishers, authors, categories, process descriptions, product names or regions. Exclude producers or suppliers of %q itself and any company the text only describes as supplying the product rather than operating the stage. For each company return name, country, reason, evidence_url (the supplied page URL that names it), and producer as false. Return a JSON array.
Pages:
%s`, query.Activity, stageContext, query.Activity, leadText.String())
	var extracted []models.MarketLandscapeLeadCandidate
	if err := a.engine.GenerateStructured(ctx, "You extract company names from stage-gap pages for a buyer roster. Never invent a name and never promote a page title, publisher, person or category to a company.", prompt, &extracted); err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("stage-gap extraction failed for %s: %v", stage, err))
		emitAgentEvent(eventCh, models.EventError, "Stage-gap extraction failed: "+err.Error(), "demand_gap", "")
		return nil
	}
	report.LeadsExtracted += len(extracted)
	accepted := make([]*models.MarketLandscapeCompany, 0, len(extracted))
	seen := make(map[string]bool)
	geoNames := demandGeoNames(query)
	firstSource := sources[0]
	for _, candidate := range extracted {
		if candidate.Producer {
			continue
		}
		name := demandNormalizeCandidateName(strings.TrimSpace(candidate.Name))
		if name == "" || strings.EqualFold(name, strings.TrimSpace(candidate.Country)) || geoNames[normalizeMarketName(name)] ||
			normalizeMarketName(name) == normalizeMarketName(query.Activity) || demandSourceName(name, query.Activity) ||
			!isPlausibleMarketLeadName(name) || !demandCompanyTitleFor(name, query.Activity) {
			continue
		}
		key := normalizeMarketName(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		evidenceURL := strings.TrimSpace(candidate.EvidenceURL)
		source := sourceByURL[demandCanonicalURL(evidenceURL)]
		if source.URL == "" {
			source = firstSource
			evidenceURL = firstSource.URL
		}
		domain := ""
		if host := hostname(evidenceURL); host != "" && demandCompanyOwnsDomain(name, host) {
			domain = strings.ToLower(strings.TrimPrefix(host, "www."))
		}
		accepted = append(accepted, &models.MarketLandscapeCompany{
			EntityKey: "company:name:" + key, Name: name, Domain: domain,
			Country: strings.TrimSpace(candidate.Country), Activity: query.Activity, Stage: stage,
			Status: "candidate", EvidenceType: "roster-extracted", Confidence: 0.45,
			EvidenceSummary: strings.TrimSpace(candidate.Reason),
			Evidence: []models.MarketLandscapeEvidence{{
				URL: evidenceURL, Title: source.Title, Snippet: source.Snippet,
				Lane: "stage gap roster", ClaimType: models.DemandClaimOperator,
				SourceTier: source.SourceTier, RetrievedAt: time.Now(), Status: "roster-extracted",
			}},
		})
	}
	return accepted
}

// isDemandDocumentTitle catches titles that name documents rather than
// companies — market reports, patents, studies and analyses rank well on stage
// vocabulary, so the discovery lanes pull in plenty of them. They are evidence,
// and stay in the appendix, never in the company rosters.

func isDemandDocumentTitle(title string) bool {
	if isResearchDocumentTitle(title) {
		return true
	}
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
func demandNameIsPerson(name string) bool {
	lowered := " " + strings.ToLower(strings.TrimSpace(name)) + " "
	personParticles := []string{" von "}
	matches := false
	for _, particle := range personParticles {
		if strings.Contains(lowered, particle) {
			matches = true
			break
		}
	}
	if !matches {
		return false
	}
	for _, token := range strings.Fields(lowered) {
		if demandCorporateSuffixes[strings.Trim(token, ".,;:()")] {
			return false
		}
	}
	return true
}

func demandCompanyTitle(name string) bool {
	return demandCompanyTitleFor(name, "")
}

func demandCompanyTitleFor(name, activity string) bool {
	return isPlausibleMarketLeadName(name) && !isDemandDocumentTitle(name) && !isGenericMarketTitle(name) &&
		!isDemandBaitTitle(name) && !demandNameIsCategoryFor(name, activity) && !demandNameIsPerson(name) &&
		!marketPublisherSuffix(name) && !demandNameIsInstitution(name) && !demandNameIsHeadline(name) &&
		!demandNameEchoesActivity(name, activity)
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

// demandSourceOverride reclassifies aggregator, publisher, consultancy and
// institution domains for the buyer side. It is the shared discovery-source
// classifier (entity_source.go) under the name the buyer code reads; the labels
// are the same, so a vendor added for one landscape mode is known to both.
// The returned tier never qualifies, so such pages stay in the run as demand
// signals and leads while being unable to present as companies themselves.
func demandSourceOverride(domain string) string {
	return sharedSourceOverride(domain)
}

// demandSourceName catches a candidate whose *name* is an aggregator page,
// consultancy, institution or publication rather than a company — the
// extraction occasionally echoes the page title ("Latest Alcohol Tenders in
// India 2026 - TendersOnTime", "IMARC Group") as a buyer. A "topic - site"
// echo whose topic names the product is likewise a page title, not a buyer.
// demandNameIsCategory rejects names that describe a category, process, product page or article
// rather than one operator: "Biodiesel producers", "Biodiesel Production and Distribution",
// "TDI Products", "Renewable ... Production Plant", "Technology by Covestro". A legal suffix
// overrides the heuristic, so "Acme Products Ltd" remains a company.
func demandNameIsCategory(name string) bool {
	return demandNameIsCategoryFor(name, "")
}

func demandNameIsCategoryFor(name, activity string) bool {
	lowered := strings.ToLower(strings.TrimSpace(name))
	if lowered == "" {
		return false
	}
	if strings.Contains(" "+lowered+" ", " by ") {
		return true
	}
	fields := strings.Fields(lowered)
	if len(fields) == 0 {
		return false
	}
	hasLegalSuffix := false
	for _, token := range fields {
		if demandCorporateSuffixes[strings.Trim(token, ".,;:()")] {
			hasLegalSuffix = true
			break
		}
	}
	if !hasLegalSuffix {
		for _, marker := range []string{" licensing", " licencing", " regulation", " regulatory", " policy", " guidelines", " approval", " permit", " plant ", " project ", " production"} {
			if strings.Contains(" "+lowered+" ", marker) {
				return true
			}
		}
	}
	categoryWords := map[string]bool{
		"producer": true, "producers": true, "manufacturer": true, "manufacturers": true,
		"supplier": true, "suppliers": true, "product": true, "products": true,
		"plants": true, "plant": true, "factory": true, "factories": true, "industry": true, "industries": true,
		"market": true, "markets": true, "members": true,
	}
	last := strings.Trim(fields[len(fields)-1], ".,;:()")
	if !categoryWords[last] {
		return false
	}
	if hasLegalSuffix {
		return false
	}
	// Multiple role/category words without a legal suffix are a category or a list title, not a
	// company: "China TDI Foam Chemical Manufacturers Suppliers Factory".
	roleCount := 0
	for _, field := range fields {
		switch strings.Trim(field, ".,;:()") {
		case "manufacturer", "manufacturers", "supplier", "suppliers", "producer", "producers", "factory", "factories", "plant", "plants", "members":
			roleCount++
		}
	}
	if roleCount >= 2 {
		return true
	}
	// "Deccan Foam Products" and "Faridabad Foam Industries" are plausible company names;
	// "TDI Products" is a product phrase, not a company. A short/acronym first token marks
	// the product-title shape, while a longer first token reads as a brand/place.
	if last == "product" || last == "products" || last == "industry" || last == "industries" {
		first := strings.Trim(fields[0], ".,;:()")
		if len(fields) == 1 || len(first) <= 3 {
			return true
		}
		activity = strings.ToLower(strings.TrimSpace(activity))
		if activity != "" && (strings.HasPrefix(activity, first) || strings.HasPrefix(first, activity)) {
			return true
		}
		return false
	}
	return true
}

// demandNameIsInstitution catches government/agency/procurement-portal shapes that are not
// operating companies. It is deliberately narrow: real company names do not end in these roles.
func demandNameIsInstitution(name string) bool {
	lowered := " " + strings.ToLower(strings.TrimSpace(name)) + " "
	for _, marker := range []string{" administration ", " agency ", " department ", " authority ", " procurement portal ", " passport ", " customs ruling ", " oehha ", " specialchem ", " wholesale central ", " members "} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// demandNameIsHeadline catches article headlines that describe an event rather than a company.
func demandNameIsHeadline(name string) bool {
	lowered := strings.ToLower(strings.TrimSpace(name))
	for _, marker := range []string{" pioneers ", " supply of ", " announces ", " launches ", " opens ", " acquires ", " wins ", " signs ", " plans to "} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// demandNameEchoesActivity rejects a product/report phrase that begins with the search product
// itself, such as "Toluene diisocyanates (TDI), TDA and DNT REACH".
func demandNameEchoesActivity(name, activity string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	activity = strings.ToLower(strings.TrimSpace(activity))
	if name == "" || activity == "" {
		return false
	}
	if !strings.HasPrefix(name, activity) {
		return false
	}
	for _, token := range strings.Fields(name) {
		if demandCorporateSuffixes[strings.Trim(token, ".,;:()")] {
			return false
		}
	}
	return true
}
func demandSourceName(name, activity string) bool {

	lowered := strings.ToLower(name)
	// En/em dashes are headline punctuation too; normalize before matching.
	lowered = strings.ReplaceAll(lowered, "–", "-")
	lowered = strings.ReplaceAll(lowered, "—", "-")
	if demandNameIsCategoryFor(name, activity) || demandNameIsPerson(name) || marketPublisherSuffix(name) ||
		demandNameIsInstitution(name) || demandNameIsHeadline(name) || demandNameEchoesActivity(name, activity) {
		return true
	}
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
		"publication", "publications", "publisher", " press", " news", "magazine",
		"acs publications", "taylor & francis", "tandfonline",
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

// demandStatus is the buyer-side verification ladder. Directory, trade, news and publisher
// pages are corroboration only: they can help a human judge a row but cannot make it "verified".
// A verified row needs either an operator-owned page plus a transaction/input claim, or two
// independent operator-owned sources. A single unknown/official page stays a lead.
func demandStatus(company *models.MarketLandscapeCompany) string {
	if company == nil {
		return "lead"
	}
	ownedDomains := make(map[string]bool)
	transaction := false
	for _, evidence := range company.Evidence {
		if evidence.Status == "excluded" || evidence.Status == "producer-signal" {
			continue
		}
		if !isQualifyingMarketTier(evidence.SourceTier) {
			continue
		}
		// Directory and trade pages host many companies; they never own one identity.
		if evidence.SourceTier == "directory" || evidence.SourceTier == "trade" {
			continue
		}
		domain := hostname(evidence.URL)
		if evidence.SourceTier == "official" && !demandCompanyOwnsDomain(company.Name, domain) {
			// An unrecognised blog is classified "official" by the shared tier helper; require
			// the domain to look like the company's own before it can own evidence. A tender or
			// import record hosted elsewhere still counts as a transaction, not as ownership.
			if evidence.ClaimType == models.DemandClaimTender || evidence.ClaimType == models.DemandClaimImport {
				transaction = true
			}
			continue
		}
		ownedDomains[domain] = true
		switch evidence.ClaimType {
		case models.DemandClaimImport, models.DemandClaimTender, models.DemandClaimUsage:
			transaction = true
		}
	}
	owned := len(ownedDomains)
	if owned >= 2 || (owned >= 1 && transaction) {
		return "verified"
	}
	if owned == 1 {
		// One operator-owned page corroborates identity and demand language, but is not the
		// two-source verification threshold. Directory hits never reach this branch.
		return "corroborated"
	}
	return "lead"
}

// demandCompanyOwnsDomain extends the shared brand-token test with an acronym test: "Indian Oil
// Corporation" -> iocl.com (IOC + legal-form L) is the company's own domain, while a generic
// article domain is not.
func demandCompanyOwnsDomain(name, domain string) bool {
	if hasCompanyIdentitySignal(name, domain) || marketBrandMatchesDomain(name, domain) {
		return true
	}
	label := strings.ToLower(strings.TrimPrefix(domain, "www."))
	if index := strings.Index(label, "."); index > 0 {
		label = label[:index]
	}
	label = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r
		}
		return -1
	}, label)
	if len(label) < 2 {
		return false
	}
	var initials strings.Builder
	for _, token := range strings.Fields(strings.ToLower(name)) {
		token = strings.Trim(token, ".,;:()&")
		if token == "" || demandCorporateSuffixes[token] || len(token) < 2 {
			continue
		}
		initials.WriteByte(token[0])
	}
	acronym := initials.String()
	return len(acronym) >= 2 && strings.HasPrefix(label, acronym)
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

// demandDistributionStage is the separate channel category. It is deliberately not part of the
// inferred manufacturing chain: distributors buy to resell, so they are buyers without being a
// transformation step. The stage shell exists so the stage roster, QC coverage and artifacts can
// treat the category uniformly.
func demandDistributionStage() models.ValueChainStage {
	return models.ValueChainStage{
		Name:      demandDistributionStageName,
		Rationale: "Distributors and wholesalers buy the product to resell it; real demand, not a manufacturing step.",
		Source:    "category",
		Distance:  models.StageDistanceDirect,
		Operators: []string{"distributor", "wholesaler", "authorised distributor"},
	}
}

// buildStageCandidates groups identity-passed companies by their consuming
// stage, in the chain's order, deterministically. Raw discovery leads are
// evidence for the LLM extraction and verification queue, not companies; they
// remain auditable in the appendix until a company identity is established.
// Sorted by confidence, then name, like every other view.
// demandStageCoverage is the deterministic QC check: every inferred stage and the separate
// distribution category must have more than a handful of identity-passed companies. The general
// demand-lane bucket is not a stage and is excluded.
func demandStageCoverage(sections []models.StageCandidates) []models.DemandStageCoverage {
	coverage := make([]models.DemandStageCoverage, 0, len(sections))
	for _, section := range sections {
		if section.Stage.Source == "lanes" || strings.TrimSpace(section.Stage.Name) == "" {
			continue
		}
		count := len(section.Companies)
		coverage = append(coverage, models.DemandStageCoverage{
			Stage: section.Stage.Name, Count: count, Minimum: demandMinStageCompanies,
			Pass: count >= demandMinStageCompanies,
		})
	}
	return coverage
}

// reviewDemandReport is the LLM judge for the buyer landscape. It proposes rejects and
// follow-up searches; the caller applies them deterministically, and no name can enter the
// report that was not already supported by the run's evidence. Stage coverage is computed
// before the call and is always a hard criterion, even when the judge is unavailable.
func (a *DemandChainAgent) reviewDemandReport(ctx context.Context, query models.DemandChainQuery, chain []models.ValueChainStage, merged []*models.MarketLandscapeCompany, coverage []models.DemandStageCoverage, eventCh chan<- models.AgentEvent) models.DemandChainReview {
	review := models.DemandChainReview{StageCoverage: coverage}
	var rows strings.Builder
	limit := len(merged)
	if limit > 50 {
		limit = 50
	}
	for index := 0; index < limit; index++ {
		company := merged[index]
		if company == nil {
			continue
		}
		fmt.Fprintf(&rows, "- %s | stage=%s | country=%s | status=%s | confidence=%.2f\n",
			company.Name, company.Stage, company.Country, demandStatus(company), company.Confidence)
		evidenceLimit := len(company.Evidence)
		if evidenceLimit > 2 {
			evidenceLimit = 2
		}
		for _, evidence := range company.Evidence[:evidenceLimit] {
			fmt.Fprintf(&rows, "  source: %s | %s | tier=%s | claim=%s | %s\n",
				evidence.Title, evidence.URL, evidence.SourceTier, evidence.ClaimType, truncateMarketText(evidence.Snippet, 240))
		}
	}
	coverageText := make([]string, 0, len(coverage))
	for _, item := range coverage {
		coverageText = append(coverageText, fmt.Sprintf("%s: %d (minimum %d)", item.Stage, item.Count, item.Minimum))
	}
	prompt := fmt.Sprintf(`Review this buyer-landscape result for "%s" in %s.
The buyer definition: a company that consumes the product as an input, or buys it to resell;
manufacturers at most two transformation steps downstream, plus a separate distribution and
wholesale category. Every stage and the distribution category should have at least %d
identity-passed companies: %s.
Reject a row only when it is a publisher, person, category/process title, article or product
page, a supplier/trader rather than a buyer, tied to the wrong stage, or has no demand evidence.
Do not invent companies. Propose at most 3 follow-up searches, each anchored on the activity and
a stage/company. Return JSON with verdict (pass or revise), score 0-1, rejected (name and
reason), missing_stages, follow_up_queries, and reason.
Rows:
%s`, query.Activity, demandGeography(query), demandMinStageCompanies, strings.Join(coverageText, "; "), rows.String())
	if err := a.engine.GenerateStructured(ctx, "You are a strict buyer-research quality reviewer. Prefer a shorter, accurate list over a longer list containing publishers, people, categories or suppliers. Never invent a company or a source.", prompt, &review); err != nil {
		review.Verdict = "unavailable"
		review.Reason = err.Error()
		review.Rejected = nil
		review.FollowUpQueries = nil
		emitAgentEvent(eventCh, models.EventError, "Buyer QC review unavailable: "+err.Error(), "demand_qc", "")
		return review
	}
	review.Verdict = strings.ToLower(strings.TrimSpace(review.Verdict))
	if review.Verdict == "" {
		review.Verdict = "unavailable"
	} else if review.Verdict != "pass" {
		review.Verdict = "revise"
	}
	for _, gap := range demandUndercovered(coverage) {
		review.MissingStages = appendUniqueStrings(review.MissingStages, gap.Stage)
	}
	if len(review.MissingStages) > 0 || len(review.Rejected) > 0 {
		review.Verdict = "revise"
	}
	emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Buyer QC review: %s (%.2f) — %d rejection(s), %d under-covered stage(s)", review.Verdict, review.Score, len(review.Rejected), len(review.MissingStages)), "demand_qc", "")
	return review
}

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
		if company.EvidenceType == "discovery-lead" {
			// Discovery leads have not passed the structured buyer/entity
			// classifier and verification gate. Never render their page titles
			// as company names in a stage roster.
			demoted = append(demoted, company.Evidence...)
			continue
		}
		company.Status = demandStatus(company)
		if strings.TrimSpace(company.Stage) == "" {
			unclassified = append(unclassified, company)
			continue
		}
		// A verified company need not spell its consuming stage in its name; it
		// earned its place through extraction, verification and the identity
		// gate.
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
	// The channel category is always present in the roster (empty when the run found none), so
	// the operator can see the distributor/wholesaler lane was run and judge its coverage.
	sections = append(sections, models.StageCandidates{
		Stage: demandDistributionStage(), Companies: sortStageCompanies(byStage[demandDistributionStageName]),
	})
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
func demandEvidenceRank(evidenceType string) int {
	switch evidenceType {
	case "company-page":
		return 2
	case "roster-extracted":
		return 1
	default:
		return 0
	}
}

func demandMergeBucket(members []*models.MarketLandscapeCompany) *models.MarketLandscapeCompany {
	best := members[0]
	for _, member := range members[1:] {
		// A raw discovery lead must never represent a bucket that contains an
		// identity-passed company page: its title can be an article or process
		// description, and choosing it would demote every merged evidence row.
		if demandEvidenceRank(best.EvidenceType) < demandEvidenceRank(member.EvidenceType) {
			best = member
			continue
		}
		if demandEvidenceRank(member.EvidenceType) < demandEvidenceRank(best.EvidenceType) {
			continue
		}
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
	rep.Status = demandStatus(&rep)
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
	if query.DiscoveryQueries > 100 {
		query.DiscoveryQueries = 100
	}
	if query.VerificationQueries <= 0 {
		query.VerificationQueries = demandMaxVerificationQueries
	}
	if query.VerificationQueries > 350 {
		query.VerificationQueries = 350
	}
	if query.MaxLeadsEnriched <= 0 {
		query.MaxLeadsEnriched = demandMaxLeadsEnriched
	}
	if query.MaxLeadsEnriched > 120 {
		query.MaxLeadsEnriched = 120
	}
	if query.MaxCompanies <= 0 || query.MaxCompanies > 80 {
		query.MaxCompanies = 48
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
func (a *DemandChainAgent) inferDemandChainWithCategory(ctx context.Context, query models.DemandChainQuery, eventCh chan<- models.AgentEvent) ([]models.ValueChainStage, string, string, string, bool, []string) {
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
				Products:  knownStageProducts(query.Activity, name),
			})
		}
		category := demandFallbackCategory(query.Activity, stages)
		emitAgentEvent(eventCh, models.EventToolResult, fmt.Sprintf("Using %d operator-supplied consuming stage(s)", len(stages)), "", "")
		return stages, category, "Derived from operator-supplied stages.", "fallback", false, nil
	}

	var inference models.DemandChainInference
	// The prompt encodes the buyer definition: a buyer is a manufacturer whose
	// process consumes the product as an input, so the chain stops at two
	// transformation steps downstream — deeper steps are context, not buyers.
	prompt := fmt.Sprintf(`Identify the industrial manufacturing processes that are potential buyers of %q: processes that consume the product%s. Scope: at most two transformation steps downstream.
Also identify a broad buyer-side category for the product: the immediate downstream product family or industry that consumes it, specific enough to search, not an end-use sector and not a generic superclass. Examples: toluene diisocyanate -> polyurethanes; acetic acid -> solvents or acetates; toluene -> aromatics; coal -> coal products, never fuel or energy. Use one to four words. Return a JSON object with category, category_rationale, and stages.
Each stage has name (the consuming manufacturing process), rationale (one line naming which input the stage consumes), products (up to 4 concrete products made within the stage that consume the product or its immediate derivative; do not return generic category labels), operators (2-3 search phrases for the plants or companies operating that stage), and distance: 1 when the stage consumes %q directly as an input or raw material, 2 when it consumes the output of a distance-1 process. For example, decompose a generic solvent stage into concrete acetate-esters or other product families actually derived from the input. Exclude stages beyond two steps, end-use sectors that manufacture nothing, retail, logistics, trading and publication stages.`, query.Activity, demandGeographyClause(query), query.Activity)
	if err := a.engine.GenerateStructured(ctx,
		"You infer industrial value chains for buyer research. A buyer is a manufacturer whose process consumes the product as an input; never propose stages more than two transformation steps downstream, and keep operator phrases as concrete plant vocabulary. The category must be the immediate downstream product family, not an end-use sector.",
		prompt, &inference); err != nil {
		stages, inferred, warnings := a.demandChainFallback(ctx, "Value-chain inference failed", err, query, eventCh)
		category := demandFallbackCategory(query.Activity, stages)
		return stages, category, "Derived after inference failure.", "fallback", inferred, warnings
	}

	var warnings []string
	sanitized := sanitizeDemandStages(inference.Stages)
	if len(sanitized.dropped) > 0 {
		// Audit trail for the buyer definition: what the model proposed
		// beyond the two-step scope is reported, not silently discarded.
		warnings = append(warnings, fmt.Sprintf("%d proposed stage(s) beyond two transformation steps were dropped as out of buyer scope: %s.", len(sanitized.dropped), strings.Join(sanitized.dropped, ", ")))
	}
	stages := sanitized.kept
	if len(stages) == 0 {
		stages, inferred, fallbackWarnings := a.demandChainFallback(ctx, "Value-chain inference returned no usable stages", fmt.Errorf("the model proposed no stages"), query, eventCh)
		category := demandFallbackCategory(query.Activity, stages)
		return stages, category, "Derived after empty inference.", "fallback", inferred, append(warnings, fallbackWarnings...)
	}
	for index := range stages {
		if len(stages[index].Operators) == 0 {
			stages[index].Operators = []string{strings.TrimSpace(stages[index].Name) + " plant operator manufacturer"}
		}
		stages[index].Products = normalizeStageProducts(stages[index].Products)
		if len(stages[index].Products) == 0 {
			stages[index].Products = knownStageProducts(query.Activity, stages[index].Name)
		}
	}
	category, categorySource, categoryWarnings := validateDemandCategory(inference.Category, inference.CategoryRationale, query.Activity, stages)
	warnings = append(warnings, categoryWarnings...)
	if category == "" {
		category = demandFallbackCategory(query.Activity, stages)
		if category != "" {
			categorySource = "fallback"
		}
	}
	categoryRationale := strings.TrimSpace(inference.CategoryRationale)
	if categorySource == "fallback" {
		categoryRationale = "Derived from the inferred consuming stages after category validation."
	}
	if categoryRationale == "" && category != "" {
		categoryRationale = "Derived from the inferred consuming stages."
	}
	emitAgentEvent(eventCh, models.EventToolResult, fmt.Sprintf("Inferred %d consuming stage(s): %s; category: %s", len(stages), stageNames(stages), category), "", "")
	return stages, category, categoryRationale, categorySource, true, warnings
}

// inferDemandChain preserves the test-facing signature; the orchestrator uses the
// category-aware variant above.
func (a *DemandChainAgent) inferDemandChain(ctx context.Context, query models.DemandChainQuery, eventCh chan<- models.AgentEvent) ([]models.ValueChainStage, bool, []string) {
	stages, _, _, _, inferred, warnings := a.inferDemandChainWithCategory(ctx, query, eventCh)
	return stages, inferred, warnings
}

// demandForbiddenCategoryTerms are end-use sectors and generic superclasses. They are exactly
// what an unconstrained model drifts to when asked for a product category ("coal" -> "fuel").
var demandForbiddenCategoryTerms = map[string]bool{
	"fuel": true, "fuels": true, "energy": true, "power": true,
	"chemical": true, "chemicals": true, "manufacturing": true, "industrial": true,
	"materials": true, "products": true, "commodities": true, "consumer": true,
	"automotive": true, "construction": true, "packaging": true, "healthcare": true,
	"agriculture": true, "food": true, "textile": true, "textiles": true,
	"retail": true, "logistics": true, "transport": true, "pharmaceuticals": true,
}

// demandCategoryRelatedTokens is the specificity guard: a category must share a real token with
// the product or one of its inferred stages, or be a known product-family relation. "polyurethanes"
// relates to TDI/foam; "fuel" relates to nothing in a coal value chain except the forbidden
// end-use sector we are rejecting.
var demandCategoryRelatedTokens = map[string][]string{
	"polyurethanes":           {"polyurethane", "isocyanate", "tdi", "mdi", "foam", "elastomer", "resin", "adhesive", "sealant", "coating"},
	"adhesives and sealants":  {"adhesive", "sealant", "polyurethane"},
	"solvents":                {"solvent", "acetate", "ester", "alcohol", "acetone", "methanol", "toluene", "acetic", "butanol", "ethanol"},
	"aromatics":               {"aromatic", "benzene", "toluene", "xylene", "styrene"},
	"coatings":                {"coating", "paint", "resin", "polyurethane"},
	"polyurethane elastomers": {"elastomer", "polyurethane", "isocyanate", "tdi"},
	"polyurethane foam":       {"foam", "polyurethane", "isocyanate", "tdi"},
	"polyurethane resins":     {"resin", "polyurethane", "isocyanate", "tdi"},
	"oleochemicals":           {"oleochemical", "fatty", "palm", "glycerin", "oil", "soap"},
	"surfactants":             {"surfactant", "detergent", "fatty", "soap", "linear alkyl benzene"},
	"plasticizers":            {"plasticizer", "phthalate", "ester"},
	"lubricants":              {"lubricant", "oil", "ester"},
	"biodiesel":               {"biodiesel", "fatty", "oil", "glycerin"},
	"coal products":           {"coal", "coke", "carbon"},
}

func demandCategoryFields(category string) []string {
	return strings.Fields(strings.ToLower(strings.TrimSpace(category)))
}

func demandCategoryLooksRelated(category, activity string, stages []models.ValueChainStage) bool {
	fields := demandCategoryFields(category)
	if len(fields) == 0 {
		return false
	}
	text := " " + strings.ToLower(strings.TrimSpace(activity)) + " "
	for _, stage := range stages {
		text += strings.ToLower(stage.Name) + " " + strings.Join(stage.Products, " ") + " " + strings.Join(stage.Operators, " ") + " "
	}
	for _, token := range fields {
		if len(token) >= 4 && strings.Contains(text, token) {
			return true
		}
	}
	for key, related := range demandCategoryRelatedTokens {
		if !strings.Contains(strings.ToLower(category), key) {
			continue
		}
		for _, token := range related {
			if strings.Contains(text, token) {
				return true
			}
		}
	}
	return false
}

// validateDemandCategory is the deterministic gate between the model's broad category and the
// fallback search. A category can only be used when it is specific, buyer-side and related to
// the product/stages; otherwise the run records the issue and falls back to the stage-derived
// category.
func validateDemandCategory(raw, rationale, activity string, stages []models.ValueChainStage) (string, string, []string) {
	category := strings.Join(demandCategoryFields(raw), " ")
	if category == "" {
		return "", "", nil
	}
	fields := demandCategoryFields(category)
	var warning []string
	reject := func(reason string) (string, string, []string) {
		warning = append(warning, fmt.Sprintf("LLM category %q rejected: %s; using the stage-derived category instead.", category, reason))
		return "", "rejected", warning
	}
	if len(fields) > 4 {
		return reject("more than four words")
	}
	if len(fields) == 1 && demandForbiddenCategoryTerms[fields[0]] {
		return reject("end-use or generic superclass")
	}
	if demandForbiddenCategoryTerms[fields[0]] {
		return reject("starts with an end-use or generic superclass")
	}
	for _, field := range fields {
		if field == "production" || field == "manufacturing" || field == "manufacturers" || field == "manufacturer" ||
			field == "suppliers" || field == "supplier" || field == "producers" || field == "producer" ||
			field == "synthesis" || field == "processing" {
			return reject("describes a process or company role, not a product family")
		}
	}
	if strings.EqualFold(category, activity) {
		return reject("repeats the product name")
	}
	for _, stage := range stages {
		if strings.EqualFold(category, strings.TrimSpace(stage.Name)) {
			return reject("repeats an inferred stage")
		}
	}
	if !demandCategoryLooksRelated(category, activity, stages) {
		return reject("no lexical or product-family relation to the product/stages")
	}
	return category, "model", warning
}

// demandFallbackCategory derives the category from the inferred stages when the model's label is
// absent or rejected. It is still only a search fallback, never a stage or a buyer.
func demandFallbackCategory(activity string, stages []models.ValueChainStage) string {
	activityText := strings.ToLower(strings.TrimSpace(activity))
	// Prefer a known product-family relation over the stage label. This is where a rejected
	// "coal -> fuel" category lands on "coal products", not "coal combustion".
	for _, category := range []string{"coal products", "polyurethanes", "solvents", "aromatics", "oleochemicals", "surfactants", "plasticizers", "lubricants", "biodiesel"} {
		for _, token := range demandCategoryRelatedTokens[category] {
			if token != "" && strings.Contains(activityText, token) {
				return category
			}
		}
	}
	for _, stage := range stages {
		categories := demandStageCategories(stage.Name, &stage, models.DemandChainQuery{Activity: activity})
		for _, category := range categories {
			category = strings.TrimSpace(category)
			if category != "" && !strings.EqualFold(category, activity) {
				return category
			}
		}
	}
	return ""
}

// reviewDemandChain is the adversarial first-pass chain review. It asks whether each proposed
// stage really consumes the product under the two-step definition, and applies only rejections and
// distance corrections to stages the model already proposed. It never adds a stage from the judge.
func (a *DemandChainAgent) reviewDemandChain(ctx context.Context, query models.DemandChainQuery, chain []models.ValueChainStage, eventCh chan<- models.AgentEvent) ([]models.ValueChainStage, *models.ValueChainReview, []string) {
	if len(chain) == 0 {
		return chain, nil, nil
	}
	var rows strings.Builder
	for _, stage := range chain {
		distance := stage.Distance
		if distance <= 0 {
			distance = models.StageDistanceDirect
		}
		fmt.Fprintf(&rows, "- %s | claimed distance=%d | rationale=%s | products=%s | operators=%s\n",
			stage.Name, distance, stage.Rationale, strings.Join(stage.Products, "; "), strings.Join(stage.Operators, "; "))
	}
	prompt := fmt.Sprintf(`Act as an adversarial reviewer of this buyer value chain for %q.
The buyer definition is strict: a stage must be a manufacturing process that consumes the product
itself as an input (distance 1), or consumes the output of a distance-1 process (distance 2).
Reject any stage where the claimed distance is wrong, where the rationale only says the stage
"uses" or "is associated with" the product without establishing the input, where the stage is a
supplier/producer of the product, or where it is an end-use market/consumer product rather than a
manufacturing process.
Example: TDI is not consumed directly by mattress manufacturing. A mattress plant consumes
polyurethane foam made from TDI, so it can only be distance 2; if it was claimed as distance 1,
correct it or reject it. A furniture retailer or a "mattress market" is not a manufacturing stage.
Return JSON with verdict (pass or revise), score 0-1, rejected_stages (name and reason),
distance_corrections (name and distance 1 or 2), missing_stages, and reason.
Chain:
%s`, query.Activity, rows.String())
	var review models.ValueChainReview
	if err := a.engine.GenerateStructured(ctx,
		"You are a skeptical industrial value-chain reviewer. Reject unsupported consumption claims and end-use sectors; never invent a replacement stage. Correct only distances that the rationale supports.",
		prompt, &review); err != nil {
		review.Verdict = "unavailable"
		review.Reason = err.Error()
		emitAgentEvent(eventCh, models.EventError, "Value-chain review unavailable: "+err.Error(), "demand_review", "")
		return chain, &review, []string{"Adversarial value-chain review was unavailable: " + err.Error()}
	}
	review.Verdict = strings.ToLower(strings.TrimSpace(review.Verdict))
	if review.Verdict == "" {
		review.Verdict = "unavailable"
	}
	var warnings []string
	rejected := make(map[string]string, len(review.RejectedStages))
	for _, rejection := range review.RejectedStages {
		if key := normalizeMarketName(rejection.Name); key != "" {
			rejected[key] = rejection.Reason
		}
	}
	if len(rejected) > 0 {
		kept := make([]models.ValueChainStage, 0, len(chain))
		for _, stage := range chain {
			if reason, ok := rejected[normalizeMarketName(stage.Name)]; ok {
				warnings = append(warnings, fmt.Sprintf("Adversarial chain review rejected stage %q: %s.", stage.Name, reason))
				continue
			}
			kept = append(kept, stage)
		}
		chain = kept
	}
	for _, correction := range review.DistanceCorrections {
		if correction.Distance != models.StageDistanceDirect && correction.Distance != models.StageDistanceDerived {
			continue
		}
		for index := range chain {
			if normalizeMarketName(chain[index].Name) == normalizeMarketName(correction.Name) {
				if chain[index].Distance != correction.Distance {
					warnings = append(warnings, fmt.Sprintf("Adversarial chain review corrected %q to distance %d.", chain[index].Name, correction.Distance))
					chain[index].Distance = correction.Distance
				}
				break
			}
		}
	}
	if len(chain) == 0 {
		fallback, _, fallbackWarnings := a.demandChainFallback(ctx, "Adversarial review rejected every inferred stage", fmt.Errorf("no stage survived review"), query, eventCh)
		review.Verdict = "revise"
		return fallback, &review, append(warnings, fallbackWarnings...)
	}
	if len(rejected) > 0 || len(review.DistanceCorrections) > 0 {
		review.Verdict = "revise"
	}
	if len(review.MissingStages) > 0 {
		warnings = append(warnings, fmt.Sprintf("Adversarial chain review suggests missing stage hypotheses: %s (not added automatically).", strings.Join(review.MissingStages, ", ")))
	}
	emitAgentEvent(eventCh, models.EventThinking,
		fmt.Sprintf("Adversarial chain review: %s (%.2f), %d rejected stage(s), %d correction(s)", review.Verdict, review.Score, len(rejected), len(review.DistanceCorrections)),
		"demand_review", "")
	return chain, &review, warnings
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
		stage.Products = normalizeStageProducts(stage.Products)
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
				term:  "largest manufacturers of " + demandStageSearchLabel(stageName),
				stage: stage.Name,
			})
			for _, product := range stage.Products {
				lanes = append(lanes, demandLane{
					term:  "largest manufacturers of " + product,
					stage: stage.Name,
				})
			}
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
		// the product to resell it, which is real demand. They are a separate
		// channel category rather than a manufacturing stage, and two lanes keep
		// the category from being lost when the query budget is tight.
		demandLane{term: "distributor wholesaler of", stage: demandDistributionStageName},
		demandLane{term: "authorised distributor wholesale supplier of", stage: demandDistributionStageName},
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

func demandStageSearchLabel(stage string) string {
	label := strings.ToLower(strings.TrimSpace(stage))
	for _, suffix := range []string{" production", " manufacturing", " processing"} {
		if strings.HasSuffix(label, suffix) {
			label = strings.TrimSpace(strings.TrimSuffix(label, suffix))
			break
		}
	}
	if label == "" {
		return strings.ToLower(strings.TrimSpace(stage))
	}
	return label
}

func normalizeStageProducts(products []string) []string {
	out := make([]string, 0, 4)
	seen := make(map[string]bool)
	for _, product := range products {
		product = strings.TrimSpace(product)
		key := strings.ToLower(product)
		if product == "" || len(product) > 100 || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, product)
		if len(out) == 4 {
			break
		}
	}
	return out
}

func knownStageProducts(activity, stage string) []string {
	activity = strings.ToLower(strings.TrimSpace(activity))
	stage = strings.ToLower(strings.TrimSpace(stage))
	if strings.Contains(activity, "acetic acid") && strings.Contains(stage, "solvent") {
		return []string{"ethyl acetate", "butyl acetate", "propyl acetate", "acetate esters"}
	}
	return nil
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
		"distributor wholesaler of":  true,
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
	// Stage-carrying lanes run before generic product-shaped lanes: a budget-limited run must
	// still tie at least one lane to each consuming stage the chain inferred.
	addOptional := func(lanes []demandLane) {
		for _, lane := range lanes {
			if optionalBudget == 0 {
				return
			}
			if mandatory[lane.term] {
				continue
			}
			selected = append(selected, lane)
			optionalBudget--
		}
	}
	var stageLanes, otherLanes []demandLane
	for _, lane := range lanes {
		if lane.stage != "" {
			stageLanes = append(stageLanes, lane)
		} else {
			otherLanes = append(otherLanes, lane)
		}
	}
	// Operator vocabulary ("biodiesel plant") is a more precise identity signal than a generic
	// "largest manufacturers of ..." query, so within the stage reserve the shorter lane runs
	// first when the budget cannot carry both.
	sort.SliceStable(stageLanes, func(i, j int) bool { return len(stageLanes[i].term) < len(stageLanes[j].term) })
	addOptional(stageLanes)
	addOptional(otherLanes)
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
	// Prefer the shared canonical-name scorer: company pages commonly put the
	// capability tagline in the longest title segment ("Acme Foam | Polyurethane
	// Foam Manufacturer"), so longest-segment selection would promote the
	// descriptor and starve the stage roster.
	if canonical := marketCanonicalName(name, domain); canonical != "" {
		name = canonical
	} else if strings.Contains(name, "|") {
		// Fallback for a title shape the canonical scorer cannot reduce.
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
	articleShaped := marketDocumentTitle(hit.Title) || marketHeadlineTitle(hit.Title) ||
		marketPublisherSuffix(hit.Title) || isDemandDocumentTitle(hit.Title) || isDemandBaitTitle(hit.Title) ||
		isGenericMarketTitle(hit.Title)
	if !articleShaped && !hasCompanyIdentitySignal(hit.Title, domain) {
		// The title alone looks like a company but the snippet/headline context is a report
		// or article. Keep it as a lead for extraction rather than promoting the title.
		articleShaped = isDemandDocumentTitle(hit.Title + " " + hit.Snippet)
	}
	if articleShaped {
		// The page title is a document/headline, not a company name. Keep the page as a lead so
		// its *content* can supply company names, but never use its title as the company.
		name = domain
	}
	if classifyDiscoverySource(hit) != sourceOperator || sourceTier == "news" || sourceTier == "social" || sourceOverride != "" || articleShaped || isGenericMarketTitle(name) || isDemandBaitTitle(name) || !hasCompanyIdentitySignal(name, domain) {
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
			Date: hit.Date, RetrievedAt: time.Now(), Status: "candidate",
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
	// A page can be read for company names without being promoted as a company. Dedupe the
	// leads first, then spend the bounded read budget on the densest pages: research reports,
	// directories and trade pages that list companies are exactly the pages buyer research must
	// read, even though they can never be a buyer themselves.
	deduped := make([]models.MarketLandscapeEvidence, 0, len(leads))
	seenLeadURL := make(map[string]bool, len(leads))
	for _, lead := range leads {
		key := demandCanonicalURL(lead.URL)
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(lead.URL))
		}
		if key == "" || seenLeadURL[key] {
			continue
		}
		seenLeadURL[key] = true
		deduped = append(deduped, lead)
	}
	enrichable := deduped
	// Reserve the bounded extraction/read budget for stage-tied leads first. A tender or
	// research-page lead is valuable, but if the first sixty leads are all general demand pages,
	// a consuming stage can be starved before verification ever sees its operators.
	hasStage := func(lead models.MarketLandscapeEvidence) bool {
		if stage := strings.TrimSpace(laneStages[lead.Lane]); stage != "" {
			return true
		}
		for term, stage := range laneStages {
			if stage != "" && strings.Contains(lead.Lane, term) {
				return true
			}
		}
		return false
	}
	sort.SliceStable(enrichable, func(i, j int) bool {
		left, right := hasStage(enrichable[i]), hasStage(enrichable[j])
		if left != right {
			return left
		}
		return marketLeadReadPriority(enrichable[i].SourceTier, enrichable[i].Title) < marketLeadReadPriority(enrichable[j].SourceTier, enrichable[j].Title)
	})
	if len(enrichable) > query.MaxLeadsEnriched {
		enrichable = enrichable[:query.MaxLeadsEnriched]
	}
	ordered := append([]models.MarketLandscapeEvidence(nil), enrichable...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := hasStage(ordered[i]), hasStage(ordered[j])
		if left != right {
			return left
		}
		return marketLeadReadPriority(ordered[i].SourceTier, ordered[i].Title) < marketLeadReadPriority(ordered[j].SourceTier, ordered[j].Title)
	})
	if len(ordered) > demandMaxLeadPages {
		ordered = ordered[:demandMaxLeadPages]
	}
	readable := make([]models.MarketLandscapeEvidence, 0, len(ordered))
	targets := make([]string, 0, len(ordered))
	for _, lead := range ordered {
		if marketLeadReadable(lead.SourceTier, lead.Title) {
			readable = append(readable, lead)
			targets = append(targets, lead.URL)
		}
	}
	leadByURL := make(map[string]models.MarketLandscapeEvidence, len(readable))
	for _, lead := range readable {
		leadByURL[demandCanonicalURL(lead.URL)] = lead
	}
	// Enrichment is bounded-concurrent through the engine's batch scraper —
	// the same scrape semaphore every other scraping stage shares — so a deep
	// run's dozens of lead pages do not crawl one by one. Outcomes arrive in
	// submission order, so the lead order is stable.
	outcomes := a.engine.ScrapeCachedBatch(ctx, targets)
	contentByURL := make(map[string]string, len(outcomes))
	pagesEnriched := 0
	for _, outcome := range outcomes {
		if outcome.Err != nil || outcome.Result == nil {
			continue
		}
		content := strings.TrimSpace(outcome.Result.Content)
		if content == "" {
			continue
		}
		lead := leadByURL[demandCanonicalURL(outcome.URL)]
		limit := marketCompanyPageExcerpt
		if marketRosterSourceTier(lead.SourceTier) || marketTitleHintsRoster(lead.Title) {
			limit = marketRosterPageExcerpt
		}
		contentByURL[demandCanonicalURL(outcome.URL)] = marketPageExcerpt(content, limit)
		pagesEnriched++
		emitAgentEvent(eventCh, models.EventToolResult,
			fmt.Sprintf("Read %s (%s) for buyer names: %d characters", hostname(outcome.URL), lead.SourceTier, len(content)),
			"scrape", outcome.URL)
	}
	enrichedLeads := make([]models.MarketLandscapeEvidence, 0, len(enrichable))
	for _, lead := range enrichable {
		leadCopy := lead
		if content, ok := contentByURL[demandCanonicalURL(lead.URL)]; ok {
			leadCopy.Content = content
			leadCopy.Status = "page read"
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
			prompt := fmt.Sprintf("Extract every actual company or facility operator explicitly supported by these leads as a consumer, buyer, importer, or processor of %q. %sThese leads include company pages, directories, news articles and research reports. A report or article is a valid source for the companies it names or lists: read its page content and extract every company named there, including each row of a key-players or buyer table. Name the operator, never the page: a report title, article headline, publisher name, site name, region, project or generic category is not a company. Exclude publishers, list titles, market-research vendors, regions, projects, and generic categories. Return the company's legal name — never a news headline, page title, or project name. Set producer to true when the page describes the company as a producer, manufacturer, or supplier of %q itself rather than a consumer of it. Return a JSON array with name, country, reason, evidence_url as a string, and producer as a boolean.\nActivity: %s\nLeads:\n%s", query.Activity, geoPreference, query.Activity, query.Activity, strings.Join(leadText, "\n"))
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
		company.Status = demandStatus(company)
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
// demandVerificationTier is the buyer-side verification tier rule: only an operator-owned or
// regulatory source can verify a buyer. Directory, trade, news, research, publisher and social
// pages are corroboration only.
func demandVerificationTier(tier string) bool {
	return tier == "official" || tier == "regulatory"
}

func demandVerificationEvidence(hit models.SearchResult, company, activity, purpose string) (models.MarketLandscapeEvidence, bool) {
	if !demandCompanyTitleFor(company, activity) {
		return models.MarketLandscapeEvidence{}, false
	}
	domain := hostname(hit.Link)
	tier := marketSourceTier(domain)
	override := demandSourceOverride(domain)
	if override != "" && override != "aggregator" {
		// Publishers, consultancies and institutions are commentary — never
		// consumption evidence. Tender-portal domains are NOT overridden
		// here: a tender listing hosted on a portal that names the buyer is
		// real claim evidence about that buyer (the aggregators themselves
		// are stopped from masquerading as companies at extraction).
		tier = override
	}
	if !demandVerificationTier(tier) || !mentionsMarketCompany(hit, company) {
		return models.MarketLandscapeEvidence{}, false
	}
	// The shared tier helper calls any unrecognised domain "official". For an ordinary
	// publisher or blog that is too generous, so an official source must also look like the
	// company's own domain. Aggregator-hosted tender records are the explicit exception.
	if tier == "official" && override == "" && !demandCompanyOwnsDomain(company, domain) {
		return models.MarketLandscapeEvidence{}, false
	}
	if !hasDemandSignalEvidence(hit) && !hasMarketActivityEvidence(hit, activity) {
		return models.MarketLandscapeEvidence{}, false
	}
	claim := demandClaimFromText(hit)
	if override == "aggregator" && claim != models.DemandClaimTender && claim != models.DemandClaimImport {
		// A directory/portal listing is a company page only when it carries a transaction record;
		// its generic "company profile" text is corroboration, not verification.
		return models.MarketLandscapeEvidence{}, false
	}
	return models.MarketLandscapeEvidence{
		URL: hit.Link, Title: hit.Title, Snippet: hit.Snippet, Lane: "demand verification",
		ClaimType: claim, SourceTier: tier,
		Date: hit.Date, RetrievedAt: time.Now(), Status: purpose,
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
