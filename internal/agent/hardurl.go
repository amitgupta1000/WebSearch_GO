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
	// defaultHardURLsPerRun caps how many URLs a single research run may escalate
	// when no limit is configured.
	defaultHardURLsPerRun = 2
	// hardURLPostLadderMargin is how much of the run's deadline must still be left
	// after the ladder, for the synthesis and QC that follow it.
	hardURLPostLadderMargin = 20 * time.Second
)

// defaultHardURLBudget bounds the whole ladder for one URL. It is derived from the
// ladder's own step caps, so raising a step timeout can never silently clip the
// final step: a hand-written budget below the step sum did exactly that, leaving
// the extended-settle render with no time to run.
func defaultHardURLBudget() time.Duration {
	return tools.HardLadderBudget() + 5*time.Second
}

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

// perRunLimit is how many URLs this run may escalate: the configured value, or the
// default when none is set.
func (h *hardURLHandler) perRunLimit() int {
	if h.engine != nil {
		if configured := h.engine.HardURLsPerRun(); configured > 0 {
			return configured
		}
	}
	return defaultHardURLsPerRun
}

// urlBudget bounds the whole ladder for one URL: the configured value, or the
// default derived from the ladder's own step caps.
func (h *hardURLHandler) urlBudget() time.Duration {
	if h.engine != nil {
		if configured := h.engine.HardURLBudget(); configured > 0 {
			return configured
		}
	}
	return defaultHardURLBudget()
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
	if deadline, ok := ctx.Deadline(); ok {
		// Escalation only starts when the whole ladder, plus the synthesis that must
		// follow it, still fits inside the run's deadline. Starting one that cannot
		// finish wastes the ladder's whole budget and returns nothing.
		if remaining := time.Until(deadline); remaining < h.urlBudget()+hardURLPostLadderMargin {
			return hardOutcome{Status: "deferred", Reason: fmt.Sprintf(
				"insufficient remaining research budget (%s left, need %s)",
				remaining.Truncate(time.Second), (h.urlBudget() + hardURLPostLadderMargin).Truncate(time.Second))}
		}
	}
	if limit := h.perRunLimit(); h.used >= limit {
		return hardOutcome{Status: "deferred", Reason: fmt.Sprintf("per-run limit of %d hard URLs reached", limit)}
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

	attemptCtx, cancel := context.WithTimeout(ctx, h.urlBudget())
	defer cancel()
	result, err := h.engine.ScrapeHardCached(attemptCtx, targetURL, opts)

	outcome := hardOutcome{Result: result}
	switch {
	case err == nil:
		outcome.Status = "recovered"
		outcome.Reason = "hard-URL ladder recovered the page (" + result.FallbackReason + ")"
	case errors.Is(err, tools.ErrHardLadderUnavailable):
		outcome.Status = "unavailable"
		outcome.Reason = err.Error()
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		// The ladder ran out of its own slice of the budget rather than losing to
		// the wall. Calling that a failure would hide the fact that a step never got
		// the time it asked for, which is exactly the signal needed to tune it.
		outcome.Status = "deferred"
		outcome.Reason = "hard-URL ladder ran out of budget: " + err.Error()
	default:
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
