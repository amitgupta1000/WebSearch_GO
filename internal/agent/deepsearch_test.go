package agent

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// deepTestSearcher hands every query one distinct hit at a deterministic URL,
// so rounds can be told apart by the pages they collect.
type deepTestSearcher struct {
	mu      sync.Mutex
	queries []string
}

func (s *deepTestSearcher) Search(_ context.Context, query string, maxResults int) ([]models.SearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries = append(s.queries, query)
	escaped := url.PathEscape(query)
	return []models.SearchResult{
		{Title: "Result for " + query, Link: fmt.Sprintf("https://example.com/%s/page", escaped), Snippet: "snippet for " + query, Date: "2026-01-15"},
	}, nil
}

var (
	deepIDPattern  = regexp.MustCompile(`\[id=([a-z0-9\-]+)\]`)
	deepURLPattern = regexp.MustCompile(`\[url=([^\s\]]+)\]`)
)

// scriptedDeepLLM fills structured targets in the order the agent calls them:
// decompose, compose, then per round judge → extract → review, and finally
// assemble. Judge and extraction responses are derived from the prompt itself
// (the candidate ids and page urls the agent offered), so tests stay valid as
// the pipeline changes. Anything not scripted defaults to a permissive value.
type scriptedDeepLLM struct {
	mu sync.Mutex
	// scripted stages, consumed in order and then reused.
	reviews     []models.DeepSearchReview
	extraPairs  []models.DeepSearchQAPair // appended to every extraction
	synthesis   models.DeepSearchSynthesis
	// observability
	planSystemPrompt   string
	composeSystemPrompt string
	focusSystemNote    string
	counts             map[string]int
}

func (s *scriptedDeepLLM) GenerateStructured(_ context.Context, systemPrompt, userPrompt string, target any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.counts == nil {
		s.counts = map[string]int{}
	}
	switch typed := target.(type) {
	case *models.DeepSearchPlan:
		s.planSystemPrompt = systemPrompt
		if strings.Contains(systemPrompt, "exactly 5") {
			s.focusSystemNote = "5"
		} else if strings.Contains(systemPrompt, "exactly 10") {
			s.focusSystemNote = "10"
		}
		typed.Understanding = "test understanding"
		typed.SubQuestions = []string{"alpha facet", "beta facet"}
		s.counts["plan"]++
	case *models.DeepSearchQueryPlan:
		s.composeSystemPrompt = systemPrompt
		typed.Entries = []models.DeepSubQuestionQueries{
			{SubQuestion: "alpha facet", Queries: []string{"alpha query", "alpha alt"}},
			{SubQuestion: "beta facet", Queries: []string{"beta query", "beta alt"}},
		}
		s.counts["compose"]++
	case *models.DeepSearchSelection:
		// Keep every candidate the prompt offered, scored equally, so ordering
		// tests can control the outcome through dates alone.
		typed.Kept = nil
		for _, id := range deepIDPattern.FindAllStringSubmatch(userPrompt, -1) {
			typed.Kept = append(typed.Kept, models.DeepSelectedHit{ID: id[1], Score: 0.6, Reason: "matches"})
		}
		s.counts["judge"]++
	case *models.DeepSearchExtraction:
		typed.Pairs = nil
		for index, match := range deepURLPattern.FindAllStringSubmatch(userPrompt, -1) {
			typed.Pairs = append(typed.Pairs, models.DeepSearchQAPair{
				SubQuestion: "alpha facet",
				Question:    fmt.Sprintf("What does page %d say?", index+1),
				Answer:      fmt.Sprintf("Grounded answer %d extracted from the page text.", index+1),
				Quote:       "verbatim supporting quote",
				URL:         match[1],
			})
		}
		typed.Pairs = append(typed.Pairs, s.extraPairs...)
		s.counts["extract"]++
	case *models.DeepSearchReview:
		index := s.counts["review"]
		s.counts["review"]++
		if index < len(s.reviews) {
			*typed = s.reviews[index]
		} else if len(s.reviews) > 0 {
			*typed = s.reviews[len(s.reviews)-1]
		} else {
			*typed = models.DeepSearchReview{Complete: true}
		}
	case *models.DeepSearchSynthesis:
		*typed = s.synthesis
		// A permissive default report, so tests that do not care about the
		// assembly stage still get a valid one.
		if strings.TrimSpace(typed.Title) == "" {
			typed.Title = "Default report title"
		}
		if strings.TrimSpace(typed.Summary) == "" {
			typed.Summary = "Default report summary."
		}
		if strings.TrimSpace(typed.Report) == "" {
			typed.Report = "# Default report body"
		}
		if len(typed.Findings) == 0 {
			// Cite the first pair URL the prompt offered, so the default is a
			// well-grounded report; tests override for hallucination cases.
			if matches := deepURLPattern.FindAllStringSubmatch(userPrompt, 1); len(matches) == 1 {
				typed.Findings = []models.DeepSearchFinding{{Statement: "a supported claim", URLs: []string{matches[0][1]}}}
			}
		}
		s.counts["synthesize"]++
	}
	return nil
}

func newDeepTestEngine(t *testing.T, llm *scriptedDeepLLM, searcher *deepTestSearcher, scraper interface {
	ScrapeURL(context.Context, string) (*models.ScrapeResult, error)
}) *Engine {
	t.Helper()
	store := cache.NewMemoryCache(100)
	t.Cleanup(func() { store.Close() })
	return NewEngine(llm, searcher, scraper, store, deepMaxScrapes, deepMaxSearchResults, 10)
}

// TestDeepSearchAgentRunsReviewRerunRound pins the full pipeline: composed
// queries are sampled across sub-questions, results are judged and read,
// answers are extracted as QA pairs, and an incomplete review triggers one
// rerun round before the report is assembled.
func TestDeepSearchAgentRunsReviewRerunRound(t *testing.T) {
	llm := &scriptedDeepLLM{
		reviews: []models.DeepSearchReview{
			{Complete: false, Missing: []string{"no answer for beta facet"}, RerunQueries: []string{"gamma rerun"}},
			{Complete: true},
		},
		synthesis: models.DeepSearchSynthesis{
			Title:   "Test title",
			Summary: "Test summary",
			Report:  "# Test report body",
		},
	}
	searcher := &deepTestSearcher{}
	eng := newDeepTestEngine(t, llm, searcher, &mockScraper{})
	agent := NewDeepSearchAgent(eng)

	report, err := agent.Research(context.Background(), "sess", models.DeepSearchQuery{Question: "what is deep search?", MaxRounds: 3}, nil)
	if err != nil {
		t.Fatalf("research failed: %v", err)
	}
	if report.RoundsRun != 2 {
		t.Errorf("expected 2 rounds (rerun + complete), got %d", report.RoundsRun)
	}
	if len(report.Rounds) != 2 {
		t.Fatalf("expected a trace per round, got %d", len(report.Rounds))
	}
	if report.Rounds[0].Complete {
		t.Error("round 1 should be recorded as incomplete")
	}
	if !report.Rounds[1].Complete {
		t.Error("round 2 should be recorded as complete")
	}
	if got := report.Rounds[1].Queries; len(got) != 1 || got[0] != "gamma rerun" {
		t.Errorf("round 2 should run only the rerun query, got %v", got)
	}
	// 4 composed queries executed in round 1 + 1 rerun in round 2.
	if report.QueriesUsed != 5 {
		t.Errorf("expected 5 executed queries, got %d", report.QueriesUsed)
	}
	if report.Rounds[0].Composed != 4 {
		t.Errorf("expected 4 composed queries recorded, got %d", report.Rounds[0].Composed)
	}
	if len(report.Sources) == 0 || len(report.QAPairs) == 0 {
		t.Fatalf("expected sources and answer pairs, got %d sources / %d pairs", len(report.Sources), len(report.QAPairs))
	}
	for _, source := range report.Sources {
		// Rerun queries are not tied to a composed sub-question, so only
		// round-1 sources must carry the attribution.
		if source.Round == 1 && source.SubQuestion == "" {
			t.Errorf("source %s lost its sub-question attribution", source.URL)
		}
	}
	if report.Synthesis.Title != "Test title" || report.Synthesis.Report == "" {
		t.Errorf("synthesis not stored: %+v", report.Synthesis)
	}
	if len(report.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", report.Warnings)
	}
	if report.CompletedAt.Before(report.StartedAt) {
		t.Error("completion time predates start time")
	}
}

// TestDeepSearchAgentDropsUnsourcedCitations pins the assembly guard: a
// synthesized claim pointing at a URL the run never read is dropped with a
// warning, and the report itself survives.
func TestDeepSearchAgentDropsUnsourcedCitations(t *testing.T) {
	llm := &scriptedDeepLLM{
		synthesis: models.DeepSearchSynthesis{
			Title:   "Title",
			Summary: "Summary",
			Report:  "Body",
			Findings: []models.DeepSearchFinding{
				{Statement: "invented claim", URLs: []string{"https://invented.example/nope"}},
			},
			Citations: []models.DeepSearchFinding{
				{Statement: "invented citation", URLs: []string{"https://invented.example/also-nope"}},
			},
		},
	}
	searcher := &deepTestSearcher{}
	eng := newDeepTestEngine(t, llm, searcher, &mockScraper{})
	agent := NewDeepSearchAgent(eng)

	report, err := agent.Research(context.Background(), "sess", models.DeepSearchQuery{Question: "question"}, nil)
	if err != nil {
		t.Fatalf("research failed: %v", err)
	}
	if len(report.Synthesis.Findings) != 0 || len(report.Synthesis.Citations) != 0 {
		t.Errorf("unsourced findings/citations survived filtering: %+v", report.Synthesis)
	}
	found := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "citations") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a citation-drop warning, got %v", report.Warnings)
	}
}

// TestDeepSearchAgentDropsUnscrapedExtraction pins the extraction guard: a QA
// pair whose URL was never scraped is dropped with a warning, so nothing but
// read pages can enter the corpus.
func TestDeepSearchAgentDropsUnscrapedExtraction(t *testing.T) {
	llm := &scriptedDeepLLM{
		extraPairs: []models.DeepSearchQAPair{
			{Question: "invented question", Answer: "invented answer", URL: "https://invented.example/never-read"},
		},
	}
	searcher := &deepTestSearcher{}
	eng := newDeepTestEngine(t, llm, searcher, &mockScraper{})
	agent := NewDeepSearchAgent(eng)

	report, err := agent.Research(context.Background(), "sess", models.DeepSearchQuery{Question: "question"}, nil)
	if err != nil {
		t.Fatalf("research failed: %v", err)
	}
	for _, pair := range report.QAPairs {
		if pair.URL == "https://invented.example/never-read" {
			t.Error("an unscraped-URL pair survived filtering")
		}
	}
	found := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "unscraped URL") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an unscraped-URL warning, got %v", report.Warnings)
	}
}

// TestDeepSearchAgentRespectsScrapeBudget pins the run-level scrape budget: a
// four-candidate plan with MaxScrapes=2 must read exactly two pages even when
// the reviewer asks for a rerun round.
func TestDeepSearchAgentRespectsScrapeBudget(t *testing.T) {
	llm := &scriptedDeepLLM{
		reviews: []models.DeepSearchReview{
			{Complete: false, RerunQueries: []string{"gamma rerun"}},
			{Complete: true},
		},
		synthesis: models.DeepSearchSynthesis{Title: "T", Summary: "S", Report: "R"},
	}
	searcher := &deepTestSearcher{}
	eng := newDeepTestEngine(t, llm, searcher, &mockScraper{})
	agent := NewDeepSearchAgent(eng)

	report, err := agent.Research(context.Background(), "sess", models.DeepSearchQuery{
		Question: "question", MaxScrapes: 2,
	}, nil)
	if err != nil {
		t.Fatalf("research failed: %v", err)
	}
	if report.PagesScraped != 2 {
		t.Errorf("expected the scrape budget to cap pages read at 2, got %d", report.PagesScraped)
	}
	if report.RoundsRun != 2 {
		t.Errorf("expected the run to continue into the rerun round, got %d rounds", report.RoundsRun)
	}
	if report.Rounds[1].Selected != 0 {
		t.Errorf("the rerun round must not read beyond the budget, selected %d", report.Rounds[1].Selected)
	}
}

// TestDeepSearchAgentReportsEmptyEvidence pins the honest-failure path: when no
// page can be read, the run completes with a warning and no invented report.
func TestDeepSearchAgentReportsEmptyEvidence(t *testing.T) {
	llm := &scriptedDeepLLM{synthesis: models.DeepSearchSynthesis{Title: "should never be used", Summary: "x", Report: "y"}}
	searcher := &deepTestSearcher{}
	eng := newDeepTestEngine(t, llm, searcher, &failingScraper{err: errors.New("scraping returned status code: 403")})
	agent := NewDeepSearchAgent(eng)

	report, err := agent.Research(context.Background(), "sess", models.DeepSearchQuery{Question: "question"}, nil)
	if err != nil {
		t.Fatalf("research failed: %v", err)
	}
	if len(report.Sources) != 0 || len(report.QAPairs) != 0 {
		t.Errorf("expected no sources or pairs, got %d / %d", len(report.Sources), len(report.QAPairs))
	}
	if report.Synthesis.Title != "" {
		t.Errorf("no synthesis should exist without evidence, got %q", report.Synthesis.Title)
	}
	sawWarning := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "No page could be read") {
			sawWarning = true
		}
	}
	if !sawWarning {
		t.Errorf("expected the empty-evidence warning, got %v", report.Warnings)
	}
}

// TestDeepSearchFocusShapesDecomposition pins the focus wiring: the planner is
// asked for 5 sub-questions under recency and 10 under depth, and the focus is
// carried onto the report.
func TestDeepSearchFocusShapesDecomposition(t *testing.T) {
	searcher := &deepTestSearcher{}
	for _, testCase := range []struct {
		focus    string
		expected string
	}{
		{models.FocusRecency, "5"},
		{models.FocusDepth, "10"},
		{"", "10"}, // empty focus defaults to depth
	} {
		llm := &scriptedDeepLLM{}
		eng := newDeepTestEngine(t, llm, searcher, &mockScraper{})
		agent := NewDeepSearchAgent(eng)
		report, err := agent.Research(context.Background(), "sess", models.DeepSearchQuery{Question: "question", Focus: testCase.focus}, nil)
		if err != nil {
			t.Fatalf("research failed (%s): %v", testCase.focus, err)
		}
		if llm.focusSystemNote != testCase.expected {
			t.Errorf("focus %q should ask for %s sub-questions, planner was asked for %q", testCase.focus, testCase.expected, llm.focusSystemNote)
		}
		want := testCase.focus
		if want == "" {
			want = models.FocusDepth
		}
		if report.Focus != want {
			t.Errorf("report focus = %q, want %q", report.Focus, want)
		}
	}
}

func TestDeepSearchAgentRequiresQuestion(t *testing.T) {
	eng := newDeepTestEngine(t, &scriptedDeepLLM{}, &deepTestSearcher{}, &mockScraper{})
	agent := NewDeepSearchAgent(eng)
	if _, err := agent.Research(context.Background(), "sess", models.DeepSearchQuery{Question: "   "}, nil); err == nil {
		t.Fatal("expected an error for an empty question")
	}
}

// TestDeepSelectExecutedQueriesRoundRobin pins the round-robin sampler: every
// sub-question gets its first query before any gets a second, the cap is
// respected, and duplicate queries are executed once.
func TestDeepSelectExecutedQueriesRoundRobin(t *testing.T) {
	plan := models.DeepSearchQueryPlan{Entries: []models.DeepSubQuestionQueries{
		{SubQuestion: "one", Queries: []string{"a1", "a2", "a3"}},
		{SubQuestion: "two", Queries: []string{"b1", "b2", "b3"}},
		{SubQuestion: "three", Queries: []string{"c1", "c2", "c3"}},
	}}
	queries, subByQuery := deepSelectExecutedQueries(plan, 7)
	want := []string{"a1", "b1", "c1", "a2", "b2", "c2", "a3"}
	if len(queries) != len(want) {
		t.Fatalf("expected %d executed queries, got %v", len(want), queries)
	}
	for i := range want {
		if queries[i] != want[i] {
			t.Fatalf("round-robin order wrong at %d: got %v, want %v", i, queries, want)
		}
	}
	if subByQuery["a1"] != "one" || subByQuery["b2"] != "two" || subByQuery["c2"] != "three" {
		t.Errorf("sub-question attribution lost: %v", subByQuery)
	}

	duplicated := models.DeepSearchQueryPlan{Entries: []models.DeepSubQuestionQueries{
		{SubQuestion: "one", Queries: []string{"same", "a2"}},
		{SubQuestion: "two", Queries: []string{"same", "b2"}},
	}}
	queries, _ = deepSelectExecutedQueries(duplicated, 10)
	if len(queries) != 3 || queries[0] != "same" {
		t.Errorf("duplicates must execute once, got %v", queries)
	}
}

func TestDeepTakeTaggedQueries(t *testing.T) {
	tagged := deepTaggedQueries([]string{"gamma", "delta"}, 2)
	if len(tagged) != 2 || tagged[0] != "r3|gamma" {
		t.Fatalf("rerun queries must be tagged for the next round, got %v", tagged)
	}
	if got := deepTakeTagged(tagged, 3); len(got) != 2 || got[0] != "gamma" {
		t.Errorf("expected round 3 to take its own queries, got %v", got)
	}
	if got := deepTakeTagged(tagged, 2); len(got) != 0 {
		t.Errorf("round 2 must not run round 3's queries, got %v", got)
	}
}

// TestDeepRecencyBonusAndOrdering pins the selection ranking: equal judge
// scores, recency focus lifts a current-year dated candidate above an undated
// one; depth focus keeps judge order alone.
func TestDeepRecencyBonusAndOrdering(t *testing.T) {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if got := deepRecencyBonus("2026-02-20", now); got != deepRecencyYearBonus {
		t.Errorf("a current-year date should get the year bonus, got %v", got)
	}
	if got := deepRecencyBonus("2024-02-20", now); got != deepRecencyDatedBonus {
		t.Errorf("an older date should get the dated bonus, got %v", got)
	}
	if got := deepRecencyBonus("", now); got != 0 {
		t.Errorf("an undated candidate should get no bonus, got %v", got)
	}

	dated := models.DeepSearchCandidate{ID: "r1-1", URL: "https://a.example", Date: "2026-02-20"}
	undated := models.DeepSearchCandidate{ID: "r1-2", URL: "https://b.example"}
	selection := models.DeepSearchSelection{Kept: []models.DeepSelectedHit{
		{ID: "r1-1", Score: 0.5}, {ID: "r1-2", Score: 0.5},
	}}
	ordered := deepOrderCandidates([]models.DeepSearchCandidate{undated, dated}, selection, nil, models.FocusRecency, now)
	if ordered[0].ID != "r1-1" {
		t.Errorf("recency focus should rank the dated candidate first, got %v", ordered)
	}
	ordered = deepOrderCandidates([]models.DeepSearchCandidate{undated, dated}, selection, nil, models.FocusDepth, now)
	if ordered[0].ID != "r1-2" {
		t.Errorf("depth focus should keep judge order, got %v", ordered)
	}
	// A failed judge call falls back to every candidate in search order.
	ordered = deepOrderCandidates([]models.DeepSearchCandidate{undated, dated}, models.DeepSearchSelection{}, errors.New("judge down"), models.FocusRecency, now)
	if len(ordered) != 2 || ordered[0].ID != "r1-2" {
		t.Errorf("judge failure should fall back to search order, got %v", ordered)
	}
	// A judge that kept nothing selects nothing.
	ordered = deepOrderCandidates([]models.DeepSearchCandidate{undated, dated}, models.DeepSearchSelection{}, nil, models.FocusDepth, now)
	if len(ordered) != 0 {
		t.Errorf("an empty verdict must select nothing, got %v", ordered)
	}
}

func TestDeepCanonicalURLDedupesVariants(t *testing.T) {
	base := deepCanonicalURL("https://www.Example.com/page")
	variants := []string{
		"https://example.com/page/",
		"https://WWW.example.com/page#section",
	}
	for _, variant := range variants {
		if deepCanonicalURL(variant) != base {
			t.Errorf("%s should canonicalize to %s, got %s", variant, base, deepCanonicalURL(variant))
		}
	}
	if got := deepCanonicalURL("not a url"); got != "" {
		t.Errorf("expected an empty canonical form for a non-URL, got %q", got)
	}
}

// TestDeepSearchAgentEmitsEvents checks the SSE stream carries the phases an
// operator watches in the console.
func TestDeepSearchAgentEmitsEvents(t *testing.T) {
	llm := &scriptedDeepLLM{synthesis: models.DeepSearchSynthesis{Title: "T", Summary: "S", Report: "R"}}
	searcher := &deepTestSearcher{}
	eng := newDeepTestEngine(t, llm, searcher, &mockScraper{})
	agent := NewDeepSearchAgent(eng)

	eventCh := make(chan models.AgentEvent, 64)
	var events []models.AgentEvent
	done := make(chan struct{})
	go func() {
		for event := range eventCh {
			events = append(events, event)
		}
		close(done)
	}()

	report, err := agent.Research(context.Background(), "sess", models.DeepSearchQuery{Question: "question"}, eventCh)
	close(eventCh)
	<-done
	if err != nil {
		t.Fatalf("research failed: %v", err)
	}
	if report.Synthesis.Title != "T" {
		t.Fatalf("expected a synthesized report, got %+v", report.Synthesis)
	}
	kinds := make(map[models.EventType]bool)
	for _, event := range events {
		kinds[event.Type] = true
	}
	for _, kind := range []models.EventType{models.EventThinking, models.EventToolCall, models.EventComplete} {
		if !kinds[kind] {
			t.Errorf("expected a %s event, saw %v", kind, kinds)
		}
	}
}


