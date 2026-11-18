package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// trade_profile.go builds one company's aggregated Crystal customs profile
// from the entity endpoints, shared by three surfaces: the company dossier,
// the landscape runs' verified rows, and the on-demand "Fetch Trade Profile"
// button.
//
// Three endpoints compose:
//   - /names (typeahead) resolves the operator's surface name onto the DB's
//     exact spelling on both sides of the transaction;
//   - /entity/{importers|suppliers} returns the aggregate profile (rank,
//     shipments, product count, value) — scoped to the profiled top set
//     (top 500 importers / top 100 suppliers), which is why GlobalRank is
//     always labelled as a rank within that set;
//   - /entity/{...}/{name}/products returns the entity's own product mix and
//     answers for any name the data carries, profiled or not.
//
// Every miss is a value, not an error: an unknown name answers with an empty
// breakdown (HTTP 200), and the builder folds that into the background's Note.

const (
	// tradeProfileNamesLimit bounds the typeahead resolution per direction.
	tradeProfileNamesLimit = 5
	// tradeProfileProductsLimit bounds the product mix carried per side.
	tradeProfileProductsLimit = 5
	// TradeProfileTimeout bounds one profile build; the endpoints answer in
	// tens of milliseconds warm, but a fresh Cloud Run instance can cold
	// start a query, and a finishing run must never hang on that. Exported:
	// the on-demand button handler shares the same budget.
	TradeProfileTimeout = 20 * time.Second
	// tradeProfileCacheTTL: customs aggregates change only when the DB's
	// pipeline re-standardizes, so a short TTL keeps repeat runs and QC
	// iterations from re-fetching without hiding corrections for long.
	tradeProfileCacheTTL = time.Hour
	// tradeProfileCacheMax bounds the cache; entries are evicted FIFO past it.
	tradeProfileCacheMax = 256
)

// tradeProfileEntry is one cached profile with its log line.
type tradeProfileEntry struct {
	background *models.TradeBackground
	summary    string
	expires    time.Time
}

// tradeProfileCache memoises built profiles per normalized company name, the
// same pattern as the RocketReach profile cache: a dossier, its exports and a
// second click on the same name pay the round trip once.
type tradeProfileCache struct {
	mu      sync.Mutex
	entries map[string]tradeProfileEntry
	order   []string
	now     func() time.Time
}

var tradeProfileMemo = &tradeProfileCache{
	entries: make(map[string]tradeProfileEntry),
	order:   make([]string, 0, tradeProfileCacheMax),
	now:     time.Now,
}

func (c *tradeProfileCache) get(key string) (tradeProfileEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return tradeProfileEntry{}, false
	}
	if c.now().After(entry.expires) {
		delete(c.entries, key)
		return tradeProfileEntry{}, false
	}
	return entry, true
}

func (c *tradeProfileCache) put(key string, entry tradeProfileEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
		if len(c.order) > tradeProfileCacheMax {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
	}
	entry.expires = c.now().Add(tradeProfileCacheTTL)
	c.entries[key] = entry
}

// tradeLegalSuffixes are the trailing legal forms dropped when reducing a
// company name to its comparable core. The set spans the forms the customs
// data actually carries -- Indian and Anglophone (limited, pvt, pte, bhd,
// pty), continental European (gmbh, ag, se, sa, srl, spa, bv, nv, kgaa) and
// the common abbreviations -- because the supplier side of the DB is foreign
// exporters, where "GmbH" and "B.V." are exactly as common as "Ltd".
var tradeLegalSuffixes = map[string]bool{
	"limited": true, "ltd": true, "ltd.": true, "private": true, "pvt": true,
	"inc": true, "llc": true, "llp": true, "lp": true, "plc": true,
	"incorporated": true, "corp": true, "corporation": true, "co": true,
	"company": true, "pty": true, "pte": true, "bhd": true, "sdn": true,
	"gmbh": true, "ag": true, "se": true, "sa": true, "sas": true, "sarl": true,
	"srl": true, "spa": true, "bv": true, "nv": true, "kg": true, "kgaa": true,
	"kk": true, "oy": true, "ab": true, "as": true, "asa": true, "ltda": true,
	"kft": true, "sp": true, "zoo": true, "doo": true, "jsc": true,
	"pjsc": true, "ojsc": true, "sro": true,
}

// normalizeTradeEntityName folds the punctuation and symbol differences the
// customs spellings carry: case, "&" versus "and", dots, commas, dashes,
// slashes and brackets all become space, and runs of space collapse. The
// comparison tiers build on this form.
func normalizeTradeEntityName(value string) string {
	replacer := strings.NewReplacer(
		"&", " and ", ".", " ", ",", " ", "-", " ", "/", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ",
		"'", "", "\"", "", "#", " ",
	)
	fields := strings.Fields(replacer.Replace(strings.ToLower(strings.TrimSpace(value))))
	return strings.Join(fields, " ")
}

// tradeNameCore normalizes a name and drops its trailing legal-form tokens
// (parenthetical forms included: "... (PTY) LTD." normalizes to
// "... pty ltd" and both tokens go). Dotted abbreviations split by
// normalization ("N.V." becomes "n v", "A/S" becomes "a s") are dropped as a
// single-letter pair. At least one token always survives.
func tradeNameCore(value string) string {
	tokens := strings.Fields(normalizeTradeEntityName(value))
	for len(tokens) > 2 {
		last, previous := tokens[len(tokens)-1], tokens[len(tokens)-2]
		if len(last) == 1 && len(previous) == 1 {
			tokens = tokens[:len(tokens)-2]
			continue
		}
		if !tradeLegalSuffixes[last] && last != "and" {
			break
		}
		tokens = tokens[:len(tokens)-1]
	}
	for len(tokens) > 1 {
		last := tokens[len(tokens)-1]
		if !tradeLegalSuffixes[last] && last != "and" {
			break
		}
		tokens = tokens[:len(tokens)-1]
	}
	return strings.Join(tokens, " ")
}

// tradeNameMatches reports whether two company spellings name the same
// customs entity: equal after normalization, or equal after the legal forms
// are dropped.
func tradeNameMatches(a, b string) bool {
	if normalizeTradeEntityName(a) == normalizeTradeEntityName(b) {
		return true
	}
	coreA, coreB := tradeNameCore(a), tradeNameCore(b)
	return coreA != "" && coreA == coreB
}

// BuildTradeProfile resolves companyName onto the DB's entity vocabulary and
// aggregates what customs knows: presence on the matched side of the
// transaction, the profiled rank and shipment/value totals, and the entity's
// own product mix. The operator's saved pick wins; otherwise the shared
// resolver (trade_entity_resolve.go) auto-resolves only a confident match and
// returns ranked, semantically re-ranked suggestions on a miss. It never fails
// the caller — every miss or transport error becomes a Note, and a nil client
// or empty name returns nil. The summary is the operator-facing log line.
func BuildTradeProfile(ctx context.Context, client TradeDBSource, llm LLMClient, companyName string) (*models.TradeBackground, string) {
	name := strings.TrimSpace(companyName)
	if name == "" || client == nil {
		return nil, ""
	}
	key := strings.ToLower(name)
	if entry, ok := tradeProfileMemo.get(key); ok {
		return entry.background, entry.summary
	}

	// The operator's pick wins outright.
	if override, ok := lookupTradeEntityOverride(name); ok {
		background, summary := buildTradeProfileForEntity(ctx, client, name, override.name, override.direction)
		if background != nil {
			tradeProfileMemo.put(key, tradeProfileEntry{background: background, summary: summary})
		}
		return background, summary
	}

	match, candidates, seen := resolveTradeEntity(ctx, client, name)
	if match.name == "" {
		// No confident match: the ranked suggestions are the answer, with a
		// semantic re-rank when a model is available, for the operator to
		// pick from on the dossier card or the Fetch Trade Profile result.
		// When nothing scored, the raw typeahead hits still travel as context.
		ranked := semanticTradeEntityCandidates(ctx, llm, name, candidates)
		background := &models.TradeBackground{CheckedAt: time.Now(), Candidates: ranked}
		if len(ranked) > 0 {
			for _, candidate := range ranked {
				background.NameVariants = append(background.NameVariants, candidate.Name)
			}
		} else if len(seen) > tradeEntityCandidateLimit {
			background.NameVariants = append(background.NameVariants, seen[:tradeEntityCandidateLimit]...)
		} else {
			background.NameVariants = append(background.NameVariants, seen...)
		}
		summary := tradeProfileSummary(name, "", background)
		tradeProfileMemo.put(key, tradeProfileEntry{background: background, summary: summary})
		return background, summary
	}

	background, summary := buildTradeProfileForEntity(ctx, client, name, match.name, match.direction)
	if background != nil {
		tradeProfileMemo.put(key, tradeProfileEntry{background: background, summary: summary})
	}
	return background, summary
}

// buildTradeProfileForEntity aggregates the profile and the product mix for a
// KNOWN stored entity name and side — the auto-resolved match or the
// operator's pick. Presence is reported on that side; the other side is not
// consulted, because the resolver already searched both.
func buildTradeProfileForEntity(ctx context.Context, client TradeDBSource, requested, entityName, direction string) (*models.TradeBackground, string) {
	background := &models.TradeBackground{
		CheckedAt:   time.Now(),
		Present:     true,
		MatchedName: entityName,
		Directions:  []string{direction},
	}
	// The aggregate profile row (profiled top set only).
	if profiles, err := client.EntityProfiles(ctx, direction, entityName, true, tradeProfileNamesLimit); err == nil {
		for _, profile := range profiles {
			if tradeNameMatches(profile.Name, entityName) {
				background.GlobalRank = profile.GlobalRank
				background.TransactionCount = profile.TransactionCount
				background.ProductCount = profile.ProductCount
				background.TotalValueUSD = profile.TotalValueUSD
				background.TotalQuantityMT = profile.TotalQuantityMT
				break
			}
		}
	} else {
		background.Note = fmt.Sprintf("the aggregate profile lookup failed: %v", err)
	}
	// The product mix answers for any name the data carries.
	if breakdown, err := client.EntityProducts(ctx, direction, entityName, tradeProfileProductsLimit); err == nil && breakdown.TotalCount > 0 {
		background.TopProducts = breakdown.Products
		if background.TransactionCount == 0 {
			// Outside the profiled set: the breakdown's own totals stand in
			// for the aggregate profile, and the product count is the
			// breakdown's row count.
			for _, product := range breakdown.Products {
				background.TransactionCount += product.TransactionCount
				background.TotalValueUSD += product.TotalValueUSD
				background.TotalQuantityMT += product.TotalQuantityMT
			}
			background.ProductCount = breakdown.TotalCount
		}
	} else if err != nil && background.Note == "" {
		background.Note = fmt.Sprintf("the product breakdown lookup failed: %v", err)
	}
	summary := tradeProfileSummary(requested, direction, background)
	return background, summary
}

// tradeProfileSummary lines the profile up for the console log: the aggregates
// when they exist, the product mix when only the breakdown answered, and the
// closest names when nothing matched.
func tradeProfileSummary(name, primary string, background *models.TradeBackground) string {
	if !background.Present {
		message := fmt.Sprintf("Trade DB profile: no exact customs-entity match for %q", name)
		if len(background.NameVariants) > 0 {
			message += " (closest names: " + orNoneList(background.NameVariants) + ")"
		}
		return message
	}
	head := fmt.Sprintf("Trade DB profile for %q: %s side, matched as %q", name, primary, background.MatchedName)
	if background.GlobalRank > 0 {
		head += fmt.Sprintf(", rank #%d among the profiled top %s(s)", background.GlobalRank, primary)
	}
	if background.TransactionCount > 0 {
		head += fmt.Sprintf(", %d shipment(s) across %d product(s), USD %.0f (%.0f MT)",
			background.TransactionCount, background.ProductCount, background.TotalValueUSD, background.TotalQuantityMT)
	}
	if len(background.TopProducts) > 0 {
		top := make([]string, 0, 3)
		for _, product := range background.TopProducts {
			if len(top) == 3 {
				break
			}
			top = append(top, fmt.Sprintf("%s (%d)", product.Name, product.TransactionCount))
		}
		head += " — top products: " + strings.Join(top, ", ")
	}
	return head
}
