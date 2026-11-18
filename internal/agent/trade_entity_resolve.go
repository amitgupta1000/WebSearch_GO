package agent

// trade_entity_resolve.go resolves a researched company name onto the Crystal
// DB's stored entity spelling -- the same shared resolver every surface uses
// (company dossier, both landscape models' verified rows, the Fetch Trade
// Profile button).
//
// The typeahead matches substrings of the stored names, so the resolver walks
// a query ladder (full name, suffix-stripped core, first two significant
// tokens), scores every candidate it sees, and distinguishes two outcomes:
// a CONFIDENT match (normalized-equal or core-equal) resolves automatically,
// and anything weaker becomes a ranked suggestion the operator picks -- the
// same suggestion-first contract as the trade-product picker. On a miss the
// deterministic candidates get one bounded semantic re-rank (trade-agent model
// call) so naming variants ("Sasol Chemicals" vs "SASOL CHEMICALS AFRICA (PTY)
// LTD") surface at the top, validated against the candidate list.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

const (
	// tradeEntityCandidateLimit caps the suggestions offered to the operator.
	tradeEntityCandidateLimit = 5
	// tradeEntityConfidentScore is the floor at which a match resolves
	// automatically (core-equal). Anything below is a suggestion.
	tradeEntityConfidentScore = 0.98
	// tradeEntityLooseThreshold is the character similarity below which a
	// candidate is not worth offering.
	tradeEntityLooseThreshold = 0.45
)

// tradeEntityMatch is the resolver's confident outcome.
type tradeEntityMatch struct {
	name      string
	direction string
}

// tradeEntityQueryLadder is the progressive typeahead query list for one name.
func tradeEntityQueryLadder(name string) []string {
	queries := make([]string, 0, 3)
	seen := map[string]bool{}
	add := func(query string) {
		query = strings.TrimSpace(query)
		if query == "" || seen[strings.ToLower(query)] {
			return
		}
		seen[strings.ToLower(query)] = true
		queries = append(queries, query)
	}
	add(name)
	core := tradeNameCore(name)
	if core != "" && !strings.EqualFold(core, normalizeTradeEntityName(name)) {
		add(core)
	}
	if tokens := strings.Fields(core); len(tokens) > 2 {
		add(strings.Join(tokens[:2], " "))
	}
	return queries
}

// scoreTradeEntityCandidate grades one stored entity name against the typed
// name: 1.0 normalized-equal, 0.98 core-equal (both confident), 0.85 token
// containment, then a character-similarity band down to the loose threshold.
func scoreTradeEntityCandidate(query, candidate string) (float64, string) {
	queryNorm, candidateNorm := normalizeTradeEntityName(query), normalizeTradeEntityName(candidate)
	if queryNorm != "" && queryNorm == candidateNorm {
		return 1.0, "exact"
	}
	queryCore, candidateCore := tradeNameCore(query), tradeNameCore(candidate)
	if queryCore != "" && queryCore == candidateCore {
		return tradeEntityConfidentScore, "core"
	}
	queryTokens := strings.Fields(queryCore)
	if len(queryTokens) > 0 {
		candidateTokens := strings.Fields(candidateCore)
		contained := 0
		for _, token := range queryTokens {
			for _, candidateToken := range candidateTokens {
				if token == candidateToken {
					contained++
					break
				}
			}
		}
		if contained == len(queryTokens) {
			return 0.85, "related"
		}
	}
	compact := func(value string) string { return strings.ReplaceAll(value, " ", "") }
	ratio := tradeProductSimilarity(compact(queryCore), compact(candidateCore))
	if ratio >= tradeEntityLooseThreshold {
		return 0.3 + 0.5*ratio, "loose"
	}
	return 0, ""
}

// resolveTradeEntity walks both sides of the transaction and returns either a
// confident match or the ranked suggestions, plus every distinct typeahead hit
// (context for the operator when nothing scored). Importer matches win ties
// (an Indian buyer is the side the operator usually means).
func resolveTradeEntity(ctx context.Context, client TradeDBSource, name string) (tradeEntityMatch, []models.TradeEntityCandidate, []string) {
	var match tradeEntityMatch
	byKey := map[string]models.TradeEntityCandidate{}
	seen := make([]string, 0, tradeProfileNamesLimit)
	seenSet := map[string]bool{}
	for _, direction := range []string{models.TradeDirectionImporter, models.TradeDirectionSupplier} {
		for _, query := range tradeEntityQueryLadder(name) {
			names, err := client.EntityNames(ctx, direction, query, tradeProfileNamesLimit)
			if err != nil {
				continue
			}
			for _, candidate := range names {
				candidate = strings.TrimSpace(candidate)
				if candidate == "" {
					continue
				}
				if !seenSet[strings.ToLower(candidate)] {
					seenSet[strings.ToLower(candidate)] = true
					seen = append(seen, candidate)
				}
				score, grade := scoreTradeEntityCandidate(name, candidate)
				if score == 0 {
					continue
				}
				key := strings.ToLower(candidate) + "|" + direction
				if existing, ok := byKey[key]; ok && existing.Score >= score {
					continue
				}
				byKey[key] = models.TradeEntityCandidate{Name: candidate, Direction: direction, Score: score, Grade: grade}
				if score >= tradeEntityConfidentScore && match.name == "" {
					match = tradeEntityMatch{name: candidate, direction: direction}
				}
			}
			if match.name != "" && match.direction == direction {
				break
			}
		}
	}
	if match.name != "" {
		return match, nil, seen
	}
	candidates := make([]models.TradeEntityCandidate, 0, len(byKey))
	for _, candidate := range byKey {
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].Name != candidates[j].Name {
			return candidates[i].Name < candidates[j].Name
		}
		return candidates[i].Direction < candidates[j].Direction
	})
	if len(candidates) > tradeEntityCandidateLimit {
		candidates = candidates[:tradeEntityCandidateLimit]
	}
	return tradeEntityMatch{}, candidates, seen
}

// semanticTradeEntityCandidates re-ranks the deterministic suggestions with
// one bounded model call when no confident match exists and a model is
// available. The model may only return names from the candidate list; when it
// fails or returns nothing valid, the deterministic ranking stands.
func semanticTradeEntityCandidates(ctx context.Context, llm LLMClient, query string, candidates []models.TradeEntityCandidate) []models.TradeEntityCandidate {
	if llm == nil || len(candidates) < 2 {
		return candidates
	}
	valid := make(map[string]bool, len(candidates))
	lines := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		valid[strings.ToLower(candidate.Name)] = true
		lines = append(lines, "- "+candidate.Name+" ("+candidate.Direction+")")
	}
	var target struct {
		Matches []struct {
			Name      string `json:"name"`
			Rationale string `json:"rationale,omitempty"`
		} `json:"matches"`
	}
	system := "You match a researched company onto a customs database's stored entity names. " +
		"Rank semantic closeness: which stored name is the same legal entity the researcher means, including " +
		"legal-form and suffix variants, word order, abbreviations and subsidiary naming. " +
		"Return at most 5 names, closest first, as JSON matching {matches: [{name, rationale}]}. " +
		"Use ONLY names from the candidate list; never invent a name. One short rationale per pick."
	prompt := fmt.Sprintf("Researched company: %q\nCandidate stored names:\n%s", strings.TrimSpace(query), strings.Join(lines, "\n"))
	if err := llm.GenerateStructured(ctx, system, prompt, &target); err != nil {
		return candidates
	}
	byName := make(map[string]models.TradeEntityCandidate, len(candidates))
	for _, candidate := range candidates {
		byName[strings.ToLower(candidate.Name)] = candidate
	}
	ranked := make([]models.TradeEntityCandidate, 0, tradeEntityCandidateLimit)
	seen := make(map[string]bool, tradeEntityCandidateLimit)
	for _, pick := range target.Matches {
		key := strings.ToLower(strings.TrimSpace(pick.Name))
		candidate, ok := byName[key]
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		candidate.Score = 0.9 - float64(len(ranked))*0.02
		candidate.Grade = "semantic"
		candidate.Rationale = strings.TrimSpace(pick.Rationale)
		ranked = append(ranked, candidate)
		if len(ranked) == tradeEntityCandidateLimit {
			break
		}
	}
	if len(ranked) == 0 {
		return candidates
	}
	return ranked
}

// tradeEntityOverride records an operator's explicit pick: the researched name
// resolves to this stored entity from here on (process lifetime), so the
// profile fetch, repeat lookups and the button all use it.
type tradeEntityOverride struct {
	name      string
	direction string
}

type tradeEntityOverrideStore struct {
	mu      sync.Mutex
	entries map[string]tradeEntityOverride
	order   []string
}

var tradeEntityOverrides = &tradeEntityOverrideStore{entries: make(map[string]tradeEntityOverride)}

// tradeEntityOverrideMax bounds the override store; entries are evicted FIFO.
const tradeEntityOverrideMax = 128

// SetTradeEntityOverride remembers (or clears, with an empty entity name) the
// operator's entity choice for one researched name.
func SetTradeEntityOverride(companyName, entityName, direction string) {
	key := strings.ToLower(strings.TrimSpace(companyName))
	if key == "" {
		return
	}
	tradeEntityOverrides.mu.Lock()
	defer tradeEntityOverrides.mu.Unlock()
	if strings.TrimSpace(entityName) == "" {
		delete(tradeEntityOverrides.entries, key)
		// The cached profile for this name described the old resolution.
		tradeProfileMemo.mu.Lock()
		delete(tradeProfileMemo.entries, key)
		tradeProfileMemo.mu.Unlock()
		return
	}
	if _, exists := tradeEntityOverrides.entries[key]; !exists {
		tradeEntityOverrides.order = append(tradeEntityOverrides.order, key)
		if len(tradeEntityOverrides.order) > tradeEntityOverrideMax {
			delete(tradeEntityOverrides.entries, tradeEntityOverrides.order[0])
			tradeEntityOverrides.order = tradeEntityOverrides.order[1:]
		}
	}
	tradeEntityOverrides.entries[key] = tradeEntityOverride{
		name:      strings.TrimSpace(entityName),
		direction: strings.TrimSpace(direction),
	}
	// A cached miss (or a stale auto-match) must not shadow the new pick.
	tradeProfileMemo.mu.Lock()
	delete(tradeProfileMemo.entries, key)
	tradeProfileMemo.mu.Unlock()
}

// lookupTradeEntityOverride returns the operator's pick for one name.
func lookupTradeEntityOverride(companyName string) (tradeEntityOverride, bool) {
	tradeEntityOverrides.mu.Lock()
	defer tradeEntityOverrides.mu.Unlock()
	entity, ok := tradeEntityOverrides.entries[strings.ToLower(strings.TrimSpace(companyName))]
	return entity, ok
}
