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

// tradeLegalSuffixes are dropped when reducing a company name to its
// comparable core, so the dossier's confirmed spelling ("KLJ Resources
// Limited") and the customs spelling ("KLJ RESOURCES LTD") resolve to the
// same entity.
var tradeLegalSuffixes = map[string]bool{
	"limited": true, "ltd": true, "ltd.": true, "private": true, "pvt": true,
	"inc": true, "llc": true, "plc": true, "incorporated": true,
	"corp": true, "corporation": true, "co": true, "company": true,
}

// tradeNameCore lowercases a name and drops its trailing legal suffixes.
func tradeNameCore(value string) string {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	for len(tokens) > 1 && tradeLegalSuffixes[tokens[len(tokens)-1]] {
		tokens = tokens[:len(tokens)-1]
	}
	return strings.Join(tokens, " ")
}

// tradeNameMatches reports whether two company spellings name the same
// customs entity: case-insensitive equality, or equality after the legal
// suffixes are dropped.
func tradeNameMatches(a, b string) bool {
	if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) {
		return true
	}
	return tradeNameCore(a) == tradeNameCore(b) && tradeNameCore(a) != ""
}

// BuildTradeProfile resolves companyName onto the DB's entity vocabulary and
// aggregates what customs knows: presence on each side of the transaction,
// the profiled rank and shipment/value totals, and the entity's own product
// mix. It never fails the caller — every miss or transport error becomes a
// Note on the returned background, and a nil client or empty name returns nil.
// The summary is the operator-facing log line.
func BuildTradeProfile(ctx context.Context, client TradeDBSource, companyName string) (*models.TradeBackground, string) {
	name := strings.TrimSpace(companyName)
	if name == "" || client == nil {
		return nil, ""
	}
	key := strings.ToLower(name)
	if entry, ok := tradeProfileMemo.get(key); ok {
		return entry.background, entry.summary
	}

	background := &models.TradeBackground{CheckedAt: time.Now()}
	// Step 1 — resolve the DB spelling on both sides of the transaction.
	matched := map[string]string{}
	var variants []string
	seen := map[string]bool{}
	for _, direction := range []string{models.TradeDirectionImporter, models.TradeDirectionSupplier} {
		// The typeahead matches substrings of the stored name, so the
		// dossier's own legal suffix ("KLJ Resources Limited" vs the stored
		// "KLJ RESOURCES LTD") can zero the search; the suffix-stripped core
		// is the fallback query.
		queries := []string{name}
		if core := tradeNameCore(name); core != "" && !strings.EqualFold(core, strings.ToLower(name)) {
			queries = append(queries, core)
		}
		for _, search := range queries {
			names, err := client.EntityNames(ctx, direction, search, tradeProfileNamesLimit)
			if err != nil {
				background.Note = fmt.Sprintf("the trade DB vocabulary check failed: %v", err)
				continue
			}
			for _, candidate := range names {
				candidate = strings.TrimSpace(candidate)
				if candidate == "" || seen[strings.ToLower(candidate)] {
					continue
				}
				seen[strings.ToLower(candidate)] = true
				variants = append(variants, candidate)
				if tradeNameMatches(candidate, name) {
					matched[direction] = candidate
				}
			}
			if _, ok := matched[direction]; ok {
				break
			}
		}
	}
	background.NameVariants = variants
	background.Present = len(matched) > 0
	primary := ""
	for _, direction := range []string{models.TradeDirectionImporter, models.TradeDirectionSupplier} {
		if _, ok := matched[direction]; ok {
			background.Directions = append(background.Directions, direction)
			if primary == "" {
				primary = direction
			}
		}
	}
	if background.Present {
		background.MatchedName = matched[primary]
	}

	// Step 2 — the aggregate profile of the primary matched side.
	if primary != "" {
		if profiles, err := client.EntityProfiles(ctx, primary, matched[primary], true, tradeProfileNamesLimit); err == nil {
			for _, profile := range profiles {
				if tradeNameMatches(profile.Name, matched[primary]) {
					background.GlobalRank = profile.GlobalRank
					background.TransactionCount = profile.TransactionCount
					background.ProductCount = profile.ProductCount
					background.TotalValueUSD = profile.TotalValueUSD
					background.TotalQuantityMT = profile.TotalQuantityMT
					break
				}
			}
		} else if background.Note == "" {
			background.Note = fmt.Sprintf("the aggregate profile lookup failed: %v", err)
		}
		// Step 3 — the entity's own product mix; this answers for any name
		// the data carries, profiled or not.
		if breakdown, err := client.EntityProducts(ctx, primary, matched[primary], tradeProfileProductsLimit); err == nil && breakdown.TotalCount > 0 {
			background.TopProducts = breakdown.Products
			if background.TransactionCount == 0 {
				// Outside the profiled set: the breakdown's own totals stand
				// in for the aggregate profile, and the product count is the
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
	}

	summary := tradeProfileSummary(name, primary, background)
	tradeProfileMemo.put(key, tradeProfileEntry{background: background, summary: summary})
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
