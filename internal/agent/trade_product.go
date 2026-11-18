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
	// Grade labels the suggestion for the picker: exact (the normalized name
	// is the canonical one), strong (a joined-word or substring match),
	// related (shared words) or loose (character similarity). A loose entry
	// exists for the operator to judge — it is never picked automatically.
	Grade string `json:"grade,omitempty"`
	// Rationale is the model's one-line reason for a semantic suggestion.
	Rationale string `json:"rationale,omitempty"`
}

const (
	// TradeProductGradeExact marks a normalized exact match.
	TradeProductGradeExact = "exact"
	// TradeProductGradeStrong marks a joined-word or substring match.
	TradeProductGradeStrong = "strong"
	// TradeProductGradeRelated marks a shared-word match.
	TradeProductGradeRelated = "related"
	// TradeProductGradeLoose marks a character-similarity suggestion.
	TradeProductGradeLoose = "loose"
	// TradeProductGradeSemantic marks a suggestion the model ranked as the
	// semantically closest candidate.
	TradeProductGradeSemantic = "semantic"

	// tradeProductLooseThreshold is the character-trigram similarity at which
	// a name is still worth offering as a loose suggestion. It exists so the
	// picker is never empty for a near spelling; the operator decides.
	tradeProductLooseThreshold = 0.30
)

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

// tradeProductSimilarity is the Jaccard overlap of character trigrams over
// two compact forms — a deterministic near-spelling signal that fills the
// picker with loose suggestions when no word-level signal exists. It never
// auto-picks; it only offers names for the operator to judge.
func tradeProductSimilarity(a, b string) float64 {
	if len(a) < 3 || len(b) < 3 {
		return 0
	}
	grams := func(value string) map[string]bool {
		set := make(map[string]bool, len(value))
		for i := 0; i+3 <= len(value); i++ {
			set[value[i:i+3]] = true
		}
		return set
	}
	left, right := grams(a), grams(b)
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	shared := 0
	for gram := range left {
		if right[gram] {
			shared++
		}
	}
	union := len(left) + len(right) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

// TradeProductMatches ranks the canonical vocabulary against the operator's
// activity. Deterministic: same inputs, same ranking — the console picker and
// the run's own resolution see the same list.
func TradeProductMatches(activity string, vocabulary []string) []TradeProductMatch {
	return rankTradeProductNames(activity, vocabulary, tradeProductMatchLimit)
}

// rankTradeProductNames is the shared deterministic string ranking. The
// picker's semantic prefilter (a wide limit) and the run's own resolution (the
// display limit) read the same order, so the suggestions the operator sees
// are seeded from the ranking the run would use.
func rankTradeProductNames(activity string, vocabulary []string, limit int) []TradeProductMatch {
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
			match.Score, match.Exact, match.Grade = 100, true, TradeProductGradeExact
		case strings.Contains(compactName, compactActivity) && len(compactActivity) >= 4:
			// A joined-word fragment of the canonical name ("nbutanol"
			// inside "nbutanolnba"): a real candidate the operator should
			// confirm.
			match.Score, match.Grade = 80, TradeProductGradeStrong
		case strings.Contains(nameNorm, activityNorm) && len(activityNorm) >= 3:
			// The activity is a prefix/substring of the canonical name
			// ("caustic" inside "CAUSTIC SODA (PEARL)"): a real candidate,
			// but the operator should confirm which variant they mean.
			match.Score, match.Grade = 80, TradeProductGradeStrong
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
				// No word signals at all: fall back to character-trigram
				// similarity, so the picker still offers near spellings
				// ("styren" -> STYRENE MONOMER, "polysteren" -> POLYSTYRENE)
				// as loose suggestions instead of an empty list. Loose
				// entries are never auto-picked.
				ratio := tradeProductSimilarity(compactActivity, compactName)
				if ratio < tradeProductLooseThreshold {
					continue
				}
				match.Score, match.Grade = 20+int(19*ratio), TradeProductGradeLoose
				break
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
			match.Grade = TradeProductGradeRelated
		}
		matches = append(matches, match)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Name < matches[j].Name
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
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
