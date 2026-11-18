package agent

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

const (
	marketMaxDiscoveryQueries = 24
	// Company verification gets 100 searches now that key-person discovery no longer consumes
	// a reserve; the extra budget goes to company identity and facility evidence.
	marketMaxVerificationQueries = 100
	marketMaxResults             = 10
	marketMaxCandidates          = 48
	marketMinDiscoveryFindings   = 6
	marketCoverageMinimum        = 6
	// Verification spends three searches per company. The query budget is the
	// real cap; this bound only stops an extraction flood from queueing work
	// the budget cannot pay for.
	marketMaxVerifiedCompanies = 32
	// Lead enrichment reads up to this many pages, densest first, through the
	// engine's bounded batch scraper.
	marketMaxLeadPages = 30
	// Bounded concurrency for the seller pipeline's network-bound stages:
	// lane searches, LLM extraction batches and per-candidate verification
	// searches each run at their cap; results land in pre-assigned slots so
	// ordering never depends on completion order.
	marketLaneSearchConcurrency   = 4
	marketExtractionConcurrency   = 3
	marketVerificationConcurrency = 4
	marketExtractionBatchSize     = 4
	// A company page needs its opening lines; a report or directory page needs
	// the competitive-landscape table, which sits further down the page.
	marketCompanyPageExcerpt = 3000
	marketRosterPageExcerpt  = 6000
)

// MarketLandscapeAgent owns the additive, vector-free market workflow.
type MarketLandscapeAgent struct {
	engine *Engine
}

func marketLandscapeGeography(query models.MarketLandscapeQuery) string {
	locations := append(append([]string{}, query.Countries...), query.Regions...)
	cleaned := make([]string, 0, len(locations))
	seen := make(map[string]bool)
	for _, location := range locations {
		location = strings.TrimSpace(location)
		key := strings.ToLower(location)
		if location != "" && !seen[key] {
			seen[key] = true
			cleaned = append(cleaned, location)
		}
	}
	if len(cleaned) == 0 {
		return "Global"
	}
	return strings.Join(cleaned, ", ")
}

func NewMarketLandscapeAgent(engine *Engine) *MarketLandscapeAgent {
	return &MarketLandscapeAgent{engine: engine}
}

// Research performs broad discovery, lead extraction, bounded verification, and
// deterministic report assembly. Existing search modes are not involved.
func (a *MarketLandscapeAgent) Research(ctx context.Context, researchID string, query models.MarketLandscapeQuery, eventCh chan<- models.AgentEvent) (*models.MarketLandscapeReport, error) {
	query.Activity = strings.TrimSpace(query.Activity)
	if query.Activity == "" {
		return nil, fmt.Errorf("market activity is required")
	}
	if strings.TrimSpace(query.Objective) == "" {
		query.Objective = query.Activity
	}
	if query.MaxCompanies <= 0 || query.MaxCompanies > 80 {
		query.MaxCompanies = 48
	}

	report := &models.MarketLandscapeReport{
		ResearchID: researchID, Objective: query.Objective, Activity: query.Activity,
		Geography: marketLandscapeGeography(query),
		Lanes:     marketLanes(query), Iterations: 2, StartedAt: time.Now(),
	}
	qcPolicy, qcPromptSource := marketQCPolicy(query)
	report.QCPrompt = qcPolicy
	report.QCPromptSource = qcPromptSource
	if query.IncludeContacts {
		report.Warnings = append(report.Warnings, "Public business-contact collection is reserved for a later bounded pass; use the per-person Fetch contact details action for RocketReach enrichment.")
	}
	// Activity resolution: the curated graph decides the research path when it
	// knows the activity (directly or through a model-confirmed alias); otherwise
	// the model plans the path and the activity is reported as unmodelled.
	resolution := resolveActivity(ctx, a.engine, query.Activity, eventCh)
	if resolution.matched() {
		resolution = chooseOntologyFamily(ctx, a.engine, resolution, marketLandscapeGeography(query), eventCh)
	}
	report.Warnings = append(report.Warnings, resolution.Warnings...)
	// The trade-DB lane is the first step after the ontology check: its
	// queries and answers land in the console log before any web lane spends
	// a search, and the first-party sections are assembled before the long
	// web pipeline runs.
	a.attachTradePartners(ctx, query, report, eventCh)
	category, categoryRationale, categorySource, categoryWarnings := a.marketCategoryFromResolution(ctx, resolution, query, eventCh)
	report.ProductCategory = category
	report.CategoryRationale = categoryRationale
	report.CategorySource = categorySource
	report.Warnings = append(report.Warnings, categoryWarnings...)
	report.CategoryLanes = marketCategoryLanes(category)
	// The per-company verification template is reviewed once, before any
	// lane runs, and the surviving purposes are shared by both verification
	// stages (category and main).
	_, purposes, purposeReviews, purposeReviewWarning := a.reviewMarketQueryBatch(ctx, "verification", nil, marketVerificationPurposes(), category, query, eventCh)
	report.QueryReviews = append(report.QueryReviews, purposeReviews...)
	if purposeReviewWarning != "" {
		report.Warnings = append(report.Warnings, purposeReviewWarning)
	}
	categoryResults, categoryQueries, categoryHits, categoryVerificationQueries, categoryQueryReviews, categoryLaneWarnings := a.researchCategoryLane(ctx, query, category, purposes, eventCh, &report.SearchQueries)
	report.CategoryResults = categoryResults
	report.CategoryQueries = categoryQueries
	report.SearchHits += categoryHits
	report.QueryReviews = append(report.QueryReviews, categoryQueryReviews...)
	report.Warnings = append(report.Warnings, categoryLaneWarnings...)

	companies := make(map[string]*models.MarketLandscapeCompany)
	var leads []models.MarketLandscapeEvidence
	leadURLs := make(map[string]bool)
	queriesUsed := 0
	processHits := func(hits []models.SearchResult, lane string) {
		for _, hit := range hits {
			candidate := marketCandidate(hit, lane, query)
			if candidate == nil {
				continue
			}
			if excludedMarketDomain(candidate.Domain, query.Exclusions) {
				candidate.Evidence[0].Status = "excluded"
				report.Appendix = append(report.Appendix, candidate.Evidence...)
				continue
			}
			if candidate.EvidenceType == "discovery-lead" {
				evidence := candidate.Evidence[0]
				if !leadURLs[evidence.URL] {
					leadURLs[evidence.URL] = true
					leads = append(leads, evidence)
					report.Appendix = append(report.Appendix, evidence)
				}
				continue
			}
			if len(companies) >= marketMaxCandidates {
				continue
			}
			mergeMarketCompany(companies, candidate)
		}
	}
	emitAgentEvent(eventCh, models.EventThinking, "Planning market discovery lanes", "", "")
	// Lane searches are bounded-concurrent and order-preserving: each lane's
	// hits land in its slot, so the leads, the appendix and the company
	// merging below never depend on completion order.
	type laneSearch struct {
		hits  []models.SearchResult
		err   error
		trace models.MarketSearchTrace
	}
	discoverySearches := marketDiscoverySearchesWithTerms(query, resolution.searchTerms())
	// Adversarial query review: every composed lane is judged for aptness and
	// economy before it spends a search — degenerate lanes are rewritten or
	// dropped here, never after their hits pollute the candidate pool.
	discoverySearches, _, discoveryReviews, reviewWarning := a.reviewMarketQueryBatch(ctx, "discovery", discoverySearches, nil, category, query, eventCh)
	report.QueryReviews = append(report.QueryReviews, discoveryReviews...)
	if reviewWarning != "" {
		report.Warnings = append(report.Warnings, reviewWarning)
	}
	laneResults := make([]laneSearch, len(discoverySearches))
	searchSemaphore := make(chan struct{}, marketLaneSearchConcurrency)
	var searchGroup sync.WaitGroup
	for laneIndex, search := range discoverySearches {
		if laneIndex >= marketMaxDiscoveryQueries {
			break
		}
		searchGroup.Add(1)
		go func(search marketSearchSpec, slot int) {
			defer searchGroup.Done()
			searchSemaphore <- struct{}{}
			defer func() { <-searchSemaphore }()
			if err := ctx.Err(); err != nil {
				laneResults[slot].err = err
				return
			}
			emitAgentEvent(eventCh, models.EventToolCall, "Searching "+search.Lane, "search", search.Query)
			hits, err := a.engine.SearchCached(ctx, search.Query, marketMaxResults)
			laneResults[slot].hits, laneResults[slot].err = hits, err
			laneResults[slot].trace = models.MarketSearchTrace{
				Phase: "discovery", Lane: search.Lane, Query: search.Query,
				Status: marketSearchStatus(err), Hits: len(hits),
			}
		}(search, laneIndex)
	}
	searchGroup.Wait()
	for laneIndex, search := range discoverySearches {
		if laneIndex >= marketMaxDiscoveryQueries {
			break
		}
		queriesUsed++
		hits, err := laneResults[laneIndex].hits, laneResults[laneIndex].err
		report.SearchQueries = append(report.SearchQueries, laneResults[laneIndex].trace)
		report.SearchHits += len(hits)
		if err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s search failed: %v", search.Query, err))
			continue
		}
		processHits(hits, search.Lane)
	}

	report.LeadsReviewed = len(leads)
	report.QueriesUsed = queriesUsed + categoryQueries
	emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Extracting companies from %d discovery lead(s)", len(leads)), "", "")
	resolved, verificationQueries, extracted, rejected, enriched, _, _, _, err := a.verifyMarketLeads(ctx, leads, query, purposes, eventCh, &report.SearchQueries)
	report.QueriesUsed += verificationQueries + categoryVerificationQueries
	report.LeadsExtracted = extracted
	report.LeadsRejected = rejected
	report.PagesEnriched = enriched
	if err != nil {
		report.Warnings = append(report.Warnings, "Lead extraction could not complete: "+err.Error())
	} else {
		report.LeadsResolved = len(resolved)
		for _, company := range resolved {
			mergeMarketCompany(companies, company)
		}
	}

	classifyMarketCompanies(companies, query)
	a.reviewMarketReport(ctx, query, companies, report, eventCh)

	report.CandidatesFound = len(companies)
	for _, company := range companies {
		company.Status = marketStatus(company)
		if company.Status == "verified" || company.Status == "corroborated" {
			report.Results = append(report.Results, *company)
		} else {
			report.Appendix = append(report.Appendix, company.Evidence...)
		}
	}
	sort.Slice(report.Results, func(i, j int) bool {
		if report.Results[i].Confidence == report.Results[j].Confidence {
			return report.Results[i].Name < report.Results[j].Name
		}
		return report.Results[i].Confidence > report.Results[j].Confidence
	})
	if len(report.Results) > query.MaxCompanies {
		for _, company := range report.Results[query.MaxCompanies:] {
			report.Appendix = append(report.Appendix, company.Evidence...)
		}
		report.Results = report.Results[:query.MaxCompanies]
	}
	if len(report.Results) == 0 {
		report.Warnings = append(report.Warnings, "No company met the verification threshold; discovery leads remain in the appendix.")
	}
	// The verified rows carry their aggregated customs profile: cheap
	// (~110 ms per company warm, cached), bounded by the trade-partner cap,
	// and fail-soft — a miss leaves the row unchanged.
	enrichVerifiedTradeProfiles(ctx, a.engine, report.Results, eventCh)
	report.Groups = marketGroupsFor(report.Results, query)
	report.Coverage = marketCoverageFor(companies, query)
	if !report.Coverage.Pass {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Market coverage is weak: %d in-scope company(ies) identified; target is at least %d. Global/cross-border findings are listed separately and are not counted.", report.Coverage.Companies, marketCoverageMinimum))
	}
	if report.Coverage.Global > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d global/cross-border company(ies) have no evidence of a plant, office or operation in the requested geography; they remain visible in the Global / cross-border section and are excluded from local coverage.", report.Coverage.Global))
	}
	report.CompletedAt = time.Now()
	if exports := a.engine.ExportMarketLandscape(ctx, report); len(exports) > 0 {
		report.Exports = exports
	}
	emitAgentEvent(eventCh, models.EventComplete, fmt.Sprintf("Seller Landscape complete: %d verified result(s), %d candidate(s)", len(report.Results), report.CandidatesFound), "", "")
	return report, nil
}

// reviewMarketReport is the producer/seller QC judge. It challenges company identity and relevance,
// applies only rejections of names already in the run, and records the deterministic coverage.
func (a *MarketLandscapeAgent) reviewMarketReport(ctx context.Context, query models.MarketLandscapeQuery, companies map[string]*models.MarketLandscapeCompany, report *models.MarketLandscapeReport, eventCh chan<- models.AgentEvent) models.MarketLandscapeReview {
	review := models.MarketLandscapeReview{}
	classifyMarketCompanies(companies, query)
	qcPolicy, qcPromptSource := marketQCPolicy(query)
	report.QCPrompt = qcPolicy
	report.QCPromptSource = qcPromptSource
	submitted := make([]*models.MarketLandscapeCompany, 0, len(companies))
	for _, company := range companies {
		submitted = append(submitted, company)
	}
	sort.Slice(submitted, func(i, j int) bool {
		return submitted[i].Name < submitted[j].Name
	})
	var rows strings.Builder
	for index, company := range submitted {
		if index >= 50 {
			break
		}
		fmt.Fprintf(&rows, "- %s | role=%s | presence=%s | status=%s | country=%s | confidence=%.2f\n", company.Name, company.Role, company.Presence, marketStatus(company), company.Country, company.Confidence)
		evidenceLimit := len(company.Evidence)
		if evidenceLimit > 2 {
			evidenceLimit = 2
		}
		for _, evidence := range company.Evidence[:evidenceLimit] {
			fmt.Fprintf(&rows, "  source: %s | %s | tier=%s | %s\n", evidence.Title, evidence.URL, evidence.SourceTier, truncateMarketText(evidence.Snippet, 220))
		}
	}
	prompt := fmt.Sprintf(`Adversarially review this Seller Landscape for %q.

QC POLICY SUPPLIED BY THE OPERATOR:
%s

The landscape should identify at least %d separate companies; report missing coverage if it does
not. Return JSON with verdict (pass or revise), score 0-1, rejected (name and reason), missing, and
reason. Never add a company that is not in the supplied list.
Companies:
%s`, query.Activity, qcPolicy, marketCoverageMinimum, rows.String())
	if err := a.engine.GenerateStructured(ctx,
		"You are a strict Seller Landscape reviewer. Reject publishers, directories, people, categories, articles, product pages and unrelated companies. For a process activity, a manufacturer that operates the process for its own products is a valid producer/seller; do not require it to sell the process as a service. Never invent a company or a replacement.",
		prompt, &review); err != nil {
		review.Verdict = "unavailable"
		review.Reason = err.Error()
		review.Verdicts, review.Passed, review.Failed = marketQCVerdicts(submitted, nil, "QC review unavailable: "+err.Error())
		report.Warnings = append(report.Warnings, "Producer QC review unavailable: "+err.Error())
		review.Coverage = marketCoverageFor(companies, query)
		report.Coverage = review.Coverage
		report.Reviews = append(report.Reviews, review)
		return review
	}
	review.Verdict = strings.ToLower(strings.TrimSpace(review.Verdict))
	if review.Verdict == "" {
		review.Verdict = "unavailable"
	}
	rejected := make(map[string]string, len(review.Rejected))
	for _, rejection := range review.Rejected {
		if key := normalizeMarketName(rejection.Name); key != "" {
			rejected[key] = rejection.Reason
		}
	}
	for key, reason := range rejected {
		for companyKey, company := range companies {
			if normalizeMarketName(company.Name) != key {
				continue
			}
			report.Appendix = append(report.Appendix, company.Evidence...)
			report.Warnings = append(report.Warnings, fmt.Sprintf("Producer QC removed %s: %s.", company.Name, reason))
			delete(companies, companyKey)
			break
		}
	}
	if len(rejected) > 0 {
		review.Verdict = "revise"
	}
	if len(review.Missing) > 0 {
		review.Verdict = "revise"
	}
	qcUnavailableReason := ""
	if review.Verdict == "unavailable" {
		qcUnavailableReason = "QC reviewer returned no verdict."
	}
	review.Verdicts, review.Passed, review.Failed = marketQCVerdicts(submitted, rejected, qcUnavailableReason)
	review.Coverage = marketCoverageFor(companies, query)
	report.Coverage = review.Coverage
	report.Reviews = append(report.Reviews, review)
	emitAgentEvent(eventCh, models.EventThinking,
		fmt.Sprintf("Producer QC review: %s (%.2f), %d rejection(s), coverage %d/%d", review.Verdict, review.Score, len(rejected), review.Coverage.Companies, review.Coverage.Minimum),
		"market_qc", "")
	return review
}

// inferMarketCategory asks the first pass for the immediate downstream category that consumes the
// product. The category is validated by the same deterministic gate as the buyer-side category and
// is used only to broaden weak discovery, never as a company or a result.
func (a *MarketLandscapeAgent) inferMarketCategory(ctx context.Context, query models.MarketLandscapeQuery, eventCh chan<- models.AgentEvent) (string, string, string, []string) {
	var inference models.MarketLandscapeInference
	prompt := fmt.Sprintf(`Identify the immediate downstream product family or industry category that consumes %q for a Seller Landscape. It must be specific enough to search, one to four words, and not an end-use sector or generic superclass. If the activity is a process or service without a clearly named product, return an empty category and explain that a product must be specified. When the activity is a process that handles a named product family (blending, refining, storage, packing), name the family the process handles, not a neighbour the words merely resemble: a family that shares a word with the activity but is a different chemistry is wrong even when its phrasing is good. Examples: toluene diisocyanate -> polyurethanes; acetic acid -> solvents or acetates; toluene -> aromatics; n-butanol -> solvents; linear alkyl benzene -> surfactants; coal -> coal products, never fuel or energy. Return JSON with category and category_rationale.`, query.Activity)
	if err := a.engine.GenerateStructured(ctx,
		"You identify buyer-side product categories for industrial market research. Return a specific product family the activity handles or feeds — never a bare end-use sector such as fuel, energy, chemicals, industrial or automotive.",
		prompt, &inference); err != nil {
		category := demandFallbackCategory(query.Activity, nil)
		return category, "Derived from the product family after category inference failed.", "fallback", []string{"Market category inference failed: " + err.Error()}
	}
	category, source, warnings := validateDemandCategory(inference.Category, inference.CategoryRationale, query.Activity, nil)
	if category == "" {
		category = demandFallbackCategory(query.Activity, nil)
		if category != "" {
			source = "fallback"
		}
	}
	rationale := strings.TrimSpace(inference.CategoryRationale)
	if source == "fallback" {
		rationale = "Derived from the product family after category validation."
	}
	emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Market category: %s (%s)", category, source), "market_category", "")
	return category, rationale, source, warnings
}

// marketCategoryFromResolution turns the activity resolution into the category
// the Seller Landscape broadens on. A graph match (direct or alias) is
// authoritative and costs no inference call; an unmodelled activity keeps the
// model's own inference, which is branch (2.2) of the resolution flow.
func (a *MarketLandscapeAgent) marketCategoryFromResolution(ctx context.Context, resolution activityResolution, query models.MarketLandscapeQuery, eventCh chan<- models.AgentEvent) (string, string, string, []string) {
	if resolution.matched() {
		if category := resolution.primaryCategory(); category != "" {
			source := string(resolution.Path)
			emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Market category: %s (%s)", category, source), "market_category", "")
			return category, resolution.categoryRationale(), source, nil
		}
	}
	return a.inferMarketCategory(ctx, query, eventCh)
}

func marketCategoryLanes(category string) []string {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil
	}
	return []string{category + " manufacturers", category + " companies"}
}

type marketSearchSpec struct {
	Lane  string
	Query string
}

func marketSearchStatus(err error) string {
	if err != nil {
		return "failed"
	}
	return "executed"
}

func marketSearchPhrase(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.ContainsAny(value, `"()`) {
		return value
	}
	return `"` + value + `"`
}

func marketSearchGeographies(query models.MarketLandscapeQuery) []string {
	if len(query.Countries) > 0 {
		return []string{strings.Join(query.Countries, " ")}
	}
	if len(query.Regions) > 0 {
		return append([]string(nil), query.Regions...)
	}
	return []string{""}
}

// marketActivitySearchTerms is the operator's own wording: the base term every
// discovery lane starts from. Product-family expansions ("lubricant blending" for
// "oil blending") come from the value-chain graph through
// marketDiscoverySearchesWithTerms, never from a table here — a phrase the graph
// does not know is a phrase the run should not invent.
func marketActivitySearchTerms(activity string) []string {
	return []string{strings.TrimSpace(activity)}
}

func marketDiscoverySearches(query models.MarketLandscapeQuery) []marketSearchSpec {
	return marketDiscoverySearchesWithTerms(query, nil)
}

// marketDiscoverySearchesWithTerms composes the discovery lanes, optionally
// guided by the value-chain graph's indexed phrases. The extra terms are capped:
// the operator's own wording keeps the lane budget, and the graph only adds the
// phrasing the trade actually indexes ("lubricant blending" for "oil blending").
func marketDiscoverySearchesWithTerms(query models.MarketLandscapeQuery, ontologyTerms []string) []marketSearchSpec {
	terms := marketActivitySearchTerms(query.Activity)
	for _, term := range ontologyTerms {
		term = strings.TrimSpace(term)
		if term == "" || len(terms) >= 3 {
			break
		}
		duplicate := false
		for _, existing := range terms {
			if strings.EqualFold(existing, term) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			terms = append(terms, term)
		}
	}
	geographies := marketSearchGeographies(query)
	searches := make([]marketSearchSpec, 0, marketMaxDiscoveryQueries)
	seen := make(map[string]bool)
	appendSearch := func(lane, search string) {
		search = strings.TrimSpace(search)
		if search == "" || seen[search] || len(searches) >= marketMaxDiscoveryQueries {
			return
		}
		seen[search] = true
		searches = append(searches, marketSearchSpec{Lane: lane, Query: search})
	}

	// Use a small set of natural phrases for each product/process variant. The
	// geography is attached once, and regional scopes remain separate. The role
	// words are the seller roles the report groups — manufacturer, plant (the
	// production/blending site), distributor, wholesaler — plus the trade-record
	// vocabulary; "company"/"facility" were dropped because they qualify almost
	// any page and bought no marginal recall.
	for _, term := range terms {
		for _, geography := range geographies {
			suffix := strings.TrimSpace(geography)
			if suffix != "" {
				suffix = " " + suffix
			}
			// A graph phrase may already carry the lane's role word ("blending
			// plant"); pairing it with the same role would repeat a word, so that
			// lane is composed from the operator's own term instead.
			if !marketTermCarriesRole(term, "manufacturer") {
				appendSearch("manufacturer", marketSearchPhrase(term)+" manufacturer"+suffix)
			}
			if !marketTermCarriesRole(term, "plant") {
				appendSearch("plant", marketSearchPhrase(term)+" plant"+suffix)
			}
		}
	}

	primary := terms[0]
	for _, site := range []string{"ensun.io", "revenuebase.ai"} {
		appendSearch("site:"+site, "site:"+site+" "+marketSearchPhrase(primary))
	}
	for _, geography := range geographies {
		suffix := strings.TrimSpace(geography)
		if suffix != "" {
			suffix = " " + suffix
		}
		appendSearch("distributor", marketSearchPhrase(primary)+" distributor"+suffix)
		appendSearch("wholesaler", marketSearchPhrase(primary)+" wholesaler"+suffix)
		appendSearch("exporter", marketSearchPhrase(primary)+" exporter"+suffix)
		appendSearch("shipment", marketSearchPhrase(primary)+" shipment"+suffix)
	}
	return searches
}

// marketTermCarriesRole reports whether a search term already names the lane's role
// word, in which case appending that role again would repeat a word.
func marketTermCarriesRole(term, role string) bool {
	for _, word := range strings.Fields(strings.ToLower(term)) {
		if word == role {
			return true
		}
	}
	return false
}

func (a *MarketLandscapeAgent) researchCategoryLane(ctx context.Context, query models.MarketLandscapeQuery, category string, purposes []string, eventCh chan<- models.AgentEvent, trace *[]models.MarketSearchTrace) ([]models.MarketLandscapeCompany, int, int, int, []models.MarketQueryReview, []string) {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil, 0, 0, 0, nil, nil
	}
	categoryQuery := query
	categoryQuery.Activity = category
	categoryQuery.Objective = "Category-level results for " + category
	companies := make(map[string]*models.MarketLandscapeCompany)
	var leads []models.MarketLandscapeEvidence
	leadURLs := make(map[string]bool)
	warnings := []string{}
	queriesUsed := 0
	hitsUsed := 0
	// The lane runs once per requested geography: two regions mean two
	// queries per lane, never one query carrying "Middle East Africa" as a
	// token soup the engine cannot interpret.
	specs := make([]marketSearchSpec, 0, 8)
	for _, lane := range marketCategoryLanes(category) {
		for _, geography := range marketSearchGeographies(query) {
			searchQuery := strings.TrimSpace(lane)
			if geography != "" {
				searchQuery = strings.TrimSpace(lane + " " + geography)
			}
			specs = append(specs, marketSearchSpec{Lane: lane, Query: searchQuery})
		}
	}
	specs, _, queryReviews, reviewWarning := a.reviewMarketQueryBatch(ctx, "category", specs, nil, category, query, eventCh)
	if reviewWarning != "" {
		warnings = append(warnings, reviewWarning)
	}
	for _, spec := range specs {
		if err := ctx.Err(); err != nil {
			warnings = append(warnings, "Category research stopped: "+err.Error())
			break
		}
		emitAgentEvent(eventCh, models.EventToolCall, "Searching category "+category, "search", spec.Query)
		hits, err := a.engine.SearchCached(ctx, spec.Query, marketMaxResults)
		if trace != nil {
			*trace = append(*trace, models.MarketSearchTrace{
				Phase: "category", Lane: spec.Lane, Query: spec.Query,
				Status: marketSearchStatus(err), Hits: len(hits),
			})
		}
		queriesUsed++
		hitsUsed += len(hits)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("category search %q failed: %v", spec.Query, err))
			continue
		}
		for _, hit := range hits {
			candidate := marketCandidate(hit, spec.Lane, categoryQuery)
			if candidate == nil {
				continue
			}
			if excludedMarketDomain(candidate.Domain, query.Exclusions) {
				continue
			}
			if candidate.EvidenceType == "discovery-lead" {
				evidence := candidate.Evidence[0]
				if !leadURLs[evidence.URL] {
					leadURLs[evidence.URL] = true
					leads = append(leads, evidence)
				}
				continue
			}
			mergeMarketCompany(companies, candidate)
		}
	}
	resolved, verificationQueries, _, rejected, _, _, _, _, err := a.verifyMarketLeads(ctx, leads, categoryQuery, purposes, eventCh, trace)
	if err != nil {
		warnings = append(warnings, "Category lead extraction could not complete: "+err.Error())
	}
	for _, company := range resolved {
		mergeMarketCompany(companies, company)
	}
	classifyMarketCompanies(companies, categoryQuery)
	results := make([]models.MarketLandscapeCompany, 0, len(companies))
	for _, company := range companies {
		company.Status = marketStatus(company)
		if company.Status == "verified" || company.Status == "corroborated" {
			results = append(results, *company)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Confidence == results[j].Confidence {
			return results[i].Name < results[j].Name
		}
		return results[i].Confidence > results[j].Confidence
	})
	if len(results) > query.MaxCompanies {
		results = results[:query.MaxCompanies]
	}
	if rejected > 0 {
		warnings = append(warnings, fmt.Sprintf("Category research rejected %d extracted lead(s).", rejected))
	}
	return results, queriesUsed, hitsUsed, verificationQueries, queryReviews, warnings
}

// marketLanes names the search lanes the run intends to cover, in the same
// vocabulary the discovery queries actually use. It is report provenance, not a
// query builder: every entry here must be a phrase the planner issues, so the
// operator can audit coverage without decoding invented role stacks such as
// "company operator manufacturer" or "facility operator".
func marketLanes(query models.MarketLandscapeQuery) []string {
	activity := strings.ToLower(strings.TrimSpace(query.Activity))
	lanes := []string{"manufacturer", "plant", "distributor", "wholesaler"}
	if strings.Contains(activity, "blend") {
		lanes = append(lanes, "blending plant")
	}
	// Seller-side trade data: an exporter or shipment record is direct evidence
	// that a company supplies the product, not merely that it operates a plant.
	lanes = append(lanes, marketTradeDataLanes()...)
	// Seller-side market-intelligence directories (ensun, revenuebase): their
	// pages rank manufacturers of the product and are read ahead of company
	// homepages, so a site-scoped query surfaces the densest rosters in the run.
	lanes = append(lanes, sellerMarketIntelLanes(strings.TrimSpace(query.Activity))...)
	for _, country := range query.Countries {
		lanes = append(lanes, strings.TrimSpace(country)+" manufacturer")
	}
	for _, region := range query.Regions {
		lanes = append(lanes, strings.TrimSpace(region)+" manufacturer")
	}
	return uniqueStrings(lanes)
}

func marketCandidate(hit models.SearchResult, lane string, query models.MarketLandscapeQuery) *models.MarketLandscapeCompany {
	parsed, err := url.Parse(hit.Link)
	if err != nil || parsed.Hostname() == "" {
		return nil
	}
	domain := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	title := strings.TrimSpace(hit.Title)
	if title == "" {
		title = domain
	}
	// The page title is a *source* for the name, never the name itself: it is
	// folded to its company segment, and the raw title stays in the evidence
	// where a reader can audit it.
	name := marketCanonicalName(title, domain)
	usableName := name != ""
	if usableName {
		if cleaned := marketLeadName(name, ""); cleaned == "" {
			usableName = false
		} else {
			name = cleaned
		}
	}
	if !usableName {
		// No company segment (report, listicle, publisher page): the domain is
		// the placeholder identity. The raw title still travels in the evidence
		// record for audit and for the extractor to read.
		name = domain
	}
	sourceTier := marketDiscoveryTier(domain)
	articleShaped := marketDocumentTitle(title) || marketHeadlineTitle(title) || marketPublisherSuffix(title)
	evidenceType := "company-page"
	if classifyDiscoverySource(hit) != sourceOperator || !isQualifyingMarketTier(sourceTier) || articleShaped || !usableName || isGenericMarketTitle(title) || !hasMarketActivityEvidence(hit, query.Activity) || !hasCompanyIdentitySignal(title, domain) {
		evidenceType = "discovery-lead"
	}
	return &models.MarketLandscapeCompany{
		EntityKey: "company:" + domain, Name: name, Domain: domain,
		Activity: query.Activity, Status: "candidate", EvidenceType: evidenceType, Confidence: 0.45,
		Evidence: []models.MarketLandscapeEvidence{{
			URL: hit.Link, Title: hit.Title, Snippet: hit.Snippet, Lane: lane,
			ClaimType: "activity", SourceTier: sourceTier, RetrievedAt: time.Now(), Status: "candidate",
		}},
	}
}

// marketVerificationPurposes are the per-company verification angles: each is
// appended after the quoted company name and the quoted activity for every
// candidate. The adversarial query review may rewrite or retire a purpose
// before any verification search runs; this default is also the fail-open
// floor when the review is unavailable.
//
// The purposes are indexed role vocabulary, one per seller role the report
// groups: the company's own pages, its production/blending site, and its
// distribution channel. "Facility operator" was retired — no page contains that
// phrase, so it spent a search per candidate and returned only directory noise.
func marketVerificationPurposes() []string {
	return []string{"official website", "plant", "distributor"}
}

func (a *MarketLandscapeAgent) verifyMarketLeads(ctx context.Context, leads []models.MarketLandscapeEvidence, query models.MarketLandscapeQuery, purposes []string, eventCh chan<- models.AgentEvent, trace *[]models.MarketSearchTrace) ([]*models.MarketLandscapeCompany, int, int, int, int, int, int, int, error) {
	if len(leads) == 0 {
		return nil, 0, 0, 0, 0, 0, 0, 0, nil
	}
	if len(purposes) == 0 {
		purposes = marketVerificationPurposes()
	}
	// Reading a page and promoting a page are two different questions. A
	// market-research report, a trade directory or a news article can never be
	// the operating company, but it very often *lists* the companies — the
	// competitive-landscape table on a publisher page is one of the densest
	// name sources a landscape run ever sees. So the read policy is wider than
	// the promotion gate, and it spends the bounded scrape budget on the
	// densest pages first.
	ordered := append([]models.MarketLandscapeEvidence(nil), leads...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return marketLeadReadPriority(ordered[i].SourceTier, ordered[i].Title) < marketLeadReadPriority(ordered[j].SourceTier, ordered[j].Title)
	})
	if len(ordered) > marketMaxLeadPages {
		ordered = ordered[:marketMaxLeadPages]
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
		leadByURL[lead.URL] = lead
	}
	// Reads run through the engine's bounded batch scraper: a deep report page
	// must not serialise the enrichment stage, and outcomes keep lead order.
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
		lead := leadByURL[outcome.URL]
		limit := marketCompanyPageExcerpt
		if marketRosterSourceTier(lead.SourceTier) || marketTitleHintsRoster(lead.Title) {
			limit = marketRosterPageExcerpt
		}
		contentByURL[outcome.URL] = marketPageExcerpt(content, limit)
		pagesEnriched++
		emitAgentEvent(eventCh, models.EventToolResult,
			fmt.Sprintf("Read %s (%s) for company names: %d characters", hostname(outcome.URL), lead.SourceTier, len(content)),
			"scrape", outcome.URL)
	}
	enrichedLeads := make([]models.MarketLandscapeEvidence, 0, len(ordered))
	for _, lead := range ordered {
		leadCopy := lead
		if content, ok := contentByURL[lead.URL]; ok {
			leadCopy.Content = content
			leadCopy.Status = "page read"
		}
		enrichedLeads = append(enrichedLeads, leadCopy)
	}

	var extracted []models.MarketLandscapeLeadCandidate
	rejected := 0
	var extractionErrors []string
	// Extraction batches are bounded-concurrent: each batch's results land in
	// its slot and are appended in lead order after the wait, so the dedupe
	// and rejection accounting below never depend on completion order.
	batchCount := (len(enrichedLeads) + marketExtractionBatchSize - 1) / marketExtractionBatchSize
	batchResults := make([][]models.MarketLandscapeLeadCandidate, batchCount)
	batchErrors := make([]error, batchCount)
	extractSemaphore := make(chan struct{}, marketExtractionConcurrency)
	var extractGroup sync.WaitGroup
	for batchIndex := 0; batchIndex < batchCount; batchIndex++ {
		start := batchIndex * marketExtractionBatchSize
		end := start + marketExtractionBatchSize
		if end > len(enrichedLeads) {
			end = len(enrichedLeads)
		}
		extractGroup.Add(1)
		go func(batchIndex, start, end int) {
			defer extractGroup.Done()
			extractSemaphore <- struct{}{}
			defer func() { <-extractSemaphore }()
			leadText := make([]string, 0, end-start)
			for index, lead := range enrichedLeads[start:end] {
				content := lead.Snippet
				if lead.Content != "" {
					content += "\nPage content: " + lead.Content
				}
				leadText = append(leadText, fmt.Sprintf("%d. %s — %s (%s)", index+1, lead.Title, content, lead.URL))
			}
			var batch []models.MarketLandscapeLeadCandidate
			prompt := fmt.Sprintf("Extract every actual company or facility operator name explicitly supported by these market leads. These leads include company pages, directory pages, news articles and market-research report pages. A report or an article is a valid source for the companies it names or lists: read its page content and extract every company named there, including each row of a top-companies, key-players or competitive-landscape table, using the company's own trading name exactly as the page writes it and dropping any tagline. Name the operator, never the page: a report title, article headline, publisher name, site name, navigation label, region, project or generic category is not a company. Never return a headline that contains an action verb (opens, launches, announces, plans, acquires, sells) as a name. Return a JSON array where every element has a non-empty name, country, reason, evidence_url as a string, source_type (operator, news_article or research_report), mention_context and supporting_quote, and role (manufacturer, distributor, wholesaler, trader, or unknown) based on the page's description. A research_report or news_article candidate must be independently verified before promotion. Return a JSON array.\nActivity: %s\nLeads:\n%s", query.Activity, strings.Join(leadText, "\n"))
			if err := a.engine.GenerateStructured(ctx, "You extract company names for a market research verification queue. Return the operating company's own trading name only: never a report title, headline, publisher, site name or category. Never invent names and do not collapse multiple companies into one.", prompt, &batch); err != nil {
				batchErrors[batchIndex] = err
				return
			}
			batchResults[batchIndex] = batch
		}(batchIndex, start, end)
	}
	extractGroup.Wait()
	for batchIndex := 0; batchIndex < batchCount; batchIndex++ {
		start := batchIndex * marketExtractionBatchSize
		end := start + marketExtractionBatchSize
		if end > len(enrichedLeads) {
			end = len(enrichedLeads)
		}
		if batchErrors[batchIndex] != nil {
			rejected += end - start
			extractionErrors = append(extractionErrors, batchErrors[batchIndex].Error())
			emitAgentEvent(eventCh, models.EventError, "Market extraction batch failed: "+batchErrors[batchIndex].Error(), "market_extract", "")
			continue
		}
		batch := batchResults[batchIndex]
		emitAgentEvent(eventCh, models.EventToolResult,
			fmt.Sprintf("Market extraction batch: %d candidate(s) from %d lead(s)", len(batch), end-start),
			"market_extract", "")
		extracted = append(extracted, batch...)
	}

	uniqueExtracted := make([]models.MarketLandscapeLeadCandidate, 0, len(extracted))
	seenNames := make(map[string]bool)
	leadTitles := make([]string, 0, len(enrichedLeads))
	for _, lead := range enrichedLeads {
		if strings.TrimSpace(lead.Title) != "" {
			leadTitles = append(leadTitles, lead.Title)
		}
	}
	for _, candidate := range extracted {
		candidate.Name = strings.TrimSpace(candidate.Name)
		if candidate.SourceType == "" {
			candidate.SourceType = "discovery"
		}
		// A name is a company name or it is nothing: strip page decoration,
		// then reject documents, headlines, publisher echoes and query echoes.
		cleaned := marketLeadName(candidate.Name, "")
		candidate.Name = cleaned
		key := marketNameKey(cleaned)
		if cleaned == "" || !marketLeadNameAcceptable(cleaned, query) || marketNameEchoesLead(cleaned, leadTitles) || key == "" || seenNames[key] {
			rejected++
			if rejected <= 12 {
				emitAgentEvent(eventCh, models.EventToolResult,
					fmt.Sprintf("Market extraction rejected %q (cleaned %q)", candidate.Name, cleaned), "market_extract", "")
			}
			continue
		}
		seenNames[key] = true
		uniqueExtracted = append(uniqueExtracted, candidate)
	}

	verified := make([]*models.MarketLandscapeCompany, 0, len(uniqueExtracted))
	// Verification searches are bounded-concurrent across candidates: each
	// candidate runs its three purposes in order inside its goroutine and
	// collects raw hit groups; the evidence application below stays sequential
	// in candidate order, so summaries, confidence accumulation and domain
	// attribution never depend on completion order. The budget is an atomic
	// counter: every purpose search spends one, so the cap holds exactly and
	// a small budget is partially spendable, like the serial loop it replaced.
	var remainingBudget int64 = int64(marketMaxVerificationQueries)
	spendVerification := func() bool {
		for {
			current := atomic.LoadInt64(&remainingBudget)
			if current <= 0 {
				return false
			}
			if atomic.CompareAndSwapInt64(&remainingBudget, current, current-1) {
				return true
			}
		}
	}
	verificationPurposes := purposes
	type candidateVerification struct {
		searches int
		hits     [][]models.SearchResult
		traces   []models.MarketSearchTrace
	}
	verificationResults := make([]candidateVerification, len(uniqueExtracted))
	verifySemaphore := make(chan struct{}, marketVerificationConcurrency)
	var verifyGroup sync.WaitGroup
	launched := 0
	for index, lead := range uniqueExtracted {
		if index >= marketMaxVerifiedCompanies {
			break
		}
		if atomic.LoadInt64(&remainingBudget) <= 0 {
			break
		}
		launched++
		verifyGroup.Add(1)
		go func(index int, lead models.MarketLandscapeLeadCandidate) {
			defer verifyGroup.Done()
			verifySemaphore <- struct{}{}
			defer func() { <-verifySemaphore }()
			if err := ctx.Err(); err != nil {
				return
			}
			for _, purpose := range verificationPurposes {
				if !spendVerification() {
					break
				}
				searchQuery := fmt.Sprintf("%q %q %s", strings.TrimSpace(lead.Name), query.Activity, purpose)
				emitAgentEvent(eventCh, models.EventToolCall, "Verifying "+lead.Name+" via "+purpose, "search", searchQuery)
				hits, err := a.engine.SearchCached(ctx, searchQuery, marketMaxResults)
				verificationResults[index].traces = append(verificationResults[index].traces, models.MarketSearchTrace{
					Phase: "verification", Lane: purpose, Query: searchQuery,
					Status: marketSearchStatus(err), Hits: len(hits),
				})
				if err != nil {
					continue
				}
				verificationResults[index].searches++
				verificationResults[index].hits = append(verificationResults[index].hits, hits)
			}
		}(index, lead)
	}
	verifyGroup.Wait()
	queriesUsed := 0
	for index, lead := range uniqueExtracted {
		if index >= launched {
			break
		}
		result := verificationResults[index]
		queriesUsed += result.searches
		if trace != nil {
			*trace = append(*trace, result.traces...)
		}
		for groupIndex, hits := range result.hits {
			purpose := ""
			if groupIndex < len(verificationPurposes) {
				purpose = verificationPurposes[groupIndex]
			}
			for _, hit := range hits {
				evidence, ok := marketVerificationEvidence(hit, lead.Name, query.Activity, purpose)
				if !ok {
					continue
				}
				company := findMarketCompany(verified, lead.Name)
				if company == nil {
					company = &models.MarketLandscapeCompany{
						EntityKey: "company:name:" + normalizeMarketName(lead.Name), Name: lead.Name,
						Country: lead.Country, Activity: query.Activity, EvidenceType: "company-page", Confidence: 0.62,
						Role: marketNormalizeRole(lead.Role),
					}
					verified = append(verified, company)
				}
				company.Evidence = append(company.Evidence, evidence)
				if company.EvidenceSummary == "" {
					company.EvidenceSummary = strings.TrimSpace(evidence.Snippet)
				}
				company.Confidence = minFloat(0.98, company.Confidence+0.08)
				// A publisher's domain is the publisher, not the company: only
				// an operator-owned page (its own site, or a government
				// registry) may lend the company its domain. Otherwise one
				// directory page listing ten firms would collapse them all
				// into a single row on that domain.
				if company.Domain == "" && marketOperatorOwnedTier(evidence.SourceTier) {
					company.Domain = strings.TrimPrefix(strings.ToLower(hostname(hit.Link)), "www.")
				}
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
	return verified, queriesUsed, len(uniqueExtracted), rejected, pagesEnriched, 0, 0, 0, extractionErr
}

// isPlausibleMarketLeadName is the name-hygiene gate both landscape modes apply
// to extractor output: a real name survives, a document or a headline does not.
func isPlausibleMarketLeadName(name string) bool {
	cleaned := strings.TrimSpace(name)
	lowered := strings.ToLower(cleaned)
	if len(lowered) < 2 || len(lowered) > 120 {
		return false
	}
	// A sentence is a headline or a description, not a name.
	if len(strings.Fields(cleaned)) > 12 {
		return false
	}
	// Reuse the shared name hygiene: documents, headlines and publisher echoes
	// are evidence, never roster names.
	return marketLeadName(cleaned, "") != ""
}

// marketLeadNameAcceptable adds the producer-side check that the extractor did
// not simply restate the query (its activity, a geography, category words).
func marketLeadNameAcceptable(name string, query models.MarketLandscapeQuery) bool {
	return isPlausibleMarketLeadName(name) && !marketNameIsQueryEcho(name, query)
}

// marketNameIsQueryEcho reports whether a proposed name is the query restated —
// the activity, a geography, or a heap of generic category words — rather than
// a company. Every word must be query or category vocabulary to qualify, so
// "Acme Blending" survives while "oil blending in Singapore" does not.
func marketNameIsQueryEcho(name string, query models.MarketLandscapeQuery) bool {
	words := strings.Fields(marketWordText(name))
	if len(words) == 0 {
		return false
	}
	allowed := make(map[string]bool)
	queryText := append(append([]string{}, query.Countries...), query.Regions...)
	queryText = append(queryText, query.Activity)
	for _, value := range queryText {
		for _, word := range strings.Fields(marketWordText(value)) {
			allowed[word] = true
		}
	}
	for _, word := range strings.Fields(marketWordText("manufacturer manufacturers supplier suppliers producer producers company companies facility facilities plant plants terminal terminals operator operators blending blend")) {
		allowed[word] = true
	}
	for _, word := range words {
		if allowed[word] || marketConnectorWords[word] || marketGenericNameWords[word] || marketIndustryWords[word] || marketTitleNoise[word] {
			continue
		}
		return false
	}
	return true
}

func truncateMarketText(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}

func marketVerificationEvidence(hit models.SearchResult, company, activity, purpose string) (models.MarketLandscapeEvidence, bool) {
	tier := marketDiscoveryTier(hostname(hit.Link))
	if !isQualifyingMarketTier(tier) || marketDocumentTitle(company) || marketHeadlineTitle(company) || marketPublisherSuffix(company) || !hasMarketActivityEvidence(hit, activity) || !mentionsMarketCompany(hit, company) {
		return models.MarketLandscapeEvidence{}, false
	}
	return models.MarketLandscapeEvidence{
		URL: hit.Link, Title: hit.Title, Snippet: hit.Snippet, Lane: "lead verification",
		ClaimType: "activity-verification", SourceTier: tier, RetrievedAt: time.Now(), Status: purpose,
	}, true
}

func mergeMarketCompany(companies map[string]*models.MarketLandscapeCompany, candidate *models.MarketLandscapeCompany) {
	candidateKey := marketNameKey(candidate.Name)
	for _, existing := range companies {
		existingKey := marketNameKey(existing.Name)
		// One company, one row: spelling variants ("Acme" / "Acme Ltd."), the
		// same normalized name, or the same domain all merge.
		if strings.EqualFold(existing.Name, candidate.Name) ||
			(existingKey != "" && existingKey == candidateKey) ||
			(existing.Domain != "" && existing.Domain == candidate.Domain) {
			existing.Evidence = append(existing.Evidence, candidate.Evidence...)
			existing.Confidence = minFloat(0.98, existing.Confidence+0.1)
			if candidate.EvidenceType == "company-page" {
				existing.EvidenceType = candidate.EvidenceType
			} else if candidate.EvidenceType == "roster-extracted" && existing.EvidenceType == "discovery-lead" {
				existing.EvidenceType = candidate.EvidenceType
			}
			if existing.Role == "" && candidate.Role != "" {
				existing.Role = candidate.Role
			}
			// A company first seen through a product-shaped demand lane has no stage.
			// A later stage-specific hit gives it one; without this, gap searches can
			// find the company and still leave its stage empty.
			if strings.TrimSpace(existing.Stage) == "" && strings.TrimSpace(candidate.Stage) != "" {
				existing.Stage = candidate.Stage
			}
			if strings.TrimSpace(existing.Country) == "" && strings.TrimSpace(candidate.Country) != "" {
				existing.Country = candidate.Country
			}
			return
		}
	}
	companies[candidate.EntityKey] = candidate
}

func findMarketCompany(companies []*models.MarketLandscapeCompany, name string) *models.MarketLandscapeCompany {
	key := marketNameKey(name)
	for _, company := range companies {
		if strings.EqualFold(company.Name, name) || (key != "" && marketNameKey(company.Name) == key) {
			return company
		}
	}
	return nil
}

func marketStatus(company *models.MarketLandscapeCompany) string {
	qualifying := 0
	tiers := make(map[string]bool)
	for _, evidence := range company.Evidence {
		if isQualifyingMarketTier(evidence.SourceTier) && evidence.Status != "excluded" {
			qualifying++
			tiers[evidence.SourceTier] = true
		}
	}
	if qualifying >= 2 && len(tiers) >= 2 && company.EvidenceType == "company-page" {
		return "corroborated"
	}
	if qualifying >= 1 && company.EvidenceType == "company-page" {
		return "verified"
	}
	return "lead"
}

func marketSourceTier(domain string) string {
	domain = strings.ToLower(domain)
	if isCustodyDomain(domain) {
		return TradeSourceCustody
	}
	if isTradeDataDomain(domain) {
		return "trade"
	}
	if isMarketIntelDomain(domain) {
		// Operator-promoted market-intelligence directories (ensun,
		// revenuebase): ranked manufacturer rosters — dense discovery sources
		// that are read first, never the operating company itself.
		return "directory"
	}
	if isResearchPublisherDomain(domain) {
		return "research"
	}
	if isNewsSourceDomain(domain) {
		return "news"
	}
	for _, marker := range []string{"facebook.", "youtube.", "twitter.", "x.com"} {
		if strings.Contains(domain, marker) {
			return "social"
		}
	}
	if strings.Contains(domain, ".gov.") || strings.HasSuffix(domain, ".gov") {
		return "regulatory"
	}
	if strings.Contains(domain, "linkedin.") || strings.Contains(domain, "directory") || strings.Contains(domain, "wikipedia.") {
		return "directory"
	}
	for _, marker := range []string{"spglobal.", "lubesngreases.", "irena.", "mordorintelligence."} {
		if strings.Contains(domain, marker) {
			return "trade"
		}
	}
	if strings.Contains(domain, "icis.") || strings.Contains(domain, "argusmedia.") || strings.Contains(domain, "tradeassociation") {
		return "trade"
	}
	return "official"
}

// marketDiscoveryTier is the producer-side tier: the shared base tier plus the
// publisher/aggregator override. A page that writes *about* companies can never
// qualify as one. It is deliberately not folded into marketSourceTier because
// the buyer mode accepts aggregator-hosted tender records as buyer evidence.
func marketDiscoveryTier(domain string) string {
	if override := sharedSourceOverride(domain); override != "" {
		return override
	}
	return marketSourceTier(domain)
}

func isQualifyingMarketTier(tier string) bool {
	return tier == "official" || tier == "regulatory" || tier == "directory" || tier == "trade" || tier == TradeSourceCustody
}

// marketOperatorOwnedTier reports whether the page belongs to the company
// itself (its own site) or to a registry that is authoritative about it. Only
// these may lend a company its domain: a directory or trade page hosts many
// companies and is not any one of them.
func marketOperatorOwnedTier(tier string) bool {
	return tier == "official" || tier == "regulatory"
}

func isResearchPublisherDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimPrefix(domain, "www."))
	// The consultancy list is the shared one (entity_source.go): every market
	// research vendor either landscape mode should refuse lives there once.
	for _, marker := range sourceConsultancyMarkers {
		if strings.Contains(domain, marker) {
			return true
		}
	}
	for _, marker := range []string{"marketinsights", "researchand", "industryresearch", "marketpublishers", "researchreport"} {
		if strings.Contains(domain, marker) {
			return true
		}
	}
	return false
}

func isResearchDocumentTitle(title string) bool {
	lowered := strings.ToLower(strings.TrimSpace(title))
	for _, marker := range []string{
		"market report", "market research", "industry report", "industry analysis",
		"research report", "sample report", "market forecast", "industry forecast",
		"market outlook", "industry outlook", "size and share", "cost analysis",
		"feasibility study", "white paper", "whitepaper", "journal", "patent",
		"study on", "analysis of", "report on",
	} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

func isGenericMarketTitle(title string) bool {
	lowered := strings.ToLower(strings.TrimSpace(title))
	for _, marker := range []string{"top ", "list", "companies", "projects to watch", "how to start", "market map", "news", "report", "history", "inaugurates", "builds new", "supply for", "services", "guide", "sustainable"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

func hasCompanyIdentitySignal(title, domain string) bool {
	// A name and its domain share a brand token in either direction:
	// "Gulf Oil Middle East" <-> gulfoilltd.com, "KLJ Resources" <-> kljresources.com.
	if marketBrandMatchesDomain(title, domain) {
		return true
	}
	name := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(domain, "www."), ".", " "))
	for _, token := range strings.Fields(strings.ReplaceAll(name, "-", " ")) {
		if len(token) >= 5 && strings.Contains(strings.ToLower(title), token) {
			return true
		}
	}
	return false
}

func hasMarketActivityEvidence(hit models.SearchResult, activity string) bool {
	text := strings.ToLower(hit.Title + " " + hit.Snippet)
	activity = strings.ToLower(strings.TrimSpace(activity))
	if activity == "" || strings.Contains(text, activity) {
		return true
	}
	for _, token := range strings.Fields(activity) {
		token = strings.Trim(token, ".,;:()")
		if len(token) >= 5 && strings.Contains(text, token) {
			return true
		}
	}
	return false
}

// mentionsMarketCompany reports whether a hit actually names the company. The
// old rule accepted any single five-character token, so a candidate named
// "Global Lubricants Market Report" was "verified" by every page containing
// "global". Now the full name must appear, or the hit must sit on the company's
// own domain, or at least two distinctive name tokens must appear together.
func mentionsMarketCompany(hit models.SearchResult, company string) bool {
	if marketBrandMatchesDomain(company, hostname(hit.Link)) {
		return true
	}
	text := marketWordText(hit.Title + " " + hit.Snippet)
	full := strings.TrimSpace(marketWordText(company))
	if full != "" && marketContainsPhrase(text, full) {
		return true
	}
	tokens := marketDistinctiveTokens(company)
	if len(tokens) == 0 {
		return false
	}
	matched := 0
	for _, token := range tokens {
		if marketContainsPhrase(text, token) {
			matched++
		}
	}
	if len(tokens) == 1 {
		return matched == 1
	}
	return matched >= 2
}

func excludedMarketDomain(domain string, exclusions []string) bool {
	domain = strings.ToLower(domain)
	for _, exclusion := range exclusions {
		exclusion = strings.ToLower(strings.TrimSpace(exclusion))
		if exclusion != "" && strings.Contains(domain, exclusion) {
			return true
		}
	}
	return false
}

func normalizeMarketName(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func hostname(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func emitAgentEvent(ch chan<- models.AgentEvent, eventType models.EventType, content, toolName, toolInput string) {
	if ch == nil {
		return
	}
	select {
	case ch <- models.AgentEvent{Type: eventType, Content: content, ToolName: toolName, ToolInput: toolInput, Timestamp: time.Now()}:
	default:
	}
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[strings.ToLower(value)] {
			seen[strings.ToLower(value)] = true
			result = append(result, value)
		}

	}
	return result
}
