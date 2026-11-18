package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestBuildTradeProfileResolvesAndAggregates pins the three-step build on the
// scripted client: the typeahead exact match feeds the aggregate profile, and
// the product mix lands on the background with its totals.
func TestBuildTradeProfileResolvesAndAggregates(t *testing.T) {
	mock := &tradeDBMock{
		options: &models.TradeFilterOptions{CanonicalProductNames: []string{"N BUTANOL (NBA)"}},
		entityProfiles: []models.TradeEntityProfile{{
			Name: "KLJ RESOURCES LTD", GlobalRank: 1, TransactionCount: 4692,
			ProductCount: 20, TotalValueUSD: 1400347748, TotalQuantityMT: 520000,
		}},
		entityProducts: models.TradeEntityBreakdown{
			EntityName: "KLJ RESOURCES LTD", EntityType: "importer", TotalCount: 5,
			Products: []models.TradeEntityProduct{
				{Name: "TOLUENE", TransactionCount: 760, TotalValueUSD: 260442574, TotalQuantityMT: 284962, Rank: 1},
				{Name: "ACETIC ACID", TransactionCount: 439, TotalValueUSD: 79173636, TotalQuantityMT: 202558, Rank: 2},
			},
		},
	}
	// The name resolver is scripted per direction via a wrapper: the mock's
	// EntityNames returns canned names, so wrap the mock to hand back the
	// exact spelling on the importer side.
	resetTradeProfileMemo()
	client := &exactNamesMock{TradeDBSource: mock, importerNames: []string{"KLJ RESOURCES LTD"}}
	background, summary := BuildTradeProfile(context.Background(), client, "KLJ Resources Limited")
	if background == nil || !background.Present {
		t.Fatalf("background = %+v, want a present profile", background)
	}
	if background.MatchedName != "KLJ RESOURCES LTD" || background.GlobalRank != 1 || background.TransactionCount != 4692 {
		t.Errorf("aggregates = %+v, want the profiled row", background)
	}
	if len(background.TopProducts) != 2 || background.TopProducts[0].Name != "TOLUENE" {
		t.Errorf("top products = %+v, want the breakdown head", background.TopProducts)
	}
	if !strings.Contains(summary, "rank #1") || !strings.Contains(summary, "TOLUENE") {
		t.Errorf("summary = %q, want rank and product mix", summary)
	}
}

// TestBuildTradeProfileGracefulWhenNothingMatches pins the fail-soft contract:
// no vocabulary match means Present=false with the closest names carried for
// the operator, never an error path.
func TestBuildTradeProfileGracefulWhenNothingMatches(t *testing.T) {
	mock := &tradeDBMock{options: &models.TradeFilterOptions{CanonicalProductNames: []string{"N BUTANOL (NBA)"}}}
	resetTradeProfileMemo()
	client := &exactNamesMock{TradeDBSource: mock, importerNames: []string{"UNRELATED CHEMICALS LTD"}}
	background, summary := BuildTradeProfile(context.Background(), client, "KLJ Resources Limited")
	if background == nil {
		t.Fatal("background = nil, want a graceful miss")
	}
	if background.Present {
		t.Error("background.Present = true, want a miss")
	}
	if len(background.NameVariants) == 0 {
		t.Error("the miss must still carry the closest vocabulary names")
	}
	if !strings.Contains(summary, "no exact customs-entity match") {
		t.Errorf("summary = %q, want the miss wording", summary)
	}
}

// TestEnrichVerifiedTradeProfiles pins the verified-row pass: rows are
// enriched in order, the cap holds, and one summary event reports the outcome.
func TestEnrichVerifiedTradeProfiles(t *testing.T) {
	inner := &tradeDBMock{options: &models.TradeFilterOptions{}}
	resetTradeProfileMemo()
	client := &exactNamesMock{TradeDBSource: inner, importerNames: []string{"KLJ RESOURCES LTD"}}
	engine := NewEngine(nil, nil, nil, nil, 1, 1, 1, WithTradeDB(client), WithTradeDBPartnerLimit(1))
	results := []models.MarketLandscapeCompany{
		{Name: "KLJ Resources Limited", Status: "verified"},
		{Name: "Second Company Ltd", Status: "verified"},
	}
	eventCh := make(chan models.AgentEvent, 16)
	enriched := enrichVerifiedTradeProfiles(context.Background(), engine, results, eventCh)
	close(eventCh)
	if enriched != 1 {
		t.Fatalf("enriched = %d, want 1 (the partner cap)", enriched)
	}
	if results[0].TradeProfile == nil || !results[0].TradeProfile.Present {
		t.Fatalf("first row not enriched: %+v", results[0].TradeProfile)
	}
	if results[1].TradeProfile != nil {
		t.Error("second row must stay nil under the cap")
	}
	sawSummary := false
	for event := range eventCh {
		if strings.Contains(event.Content, "Trade DB profiles attached to 1 verified row(s)") {
			sawSummary = true
		}
	}
	if !sawSummary {
		t.Error("the summary event is missing from the log")
	}
}

// resetTradeProfileMemo clears the shared profile cache, so tests that build
// the same name against different scripted clients do not read each other's
// memoized answers.
func resetTradeProfileMemo() {
	tradeProfileMemo.mu.Lock()
	defer tradeProfileMemo.mu.Unlock()
	tradeProfileMemo.entries = make(map[string]tradeProfileEntry)
	tradeProfileMemo.order = tradeProfileMemo.order[:0]
}

// exactNamesMock scripts EntityNames per direction on top of another source.
type exactNamesMock struct {
	TradeDBSource
	importerNames []string
	supplierNames []string
}

func (m *exactNamesMock) EntityNames(ctx context.Context, direction, query string, limit int) ([]string, error) {
	if direction == models.TradeDirectionSupplier {
		return m.supplierNames, nil
	}
	return m.importerNames, nil
}
