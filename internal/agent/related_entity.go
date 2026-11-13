package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

const (
	relatedEntityCompanyQueries     = 32
	relatedEntityPeopleQueries      = 8
	relatedEntityPersonQueries      = 6
	relatedEntityMaxLeaders         = 16
	relatedEntitySearchConcurrency  = 8
	relatedEntityDirectEvidence     = 40
	relatedEntityLeaderEvidence     = 30
	relatedEntitySnippetMaxRunes    = 1200
	relatedEntityIdentityConfidence = 0.60
)

var directorIdentificationNumberPattern = regexp.MustCompile(`(?i)\b(?:din|director\s+identification\s+number)\D{0,12}(\d{8})\b`)

// RelatedEntityAgent discovers direct company relationships and then follows
// confirmed leadership into other companies. It never mutates CompanyBrief.
type RelatedEntityAgent struct {
	engine      *Engine
	rocketReach *tools.RocketReachClient
}

func NewRelatedEntityAgent(engine *Engine, rocketReach ...*tools.RocketReachClient) *RelatedEntityAgent {
	agent := &RelatedEntityAgent{engine: engine}
	if len(rocketReach) > 0 {
		agent.rocketReach = rocketReach[0]
	}
	return agent
}

func (a *RelatedEntityAgent) Research(ctx context.Context, sessionID string, query models.RelatedEntityQuery, eventCh chan<- models.AgentEvent) (*models.RelatedEntityReport, error) {
	query.CompanyName = strings.TrimSpace(query.CompanyName)
	if query.CompanyName == "" {
		return nil, fmt.Errorf("company name is required")
	}
	if query.MaxLeaders <= 0 || query.MaxLeaders > relatedEntityMaxLeaders {
		query.MaxLeaders = relatedEntityMaxLeaders
	}

	companyQueries := relatedDirectQueries(query)
	peopleQueries := relatedLeadershipQueries(query)
	if len(companyQueries) > relatedEntityCompanyQueries {
		companyQueries = companyQueries[:relatedEntityCompanyQueries]
	}
	if len(peopleQueries) > relatedEntityPeopleQueries {
		peopleQueries = peopleQueries[:relatedEntityPeopleQueries]
	}
	allQueries := append(append([]relatedPlannedQuery{}, companyQueries...), peopleQueries...)
	trace := &models.SearchTrace{}
	used := 0
	cacheStatus := models.RelatedEntityCache{Checked: len(allQueries)}
	for index, planned := range allQueries {
		var cached []models.SearchResult
		if cache.GetJSON(ctx, a.engine.cache, cache.HashKey("search", planned.Query), &cached) {
			cacheStatus.Hits++
		} else {
			cacheStatus.Misses++
		}
		Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventThinking, Step: index + 1, Content: "Searching " + planned.Purpose})
		hits, err := a.engine.SearchCached(ctx, planned.Query, a.engine.MaxSearchResults())
		used++
		trace.Queries = append(trace.Queries, models.QueryTrace{Query: planned.Query, Purpose: planned.Purpose, Kept: hits})
		if err != nil {
			return nil, fmt.Errorf("related-entity search %q: %w", planned.Query, err)
		}
	}

	directHits := flattenTrace(trace, 0, len(companyQueries))
	leadHits := flattenTrace(trace, len(companyQueries), len(allQueries))
	// WeeDoo is the priority registry source: its company page lists every
	// director with DIN, appointment date and role, and its director pages list
	// the other companies each director is associated with. The search provider
	// barely indexes WeeDoo, so the page is constructed from the company name
	// and its CIN (the operator's registration id, or a CIN the run's own hits
	// published) instead of being relied on from site: queries.
	registryHits, networkHits := a.weeDooRegistryHits(ctx, sessionID, query, trace, eventCh)
	// The registry page leads both payloads: its directors table freezes the
	// leaders' identities (name + DIN). The Business Network's connected
	// companies join the direct branch — connections the company declares
	// itself, ahead of any person search.
	directHits = append(append(append([]models.SearchResult{}, registryHits...), networkHits...), directHits...)
	leadHits = append(append([]models.SearchResult{}, registryHits...), leadHits...)
	directHits = directAssociationHits(query, directHits)
	leadHits = leadershipIdentityHits(query, leadHits)
	report := &models.RelatedEntityReport{
		CompanyName: query.CompanyName, Domain: query.Domain, RegistrationID: query.RegistrationID,
		EntityConfirmed: strings.TrimSpace(query.Domain) != "" || strings.TrimSpace(query.RegistrationID) != "",
		QueriesUsed:     used, QueriesPlanned: len(allQueries), CompanyQueriesUsed: len(companyQueries), PeopleQueriesUsed: len(peopleQueries), SearchTrace: trace,
		Cache: cacheStatus,
	}
	for _, queryTrace := range trace.Queries {
		report.SearchHits += len(queryTrace.Kept)
	}
	if report.SearchHits == 0 {
		report.Warnings = append(report.Warnings, "The search provider returned no cached or live hits for the planned queries.")
	}
	report.ReviewEvidence = reviewEvidence(trace, 50)
	if err := a.extract(ctx, query, directHits, leadHits, report); err != nil {
		return nil, err
	}
	report.Graph = relatedEntityGraph(report)
	if len(report.Leadership) == 0 {
		report.Warnings = append(report.Warnings, "No company-linked directors or senior executives were identified in the returned snippets.")
	}
	report.CompletedAt = time.Now().UTC()
	report.Exports = a.engine.ExportRelatedEntity(ctx, report)
	Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventComplete, Content: fmt.Sprintf("Found %d direct relationship(s), %d leader(s), and %d leadership relationship(s)", len(report.DirectRelationships), len(report.Leadership), len(report.LeadershipRelations))})
	return report, nil
}

type relatedPlannedQuery struct {
	Query   string
	Purpose string
}

func relatedDirectQueries(query models.RelatedEntityQuery) []relatedPlannedQuery {
	name := quote(query.CompanyName)
	brief := models.CompanyBrief{
		CompanyName: query.CompanyName, Domain: query.Domain,
		RegistrationID: query.RegistrationID, SourceProfile: query.SourceProfile,
		ResearchPreferences: models.ResearchPreferences{Country: query.Country},
	}
	queries := []relatedPlannedQuery{
		{name + " (group OR subsidiary OR affiliate OR parent)", "direct group, parent, subsidiary, and affiliate relationships"},
		{name + "(" + "holding company" + " OR " + "promoter group" + " OR " + "sister company" + ")", "holding and promoter-group relationships"},
		{name + " subsidiaries companies group structure", "company group structure"},
		{name + " " + "related companies", "related-company disclosures"},
		{name + " corporate structure annual report", "annual-report corporate structure"},
		{name + " " + "part of" + " group company", "group membership statements"},
	}
	if companySourceProfile(brief) == SourceProfileIndia {
		queries = append([]relatedPlannedQuery{
			relatedPlannedQuery{name + ` ("related companies" OR subsidiary OR associate OR group) site:weedoo.in`, "WeeDoo related-company evidence"},
			relatedPlannedQuery{name + ` director DIN appointment site:weedoo.in`, "WeeDoo director and appointment evidence"},
		}, queries...)
	}
	for _, host := range companySourceHosts(brief) {
		queries = append(queries, relatedPlannedQuery{
			sourceSiteQuery(name+" (group OR subsidiary OR affiliate OR parent OR director)", []string{host}),
			"source-profile relationship evidence from " + host,
		})
	}
	return queries
}

func relatedLeadershipQueries(query models.RelatedEntityQuery) []relatedPlannedQuery {
	name := quote(query.CompanyName)
	queries := []relatedPlannedQuery{
		{name + ` (director OR "managing director" OR "executive director" OR promoter OR founder OR CEO OR CFO OR COO OR partner OR "managing partner")`, "company-linked leadership"},
		{(name + " directors executives") + " site:linkedin.com", "LinkedIn-indexed leadership"},
		{name + " directors executives " + sourceHint(query), "regional leadership directories"},
	}
	if strings.Contains(strings.ToLower(query.Country), "india") || strings.EqualFold(query.SourceProfile, "india") {
		queries = append([]relatedPlannedQuery{
			{name + ` directors executives site:weedoo.in`, "WeeDoo leadership evidence"},
			{name + ` director DIN appointment site:weedoo.in`, "WeeDoo director identity evidence"},
		}, queries...)
	}
	if person := strings.TrimSpace(query.PersonName); person != "" {
		personTerm := quote(person)
		domain := strings.TrimSpace(query.PersonDomain)
		queries = append([]relatedPlannedQuery{
			{personTerm + " DIN director identification number", "person identity and DIN"},
			{personTerm + " " + name + " appointment director", "person-company appointment"},
			{personTerm + " " + name + " " + sourceHint(query), "person registry and directory identity"},
		}, queries...)
		if domain != "" {
			queries = append([]relatedPlannedQuery{
				{personTerm + " site:" + domain, "person primary-domain identity"},
			}, queries...)
		}
	}
	return queries
}

func sourceHint(query models.RelatedEntityQuery) string {
	if strings.Contains(strings.ToLower(query.Country), "india") || strings.EqualFold(query.SourceProfile, "india") {
		return "zaubacorp OR thecompanycheck OR tracxn"
	}
	return "zoominfo OR apollo OR theorg"
}

func relatedHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
}

func relatedHostMatchesDomain(host, domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimPrefix(domain, "www.")
	domain = strings.TrimSuffix(domain, "/")
	return domain != "" && (host == domain || strings.HasSuffix(host, "."+domain))
}

func relatedTrustedRegistryHost(host string) bool {
	for _, trusted := range []string{
		"weedoo.in", "zaubacorp.com", "mca.gov.in", "sebi.gov.in",
		"bseindia.com", "nseindia.com", "sec.gov", "companieshouse.gov.uk",
	} {
		if host == trusted || strings.HasSuffix(host, "."+trusted) {
			return true
		}
	}
	return false
}

func relatedAnnualReportHit(hit models.SearchResult) bool {
	text := strings.ToLower(hit.Title + " " + hit.Snippet + " " + hit.Link)
	for _, marker := range []string{"annual report", "annual-report", "form 10-k", "10-k filing", "corporate filing"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(hit.Link)), ".pdf")
}

// directAssociationHits is the boundary between discovery and promotion for
// step one. Generic search, news, directories, and research reports can still
// remain in SearchTrace, but only primary-domain, filing/registry, or explicit
// annual-report evidence reaches the direct-relationship extractor.
func directAssociationHits(query models.RelatedEntityQuery, hits []models.SearchResult) []models.SearchResult {
	out := make([]models.SearchResult, 0, len(hits))
	seen := map[string]bool{}
	for _, hit := range hits {
		if strings.TrimSpace(hit.Link) == "" || isResearchEvidenceURL(hit.Link) {
			continue
		}
		host := relatedHost(hit.Link)
		trusted := relatedHostMatchesDomain(host, query.Domain) || relatedTrustedRegistryHost(host)
		if !trusted {
			continue
		}
		if !containsCompanyIdentity(hit.Title+" "+hit.Snippet, query) {
			continue
		}
		if seen[hit.Link] {
			continue
		}
		seen[hit.Link] = true
		out = append(out, hit)
	}
	return out
}

func explicitDirectRelationshipEvidence(evidence []models.RelatedEntityEvidence) bool {
	for _, item := range evidence {
		text := strings.ToLower(item.Title + " " + item.Snippet)
		for _, marker := range []string{
			"subsidiary", "affiliate", "parent company", "holding company",
			"sister company", "associate company", "joint venture", "owned by",
			"controlled by", "promoted by", "part of the", "flagship company",
			"business network", "direct child", "ultimate child",
		} {
			if strings.Contains(text, marker) {
				return true
			}
		}
	}
	return false
}

// leadershipIdentityHits supplies step two with evidence that can establish
// who works at the primary company. Career pages and namesake search results
// are not enough unless they are tied back to an official, registry, filing,
// or LinkedIn source that also names the primary company.
func leadershipIdentityHits(query models.RelatedEntityQuery, hits []models.SearchResult) []models.SearchResult {
	out := make([]models.SearchResult, 0, len(hits))
	seen := map[string]bool{}
	for _, hit := range hits {
		if strings.TrimSpace(hit.Link) == "" || isResearchEvidenceURL(hit.Link) {
			continue
		}
		host := relatedHost(hit.Link)
		isLinkedIn := host == "linkedin.com" || strings.HasSuffix(host, ".linkedin.com")
		trusted := relatedHostMatchesDomain(host, query.Domain) || relatedTrustedRegistryHost(host) ||
			isLinkedIn || relatedAnnualReportHit(hit)
		if !trusted || !containsCompanyIdentity(hit.Title+" "+hit.Snippet, query) {
			continue
		}
		if seen[hit.Link] {
			continue
		}
		seen[hit.Link] = true
		out = append(out, hit)
	}
	return out
}

func relatedPersonEvidenceHits(query models.RelatedEntityQuery, leader models.LeadershipSubject, din string, hits []models.SearchResult, requirePrimary bool) []models.SearchResult {
	out := make([]models.SearchResult, 0, len(hits))
	seen := map[string]bool{}
	for _, hit := range hits {
		if strings.TrimSpace(hit.Link) == "" || isResearchEvidenceURL(hit.Link) ||
			!personNamesInText(hit.Title+" "+hit.Snippet, leader.Name) {
			continue
		}
		host := relatedHost(hit.Link)
		isRegistry := relatedTrustedRegistryHost(host)
		isLinkedIn := host == "linkedin.com" || strings.HasSuffix(host, ".linkedin.com")
		trusted := relatedHostMatchesDomain(host, query.Domain) || isRegistry || isLinkedIn || relatedAnnualReportHit(hit)
		if !trusted {
			continue
		}
		text := hit.Title + " " + hit.Snippet
		if requirePrimary && !containsCompanyIdentity(text, query) {
			continue
		}
		if !requirePrimary && !containsCompanyIdentity(text, query) &&
			(strings.TrimSpace(din) == "" || (!isRegistry && !relatedAnnualReportHit(hit))) {
			continue
		}
		if seen[hit.Link] {
			continue
		}
		seen[hit.Link] = true
		out = append(out, hit)
	}
	return out
}

func relatedEvidenceSearchResults(evidence []models.RelatedEntityEvidence) []models.SearchResult {
	hits := make([]models.SearchResult, 0, len(evidence))
	for _, item := range evidence {
		if strings.TrimSpace(item.URL) == "" {
			continue
		}
		hits = append(hits, models.SearchResult{Title: item.Title, Link: item.URL, Snippet: item.Snippet})
	}
	return hits
}

func quote(value string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(value), `"`, "") + `"`
}

func flattenTrace(trace *models.SearchTrace, start, end int) []models.SearchResult {
	var hits []models.SearchResult
	if trace == nil {
		return hits
	}
	if start < 0 {
		start = 0
	}
	if end > len(trace.Queries) {
		end = len(trace.Queries)
	}
	for _, query := range trace.Queries[start:end] {
		hits = append(hits, query.Kept...)
	}
	return hits
}

// weeDooRegistryHits fetches the WeeDoo company page constructed from the
// company name and its CIN, and returns it as search-shaped hits for both the
// direct-relationship and leadership extraction: the page lists every director
// with DIN, appointment date and role, and links each director's other
// companies. The first slug spelling that resolves wins. A page that does not
// name the company — a wrong guess — is dropped, and the fetch is recorded on
// the trace so the artifact shows the enrichment. The Business Network section
// is parsed separately: its connected companies (Direct/Ultimate children with
// LEI codes) are direct related entities that need no person search at all.
func (a *RelatedEntityAgent) weeDooRegistryHits(ctx context.Context, sessionID string, query models.RelatedEntityQuery, trace *models.SearchTrace, eventCh chan<- models.AgentEvent) ([]models.SearchResult, []models.SearchResult) {
	brief := models.CompanyBrief{CompanyName: query.CompanyName, RegistrationID: query.RegistrationID, Aliases: query.Aliases}
	cin := companyRegistrationNumber(brief, flattenTrace(trace, 0, len(trace.Queries)))
	if cin == "" {
		// No attributed CIN in the brief or the hits: resolve it through
		// WeeDoo's own search bar in the browser, the discovery the search
		// provider cannot do.
		if client, ok := a.engine.WeeDooSearch(); ok {
			if resolved, ok := client.LookupCompanyCIN(ctx, query.CompanyName); ok {
				if upper := strings.ToUpper(strings.TrimSpace(resolved)); companyRegistrationNumberPattern.MatchString(upper) {
					cin = upper
					log.Printf("[RELATED][session %s] WeeDoo search resolved %q -> CIN %s", sessionID, query.CompanyName, cin)
				}
			}
		}
	}
	if cin == "" {
		log.Printf("[RELATED][session %s] WeeDoo registry enrichment skipped: no CIN in the brief or the run's hits", sessionID)
		return nil, nil
	}
	hits := make([]models.SearchResult, 0, 1)
	networkHits := make([]models.SearchResult, 0, 1)
	for _, slug := range registrySlugVariants(query.CompanyName) {
		if len(hits) > 0 {
			break // the first slug spelling that resolves is enough
		}
		url := "https://weedoo.in/company/" + slug + "/" + cin
		var title, text string
		if page, err := a.engine.ScrapeCached(ctx, url); err == nil && page != nil {
			title, text = page.Title, strings.TrimSpace(page.Content)
		}
		content := truncateRunes(text, weeDooRegistrySnippetMaxRunes)
		// Prefer the browser render for WeeDoo pages: the plain fetch's
		// extraction trims them to the site navigation, cutting off the
		// directors and DIN table — the reason the registry is a priority
		// source — even when the fetch passes the identity gate.
		if client, ok := a.engine.WeeDooSearch(); ok {
			if rendered, rerr := client.FetchCompanyPageText(ctx, url); rerr == nil && len(strings.TrimSpace(rendered)) > len(content) {
				log.Printf("[RELATED][session %s] WeeDoo registry page %s read in the browser (%d chars vs %d from the plain fetch)", sessionID, url, len(rendered), len(content))
				title = firstNonEmpty(title, "WeeDoo company page")
				content = truncateRunes(strings.TrimSpace(rendered), weeDooRegistrySnippetMaxRunes)
			}
		}
		if !containsCompanyIdentity(title+" "+content, query) {
			log.Printf("[RELATED][session %s] WeeDoo registry page %s does not name the company; dropped", sessionID, url)
			continue // a constructed URL that resolved to another company
		}
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID, Type: models.EventThinking,
			Content: fmt.Sprintf("WeeDoo registry enrichment: fetched the company page for %s (directors, DINs, appointment dates, other directorships).", query.CompanyName),
		})
		hits = append(hits, models.SearchResult{
			Title:   firstNonEmpty(title, "WeeDoo company page"),
			Link:    url,
			Snippet: content,
		})
		// The Business Network is the first option for direct related
		// entities: the child companies it lists (with LEI codes) are
		// connections the company itself declares — no person search, and no
		// name-ambiguity risk.
		if children := weeDooNetworkChildren(content); len(children) > 0 {
			listing := make([]string, 0, len(children))
			for _, child := range children {
				entry := child.Relation + ": " + child.Name
				if child.LEI != "" {
					entry += " (LEI " + child.LEI + ")"
				}
				listing = append(listing, entry)
			}
			networkHits = append(networkHits, models.SearchResult{
				Title:   "WeeDoo business network for " + query.CompanyName,
				Link:    url + "#business-network",
				Snippet: "Business network of " + query.CompanyName + " — connected companies:\n" + strings.Join(listing, "\n"),
			})
		}
	}
	if len(hits) == 0 {
		return nil, nil
	}
	trace.Queries = append(trace.Queries, models.QueryTrace{
		Query:   hits[0].Link,
		Purpose: "WeeDoo registry enrichment (constructed from company name + CIN)",
		Kept:    hits,
	})
	if len(networkHits) > 0 {
		trace.Queries = append(trace.Queries, models.QueryTrace{
			Query:   networkHits[0].Link,
			Purpose: "WeeDoo business network (connected companies)",
			Kept:    networkHits,
		})
	}
	return hits, networkHits
}

// weeDooRegistrySnippetMaxRunes is the snippet budget for WeeDoo registry and
// director pages. These pages are the priority enrichment — the directors and
// DIN table sits a few thousand characters into the rendered page — so they
// carry a larger budget than ordinary search snippets.
const weeDooRegistrySnippetMaxRunes = 6000

// weeDooChildCompany is one connection the Business Network lists: a company
// related to the researched one, with how (Direct/Ultimate child) and its LEI
// code when the page publishes one.
type weeDooChildCompany struct {
	Name     string
	Relation string
	LEI      string
}

// weeDooNetworkChildren parses the connected companies from a rendered WeeDoo
// company page: the "Direct/Ultimate Children" table first (Type <tab> Legal
// Name <tab> LEI code), then the Business Network's Subsidiaries list — pairs
// of <name> / Direct|Ultimate under a "Subsidiaries" + count header — when the
// table is absent. The plain fetch flattens the tabs, so this parses the
// browser render the registry enrichment already prefers.
func weeDooNetworkChildren(pageText string) []weeDooChildCompany {
	lines := strings.Split(pageText, "\n")
	children := make([]weeDooChildCompany, 0, 4)
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			continue
		}
		relation := strings.TrimSpace(parts[0])
		if relation != "Direct" && relation != "Ultimate" {
			continue
		}
		name := strings.TrimSpace(parts[1])
		lei := strings.TrimSpace(parts[2])
		if name == "" || lei == "" || strings.EqualFold(name, "legal name") || len(lei) < 15 {
			continue
		}
		children = append(children, weeDooChildCompany{Name: name, Relation: relation, LEI: lei})
	}
	if len(children) > 0 {
		return children
	}
	for index, line := range lines {
		if strings.TrimSpace(line) != "Subsidiaries" || index+1 >= len(lines) {
			continue
		}
		count := strings.TrimSpace(lines[index+1])
		if !weeDooCountLine.MatchString(count) {
			continue
		}
		for cursor := index + 2; cursor+1 < len(lines); cursor += 2 {
			name := strings.TrimSpace(lines[cursor])
			relation := strings.TrimSpace(lines[cursor+1])
			if name == "" || (relation != "Direct" && relation != "Ultimate") {
				break
			}
			children = append(children, weeDooChildCompany{Name: name, Relation: relation})
		}
		if len(children) > 0 {
			break
		}
	}
	return children
}

// weeDooCountLine matches the connection count under a Business Network list
// header ("Directors", "Subsidiaries").
var weeDooCountLine = regexp.MustCompile(`^\d+$`)

// weeDooRegistryDIN extracts a director's DIN from a WeeDoo registry page: the
// directors table prints "Appointment <tab> Name <tab> DIN <tab> Designation",
// so the eight-digit DIN sits on the same line as the name rather than next to
// a "DIN" label. The name must sit directly before the digits: plain-fetch
// extractions strip newlines and turn a whole page into one line, where every
// director's tokens match and the first table DIN would otherwise win.
func weeDooRegistryDIN(hits []models.SearchResult, director string) string {
	tokens := nameTokens(director)
	if len(tokens) == 0 {
		return ""
	}
	for _, hit := range hits {
		if !strings.Contains(strings.ToLower(hit.Link), "weedoo.in") {
			continue
		}
		for _, line := range strings.Split(hit.Snippet, "\n") {
			lowered := strings.ToLower(line)
			present := true
			for _, token := range tokens {
				if !strings.Contains(lowered, token) {
					present = false
					break
				}
			}
			if !present {
				continue
			}
			for _, match := range bareDigitRunPattern.FindAllStringIndex(line, -1) {
				run := line[match[0]:match[1]]
				if len(run) != 8 {
					continue
				}
				windowStart := match[0] - weeDooDINDistance
				if windowStart < 0 {
					windowStart = 0
				}
				window := lowered[windowStart:match[0]]
				rowPresent := true
				for _, token := range tokens {
					if !strings.Contains(window, token) {
						rowPresent = false
						break
					}
				}
				if rowPresent {
					return run
				}
			}
		}
	}
	return ""
}

// weeDooDINDistance is how far before the digits the director's name may sit on
// a registry table row ("… Hemant Jain <tab> 00506995").
const weeDooDINDistance = 60

// labelledDINFor extracts a DIN labelled for this director: the "DIN <digits>"
// label must sit next to the director's name, because board listings name many
// directors and the first labelled number is not necessarily theirs.
func labelledDINFor(text, director string) string {
	tokens := nameTokens(director)
	if len(tokens) == 0 {
		return ""
	}
	lowered := strings.ToLower(text)
	for _, match := range directorIdentificationNumberPattern.FindAllStringSubmatchIndex(lowered, -1) {
		dinStart, dinEnd := match[2], match[3]
		windowStart := dinStart - weeDooDINDistance
		if windowStart < 0 {
			windowStart = 0
		}
		windowEnd := dinEnd + weeDooDINDistance
		if windowEnd > len(lowered) {
			windowEnd = len(lowered)
		}
		window := lowered[windowStart:windowEnd]
		present := true
		for _, token := range tokens {
			if !strings.Contains(window, token) {
				present = false
				break
			}
		}
		if present {
			return lowered[dinStart:dinEnd]
		}
	}
	return ""
}

// bareDigitRunPattern matches any contiguous digit run; weeDooRegistryDIN keeps
// only runs of exactly eight digits, so masked numbers (*****2690R), dates
// (10/10/1997) and longer runs never match.
var bareDigitRunPattern = regexp.MustCompile(`\d+`)

// weeDooDirectorHit fetches the WeeDoo director page for a resolved DIN: the
// page lists every company the director is associated with — exactly the
// other-company involvement this segment is looking for. The slug is the
// director's name as WeeDoo spells it; a miss is simply skipped.
func (a *RelatedEntityAgent) weeDooDirectorHit(ctx context.Context, name, din string) *models.SearchResult {
	if strings.TrimSpace(din) == "" {
		return nil
	}
	variants := registrySlugVariants(name)
	if len(variants) == 0 {
		return nil
	}
	url := "https://weedoo.in/director/" + variants[0] + "/" + strings.TrimSpace(din)
	var title, text string
	if page, err := a.engine.ScrapeCached(ctx, url); err == nil && page != nil {
		title, text = page.Title, strings.TrimSpace(page.Content)
	}
	plainLen := len(text)
	// Prefer the browser render: the plain fetch's extraction trims WeeDoo's
	// server-rendered pages, and the other-directorship lists are what this hit
	// exists for.
	if client, ok := a.engine.WeeDooSearch(); ok {
		if rendered, rerr := client.FetchCompanyPageText(ctx, url); rerr == nil && len(strings.TrimSpace(rendered)) > len(text) {
			text = strings.TrimSpace(rendered)
		} else if rerr != nil {
			log.Printf("[RELATED] WeeDoo director page %s render unavailable: %v", url, rerr)
		}
	}
	log.Printf("[RELATED] WeeDoo director page %s fetched (%d chars plain, %d total)", url, plainLen, len(text))
	if strings.TrimSpace(text) == "" {
		return nil
	}
	content := truncateRunes(text, weeDooRegistrySnippetMaxRunes)
	return &models.SearchResult{
		Title:   firstNonEmpty(title, "WeeDoo director page"),
		Link:    url,
		Snippet: content,
	}
}

func (a *RelatedEntityAgent) extract(ctx context.Context, query models.RelatedEntityQuery, directHits, leaderHits []models.SearchResult, report *models.RelatedEntityReport) error {
	directPayload, err := json.Marshal(struct {
		Company string
		Hits    []models.SearchResult
	}{query.CompanyName, boundedRelatedEvidence(directHits, relatedEntityDirectEvidence)})
	if err != nil {
		return fmt.Errorf("prepare direct related-entity evidence: %w", err)
	}
	var directExtracted relatedDirectExtraction
	if err := a.engine.llmClient.GenerateStructured(ctx, `Extract only explicitly stated direct corporate relationships for the primary company from the supplied Google-indexed results. Do not infer ownership from a name alone. For every relationship include evidence: an array of the hit(s) that state it, each with url (required — copy the url from the supplied hits), title and snippet. Omit any relationship you cannot cite from the supplied hits. Return JSON with direct_relationships; return an empty array when evidence is insufficient.`, string(directPayload), &directExtracted); err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Direct relationship extraction failed: %v", err))
	} else {
		relationships := make([]models.RelatedCompany, 0, len(directExtracted.DirectRelationships))
		for _, item := range directExtracted.DirectRelationships {
			relationship := item.RelatedCompany()
			relationship.Evidence = evidenceBackedByHits(relationship.Evidence, directHits)
			relationship.Evidence = attachEvidenceFromHits(relationship.Evidence, relationship.Name, query.CompanyName, directHits)
			if !explicitDirectRelationshipEvidence(relationship.Evidence) {
				continue
			}
			relationships = append(relationships, relationship)
		}
		report.DirectRelationships = normalizeRelated(relationships, query.CompanyName)
	}

	leaderPayload, err := json.Marshal(struct {
		Company string
		Hits    []models.SearchResult
	}{query.CompanyName, boundedRelatedEvidence(leaderHits, relatedEntityLeaderEvidence)})
	if err != nil {
		return fmt.Errorf("prepare leadership evidence: %w", err)
	}
	var leaderExtracted relatedLeadershipExtraction
	if err := a.engine.llmClient.GenerateStructured(ctx, `Extract company-linked leadership from the supplied Google-indexed results. Include only named people with an explicit role at the primary company. Prioritize director, managing director, executive director, promoter, founder, CEO, CFO, COO, partner, and managing partner. Do not infer identity from a name alone. For every person include evidence: an array of the hit(s) that name them in the role at the company, each with url (required — copy the url from the supplied hits), title and snippet. Omit any person you cannot cite from the supplied hits. Return JSON with leadership; return an empty array when evidence is insufficient.`, string(leaderPayload), &leaderExtracted); err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Leadership extraction failed: %v", err))
		return nil
	}
	leaders := make([]models.LeadershipSubject, 0, len(leaderExtracted.Leadership))
	for _, item := range leaderExtracted.Leadership {
		leader := item.LeadershipSubject()
		leader.Evidence = evidenceBackedByHits(leader.Evidence, leaderHits)
		leader.Evidence = attachEvidenceFromHits(leader.Evidence, leader.Name, query.CompanyName, leaderHits)
		leaders = append(leaders, leader)
	}
	report.Leadership = normalizeLeaders(leaders, query.CompanyName, query.MaxLeaders)
	if err := a.discoverRocketReach(ctx, query, report); err != nil {
		report.Warnings = append(report.Warnings, err.Error())
	}
	report.Leadership = normalizeLeaders(report.Leadership, query.CompanyName, query.MaxLeaders)
	if len(report.Leadership) == 0 {
		return nil
	}
	return a.searchLeadership(ctx, query, report)
}

func rocketReachEmployerMatches(employer, company string) bool {
	employerTokens := strings.Fields(strings.ToLower(strings.TrimSpace(employer)))
	companyTokens := strings.Fields(strings.ToLower(strings.TrimSpace(company)))
	generic := map[string]bool{"limited": true, "ltd": true, "private": true, "pvt": true, "company": true, "inc": true, "llc": true}
	distinctive := make([]string, 0, len(companyTokens))
	for _, token := range companyTokens {
		token = strings.Trim(token, ".,()")
		if token != "" && !generic[token] {
			distinctive = append(distinctive, token)
		}
	}
	if len(distinctive) == 0 {
		return false
	}
	for _, wanted := range distinctive {
		found := false
		for _, actual := range employerTokens {
			if strings.Trim(actual, ".,()") == wanted {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func rocketReachProfileMatchesCompany(profile tools.RocketReachProfile, query models.RelatedEntityQuery) bool {
	if rocketReachEmployerMatches(profile.CurrentEmployer, query.CompanyName) {
		return true
	}
	for _, job := range profile.JobHistory {
		if rocketReachEmployerMatches(job.Company, query.CompanyName) {
			return true
		}
	}
	return false
}

func rocketReachCountryMatches(profile tools.RocketReachProfile, country string) bool {
	country = strings.TrimSpace(strings.ToLower(country))
	if country == "" || strings.TrimSpace(profile.Country) == "" {
		return true
	}
	return strings.Contains(strings.ToLower(profile.Country), country) ||
		strings.Contains(strings.ToLower(country), strings.ToLower(profile.Country))
}

// discoverRocketReach is an optional person-identity source for step two. It
// searches by employer and seniority, never by a person name, and only merges
// profiles whose employer matches the primary legal name. Affiliations are
// still searched later through the frozen identity gate.
func (a *RelatedEntityAgent) discoverRocketReach(ctx context.Context, query models.RelatedEntityQuery, report *models.RelatedEntityReport) error {
	if a.rocketReach == nil || !a.rocketReach.Enabled() {
		return nil
	}
	profiles, err := a.rocketReach.Search(ctx, map[string][]string{
		"current_employer": {query.CompanyName},
		"current_title":    {"Director", "Managing Director", "CEO", "CFO", "COO", "Founder", "Chairman"},
	})
	if err != nil {
		return fmt.Errorf("RocketReach leadership discovery failed: %w", err)
	}
	for _, profile := range profiles {
		if strings.TrimSpace(profile.Name) == "" ||
			!rocketReachProfileMatchesCompany(profile, query) ||
			!rocketReachCountryMatches(profile, query.Country) {
			continue
		}
		report.RocketReachDiscovery = append(report.RocketReachDiscovery, models.RocketReachDiscovery{
			ProfileID: profile.ID, Name: profile.Name, Title: profile.CurrentTitle,
			Employer: profile.CurrentEmployer, LinkedInURL: profile.LinkedInURL,
			Query: query.CompanyName,
		})
		if strings.TrimSpace(profile.LinkedInURL) == "" {
			continue
		}
		snippet := strings.TrimSpace(profile.CurrentTitle + " at " + profile.CurrentEmployer)
		if snippet == "at " {
			snippet = "RocketReach employer match"
		}
		merged := false
		for index := range report.Leadership {
			if personNamesInText(profile.Name, report.Leadership[index].Name) ||
				personNamesInText(report.Leadership[index].Name, profile.Name) {
				report.Leadership[index].Evidence = appendUniqueRelatedEvidence(report.Leadership[index].Evidence, models.RelatedEntityEvidence{
					URL: profile.LinkedInURL, Title: "RocketReach leadership profile", Snippet: snippet, Source: "RocketReach employer match",
				})
				report.Leadership[index].Confidence = maxFloat(report.Leadership[index].Confidence, 0.75)
				merged = true
				break
			}
		}
		if !merged {
			report.Leadership = append(report.Leadership, models.LeadershipSubject{
				Name: profile.Name, Role: profile.CurrentTitle, Company: query.CompanyName,
				Confidence: 0.75,
				Evidence: []models.RelatedEntityEvidence{{
					URL: profile.LinkedInURL, Title: "RocketReach leadership profile",
					Snippet: snippet, Source: "RocketReach employer match",
				}},
			})
		}
	}
	return nil
}

func boundedRelatedEvidence(hits []models.SearchResult, limit int) []models.SearchResult {
	out := make([]models.SearchResult, 0, limit)
	seen := map[string]bool{}
	for _, hit := range hits {
		key := strings.TrimSpace(hit.Link)
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(hit.Title + "\x00" + hit.Snippet))
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		// WeeDoo registry and director pages keep their larger snippet budget:
		// their directors and DIN table sits a few thousand characters in, and
		// trimming to the ordinary snippet size would cut it off.
		maxRunes := relatedEntitySnippetMaxRunes
		if strings.Contains(strings.ToLower(hit.Link), "weedoo.in") {
			maxRunes = weeDooRegistrySnippetMaxRunes
		}
		hit.Snippet = truncateRunes(strings.TrimSpace(hit.Snippet), maxRunes)
		out = append(out, hit)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func relatedEntityGraph(report *models.RelatedEntityReport) models.RelatedEntityGraph {
	graph := models.RelatedEntityGraph{
		Nodes: []models.RelatedEntityNode{{ID: "company:primary", Name: report.CompanyName, Type: "primary_company"}},
	}
	nodeIDs := map[string]bool{"company:primary": true}
	nodeLabels := map[string]string{"company:primary": report.CompanyName}
	addNode := func(id, name, kind string) {
		if !nodeIDs[id] {
			nodeIDs[id] = true
			nodeLabels[id] = name
			graph.Nodes = append(graph.Nodes, models.RelatedEntityNode{ID: id, Name: name, Type: kind})
		}
	}
	addCompanyEdge := func(company models.RelatedCompany, from string, relationship string) {
		name := strings.TrimSpace(company.Name)
		if name == "" {
			return
		}
		id := "company:" + strings.ToLower(name)
		addNode(id, name, "related_company")
		if relationship == "" {
			relationship = company.Relationship
		}
		graph.Edges = append(graph.Edges, models.RelatedEntityEdge{
			From: from, FromLabel: nodeLabels[from], To: id, ToLabel: name,
			Relationship: relationship, Status: company.Status, Confidence: company.Confidence,
			Evidence: company.Evidence,
		})
	}
	for _, company := range report.DirectRelationships {
		addCompanyEdge(company, "company:primary", company.Relationship)
	}
	for _, leader := range report.Leadership {
		name := strings.TrimSpace(leader.Name)
		if name == "" {
			continue
		}
		id := "person:" + strings.ToLower(name)
		addNode(id, name, "leader")
		graph.Edges = append(graph.Edges, models.RelatedEntityEdge{
			From: "company:primary", FromLabel: report.CompanyName, To: id, ToLabel: name,
			Relationship: leader.Role, Confidence: leader.Confidence, Evidence: leader.Evidence,
		})
	}
	for _, company := range report.LeadershipRelations {
		personID := "person:" + strings.ToLower(strings.TrimSpace(company.Person))
		if !nodeIDs[personID] {
			addNode(personID, company.Person, "leader")
		}
		addCompanyEdge(company, personID, company.Relationship)
	}
	layoutRelatedGraph(&graph)
	return graph
}

func layoutRelatedGraph(graph *models.RelatedEntityGraph) {
	if graph == nil {
		return
	}
	positions := map[string][2]int{}
	positions["company:primary"] = [2]int{500, 70}
	leaders, companies := []int{}, []int{}
	for index := range graph.Nodes {
		node := &graph.Nodes[index]
		if node.ID == "company:primary" {
			node.X, node.Y = 500, 70
			continue
		}
		if node.Type == "leader" {
			leaders = append(leaders, index)
		} else {
			companies = append(companies, index)
		}
	}
	place := func(indexes []int, y int) {
		for position, index := range indexes {
			x := 500
			if len(indexes) > 1 {
				x = 80 + position*840/(len(indexes)-1)
			}
			graph.Nodes[index].X, graph.Nodes[index].Y = x, y
		}
	}
	place(leaders, 250)
	place(companies, 430)
	for _, node := range graph.Nodes {
		positions[node.ID] = [2]int{node.X, node.Y}
	}
	for index := range graph.Nodes {
		graph.Nodes[index].LabelY = graph.Nodes[index].Y + 46
	}
	for index := range graph.Edges {
		edge := &graph.Edges[index]
		from, fromOK := positions[edge.From]
		to, toOK := positions[edge.To]
		if fromOK && toOK {
			edge.X1, edge.Y1 = from[0], from[1]
			edge.X2, edge.Y2 = to[0], to[1]
		}
	}
}

// zaubaDirectorHit fetches the zaubacorp director page for a frozen (name,
// DIN) pair: the URL is constructible as /<NAME-SLUG>-<DIN>, the page is
// server-rendered (the directors table survives extraction), and it lists the
// director's companies with appointment dates. The page is admitted only when
// it carries the DIN and names the director — a zaubacorp miss renders a
// generic page and must not become evidence.
func (a *RelatedEntityAgent) zaubaDirectorHit(ctx context.Context, name, din string) *models.SearchResult {
	if strings.TrimSpace(din) == "" {
		return nil
	}
	variants := registrySlugVariants(name)
	if len(variants) == 0 {
		return nil
	}
	din = strings.TrimSpace(din)
	url := "https://www.zaubacorp.com/" + variants[0] + "-" + din
	var title, text string
	if page, err := a.engine.ScrapeCached(ctx, url); err == nil && page != nil {
		title, text = page.Title, strings.TrimSpace(page.Content)
	}
	if strings.TrimSpace(text) == "" || !strings.Contains(text, din) {
		return nil // a page that does not carry the frozen DIN is not this director
	}
	if !personNamesInText(text, name) {
		return nil
	}
	content := truncateRunes(text, weeDooRegistrySnippetMaxRunes)
	return &models.SearchResult{
		Title:   firstNonEmpty(title, "zaubacorp director page"),
		Link:    url,
		Snippet: content,
	}
}

// personNamesInText reports whether the text names the person: the full name's
// tokens, or an initials rendering aligned through the words before the
// surname.
func personNamesInText(text, name string) bool {
	lowered := strings.ToLower(text)
	tokens := personNameTokens(name)
	if containsAllTokens(lowered, tokens) {
		return true
	}
	return personNamedInText(lowered, tokens)
}

// weeDooRegistryEvidence returns the registry enrichment hits recorded on the
// trace: the leadership pass needs them per leader, both for identity grounding
// (the registry page names the company and its board) and for the directors
// table whose rows carry the DINs the director pages are constructed from.
func weeDooRegistryEvidence(report *models.RelatedEntityReport) []models.SearchResult {
	if report == nil {
		return nil
	}
	var hits []models.SearchResult
	for _, queryTrace := range report.SearchTrace.Queries {
		if strings.HasPrefix(queryTrace.Purpose, "WeeDoo registry enrichment") {
			hits = append(hits, queryTrace.Kept...)
		}
	}
	return hits
}

// frozenLeaderDIN resolves the (name, DIN) pair before any person search: the
// WeeDoo directors table pairs the leader with their DIN at the source, and a
// labelled DIN in the already-retained evidence freezes it too. An empty
// result means the leader's identity is not frozen and name-only searching
// must not run.
func frozenLeaderDIN(registryEvidence, retained []models.SearchResult, leaderName string) string {
	if din := weeDooRegistryDIN(registryEvidence, leaderName); din != "" {
		return din
	}
	for _, hit := range retained {
		if din := labelledDINFor(hit.Title+" "+hit.Snippet, leaderName); din != "" {
			return din
		}
	}
	return ""
}

// anchorPersonQueries appends the frozen DIN to every person query: results
// that mention both the name and the DIN are about this person, which is what
// keeps name-only searching from conflating namesakes.
func anchorPersonQueries(queries []relatedPlannedQuery, din string) []relatedPlannedQuery {
	anchored := make([]relatedPlannedQuery, 0, len(queries))
	for _, planned := range queries {
		anchored = append(anchored, relatedPlannedQuery{
			Query:   planned.Query + " " + quote(din),
			Purpose: planned.Purpose,
		})
	}
	return anchored
}

func (a *RelatedEntityAgent) searchLeadership(ctx context.Context, query models.RelatedEntityQuery, report *models.RelatedEntityReport) error {
	type outcome struct {
		index    int
		items    []models.RelatedCompany
		trace    []models.QueryTrace
		identity *models.PersonIdentity
		err      error
	}
	jobs := make(chan int)
	outcomes := make(chan outcome, len(report.Leadership))
	var wg sync.WaitGroup
	for worker := 0; worker < relatedEntitySearchConcurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				leader := report.Leadership[index]
				registryEvidence := weeDooRegistryEvidence(report)
				// FREEZE the identity before any person search: the WeeDoo
				// directors table pairs the leader with their DIN at the
				// source, and a labelled DIN in the already-retained evidence
				// freezes it too. Name-only searching for an unfrozen leader
				// is what conflated namesakes into wrong related entities.
				frozenDIN := frozenLeaderDIN(registryEvidence, flattenTrace(report.SearchTrace, 0, len(report.SearchTrace.Queries)), leader.Name)
				identityQueries, affiliationQueries := relatedPersonQueries(query, leader)
				if frozenDIN == "" && len(registryEvidence) > 0 {
					// WeeDoo covers this company, so an unfrozen leader has no
					// verified identity: no person searches at all — the
					// Business Network in the retained evidence is the record,
					// and its connected companies already flowed into the
					// direct branch.
					log.Printf("[RELATED] leader %q skipped: no DIN frozen from the WeeDoo directors table or retained evidence; name-only related-entity searching stays off", leader.Name)
					outcomes <- outcome{index: index, identity: &models.PersonIdentity{CanonicalName: leader.Name, Confidence: 0.5}}
					continue
				}
				if frozenDIN != "" {
					// DIN-anchored searching: results that mention both the
					// name and the DIN are about this person.
					identityQueries = anchorPersonQueries(identityQueries, frozenDIN)
					affiliationQueries = anchorPersonQueries(affiliationQueries, frozenDIN)
				}
				// Seed the leader's hits with the WeeDoo registry page: it
				// grounds the leader at the company and carries the directors
				// table with the DIN rows.
				personHits := append(relatedEvidenceSearchResults(leader.Evidence), registryEvidence...)
				var traces []models.QueryTrace
				for _, planned := range identityQueries {
					hits, err := a.engine.SearchCached(ctx, planned.Query, a.engine.MaxSearchResults())
					if err != nil {
						outcomes <- outcome{index: index, trace: traces, err: err}
						personHits = nil
						break
					}
					personHits = append(personHits, hits...)
					traces = append(traces, models.QueryTrace{Query: planned.Query, Purpose: planned.Purpose, Kept: hits})
				}
				if personHits == nil && len(traces) < len(identityQueries) {
					continue
				}
				identityHits := relatedPersonEvidenceHits(query, leader, frozenDIN, personHits, true)
				identity := resolvePersonIdentity(leader, query, identityHits)
				if identity.DIN == "" && frozenDIN != "" {
					identity.DIN = frozenDIN
					identity.Confidence = 1.0
					identity.Attributes = appendUnique(identity.Attributes, "DIN")
				}
				personHits = append(personHits, a.fetchWeeDooEvidence(ctx, personHits)...)
				identityHits = relatedPersonEvidenceHits(query, leader, frozenDIN, personHits, true)
				identity = resolvePersonIdentity(leader, query, identityHits)
				if identity.DIN == "" && frozenDIN != "" {
					identity.DIN = frozenDIN
					identity.Confidence = 1.0
					identity.Attributes = appendUnique(identity.Attributes, "DIN")
				}
				if identity.Confidence < relatedEntityIdentityConfidence {
					outcomes <- outcome{index: index, trace: traces, identity: identity,
						err: fmt.Errorf("identity confidence %.2f is below the %.2f threshold", identity.Confidence, relatedEntityIdentityConfidence)}
					continue
				}
				if identity.DIN != "" {
					dinQuery := relatedPlannedQuery{
						quote(identity.DIN) + " (director OR company OR appointment)",
						"DIN-linked company appointments",
					}
					hits, err := a.engine.SearchCached(ctx, dinQuery.Query, a.engine.MaxSearchResults())
					if err != nil {
						outcomes <- outcome{index: index, trace: traces, identity: identity, err: err}
						continue
					}
					personHits = append(personHits, hits...)
					traces = append(traces, models.QueryTrace{Query: dinQuery.Query, Purpose: dinQuery.Purpose, Kept: hits})
				}
				// WeeDoo's director page lists every other company the director
				// is associated with — constructed from the frozen name+DIN
				// pair, since the search provider barely indexes WeeDoo. Its
				// content feeds the other-company extraction below.
				din := identity.DIN
				if din == "" {
					din = weeDooRegistryDIN(personHits, leader.Name)
				}
				if directorHit := a.weeDooDirectorHit(ctx, leader.Name, din); directorHit != nil {
					personHits = append(personHits, *directorHit)
					traces = append(traces, models.QueryTrace{
						Query: directorHit.Link, Purpose: "WeeDoo director page (other companies)",
						Kept: []models.SearchResult{*directorHit},
					})
				}
				// zaubacorp's director page (frozen name+DIN URL, server-rendered,
				// directors table now extracted) is the second authoritative
				// source for the leader's other companies.
				if zaubaHit := a.zaubaDirectorHit(ctx, leader.Name, din); zaubaHit != nil {
					personHits = append(personHits, *zaubaHit)
					traces = append(traces, models.QueryTrace{
						Query: zaubaHit.Link, Purpose: "zaubacorp director page (frozen name+DIN)",
						Kept: []models.SearchResult{*zaubaHit},
					})
				}
				for _, planned := range affiliationQueries {
					hits, err := a.engine.SearchCached(ctx, planned.Query, a.engine.MaxSearchResults())
					if err != nil {
						outcomes <- outcome{index: index, trace: traces, identity: identity, err: err}
						personHits = nil
						break
					}
					personHits = append(personHits, hits...)
					traces = append(traces, models.QueryTrace{Query: planned.Query, Purpose: planned.Purpose, Kept: hits})
				}
				if personHits == nil && len(traces) < len(identityQueries)+len(affiliationQueries) {
					continue
				}
				relationHits := relatedPersonEvidenceHits(query, leader, identity.DIN, personHits, false)
				var extracted struct {
					Companies []relatedCompanyExtraction `json:"companies"`
				}
				// WeeDoo registry and director pages are the priority evidence for
				// a leader: lead the payload with them so the bounded evidence
				// window always carries the board table and the
				// other-directorship lists, whatever the provider returned.
				payload, marshalErr := json.Marshal(struct {
					Person string
					Hits   []models.SearchResult
				}{leader.Name, boundedRelatedEvidence(reorderWeeDooFirst(relationHits), relatedEntityLeaderEvidence)})
				if marshalErr == nil {
					prompt := `Extract companies other than the primary company that this person is explicitly connected to in the snippets. Return JSON with a companies array; return {"companies":[]} when evidence is insufficient. Include relationship, role, current/former status, confidence, and evidence. Do not report name-only matches. Prefer registry directorship pages — they list every company the person serves with role and status — over profile pages about people who merely share the name.`
					marshalErr = a.engine.llmClient.GenerateStructured(ctx, prompt, string(payload), &extracted)
				}
				if marshalErr != nil {
					outcomes <- outcome{index: index, trace: traces, identity: identity, err: marshalErr}
					continue
				}
				companies := make([]models.RelatedCompany, 0, len(extracted.Companies))
				for _, item := range extracted.Companies {
					company := item.RelatedCompany()
					company.Person = leader.Name
					company.Evidence = evidenceBackedByHits(company.Evidence, relationHits)
					company.Evidence = attachEvidenceFromHits(company.Evidence, company.Name, query.CompanyName, relationHits)
					companies = append(companies, company)
				}
				outcomes <- outcome{index: index, items: companies, trace: traces, identity: identity}
			}

		}()
	}
	go func() {
		for index := range report.Leadership {
			jobs <- index
		}
		close(jobs)
		wg.Wait()
		close(outcomes)
	}()
	for result := range outcomes {
		report.Leadership[result.index].Identity = result.identity
		report.SearchTrace.Queries = append(report.SearchTrace.Queries, result.trace...)
		report.QueriesUsed += len(result.trace)
		report.QueriesPlanned += len(result.trace)
		for _, trace := range result.trace {
			report.SearchHits += len(trace.Kept)
		}
		if result.err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("Leadership search for %s failed: %v", report.Leadership[result.index].Name, result.err))
			continue
		}
		report.LeadershipRelations = append(report.LeadershipRelations, normalizeRelated(result.items, query.CompanyName)...)
	}
	sort.SliceStable(report.LeadershipRelations, func(i, j int) bool {
		return report.LeadershipRelations[i].Confidence > report.LeadershipRelations[j].Confidence
	})
	return nil
}

func (a *RelatedEntityAgent) fetchWeeDooEvidence(ctx context.Context, hits []models.SearchResult) []models.SearchResult {
	const maxPages = 3
	var enriched []models.SearchResult
	seen := map[string]bool{}
	for _, hit := range hits {
		link := strings.TrimSpace(hit.Link)
		if !strings.Contains(strings.ToLower(link), "weedoo.in/") || seen[link] {
			continue
		}
		seen[link] = true
		page, err := a.engine.ScrapeCached(ctx, link)
		if err != nil || page == nil || strings.TrimSpace(page.Content) == "" {
			continue
		}
		content := truncateRunes(strings.TrimSpace(page.Content), relatedEntitySnippetMaxRunes)
		enriched = append(enriched, models.SearchResult{
			Title: page.Title, Link: link, Snippet: content, Position: hit.Position,
		})
		if len(enriched) >= maxPages {
			break
		}
	}
	return enriched
}

func relatedPersonQueries(query models.RelatedEntityQuery, leader models.LeadershipSubject) ([]relatedPlannedQuery, []relatedPlannedQuery) {
	person := quote(leader.Name)
	company := quote(query.CompanyName)
	identity := []relatedPlannedQuery{
		{person + " DIN director identification number", "person DIN identity"},
		{person + " " + company + " appointment director", "person-company appointment"},
		{person + " " + sourceHint(query), "person registry and directory records"},
	}
	if strings.Contains(strings.ToLower(query.Country), "india") || strings.EqualFold(query.SourceProfile, "india") {
		identity = append(identity,
			relatedPlannedQuery{person + " " + company + " site:weedoo.in", "WeeDoo company and leadership identity"},
			relatedPlannedQuery{person + " " + company + " DIN site:zaubacorp.com", "Indian director identity"},
		)
	}
	affiliation := []relatedPlannedQuery{
		{person + " (director OR founder OR chairman OR CEO OR CFO OR board) -" + company, "person other-company involvement"},
		{person + " career profile current former company", "person career identity"},
		{person + " " + company + " site:linkedin.com", "person LinkedIn-indexed identity"},
	}
	if strings.TrimSpace(query.PersonDomain) != "" {
		affiliation[2] = relatedPlannedQuery{person + " site:" + strings.TrimSpace(query.PersonDomain), "person primary-domain identity"}
	}
	return identity, affiliation
}

// reorderWeeDooFirst moves the WeeDoo registry and director pages to the front
// of a leader's hits: the bounded evidence windows then always carry the board
// table and the other-directorship lists, whatever the search provider returned.
func reorderWeeDooFirst(hits []models.SearchResult) []models.SearchResult {
	weedoo := make([]models.SearchResult, 0, 2)
	rest := make([]models.SearchResult, 0, len(hits))
	for _, hit := range hits {
		if strings.Contains(strings.ToLower(hit.Link), "weedoo.in") {
			weedoo = append(weedoo, hit)
		} else {
			rest = append(rest, hit)
		}
	}
	return append(weedoo, rest...)
}

func resolvePersonIdentity(leader models.LeadershipSubject, query models.RelatedEntityQuery, hits []models.SearchResult) *models.PersonIdentity {
	identity := &models.PersonIdentity{CanonicalName: leader.Name, Confidence: 0.35}
	seen := map[string]bool{}
	for _, hit := range hits {
		text := hit.Title + " " + hit.Snippet
		if containsCompanyIdentity(text, query) {
			identity.Confidence = maxFloat(identity.Confidence, 0.60)
			identity.Attributes = appendUnique(identity.Attributes, "company-linked evidence")
		}

		if identity.DIN == "" {
			if din := labelledDINFor(text, leader.Name); din != "" {
				identity.DIN = din
				identity.Confidence = 1.0
				identity.Attributes = appendUnique(identity.Attributes, "DIN")
			}
		}
		if strings.TrimSpace(hit.Link) != "" && !seen[hit.Link] {
			seen[hit.Link] = true
			identity.Evidence = append(identity.Evidence, models.RelatedEntityEvidence{
				URL: hit.Link, Title: hit.Title, Snippet: truncateRunes(hit.Snippet, relatedEntitySnippetMaxRunes),
			})
		}
	}
	if len(identity.Evidence) >= 2 {
		identity.Confidence = maxFloat(identity.Confidence, 0.70)
		identity.Attributes = appendUnique(identity.Attributes, "corroborated public sources")
	}
	return identity
}

// containsCompanyIdentity reports whether a text names the researched company,
// using the entity matcher's tolerant matching — the same spelling-variant
// rules the entity gate applies to kept hits. WeeDoo's pages spell names in
// collapsed slugs ("Klj-Resources-Ltd") that an exact-substring check rejects.
func containsCompanyIdentity(text string, query models.RelatedEntityQuery) bool {
	brief := models.CompanyBrief{CompanyName: query.CompanyName, RegistrationID: query.RegistrationID, Aliases: query.Aliases}
	matcher := newEntityMatcher(brief)
	_, matched := matcher.matchText(text)
	return matched
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueRelatedEvidence(values []models.RelatedEntityEvidence, value models.RelatedEntityEvidence) []models.RelatedEntityEvidence {
	for _, existing := range values {
		if strings.EqualFold(strings.TrimSpace(existing.URL), strings.TrimSpace(value.URL)) {
			return values
		}
	}
	return append(values, value)
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

type relatedCompanyExtraction struct {
	Name          string             `json:"name"`
	Company       string             `json:"company"`
	Relationship  string             `json:"relationship"`
	Status        string             `json:"status"`
	CurrentFormer string             `json:"current_former"`
	Role          string             `json:"role"`
	Confidence    flexibleConfidence `json:"confidence"`
	Evidence      json.RawMessage    `json:"evidence"`
}

// relatedDirectExtraction decodes the model's direct-relationship output. The
// model may emit the bare array or the wrapped object, and evidence arrives in
// several shapes (object arrays, plain strings, missing) that parseRelatedEvidence
// tolerates, so one odd field never fails the whole extraction.
type relatedDirectExtraction struct {
	DirectRelationships []relatedCompanyExtraction `json:"direct_relationships"`
}

func (e *relatedDirectExtraction) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '[' {
		return json.Unmarshal(data, &e.DirectRelationships)
	}
	type extraction relatedDirectExtraction
	var object extraction
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	*e = relatedDirectExtraction(object)
	return nil
}

// relatedLeaderExtraction decodes one extracted leader tolerantly, accepting the
// same evidence shapes the per-person extraction path does.
type relatedLeaderExtraction struct {
	Name       string             `json:"name"`
	Role       string             `json:"role"`
	Company    string             `json:"company"`
	Confidence flexibleConfidence `json:"confidence"`
	Evidence   json.RawMessage    `json:"evidence"`
}

func (item relatedLeaderExtraction) LeadershipSubject() models.LeadershipSubject {
	return models.LeadershipSubject{
		Name:       item.Name,
		Role:       item.Role,
		Company:    item.Company,
		Confidence: float64(item.Confidence),
		Evidence:   parseRelatedEvidence(item.Evidence),
	}
}

type relatedLeadershipExtraction struct {
	Leadership []relatedLeaderExtraction `json:"leadership"`
}

func (e *relatedLeadershipExtraction) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '[' {
		return json.Unmarshal(data, &e.Leadership)
	}
	type extraction relatedLeadershipExtraction
	var object extraction
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	*e = relatedLeadershipExtraction(object)
	return nil
}

func (item relatedCompanyExtraction) RelatedCompany() models.RelatedCompany {
	status := item.Status
	if status == "" {
		status = item.CurrentFormer
	}
	name := item.Name
	if name == "" {
		name = item.Company
	}
	return models.RelatedCompany{
		Name: name, Relationship: item.Relationship, Status: status,
		Role: item.Role, Confidence: float64(item.Confidence), Evidence: parseRelatedEvidence(item.Evidence),
	}
}

type flexibleConfidence float64

func (c *flexibleConfidence) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	var numeric float64
	if err := json.Unmarshal(data, &numeric); err == nil {
		*c = flexibleConfidence(numeric)
		return nil
	}
	var label string
	if err := json.Unmarshal(data, &label); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "high":
		*c = 0.9
	case "medium", "moderate":
		*c = 0.6
	case "low":
		*c = 0.3
	default:
		*c = 0.5
	}
	return nil
}

func parseRelatedEvidence(raw json.RawMessage) []models.RelatedEntityEvidence {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var evidence []models.RelatedEntityEvidence
	if json.Unmarshal(raw, &evidence) == nil {
		return evidence
	}
	var snippet string
	if json.Unmarshal(raw, &snippet) == nil && strings.TrimSpace(snippet) != "" {
		return []models.RelatedEntityEvidence{{Snippet: strings.TrimSpace(snippet)}}
	}
	return nil
}

func normalizeLeaders(leaders []models.LeadershipSubject, company string, limit int) []models.LeadershipSubject {
	out := make([]models.LeadershipSubject, 0, limit)
	for _, leader := range leaders {
		leader.Name = strings.TrimSpace(leader.Name)
		if leader.Name == "" {
			continue
		}
		// An initials rendering of an already-kept leader ("K L Jain" for
		// "Kanhaiya Lal Jain") is the same person: keep one row and let the
		// fuller name stand as the display name.
		variant := false
		for index := range out {
			if personNamesMatch(out[index].Name, leader.Name) {
				variant = true
				out[index].Name = fullerPersonName(out[index].Name, leader.Name)
				break
			}
		}
		if variant {
			continue
		}
		if !hasRelatedEvidenceURL(leader.Evidence) {
			continue
		}
		leader.Company = company
		if leader.Confidence <= 0 {
			leader.Confidence = 0.5
		}
		out = append(out, leader)
		if len(out) == limit {
			break
		}
	}
	return out
}

func normalizeRelated(items []models.RelatedCompany, primary string) []models.RelatedCompany {
	seen := map[string]bool{}
	out := make([]models.RelatedCompany, 0, len(items))
	for _, item := range items {
		item.Name = strings.TrimSpace(item.Name)
		if item.Name == "" || strings.EqualFold(item.Name, primary) || isResearchDocumentTitle(item.Name) || seen[strings.ToLower(item.Name)] {
			continue
		}
		if !hasRelatedEvidenceURL(item.Evidence) || !hasNonResearchRelatedEvidence(item.Evidence) {
			continue
		}
		if item.Confidence <= 0 {
			item.Confidence = 0.5
		}
		seen[strings.ToLower(item.Name)] = true
		out = append(out, item)
	}
	return out
}

func hasRelatedEvidenceURL(evidence []models.RelatedEntityEvidence) bool {
	for _, item := range evidence {
		if strings.TrimSpace(item.URL) != "" {
			return true
		}
	}
	return false
}

func hasNonResearchRelatedEvidence(evidence []models.RelatedEntityEvidence) bool {
	for _, item := range evidence {
		if strings.TrimSpace(item.URL) != "" && !isResearchEvidenceURL(item.URL) {
			return true
		}
	}
	return false
}

func evidenceBackedByHits(evidence []models.RelatedEntityEvidence, hits []models.SearchResult) []models.RelatedEntityEvidence {
	if len(evidence) == 0 {
		return nil
	}
	allowed := map[string]bool{}
	for _, hit := range hits {
		if strings.TrimSpace(hit.Link) != "" {
			allowed[hit.Link] = true
		}
	}
	out := make([]models.RelatedEntityEvidence, 0, len(evidence))
	for _, item := range evidence {
		if allowed[item.URL] && !isResearchEvidenceURL(item.URL) {
			out = append(out, item)
		}
	}
	return out
}

// attachEvidenceFromHits grounds an extracted claim the model returned without
// URL evidence. It looks through the supplied hits for the best one — a hit
// naming both the claim and the primary company beats a hit naming the claim —
// and attaches it as the evidence. A claim no hit mentions stays evidence-free,
// so the normalizer's URL requirement drops it exactly as before: a fact the
// searches never surfaced cannot be rescued into the report.
func attachEvidenceFromHits(evidence []models.RelatedEntityEvidence, name, company string, hits []models.SearchResult) []models.RelatedEntityEvidence {
	if hasRelatedEvidenceURL(evidence) {
		return evidence
	}
	if hit := hitMentioning(name, company, hits); hit != nil {
		return []models.RelatedEntityEvidence{{
			URL:     hit.Link,
			Title:   hit.Title,
			Snippet: truncateRunes(strings.TrimSpace(hit.Snippet), relatedEntitySnippetMaxRunes),
		}}
	}
	return evidence
}

// hitMentioning picks the hit that best grounds a name: the first hit whose
// title or snippet names both the claim and the primary company, then the first
// hit that merely names the claim.
func hitMentioning(name, company string, hits []models.SearchResult) *models.SearchResult {
	nameLower := strings.ToLower(strings.TrimSpace(name))
	companyLower := strings.ToLower(strings.TrimSpace(company))
	if nameLower == "" {
		return nil
	}
	var first *models.SearchResult
	for index := range hits {
		text := strings.ToLower(hits[index].Title + " " + hits[index].Snippet)
		if !strings.Contains(text, nameLower) {
			continue
		}
		if companyLower == "" || strings.Contains(text, companyLower) {
			return &hits[index]
		}
		if first == nil {
			first = &hits[index]
		}
	}
	return first
}

func reviewEvidence(trace *models.SearchTrace, limit int) []models.RelatedEntityEvidence {
	if trace == nil || limit <= 0 {
		return nil
	}
	evidence := make([]models.RelatedEntityEvidence, 0, limit)
	for _, query := range trace.Queries {
		for _, hit := range query.Kept {
			evidence = append(evidence, models.RelatedEntityEvidence{
				URL: hit.Link, Title: hit.Title, Snippet: hit.Snippet, Source: query.Purpose,
			})
			if len(evidence) == limit {
				return evidence
			}
		}
	}
	return evidence
}
