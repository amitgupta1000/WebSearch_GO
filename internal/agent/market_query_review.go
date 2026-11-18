package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/ontology"
)

// market_query_review.go is the Seller Landscape's adversarial query review.
//
// Every planned search query — discovery lanes, category lanes and the
// per-company verification purposes — is judged for aptness before it spends
// a search. The review is layered on purpose:
//
//  1. Mechanical guards run first and always: a query that repeats its own
//     words, duplicates another query's vocabulary without adding a hard
//     constraint (a site: operator), leans on a site: domain the operator
//     never promoted, or exceeds the word cap cannot be made good by
//     judgement, so it is dropped outright.
//  2. An LLM review then judges what remains for aptness and economy — does
//     the phrasing match pages a search engine actually indexes, does trade
//     vocabulary ride on a tradable product noun, is one query worth one
//     search. It may keep, rewrite (within hard constraints) or drop.
//
// The review fails open: when the model is unavailable the original queries
// run, because a noisy lane is recoverable downstream and a missing lane is
// not. Every rewritten or dropped entry is recorded on the report as a
// MarketQueryReview.

const (
	// marketQueryReviewMaxWords bounds any reviewed query, kept or rewritten.
	marketQueryReviewMaxWords = 16
	// marketQueryReviewMaxPurposeWords bounds a verification purpose phrase;
	// it is appended after two quoted terms for every candidate company.
	marketQueryReviewMaxPurposeWords = 8
	// marketQueryReviewTimeout bounds the single review LLM call.
	marketQueryReviewTimeout = 45 * time.Second
	// marketQueryReviewMaxEvents caps the per-change event lines.
	marketQueryReviewMaxEvents = 12
)

// marketQueryFamilyAnchor resolves the product family the activity itself names,
// independently of the model's category inference. It is the reviewer's
// independent anchor: the inferred category is a hypothesis, so a category lane
// that names a different chemistry must be challenged rather than trusted.
//
// The graph answers first — including the qualifier that picks a family out of an
// ambiguous hub — and the legacy qualifier table answers only for oil names the
// graph has no node for yet. A nil result means neither can prove a family, so the
// reviewer falls back to its own judgement.
func marketQueryFamilyAnchor(activity string) []string {
	if document, err := ontology.Shared(); err == nil {
		anchor := rankHypernymVariants(activityResolution{
			Activity: strings.TrimSpace(activity),
			Graph:    document.Resolve(activity, ontology.DefaultOptions),
		})
		if len(anchor.Graph.Families) > 0 {
			labels := anchor.familyLabels()
			// A qualifier that picks a family out of an ambiguous hub is the
			// authoritative answer; the other variants are context for the model.
			if anchor.ambiguousHub() {
				if _, decided := oilQualifierFamily(activity); decided != "" {
					labels = labels[:1]
				}
			}
			return labels
		}
	}
	return demandOilFamilies(activity)
}

// marketQueryReviewSystem is the reviewer's standing instruction: what makes a
// query worth its search, and the hard constraints a rewrite may not cross.
const marketQueryReviewSystem = `You are the adversarial query reviewer for an industrial seller-landscape research pipeline. The queries below were composed mechanically and will be issued verbatim to a web search engine; a query is only worth its search if the engine can plausibly return pages that name operating companies (plants, facilities, blenders, manufacturers, exporters) for the activity in the requested geography.

Judge every query on:
1. Natural phrasing - real indexed industry phrasing ("lubricant blending plant", "blending and storage terminal") beats role-word stacks ("company operator manufacturer") that no page contains.
2. Aptness - the vocabulary must match what the target pages say. Trade and customs vocabulary (exporter, shipment, customs, bill of lading) only pays off on a tradable product noun; a process or service activity alone is not a commodity, so re-anchor such lanes on the product category when one is given.
3. Economy - no duplicated words, no redundant geography, one geography per query, and no query that only restates another query's words without adding a hard constraint such as a site: operator.
4. Semantic correctness (product family) - a query must stay inside the product family the activity belongs to. The inferred product category is a hypothesis supplied by an earlier step, not ground truth: if a category lane names a different chemistry or industry than the activity's family, that lane is wrong even though its phrasing is good, and it must be dropped or rewritten to the activity's real family. A family that merely shares a word with the activity is not the same family. When a deterministic family is stated below, it is authoritative over the inferred category.

Verdicts: "keep", "rewrite" (provide revised_query), or "drop" (with a short reason). Rewrite rules: preserve the lane's intent; you may re-anchor on the product category when it belongs to the activity's family, and you may replace a cross-family product term with the activity's family; use at most one geography and only inside the requested scope; a site: operator only from the allowlist, at most one, and then omit geography; at most 16 words per query; never repeat a content word; plain keyword queries, at most one quoted phrase for the exact activity or product term; never invent company names, places or domains outside the allowlist. When in doubt between a rewrite and a drop, prefer a drop: a noisy lane wastes a search and pollutes the candidate pool.`

// marketQueryVerdict is one reviewer verdict as returned by the model.
type marketQueryVerdict struct {
	Query        string `json:"query"`
	Verdict      string `json:"verdict"`
	RevisedQuery string `json:"revised_query,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// marketQueryReviewTarget binds the single review call: one verdict per query
// and, when purposes were submitted, one verdict per verification purpose.
type marketQueryReviewTarget struct {
	Queries  []marketQueryVerdict `json:"queries"`
	Purposes []marketQueryVerdict `json:"verification_purposes,omitempty"`
}

// marketQueryWords tokenizes a query: lowercase whitespace fields with quotes
// stripped. A site: prefix stays one token so its domain can be validated.
func marketQueryWords(query string) []string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	words := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, `"`)
		if field != "" {
			words = append(words, field)
		}
	}
	return words
}

// marketQuerySiteDomain returns the host of a site: token, or "".
func marketQuerySiteDomain(word string) string {
	if strings.HasPrefix(word, "site:") {
		return strings.TrimPrefix(word, "site:")
	}
	return ""
}

// marketQueryConnectorWord reports whether a word is engine glue: it may
// repeat inside a compound phrase ("import data OR importer of OR customs
// shipment") without making the query degenerate.
func marketQueryConnectorWord(word string) bool {
	return word == "or" || marketConnectorWords[word]
}

// marketQuerySiteClass is the hard-constraint class a query belongs to: the
// site: host it is scoped to, or "" for an open web query. Redundancy is only
// judged within a class — a site-scoped query is never redundant with an open
// one, because the operator restricts a different retrieval space.
func marketQuerySiteClass(query string) string {
	for _, word := range marketQueryWords(query) {
		if domain := marketQuerySiteDomain(word); domain != "" {
			return domain
		}
	}
	return ""
}

// marketQueryTokenKey is the order-insensitive identity of a query's words.
func marketQueryTokenKey(words []string) string {
	sorted := append([]string(nil), words...)
	sort.Strings(sorted)
	return strings.Join(sorted, " ")
}

// marketQueryReviewSiteAllowlist lists the site: domains a reviewed query may
// carry: the public trade-data platforms and the operator-promoted market
// intelligence directories. A rewrite may restrict a trade-shaped lane to one
// of these hosts; it may never invent a new one.
func marketQueryReviewSiteAllowlist() []string {
	sites := make([]string, 0, len(tradeDataSourceMarkers)+len(sourceMarketIntelMarkers))
	seen := make(map[string]bool)
	for _, marker := range append(append([]string(nil), tradeDataSourceMarkers...), sourceMarketIntelMarkers...) {
		site := strings.TrimSuffix(marker, ".")
		if site != "" && !seen[site] {
			seen[site] = true
			sites = append(sites, site)
		}
	}
	sort.Strings(sites)
	return sites
}

// marketQueryReviewSiteAllowed reports whether a site: host may appear in a
// reviewed query.
func marketQueryReviewSiteAllowed(domain string) bool {
	if domain == "" {
		return false
	}
	for _, marker := range tradeDataSourceMarkers {
		if strings.Contains(domain, marker) {
			return true
		}
	}
	return isMarketIntelDomain(domain)
}

// marketKnownGeographyTokens is every region and country token the geography
// vocabulary knows. A reviewed rewrite may only narrow geography inside the
// requested scope, so a token that is known geography but not in scope is a
// scope violation.
func marketKnownGeographyTokens() map[string]bool {
	tokens := make(map[string]bool)
	for region, members := range marketRegionTerms {
		tokens[region] = true
		for _, member := range members {
			tokens[member] = true
			for _, term := range marketCountryTerms(member) {
				tokens[term] = true
			}
		}
	}
	for country := range marketCountryAliases {
		tokens[country] = true
		for _, term := range marketCountryTerms(country) {
			tokens[term] = true
		}
	}
	return tokens
}

// marketQueryScopeTokens is the geography vocabulary a rewrite may use: the
// requested countries and regions plus the members those regions expand to,
// so a Middle East run may narrow to "UAE" but never widen to "India".
func marketQueryScopeTokens(query models.MarketLandscapeQuery) map[string]bool {
	scope := make(map[string]bool)
	add := func(value string) {
		for _, word := range marketQueryWords(value) {
			scope[word] = true
		}
	}
	for _, country := range query.Countries {
		add(country)
		for _, term := range marketCountryTerms(country) {
			add(term)
		}
	}
	for _, region := range marketTargetRegions(query) {
		add(region)
		for _, term := range marketRegionTermsFor(region) {
			add(term)
		}
	}
	return scope
}

// marketQueryMechanical carries the deterministic defects found in one query.
type marketQueryMechanical struct {
	dropReason string // non-empty means the query cannot run
	echo       bool   // repeats one of its own content words
	notes      string // human-readable defect notes for the reviewer prompt
}

// reviewMarketQueryMechanics inspects every query for the defects judgement
// cannot fix — self-repetition, an unknown or doubled site: host, an oversized
// query — and every pairwise pair for vocabulary duplicated without a hard
// constraint. Redundancy is judged within a site class: a later query whose
// words are a subset of an earlier same-class query's words buys no retrieval
// the earlier query does not already deliver.
func reviewMarketQueryMechanics(specs []marketSearchSpec) map[string]marketQueryMechanical {
	result := make(map[string]marketQueryMechanical, len(specs))
	sets := make([]map[string]bool, len(specs))
	for i, spec := range specs {
		set := make(map[string]bool)
		for _, word := range marketQueryWords(spec.Query) {
			if marketQuerySiteDomain(word) == "" {
				set[word] = true
			}
		}
		sets[i] = set
		mechanical := marketQueryMechanical{}
		if len(marketQueryWords(spec.Query)) > marketQueryReviewMaxWords {
			mechanical.dropReason = fmt.Sprintf("query exceeds %d words", marketQueryReviewMaxWords)
		}
		counts := make(map[string]int)
		siteDomains := 0
		for _, word := range marketQueryWords(spec.Query) {
			if domain := marketQuerySiteDomain(word); domain != "" {
				siteDomains++
				if !marketQueryReviewSiteAllowed(domain) {
					mechanical.dropReason = fmt.Sprintf("site:%s is outside the operator allowlist", domain)
				}
				continue
			}
			counts[word]++
		}
		if siteDomains > 1 {
			mechanical.dropReason = "more than one site: operator"
		}
		for word, count := range counts {
			if count > 1 && !marketQueryConnectorWord(word) {
				mechanical.echo = true
				mechanical.notes = appendMarketNote(mechanical.notes, fmt.Sprintf("repeats %q", word))
			}
		}
		result[spec.Query] = mechanical
	}
	for j := 1; j < len(specs); j++ {
		if result[specs[j].Query].dropReason != "" || len(sets[j]) == 0 {
			continue
		}
		for i := 0; i < j; i++ {
			if marketQuerySiteClass(specs[i].Query) != marketQuerySiteClass(specs[j].Query) {
				continue
			}
			subset := true
			for word := range sets[j] {
				if !sets[i][word] {
					subset = false
					break
				}
			}
			if subset {
				mechanical := result[specs[j].Query]
				reason := fmt.Sprintf("redundant with query %d (%s)", i+1, specs[i].Query)
				mechanical.dropReason = reason
				mechanical.notes = appendMarketNote(mechanical.notes, reason)
				result[specs[j].Query] = mechanical
				break
			}
		}
	}
	return result
}

// marketSanitizeRevisedQuery enforces the hard rewrite constraints: bounded
// length, at most one allowlisted site: operator, no repeated content words,
// no geography outside the requested scope, and enough shared vocabulary with
// the original that the rewrite stays the same lane. It returns the cleaned
// query, or an empty string with the reason the rewrite was refused.
func marketSanitizeRevisedQuery(revised, original string, scope, knownGeography, acceptedKeys map[string]bool, maxWords int) (string, string) {
	revised = strings.Join(strings.Fields(revised), " ")
	if revised == "" {
		return "", "the rewrite is empty"
	}
	if strings.EqualFold(revised, original) {
		return "", "the rewrite equals the original"
	}
	words := marketQueryWords(revised)
	if len(words) == 0 || len(words) > maxWords {
		return "", fmt.Sprintf("the rewrite must be 1-%d words", maxWords)
	}
	sites := 0
	counts := make(map[string]int)
	for _, word := range words {
		if domain := marketQuerySiteDomain(word); domain != "" {
			sites++
			if !marketQueryReviewSiteAllowed(domain) {
				return "", fmt.Sprintf("site:%s is outside the operator allowlist", domain)
			}
			continue
		}
		counts[word]++
	}
	if sites > 1 {
		return "", "at most one site: operator is allowed"
	}
	for word, count := range counts {
		if count > 1 && !marketQueryConnectorWord(word) {
			return "", fmt.Sprintf("the rewrite repeats %q", word)
		}
		if knownGeography[word] && !scope[word] {
			return "", fmt.Sprintf("the rewrite names %q outside the requested geography", word)
		}
	}
	originalWords := make(map[string]bool)
	for _, word := range marketQueryWords(original) {
		if marketQuerySiteDomain(word) == "" {
			originalWords[word] = true
		}
	}
	shared := false
	for _, word := range words {
		if marketQuerySiteDomain(word) == "" && originalWords[word] {
			shared = true
			break
		}
	}
	if !shared {
		return "", "the rewrite shares no vocabulary with the original lane"
	}
	if acceptedKeys[marketQueryTokenKey(words)] {
		return "", "the rewrite duplicates another surviving query"
	}
	return revised, ""
}

// marketSanitizeRevisedPurpose enforces the constraints on a rewritten
// verification purpose: a short plain phrase with no site: operator and no
// geography — a purpose runs for every candidate company, so a geography
// inside it would silently scope the whole verification stage.
func marketSanitizeRevisedPurpose(revised, original string, knownGeography map[string]bool) (string, string) {
	revised = strings.Join(strings.Fields(revised), " ")
	if revised == "" {
		return "", "the rewrite is empty"
	}
	if strings.EqualFold(revised, original) {
		return "", "the rewrite equals the original"
	}
	words := marketQueryWords(revised)
	if len(words) == 0 || len(words) > marketQueryReviewMaxPurposeWords {
		return "", fmt.Sprintf("the rewrite must be 1-%d words", marketQueryReviewMaxPurposeWords)
	}
	counts := make(map[string]int)
	for _, word := range words {
		if marketQuerySiteDomain(word) != "" {
			return "", "a verification purpose cannot carry a site: operator"
		}
		if knownGeography[word] {
			return "", "a verification purpose cannot name geography; it runs for every candidate company"
		}
		counts[word]++
	}
	for word, count := range counts {
		if count > 1 && !marketQueryConnectorWord(word) {
			return "", fmt.Sprintf("the rewrite repeats %q", word)
		}
	}
	return revised, ""
}

// reviewMarketQueryBatch reviews the seller landscape's planned queries; the
// buyer landscape reviews through the engine-neutral reviewQueryBatch below.
func (a *MarketLandscapeAgent) reviewMarketQueryBatch(ctx context.Context, phase string, specs []marketSearchSpec, purposes []string, category string, query models.MarketLandscapeQuery, eventCh chan<- models.AgentEvent) ([]marketSearchSpec, []string, []models.MarketQueryReview, string) {
	return reviewQueryBatch(ctx, a.engine, phase, specs, purposes, category, query, eventCh)
}

// reviewQueryBatch runs the adversarial query review over one batch of planned
// queries and, optionally, the per-company verification purposes. It returns
// the surviving queries (rewrites applied), the surviving purposes, the audit
// records for every rewritten or dropped entry, and a warning when the review
// itself failed — fail-open, with the originals returned. Both landscape
// agents share it: the review reads only the activity and the requested
// geography off the query.
func reviewQueryBatch(ctx context.Context, engine *Engine, phase string, specs []marketSearchSpec, purposes []string, category string, query models.MarketLandscapeQuery, eventCh chan<- models.AgentEvent) ([]marketSearchSpec, []string, []models.MarketQueryReview, string) {
	if len(specs) == 0 && len(purposes) == 0 {
		return specs, purposes, nil, ""
	}
	mechanics := reviewMarketQueryMechanics(specs)
	scope := marketQueryScopeTokens(query)
	knownGeography := marketKnownGeographyTokens()

	reviewCtx, cancel := context.WithTimeout(ctx, marketQueryReviewTimeout)
	defer cancel()
	var target marketQueryReviewTarget
	prompt := marketQueryReviewPrompt(specs, purposes, mechanics, category, query)
	if err := engine.GenerateStructured(reviewCtx, marketQueryReviewSystem, prompt, &target); err != nil {
		warning := fmt.Sprintf("Query review unavailable (%v); the original queries run unchanged.", err)
		emitAgentEvent(eventCh, models.EventThinking, warning, "market_query_review", "")
		return specs, purposes, nil, warning
	}

	llmByQuery := make(map[string]marketQueryVerdict)
	for _, verdict := range target.Queries {
		key := strings.ToLower(strings.TrimSpace(verdict.Query))
		if key != "" {
			llmByQuery[key] = verdict
		}
	}
	surviving := make([]marketSearchSpec, 0, len(specs))
	var reviews []models.MarketQueryReview
	acceptedKeys := make(map[string]bool)
	kept, rewritten, dropped := 0, 0, 0
	for _, spec := range specs {
		mechanic := mechanics[spec.Query]
		verdict, hasVerdict := llmByQuery[strings.ToLower(strings.TrimSpace(spec.Query))]

		drop := false
		rewrittenQuery := ""
		verdictLabel := "kept"
		reason := ""
		switch {
		case mechanic.dropReason != "":
			drop, verdictLabel, reason = true, "dropped", mechanic.dropReason
		case mechanic.echo:
			revised, refuse := "", ""
			if hasVerdict && strings.EqualFold(verdict.Verdict, "rewrite") {
				revised, refuse = marketSanitizeRevisedQuery(verdict.RevisedQuery, spec.Query, scope, knownGeography, acceptedKeys, marketQueryReviewMaxWords)
			}
			if revised != "" {
				rewrittenQuery, verdictLabel = revised, "rewritten"
				reason = appendMarketNote("repeats its own words", verdict.Reason)
			} else {
				drop, verdictLabel, reason = true, "dropped", "repeats its own words"
				if refuse != "" {
					reason = appendMarketNote(reason, "rewrite refused: "+refuse)
				}
			}
		case hasVerdict && strings.EqualFold(verdict.Verdict, "drop"):
			drop, verdictLabel, reason = true, "dropped", marketReviewFirstNonEmpty(verdict.Reason, "the reviewer judged the query not worth a search")
		case hasVerdict && strings.EqualFold(verdict.Verdict, "rewrite"):
			revised, refuse := marketSanitizeRevisedQuery(verdict.RevisedQuery, spec.Query, scope, knownGeography, acceptedKeys, marketQueryReviewMaxWords)
			if revised != "" {
				rewrittenQuery, verdictLabel = revised, "rewritten"
				reason = marketReviewFirstNonEmpty(verdict.Reason, "rewritten for aptness")
			} else {
				verdictLabel, reason = "kept", appendMarketNote("rewrite refused: "+refuse, verdict.Reason)
			}
		default:
			// Clean and unchallenged: it runs, and only SearchQueries records it.
			acceptedKeys[marketQueryTokenKey(marketQueryWords(spec.Query))] = true
			kept++
			surviving = append(surviving, spec)
			continue
		}
		reviews = append(reviews, models.MarketQueryReview{
			Phase: phase, Original: spec.Query, Verdict: verdictLabel,
			RevisedQuery: rewrittenQuery, Reason: reason,
		})
		if drop {
			dropped++
			continue
		}
		if rewrittenQuery != "" {
			spec.Query = rewrittenQuery
			rewritten++
		} else {
			kept++
		}
		acceptedKeys[marketQueryTokenKey(marketQueryWords(spec.Query))] = true
		surviving = append(surviving, spec)
	}

	survivingPurposes := make([]string, 0, len(purposes))
	if len(purposes) > 0 {
		llmByPurpose := make(map[string]marketQueryVerdict)
		for _, verdict := range target.Purposes {
			key := strings.ToLower(strings.TrimSpace(verdict.Query))
			if key != "" {
				llmByPurpose[key] = verdict
			}
		}
		for _, purpose := range purposes {
			verdict, hasVerdict := llmByPurpose[strings.ToLower(strings.TrimSpace(purpose))]
			drop := false
			rewrittenValue := ""
			verdictLabel := "kept"
			reason := ""
			switch {
			case hasVerdict && strings.EqualFold(verdict.Verdict, "drop"):
				drop, verdictLabel, reason = true, "dropped", marketReviewFirstNonEmpty(verdict.Reason, "the reviewer retired the purpose")
			case hasVerdict && strings.EqualFold(verdict.Verdict, "rewrite"):
				revised, refuse := marketSanitizeRevisedPurpose(verdict.RevisedQuery, purpose, knownGeography)
				if revised != "" {
					rewrittenValue, verdictLabel, reason = revised, "rewritten", marketReviewFirstNonEmpty(verdict.Reason, "rewritten for aptness")
				} else {
					verdictLabel, reason = "kept", appendMarketNote("rewrite refused: "+refuse, verdict.Reason)
				}
			default:
				survivingPurposes = append(survivingPurposes, purpose)
				kept++
				continue
			}
			reviews = append(reviews, models.MarketQueryReview{
				Phase: "verification", Original: purpose, Verdict: verdictLabel,
				RevisedQuery: rewrittenValue, Reason: reason,
			})
			if drop {
				dropped++
				continue
			}
			if rewrittenValue != "" {
				purpose = rewrittenValue
				rewritten++
			} else {
				kept++
			}
			survivingPurposes = append(survivingPurposes, purpose)
		}
		if len(survivingPurposes) == 0 {
			// Verification never runs without a purpose: a reviewer that
			// retired every one falls back to the submitted defaults.
			survivingPurposes = append(survivingPurposes, purposes...)
		}
	}

	if len(reviews) > 0 {
		emitAgentEvent(eventCh, models.EventThinking,
			fmt.Sprintf("Query review (%s): %d kept, %d rewritten, %d dropped", phase, kept, rewritten, dropped),
			"market_query_review", "")
		shown := 0
		for _, review := range reviews {
			if shown >= marketQueryReviewMaxEvents {
				break
			}
			if review.Verdict == "kept" {
				continue
			}
			shown++
			detail := review.RevisedQuery
			if detail == "" {
				detail = review.Reason
			}
			emitAgentEvent(eventCh, models.EventToolResult,
				fmt.Sprintf("Query review: %s %q (%s)", review.Verdict, review.Original, detail),
				"market_query_review", "")
		}
	}
	return surviving, survivingPurposes, reviews, ""
}

// marketQueryReviewPrompt renders the batch under review: the run's anchor
// vocabulary, every query with its mechanical defect notes, and the
// verification purposes.
func marketQueryReviewPrompt(specs []marketSearchSpec, purposes []string, mechanics map[string]marketQueryMechanical, category string, query models.MarketLandscapeQuery) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review the planned searches for one Seller Landscape run.\n")
	fmt.Fprintf(&b, "Activity: %q\n", query.Activity)
	if strings.TrimSpace(category) != "" {
		fmt.Fprintf(&b, "Inferred product category (hypothesis, challenge it): %q (trade and customs lanes may be re-anchored on it, since they need a tradable product noun)\n", strings.TrimSpace(category))
	} else {
		b.WriteString("Inferred product category (hypothesis, challenge it): none\n")
	}
	if families := marketQueryFamilyAnchor(query.Activity); len(families) > 0 {
		fmt.Fprintf(&b, "Activity product family (deterministic): %s — a category lane naming a different family is semantically wrong and must be dropped or rewritten to this family.\n", strings.Join(families, ", "))
	}
	fmt.Fprintf(&b, "Requested geography: %s\n", marketLandscapeGeography(query))
	fmt.Fprintf(&b, "Allowlisted site: domains: %s\n\n", strings.Join(marketQueryReviewSiteAllowlist(), ", "))
	b.WriteString("Queries:\n")
	for i, spec := range specs {
		notes := mechanics[spec.Query].notes
		if notes == "" {
			notes = "clean"
		}
		fmt.Fprintf(&b, "%d. [%s] %s (mechanical notes: %s)\n", i+1, spec.Lane, spec.Query, notes)
	}
	if len(purposes) > 0 {
		b.WriteString("\nVerification purposes (each phrase is appended after the quoted company name and the quoted activity for every candidate company; judge the phrase itself):\n")
		for i, purpose := range purposes {
			fmt.Fprintf(&b, "%d. %s\n", i+1, purpose)
		}
	}
	b.WriteString("\nReturn JSON {\"queries\":[{\"query\":\"<the original query text>\",\"verdict\":\"keep|rewrite|drop\",\"revised_query\":\"<only for rewrite>\",\"reason\":\"<short reason>\"}]")
	if len(purposes) > 0 {
		b.WriteString(",\"verification_purposes\":[{\"query\":\"<the original purpose>\",\"verdict\":\"keep|rewrite|drop\",\"revised_query\":\"<only for rewrite>\",\"reason\":\"<short reason>\"}]")
	}
	b.WriteString(". Verdict every query.\n")
	return b.String()
}

func appendMarketNote(notes, note string) string {
	if strings.TrimSpace(note) == "" {
		return strings.TrimSpace(notes)
	}
	if strings.TrimSpace(notes) == "" {
		return note
	}
	return notes + "; " + note
}

func marketReviewFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
