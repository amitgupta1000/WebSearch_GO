package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// trade_product.go resolves the operator's activity onto the trade DB's own
// standardized chemical vocabulary.
//
// The DB matches product names with a case-insensitive but punctuation-blind
// token match: "n butanol" returns the full ranking while "n-butanol" — the
// shape a person types, and the shape the ontology's label carries — matches
// nothing. Internal normalization fixes the punctuation, but it cannot know
// that an operator typing "caustic" means "CAUSTIC SODA (PEARL)" and not one
// of the vocabulary's other caustic entries, so the console also offers the
// ranked candidates and the operator's pick always wins.
//
// The vocabulary comes from the DB itself (/api/filters/options — the same
// canonical names the pipeline standardizes records to), so a match here is
// the exact string the ranking and record endpoints accept.

const (
	// TradeProductSourceOperator marks a name the operator picked explicitly.
	TradeProductSourceOperator = "operator"
	// TradeProductSourceVocabulary marks a name the run matched against the
	// DB's canonical vocabulary — exact after normalization.
	TradeProductSourceVocabulary = "vocabulary"
	// TradeProductSourceRaw marks the fallback: nothing matched, so the raw
	// activity travels to the DB as-is (the pre-existing behavior).
	TradeProductSourceRaw = "raw"

	// tradeProductMatchLimit caps the candidates offered to the operator.
	tradeProductMatchLimit = 5
	// tradeProductVocabularyTimeout bounds the one FilterOptions read the
	// resolution needs. The vocabulary is served from the API's startup
	// mapping files even while its database is disconnected, so a healthy
	// deployment answers fast; a slow one degrades to the raw name rather
	// than delaying the run.
	tradeProductVocabularyTimeout = 20 * time.Second
)

// TradeProductMatch is one candidate name from the trade DB's canonical
// vocabulary, ranked by how closely it matches the operator's activity.
type TradeProductMatch struct {
	// Name is the canonical vocabulary entry exactly as the DB serves it.
	Name string `json:"name"`
	// Score is the match strength, 0-100. 100 is an exact match after
	// normalizing hyphens, underscores and case.
	Score int `json:"score"`
	// Exact reports a normalized exact match — the run would pick this name
	// on its own.
	Exact bool `json:"exact,omitempty"`
}

// normalizeTradeProductName folds the punctuation differences that the trade
// DB's token match is blind to: hyphens and underscores become spaces, case
// flattens, and runs of spaces collapse. "n-Butanol" and "N BUTANOL" meet.
func normalizeTradeProductName(value string) string {
	replacer := strings.NewReplacer("-", " ", "_", " ", "(", " ", ")", " ")
	fields := strings.Fields(replacer.Replace(strings.ToLower(strings.TrimSpace(value))))
	return strings.Join(fields, " ")
}

// tradeProductTokens splits a normalized name into its significant tokens.
func tradeProductTokens(value string) []string {
	return strings.Fields(normalizeTradeProductName(value))
}

// TradeProductMatches ranks the canonical vocabulary against the operator's
// activity. Deterministic: same inputs, same ranking — the console picker and
// the run's own resolution see the same list.
func TradeProductMatches(activity string, vocabulary []string) []TradeProductMatch {
	activityNorm := normalizeTradeProductName(activity)
	if activityNorm == "" {
		return nil
	}
	activityTokens := tradeProductTokens(activity)
	matches := make([]TradeProductMatch, 0, len(vocabulary))
	for _, name := range vocabulary {
		name = strings.TrimSpace(name)
		nameNorm := normalizeTradeProductName(name)
		if nameNorm == "" {
			continue
		}
		match := TradeProductMatch{Name: name}
		// The compact form joins words back together: operators type
		// "styrenemonomer" and "nbutanol" as readily as they type spaces,
		// and the vocabulary stores "STYRENE MONOMER" / "N BUTANOL (NBA)".
		compactName := strings.ReplaceAll(nameNorm, " ", "")
		compactActivity := strings.ReplaceAll(activityNorm, " ", "")
		switch {
		case nameNorm == activityNorm, compactName == compactActivity:
			match.Score, match.Exact = 100, true
		case strings.Contains(compactName, compactActivity) && len(compactActivity) >= 4:
			// A joined-word fragment of the canonical name ("nbutanol"
			// inside "nbutanolnba"): a real candidate the operator should
			// confirm.
			match.Score = 80
		case strings.Contains(nameNorm, activityNorm) && len(activityNorm) >= 3:
			// The activity is a prefix/substring of the canonical name
			// ("caustic" inside "CAUSTIC SODA (PEARL)"): a real candidate,
			// but the operator should confirm which variant they mean.
			match.Score = 80
		default:
			nameTokens := tradeProductTokens(name)
			shared := 0
			for _, token := range activityTokens {
				for _, candidate := range nameTokens {
					if token == candidate {
						shared++
						break
					}
				}
			}
			if shared == 0 {
				continue
			}
			// Shared tokens over the smaller token count: "n butanol" shares
			// both tokens with "N BUTANOL (NBA)" (2/2) but only one with
			// "ISOBUTANOL" (the compound token does not split), so the
			// ranking prefers the full match without inventing chemistry.
			denominator := len(activityTokens)
			if len(nameTokens) < denominator {
				denominator = len(nameTokens)
			}
			match.Score = 60 * shared / denominator
			if match.Score > 59 {
				match.Score = 59
			}
		}
		matches = append(matches, match)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Name < matches[j].Name
	})
	if len(matches) > tradeProductMatchLimit {
		matches = matches[:tradeProductMatchLimit]
	}
	return matches
}

// ResolveTradeProduct decides which name the trade DB queries carry. The
// operator's pick wins outright; otherwise an exact normalized match is taken
// automatically; anything fuzzier stays with the raw activity and the ranked
// candidates travel to the log so the operator can pick next run.
func ResolveTradeProduct(activity, picked string, vocabulary []string) (product string, match *TradeProductMatch, candidates []TradeProductMatch, source string) {
	if trimmed := strings.TrimSpace(picked); trimmed != "" {
		return trimmed, nil, TradeProductMatches(activity, vocabulary), TradeProductSourceOperator
	}
	candidates = TradeProductMatches(activity, vocabulary)
	for index := range candidates {
		if candidates[index].Exact {
			return candidates[index].Name, &candidates[index], candidates, TradeProductSourceVocabulary
		}
	}
	return strings.TrimSpace(activity), nil, candidates, TradeProductSourceRaw
}

// resolveTradeDBProduct resolves the product name for one run's trade queries
// and emits every outcome, so the console log shows exactly which string the
// customs queries carry and, when nothing matched, which names to pick from.
func resolveTradeDBProduct(ctx context.Context, client TradeDBSource, activity, picked string, eventCh chan<- models.AgentEvent) string {
	requested := strings.TrimSpace(activity)
	if trimmed := strings.TrimSpace(picked); trimmed != "" {
		emitAgentEvent(eventCh, models.EventToolResult,
			fmt.Sprintf("Trade DB product: using operator-picked name %q (typed %q)", trimmed, requested),
			"trade-db", "product-match")
		return trimmed
	}

	vocabulary := []string(nil)
	if client != nil {
		vocabCtx, cancel := context.WithTimeout(ctx, tradeProductVocabularyTimeout)
		if options, err := client.FilterOptions(vocabCtx); err == nil && options != nil {
			vocabulary = options.CanonicalProductNames
		}
		cancel()
	}

	product, match, candidates, source := ResolveTradeProduct(requested, "", vocabulary)
	switch source {
	case TradeProductSourceVocabulary:
		emitAgentEvent(eventCh, models.EventToolResult,
			fmt.Sprintf("Trade DB product: %q matched the canonical vocabulary → querying %q", requested, match.Name),
			"trade-db", "product-match")
	case TradeProductSourceRaw:
		emitAgentEvent(eventCh, models.EventThinking,
			fmt.Sprintf("Trade DB product: no canonical name matches %q; the queries carry it as-is", requested),
			"trade-db", "product-match")
		if len(candidates) > 0 {
			names := make([]string, 0, len(candidates))
			for _, candidate := range candidates {
				names = append(names, candidate.Name)
			}
			emitAgentEvent(eventCh, models.EventThinking,
				"Closest trade DB names (pick one in the console's trade-product matcher for the next run): "+strings.Join(names, " · "),
				"trade-db", "product-match")
		}
	}
	return product
}
