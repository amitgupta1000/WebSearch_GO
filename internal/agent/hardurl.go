package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// Blast-radius containment for the hard-URL handler: it may only consume a
// bounded slice of the run's budget, so a burst of blocked pages or profile
// walls can never starve the normal scrape path.
const (
	// maxHardURLsPerRun caps how many URLs a single research run may escalate.
	maxHardURLsPerRun = 2
	// hardURLBudget bounds the whole ladder for one URL.
	hardURLBudget = 30 * time.Second
	// hardURLMinRunRemaining: escalation is only worth starting when at least
	// this much of the run's deadline remains (mirrors minQCIterationBudget),
	// so a long run degrades to snippets instead of failing on the clock.
	hardURLMinRunRemaining = 45 * time.Second
)

// hardURLHandler is the bounded specialist for URLs the normal pipeline
// refuses or fails on: bot-challenge interstitials and professional profiles
// (LinkedIn). It escalates through the scraper's ladder — plain browser
// render, cookie-bridged render, extended-settle render — under its own
// budget and cache mode. Every failure degrades to exactly the previous
// behaviour (snippet-fallback evidence), so the main run's blast radius is
// one bounded time slice plus a report line, never a stall or an error.
//
// The handler still never solves challenges: no CAPTCHA clicks, no
// press-and-hold, no credential entry. It renders public pages (optionally
// carrying the user's own exported session) and reports honestly when the
// wall survives every step.
type hardURLHandler struct {
	engine *Engine
	used   int
}

func newHardURLHandler(engine *Engine) *hardURLHandler {
	return &hardURLHandler{engine: engine}
}

// hardOutcome is the handler's report back to the calling agent.
type hardOutcome struct {
	// Status is "recovered", "failed", "deferred" or "unavailable".
	Status string
	Reason string
	Result *models.ScrapeResult
}

// available reports whether escalation may run at all: the engine has the
// feature enabled and the scraper implements the ladder interface.
func (h *hardURLHandler) available() bool {
	return h.engine != nil && h.engine.HardURLsAvailable()
}

// handle escalates one URL. Outcomes never panic the caller: every path
// returns a classified hardOutcome the caller can record and move past.
func (h *hardURLHandler) handle(ctx context.Context, sessionID, targetURL, label string, step int, eventCh chan<- models.AgentEvent, opts tools.HardScrapeOptions) hardOutcome {
	if !h.available() {
		return hardOutcome{Status: "unavailable", Reason: "hard-URL handler disabled or browser unavailable"}
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < hardURLMinRunRemaining {
		return hardOutcome{Status: "deferred", Reason: "insufficient remaining research budget"}
	}
	if h.used >= maxHardURLsPerRun {
		return hardOutcome{Status: "deferred", Reason: fmt.Sprintf("per-run limit of %d hard URLs reached", maxHardURLsPerRun)}
	}
	h.used++

	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventToolCall,
		Step:      step,
		ToolName:  "hard_url_handler",
		ToolInput: targetURL,
		Content:   "Escalating " + label + " to the hard-URL handler...",
	})

	attemptCtx, cancel := context.WithTimeout(ctx, hardURLBudget)
	defer cancel()
	result, err := h.engine.ScrapeHardCached(attemptCtx, targetURL, opts)

	outcome := hardOutcome{Result: result}
	if err == nil {
		outcome.Status = "recovered"
		outcome.Reason = "hard-URL ladder recovered the page (" + result.FallbackReason + ")"
	} else if errors.Is(err, tools.ErrHardLadderUnavailable) {
		outcome.Status = "unavailable"
		outcome.Reason = err.Error()
	} else {
		outcome.Status = "failed"
		outcome.Reason = err.Error()
	}

	if outcome.Status == "recovered" {
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventToolResult,
			Step:      step,
			ToolName:  "hard_url_handler",
			Content:   "Hard-URL handler recovered " + label + " via " + result.FallbackReason + " (" + fmt.Sprintf("%d words)", result.WordCount),
		})
	} else {
		Emit(eventCh, models.AgentEvent{
			SessionID: sessionID,
			Type:      models.EventToolResult,
			Step:      step,
			ToolName:  "hard_url_handler",
			Content:   "Hard-URL handler could not read " + label + ": " + outcome.Reason,
		})
	}
	return outcome
}