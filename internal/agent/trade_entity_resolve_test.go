package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestNormalizeTradeEntityName pins the folding the comparison tiers rely on:
// case, "&" versus "and", dots, brackets, dashes and slashes all collapse to
// plain spaced words.
func TestNormalizeTradeEntityName(t *testing.T) {
	cases := map[string]string{
		"SASOL CHEMICALS (PTY) LTD.":      "sasol chemicals pty ltd",
		"Bharat Petroleum & Co":           "bharat petroleum and co",
		"  Reliance-Industries/Ltd ":      "reliance industries ltd",
		"Saint-Gobain India Pvt. Ltd.":    "saint gobain india pvt ltd",
		"Merck KGaA":                      "merck kgaa",
		"Hindustan Petroleum Corpn. Ltd.": "hindustan petroleum corpn ltd",
	}
	for input, want := range cases {
		if got := normalizeTradeEntityName(input); got != want {
			t.Errorf("normalizeTradeEntityName(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestTradeNameCoreDropsLegalForms pins the suffix stripping across the legal
// forms the customs data carries, including the foreign supplier-side ones.
func TestTradeNameCoreDropsLegalForms(t *testing.T) {
	cases := map[string]string{
		"KLJ Resources Limited":              "klj resources",
		"KLJ RESOURCES LTD":                  "klj resources",
		"SASOL CHEMICALS (PTY) LTD.":         "sasol chemicals",
		"BASF SE":                            "basf",
		"Merck KGaA":                         "merck",
		"Evonik Industries AG":               "evonik industries",
		"Akzo Nobel N.V.":                    "akzo nobel",
		"GreenDiesel Sdn Bhd":                "greendiesel",
		"Sinopec Shanghai Petrochemical Co.": "sinopec shanghai petrochemical",
		"Bharat Petroleum & Co":              "bharat petroleum",
		"Limited":                            "limited",
	}
	for input, want := range cases {
		if got := tradeNameCore(input); got != want {
			t.Errorf("tradeNameCore(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestTradeNameMatchesVariants pins the confident-match contract: punctuation,
// case and legal-form differences match; different trading names do not.
func TestTradeNameMatchesVariants(t *testing.T) {
	matching := [][2]string{
		{"Sasol Chemicals", "SASOL CHEMICALS (PTY) LTD"},
		{"KLJ Resources Limited", "KLJ RESOURCES LTD"},
		{"Reliance Industries", "RELIANCE INDUSTRIES LIMITED"},
		{"Bharat Petroleum", "BHARAT PETROLEUM & CO"},
		{"BASF", "BASF SE"},
	}
	for _, pair := range matching {
		if !tradeNameMatches(pair[0], pair[1]) {
			t.Errorf("tradeNameMatches(%q, %q) = false, want true", pair[0], pair[1])
		}
	}
	differing := [][2]string{
		{"Sasol", "SASOL CHEMICALS (PTY) LTD"},
		{"KLJ Resources", "KLJ LOGISTICS LTD"},
	}
	for _, pair := range differing {
		if tradeNameMatches(pair[0], pair[1]) {
			t.Errorf("tradeNameMatches(%q, %q) = true, want false", pair[0], pair[1])
		}
	}
}

// TestScoreTradeEntityCandidateTiers pins the scoring bands: exact and core
// are the confident tiers, containment and character similarity are
// suggestions, and unrelated names score nothing.
func TestScoreTradeEntityCandidateTiers(t *testing.T) {
	cases := []struct {
		query, candidate string
		want             float64
		grade            string
	}{
		{"Acme Ltd", "ACME LTD", 1.0, "exact"},
		{"Sasol Chemicals", "SASOL CHEMICALS (PTY) LTD", tradeEntityConfidentScore, "core"},
		{"Aditya Birla", "ADITYA BIRLA CHEMICALS LTD", 0.85, "related"},
		{"Tata Chem", "TATA CHEMICALS LTD", 0, "loose"},
		{"KLJ", "UNRELATED CHEMICALS LTD", 0, ""},
	}
	for _, tc := range cases {
		score, grade := scoreTradeEntityCandidate(tc.query, tc.candidate)
		if tc.grade == "loose" {
			if grade != "loose" || score < 0.3 || score >= 0.85 {
				t.Errorf("score(%q, %q) = %.2f/%q, want a loose band score", tc.query, tc.candidate, score, grade)
			}
			continue
		}
		if score != tc.want || grade != tc.grade {
			t.Errorf("score(%q, %q) = %.2f/%q, want %.2f/%q", tc.query, tc.candidate, score, grade, tc.want, tc.grade)
		}
	}
}

// TestTradeEntityQueryLadder pins the progressive typeahead queries: the typed
// name, its legal-form-stripped core, and the first two significant tokens.
func TestTradeEntityQueryLadder(t *testing.T) {
	ladder := tradeEntityQueryLadder("KLJ Resources Trading Company Limited")
	want := []string{"KLJ Resources Trading Company Limited", "klj resources trading", "klj resources"}
	if len(ladder) != len(want) {
		t.Fatalf("ladder = %v, want %v", ladder, want)
	}
	for i := range want {
		if ladder[i] != want[i] {
			t.Errorf("ladder[%d] = %q, want %q", i, ladder[i], want[i])
		}
	}
	if got := tradeEntityQueryLadder("Sasol Chemicals"); len(got) != 1 || got[0] != "Sasol Chemicals" {
		t.Errorf("short-name ladder = %v, want only the typed name", got)
	}
}

// recordingNamesMock scripts EntityNames per query and direction, recording
// every query so tests can pin the ladder walk. The mutex guards the recording:
// the resolver walks the two sides concurrently, so EntityNames is called from
// two goroutines at once.
type recordingNamesMock struct {
	TradeDBSource
	mu       sync.Mutex
	importer map[string][]string
	supplier map[string][]string
	queries  []string
}

func (m *recordingNamesMock) EntityNames(_ context.Context, direction, query string, _ int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queries = append(m.queries, direction+":"+query)
	table := m.importer
	if direction == models.TradeDirectionSupplier {
		table = m.supplier
	}
	if names, ok := table[strings.ToLower(query)]; ok {
		return names, nil
	}
	return nil, nil
}

// TestResolveTradeEntityMatchesOnEitherSide pins the resolver outcomes: a
// supplier-side core match resolves confidently with that direction; a miss
// yields ranked suggestions, each carrying its side.
func TestResolveTradeEntityMatchesOnEitherSide(t *testing.T) {
	resetTradeEntityOverrides()
	client := &recordingNamesMock{
		TradeDBSource: &tradeDBMock{options: &models.TradeFilterOptions{}},
		importer:      map[string][]string{},
		supplier: map[string][]string{
			"basf": {"BASF SE", "BASF COATINGS GMBH"},
		},
	}
	match, candidates, _ := resolveTradeEntity(context.Background(), client, "BASF")
	if match.name != "BASF SE" || match.direction != models.TradeDirectionSupplier {
		t.Fatalf("match = %+v, want the supplier-side BASF SE", match)
	}
	if len(candidates) != 0 {
		t.Errorf("candidates = %+v, want none on a confident match", candidates)
	}

	miss := &recordingNamesMock{
		TradeDBSource: &tradeDBMock{options: &models.TradeFilterOptions{}},
		importer: map[string][]string{
			"klj": {"KLJ RESOURCES AFRICA LTD", "KLJ RESOURCES KENYA LTD", "UNRELATED CHEMICALS LTD"},
		},
		supplier: map[string][]string{},
	}
	match, candidates, _ = resolveTradeEntity(context.Background(), miss, "KLJ")
	if match.name != "" {
		t.Fatalf("match = %+v, want a suggestion-only miss", match)
	}
	if len(candidates) == 0 {
		t.Fatal("candidates = none, want the closest stored names")
	}
	for _, candidate := range candidates {
		if candidate.Direction != models.TradeDirectionImporter {
			t.Errorf("candidate %+v lost its side", candidate)
		}
		if candidate.Grade == "" || candidate.Score <= 0 {
			t.Errorf("candidate %+v is not graded", candidate)
		}
	}
}

// TestSemanticTradeEntityCandidatesValidatesPicks pins the model layer: the
// re-rank reorders only names from the candidate list, carries the rationale,
// and falls back to the deterministic order on failure.
func TestSemanticTradeEntityCandidatesValidatesPicks(t *testing.T) {
	candidates := []models.TradeEntityCandidate{
		{Name: "SASOL CHEMICALS AFRICA (PTY) LTD", Direction: models.TradeDirectionSupplier, Score: 0.72, Grade: "loose"},
		{Name: "SASOL CHEMICALS (PTY) LTD", Direction: models.TradeDirectionSupplier, Score: 0.68, Grade: "loose"},
	}
	llm := &scriptedProductLLM{response: []struct {
		Name      string `json:"name"`
		Rationale string `json:"rationale"`
	}{
		{Name: "SASOL CHEMICALS (PTY) LTD", Rationale: "the group's chemicals arm"},
		{Name: "INVENTED TRADING LLC", Rationale: "not a candidate"},
	}}
	ranked := semanticTradeEntityCandidates(context.Background(), llm, "Sasol Chemicals", candidates)
	if len(ranked) != 1 || ranked[0].Name != "SASOL CHEMICALS (PTY) LTD" {
		t.Fatalf("ranked = %+v, want the model pick only", ranked)
	}
	if ranked[0].Grade != "semantic" || ranked[0].Rationale != "the group's chemicals arm" {
		t.Errorf("pick = %+v, want the semantic grade and rationale", ranked[0])
	}
	if len(llm.prompts) != 1 || !strings.Contains(llm.prompts[0], "Sasol Chemicals") {
		t.Errorf("prompt = %v, want the researched name", llm.prompts)
	}

	failing := &scriptedProductLLM{err: context.DeadlineExceeded}
	fallback := semanticTradeEntityCandidates(context.Background(), failing, "Sasol Chemicals", candidates)
	if len(fallback) != 2 || fallback[0].Name != candidates[0].Name {
		t.Errorf("fallback = %+v, want the deterministic order", fallback)
	}
	if got := semanticTradeEntityCandidates(context.Background(), nil, "Sasol Chemicals", candidates); len(got) != 2 {
		t.Errorf("nil model changed the list: %+v", got)
	}
}

// TestSetTradeEntityOverrideWins pins the operator pick: the override resolves
// directly on the chosen side, skips the typeahead, and replaces a cached miss.
func TestSetTradeEntityOverrideWins(t *testing.T) {
	resetTradeEntityOverrides()
	resetTradeProfileMemo()
	defer func() {
		SetTradeEntityOverride("Sasol Chemicals", "", "")
		resetTradeEntityOverrides()
		resetTradeProfileMemo()
	}()
	inner := &tradeDBMock{
		options: &models.TradeFilterOptions{},
		entityProfiles: []models.TradeEntityProfile{{
			Name: "SASOL CHEMICALS (PTY) LTD", GlobalRank: 12, TransactionCount: 900,
			ProductCount: 7, TotalValueUSD: 5e8, TotalQuantityMT: 120000,
		}},
		entityProducts: models.TradeEntityBreakdown{
			EntityName: "SASOL CHEMICALS (PTY) LTD", EntityType: "supplier", TotalCount: 1,
			Products: []models.TradeEntityProduct{{Name: "ETHYLENE", TransactionCount: 900, Rank: 1}},
		},
	}
	client := &recordingNamesMock{
		TradeDBSource: inner,
		importer:      map[string][]string{"sasol chemicals": {"OTHER SASOL ENTITY LTD"}},
		supplier:      map[string][]string{},
	}
	// A miss is cached first, proving the override clears it.
	if background, _ := BuildTradeProfile(context.Background(), client, nil, "Sasol Chemicals"); background == nil || background.Present {
		t.Fatalf("pre-override background = %+v, want a cached miss", background)
	}
	SetTradeEntityOverride("Sasol Chemicals", "SASOL CHEMICALS (PTY) LTD", models.TradeDirectionSupplier)
	client.queries = nil
	background, summary := BuildTradeProfile(context.Background(), client, nil, "Sasol Chemicals")
	if background == nil || !background.Present {
		t.Fatalf("background = %+v, want the overridden profile", background)
	}
	if background.MatchedName != "SASOL CHEMICALS (PTY) LTD" || background.GlobalRank != 12 {
		t.Errorf("background = %+v, want the chosen entity's aggregates", background)
	}
	if len(background.Directions) != 2 || background.Directions[0] != models.TradeDirectionImporter || background.Directions[1] != models.TradeDirectionSupplier {
		t.Errorf("directions = %v, want both available sides", background.Directions)
	}
	if len(client.queries) != 0 {
		t.Errorf("queries = %v, want no typeahead calls after an explicit pick", client.queries)
	}
	if !strings.Contains(summary, "rank #12") {
		t.Errorf("summary = %q, want the aggregates", summary)
	}
}

// resetTradeEntityOverrides clears the shared override store between tests.
func resetTradeEntityOverrides() {
	tradeEntityOverrides.mu.Lock()
	defer tradeEntityOverrides.mu.Unlock()
	tradeEntityOverrides.entries = make(map[string]tradeEntityOverride)
	tradeEntityOverrides.order = nil
}

// TestTradeEntityMatchesSurfacesPicksBeforeTheRun pins the console matcher's
// contract: a confident resolution comes back alone, and a miss comes back as
// graded suggestions carrying their side, so the operator can pin one before a
// run instead of discovering the empty profile afterwards.
func TestTradeEntityMatchesSurfacesPicksBeforeTheRun(t *testing.T) {
	resetTradeEntityOverrides()
	exact := &recordingNamesMock{
		TradeDBSource: &tradeDBMock{options: &models.TradeFilterOptions{}},
		importer:      map[string][]string{},
		supplier:      map[string][]string{"basf": {"BASF SE"}},
	}
	matches, confident := TradeEntityMatches(context.Background(), exact, nil, "BASF")
	if !confident || len(matches) != 1 || matches[0].Name != "BASF SE" || matches[0].Direction != models.TradeDirectionSupplier {
		t.Fatalf("matches = %+v confident=%v, want the single supplier-side exact match", matches, confident)
	}
	if matches[0].Grade != "exact" {
		t.Errorf("grade = %q, want exact", matches[0].Grade)
	}

	miss := &recordingNamesMock{
		TradeDBSource: &tradeDBMock{options: &models.TradeFilterOptions{}},
		importer: map[string][]string{
			"klj": {"KLJ RESOURCES AFRICA LTD", "KLJ RESOURCES KENYA LTD"},
		},
		supplier: map[string][]string{},
	}
	matches, confident = TradeEntityMatches(context.Background(), miss, nil, "KLJ")
	if confident {
		t.Error("confident = true on a miss, want suggestions only")
	}
	if len(matches) == 0 {
		t.Fatal("matches = none, want the stored spellings offered")
	}
	for _, match := range matches {
		if match.Name == "" || match.Direction != models.TradeDirectionImporter {
			t.Errorf("match = %+v, want a named importer-side suggestion", match)
		}
	}

	if matches, confident := TradeEntityMatches(context.Background(), exact, nil, "  "); len(matches) != 0 || confident {
		t.Errorf("blank name = %+v confident=%v, want nothing", matches, confident)
	}
	if matches, confident := TradeEntityMatches(context.Background(), nil, nil, "BASF"); len(matches) != 0 || confident {
		t.Errorf("nil client = %+v confident=%v, want nothing", matches, confident)
	}
}

// blockingNamesMock holds each side's first EntityNames call until both sides
// have arrived, so the resolver can only finish if it walks the two sides
// concurrently. A serial implementation deadlocks on the rendezvous until the
// context expires.
type blockingNamesMock struct {
	TradeDBSource
	arrived chan string
	release chan struct{}
}

func (m *blockingNamesMock) EntityNames(ctx context.Context, direction, _ string, _ int) ([]string, error) {
	select {
	case m.arrived <- direction:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-m.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return nil, nil
}

// TestResolveTradeEntityQueriesBothSidesConcurrently pins the concurrency the
// resolver promises: a cold profile build must not pay the typeahead latency
// twice. Both sides have to be in flight before either is released.
func TestResolveTradeEntityQueriesBothSidesConcurrently(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &blockingNamesMock{arrived: make(chan string, 2), release: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		resolveTradeEntity(ctx, client, "ACME TRADING LIMITED")
	}()
	seen := map[string]bool{}
	for index := 0; index < 2; index++ {
		select {
		case side := <-client.arrived:
			seen[side] = true
		case <-ctx.Done():
			t.Fatalf("only %d side(s) reached the typeahead within the budget; the sides are not concurrent", index)
		}
	}
	close(client.release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("the resolver did not finish after both sides were released")
	}
	if !seen[models.TradeDirectionImporter] || !seen[models.TradeDirectionSupplier] {
		t.Errorf("sides seen = %v, want both importer and supplier in flight", seen)
	}
}
