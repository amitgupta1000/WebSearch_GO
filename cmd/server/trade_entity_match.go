package main

// trade_entity_match.go serves the console's pre-run customs-entity matcher for
// Company Research, the entity analogue of the trade-product picker.
//
// The resolver behind the dossier and the Fetch Trade Profile button already
// returns ranked candidates when no confident match exists, but a run used to
// discover that only after it finished: an imperfect company name silently
// produced no customs profile. This endpoint puts the same suggestions in front
// of the operator before the run. The chips fill trade_entity and
// trade_entity_direction, and the run then builds the profile from the pick.

import (
	"context"
	"net/http"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

func (s *Server) handleTradeEntityMatch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.FormValue("query"))
	if query == "" {
		query = strings.TrimSpace(r.FormValue("company_name"))
	}
	if query == "" {
		s.render(w, "trade_entity_matches", tradeEntityMatchView{
			Note: "Type the company name first - the matcher ranks the trade DB's stored entity spellings against it.",
		})
		return
	}
	client := s.engine.TradeDB()
	if client == nil {
		s.render(w, "trade_entity_matches", tradeEntityMatchView{
			Query: query,
			Note:  "The trade DB is not configured on this deployment, so there are no stored customs entities to match against.",
		})
		return
	}
	cacheKey := "entity-match:" + strings.ToLower(query)
	if cached, ok := tradeProductMemo.get(cacheKey); ok {
		if matches, ok := cached.([]models.TradeEntityCandidate); ok {
			s.renderTradeEntityMatches(w, query, matches)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), tradeProductLookupBudget)
	defer cancel()
	var llm agent.LLMClient
	if s.engine != nil {
		llm = s.engine
	}
	matches, _ := agent.TradeEntityMatches(ctx, client, llm, query)
	tradeProductMemo.put(cacheKey, matches)
	s.renderTradeEntityMatches(w, query, matches)
}

// renderTradeEntityMatches renders one matcher answer, with the honest empty
// case: nothing close is a statement, not a failure.
func (s *Server) renderTradeEntityMatches(w http.ResponseWriter, query string, matches []models.TradeEntityCandidate) {
	view := tradeEntityMatchView{Query: query, Matches: matches, Available: true}
	if len(matches) == 0 {
		view.Note = "No stored customs name is close to this company - leave the field blank and the run resolves it itself, or check the spelling."
	}
	s.render(w, "trade_entity_matches", view)
}

// tradeEntityMatchView is one matcher answer.
type tradeEntityMatchView struct {
	Query     string
	Matches   []models.TradeEntityCandidate
	Available bool
	Note      string
}
