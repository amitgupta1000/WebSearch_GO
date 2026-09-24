package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// entity_lookup.go runs company-anchored lookups *before* synthesis, so a thin roster is
// filled from sources that name the company instead of from whatever a generic query
// returned.
//
// This is the proactive half of the name+company lookup policy. The post-synthesis pass in
// validate.go validates and rescues rows that already exist; this pass supplies the evidence
// those rows would otherwise lack. Its queries are anchored by construction
// (`"<company>" <area> head`), so every snippet it admits already names the company — which
// is why it is safe to run before the entity gate can inspect rows.
const (
	// defaultGapLookups is how many uncovered areas one run may look up.
	defaultGapLookups = 4
	// gapLookupTimeout is the ceiling for one gap search. They are snippet-only, so this is
	// generous; the effective timeout is the smaller of this and the headroom below.
	gapLookupTimeout = 10 * time.Second
	// gapEvidencePerLookup caps how many snippets one gap lookup contributes.
	gapEvidencePerLookup = 3
)

// gapAreas are the functions worth one anchored lookup when the collected evidence does not
// cover them. Leadership always comes first because an org chart with no leadership is the
// thinnest possible result.
var gapAreas = []struct {
	name     string
	keywords []string
}{
	{name: "leadership", keywords: []string{"managing director", "chief executive", "ceo", "promoter", "board"}},
	{name: "procurement", keywords: []string{"procurement", "purchas", "sourcing"}},
	{name: "supply chain", keywords: []string{"supply chain", "logistics", "warehouse"}},
	{name: "finance", keywords: []string{"finance", "chief financial", "cfo", "accounts"}},
	{name: "marketing", keywords: []string{"marketing", "brand", "sales"}},
}

// fillEntityGaps looks up the areas the evidence does not cover and appends the anchored
// snippets to the evidence set. It reports how many searches it ran and how many evidence
// items it added on the gate, so a run can say where its roster came from.
func (ca *CompanyAgent) fillEntityGaps(ctx context.Context, sessionID string, brief models.CompanyBrief, state *models.CompanyResearchContext, eventCh chan<- models.AgentEvent, matcher *entityMatcher, gate *models.EntityGate) {
	if matcher == nil || state == nil || gate == nil {
		return
	}
	if !hasGapLookupBudget(ctx) {
		return
	}
	limit := defaultGapLookups
	added := 0
	for _, area := range gapAreas {
		if gate.GapLookups >= limit {
			break
		}
		if evidenceCovers(state.Evidence, area.keywords) {
			continue
		}
		if !hasGapLookupBudget(ctx) {
			break
		}
		query := fmt.Sprintf("\"%s\" %s head director manager", strings.TrimSpace(brief.CompanyName), area.name)
		searchCtx, cancel := context.WithTimeout(ctx, gapLookupTimeoutFor(ctx))
		results, err := ca.engine.SearchCached(searchCtx, query, ca.engine.MaxSearchResults())
		cancel()
		gate.GapLookups++
		if err != nil {
			// A failed gap lookup is not a run failure: the evidence already collected is
			// what the synthesis will use.
			continue
		}
		kept := 0
		for _, result := range results {
			if kept >= gapEvidencePerLookup {
				break
			}
			// The query names the company, but the result must too: a search engine will
			// happily return a same-named firm for an anchored query.
			if !matcher.match(result.Link, result.Title, result.Snippet).Matched {
				continue
			}
			entry := models.Evidence{
				URL:          result.Link,
				Title:        result.Title,
				Content:      result.Snippet,
				Score:        0.5,
				EvidenceType: "entity_lookup",
				SourceType:   "entity-lookup",
				QualityScore: 0.4,
			}
			if when, ok := searchResultDate(result); ok {
				entry.AsOf = when
				entry.DateSignal = dateSignalSearch
			}
			before := len(state.Evidence)
			state.Evidence = appendUniqueEvidence(state.Evidence, []models.Evidence{entry})
			if len(state.Evidence) > before {
				kept++
			}
		}
		added += kept
		if kept > 0 {
			Emit(eventCh, models.AgentEvent{
				SessionID: sessionID,
				Type:      models.EventToolResult,
				Step:      6,
				ToolName:  "entity_lookup",
				Content:   fmt.Sprintf("Anchored lookup for %s added %d company-named source(s)", area.name, kept),
			})
		}
	}
	gate.GapEvidence = added
}

// hasGapLookupBudget reports whether the run can afford a gap lookup and still leave both
// the synthesis and the post-synthesis validation pass their reserves. Scraping stops
// scrapeReserve (both reserves included) before the deadline, so this is the test that keeps
// the proactive pass from being the reason the reactive one does not run.
func hasGapLookupBudget(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return time.Until(deadline) > validationReserve+synthesisReserve
}

// gapLookupTimeoutFor bounds one gap lookup by whichever is smaller: the fixed ceiling, or
// what is left once both reserves are set aside. A single lookup can then never be the reason
// synthesis or the freshness pass runs out of budget.
func gapLookupTimeoutFor(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return gapLookupTimeout
	}
	headroom := time.Until(deadline) - validationReserve - synthesisReserve
	switch {
	case headroom <= 0:
		return time.Millisecond
	case headroom < gapLookupTimeout:
		return headroom
	default:
		return gapLookupTimeout
	}
}

// evidenceCovers reports whether the collected evidence already touches an area, judged by
// its keywords in the title, headings or the first part of its text. It is a cheap
// "do not spend a search on what we have" test, deliberately generous: the point is to fill
// genuine gaps, not to top up areas that are already covered.
func evidenceCovers(evidence []models.Evidence, keywords []string) bool {
	for _, item := range evidence {
		haystack := strings.ToLower(item.Title + " " + strings.Join(item.Headings, " "))
		text := strings.ToLower(item.Content)
		if len(text) > 2000 {
			text = text[:2000]
		}
		haystack += " " + text
		for _, keyword := range keywords {
			if strings.Contains(haystack, keyword) {
				return true
			}
		}
	}
	return false
}
