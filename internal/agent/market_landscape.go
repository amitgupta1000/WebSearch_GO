package agent

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
	marketMaxDiscoveryQueries = 24
	// Company verification gets 100 searches now that key-person discovery no longer consumes
	// a reserve; the extra budget goes to company identity and facility evidence.
	marketMaxVerificationQueries = 100
	marketMaxResults             = 10
	marketMaxCandidates          = 48
	marketMinDiscoveryFindings   = 6
	marketMaxCategoryQueries     = 2
	marketCoverageMinimum        = 6
	// Verification spends three searches per company. The query budget is the
	// real cap; this bound only stops an extraction flood from queueing work
	// the budget cannot pay for.
	marketMaxVerifiedCompanies = 32
	// Lead enrichment reads up to this many pages, densest first, through the
	// engine's bounded batch scraper.
	marketMaxLeadPages = 30
	// A company page needs its opening lines; a report or directory page needs
	// the competitive-landscape table, which sits further down the page.
	marketCompanyPageExcerpt = 3000
	marketRosterPageExcerpt  = 6000
)

// MarketLandscapeAgent owns the additive, vector-free market workflow.
type MarketLandscapeAgent struct {
	engine *Engine
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
		Lanes: marketLanes(query), Iterations: 2, StartedAt: time.Now(),
	}
	if query.IncludeContacts {
		report.Warnings = append(report.Warnings, "Public business-contact collection is reserved for a later bounded pass; use the per-person Fetch contact details action for RocketReach enrichment.")
	}
	category, categoryRationale, categorySource, categoryWarnings := a.inferMarketCategory(ctx, query, eventCh)
	report.ProductCategory = category
	report.CategoryRationale = categoryRationale
	report.CategorySource = categorySource
	report.Warnings = append(report.Warnings, categoryWarnings...)

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
	for _, lane := range report.Lanes {
		if queriesUsed >= marketMaxDiscoveryQueries {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		searchQuery := strings.TrimSpace(query.Activity + " " + lane)
		if len(query.Countries) > 0 {
			searchQuery += " " + strings.Join(query.Countries, " ")
		} else if len(query.Regions) > 0 {
			searchQuery += " " + strings.Join(query.Regions, " ")
		}
		emitAgentEvent(eventCh, models.EventToolCall, "Searching "+lane, "search", searchQuery)
		hits, err := a.engine.SearchCached(ctx, searchQuery, marketMaxResults)
		queriesUsed++
		report.SearchHits += len(hits)
		if err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s search failed: %v", lane, err))
			continue
		}
		processHits(hits, lane)
	}

	// If the exact producer lanes are weak, broaden to the validated first-pass category. The
	// category is a search fallback only; candidates still pass the normal identity and
	// verification gates, and the category page is never promoted as a company.
	if len(companies)+len(leads) < marketMinDiscoveryFindings && category != "" {
		for _, lane := range marketCategoryLanes(category) {
			if queriesUsed >= marketMaxDiscoveryQueries+marketMaxCategoryQueries {
				break
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			searchQuery := strings.TrimSpace(lane + " " + marketGeography(query))
			emitAgentEvent(eventCh, models.EventToolCall, "Broadened category search "+category, "search", searchQuery)
			hits, err := a.engine.SearchCached(ctx, searchQuery, marketMaxResults)
			queriesUsed++
			report.FallbackQueries++
			report.SearchHits += len(hits)
			if err != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("category fallback %q failed: %v", lane, err))
				continue
			}
			processHits(hits, lane)
		}
		report.Lanes = uniqueStrings(append(report.Lanes, marketCategoryLanes(category)...))
		emitAgentEvent(eventCh, models.EventThinking,
			fmt.Sprintf("Discovery was weak (%d finding(s)); broadened to category %q", len(companies)+len(leads), category),
			"market_category", "")
	} else if len(companies)+len(leads) < marketMinDiscoveryFindings {
		report.Warnings = append(report.Warnings, "Discovery was weak and no validated product category was available for a category fallback.")
	}

	report.LeadsReviewed = len(leads)
	report.QueriesUsed = queriesUsed
	emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Extracting companies from %d discovery lead(s)", len(leads)), "", "")
	resolved, verificationQueries, extracted, rejected, enriched, _, _, _, err := a.verifyMarketLeads(ctx, leads, query, eventCh)
	report.QueriesUsed += verificationQueries
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
	report.Coverage = &models.MarketCoverage{Companies: len(companies), Minimum: marketCoverageMinimum, Pass: len(companies) >= marketCoverageMinimum}
	if !report.Coverage.Pass {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Market coverage is weak: %d company(ies) identified; target is at least %d.", len(companies), marketCoverageMinimum))
	}
	report.CompletedAt = time.Now()
	if exports := a.engine.ExportMarketLandscape(ctx, report); len(exports) > 0 {
		report.Exports = exports
	}
	emitAgentEvent(eventCh, models.EventComplete, fmt.Sprintf("Producer/Seller Landscape complete: %d verified result(s), %d candidate(s)", len(report.Results), report.CandidatesFound), "", "")
	return report, nil
}

// inferMarketCategory asks the first pass for the immediate downstream category that consumes the
// product. The category is validated by the same deterministic gate as the buyer-side category and
// is used only to broaden weak discovery, never as a company or a result.
func (a *MarketLandscapeAgent) inferMarketCategory(ctx context.Context, query models.MarketLandscapeQuery, eventCh chan<- models.AgentEvent) (string, string, string, []string) {
	var inference models.MarketLandscapeInference
	prompt := fmt.Sprintf(`Identify the immediate downstream product family or industry category that consumes %q for a producer/seller landscape. It must be specific enough to search, one to four words, and not an end-use sector or generic superclass. Examples: toluene diisocyanate -> polyurethanes; acetic acid -> solvents or acetates; toluene -> aromatics; n-butanol -> solvents; linear alkyl benzene -> surfactants; coal -> coal products, never fuel or energy. Return JSON with category and category_rationale.`, query.Activity)
	if err := a.engine.GenerateStructured(ctx,
		"You identify buyer-side product categories for industrial market research. Never return fuel, energy, chemicals, industrial, automotive or another end-use sector.",
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

func marketGeography(query models.MarketLandscapeQuery) string {
	if len(query.Countries) > 0 {
		return strings.Join(query.Countries, " ")
	}
	return strings.Join(query.Regions, " ")
}

func marketCategoryLanes(category string) []string {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil
	}
	return []string{category + " manufacturers", category + " companies"}
}

func marketLanes(query models.MarketLandscapeQuery) []string {
	activity := strings.ToLower(strings.TrimSpace(query.Activity))
	lanes := []string{"company operator manufacturer", "facility operator", "official company"}
	if strings.Contains(activity, "blend") {
		lanes = append(lanes, "blending plant facility", "industrial oil blending", "fuel oil blending plant")
	} else {
		lanes = append(lanes, "manufacturer facility", "operator site", "industry directory trade association")
	}
	for _, country := range query.Countries {
		lanes = append(lanes, strings.TrimSpace(country)+" company operator manufacturer")
	}
	for _, region := range query.Regions {
		lanes = append(lanes, strings.TrimSpace(region)+" company operator manufacturer")
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

func (a *MarketLandscapeAgent) verifyMarketLeads(ctx context.Context, leads []models.MarketLandscapeEvidence, query models.MarketLandscapeQuery, eventCh chan<- models.AgentEvent) ([]*models.MarketLandscapeCompany, int, int, int, int, int, int, int, error) {
	if len(leads) == 0 {
		return nil, 0, 0, 0, 0, 0, 0, 0, nil
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
		prompt := fmt.Sprintf("Extract every actual company or facility operator name explicitly supported by these market leads. These leads include company pages, directory pages, news articles and market-research report pages. A report or an article is a valid source for the companies it names or lists: read its page content and extract every company named there, including each row of a top-companies, key-players or competitive-landscape table, using the company's own trading name exactly as the page writes it and dropping any tagline. Name the operator, never the page: a report title, article headline, publisher name, site name, navigation label, region, project or generic category is not a company. Never return a headline that contains an action verb (opens, launches, announces, plans, acquires, sells) as a name. For every extracted company, include source_type (operator, news_article or research_report), mention_context, supporting_quote, country, reason, and evidence_url as a string. A research_report or news_article candidate must be independently verified before promotion. Return a JSON array.\nActivity: %s\nLeads:\n%s", query.Activity, strings.Join(leadText, "\n"))
		if err := a.engine.GenerateStructured(ctx, "You extract company names for a market research verification queue. Return the operating company's own trading name only: never a report title, headline, publisher, site name or category. Never invent names and do not collapse multiple companies into one.", prompt, &batch); err != nil {
			rejected += end - start
			extractionErrors = append(extractionErrors, err.Error())
			continue
		}
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
			continue
		}
		seenNames[key] = true
		uniqueExtracted = append(uniqueExtracted, candidate)
	}

	verified := make([]*models.MarketLandscapeCompany, 0, len(uniqueExtracted))
	queriesUsed := 0
	verificationBudget := marketMaxVerificationQueries
	for index, lead := range uniqueExtracted {
		if index >= marketMaxVerifiedCompanies || queriesUsed >= verificationBudget {
			break
		}
		for _, purpose := range []string{"official site", "facility operator", "registry trade source"} {
			if queriesUsed >= verificationBudget {
				break
			}
			searchQuery := fmt.Sprintf("\"%s\" %s %s", strings.TrimSpace(lead.Name), query.Activity, purpose)
			emitAgentEvent(eventCh, models.EventToolCall, "Verifying "+lead.Name+" via "+purpose, "search", searchQuery)
			hits, err := a.engine.SearchCached(ctx, searchQuery, marketMaxResults)
			queriesUsed++
			if err != nil {
				continue
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
	return tier == "official" || tier == "regulatory" || tier == "directory" || tier == "trade"
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
