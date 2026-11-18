package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// The vocabulary below mirrors what the live Crystal API serves for the
// butanol family, plus a plain caustic entry for the exact-match path.
var tradeProductTestVocabulary = []string{
	"ISOBUTANOL",
	"N BUTANOL (NBA)",
	"TERTIARY BUTANOL",
	"CAUSTIC SODA",
	"CAUSTIC SODA (PEARL)",
}

// TestNormalizeTradeProductNameMeetsTheDatabase pins the punctuation folding:
// the DB's token match is blind to hyphens, so the normalizer is what lets a
// typed "n-Butanol" meet the records' "N BUTANOL".
func TestNormalizeTradeProductNameMeetsTheDatabase(t *testing.T) {
	cases := []struct {
		left, right string
		wantEqual   bool
	}{
		{"n-Butanol", "N BUTANOL", true},
		{"n-butanol", "N_BUTANOL", true},
		{"  N   BUTANOL ", "n butanol", true},
		{"n-butanol", "N BUTANOL (NBA)", false},
	}
	for _, tc := range cases {
		left, right := normalizeTradeProductName(tc.left), normalizeTradeProductName(tc.right)
		if got := left == right; got != tc.wantEqual {
			t.Errorf("normalize(%q)=%q vs normalize(%q)=%q: equal=%v, want %v", tc.left, left, tc.right, right, got, tc.wantEqual)
		}
	}
}

// TestTradeProductMatchesRanksTheButanolFamily pins the ranking on the live
// vocabulary's shape: the full-token match outranks the partial one, the
// compound "ISOBUTANOL" never matches "n-butanol" (the token does not split),
// and the normalized exact-ness is honest — "n-butanol" is NOT an exact match
// for "N BUTANOL (NBA)", which is precisely why the operator picks.
func TestTradeProductMatchesRanksTheButanolFamily(t *testing.T) {
	matches := TradeProductMatches("n-butanol", tradeProductTestVocabulary)
	if len(matches) == 0 {
		t.Fatal("no candidates for n-butanol")
	}
	if matches[0].Name != "N BUTANOL (NBA)" {
		t.Fatalf("top match = %q, want N BUTANOL (NBA)", matches[0].Name)
	}
	if matches[0].Exact {
		t.Error("n-butanol must not claim an exact match against N BUTANOL (NBA)")
	}
	for _, match := range matches {
		if match.Name == "ISOBUTANOL" {
			t.Error("ISOBUTANOL must not match n-butanol: its compound token does not split")
		}
	}
	for i := 1; i < len(matches); i++ {
		if matches[i].Score > matches[i-1].Score {
			t.Errorf("ranking not descending: %d (%q) before %d (%q)", matches[i-1].Score, matches[i-1].Name, matches[i].Score, matches[i].Name)
		}
	}
}

// TestResolveTradeProductPrefersTheOperatorPick pins the contract the console
// relies on: an operator-picked name wins outright, whatever the vocabulary.
func TestResolveTradeProductPrefersTheOperatorPick(t *testing.T) {
	product, _, _, source := ResolveTradeProduct("n-butanol", "TERTIARY BUTANOL", tradeProductTestVocabulary)
	if product != "TERTIARY BUTANOL" || source != TradeProductSourceOperator {
		t.Errorf("product=%q source=%q, want the operator pick", product, source)
	}
}

// TestResolveTradeProductTakesTheExactVocabularyMatch pins the one automatic
// upgrade the run takes on itself: a normalized exact match against the DB's
// canonical vocabulary ("caustic soda" → "CAUSTIC SODA").
func TestResolveTradeProductTakesTheExactVocabularyMatch(t *testing.T) {
	product, match, _, source := ResolveTradeProduct("caustic soda", "", tradeProductTestVocabulary)
	if source != TradeProductSourceVocabulary || product != "CAUSTIC SODA" || match == nil {
		t.Fatalf("product=%q source=%q match=%v, want the exact vocabulary match", product, source, match)
	}
}

// TestResolveTradeProductKeepsTheRawActivityWhenNothingMatches pins the
// conservative fallback: no exact match means the raw activity travels to the
// DB unchanged, with the ranked candidates for the operator to pick from.
func TestResolveTradeProductKeepsTheRawActivityWhenNothingMatches(t *testing.T) {
	product, match, candidates, source := ResolveTradeProduct("n-butanol", "", tradeProductTestVocabulary)
	if source != TradeProductSourceRaw || product != "n-butanol" || match != nil {
		t.Fatalf("product=%q source=%q, want the raw fallback", product, source)
	}
	if len(candidates) == 0 {
		t.Error("the raw fallback must still carry ranked candidates for the operator")
	}
}

// TestCollectTradeImportersEmitsQueryAndResultEvents pins the console-log
// contract: the trade DB's query and its answer are visible tool events, so a
// zero-row answer is as visible as a populated one.
func TestCollectTradeImportersEmitsQueryAndResultEvents(t *testing.T) {
	mock := &tradeDBMock{partners: []models.TradePartner{
		{Name: "Importer 1 Private Limited", Direction: models.TradeDirectionImporter, TransactionCount: 7, TotalValueUSD: 546885},
	}}
	eventCh := make(chan models.AgentEvent, 16)
	collectTradeImporters(context.Background(), mock, "N BUTANOL (NBA)", 5, []string{"India"}, nil, eventCh)
	close(eventCh)
	var sawCall, sawResult bool
	for event := range eventCh {
		if event.ToolName != "trade-db" {
			continue
		}
		switch event.Type {
		case models.EventToolCall:
			sawCall = true
			if !strings.Contains(event.Content, "N BUTANOL (NBA)") {
				t.Errorf("query event does not name the product searched: %q", event.Content)
			}
		case models.EventToolResult:
			sawResult = true
			if !strings.Contains(event.Content, "1 ranked importer row(s)") {
				t.Errorf("result event does not report the row count: %q", event.Content)
			}
		}
	}
	if !sawCall || !sawResult {
		t.Errorf("tool events incomplete: call=%v result=%v", sawCall, sawResult)
	}
}

// TestResolveTradeDBProductAnnouncesTheResolution pins the first trade-DB
// touch of a run: the resolution outcome (pick, vocabulary match, or raw
// fallback with candidates) lands in the log before any customs query runs.
func TestResolveTradeDBProductAnnouncesTheResolution(t *testing.T) {
	mock := &tradeDBMock{options: &models.TradeFilterOptions{CanonicalProductNames: tradeProductTestVocabulary}}
	eventCh := make(chan models.AgentEvent, 16)
	product := resolveTradeDBProduct(context.Background(), mock, "n-butanol", "", eventCh)
	close(eventCh)
	if product != "n-butanol" {
		t.Fatalf("product = %q, want the raw fallback (no exact vocabulary match)", product)
	}
	sawCandidates := false
	for event := range eventCh {
		if strings.Contains(event.Content, "Closest trade DB names") && strings.Contains(event.Content, "N BUTANOL (NBA)") {
			sawCandidates = true
		}
	}
	if !sawCandidates {
		t.Error("the raw fallback must name the closest vocabulary names in the log")
	}
}
