package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

const (
	relatedEntityMaxQueries        = 48
	relatedEntityMaxDirectQueries  = 32
	relatedEntityMaxLeaderQueries  = 8
	relatedEntityMaxLeaders        = 16
	relatedEntitySearchConcurrency = 8
	relatedEntityDirectEvidence    = 40
	relatedEntityLeaderEvidence    = 30
	relatedEntitySnippetMaxRunes   = 1200
)

// RelatedEntityAgent discovers direct company relationships and then follows
// confirmed leadership into other companies. It never mutates CompanyBrief.
type RelatedEntityAgent struct {
	engine *Engine
}

func NewRelatedEntityAgent(engine *Engine) *RelatedEntityAgent {
	return &RelatedEntityAgent{engine: engine}
}

func (a *RelatedEntityAgent) Research(ctx context.Context, sessionID string, query models.RelatedEntityQuery, eventCh chan<- models.AgentEvent) (*models.RelatedEntityReport, error) {
	query.CompanyName = strings.TrimSpace(query.CompanyName)
	if query.CompanyName == "" {
		return nil, fmt.Errorf("company name is required")
	}
	if query.MaxLeaders <= 0 || query.MaxLeaders > relatedEntityMaxLeaders {
		query.MaxLeaders = relatedEntityMaxLeaders
	}

	directQueries := relatedDirectQueries(query)
	leaderQueries := relatedLeadershipQueries(query)
	if len(directQueries) > relatedEntityMaxDirectQueries {
		directQueries = directQueries[:relatedEntityMaxDirectQueries]
	}
	if len(leaderQueries) > relatedEntityMaxLeaderQueries {
		leaderQueries = leaderQueries[:relatedEntityMaxLeaderQueries]
	}
	allQueries := append(append([]relatedPlannedQuery{}, directQueries...), leaderQueries...)
	if len(allQueries) > relatedEntityMaxQueries {
		allQueries = allQueries[:relatedEntityMaxQueries]
	}
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

	directHits := flattenTrace(trace, 0, len(directQueries))
	leadHits := flattenTrace(trace, len(directQueries), len(allQueries))
	report := &models.RelatedEntityReport{
		CompanyName: query.CompanyName, Domain: query.Domain, RegistrationID: query.RegistrationID,
		EntityConfirmed: strings.TrimSpace(query.Domain) != "" || strings.TrimSpace(query.RegistrationID) != "",
		QueriesUsed:     used, QueriesPlanned: len(allQueries), SearchTrace: trace,
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
	return []relatedPlannedQuery{
		{name + " (director OR founder OR chairman OR CEO OR CFO OR COO OR " + "managing director" + ")", "company-linked leadership"},
		{(name + " directors executives") + " site:linkedin.com", "LinkedIn-indexed leadership"},
		{name + " directors executives " + sourceHint(query), "regional leadership directories"},
	}
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

func (a *RelatedEntityAgent) extract(ctx context.Context, query models.RelatedEntityQuery, directHits, leaderHits []models.SearchResult, report *models.RelatedEntityReport) error {
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
	report.Leadership = normalizeLeaders(leaderExtracted.Leadership, query.CompanyName, query.MaxLeaders)
	if len(report.Leadership) == 0 {
		return nil
	}
	return a.searchLeadership(ctx, query, report)
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
	return graph
}

func (a *RelatedEntityAgent) searchLeadership(ctx context.Context, query models.RelatedEntityQuery, report *models.RelatedEntityReport) error {
	type outcome struct {
		index int
		items []models.RelatedCompany
		err   error
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
				search := quote(leader.Name) + " (" + "director OR founder OR chairman OR CEO OR CFO OR board" + ") -" + quote(query.CompanyName)
				hits, err := a.engine.SearchCached(ctx, search, a.engine.MaxSearchResults())
				if err != nil {
					outcomes <- outcome{index: index, err: err}
					continue
				}
				var extracted []models.RelatedCompany
				payload, marshalErr := json.Marshal(struct {
					Person string
					Hits   []models.SearchResult
				}{leader.Name, hits})
				if marshalErr == nil {
					prompt := `Extract companies other than the primary company that this person is explicitly connected to in the snippets. Return an empty JSON array when evidence is insufficient. Include relationship, role, current/former status, confidence, and evidence. Do not report name-only matches.`
					marshalErr = a.engine.llmClient.GenerateStructured(ctx, prompt, string(payload), &extracted)
				}
				if marshalErr != nil {
					outcomes <- outcome{index: index, err: marshalErr}
					continue
				}
				for i := range extracted {
					extracted[i].Person = leader.Name
				}
				outcomes <- outcome{index: index, items: extracted}
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
