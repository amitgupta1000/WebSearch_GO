package main

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
)

// trade_product_match.go serves the console's trade-product picker.
//
// The picker is suggestion-first, by design: the operator types the activity,
// sees the semantically closest canonical names and picks one -- nothing is
// auto-resolved behind their back. Two endpoints back it:
//
//   - POST /api/trade/product-match: activity in, ranked suggestions out.
//     String similarity narrows the 727-name vocabulary to 40 candidates; one
//     bounded model call ranks semantic closeness among them (see
//     agent.SemanticTradeProductMatches); a model outage degrades to the
//     string ranking, never to an empty picker.
//   - GET /api/trade/product-vocabulary: the full canonical list, rendered as
//     a datalist so the operator can always search and pick a name by hand
//     even when the suggestions miss.
//
// Both answers are cached briefly: the picker re-fires on checkbox toggles and
// query edits, and the vocabulary only changes when the DB re-standardizes.

const (
	tradeProductCacheTTL     = 10 * time.Minute
	tradeProductCacheMax     = 128
	tradeProductLookupBudget = 20 * time.Second
)

// tradeProductCacheEntry is one memoized picker answer.
type tradeProductCacheEntry struct {
	value   any
	expires time.Time
}

// tradeProductMemoStore is a small TTL memo shared by the matcher and the
// vocabulary endpoint (per server process, like the profile cache).
type tradeProductMemoStore struct {
	mu      sync.Mutex
	entries map[string]tradeProductCacheEntry
	order   []string
}

var tradeProductMemo = &tradeProductMemoStore{entries: make(map[string]tradeProductCacheEntry)}

func (m *tradeProductMemoStore) get(key string) (any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[key]
	if !ok || time.Now().After(entry.expires) {
		if ok {
			delete(m.entries, key)
		}
		return nil, false
	}
	return entry.value, true
}

func (m *tradeProductMemoStore) put(key string, value any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.entries[key]; !exists {
		m.order = append(m.order, key)
		if len(m.order) > tradeProductCacheMax {
			delete(m.entries, m.order[0])
			m.order = m.order[1:]
		}
	}
	m.entries[key] = tradeProductCacheEntry{value: value, expires: time.Now().Add(tradeProductCacheTTL)}
}

// canonicalTradeProducts returns the DB's canonical product vocabulary,
// memoized for the picker's lifetime.
func (s *Server) canonicalTradeProducts(ctx context.Context) ([]string, error) {
	if cached, ok := tradeProductMemo.get("vocabulary"); ok {
		if names, ok := cached.([]string); ok {
			return names, nil
		}
	}
	options, err := s.engine.TradeDB().FilterOptions(ctx)
	if err != nil {
		return nil, err
	}
	names := options.CanonicalProductNames
	tradeProductMemo.put("vocabulary", names)
	return names, nil
}

func (s *Server) handleTradeProductMatch(w http.ResponseWriter, r *http.Request) {
	activity := strings.TrimSpace(r.FormValue("query"))
	if activity == "" {
		s.render(w, "trade_product_matches", tradeProductMatchView{
			Note: "Type the activity above first - the picker ranks the trade DB's canonical names against it.",
		})
		return
	}
	if s.engine.TradeDB() == nil {
		s.render(w, "trade_product_matches", tradeProductMatchView{
			Query: activity,
			Note:  "The trade DB is not configured on this deployment, so there is no vocabulary to suggest from; the run queries the typed name as-is.",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), tradeProductLookupBudget)
	defer cancel()
	vocabulary, err := s.canonicalTradeProducts(ctx)
	if err != nil || len(vocabulary) == 0 {
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
	cacheKey := "match:" + strings.ToLower(activity)
	if cached, ok := tradeProductMemo.get(cacheKey); ok {
		if matches, ok := cached.([]agent.TradeProductMatch); ok {
			view := tradeProductMatchView{Query: activity, Matches: matches, Available: true}
			if len(matches) == 0 {
				view.Note = tradeProductNoMatchNote
			}
			s.render(w, "trade_product_matches", view)
			return
		}
	}
	var llm agent.LLMClient
	if s.engine != nil {
		llm = s.engine
	}
	matches := agent.SemanticTradeProductMatches(ctx, llm, activity, vocabulary)
	tradeProductMemo.put(cacheKey, matches)
	view := tradeProductMatchView{Query: activity, Matches: matches, Available: true}
	if len(matches) == 0 {
		view.Note = tradeProductNoMatchNote
	}
	s.render(w, "trade_product_matches", view)
}

// tradeProductNoMatchNote is shown when even the loose prefilter finds
// nothing: the dropdown remains the way through.
const tradeProductNoMatchNote = "No canonical name is close to this activity - search the full list in the trade-product field, or the run queries the typed name as-is."

// handleTradeProductVocabulary renders the full canonical vocabulary as a
// datalist, so the operator can always search and pick a name by hand.
func (s *Server) handleTradeProductVocabulary(w http.ResponseWriter, r *http.Request) {
	if s.engine.TradeDB() == nil {
		s.render(w, "trade_product_vocabulary", tradeProductVocabularyView{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), tradeProductLookupBudget)
	defer cancel()
	vocabulary, err := s.canonicalTradeProducts(ctx)
	if err != nil {
		vocabulary = nil
	}
	s.render(w, "trade_product_vocabulary", tradeProductVocabularyView{Names: vocabulary})
}

// tradeProductMatchView is one picker answer: the ranked suggestions, a note
// explaining the degrade paths, and whether the capability is wired.
type tradeProductMatchView struct {
	Query     string
	Matches   []agent.TradeProductMatch
	Available bool
	Note      string
}

// tradeProductVocabularyView carries the datalist entries.
type tradeProductVocabularyView struct {
	Names []string
}
