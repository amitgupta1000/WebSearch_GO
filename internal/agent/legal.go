package agent

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// legal.go is the legal mode agent: a bounded case-law check on a confirmed
// company identity plus the persons the operator named.
//
// The shape mirrors the other two modes but is deliberately smaller. Legal
// search has no planning model call, no scrape ladder and no QC loop: the run
// searches IndianKanoon (native client) and Casemine (through search-engine
// snippets, because the site walls plain clients), classifies what came back
// against the same entity keys a company run is held to, and reports. The hard
// part is identity — court titles name parties and a common business name
// matches dozens of unrelated entities — so the classification reuses
// newEntityMatcher and adds a same-named bucket instead of dropping anything:
// a legal check must never silently discard a hit that merely shares a name.
//
// Sources report themselves. A walled or empty source is named with its reason,
// because "no findings" must never be ambiguous between "none exist" and "we
// could not look".

const (
	// legalSearchTTL and legalDocTTL cache IndianKanoon responses. Judgments
	// do not change, so the cached search lives as long as a search cache.
	legalSearchTTL = 48 * time.Hour
	legalDocTTL    = 7 * 24 * time.Hour
	// legalMaxResultsPerQuery bounds what one source search page may contribute.
	legalMaxResultsPerQuery = 10
	// casemineMaxResultsPerQuery is what the Casemine track asks for: the site
	// serves twenty results per page (measured), so the client is asked for its
	// full page.
	casemineMaxResultsPerQuery = 20
	// defaultLegalQueriesPerRun and defaultLegalDocsPerRun mirror the config
	// defaults, applied when the constructor is given non-positive values.
	defaultLegalQueriesPerRun = 8
	defaultLegalDocsPerRun    = 4
	// defaultLegalPagesPerQuery is the maximum result pages one search may walk.
	// The adaptive walk usually stops earlier when pages repeat or stop matching
	// the confirmed entity. Page 1 is part of the search; every page past it
	// costs one run query.
	// IndianKanoon paginates natively (&pagenum=); Casemine is a single-page
	// source (measured: the site ignores the page parameter), so its walk ends
	// after page 1 regardless of this knob.
	defaultLegalPagesPerQuery = 5
	// legalSnippetCap bounds the snippet text carried per finding.
	legalSnippetCap        = 600
	analysisRelevanceYears = 10
)

// legalSource is the case-law search surface the agent needs. *tools.IndianKanoonClient
// satisfies it; tests substitute a fake, the same way the engine's Searcher and
// PageFetcher are mocked. page selects the results page (1-based); sources
// without pagination answer page 1 only.
type legalSource interface {
	Search(ctx context.Context, query string, page, maxResults int) ([]tools.LegalSearchHit, error)
	FetchDocument(ctx context.Context, docURL string) (*tools.LegalDocument, error)
}

// LegalAgent runs legal mode. It shares the Engine with the other modes for
// cached search (the Casemine snippet fallback rides Serper), the LLM client and
// Emit, and owns the two case-law clients.
type LegalAgent struct {
	engine        *Engine
	indianKanoon  legalSource
	casemine      *tools.CasemineClient
	juristway     legalSource
	queriesPerRun int
	docsPerRun    int
	pagesPerQuery int
	// concurrency bounds how many searches and document fetches run at once.
	// The per-source limiters keep each site polite regardless — the pool is
	// what lets an IndianKanoon wait overlap a Casemine fetch instead of
	// queueing behind it.
	concurrency int
}

// NewLegalAgent initializes the legal agent. Non-positive budgets restore the
// defaults, the same way the Engine's constructor does. The case-law sources are
// the real clients in production and fakes in tests; a nil Casemine client keeps
// the search-engine snippet track instead of the native one, and a nil
// Juristway client drops that track from the plan entirely.
func NewLegalAgent(engine *Engine, indianKanoon legalSource, casemine *tools.CasemineClient, juristway legalSource, queriesPerRun, docsPerRun, pagesPerQuery, concurrency int) *LegalAgent {
	if queriesPerRun <= 0 {
		queriesPerRun = defaultLegalQueriesPerRun
	}
	if docsPerRun <= 0 {
		docsPerRun = defaultLegalDocsPerRun
	}
	if pagesPerQuery <= 0 {
		pagesPerQuery = defaultLegalPagesPerQuery
	}
	if concurrency <= 0 {
		concurrency = defaultLegalConcurrency
	}
	return &LegalAgent{
		engine:        engine,
		indianKanoon:  indianKanoon,
		casemine:      casemine,
		juristway:     juristway,
		queriesPerRun: queriesPerRun,
		docsPerRun:    docsPerRun,
		pagesPerQuery: pagesPerQuery,
		concurrency:   concurrency,
	}
}

// defaultLegalConcurrency is how many legal searches may run at once. The
// per-source limiters (one request a second on the free IndianKanoon index, the
// scraper's own politeness on Casemine) keep any single site quiet whatever
// this is; the pool is what lets different sources progress together.
const defaultLegalConcurrency = 4

// legalBudget is the run's shared query allowance under bounded concurrency:
// continuation pages and person-search slots are claimed atomically, so
// parallel workers cannot overspend the run's budget.
type legalBudget struct {
	mu        sync.Mutex
	remaining int
}

func newLegalBudget(remaining int) *legalBudget {
	return &legalBudget{remaining: remaining}
}

// tryTake claims one unit of the allowance, reporting whether there was room.
func (b *legalBudget) tryTake() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining <= 0 {
		return false
	}
	b.remaining--
	return true
}

// left reports the unclaimed remainder.
func (b *legalBudget) left() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.remaining
}

// runBoundedLegal runs fn over the unit indices [0, units) with a bounded
// worker pool. fn writes its own outcome wherever it likes — callers index
// their result slices so the serial fold afterwards is in plan order and the
// artifacts stay deterministic whatever the scheduling did. A panic in fn fails
// that one unit, not the run: these goroutines sit outside net/http's
// per-request recovery, the same way the engine's scrape batch does.
func runBoundedLegal(units, concurrency int, fn func(index int)) {
	if units <= 0 {
		return
	}
	limit := concurrency
	if limit <= 0 {
		limit = 1
	}
	if limit > units {
		limit = units
	}
	if limit == 1 {
		for index := 0; index < units; index++ {
			fn(index)
		}
		return
	}
	semaphore := make(chan struct{}, limit)
	var waitGroup sync.WaitGroup
	for index := 0; index < units; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Printf("[ERROR] legal worker panicked (unit %d): %v\n%s", index, recovered, debug.Stack())
				}
			}()
			fn(index)
		}(index)
	}
	waitGroup.Wait()
}

// legalPlannedQuery is one bounded search the run will spend: which source,
// for which party, and why the artifact can name it.
type legalPlannedQuery struct {
	Query     string
	Purpose   string
	Source    string // "indiankanoon" or "casemine"
	Party     string
	PartyKind string // "company" or "person"
}

// legalParty is one searched party: the company or a named person. Confirmed
// aliases fold into the company party — their findings are the company's.
type legalParty struct {
	name string
	kind string
}

// planLegalQueries lays out the run's searches deterministically, strongest
// first, and caps them at the run's query budget. The CIN search comes before
// aliases (a CIN cannot collide), the company name before Casemine (the native
// source is the better one), Juristway after Casemine (its editorial corpus
// complements the raw-judgment sources), and persons last (they are the
// optional part of a run, and their queries spend two quoted names each). When
// the native Casemine client is present its query is the party name itself;
// without it the track rides the search engine's site-restricted snippets.
func planLegalQueries(query models.LegalQuery, budget int, casemineNative, juristwayNative bool) []legalPlannedQuery {
	companyName := strings.TrimSpace(query.CompanyName)
	var planned []legalPlannedQuery
	companyCap := budget
	if juristwayNative && companyCap > 1 {
		companyCap--
	}

	add := func(q legalPlannedQuery) {
		if len(planned) >= companyCap || strings.TrimSpace(q.Query) == "" {
			return
		}
		planned = append(planned, q)
	}

	// 1. The company's own name — the core party search.
	add(legalPlannedQuery{
		Query:     `"` + companyName + `"`,
		Purpose:   "company — party search",
		Source:    "indiankanoon",
		Party:     companyName,
		PartyKind: "company",
	})
	// 2. The registration id: NCLT and ROC documents cite the CIN, and a CIN
	// in a document is the one match that cannot be a same-named company.
	if regID := strings.TrimSpace(query.RegistrationID); regID != "" {
		add(legalPlannedQuery{
			Query:     `"` + regID + `"`,
			Purpose:   "company — registration id search",
			Source:    "indiankanoon",
			Party:     companyName,
			PartyKind: "company",
		})
	}
	// 3. Confirmed aliases: other names the operator folded into this entity.
	for _, alias := range query.Aliases {
		trimmed := strings.TrimSpace(alias)
		if trimmed == "" || strings.EqualFold(trimmed, companyName) {
			continue
		}
		add(legalPlannedQuery{
			Query:     `"` + trimmed + `"`,
			Purpose:   "company — confirmed alias",
			Source:    "indiankanoon",
			Party:     companyName,
			PartyKind: "company",
		})
	}
	// 4. The Casemine track: the native client fetches the site's own results
	// page (the measured access path), while without it the track rides
	// search-engine snippets, because the site walls other clients.
	casemineQuery := `site:casemine.com "` + companyName + `"`
	if casemineNative {
		casemineQuery = `"` + companyName + `"`
	}
	add(legalPlannedQuery{
		Query:     casemineQuery,
		Purpose:   "company — casemine results",
		Source:    "casemine",
		Party:     companyName,
		PartyKind: "company",
	})
	// 5. The Juristway track: a commercial case-law database whose robots.txt
	// explicitly allows programmatic /api/caseLaws access. Its corpus skews
	// toward high courts, tribunals and tax (GST) matters with editorial
	// headnotes — a complement to the raw judgments, not a duplicate. The
	// query is the party name itself: the API matches a regex term, not a
	// quoted phrase, and answers one page.
	if juristwayNative {
		juristwayQuery := legalPlannedQuery{
			Query:     companyName,
			Purpose:   "company — juristway records",
			Source:    "juristway",
			Party:     companyName,
			PartyKind: "company",
		}
		if len(planned) < budget && strings.TrimSpace(juristwayQuery.Query) != "" {
			planned = append(planned, juristwayQuery)
		}
	}
	// 6. Named persons, searched as name + company co-occurrence — the same
	// discipline as company mode's reverse lookups, because a bare person name
	// matches unrelated people.
	for _, person := range query.Persons {
		trimmed := strings.TrimSpace(person)
		if trimmed == "" {
			continue
		}
		add(legalPlannedQuery{
			Query:     `"` + trimmed + `" "` + companyName + `"`,
			Purpose:   "person — name with company",
			Source:    "indiankanoon",
			Party:     trimmed,
			PartyKind: "person",
		})
	}

	if len(planned) > budget {
		planned = planned[:budget]
	}
	return planned
}

// legalIdentityBrief projects the legal query's confirmed identity onto the
// brief shape newEntityMatcher compiles, so legal classification runs on
// exactly the same keys a company run is held to.
func legalIdentityBrief(query models.LegalQuery) models.CompanyBrief {
	return models.CompanyBrief{
		CompanyName:     strings.TrimSpace(query.CompanyName),
		Domain:          strings.TrimSpace(query.Domain),
		Aliases:         query.Aliases,
		RegistrationID:  strings.TrimSpace(query.RegistrationID),
		EntityConfirmed: query.EntityConfirmed,
		EntitySource:    query.EntitySource,
	}
}

// ResearchLegal runs the legal check: plan, search both sources, classify
// every hit against the confirmed identity, optionally fetch documents to firm
// up ambiguous matches, and report. It returns a report even when a source
// answered nothing — the run fails only when it could not establish the party
// or every source failed outright.
func (la *LegalAgent) ResearchLegal(ctx context.Context, sessionID string, query models.LegalQuery, eventCh chan<- models.AgentEvent) (*models.LegalReport, error) {
	query.CompanyName = strings.TrimSpace(query.CompanyName)
	if query.CompanyName == "" {
		return nil, fmt.Errorf("company name is required")
	}
	la.normalizePersons(&query)

	timer := newPhaseTimer()
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventThinking,
		Step:      40,
		Content:   fmt.Sprintf("Starting legal check for %s%s.", query.CompanyName, legalPersonSuffix(query.Persons)),
	})

	matcher := newEntityMatcher(legalIdentityBrief(query))
	identity := legalIdentityBrief(query)

	// --- Plan (deterministic; no model call) -------------------------------
	finishPlan := timer.start("plan")
	planned := planLegalQueries(query, la.queriesPerRun, la.casemine != nil, la.juristway != nil)
	var companyPlans, operatorPersonPlans []legalPlannedQuery
	for _, plannedQuery := range planned {
		if plannedQuery.PartyKind == "person" {
			operatorPersonPlans = append(operatorPersonPlans, plannedQuery)
			continue
		}
		companyPlans = append(companyPlans, plannedQuery)
	}
	finishPlan(fmt.Sprintf("%d company-track queries across %d source(s); %d named person(s) to check",
		len(companyPlans), legalSources(companyPlans), len(operatorPersonPlans)))

	// --- Search both sources (company track, bounded parallel) --------------
	finishSearch := timer.start("search")
	trace := &models.SearchTrace{}
	var findings, sameNamed []models.LegalFinding
	sources := map[string]*models.LegalSourceReport{}
	for _, name := range []string{"indiankanoon", "casemine"} {
		sources[name] = &models.LegalSourceReport{Name: name}
	}
	if la.juristway != nil {
		sources["juristway"] = &models.LegalSourceReport{Name: "juristway"}
	}
	seenDocs := map[string]bool{}
	budget := newLegalBudget(la.queriesPerRun - len(companyPlans))

	// The company-track searches run concurrently within the configured
	// bound. Outcomes are indexed by plan position, and the fold below is
	// serial in that order, so the findings and the search trace are
	// deterministic whatever the scheduling did.
	type legalQueryOutcome struct {
		kept     []tools.LegalSearchHit
		excluded []tools.LegalSearchHit
		err      error
	}
	outcomes := make([]legalQueryOutcome, len(companyPlans))
	runBoundedLegal(len(companyPlans), la.concurrency, func(index int) {
		outcomes[index].kept, outcomes[index].excluded, outcomes[index].err =
			la.executeLegalQuery(ctx, sessionID, companyPlans[index], matcher, eventCh, budget)
	})
	for index, plannedQuery := range companyPlans {
		report := sources[plannedQuery.Source]
		report.Queries++

		outcome := outcomes[index]
		if outcome.err != nil {
			log.Printf("[LEGAL][session %s] %s search failed: %v", sessionID, plannedQuery.Source, outcome.err)
			if report.Unavailable == "" {
				report.Unavailable = outcome.err.Error()
			}
			continue
		}
		report.Hits += len(outcome.kept) + len(outcome.excluded)
		// The trace is appended here, in plan order, so the searches artifact
		// is the same on every run however the workers interleaved.
		trace.Queries = append(trace.Queries, models.QueryTrace{
			Query:    plannedQuery.Query,
			Purpose:  plannedQuery.Purpose,
			Kept:     legalHitsToSearchResults(outcome.kept),
			Excluded: legalHitsToSearchResults(outcome.excluded),
		})
		foldLegalHits(outcome.kept, plannedQuery, matcher, query.RegistrationID, identity.Aliases, seenDocs, report, &findings, &sameNamed)
	}
	finishSearch(fmt.Sprintf("%d matched, %d same-named across %d company-track queries", len(findings), len(sameNamed), len(companyPlans)))

	// Reserved authority searches complement the case-law sources without
	// consuming the bounded IndianKanoon/Casemine/Juristway plan.
	if la.engine != nil {
		finishRegulatory := timer.start("regulatory")
		regulatoryReport := &models.LegalSourceReport{Name: "regulatory"}
		sources["regulatory"] = regulatoryReport
		regulatoryFindings := la.regulatoryOrdersScan(ctx, sessionID, identity, regulatoryReport, trace, eventCh)
		findings = append(findings, regulatoryFindings...)
		finishRegulatory(fmt.Sprintf("%d regulatory order(s) matched", len(regulatoryFindings)))
	}

	// --- Directors discovery (toggle-driven, company-linked) ----------------
	discoveredVia := map[string]string{}
	var discovered []discoveredPerson
	if query.IncludeDirectors {
		finishDiscovery := timer.start("discovery")
		discoveryReport := &models.LegalSourceReport{Name: directorsDiscoverySource}
		var usedByDiscovery int
		discovered, usedByDiscovery = la.discoverDirectors(ctx, sessionID, identity, matcher, query.Persons, trace, eventCh, budget.left(), discoveryReport)
		sources[directorsDiscoverySource] = discoveryReport
		for i := 0; i < usedByDiscovery; i++ {
			budget.tryTake()
		}
		for _, person := range discovered {
			discoveredVia[strings.ToLower(person.name)] = person.via
		}
		finishDiscovery(fmt.Sprintf("%d company-linked person(s) discovered in %d search(es)", len(discovered), usedByDiscovery))
	}
	discoveredPersons := make([]string, 0, len(discovered))
	for _, person := range discovered {
		discoveredPersons = append(discoveredPersons, person.name)
	}

	// --- Person track within the remaining budget (bounded parallel) -------
	finishPersons := timer.start("persons")
	personPlans := operatorPersonPlans
	for _, name := range discoveredPersons {
		personPlans = append(personPlans, legalPlannedQuery{
			Query:     `"` + name + `" "` + query.CompanyName + `"`,
			Purpose:   "person — discovered director",
			Source:    "indiankanoon",
			Party:     name,
			PartyKind: "person",
		})
	}
	// An operator-named person may also surface from discovery; one search
	// covers both, so the duplicates are dropped before the pool launches —
	// first occurrence wins, and operator-named plans come first.
	uniquePersonPlans := make([]legalPlannedQuery, 0, len(personPlans))
	seenPersons := map[string]bool{}
	for _, plannedQuery := range personPlans {
		key := strings.ToLower(plannedQuery.Party)
		if seenPersons[key] {
			continue
		}
		seenPersons[key] = true
		uniquePersonPlans = append(uniquePersonPlans, plannedQuery)
	}

	personOutcomes := make([]legalQueryOutcome, len(uniquePersonPlans))
	personRan := make([]bool, len(uniquePersonPlans))
	runBoundedLegal(len(uniquePersonPlans), la.concurrency, func(index int) {
		// Each worker claims its query slot atomically; a worker that cannot
		// is recorded as not searched rather than racing the budget.
		if !budget.tryTake() {
			return
		}
		personRan[index] = true
		personOutcomes[index].kept, personOutcomes[index].excluded, personOutcomes[index].err =
			la.executeLegalQuery(ctx, sessionID, uniquePersonPlans[index], matcher, eventCh, budget)
	})
	executedPersons := 0
	for index, plannedQuery := range uniquePersonPlans {
		if !personRan[index] {
			continue
		}
		executedPersons++
		report := sources[plannedQuery.Source]
		report.Queries++

		outcome := personOutcomes[index]
		if outcome.err != nil {
			log.Printf("[LEGAL][session %s] %s person search failed: %v", sessionID, plannedQuery.Source, outcome.err)
			if report.Unavailable == "" {
				report.Unavailable = outcome.err.Error()
			}
			continue
		}
		report.Hits += len(outcome.kept) + len(outcome.excluded)
		trace.Queries = append(trace.Queries, models.QueryTrace{
			Query:    plannedQuery.Query,
			Purpose:  plannedQuery.Purpose,
			Kept:     legalHitsToSearchResults(outcome.kept),
			Excluded: legalHitsToSearchResults(outcome.excluded),
		})
		foldLegalHits(outcome.kept, plannedQuery, matcher, query.RegistrationID, identity.Aliases, seenDocs, report, &findings, &sameNamed)
	}
	finishPersons(fmt.Sprintf("%d person search(es) run, %d left unchecked by the budget", executedPersons, len(uniquePersonPlans)-executedPersons))

	// --- Fold case iterations (same matter, latest record wins) -------------
	finishFold := timer.start("fold")
	folded := foldCaseIterations(&findings, &sameNamed)
	finishFold(fmt.Sprintf("%d earlier record(s) folded into their latest iteration", folded))

	// Findings are mutated in place from here on (document confirmation and
	// classification), so every later change lands in the report.
	pointers := make([]*models.LegalFinding, 0, len(findings)+len(sameNamed))
	for i := range findings {
		pointers = append(pointers, &findings[i])
	}
	for i := range sameNamed {
		pointers = append(pointers, &sameNamed[i])
	}

	// --- Confirm ambiguous matches against document text -------------------
	finishConfirm := timer.start("confirm")
	confirmed, fetched := la.confirmWithDocuments(ctx, sessionID, identity, pointers)
	// A same-named hit the document proved is the company moves out of the
	// same-named bucket; nothing is re-searched, only re-graded.
	var moved []models.LegalFinding
	for i := len(sameNamed) - 1; i >= 0; i-- {
		if sameNamed[i].MatchKey != models.LegalKeyNameOnly {
			moved = append(moved, sameNamed[i])
			sameNamed = append(sameNamed[:i], sameNamed[i+1:]...)
		}
	}
	findings = append(findings, moved...)
	finishConfirm(fmt.Sprintf("%d document(s), %d upgraded", fetched, confirmed))

	// --- Classify with the model (deterministic fallback) -------------------
	finishClassify := timer.start("classify")
	classified, _ := la.classifyFindings(ctx, sessionID, identity, pointers, eventCh)

	// Person seeds: operator-named first, then the discovered directors, each
	// with how they entered the run. The seeds come from what was *asked*, not
	// from what the plan could afford — a person the budget never reached is
	// exactly the one the report must name as unchecked.
	seeds := make([]personSeed, 0, len(query.Persons)+len(discoveredPersons))
	for _, person := range query.Persons {
		seeds = append(seeds, personSeed{name: person})
	}
	for _, person := range discoveredPersons {
		via := discoveredVia[strings.ToLower(person)]
		if via == "" {
			via = "discovered from a company-linked registry snippet"
		}
		seeds = append(seeds, personSeed{name: person, via: via})
	}

	finishClassify(map[bool]string{true: "model classification applied", false: "model classification unavailable; findings keep identity labels only"}[classified])

	// --- Dockets and deterministic risk assessment ---------------------------
	finishRisk := timer.start("risk")
	report := assembleLegalReport(query, identity, findings, sameNamed, seeds, sources, trace, timer)
	report.Dockets = buildDockets(findings)
	report.Summary = reportSummary(report.Dockets, len(findings), len(sameNamed), len(uniquePersonPlans)-executedPersons, classified)
	report.Risk = buildRiskPosition(report.Dockets, time.Now())
	facingCount := 0
	for _, docket := range report.Dockets {
		if docket.Category == docketCompanyLaw {
			facingCount += docket.Defendant
		}
	}
	finishRisk(fmt.Sprintf("level %s (deterministic; %d company-law matter(s) on the responding side)", report.Risk.Level, facingCount))

	// A person whose planned query never executed (budget) is named as such.
	for i := range report.Persons {
		if !report.Persons[i].Checked {
			if report.Persons[i].DiscoveredVia != "" {
				report.Persons[i].SkippedReason = "discovered, but the run's query budget was exhausted before this person was searched"
			} else {
				report.Persons[i].SkippedReason = "the run's query budget was exhausted before this person was searched"
			}
		}
	}

	// Exports run on a detached context inside the engine, so a run that used
	// its whole research budget still saves its results.
	if exports := la.engine.ExportLegal(ctx, report); len(exports) > 0 {
		report.Exports = exports
		Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolResult, Step: 46, ToolName: "exporter", Content: exportSummary(exports)})
	}

	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventComplete,
		Step:      45,
		Content:   fmt.Sprintf("Legal check complete for %s: %d finding(s), %d same-named.", query.CompanyName, report.FindingCount(), len(report.SameNamed)),
	})
	return report, nil
}

// normalizePersons trims and de-duplicates the requested person names so the
// plan and the report agree on who was asked for.
func (la *LegalAgent) normalizePersons(query *models.LegalQuery) {
	seen := map[string]bool{}
	var persons []string
	for _, person := range query.Persons {
		trimmed := strings.TrimSpace(person)
		if trimmed == "" || seen[strings.ToLower(trimmed)] {
			continue
		}
		seen[strings.ToLower(trimmed)] = true
		persons = append(persons, trimmed)
	}
	query.Persons = persons
}

// legalPersonSuffix renders the person list for the opening event.
func legalPersonSuffix(persons []string) string {
	if len(persons) == 0 {
		return ""
	}
	return fmt.Sprintf(" and %d named person(s)", len(persons))
}

// legalSources counts the distinct sources the plan touches.
func legalSources(planned []legalPlannedQuery) int {
	seen := map[string]bool{}
	for _, q := range planned {
		seen[q.Source] = true
	}
	return len(seen)
}

// legalDocKey is the dedupe key for a hit: the document id when the source
// carries one, otherwise the normalised title (the Casemine track has no ids).
func legalDocKey(hit tools.LegalSearchHit) string {
	if hit.DocID != "" {
		return "doc:" + hit.DocID
	}
	return "title:" + strings.ToLower(strings.Join(strings.Fields(hit.Title), " "))
}

// foldLegalHits dedupes and classifies one query's hits into the run's two
// buckets, counting per source. It is the shared body of the company and person
// loops, so both tracks classify identically.
func foldLegalHits(hits []tools.LegalSearchHit, planned legalPlannedQuery, matcher *entityMatcher, registrationID string, aliases []string, seenDocs map[string]bool, report *models.LegalSourceReport, findings, sameNamed *[]models.LegalFinding) {
	for _, hit := range hits {
		key := legalDocKey(hit)
		if seenDocs[key] {
			continue
		}
		seenDocs[key] = true
		finding := legalFindingFromHit(hit, planned, matcher, registrationID, aliases)
		if finding.MatchKey == models.LegalKeyNameOnly {
			*sameNamed = append(*sameNamed, finding)
			report.Kept++
			continue
		}
		*findings = append(*findings, finding)
		report.Kept++
	}
}

// legalMaxDiscoveredDirectors caps how many persons one discovery pass may
// contribute: the searches that follow each spend a run query, and a registry
// page that names twenty people is not a mandate to check all of them.
const legalMaxDiscoveredDirectors = 4

// legalDiscoveryQueries caps the searches the discovery pass may spend.
const legalDiscoveryQueries = 2

// directorsDiscoverySource is the report entry the directors-discovery pass
// answers for, so its searches and its yield are visible next to the other
// sources instead of disappearing into the run.
const directorsDiscoverySource = "directors discovery"

// legalCaseDateLayouts are the date shapes the two sources put on findings:
// Casemine's "Aug 13, 2010", IndianKanoon's "5 November, 2004" and
// "05/11/2004" (Indian day-first), and the ISO form.
var legalCaseDateLayouts = []string{
	"Jan 2, 2006",
	"January 2, 2006",
	"2 January, 2006",
	"02/01/2006",
	"2006-01-02",
}

// legalCaseDateSuffix matches the trailing " on <date>" an IndianKanoon case
// title carries, so two records of the same matter at different stages fold
// into one case key.
var legalCaseDateSuffix = regexp.MustCompile(`(?i)\s+on\s+[^,]*,?\s*\d{4}\s*$`)

// legalCaseKey groups dated records of the same matter: the case title with its
// trailing date suffix removed, punctuation levelled, whitespace collapsed.
// A case travels through stages — trial, appeal, revision — and each stage is a
// separate document with its own id and date; this key is what says they are
// the same matter. Titles without dates (Casemine's) are already stage-neutral.
func legalCaseKey(title string) string {
	cleaned := strings.ToLower(strings.Join(strings.Fields(title), " "))
	cleaned = legalCaseDateSuffix.ReplaceAllString(cleaned, "")
	cleaned = strings.Trim(cleaned, " .,;:")
	return cleaned
}

// legalCaseDate parses a finding's own date — the structured field first, then
// the title's "on <date>" suffix — so two records of one matter can be ordered.
// False means no date could be read, and the record keeps its search position.
func legalCaseDate(finding models.LegalFinding) (time.Time, bool) {
	candidates := []string{strings.TrimSpace(finding.Date)}
	if match := legalTitleDateExtract.FindStringSubmatch(finding.CaseTitle); match != nil {
		candidates = append(candidates, strings.TrimSpace(match[1]))
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		for _, layout := range legalCaseDateLayouts {
			if parsed, err := time.Parse(layout, candidate); err == nil {
				return parsed, true
			}
		}
	}
	return time.Time{}, false
}

// legalTitleDateExtract pulls the " on <date>" suffix out of a case title.
var legalTitleDateExtract = regexp.MustCompile(`(?i)\bon\s+([^,]+(?:,\s*\d{4})?|\d{2}/\d{2}/\d{4})\s*$`)

// legalTitleSideSplit matches the "v." / "vs" / "versus" separator an Indian
// case title uses between its two sides.
var legalTitleSideSplit = regexp.MustCompile(`(?i)\s+(?:vs?\.?|versus)\s+`)

// legalTitleSides splits "Petitioner v. Respondent on Date" into its two sides.
// False means the title has no sides to read (it is not a two-party case
// title), so no role can be parsed from it.
func legalTitleSides(title string) (left, right string, ok bool) {
	loc := legalTitleSideSplit.FindStringIndex(title)
	if loc == nil {
		return "", "", false
	}
	left = strings.TrimSpace(title[:loc[0]])
	right = strings.TrimSpace(title[loc[1]:])
	// The trailing date suffix sits on the right side; drop it so the side is
	// the party text only.
	right = strings.TrimSpace(legalCaseDateSuffix.ReplaceAllString(right, ""))
	if left == "" || right == "" {
		return "", "", false
	}
	return left, right, true
}

// legalSideNamesTheParty reports whether a title side carries the researched
// party: the searched name (a person), or the company's name or one of its
// confirmed aliases.
func legalSideNamesTheParty(side string, party string, aliases []string) bool {
	loweredSide := strings.ToLower(side)
	if party != "" && strings.Contains(loweredSide, strings.ToLower(party)) {
		return true
	}
	for _, alias := range aliases {
		trimmed := strings.TrimSpace(alias)
		if trimmed != "" && strings.Contains(loweredSide, strings.ToLower(trimmed)) {
			return true
		}
	}
	return false
}

// legalPartyRole reads how the researched party stands in a case from the
// title's side structure: the left side of "X v. Y" is the one initiating
// (petitioner, complainant, appellant), the right side is the one responding
// (respondent, defendant, accused). A party that appears in neither side but
// matched the record is only mentioned in it, and a title without sides cannot
// attribute a role at all.
func legalPartyRole(title, party string, aliases []string, matched bool) (involvement, role string) {
	left, right, ok := legalTitleSides(title)
	if !ok {
		if matched {
			return models.LegalPartyTo, ""
		}
		return "", ""
	}
	switch {
	case legalSideNamesTheParty(left, party, aliases):
		return models.LegalPartyTo, models.LegalRoleInitiated
	case legalSideNamesTheParty(right, party, aliases):
		return models.LegalPartyTo, models.LegalRoleFacing
	case matched:
		return models.LegalMentioned, ""
	}
	return "", ""
}

// foldCaseIterations groups every collected record by case key and keeps the
// latest dated record of each matter, because a case's latest stage is its
// current state. The group's identity class upgrades to its strongest member
// (a name-only early record folded into an exact-name appeal is an exact-name
// finding), missing fields are backfilled from the older record, and the
// survivor carries Iterations so the artifact can say how many stages folded.
// It returns how many records were folded away.
func foldCaseIterations(findings, sameNamed *[]models.LegalFinding) int {
	type caseGroup struct {
		latest  models.LegalFinding
		date    time.Time
		dated   bool
		count   int
		weight  int
		hasSelf bool
	}

	all := append(append([]models.LegalFinding(nil), *findings...), *sameNamed...)
	order := make([]string, 0, len(all))
	groups := map[string]*caseGroup{}

	for _, finding := range all {
		key := legalCaseKey(finding.CaseTitle)
		if key == "" {
			// No title to group by: the record stands alone.
			key = "url:" + finding.URL
		}
		date, dated := legalCaseDate(finding)
		group, exists := groups[key]
		if !exists {
			groups[key] = &caseGroup{latest: finding, date: date, dated: dated, count: 1, weight: legalMatchWeight(finding.MatchKey)}
			order = append(order, key)
			continue
		}
		group.count++
		// The strongest identity class in the matter wins, whichever stage
		// carried it.
		if weight := legalMatchWeight(finding.MatchKey); weight > group.weight {
			group.weight = weight
		}
		replace := false
		switch {
		case dated && !group.dated:
			replace = true
		case dated && group.dated:
			replace = date.After(group.date)
		}
		if replace {
			merged := finding
			if merged.Court == "" {
				merged.Court = group.latest.Court
			}
			if merged.Snippet == "" {
				merged.Snippet = group.latest.Snippet
			}
			group.latest = merged
			group.date, group.dated = date, dated
		} else {
			// The kept record keeps its fields; backfill anything it lacks
			// from the folded one.
			if group.latest.Court == "" {
				group.latest.Court = finding.Court
			}
			if group.latest.Snippet == "" {
				group.latest.Snippet = finding.Snippet
			}
		}
	}

	findingsOut := make([]models.LegalFinding, 0, len(groups))
	sameNamedOut := make([]models.LegalFinding, 0)
	folded := 0
	for _, key := range order {
		group := groups[key]
		survivor := group.latest
		survivor.Iterations = group.count
		// Re-grade the survivor to the matter's strongest identity class so
		// an early name-only record cannot hold a matter down after its
		// latest stage matched exactly.
		switch {
		case group.weight >= legalMatchWeight(models.LegalKeyCIN):
			survivor.MatchKey = models.LegalKeyCIN
		case group.weight >= legalMatchWeight(models.LegalKeyAlias):
			survivor.MatchKey = models.LegalKeyAlias
		case group.weight >= legalMatchWeight(models.LegalKeyName):
			survivor.MatchKey = models.LegalKeyName
		}
		if survivor.MatchKey == models.LegalKeyNameOnly {
			sameNamedOut = append(sameNamedOut, survivor)
		} else {
			findingsOut = append(findingsOut, survivor)
		}
		if group.count > 1 {
			folded += group.count - 1
		}
	}
	*findings = findingsOut
	*sameNamed = sameNamedOut
	return folded
}

// discoveredPerson is one key person the run found on its own, with how the
// run came to know them.
type discoveredPerson struct {
	name string
	via  string
}

// discoverDirectors spends up to legalDiscoveryQueries of the run's remaining
// query budget on registry and directory snippets that name the company, and
// extracts the directors and key persons those snippets tie to it.
//
// Two gates keep the yield honest:
//
//   - every snippet is admitted only when it names the confirmed company
//     (the same entityMatcher keys a company run is held to) — the
//     "company-linked" rule, so a snippet about a different Acme cannot
//     nominate its people;
//   - extraction runs through the model over the company-linked snippets, and
//     the returned names pass deterministic checks (real multi-token names,
//     not the company itself, deduplicated, capped) before any search is spent
//     on them.
//
// The pass reports itself as its own source, so a run's artifact says what
// discovery searched, what it kept and why it came up empty.
func (la *LegalAgent) discoverDirectors(ctx context.Context, sessionID string, identity models.CompanyBrief, matcher *entityMatcher, operatorPersons []string, trace *models.SearchTrace, eventCh chan<- models.AgentEvent, budget int, report *models.LegalSourceReport) ([]discoveredPerson, int) {
	if budget <= 0 {
		report.Unavailable = "the run's query budget was exhausted before discovery"
		return nil, 0
	}

	// Collect the company-linked registry rows.
	var rows []discoveryRow
	used := 0
	planned := []struct{ query, purpose string }{
		{`"` + identity.CompanyName + `" directors list`, "directors — registry discovery"},
		{`site:zaubacorp.com "` + identity.CompanyName + `" directors`, "directors — zaubacorp registry"},
	}
	for _, plannedQuery := range planned {
		if used >= budget || used >= legalDiscoveryQueries {
			break
		}
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventToolCall,
			Step:      41,
			ToolName:  "legal_search:discovery",
			ToolInput: plannedQuery.query,
			Content:   plannedQuery.purpose,
		})
		results, err := la.engine.SearchCached(ctx, plannedQuery.query, legalMaxResultsPerQuery)
		used++
		report.Queries++
		if err != nil {
			log.Printf("[LEGAL][session %s] discovery search failed: %v", sessionID, err)
			if report.Unavailable == "" {
				report.Unavailable = err.Error()
			}
			continue
		}

		var kept, excluded []models.SearchResult
		for _, result := range results {
			report.Hits++
			if _, matched := matcher.matchText(result.Title + " " + result.Snippet); matched {
				rows = append(rows, discoveryRow{host: legalSnippetHost(result.Link), title: result.Title, snippet: result.Snippet})
				kept = append(kept, result)
				continue
			}
			excluded = append(excluded, result)
		}
		trace.Queries = append(trace.Queries, models.QueryTrace{
			Query:    plannedQuery.query,
			Purpose:  plannedQuery.purpose,
			Kept:     kept,
			Excluded: excluded,
		})
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventToolResult,
			Step:      42,
			ToolName:  "legal_search:discovery",
			Content:   fmt.Sprintf("%d company-linked snippet(s)", len(kept)),
		})
	}
	if len(rows) == 0 {
		if report.Unavailable == "" {
			report.Unavailable = "no company-linked registry snippet named a person"
		}
		return nil, used
	}

	// Extract the person names through the model, then gate them
	// deterministically before any search is spent on one.
	names, err := la.extractDirectors(ctx, sessionID, identity, rows)
	if err != nil {
		log.Printf("[LEGAL][session %s] director extraction failed: %v", sessionID, err)
		if report.Unavailable == "" {
			report.Unavailable = "director extraction unavailable: " + err.Error()
		}
		return nil, used
	}

	operator := map[string]bool{}
	for _, person := range operatorPersons {
		operator[strings.ToLower(person)] = true
	}
	seen := map[string]bool{}
	var discovered []discoveredPerson
	for _, name := range names {
		if len(discovered) >= legalMaxDiscoveredDirectors {
			break
		}
		cleaned := strings.Join(strings.Fields(name), " ")
		if cleaned == "" {
			continue
		}
		key := strings.ToLower(cleaned)
		if seen[key] || operator[key] {
			continue
		}
		if strings.EqualFold(cleaned, identity.CompanyName) ||
			strings.Contains(strings.ToLower(cleaned), strings.ToLower(identity.CompanyName)) ||
			strings.Contains(strings.ToLower(identity.CompanyName), strings.ToLower(cleaned)) {
			continue
		}
		if len(strings.Fields(cleaned)) < 2 {
			// A single token is as likely a company word or a fragment as a
			// person; registry snippets carry full names.
			continue
		}
		seen[key] = true
		discovered = append(discovered, discoveredPerson{name: cleaned, via: "discovered from a company-linked registry snippet"})
	}
	report.Kept = len(discovered)
	if len(discovered) == 0 && report.Unavailable == "" {
		report.Unavailable = "the registry snippets named no usable person"
	}
	return discovered, used
}

// directorExtractionRow is one model-extracted person, keyed to the snippet row
// that named them.
type directorExtractionRow struct {
	Row  int    `json:"row"`
	Name string `json:"name"`
}

// directorExtractionTarget is the shape the extraction call returns.
type directorExtractionTarget struct {
	Directors []directorExtractionRow `json:"directors"`
}

// discoveryRow is one company-linked registry snippet, with the host that
// published it.
type discoveryRow struct {
	host    string
	title   string
	snippet string
}

// legalSnippetHost reduces a result link to its hostname, for the provenance a
// report shows. An unparseable link keeps the search engine as the named source
// rather than an empty string.
func legalSnippetHost(link string) string {
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil || parsed.Hostname() == "" {
		return "search snippet"
	}
	return parsed.Hostname()
}

// extractDirectors asks the model to read person names out of the
// company-linked registry snippets. The row index is what carries the source
// host back deterministically — the model never invents where a name came from.
func (la *LegalAgent) extractDirectors(_ context.Context, _ string, identity models.CompanyBrief, rows []discoveryRow) ([]string, error) {
	if la.engine == nil {
		return nil, fmt.Errorf("no model client")
	}

	var listBuilder strings.Builder
	for index, row := range rows {
		fmt.Fprintf(&listBuilder, "%d. host=%s | title=%q | snippet=%s\n", index, row.host, row.title, truncateLegalSnippet(row.snippet))
	}

	systemPrompt := "You extract the names of directors and key managerial persons of the researched company from Indian registry and business-directory snippets. " +
		"Return one row per distinct person name found, with the row number of the snippet that names them. " +
		"Use the name exactly as the snippet spells it. Never return the company itself, its product names, or generic role words. Respond ONLY with valid JSON."
	userPrompt := fmt.Sprintf("Researched company: %q.\n\nSnippets:\n%s\n"+
		"Return JSON: {\"directors\": [{\"row\": <n>, \"name\": \"...\"}]}",
		identity.CompanyName, listBuilder.String())

	var target directorExtractionTarget
	if err := la.engine.GenerateStructured(context.Background(), systemPrompt, userPrompt, &target); err != nil {
		return nil, err
	}

	// Collect the names in first-seen order; the caller applies the
	// deterministic gates (dedupe, company-name exclusion, token count, cap).
	seen := map[string]bool{}
	var names []string
	for _, row := range target.Directors {
		if row.Row < 0 || row.Row >= len(rows) {
			continue
		}
		name := strings.Join(strings.Fields(row.Name), " ")
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, name)
	}
	return names, nil
}

// executeLegalQuery runs one planned search on its source, walking result
// pages while the run's query budget allows, and returns the parsed hits split
// into what the run keeps (judgments) and what it excluded (statute sections).
//
// It is safe to run concurrently: the per-source limiters and caches are
// mutex-protected, so parallel workers queue politely per site while different
// sources progress together.
//
// Pagination is bounded and budgeted: page 1 is part of the search itself, and
// each page past it claims one run query atomically through the shared budget —
// under parallel workers, first claim wins. A page that answers nothing ends
// the walk — as does a source without pagination (Casemine answers empty for
// page 2, measured) or an exhausted budget. The IndianKanoon track goes through
// the native client and its cache; the Casemine track goes through the native
// client when one is configured — its own fetch ladder with the render
// fallback — and rides the engine's cached Serper search otherwise. The snippet
// track does not paginate.
//
// The search trace is NOT written here: parallel workers would append out of
// completion order and the searches artifact would shuffle between runs. The
// coordinator folds the returned hits into the trace in plan order after the
// pool drains.
func (la *LegalAgent) executeLegalQuery(ctx context.Context, sessionID string, planned legalPlannedQuery, matcher *entityMatcher, eventCh chan<- models.AgentEvent, budget *legalBudget) (kept, excluded []tools.LegalSearchHit, err error) {
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventToolCall,
		Step:      41,
		ToolName:  "legal_search:" + planned.Source,
		ToolInput: planned.Query,
		Content:   planned.Purpose,
	})

	// Walk the result pages: page 1 always, then continue only while the budget
	// pays for it and the previous page adds new entity-relevant evidence.
	var allHits []tools.LegalSearchHit
	seenHits := make(map[string]bool)
	fetchErr := error(nil)
	pagesRead := 0
	for page := 1; page <= la.pagesPerQuery; page++ {
		if page > 1 {
			// Sources without pagination end the walk before claiming budget
			// for a page that cannot exist (Casemine, measured: the site
			// ignores the page parameter; Juristway's track is asked for
			// page 1 only).
			if planned.Source == "casemine" || planned.Source == "juristway" {
				break
			}
			if !budget.tryTake() {
				break
			}
		}
		var pageHits []tools.LegalSearchHit
		var pageErr error
		switch planned.Source {
		case "indiankanoon":
			pageHits, pageErr = la.indianKanoonSearchCached(ctx, planned.Query, page)
		case "casemine":
			if la.casemine != nil {
				pageHits, pageErr = la.casemineNativeSearchCached(ctx, planned.Query, page)
			} else {
				// The search-engine snippet fallback is a single page.
				if page > 1 {
					break
				}
				pageHits, pageErr = la.casemineSnippetSearchCached(ctx, planned.Query)
			}
		case "juristway":
			if la.juristway == nil {
				return nil, nil, fmt.Errorf("juristway source is not configured")
			}
			pageHits, pageErr = la.juristwaySearchCached(ctx, planned.Query)
		default:
			return nil, nil, fmt.Errorf("unknown legal source %q", planned.Source)
		}
		if pageErr != nil {
			if page == 1 {
				// The search itself failed: report it the way a
				// non-paginating call would.
				return nil, nil, pageErr
			}
			// A later page failing does not undo the pages already read.
			fetchErr = pageErr
			break
		}
		if len(pageHits) == 0 {
			break
		}
		newHits, newEntityHits := 0, 0
		for _, hit := range pageHits {
			key := strings.ToLower(strings.TrimSpace(hit.DocURL))
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(hit.Title + "|" + hit.Date + "|" + hit.Snippet))
			}
			if seenHits[key] {
				continue
			}
			seenHits[key] = true
			allHits = append(allHits, hit)
			newHits++
			if matcher.match(hit.DocURL, hit.Title, hit.Snippet).Matched {
				newEntityHits++
			}
		}
		pagesRead = page
		if page > 1 && (newHits == 0 || newEntityHits == 0) {
			break
		}
		if page == 1 && len(pageHits) < legalMaxResultsPerQuery && planned.Source == "indiankanoon" {
			// A short first page is the last page: no continuation can
			// carry more records, so the walk ends without spending a
			// query. Other sources have their own page sizes.
			break
		}
		if page > 1 && len(pageHits) < legalMaxResultsPerQuery {
			// A short continuation page is the last page.
			break
		}
	}

	kept = make([]tools.LegalSearchHit, 0, len(allHits))
	excluded = make([]tools.LegalSearchHit, 0)
	for _, hit := range allHits {
		if hit.DocType == "statute" {
			excluded = append(excluded, hit)
			continue
		}
		kept = append(kept, hit)
	}

	summary := fmt.Sprintf("%d judgments, %d statute entries across %d page(s)", len(kept), len(excluded), pagesRead)
	if fetchErr != nil {
		summary += fmt.Sprintf("; page walk stopped early (%v)", fetchErr)
	}
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventToolResult,
		Step:      42,
		ToolName:  "legal_search:" + planned.Source,
		Content:   summary,
	})
	return kept, excluded, nil
}

// indianKanoonSearchCached caches one IndianKanoon search page under the legal
// prefix, keyed by page as well as query. Judgments do not change, so the entry
// lives as long as a search cache.
func (la *LegalAgent) indianKanoonSearchCached(ctx context.Context, query string, page int) ([]tools.LegalSearchHit, error) {
	key := cache.HashKey("legal_search:", fmt.Sprintf("%s|page=%d", query, page))
	var hits []tools.LegalSearchHit
	if cache.GetJSON(ctx, la.engine.cache, key, &hits) {
		return hits, nil
	}
	fresh, err := la.indianKanoon.Search(ctx, query, page, legalMaxResultsPerQuery)
	if err != nil {
		return nil, err
	}
	cache.SetJSON(ctx, la.engine.cache, key, fresh, legalSearchTTL)
	return fresh, nil
}

// casemineNativeSearchCached caches one native Casemine search under the legal
// prefix, keyed by page as well as query — though the measured source answers
// page 1 only. The client's own fetch ladder — plain fetch first,
// cookie-bridged render on a wall — lives behind this call.
func (la *LegalAgent) casemineNativeSearchCached(ctx context.Context, query string, page int) ([]tools.LegalSearchHit, error) {
	key := cache.HashKey("legal_search:", fmt.Sprintf("casemine|%s|page=%d", query, page))
	var hits []tools.LegalSearchHit
	if cache.GetJSON(ctx, la.engine.cache, key, &hits) {
		return hits, nil
	}
	// Casemine serves twenty results per page; ask for the full page.
	fresh, err := la.casemine.Search(ctx, query, page, casemineMaxResultsPerQuery)
	if err != nil {
		return nil, err
	}
	cache.SetJSON(ctx, la.engine.cache, key, fresh, legalSearchTTL)
	return fresh, nil
}

// juristwaySearchCached caches one Juristway search under the legal prefix,
// keyed by the query alone — the track answers page 1 only. The client's own
// politeness and token lifecycle live behind this call.
func (la *LegalAgent) juristwaySearchCached(ctx context.Context, query string) ([]tools.LegalSearchHit, error) {
	key := cache.HashKey("legal_search:", fmt.Sprintf("juristway|%s", query))
	var hits []tools.LegalSearchHit
	if cache.GetJSON(ctx, la.engine.cache, key, &hits) {
		return hits, nil
	}
	fresh, err := la.juristway.Search(ctx, query, 1, legalMaxResultsPerQuery)
	if err != nil {
		return nil, err
	}
	cache.SetJSON(ctx, la.engine.cache, key, fresh, legalSearchTTL)
	return fresh, nil
}

// casemineSnippetSearchCached runs the snippet track through the engine's cached
// Serper search and reshapes the results into legal hits, the fallback for
// deployments without the native client. Serper knows nothing about doc types
// or parties, so the fields it cannot supply stay empty and the classification
// leans on title and snippet text.
func (la *LegalAgent) casemineSnippetSearchCached(ctx context.Context, query string) ([]tools.LegalSearchHit, error) {
	results, err := la.engine.SearchCached(ctx, query, legalMaxResultsPerQuery)
	if err != nil {
		return nil, err
	}
	hits := make([]tools.LegalSearchHit, 0, len(results))
	for _, result := range results {
		hits = append(hits, tools.LegalSearchHit{
			Title:   result.Title,
			DocURL:  result.Link,
			Snippet: result.Snippet,
			Date:    result.Date,
			Court:   "Casemine",
		})
	}
	return hits, nil
}

// legalHitsToSearchResults reshapes hits into the trace's SearchResult rows, so
// the run's searches artifact carries legal searches the same way it carries
// every other search.
func legalHitsToSearchResults(hits []tools.LegalSearchHit) []models.SearchResult {
	if len(hits) == 0 {
		return nil
	}
	results := make([]models.SearchResult, 0, len(hits))
	for i, hit := range hits {
		results = append(results, models.SearchResult{
			Title:    hit.Title,
			Link:     hit.DocURL,
			Snippet:  hit.Snippet,
			Position: i + 1,
			Date:     hit.Date,
		})
	}
	return results
}

// legalFindingFromHit classifies one hit against the confirmed identity and
// converts it to a finding. Match strength, strongest first:
//
//   - the registration id appears in title, parties or snippet → cin;
//   - the matcher ties the text to the company (full name, alias, domain) →
//     the matcher's own key;
//   - a person query's party name appears with the company nearby → name;
//   - only fragments of the name appear → the same-named bucket.
//
// The party's side of the case is parsed deterministically from the title's
// "Petitioner v. Respondent" structure, so a company initiating an action is
// distinguishable from one defending one — a distinction that survives model
// outages, because it never depended on the model.
func legalFindingFromHit(hit tools.LegalSearchHit, planned legalPlannedQuery, matcher *entityMatcher, registrationID string, aliases []string) models.LegalFinding {
	finding := models.LegalFinding{
		Party:     planned.Party,
		PartyKind: planned.PartyKind,
		CaseTitle: hit.Title,
		Court:     hit.Court,
		Date:      hit.Date,
		URL:       hit.DocURL,
		Source:    planned.Source,
		Snippet:   truncateLegalSnippet(hit.Snippet),
	}
	if hit.CaseNumber != "" {
		finding.Citation = hit.CaseNumber
	}

	text := strings.Join([]string{hit.Title, hit.Petitioner, hit.Respondent, hit.Snippet}, " ")
	if regID := normalizeRegistrationID(registrationID); regID != "" && strings.Contains(text, regID) {
		finding.MatchKey = models.LegalKeyCIN
		finding.Reason = "registration id appears in the case record"
		finding.Involvement, finding.Role = legalPartyRole(hit.Title, planned.Party, aliases, true)
		return finding
	}

	if match, matched := matcher.matchText(text); matched {
		finding.MatchKey = models.LegalKeyName
		switch match.Key {
		case models.EntityKeyAlias:
			finding.MatchKey = models.LegalKeyAlias
			finding.Reason = "the confirmed alias names this case"
		case models.EntityKeyRegistration:
			finding.MatchKey = models.LegalKeyCIN
			finding.Reason = "registration id appears in the case record"
		default:
			finding.Reason = "the company's full name appears in the case record"
		}
		finding.Involvement, finding.Role = legalPartyRole(hit.Title, planned.Party, aliases, true)
		return finding
	}

	// The person co-occurrence case: a person query whose hit names the person
	// while the company was in the query itself is the confirmation the plan
	// searched for.
	if planned.PartyKind == "person" && strings.Contains(strings.ToLower(text), strings.ToLower(planned.Party)) {
		finding.MatchKey = models.LegalKeyName
		finding.Reason = "names the person in a case found by the person-and-company search"
		finding.Involvement, finding.Role = legalPartyRole(hit.Title, planned.Party, aliases, true)
		return finding
	}

	finding.MatchKey = models.LegalKeyNameOnly
	finding.Reason = "shares part of the party name only — the same-named entity hazard"
	return finding
}

// truncateLegalSnippet caps the snippet a finding carries into reports and
// prompts, rune-safe like the engine's other content caps.
func truncateLegalSnippet(snippet string) string {
	runes := []rune(strings.TrimSpace(snippet))
	if len(runes) <= legalSnippetCap {
		return string(runes)
	}
	return string(runes[:legalSnippetCap]) + "…"
}

// confirmWithDocuments fetches up to the run's document budget for the
// findings whose identity link is weakest, and upgrades them when the document
// text ties them to the company. A CIN in a judgment body is the strongest
// match there is; a full name in the body is the one a title-only reading can
// miss. Findings are mutated through their pointers, so every upgrade lands in
// the caller's slices.
//
// The fetches run through the bounded pool — each worker touches only its own
// finding pointer, and the IndianKanoon client's politeness limiter keeps the
// actual requests one a second whatever the pool size, so the wall clock is
// bounded by the limiter rather than the sum of the fetches.
func (la *LegalAgent) confirmWithDocuments(ctx context.Context, sessionID string, identity models.CompanyBrief, pointers []*models.LegalFinding) (upgraded, fetched int) {
	if la.docsPerRun <= 0 || la.indianKanoon == nil || len(pointers) == 0 {
		return 0, 0
	}
	matcher := newEntityMatcher(identity)

	// Weakest identity first: a same-named hit that a document proves is the
	// company is worth more than re-reading a CIN-matched one.
	ordered := append([]*models.LegalFinding(nil), pointers...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return legalMatchWeight(ordered[i].MatchKey) < legalMatchWeight(ordered[j].MatchKey)
	})

	// The candidates that are worth a fetch: the run's document budget, in
	// weakest-first order, skipping sources without a fetchable document page.
	candidates := make([]*models.LegalFinding, 0, la.docsPerRun)
	for _, finding := range ordered {
		if len(candidates) >= la.docsPerRun {
			break
		}
		if !strings.Contains(finding.URL, "/doc/") {
			continue // the Casemine track has no fetchable document page
		}
		candidates = append(candidates, finding)
	}
	if len(candidates) == 0 {
		return 0, 0
	}

	var counters sync.Mutex
	runBoundedLegal(len(candidates), la.concurrency, func(index int) {
		finding := candidates[index]
		document, err := la.indianKanoonDocCached(ctx, finding.URL)
		if err != nil || document == nil || document.Text == "" {
			return
		}

		match, matched := matcher.matchText(document.Text)
		counters.Lock()
		fetched++
		counters.Unlock()
		if !matched {
			return
		}
		// The document confirms the link; upgrade the key when it is stronger
		// than what the snippet suggested. The caller re-grades the buckets.
		// Each worker mutates only its own finding pointer.
		newKey := models.LegalKeyName
		if match.Key == models.EntityKeyRegistration {
			newKey = models.LegalKeyCIN
		}
		counters.Lock()
		if legalMatchWeight(newKey) < legalMatchWeight(finding.MatchKey) {
			finding.MatchKey = newKey
			finding.Reason = "confirmed in the document text (" + match.Key + ")"
			upgraded++
		}
		if finding.Court == "" && document.Court != "" {
			finding.Court = document.Court
		}
		if document.Title != "" && finding.CaseTitle == "" {
			finding.CaseTitle = document.Title
		}
		counters.Unlock()
	})
	return upgraded, fetched
}

// legalMatchWeight orders match keys weakest first, so the document pass spends
// its budget where it can change the answer.
func legalMatchWeight(key string) int {
	switch key {
	case models.LegalKeyNameOnly:
		return 0
	case models.LegalKeyName:
		return 1
	case models.LegalKeyAlias:
		return 2
	case models.LegalKeyCIN:
		return 3
	}
	return 4
}

// indianKanoonDocCached caches one document fetch under the legal prefix.
// Documents are immutable, so the entry lives a week.
func (la *LegalAgent) indianKanoonDocCached(ctx context.Context, docURL string) (*tools.LegalDocument, error) {
	key := cache.HashKey("legal_doc:", docURL)
	var document tools.LegalDocument
	if cache.GetJSON(ctx, la.engine.cache, key, &document) && document.URL != "" {
		return &document, nil
	}
	fresh, err := la.indianKanoon.FetchDocument(ctx, docURL)
	if err != nil {
		return nil, err
	}
	cache.SetJSON(ctx, la.engine.cache, key, fresh, legalDocTTL)
	return fresh, nil
}

// legalClassificationRow is one model-returned classification, keyed back to a
// finding by its deterministic-pass index.
type legalClassificationRow struct {
	Index       int     `json:"index"`
	Involvement string  `json:"involvement"`
	Category    string  `json:"category"`
	Status      string  `json:"status"`
	Confidence  float64 `json:"confidence"`
	Reason      string  `json:"reason"`
}

// legalClassificationTarget is the shape the model is asked to return: one
// classification row per finding plus the exposure summary. The model never
// adds, removes or re-matches findings — the index keys it back to what the
// deterministic pass collected.
type legalClassificationTarget struct {
	Findings []legalClassificationRow `json:"findings"`
	Summary  string                   `json:"summary"`
}

// classifyFindings asks the model to categorise the collected findings and
// write the exposure summary. It mutates the findings through their pointers
// and reports whether the model pass succeeded; on failure the findings keep
// their deterministic identity labels and simply stay unclassified — evidence
// is never discarded for want of a model.
func (la *LegalAgent) classifyFindings(ctx context.Context, sessionID string, identity models.CompanyBrief, pointers []*models.LegalFinding, eventCh chan<- models.AgentEvent) (bool, string) {
	if la.engine == nil || len(pointers) == 0 {
		return false, ""
	}

	var listBuilder strings.Builder
	for index, finding := range pointers {
		fmt.Fprintf(&listBuilder, "%d. %q | court=%s | date=%s | match=%s | snippet=%s\n",
			index, finding.CaseTitle, finding.Court, finding.Date, finding.MatchKey, finding.Snippet)
	}

	systemPrompt := "You are an Indian legal research analyst building a litigation-exposure summary for a company dossier. " +
		"Classify each finding. involvement: party when the researched company/person is a party on either side of the case title, " +
		"mentioned when only referenced in the text, referenced when cited as precedent only. " +
		"category: one of insolvency, sebi, tax, criminal, civil, arbitration, consumer, labour, other. " +
		"status: decided, pending, or unknown when the snippet does not say. Respond ONLY with valid JSON."
	userPrompt := fmt.Sprintf("Researched company: %q (aliases: %v, registration id: %s).\n\nFindings:\n%s\n"+
		"Return JSON: {\"findings\": [{\"index\": <n>, \"involvement\": \"...\", \"category\": \"...\", \"status\": \"...\", \"confidence\": <0-1>, \"reason\": \"...\"}], "+
		"\"summary\": \"<2-4 sentence exposure summary: pending matters first, then decided ones, criminal vs civil mix, recency>\"}",
		identity.CompanyName, identity.Aliases, identity.RegistrationID, listBuilder.String())

	var target legalClassificationTarget
	if err := la.engine.GenerateStructured(ctx, systemPrompt, userPrompt, &target); err != nil {
		log.Printf("[LEGAL][session %s] classification failed, findings stay deterministic: %v", sessionID, err)
		return false, ""
	}

	for _, row := range target.Findings {
		if row.Index < 0 || row.Index >= len(pointers) {
			continue
		}
		finding := pointers[row.Index]
		if category := strings.TrimSpace(row.Category); category != "" {
			finding.Category = strings.ToLower(category)
		}
		if status := strings.TrimSpace(row.Status); status != "" {
			finding.Status = strings.ToLower(status)
		}
		if involvement := strings.TrimSpace(row.Involvement); involvement != "" {
			finding.Involvement = strings.ToLower(involvement)
		}
		finding.Confidence = row.Confidence
		if reason := strings.TrimSpace(row.Reason); reason != "" {
			finding.Reason = reason
		}
		finding.Classified = true
	}
	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventToolResult,
		Step:      44,
		ToolName:  "legal_classify",
		Content:   fmt.Sprintf("%d finding(s) classified", len(pointers)),
	})
	return true, target.Summary
}

// personSeed is one person the report carries results for: the operator-named
// ones first, then the discovered directors, each with how they entered the run.
type personSeed struct {
	name string
	via  string
}

// assembleLegalReport puts the report together: matched and same-named groups,
// per-person results in request order, source honesty, the search trace and
// the timings. Person findings also appear in the top-level lists, so the
// counts agree everywhere they are shown.
func assembleLegalReport(query models.LegalQuery, identity models.CompanyBrief, findings, sameNamed []models.LegalFinding, seeds []personSeed, sources map[string]*models.LegalSourceReport, trace *models.SearchTrace, timer *phaseTimer) *models.LegalReport {
	report := &models.LegalReport{
		CompanyName:     query.CompanyName,
		Domain:          identity.Domain,
		Aliases:         identity.Aliases,
		RegistrationID:  identity.RegistrationID,
		EntityConfirmed: identity.EntityConfirmed,
		EntitySource:    identity.EntitySource,
		Findings:        findings,
		SameNamed:       sameNamed,
		SearchTrace:     trace,
		LastUpdated:     time.Now().UTC(),
	}

	// Sources in a stable order, whether or not they answered.
	for _, name := range []string{"indiankanoon", "casemine", directorsDiscoverySource} {
		if source, exists := sources[name]; exists {
			report.Sources = append(report.Sources, *source)
		}
	}

	// Per-person results, in the order the run planned them: operator-named
	// first, then the discovered directors.
	for _, seed := range seeds {
		result := models.LegalPersonResult{Name: seed.name, DiscoveredVia: seed.via}
		for _, finding := range findings {
			if finding.PartyKind == "person" && strings.EqualFold(finding.Party, seed.name) {
				result.Checked = true
				result.Findings = append(result.Findings, finding)
			}
		}
		for _, finding := range sameNamed {
			if finding.PartyKind == "person" && strings.EqualFold(finding.Party, seed.name) {
				result.Checked = true
				result.Findings = append(result.Findings, finding)
			}
		}
		// A person with no findings was still searched, and the trace proves
		// it: checked means "the searches ran", not "nothing exists".
		if !result.Checked && legalPersonWasSearched(trace, seed.name) {
			result.Checked = true
		}
		report.Persons = append(report.Persons, result)
	}

	report.Timings = timer.results()
	return report
}

// legalPersonWasSearched reports whether any executed query named the person,
// which is what makes "no findings" an answer rather than an omission.
func legalPersonWasSearched(trace *models.SearchTrace, person string) bool {
	if trace == nil {
		return false
	}
	needle := strings.ToLower(person)
	for _, queryTrace := range trace.Queries {
		if strings.Contains(strings.ToLower(queryTrace.Query), needle) {
			return true
		}
	}
	return false
}
