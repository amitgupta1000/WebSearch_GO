package agent

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

const (
	marketMaxDiscoveryQueries    = 18
	// Company verification gets 30 searches. Web key-person discovery and the
	// optional RocketReach discovery each get their own 20-search budget.
	marketMaxVerificationQueries = 70
	marketPeopleQueryReserve     = 40
	marketMaxResults             = 10
	marketMaxCandidates          = 40
)

// MarketLandscapeAgent owns the additive, vector-free market workflow.
type MarketLandscapeAgent struct {
	engine      *Engine
	rocketReach *tools.RocketReachClient
}

func NewMarketLandscapeAgent(engine *Engine, rocketReach ...*tools.RocketReachClient) *MarketLandscapeAgent {
	var client *tools.RocketReachClient
	if len(rocketReach) > 0 {
		client = rocketReach[0]
	}
	return &MarketLandscapeAgent{engine: engine, rocketReach: client}
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
	if query.MaxCompanies <= 0 || query.MaxCompanies > 50 {
		query.MaxCompanies = 20
	}

	report := &models.MarketLandscapeReport{
		ResearchID: researchID, Objective: query.Objective, Activity: query.Activity,
		Lanes: marketLanes(query), Iterations: 2, StartedAt: time.Now(),
		RocketReachDiscoveryEnabled: query.UseRocketReachDiscovery,
	}
	if query.UseRocketReachDiscovery && (a.rocketReach == nil || !a.rocketReach.Enabled()) {
		report.Warnings = append(report.Warnings, "RocketReach discovery was selected but RocketReach is not configured; no RocketReach profiles were searched.")
	}
	if query.IncludeContacts {
		report.Warnings = append(report.Warnings, "Public business-contact collection is reserved for a later bounded pass; use the per-person Fetch contact details action for RocketReach enrichment.")
	}

	companies := make(map[string]*models.MarketLandscapeCompany)
	var leads []models.MarketLandscapeEvidence
	leadURLs := make(map[string]bool)
	queriesUsed := 0
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

	rrCalls, rrProfiles, rrPromoted := 0, 0, 0
	if query.UseRocketReachDiscovery {
		candidates := make([]*models.MarketLandscapeCompany, 0, len(companies))
		for _, company := range companies {
			candidates = append(candidates, company)
		}
		emitAgentEvent(eventCh, models.EventThinking, "Running RocketReach discovery before web key-person searches", "rocketreach_search", "")
		if len(candidates) == 0 {
			emitAgentEvent(eventCh, models.EventToolResult, "RocketReach discovery skipped: no company candidates were produced by market discovery", "rocketreach_search", "")
		} else {
			rrCalls, rrProfiles, rrPromoted = a.discoverMarketRocketReach(ctx, candidates, marketPeopleQueryReserve/2, eventCh)
			emitAgentEvent(eventCh, models.EventToolResult,
				fmt.Sprintf("RocketReach discovery complete: %d call(s), %d profile(s) returned, %d candidate(s) promoted", rrCalls, rrProfiles, rrPromoted),
				"rocketreach_search", "")
		}
	}
	peopleQueries := 0
	if query.IncludePeople {
		candidates := make([]*models.MarketLandscapeCompany, 0, len(companies))
		for _, company := range companies {
			candidates = append(candidates, company)
		}
		peopleQueries = a.discoverMarketPeople(ctx, candidates, query, marketPeopleQueryReserve/2, eventCh)
	}
	report.QueriesUsed += rrCalls + peopleQueries
	report.RocketReachDiscoveryCalls = rrCalls
	report.RocketReachProfilesFound = rrProfiles
	report.RocketReachCandidatesPromoted = rrPromoted

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
	report.CompletedAt = time.Now()
	if exports := a.engine.ExportMarketLandscape(ctx, report); len(exports) > 0 {
		report.Exports = exports
	}
	emitAgentEvent(eventCh, models.EventComplete, fmt.Sprintf("Market landscape complete: %d verified result(s), %d candidate(s)", len(report.Results), report.CandidatesFound), "", "")
	return report, nil
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
	name := strings.TrimSpace(hit.Title)
	if name == "" {
		name = domain
	}
	sourceTier := marketSourceTier(domain)
	evidenceType := "company-page"
	if sourceTier == "news" || sourceTier == "social" || isGenericMarketTitle(name) || !hasMarketActivityEvidence(hit, query.Activity) || !hasCompanyIdentitySignal(name, domain) {
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
	enrichedLeads := make([]models.MarketLandscapeEvidence, 0, len(leads))
	pagesEnriched := 0
	for index, lead := range leads {
		if index >= 24 {
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
		prompt := fmt.Sprintf("Extract every actual company or facility operator name explicitly supported by these market leads. Exclude publishers, list titles, regions, projects, and generic categories. If a lead explicitly names a relevant sales, marketing, commercial, or business-development person, include the person's name in key_people; never infer people. Return a JSON array with name, country, reason, evidence_url as a string, and key_people as an array of strings.\nActivity: %s\nLeads:\n%s", query.Activity, strings.Join(leadText, "\n"))
		if err := a.engine.GenerateStructured(ctx, "You extract company names for a market research verification queue. Never invent names and do not collapse multiple companies into one.", prompt, &batch); err != nil {
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
	verificationBudget := marketMaxVerificationQueries
	if query.IncludePeople || query.UseRocketReachDiscovery {
		verificationBudget -= marketPeopleQueryReserve
	}
	for index, lead := range uniqueExtracted {
		if index >= 10 || queriesUsed >= verificationBudget {
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
						KeyPeople: append([]string(nil), lead.KeyPeople...),
					}
					verified = append(verified, company)
				}
				company.Evidence = append(company.Evidence, evidence)
				if company.EvidenceSummary == "" {
					company.EvidenceSummary = strings.TrimSpace(evidence.Snippet)
				}
				company.Confidence = minFloat(0.98, company.Confidence+0.08)
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
	return verified, queriesUsed, len(uniqueExtracted), rejected, pagesEnriched, 0, 0, 0, extractionErr
}

func (a *MarketLandscapeAgent) discoverMarketRocketReach(ctx context.Context, companies []*models.MarketLandscapeCompany, budget int, eventCh chan<- models.AgentEvent) (int, int, int) {
	if a.rocketReach == nil || !a.rocketReach.Enabled() {
		return 0, 0, 0
	}
	used, profilesFound, promoted := 0, 0, 0
	for _, company := range companies {
		if used >= budget {
			break
		}
		emitAgentEvent(eventCh, models.EventToolCall, "Discovering RocketReach key people at "+company.Name, "rocketreach_search", company.Name)
		searchFilters := map[string][]string{
			"current_employer": []string{company.Name},
			"current_title":    []string{"sales", "marketing", "commercial", "business development"},
		}
		emitAgentEvent(eventCh, models.EventThinking,
			fmt.Sprintf("RocketReach filters: current employer=%q; titles=sales, marketing, commercial, business development", company.Name),
			"rocketreach_search", "")
		profiles, err := a.rocketReach.Search(ctx, searchFilters)
		used++
		if err != nil {
			emitAgentEvent(eventCh, models.EventError, "RocketReach search failed for "+company.Name+": "+err.Error(), "rocketreach_search", company.Name)
			continue
		}
		profilesFound += len(profiles)
		emitAgentEvent(eventCh, models.EventToolResult,
			fmt.Sprintf("RocketReach returned %d profile(s) for %s; validating current-employer matches", len(profiles), company.Name),
			"rocketreach_search", company.Name)
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
					fmt.Sprintf("Promoted RocketReach candidate %s for %s (current employer matched)", name, company.Name),
					"rocketreach_search", "")
			}
		}
	}
	return used, profilesFound, promoted
}

func (a *MarketLandscapeAgent) discoverMarketPeople(ctx context.Context, companies []*models.MarketLandscapeCompany, query models.MarketLandscapeQuery, budget int, eventCh chan<- models.AgentEvent) int {
	used := 0
	for _, company := range companies {
		if used >= budget {
			break
		}
		searchQuery := fmt.Sprintf("\"%s\" (sales OR marketing OR commercial OR \"business development\") (director OR head OR manager OR VP OR chief)", company.Name)
		emitAgentEvent(eventCh, models.EventToolCall, "Discovering key people at "+company.Name, "search", searchQuery)
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
		prompt := fmt.Sprintf("Extract only named people whose sales, marketing, commercial, or business-development role is explicitly associated with %q. Exclude job vacancies, generic titles, publishers, and people whose employer is not stated. Return a JSON array with name only. Evidence:\n%s", company.Name, strings.Join(evidence, "\n"))
		if err := a.engine.GenerateStructured(ctx, "You identify explicitly named current commercial employees from anchored company evidence. Never invent names.", prompt, &people); err != nil {
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

func isPlausiblePersonName(name string) bool {
	parts := strings.Fields(strings.TrimSpace(name))
	return len(parts) >= 2 && len(name) <= 120
}

func isPlausibleMarketLeadName(name string) bool {
	lowered := strings.ToLower(strings.TrimSpace(name))
	if len(lowered) < 3 || len(lowered) > 140 {
		return false
	}
	for _, marker := range []string{"top ", "companies", "projects", "middle east", "southeast asia", "news", "report", "oil blending"} {
		if lowered == marker || strings.HasPrefix(lowered, marker) {
			return false
		}
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
	tier := marketSourceTier(hostname(hit.Link))
	if !isQualifyingMarketTier(tier) || !hasMarketActivityEvidence(hit, activity) || !mentionsMarketCompany(hit, company) {
		return models.MarketLandscapeEvidence{}, false
	}
	return models.MarketLandscapeEvidence{
		URL: hit.Link, Title: hit.Title, Snippet: hit.Snippet, Lane: "lead verification",
		ClaimType: "activity-verification", SourceTier: tier, RetrievedAt: time.Now(), Status: purpose,
	}, true
}

func mergeMarketCompany(companies map[string]*models.MarketLandscapeCompany, candidate *models.MarketLandscapeCompany) {
	for _, existing := range companies {
		if strings.EqualFold(existing.Name, candidate.Name) || (existing.Domain != "" && existing.Domain == candidate.Domain) {
			existing.Evidence = append(existing.Evidence, candidate.Evidence...)
			existing.Confidence = minFloat(0.98, existing.Confidence+0.1)
			if candidate.EvidenceType == "company-page" {
				existing.EvidenceType = candidate.EvidenceType
			}
			return
		}
	}
	companies[candidate.EntityKey] = candidate
}

func findMarketCompany(companies []*models.MarketLandscapeCompany, name string) *models.MarketLandscapeCompany {
	for _, company := range companies {
		if normalizeMarketName(company.Name) == normalizeMarketName(name) {
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
	for _, marker := range []string{"reuters.", "bloomberg.", "news.", "oilprice."} {
		if strings.Contains(domain, marker) {
			return "news"
		}
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

func isQualifyingMarketTier(tier string) bool {
	return tier == "official" || tier == "regulatory" || tier == "directory" || tier == "trade"
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

func mentionsMarketCompany(hit models.SearchResult, company string) bool {
	text := strings.ToLower(hit.Title + " " + hit.Snippet + " " + hostname(hit.Link))
	name := strings.ToLower(strings.TrimSpace(company))
	if name != "" && strings.Contains(text, name) {
		return true
	}
	for _, token := range strings.Fields(name) {
		if len(token) >= 5 && strings.Contains(text, token) {
			return true
		}
	}
	return false
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
