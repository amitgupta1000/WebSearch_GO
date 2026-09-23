package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// budgetBoundLadder blocks until its context is done, standing in for a ladder
// whose steps consume the whole budget without producing a page.
type budgetBoundLadder struct{ mockScraper }

func (b *budgetBoundLadder) ScrapeHard(ctx context.Context, _ string, _ tools.HardScrapeOptions) (*models.ScrapeResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// The budget used to be a hand-written 30s while the ladder's own step caps summed
// to 55s, which left the extended-settle step — the one written for slow
// interstitials — with no time to run.
func TestDefaultHardURLBudgetCoversEveryLadderStep(t *testing.T) {
	budget, stepSum := defaultHardURLBudget(), tools.HardLadderBudget()
	if budget <= 0 {
		t.Fatalf("expected a positive default budget, got %v", budget)
	}
	if budget < stepSum {
		t.Errorf("default budget %v is below the ladder's worst case %v, so its final step can be clipped", budget, stepSum)
	}
}

func TestHardURLHandlerHonoursConfiguredLimits(t *testing.T) {
	scraper := &hardScraperMock{recovered: recoveredProfile("https://acme.com/leadership")}
	engine, store := newHardTestEngine(scraper)
	defer store.Close()

	WithHardURLLimits(1, 90*time.Second)(engine)
	if got := engine.HardURLsPerRun(); got != 1 {
		t.Fatalf("HardURLsPerRun() = %d, want the configured 1", got)
	}
	if got := engine.HardURLBudget(); got != 90*time.Second {
		t.Fatalf("HardURLBudget() = %v, want the configured 90s", got)
	}

	handler := newHardURLHandler(engine)
	first := handler.handle(context.Background(), "session", "https://acme.com/a", "blocked page", 1, nil, tools.HardScrapeOptions{})
	if first.Status != "recovered" {
		t.Fatalf("first escalation: got %+v, want a recovery", first)
	}
	second := handler.handle(context.Background(), "session", "https://acme.com/b", "blocked page", 1, nil, tools.HardScrapeOptions{})
	if second.Status != "deferred" || !strings.Contains(second.Reason, "per-run limit of 1") {
		t.Fatalf("second escalation: got %+v, want a deferral from the configured cap", second)
	}
}

// A ladder that runs out of its own budget has not lost to the wall, so it must be
// reported as a deferral: calling it a failure would hide that a step never got the
// time it asked for.
func TestHardURLHandlerDefersWhenTheLadderRunsOutOfBudget(t *testing.T) {
	engine, store := newHardTestEngine(&budgetBoundLadder{})
	defer store.Close()
	WithHardURLLimits(2, 60*time.Millisecond)(engine)

	handler := newHardURLHandler(engine)
	outcome := handler.handle(context.Background(), "session", "https://acme.com/leadership", "blocked page", 1, nil, tools.HardScrapeOptions{})
	if outcome.Status != "deferred" {
		t.Fatalf("got %+v, want a deferral for a budget-exhausted ladder", outcome)
	}
	if !strings.Contains(outcome.Reason, "ran out of budget") {
		t.Errorf("expected the reason to name the budget, got %q", outcome.Reason)
	}
}
