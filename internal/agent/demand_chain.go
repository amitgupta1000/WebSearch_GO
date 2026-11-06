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
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

const (
	demandMaxDiscoveryQueries    = 20
	demandMaxVerificationQueries = 70
	demandMaxLeadsEnriched       = 24
	demandMaxStages              = 7
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
	chain, inferred, warnings := a.inferDemandChain(ctx, query, eventCh)
	report.Chain = chain
	report.ChainInferred = inferred
	report.Warnings = append(report.Warnings, warnings...)
	report.Warnings = append(report.Warnings,
		"Private procurement is invisible to web research: this run only sees demand that surfaced publicly.",
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
	queriesUsed := 0
	for _, lane := range lanes {
		if queriesUsed >= query.DiscoveryQueries {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		searchQuery := strings.TrimSpace(query.Activity + " " + lane.term + " " + demandGeography(query))
		emitAgentEvent(eventCh, models.EventToolCall, "Searching "+lane.term, "search", searchQuery)
		hits, err := a.engine.SearchCached(ctx, searchQuery, marketMaxResults)
		queriesUsed++
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
	report.QueriesUsed = queriesUsed
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
	results := make([]*models.MarketLandscapeCompany, 0, len(companies)+len(verified))
	for _, company := range companies {
		if company.EvidenceType == "company-page" {
			results = append(results, company)
		}
	}
	results = append(results, verified...)
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
	report.StageCandidates = buildStageCandidates(companies, report.Chain)
	report.CompletedAt = time.Now()
	if exports := a.engine.ExportDemandChain(ctx, report); len(exports) > 0 {
		report.Exports = exports
	}
	emitAgentEvent(eventCh, models.EventComplete, fmt.Sprintf("Demand chain complete: %d verified result(s), %d stage(s)", len(report.Results), len(report.Chain)), "", "")
	return report, nil
}

// buildStageCandidates groups the discovery candidates by their consuming
// stage, in the chain's order, deterministically. Companies the product-shaped
// demand lanes surfaced (no single stage) get their own section rather than
// being dropped. Sorted by confidence, then name, like every other view.
func buildStageCandidates(companies map[string]*models.MarketLandscapeCompany, chain []models.ValueChainStage) []models.StageCandidates {
	byStage := make(map[string][]*models.MarketLandscapeCompany)
	var unclassified []*models.MarketLandscapeCompany
	for _, company := range companies {
		company.Status = marketStatus(company)
		if strings.TrimSpace(company.Stage) == "" {
			unclassified = append(unclassified, company)
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
	return sections
}

// normalizeDemandBudget applies the budget defaults and caps: zero keeps the
// mode defaults, and the caps keep a mistyped budget from running away.
func normalizeDemandBudget(query *models.DemandChainQuery) {
	if query.DiscoveryQueries <= 0 {
		query.DiscoveryQueries = demandMaxDiscoveryQueries
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
				Name:      name,
				Source:    "operator",
				Operators: []string{strings.TrimSpace(name) + " plant operator manufacturer"},
			})
		}
		emitAgentEvent(eventCh, models.EventToolResult, fmt.Sprintf("Using %d operator-supplied consuming stage(s)", len(stages)), "", "")
		return stages, false, nil
	}

	var proposed []models.ValueChainStage
	prompt := fmt.Sprintf("Identify the industrial processes that consume or process %q as an input%s. Return a JSON array where each element has name (the consuming process, e.g. \"biodiesel production\"), rationale (one line on why the stage consumes the product), and operators (2-3 search phrases for the plants or companies operating that stage, e.g. \"biodiesel plant\"). Exclude retail, logistics-only and publication stages.", query.Activity, demandGeographyClause(query))
	if err := a.engine.GenerateStructured(ctx,
		"You infer industrial value chains for market research. Never invent stage names that do not plausibly consume the product; keep operator phrases as concrete plant vocabulary.",
		prompt, &proposed); err != nil {
		return a.demandChainFallback(ctx, "Value-chain inference failed", err, query, eventCh)
	}

	stages := sanitizeDemandStages(proposed)
	if len(stages) == 0 {
		return a.demandChainFallback(ctx, "Value-chain inference returned no usable stages", fmt.Errorf("the model proposed no stages"), query, eventCh)
	}
	for index := range stages {
		if len(stages[index].Operators) == 0 {
			stages[index].Operators = []string{strings.TrimSpace(stages[index].Name) + " plant operator manufacturer"}
		}
	}
	emitAgentEvent(eventCh, models.EventToolResult, fmt.Sprintf("Inferred %d consuming stage(s): %s", len(stages), stageNames(stages)), "", "")
	return stages, true, nil
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
		Rationale: "Generic fallback: the model could not propose consuming stages, so only product-shaped demand lanes ran.",
	}}
	return fallback, false, []string{warning}
}

// sanitizeDemandStages trims, dedupes and caps the model's proposal so the
// lanes downstream are always buildable from it.
func sanitizeDemandStages(proposed []models.ValueChainStage) []models.ValueChainStage {
	stages := make([]models.ValueChainStage, 0, len(proposed))
	seen := make(map[string]bool)
	for _, stage := range proposed {
		stage.Name = strings.TrimSpace(stage.Name)
		stage.Rationale = strings.TrimSpace(stage.Rationale)
		if stage.Name == "" || seen[strings.ToLower(stage.Name)] {
			continue
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
	return stages
}

func stageNames(stages []models.ValueChainStage) string {
	names := make([]string, 0, len(stages))
	for _, stage := range stages {
		names = append(names, stage.Name)
	}
	return strings.Join(names, ", ")
}

// demandChainLanes orders the discovery lanes: each stage's operator vocabulary
// first (the chain's whole point), then product-shaped demand lanes, then
// geography-scoped importer/tender lanes.
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
		vocabularyCount := 0
		for _, vocabulary := range stage.Operators {
			if vocabularyCount >= vocabularyCap {
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
		demandLane{term: "distributor wholesaler of"},
		demandLane{term: "applications uses industries"},
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
	if name == "" {
		name = domain
	}
	sourceTier := marketSourceTier(domain)
	evidenceType := "company-page"
	if sourceTier == "news" || sourceTier == "social" || isGenericMarketTitle(name) || isDemandBaitTitle(name) || !hasCompanyIdentitySignal(name, domain) {
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
	enrichedLeads := make([]models.MarketLandscapeEvidence, 0, len(leads))
	pagesEnriched := 0
	for index, lead := range leads {
		if index >= query.MaxLeadsEnriched {
			break
		}
		leadCopy := lead
		if isQualifyingMarketTier(lead.SourceTier) {
			page, err := a.engine.ScrapeCached(ctx, lead.URL)
			if err == nil && page != nil && strings.TrimSpace(page.Content) != "" {
				leadCopy.Content = truncateMarketText(page.Content, 3000)
				pagesEnriched++
			}
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
	for start := 0; start < len(enrichedLeads); start += 4 {
		end := start + 4
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
		prompt := fmt.Sprintf("Extract every actual company or facility operator explicitly supported by these leads as a consumer, buyer, importer, or processor of %q. %sExclude publishers, list titles, market-research vendors, regions, projects, and generic categories. If a lead explicitly names a relevant procurement, purchasing, or supply-chain person, include the person's name in key_people; never infer people. Return a JSON array with name, country, reason, evidence_url as a string, and key_people as an array of strings.\nActivity: %s\nLeads:\n%s", query.Activity, geoPreference, query.Activity, strings.Join(leadText, "\n"))
		if err := a.engine.GenerateStructured(ctx, "You extract company names for a buyer-research verification queue. Never invent names and do not collapse multiple companies into one.", prompt, &batch); err != nil {
			rejected += end - start
			extractionErrors = append(extractionErrors, err.Error())
			continue
		}
		extracted = append(extracted, batch...)
	}

	uniqueExtracted := make([]models.MarketLandscapeLeadCandidate, 0, len(extracted))
	seenNames := make(map[string]bool)
	for _, candidate := range extracted {
		candidate.Name = strings.TrimSpace(candidate.Name)
		// A name that arrived as a page title ("Butyl Acrylate | Products | X")
		// collapses to its longest segment — the company name is usually the
		// last meaningful part of a navigation title.
		if strings.Contains(candidate.Name, "|") {
			best := ""
			for _, segment := range strings.Split(candidate.Name, "|") {
				if trimmed := strings.TrimSpace(segment); len(trimmed) > len(best) {
					best = trimmed
				}
			}
			candidate.Name = best
		}
		// A name that equals the country the lead named is a geography, not a
		// company — the model occasionally echoes a country as an entity.
		if strings.EqualFold(candidate.Name, strings.TrimSpace(candidate.Country)) {
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
	purposes := []struct {
		phrase string
		kind   string
	}{
		{"official site", "own site"},
		{"tender OR RFP OR procurement", "tender or procurement record"},
		{"import data OR importer of", "import record"},
	}
	for index, lead := range uniqueExtracted {
		if index >= query.MaxCompanies || queriesUsed >= query.VerificationQueries {
			break
		}
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
				company.Evidence = append(company.Evidence, evidence)
				if company.EvidenceSummary == "" {
					company.EvidenceSummary = strings.TrimSpace(evidence.Snippet)
				}
				company.Confidence = minFloat(0.98, company.Confidence+demandClaimWeight(evidence.ClaimType))
				if company.Domain == "" {
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

// demandVerificationEvidence accepts one verification hit as consumption
// evidence when the tier qualifies, the company is named, and the hit carries
// buyer language or the activity itself. The claim type is read off the text.
func demandVerificationEvidence(hit models.SearchResult, company, activity, purpose string) (models.MarketLandscapeEvidence, bool) {
	tier := marketSourceTier(hostname(hit.Link))
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
