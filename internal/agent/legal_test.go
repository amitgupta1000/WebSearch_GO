package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// fakeLegalSource stands in for the IndianKanoon client: canned hits per query
// prefix, plus a canned document for the confirm pass.
type fakeLegalSource struct {
	hits        map[string][]tools.LegalSearchHit
	documents   map[string]*tools.LegalDocument
	searchCalls []string
	docCalls    []string
}

func (f *fakeLegalSource) Search(_ context.Context, query string, page, maxResults int) ([]tools.LegalSearchHit, error) {
	f.searchCalls = append(f.searchCalls, query)
	// Exact query match: the plan's quoted queries are distinct, and substring
	// matching is ambiguous when one query names both the company and a person.
	hits, exists := f.hits[query]
	if !exists {
		return nil, nil
	}
	if maxResults > 0 && len(hits) > maxResults {
		return hits[:maxResults], nil
	}
	return hits, nil
}

func (f *fakeLegalSource) FetchDocument(_ context.Context, docURL string) (*tools.LegalDocument, error) {
	f.docCalls = append(f.docCalls, docURL)
	if document, exists := f.documents[docURL]; exists {
		return document, nil
	}
	return nil, errors.New("no such document")
}

// legalClassifyLLM fills the classification target the way a real model would:
// one row per finding the prompt carries, keyed by index.
type legalClassifyLLM struct{}

func (legalClassifyLLM) GenerateStructured(_ context.Context, _, userPrompt string, target any) error {
	value, ok := target.(*legalClassificationTarget)
	if !ok {
		return nil
	}
	for _, line := range strings.Split(userPrompt, "\n") {
		// The prompt's finding rows look like "0. \"title\" | court=…".
		if len(line) > 2 && line[0] >= '0' && line[0] <= '9' && line[1] == '.' && line[2] == ' ' {
			index := int(line[0] - '0')
			value.Findings = append(value.Findings, legalClassificationRow{
				Index:       index,
				Category:    "insolvency",
				Status:      "pending",
				Involvement: models.LegalPartyTo,
				Confidence:  0.8,
			})
		}
	}
	value.Summary = "One pending insolvency matter names the company."
	return nil
}

// legalFailingLLM simulates a model outage.
type legalFailingLLM struct{}

func (legalFailingLLM) GenerateStructured(_ context.Context, _ string, _ string, _ any) error {
	return errors.New("model unavailable")
}

// emptySearcher answers nothing: the legal tests drive the IndianKanoon fake
// directly, and the Casemine track must not leak the generic mock results in.
type emptySearcher struct{}

func (emptySearcher) Search(_ context.Context, _ string, _ int) ([]models.SearchResult, error) {
	return nil, nil
}

func newLegalTestEngine(t *testing.T, llm LLMClient) *Engine {
	t.Helper()
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	return NewEngine(llm, &emptySearcher{}, &mockScraper{}, store, 5, 15, 20)
}
func TestPlanLegalQueries(t *testing.T) {
	query := models.LegalQuery{
		CompanyName:    "Acme Robotics Pvt Ltd",
		Aliases:        []string{"Acme Labs", ""},
		RegistrationID: "L29100MH2019PLC123456",
		Persons:        []string{"Jane Doe", "John Roe"},
	}
	planned := planLegalQueries(query, 8, false)

	if len(planned) != 6 {
		t.Fatalf("planned %d queries, want 6 (company, CIN, alias, casemine, 2 persons)", len(planned))
	}
	if planned[0].Query != `"Acme Robotics Pvt Ltd"` || planned[0].Source != "indiankanoon" {
		t.Errorf("first query = %q via %s, want the company name on indiankanoon", planned[0].Query, planned[0].Source)
	}
	if planned[1].Query != `"L29100MH2019PLC123456"` {
		t.Errorf("second query = %q, want the CIN search", planned[1].Query)
	}
	if planned[2].Query != `"Acme Labs"` {
		t.Errorf("third query = %q, want the alias", planned[2].Query)
	}
	if !strings.Contains(planned[3].Query, "site:casemine.com") {
		t.Errorf("fourth query = %q, want the casemine snippet track", planned[3].Query)
	}
	if planned[4].Query != `"Jane Doe" "Acme Robotics Pvt Ltd"` {
		t.Errorf("person query = %q, want name + company co-occurrence", planned[4].Query)
	}

	tight := planLegalQueries(query, 2, false)
	if len(tight) != 2 {
		t.Fatalf("budget=2 planned %d queries, want 2", len(tight))
	}
	// The strongest queries come first when the budget is tight: company, then CIN.
	if tight[1].Query != `"L29100MH2019PLC123456"` {
		t.Errorf("tight budget second query = %q, want the CIN ahead of aliases and persons", tight[1].Query)
	}
}

func TestPlanLegalQueriesNativeCasemine(t *testing.T) {
	query := models.LegalQuery{CompanyName: "Acme Robotics Pvt Ltd"}
	planned := planLegalQueries(query, 8, true)
	var casemine *legalPlannedQuery
	for i := range planned {
		if planned[i].Source == "casemine" {
			casemine = &planned[i]
		}
	}
	if casemine == nil {
		t.Fatal("the plan must carry a casemine query")
	}
	if casemine.Query != `"Acme Robotics Pvt Ltd"` {
		t.Errorf("native casemine query = %q, want the party name itself (the client fetches the site's own results page)", casemine.Query)
	}

	snippetPlanned := planLegalQueries(query, 8, false)
	for _, plannedQuery := range snippetPlanned {
		if plannedQuery.Source == "casemine" && !strings.HasPrefix(plannedQuery.Query, "site:casemine.com") {
			t.Errorf("fallback casemine query = %q, want the site-restricted snippet shape", plannedQuery.Query)
		}
	}
}

func TestLegalFindingFromHit(t *testing.T) {
	matcher := newEntityMatcher(models.CompanyBrief{
		CompanyName:    "Acme Robotics Pvt Ltd",
		Aliases:        []string{"Acme Labs"},
		RegistrationID: "L29100MH2019PLC123456",
	})
	companyPlan := legalPlannedQuery{Party: "Acme Robotics Pvt Ltd", PartyKind: "company", Source: "indiankanoon"}

	cinHit := tools.LegalSearchHit{Title: "Creditor vs Acme Robotics Pvt Ltd", Snippet: "CIN L29100MH2019PLC123456 appears in the order"}
	finding := legalFindingFromHit(cinHit, companyPlan, matcher, "L29100MH2019PLC123456", nil)
	if finding.MatchKey != models.LegalKeyCIN {
		t.Errorf("CIN hit MatchKey = %q, want cin", finding.MatchKey)
	}

	exactHit := tools.LegalSearchHit{Title: "Acme Robotics Pvt Ltd vs State Of Maharashtra on 5 November, 2004"}
	finding = legalFindingFromHit(exactHit, companyPlan, matcher, "", nil)
	if finding.MatchKey != models.LegalKeyName {
		t.Errorf("exact-name hit MatchKey = %q, want exact-name", finding.MatchKey)
	}

	aliasHit := tools.LegalSearchHit{Title: "Acme Labs merger approval order"}
	finding = legalFindingFromHit(aliasHit, companyPlan, matcher, "", nil)
	if finding.MatchKey != models.LegalKeyAlias {
		t.Errorf("alias hit MatchKey = %q, want alias", finding.MatchKey)
	}

	partialHit := tools.LegalSearchHit{Title: "Acme Traders vs Some Bank on 1 January, 2020"}
	finding = legalFindingFromHit(partialHit, companyPlan, matcher, "", nil)
	if finding.MatchKey != models.LegalKeyNameOnly {
		t.Errorf("partial-name hit MatchKey = %q, want name-only (same-named bucket)", finding.MatchKey)
	}

	personPlan := legalPlannedQuery{Party: "Jane Doe", PartyKind: "person", Source: "indiankanoon"}
	personHit := tools.LegalSearchHit{Title: "Jane Doe vs Acme Robotics Pvt Ltd"}
	finding = legalFindingFromHit(personHit, personPlan, matcher, "", nil)
	if finding.MatchKey != models.LegalKeyName || finding.PartyKind != "person" {
		t.Errorf("person co-occurrence: MatchKey = %q kind = %q, want exact-name / person", finding.MatchKey, finding.PartyKind)
	}
}

func TestLegalMatchWeightOrdersWeakestFirst(t *testing.T) {
	weights := []string{models.LegalKeyNameOnly, models.LegalKeyName, models.LegalKeyAlias, models.LegalKeyCIN}
	for i := 1; i < len(weights); i++ {
		if legalMatchWeight(weights[i-1]) >= legalMatchWeight(weights[i]) {
			t.Errorf("weight(%s) must be below weight(%s)", weights[i-1], weights[i])
		}
	}
}

func TestLegalDocKeyDedupes(t *testing.T) {
	if legalDocKey(tools.LegalSearchHit{DocID: "111"}) != "doc:111" {
		t.Error("a doc id keys by document")
	}
	if legalDocKey(tools.LegalSearchHit{Title: "Acme vs State"}) != "title:acme vs state" {
		t.Error("a title-only hit keys by normalised title")
	}
}
func TestResearchLegalClassifiesAndBuckets(t *testing.T) {
	source := &fakeLegalSource{
		hits: map[string][]tools.LegalSearchHit{
			`"Acme Robotics Pvt Ltd"`: {
				{Title: "Acme Robotics Pvt Ltd vs State Of Maharashtra on 5 November, 2004", Court: "Supreme Court of India", Date: "5 November, 2004", DocID: "111", DocURL: "/doc/111/", Snippet: "winding up petition"},
				{Title: "Section 115JB in The Income Tax Act, 1961", DocType: "statute", DocID: "222", DocURL: "/doc/222/"},
				{Title: "Acme Traders vs Some Bank on 1 January, 2020", Court: "Bombay High Court", DocID: "333", DocURL: "/doc/333/"},
			},
			`"Jane Doe" "Acme Robotics Pvt Ltd"`: {
				{Title: "Jane Doe vs Acme Robotics Pvt Ltd on 2 February, 2021", Court: "NCLT Mumbai", DocID: "444", DocURL: "/doc/444/"},
			},
		},
	}
	agent := NewLegalAgent(newLegalTestEngine(t, legalClassifyLLM{}), source, nil, 8, 0, 1)

	report, err := agent.ResearchLegal(context.Background(), "s1", models.LegalQuery{
		CompanyName: "Acme Robotics Pvt Ltd",
		Persons:     []string{"Jane Doe"},
	}, nil)
	if err != nil {
		t.Fatalf("ResearchLegal failed: %v", err)
	}

	if len(report.Findings) != 2 {
		t.Fatalf("findings = %d, want 2 (exact name + person co-occurrence)", len(report.Findings))
	}
	if len(report.SameNamed) != 1 {
		t.Fatalf("same-named = %d, want 1 (the Acme Traders hit)", len(report.SameNamed))
	}
	if report.SameNamed[0].MatchKey != models.LegalKeyNameOnly {
		t.Errorf("same-named MatchKey = %q, want name-only", report.SameNamed[0].MatchKey)
	}

	// Classification ran: the model's category and summary are in the report.
	for _, finding := range report.Findings {
		if !finding.Classified || finding.Category != "insolvency" || finding.Status != "pending" {
			t.Errorf("finding %q not classified: %+v", finding.CaseTitle, finding)
		}
	}
	if !strings.Contains(report.Summary, "pending insolvency") {
		t.Errorf("summary = %q, want the model narrative", report.Summary)
	}

	// Statute entries are excluded from the trace's kept hits but recorded.
	var statuteExcluded bool
	for _, queryTrace := range report.SearchTrace.Queries {
		for _, hit := range queryTrace.Excluded {
			if strings.Contains(hit.Title, "Income Tax Act") {
				statuteExcluded = true
			}
		}
	}
	if !statuteExcluded {
		t.Error("the statute hit must appear in the trace's excluded list")
	}

	// The named person was searched and her finding is grouped.
	if len(report.Persons) != 1 || !report.Persons[0].Checked || len(report.Persons[0].Findings) != 1 {
		t.Fatalf("persons = %+v, want one checked person with one finding", report.Persons)
	}

	// Both sources reported themselves, in the stable order.
	if len(report.Sources) != 2 || report.Sources[0].Name != "indiankanoon" {
		t.Errorf("sources = %+v, want indiankanoon first", report.Sources)
	}
	if report.Sources[0].Unavailable != "" {
		t.Errorf("indiankanoon should have answered, got %q", report.Sources[0].Unavailable)
	}
}

func TestResearchLegalModelFailureKeepsFindings(t *testing.T) {
	source := &fakeLegalSource{
		hits: map[string][]tools.LegalSearchHit{
			`"Acme Robotics Pvt Ltd"`: {
				{Title: "Acme Robotics Pvt Ltd vs State Of Maharashtra", Court: "Supreme Court of India", DocID: "111", DocURL: "/doc/111/"},
			},
		},
	}
	agent := NewLegalAgent(newLegalTestEngine(t, legalFailingLLM{}), source, nil, 8, 0, 1)

	report, err := agent.ResearchLegal(context.Background(), "s2", models.LegalQuery{CompanyName: "Acme Robotics Pvt Ltd"}, nil)
	if err != nil {
		t.Fatalf("a model outage must not fail the run: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want the hit to survive", len(report.Findings))
	}
	if report.Findings[0].Classified {
		t.Error("findings must stay unclassified when the model is down")
	}
	if !strings.Contains(report.Summary, "Classification was unavailable") {
		t.Errorf("summary = %q, want the deterministic fallback", report.Summary)
	}
}

func TestResearchLegalReportsUnavailableSource(t *testing.T) {
	agent := NewLegalAgent(newLegalTestEngine(t, legalFailingLLM{}), &failingLegalSource{}, nil, 4, 0, 1)

	report, err := agent.ResearchLegal(context.Background(), "s3", models.LegalQuery{CompanyName: "Acme Robotics Pvt Ltd"}, nil)
	if err != nil {
		t.Fatalf("a failed source must not fail the run: %v", err)
	}
	found := false
	for _, src := range report.Sources {
		if src.Name == "indiankanoon" && src.Unavailable != "" {
			found = true
		}
	}
	if !found {
		t.Error("a dead source must carry its unavailable reason, not vanish")
	}
}

type failingLegalSource struct{}

func (failingLegalSource) Search(_ context.Context, _ string, _, _ int) ([]tools.LegalSearchHit, error) {
	return nil, errors.New("network unreachable")
}

func (failingLegalSource) FetchDocument(_ context.Context, _ string) (*tools.LegalDocument, error) {
	return nil, errors.New("network unreachable")
}

func TestResearchLegalUnbudgetedPersonIsNamed(t *testing.T) {
	source := &fakeLegalSource{
		hits: map[string][]tools.LegalSearchHit{
			`"Acme Robotics Pvt Ltd"`: {
				{Title: "Acme Robotics Pvt Ltd vs State", DocID: "111", DocURL: "/doc/111/"},
			},
		},
	}
	// Two queries of budget: company + casemine only; the person never runs.
	agent := NewLegalAgent(newLegalTestEngine(t, legalFailingLLM{}), source, nil, 2, 0, 1)

	report, err := agent.ResearchLegal(context.Background(), "s4", models.LegalQuery{
		CompanyName: "Acme Robotics Pvt Ltd",
		Persons:     []string{"Jane Doe"},
	}, nil)
	if err != nil {
		t.Fatalf("ResearchLegal failed: %v", err)
	}
	if len(report.Persons) != 1 {
		t.Fatalf("persons = %d, want the requested person listed", len(report.Persons))
	}
	if report.Persons[0].Checked {
		t.Error("the person's query never ran, so checked must be false")
	}
	if report.Persons[0].SkippedReason == "" {
		t.Error("an unchecked person must name why")
	}
}

// discoverySearcher answers the registry-style queries the directors pass runs,
// with snippets that name the company — and one that does not, so the
// company-linked gate has something to exclude.
type discoverySearcher struct{}

func (discoverySearcher) Search(_ context.Context, query string, _ int) ([]models.SearchResult, error) {
	if strings.Contains(query, "directors") {
		return []models.SearchResult{
			{Title: "Acme Robotics Pvt Ltd Directors: Meera Iyer, Arjun Khanna", Link: "https://www.zaubacorp.com/company/Acme", Snippet: "Board of directors of Acme Robotics Pvt Ltd includes Meera Iyer and Arjun Khanna as of the latest filing"},
			{Title: "Unrelated Traders Directors: Some Name", Link: "https://www.otherhost.com/x", Snippet: "Board of directors of Unrelated Traders — nothing to do with the researched party"},
		}, nil
	}
	return nil, nil
}

// discoveryLLM extracts two directors from the company-linked row (dropping the
// company name and a duplicate), and classifies findings generically.
type discoveryLLM struct{}

func (discoveryLLM) GenerateStructured(_ context.Context, systemPrompt string, _ string, target any) error {
	switch value := target.(type) {
	case *directorExtractionTarget:
		if strings.Contains(systemPrompt, "directors") {
			value.Directors = []directorExtractionRow{
				{Row: 0, Name: "Meera Iyer"},
				{Row: 0, Name: "Arjun Khanna"},
				{Row: 0, Name: "Meera Iyer"},
				{Row: 0, Name: "Acme Robotics Pvt Ltd"},
				{Row: 9, Name: "Out Of Range"},
			}
		}
	case *legalClassificationTarget:
		value.Summary = "checked"
	}
	return nil
}

func TestResearchLegalDiscoversDirectors(t *testing.T) {
	source := &fakeLegalSource{
		hits: map[string][]tools.LegalSearchHit{
			`"Meera Iyer" "Acme Robotics Pvt Ltd"`: {
				{Title: "Meera Iyer vs Acme Robotics Pvt Ltd on 3 March, 2022", Court: "NCLT Mumbai", DocID: "555", DocURL: "/doc/555/"},
			},
			`"Arjun Khanna" "Acme Robotics Pvt Ltd"`: nil,
		},
	}
	// The engine's searcher answers the discovery queries; the fake answers the
	// person searches that follow.
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	engine := NewEngine(discoveryLLM{}, discoverySearcher{}, &mockScraper{}, store, 5, 15, 20)
	agent := NewLegalAgent(engine, source, nil, 8, 0, 1)

	report, err := agent.ResearchLegal(context.Background(), "s5", models.LegalQuery{
		CompanyName:      "Acme Robotics Pvt Ltd",
		IncludeDirectors: true,
	}, nil)
	if err != nil {
		t.Fatalf("ResearchLegal failed: %v", err)
	}

	if len(report.Persons) != 2 {
		t.Fatalf("persons = %d (%+v), want the two discovered directors", len(report.Persons), report.Persons)
	}
	if report.Persons[0].Name != "Meera Iyer" || report.Persons[1].Name != "Arjun Khanna" {
		t.Errorf("persons = %q, %q; want extraction order with duplicates and the company itself dropped", report.Persons[0].Name, report.Persons[1].Name)
	}
	for _, person := range report.Persons {
		if person.DiscoveredVia == "" {
			t.Errorf("person %q must carry how the run discovered them", person.Name)
		}
		if !person.Checked {
			t.Errorf("person %q was searched (budget allows it), so checked must be true", person.Name)
		}
	}
	if len(report.Persons[0].Findings) != 1 {
		t.Errorf("Meera Iyer's case must be grouped under her: %+v", report.Persons[0].Findings)
	}

	// The discovery pass reports itself.
	var discovery *models.LegalSourceReport
	for i, src := range report.Sources {
		if src.Name == directorsDiscoverySource {
			discovery = &report.Sources[i]
		}
	}
	if discovery == nil {
		t.Fatal("the discovery pass must report itself as a source")
	}
	if discovery.Queries != 2 || discovery.Kept != 2 {
		t.Errorf("discovery report = %+v, want 2 queries and 2 discovered persons", discovery)
	}

	// The discovery queries are in the trace with their purpose, and the
	// unrelated-company snippet sits in the excluded list.
	var tracedDiscovery bool
	for _, queryTrace := range report.SearchTrace.Queries {
		if strings.Contains(queryTrace.Purpose, "directors") {
			tracedDiscovery = true
			if len(queryTrace.Excluded) != 1 {
				t.Errorf("the unrelated-company snippet must sit in the excluded list: %+v", queryTrace.Excluded)
			}
		}
	}
	if !tracedDiscovery {
		t.Error("the discovery searches must appear in the search trace")
	}
}

func TestResearchLegalToggleOffSkipsDiscovery(t *testing.T) {
	agent := NewLegalAgent(newLegalTestEngine(t, legalFailingLLM{}), &fakeLegalSource{hits: map[string][]tools.LegalSearchHit{}}, nil, 8, 0, 1)

	report, err := agent.ResearchLegal(context.Background(), "s6", models.LegalQuery{CompanyName: "Acme Robotics Pvt Ltd"}, nil)
	if err != nil {
		t.Fatalf("ResearchLegal failed: %v", err)
	}
	for _, src := range report.Sources {
		if src.Name == directorsDiscoverySource {
			t.Error("with the toggle off the discovery pass must not appear in the report at all")
		}
	}
}

func TestLegalSnippetHost(t *testing.T) {
	if got := legalSnippetHost("https://www.zaubacorp.com/company/x"); got != "www.zaubacorp.com" {
		t.Errorf("legalSnippetHost = %q", got)
	}
	if got := legalSnippetHost("not a url"); got != "search snippet" {
		t.Errorf("legalSnippetHost = %q, want the fallback source name", got)
	}
}

func TestLegalCaseKeyStripsDates(t *testing.T) {
	// The same matter at two stages: IndianKanoon titles carry the date,
	// Casemine's do not — both must fold to one key.
	a := legalCaseKey("Acme Robotics Pvt Ltd vs State Of Maharashtra on 5 November, 2004")
	b := legalCaseKey("Acme Robotics Pvt Ltd vs State Of Maharashtra on 13 August, 2010")
	c := legalCaseKey("Acme Robotics Pvt Ltd vs State Of Maharashtra")
	if a != b || a != c {
		t.Errorf("case keys differ across stages: %q / %q / %q", a, b, c)
	}
}

func TestFoldCaseIterationsKeepsLatest(t *testing.T) {
	findings := []models.LegalFinding{
		{CaseTitle: "Acme Robotics Pvt Ltd vs State Of Maharashtra on 5 November, 2004", Court: "Bombay High Court", Date: "5 November, 2004", MatchKey: models.LegalKeyName, URL: "/doc/1/", Snippet: "trial stage order"},
		{CaseTitle: "Acme Robotics Pvt Ltd vs State Of Maharashtra on 13 August, 2010", Court: "Supreme Court of India", Date: "Aug 13, 2010", MatchKey: models.LegalKeyName, URL: "/doc/2/", Snippet: "appeal decided"},
		{CaseTitle: "Acme Traders vs Some Bank on 1 January, 2020", MatchKey: models.LegalKeyNameOnly, URL: "/doc/3/"},
	}
	sameNamed := []models.LegalFinding{
		// An early name-only record of the same matter as the two matched
		// stages: the folded group must re-grade to exact-name and leave the
		// same-named bucket.
		{CaseTitle: "Acme Robotics Pvt Ltd vs State Of Maharashtra", MatchKey: models.LegalKeyNameOnly, URL: "/doc/9/"},
	}

	folded := foldCaseIterations(&findings, &sameNamed)
	if folded != 2 {
		t.Fatalf("folded = %d, want 2 (two earlier records folded)", folded)
	}
	// The Acme-vs-State matter folds to one exact-name finding; the unrelated
	// Acme Traders matter is a separate name-only record and stays put.
	if len(findings) != 1 || len(sameNamed) != 1 {
		t.Fatalf("findings = %d, sameNamed = %d; want 1 folded finding and the unrelated same-named record", len(findings), len(sameNamed))
	}

	// The latest record wins the row.
	survivor := findings[0]
	if survivor.URL != "/doc/2/" {
		t.Errorf("survivor URL = %q, want the latest stage's document", survivor.URL)
	}
	if survivor.Iterations != 3 {
		t.Errorf("survivor Iterations = %d, want 3 (trial + appeal + name-only record)", survivor.Iterations)
	}
	if survivor.Court != "Supreme Court of India" {
		t.Errorf("survivor Court = %q, want the latest stage's court", survivor.Court)
	}
	if survivor.MatchKey != models.LegalKeyName {
		t.Errorf("survivor MatchKey = %q, want the matter's strongest class (exact-name)", survivor.MatchKey)
	}
	if sameNamed[0].CaseTitle != "Acme Traders vs Some Bank on 1 January, 2020" {
		t.Errorf("the unrelated matter must not fold into the company's: %q", sameNamed[0].CaseTitle)
	}
}

func TestLegalPartyRoleReadsSides(t *testing.T) {
	involvement, role := legalPartyRole("Acme Robotics Pvt Ltd vs State Of Maharashtra on 5 November, 2004", "Acme Robotics Pvt Ltd", nil, true)
	if involvement != models.LegalPartyTo || role != models.LegalRoleInitiated {
		t.Errorf("left-side company: involvement=%q role=%q, want party/initiated", involvement, role)
	}

	involvement, role = legalPartyRole("State Of Maharashtra vs Acme Robotics Pvt Ltd on 5 November, 2004", "Acme Robotics Pvt Ltd", nil, true)
	if involvement != models.LegalPartyTo || role != models.LegalRoleFacing {
		t.Errorf("right-side company: involvement=%q role=%q, want party/facing", involvement, role)
	}

	// A confirmed alias counts as the party on a side.
	involvement, role = legalPartyRole("Acme Labs vs Some Creditor", "Acme Robotics Pvt Ltd", []string{"Acme Labs"}, true)
	if involvement != models.LegalPartyTo || role != models.LegalRoleInitiated {
		t.Errorf("alias on left side: involvement=%q role=%q, want party/initiated", involvement, role)
	}

	// Matched only by snippet, not by a title side: mentioned, no role.
	involvement, role = legalPartyRole("Someone Else vs Another Party on 1 January, 2020", "Acme Robotics Pvt Ltd", nil, true)
	if involvement != models.LegalMentioned || role != "" {
		t.Errorf("mentioned: involvement=%q role=%q, want mentioned/empty", involvement, role)
	}

	// No sides in the title: party at most, never a role guess.
	involvement, role = legalPartyRole("In re Acme Robotics Pvt Ltd", "Acme Robotics Pvt Ltd", nil, true)
	if involvement != models.LegalPartyTo || role != "" {
		t.Errorf("no sides: involvement=%q role=%q, want party/empty", involvement, role)
	}
}

func TestLegalFallbackRiskLevels(t *testing.T) {
	// A pending responding-side criminal matter escalates to high.
	high := legalFallbackRisk([]models.LegalFinding{
		{CaseTitle: "State vs Acme Robotics Pvt Ltd", Role: models.LegalRoleFacing, Status: "pending", Category: "criminal"},
	})
	if high.Level != "high" {
		t.Errorf("pending facing criminal: level = %q, want high", high.Level)
	}
	if len(high.Drivers) == 0 || !strings.Contains(high.Drivers[0], "State vs Acme") {
		t.Errorf("drivers should name the specific matter: %v", high.Drivers)
	}

	// A pending responding-side civil matter is moderate.
	moderate := legalFallbackRisk([]models.LegalFinding{
		{CaseTitle: "Creditor vs Acme Robotics Pvt Ltd", Role: models.LegalRoleFacing, Status: "pending", Category: "civil"},
	})
	if moderate.Level != "moderate" {
		t.Errorf("pending facing civil: level = %q, want moderate", moderate.Level)
	}

	// Only decided responding-side matters stay low, but are counted.
	low := legalFallbackRisk([]models.LegalFinding{
		{CaseTitle: "Creditor vs Acme Robotics Pvt Ltd", Role: models.LegalRoleFacing, Status: "decided"},
		{CaseTitle: "Acme Robotics Pvt Ltd vs Defaulting Vendor", Role: models.LegalRoleInitiated, Status: "pending"},
	})
	if low.Level != "low" {
		t.Errorf("decided facing + initiated: level = %q, want low", low.Level)
	}

	// No findings at all: none.
	if legalFallbackRisk(nil).Level != "none" {
		t.Error("no findings must assess to none")
	}
}
