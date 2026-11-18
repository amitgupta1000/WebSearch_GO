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
	s.respondTradeProfile(w, r, false)
}

// handleTradeEntityProfile answers an operator's explicit entity pick (the
// suggestion chips on the dossier card or in an unmatched Fetch Trade Profile
// result): the chosen stored name is remembered for the researched company and
// the profile is built for it directly.
func (s *Server) handleTradeEntityProfile(w http.ResponseWriter, r *http.Request) {
	s.respondTradeProfile(w, r, true)
}

// respondTradeProfile is the shared body of the profile endpoints.
func (s *Server) respondTradeProfile(w http.ResponseWriter, r *http.Request, requireEntity bool) {
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
	entity := strings.TrimSpace(r.FormValue("trade_entity"))
	if requireEntity && entity == "" {
		s.render(w, "market_trade_profile_result", marketTradeProfileView{
			Company: company,
			Error:   "Choose one of the stored entity names to build its customs profile.",
		})
		return
	}
	if entity != "" {
		direction := strings.ToLower(strings.TrimSpace(r.FormValue("trade_direction")))
		if direction != models.TradeDirectionImporter && direction != models.TradeDirectionSupplier {
			direction = models.TradeDirectionImporter
		}
		// Remember the pick so later lookups (and exports in this process)
		// resolve the same entity.
		agent.SetTradeEntityOverride(company, entity, direction)
	}
	ctx, cancel := context.WithTimeout(r.Context(), agent.TradeProfileTimeout)
	defer cancel()
	var llm agent.LLMClient
	if s.engine != nil {
		llm = s.engine
	}
	background, summary := agent.BuildTradeProfile(ctx, client, llm, company)
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
