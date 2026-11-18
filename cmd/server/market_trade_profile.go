package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// handleMarketTradeProfile is the on-demand counterpart of the automatic
// verified-row enrichment: a "Fetch Trade Profile" button next to a company
// name renders that company's aggregated Crystal customs profile in place —
// rank within the profiled top set, shipment and value totals, and the
// entity's own product mix. The build is cached per company name (the same
// memo the dossier and the landscape rows use), so a click on a name the run
// already enriched pays nothing.
func (s *Server) handleMarketTradeProfile(w http.ResponseWriter, r *http.Request) {
	company := strings.TrimSpace(r.FormValue("company_name"))
	if company == "" {
		s.render(w, "market_trade_profile_result", marketTradeProfileView{
			Error: "No company name was supplied for the trade profile lookup.",
		})
		return
	}
	client := s.engine.TradeDB()
	if client == nil {
		s.render(w, "market_trade_profile_result", marketTradeProfileView{
			Company: company,
			Error:   "The trade DB is not configured on this deployment, so there is no customs profile to fetch.",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), agent.TradeProfileTimeout)
	defer cancel()
	background, summary := agent.BuildTradeProfile(ctx, client, company)
	if background == nil {
		s.render(w, "market_trade_profile_result", marketTradeProfileView{
			Company: company,
			Error:   "The trade profile could not be built for this name.",
		})
		return
	}
	s.render(w, "market_trade_profile_result", marketTradeProfileView{
		Company:    company,
		Background: background,
		Summary:    summary,
	})
}

// marketTradeProfileView is one button answer: the built background plus the
// summary line, or an error note.
type marketTradeProfileView struct {
	Company    string
	Background *models.TradeBackground
	Summary    string
	Error      string
}

// ensure the timeout constant is the one source of truth for both callers.
