package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

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
	relatedEntityRocketReachQueries = 4
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
	rrLeaders := a.discoverRocketReach(ctx, query, report)
	if err := a.extract(ctx, query, directHits, leadHits, rrLeaders, report); err != nil {
		return nil, err
	}
	report.Graph = relatedEntityGraph(report)
	if len(report.Leadership) == 0 {
		report.Warnings = append(report.Warnings, "No company-linked directors or senior executives were identified in the returned snippets.")
	}
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
		{name + " (director OR founder OR chairman OR CEO OR CFO OR COO OR " + "managing director" + ")", "company-linked leadership"},
		{(name + " directors executives") + " site:linkedin.com", "LinkedIn-indexed leadership"},
		{name + " directors executives " + sourceHint(query), "regional leadership directories"},
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
	return "zoominfo OR apollo OR rocketreach OR theorg"
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

func (a *RelatedEntityAgent) extract(ctx context.Context, query models.RelatedEntityQuery, directHits, leaderHits []models.SearchResult, rrLeaders []models.LeadershipSubject, report *models.RelatedEntityReport) error {
	directPayload, err := json.Marshal(struct {
		Company string
		Hits    []models.SearchResult
	}{query.CompanyName, boundedRelatedEvidence(directHits, relatedEntityDirectEvidence)})
	if err != nil {
		return fmt.Errorf("prepare direct related-entity evidence: %w", err)
	}
	var directExtracted struct {
		DirectRelationships []models.RelatedCompany `json:"direct_relationships"`
	}
	if err := a.engine.llmClient.GenerateStructured(ctx, `Extract only explicitly stated direct corporate relationships for the primary company from the supplied Google-indexed results. Do not infer ownership from a name alone. Return JSON with direct_relationships; return an empty array when evidence is insufficient.`, string(directPayload), &directExtracted); err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Direct relationship extraction failed: %v", err))
	} else {
		report.DirectRelationships = normalizeRelated(directExtracted.DirectRelationships, query.CompanyName)
	}

	leaderPayload, err := json.Marshal(struct {
		Company string
		Hits    []models.SearchResult
	}{query.CompanyName, boundedRelatedEvidence(leaderHits, relatedEntityLeaderEvidence)})
	if err != nil {
		return fmt.Errorf("prepare leadership evidence: %w", err)
	}
	var leaderExtracted struct {
		Leadership []models.LeadershipSubject `json:"leadership"`
	}
	if err := a.engine.llmClient.GenerateStructured(ctx, `Extract company-linked directors and senior executives from the supplied Google-indexed results. Each person must have an explicit role at the primary company. Do not infer identity from a name alone. Return JSON with leadership; return an empty array when evidence is insufficient.`, string(leaderPayload), &leaderExtracted); err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Leadership extraction failed: %v", err))
		return nil
	}
	report.Leadership = normalizeLeaders(append(leaderExtracted.Leadership, rrLeaders...), query.CompanyName, query.MaxLeaders)
	if len(report.Leadership) == 0 {
		return nil
	}
	return a.searchLeadership(ctx, query, report)
}

func (a *RelatedEntityAgent) discoverRocketReach(ctx context.Context, query models.RelatedEntityQuery, report *models.RelatedEntityReport) []models.LeadershipSubject {
	if a.rocketReach == nil || !a.rocketReach.Enabled() {
		return nil
	}
	titleGroups := [][]string{
		{"director", "chief executive officer", "chief financial officer", "chief operating officer", "managing director"},
		{"procurement", "purchasing", "supply chain"},
		{"finance", "legal", "operations"},
		{"founder", "chairman", "president", "vice president"},
	}
	var leaders []models.LeadershipSubject
	seen := map[string]bool{}
	for _, titles := range titleGroups[:relatedEntityRocketReachQueries] {
		rrQuery := map[string][]string{
			"current_employer": {query.CompanyName},
			"current_title":    titles,
		}
		profiles, err := a.rocketReach.Search(ctx, rrQuery)
		if err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("RocketReach discovery failed for title group %q: %v", titles, err))
			continue
		}
		queryLabel := query.CompanyName + " / " + strings.Join(titles, ", ")
		for _, profile := range profiles {
			name := strings.TrimSpace(profile.Name)
			if name == "" || !rocketReachEmployerMatches(profile.CurrentEmployer, query.CompanyName) {
				continue
			}
			key := strings.ToLower(name + "\x00" + strings.TrimSpace(profile.CurrentEmployer))
			if seen[key] {
				continue
			}
			seen[key] = true
			report.RocketReachDiscovery = append(report.RocketReachDiscovery, models.RocketReachDiscovery{
				ProfileID: profile.ID, Name: name, Title: profile.CurrentTitle,
				Employer: profile.CurrentEmployer, LinkedInURL: profile.LinkedInURL, Query: queryLabel,
			})
			leaders = append(leaders, models.LeadershipSubject{
				Name: name, Role: profile.CurrentTitle, Company: profile.CurrentEmployer,
				Confidence: 0.75, Evidence: []models.RelatedEntityEvidence{{
					URL: profile.LinkedInURL, Title: name + " - " + profile.CurrentTitle,
					Snippet: name + " is listed at " + profile.CurrentEmployer + " as " + profile.CurrentTitle,
					Source:  "rocketreach",
				}},
			})
		}
	}
	return leaders
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
		hit.Snippet = truncateRunes(strings.TrimSpace(hit.Snippet), relatedEntitySnippetMaxRunes)
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
				identityQueries, affiliationQueries := relatedPersonQueries(query, leader)
				var personHits []models.SearchResult
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
				identity := resolvePersonIdentity(leader, query, personHits)
				if identity.Confidence < relatedEntityIdentityConfidence {
					outcomes <- outcome{index: index, trace: traces, identity: identity,
						err: fmt.Errorf("identity confidence %.2f is below the %.2f threshold", identity.Confidence, relatedEntityIdentityConfidence)}
					continue
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
				var extracted struct {
					Companies []relatedCompanyExtraction `json:"companies"`
				}
				payload, marshalErr := json.Marshal(struct {
					Person string
					Hits   []models.SearchResult
				}{leader.Name, boundedRelatedEvidence(personHits, relatedEntityLeaderEvidence)})
				if marshalErr == nil {
					prompt := `Extract companies other than the primary company that this person is explicitly connected to in the snippets. Return JSON with a companies array; return {"companies":[]} when evidence is insufficient. Include relationship, role, current/former status, confidence, and evidence. Do not report name-only matches.`
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

func relatedPersonQueries(query models.RelatedEntityQuery, leader models.LeadershipSubject) ([]relatedPlannedQuery, []relatedPlannedQuery) {
	person := quote(leader.Name)
	company := quote(query.CompanyName)
	identity := []relatedPlannedQuery{
		{person + " DIN director identification number", "person DIN identity"},
		{person + " " + company + " appointment director", "person-company appointment"},
		{person + " " + sourceHint(query), "person registry and directory records"},
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

func resolvePersonIdentity(leader models.LeadershipSubject, query models.RelatedEntityQuery, hits []models.SearchResult) *models.PersonIdentity {
	identity := &models.PersonIdentity{CanonicalName: leader.Name, Confidence: 0.35}
	seen := map[string]bool{}
	for _, hit := range hits {
		text := hit.Title + " " + hit.Snippet
		if strings.Contains(strings.ToLower(text), strings.ToLower(query.CompanyName)) {
			identity.Confidence = maxFloat(identity.Confidence, 0.60)
			identity.Attributes = appendUnique(identity.Attributes, "company-linked evidence")
		}
		if match := directorIdentificationNumberPattern.FindStringSubmatch(text); len(match) == 2 {
			identity.DIN = match[1]
			identity.Confidence = 1.0
			identity.Attributes = appendUnique(identity.Attributes, "DIN")
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

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
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
	Name          string          `json:"name"`
	Relationship  string          `json:"relationship"`
	Status        string          `json:"status"`
	CurrentFormer string          `json:"current_former"`
	Role          string          `json:"role"`
	Confidence    float64         `json:"confidence"`
	Evidence      json.RawMessage `json:"evidence"`
}

func (item relatedCompanyExtraction) RelatedCompany() models.RelatedCompany {
	status := item.Status
	if status == "" {
		status = item.CurrentFormer
	}
	return models.RelatedCompany{
		Name: item.Name, Relationship: item.Relationship, Status: status,
		Role: item.Role, Confidence: item.Confidence, Evidence: parseRelatedEvidence(item.Evidence),
	}
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
	seen := map[string]bool{}
	out := make([]models.LeadershipSubject, 0, limit)
	for _, leader := range leaders {
		leader.Name = strings.TrimSpace(leader.Name)
		if leader.Name == "" || seen[strings.ToLower(leader.Name)] {
			continue
		}
		leader.Company = company
		if leader.Confidence <= 0 {
			leader.Confidence = 0.5
		}
		seen[strings.ToLower(leader.Name)] = true
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
		if item.Name == "" || strings.EqualFold(item.Name, primary) || seen[strings.ToLower(item.Name)] {
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
