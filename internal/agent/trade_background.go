package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// trade_background.go enriches a researched company with its aggregated
// Crystal customs profile — the entity-profile layer the trade DB publishes
// (rank within the profiled top set, shipments, product count, value) plus
// the entity's own product mix. The build itself lives in trade_profile.go so
// the dossier, the landscape rows and the on-demand button share one path.
//
// TradeProfileTimeout (there) bounds the build; the endpoints answer in tens
// of milliseconds warm, but a fresh Cloud Run instance can cold start a
// query, and a finishing run must never hang on that.

// attachTradeBackground builds the trade profile for the confirmed company
// and attaches it to the chart. Fail-soft throughout: an unreachable DB
// leaves a Note on the background rather than failing the run.
func (ca *CompanyAgent) attachTradeBackground(ctx context.Context, sessionID string, brief models.CompanyBrief, chart *models.OrgChart, eventCh chan<- models.AgentEvent) {
	client := ca.engine.TradeDB()
	if client == nil || chart == nil {
		return
	}
	name := strings.TrimSpace(brief.CompanyName)
	if name == "" {
		return
	}
	// The run may have spent its whole budget by the time this runs; the
	// build rides a detached context, like the cache writes do.
	dctx := context.WithoutCancel(ctx)
	tctx, cancel := context.WithTimeout(dctx, TradeProfileTimeout)
	defer cancel()

	var llm LLMClient
	if ca.engine != nil {
		llm = ca.engine
	}
	background, summary := BuildTradeProfile(tctx, client, llm, name)
	if background == nil {
		return
	}
	chart.TradeBackground = background
	if background.Note != "" && !background.Present {
		emitAgentEvent(eventCh, models.EventThinking,
			fmt.Sprintf("Trade DB profile unavailable for %q: %s", name, summary),
			"trade-db", "entity-profile")
		return
	}
	emitAgentEvent(eventCh, models.EventToolResult, summary, "trade-db", "entity-profile")
}

// orNoneList renders a short list for the log, or a dash when empty.
func orNoneList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, " · ")
}

// enrichVerifiedTradeProfiles attaches each verified result row's aggregated
// customs profile. It is deliberately light: the per-name cache means a name
// researched anywhere in the session pays one round trip, the trade-partner
// cap bounds the spend, and the run's own context bounds the pass — a row
// that misses, or a deadline that lands, leaves the row unchanged. One
// summary event carries the outcome, so the console log shows what customs
// knows about the verified rows without a flood of per-row lines.
func enrichVerifiedTradeProfiles(ctx context.Context, engine *Engine, results []models.MarketLandscapeCompany, eventCh chan<- models.AgentEvent) int {
	client := engine.TradeDB()
	if client == nil || len(results) == 0 {
		return 0
	}
	limit := engine.TradeDBPartnerLimit()
	enriched := 0
	var heads []string
	for index := range results {
		if enriched >= limit {
			break
		}
		row := &results[index]
		name := strings.TrimSpace(row.Name)
		if name == "" {
			continue
		}
		var llm LLMClient
		if engine != nil {
			llm = engine
		}
		background, _ := BuildTradeProfile(ctx, client, llm, name)
		if background == nil || !background.Present {
			continue
		}
		row.TradeProfile = background
		enriched++
		if len(heads) < 3 {
			heads = append(heads, name+" ("+tradeProfileLine(background)+")")
		}
	}
	if enriched > 0 {
		emitAgentEvent(eventCh, models.EventToolResult,
			fmt.Sprintf("Trade DB profiles attached to %d verified row(s) — head: %s", enriched, strings.Join(heads, " · ")),
			"trade-db", "entity-profile")
	}
	return enriched
}

// tradeProfileLine is the compact one-line form a report row carries.
func tradeProfileLine(background *models.TradeBackground) string {
	var parts []string
	if background.TransactionCount > 0 {
		parts = append(parts, fmt.Sprintf("%d shipment(s)", background.TransactionCount))
	}
	if background.TotalValueUSD > 0 {
		parts = append(parts, fmt.Sprintf("USD %.0f", background.TotalValueUSD))
	}
	if len(background.TopProducts) > 0 {
		top := make([]string, 0, 2)
		for _, product := range background.TopProducts {
			if len(top) == 2 {
				break
			}
			top = append(top, product.Name)
		}
		parts = append(parts, "top: "+strings.Join(top, ", "))
	}
	if len(parts) == 0 {
		return "in the customs vocabulary"
	}
	return strings.Join(parts, " · ")
}
