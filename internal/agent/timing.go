package agent

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// phaseTimer accumulates how long each phase of a research run took.
//
// A run is a sequence of expensive phases (plan, search, rank, scrape,
// synthesize, review) whose costs are dominated by different things: network
// latency, model latency, or the page fetch policy. Recording them turns any
// performance discussion into a comparison of measured numbers.
type phaseTimer struct {
	mu      sync.Mutex
	timings []models.PhaseTiming
}

// newPhaseTimer returns an empty timer.
func newPhaseTimer() *phaseTimer { return &phaseTimer{} }

// start begins a phase and returns the function that ends it, taking a short
// note about what the phase covered.
//
//	finish := timer.start("scrape")
//	...work...
//	finish("7 pages")
func (t *phaseTimer) start(phase string) func(detail string) {
	if t == nil {
		return func(string) {}
	}
	started := time.Now()
	return func(detail string) { t.record(phase, time.Since(started), detail) }
}

// record adds one measured duration in completion order.
func (t *phaseTimer) record(phase string, duration time.Duration, detail string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.timings = append(t.timings, models.PhaseTiming{
		Phase:  phase,
		Millis: duration.Milliseconds(),
		Detail: detail,
	})
}

// results returns a copy of the recorded timings.
func (t *phaseTimer) results() []models.PhaseTiming {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]models.PhaseTiming(nil), t.timings...)
}

// report renders the timings as a single summary line, longest phase first, for
// the agent log and the run console.
func (t *phaseTimer) report() string {
	timings := t.results()
	if len(timings) == 0 {
		return ""
	}
	ordered := append([]models.PhaseTiming(nil), timings...)
	sortPhaseTimingsByDuration(ordered)

	var builder strings.Builder
	var total int64
	for _, timing := range timings {
		total += timing.Millis
	}
	builder.WriteString("Phase timings (heaviest first): ")
	for index, timing := range ordered {
		if index > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(timing.Phase)
		builder.WriteString(" ")
		builder.WriteString(formatMillis(timing.Millis))
	}
	builder.WriteString(" | total ")
	builder.WriteString(formatMillis(total))
	return builder.String()
}

// sortPhaseTimingsByDuration orders timings by descending duration, keeping the
// first-seen order for equal durations.
func sortPhaseTimingsByDuration(timings []models.PhaseTiming) {
	for i := 1; i < len(timings); i++ {
		for j := i; j > 0 && timings[j].Millis > timings[j-1].Millis; j-- {
			timings[j], timings[j-1] = timings[j-1], timings[j]
		}
	}
}

// formatMillis renders a millisecond count as seconds or milliseconds, whichever
// reads better at that magnitude.
func formatMillis(millis int64) string {
	if millis >= 1000 {
		return strconv.FormatFloat(float64(millis)/1000, 'f', 1, 64) + "s"
	}
	return fmt.Sprintf("%dms", millis)
}
