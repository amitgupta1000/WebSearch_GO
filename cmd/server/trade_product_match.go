package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
)

// handleTradeProductMatch offers the trade DB's own canonical product names
// for the activity the operator typed, ranked, so the operator — not a
// normalization heuristic — decides which standardized name the customs
// queries carry. The vocabulary is served by the DB from its startup mapping
// files (fast, even while its database is disconnected), and the ranking is
// the same one the run's own resolution uses, so what the operator sees here
// is what the run would pick.
func (s *Server) handleTradeProductMatch(w http.ResponseWriter, r *http.Request) {
	activity := strings.TrimSpace(r.FormValue("query"))
	if activity == "" {
		s.render(w, "trade_product_matches", tradeProductMatchView{
			Note: "Type the activity above first — the matcher ranks the trade DB's canonical names against it.",
		})
		return
	}
	client := s.engine.TradeDB()
	if client == nil {
		s.render(w, "trade_product_matches", tradeProductMatchView{
			Query: activity,
			Note:  "The trade DB is not configured on this deployment, so there is no vocabulary to match against; the run queries the typed name as-is.",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	options, err := client.FilterOptions(ctx)
	if err != nil || options == nil || len(options.CanonicalProductNames) == 0 {
		detail := "no answer"
		if err != nil {
			detail = err.Error()
		}
		s.render(w, "trade_product_matches", tradeProductMatchView{
			Query: activity,
			Note:  "The trade DB vocabulary is unavailable right now (" + detail + "); the run queries the typed name as-is.",
		})
		return
	}
	view := tradeProductMatchView{
		Query:     activity,
		Matches:   agent.TradeProductMatches(activity, options.CanonicalProductNames),
		Available: true,
	}
	if len(view.Matches) == 0 {
		view.Note = "No canonical trade DB name shares a token with this activity; the run queries the typed name as-is."
	}
	s.render(w, "trade_product_matches", view)
}

// tradeProductMatchView is one matcher answer: the ranked canonical names, a
// note explaining the degrade paths, and whether the capability is wired.
type tradeProductMatchView struct {
	Query     string
	Matches   []agent.TradeProductMatch
	Available bool
	Note      string
}
