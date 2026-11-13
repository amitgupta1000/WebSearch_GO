package agent

import (
	"strings"
)

// market_names.go owns the producer/seller landscape's name hygiene.
//
// A search-result title is written for a search engine, not for a roster. It
// carries the site name, a tagline, navigation labels, and — on the pages that
// rank best for activity+geography lanes — the phrasing of a market report or a
// news article. Treating that title as the company name is how publishers,
// report titles and headlines leak into the roster.
//
// The pipeline therefore treats a title as a *source* for a name:
//
//  1. marketCanonicalName reduces a page title to the best company-name segment
//     (domain affinity wins, taglines and navigation labels lose).
//  2. marketDocumentTitle and marketHeadlineTitle decide whether a string names
//     a document or an article rather than a company. Such strings stay in the
//     run as discovery evidence and never become roster names.
//  3. marketNameKey folds legal-suffix and punctuation variants so one company
//     is one row.

// marketNameSeparators split a title into competing segments. Only a spaced
// hyphen splits: "Oil-Plus" is one word, "Acme - Lubricants" is two segments.
var marketNameSeparators = []string{"|", "—", "–", " - ", "•", "·", "::", "»", "«"}

// marketTitleNoise are segments that label a page rather than a company.
var marketTitleNoise = map[string]bool{
	"home": true, "homepage": true, "home page": true, "welcome": true,
	"about": true, "about us": true, "contact": true, "contact us": true,
	"products": true, "our products": true, "product": true, "product range": true,
	"services": true, "our services": true, "solutions": true,
	"company": true, "company profile": true, "profile": true,
	"overview": true, "news": true, "newsroom": true, "blog": true,
	"careers": true, "jobs": true, "login": true, "shop": true, "store": true,
	"menu": true, "en": true, "english": true, "official site": true,
	"official website": true, "site": true, "website": true,
}

// marketDocumentWords are unambiguous document furniture. They are matched on
// word boundaries, so "Supermarket" or "Marketplace" never trips "market".
var marketDocumentWords = []string{
	"report", "reports", "forecast", "forecasts", "outlook",
	"whitepaper", "whitepapers", "survey", "statistics", "cagr",
	"directory", "ranking", "rankings", "patent", "patents",
	"handbook", "encyclopedia", "listicle", "pdf", "market",
}

// marketDocumentPhrases are multi-word document/roster shapes that a company
// name cannot plausibly contain.
var marketDocumentPhrases = []string{
	"market size", "market share", "market growth", "market analysis",
	"market research", "market report", "market forecast", "market outlook",
	"market map", "market landscape", "industry analysis", "industry report",
	"industry forecast", "industry outlook", "industry trends",
	"research report", "company profiles", "company listing", "trade data",
	"import data", "export data", "price list", "cost analysis",
	"feasibility study", "white paper", "press release", "annual report",
	"financial results", "balance sheet", "case study", "how to start",
	"guide to", "review of", "study of", "analysis of", "list of",
	"top 10", "top 5", "top 20", "top 25", "top 50", "10 best", "5 best",
	"supply and demand", "demand and supply", "size and share",
}

// marketRosterWords are plural role nouns: they describe a category of
// companies, never one company.
var marketRosterWords = []string{
	"companies", "manufacturers", "suppliers", "producers", "vendors",
	"exporters", "importers", "distributors", "players", "brands",
}

// marketListMarkers turn a roster noun into an obvious listicle.
var marketListMarkers = []string{
	"top", "best", "leading", "largest", "biggest", "major", "key", "list",
	"emerging", "prominent", "notable", "overview", "landscape", "map",
}

// marketHeadlineVerbs are news verbs that virtually never appear inside a
// trading name. A headline pairs a subject (1-2 words) with one of these.
var marketHeadlineVerbs = []string{
	"opens", "opened", "opening", "launches", "launched", "launching",
	"unveils", "unveiled", "announces", "announced", "announcing",
	"inaugurates", "inaugurated", "commissions", "commissioned",
	"constructs", "constructing", "builds", "building", "begins", "began",
	"starts", "started", "plans", "planned", "aims", "sets", "signs",
	"signed", "wins", "won", "bags", "secures", "secured", "acquires",
	"acquired", "buys", "bought", "sells", "sold", "divests", "divested",
	"expands", "expanded", "expanding", "invests", "invested", "investing",
	"partners", "partnered", "ties", "joins", "joined", "appoints",
	"appointed", "names", "named", "hires", "hired", "cuts", "raises",
	"raised", "recalls", "recalled", "faces", "sees", "expects", "reports",
	"reported", "posts", "posted", "records", "recorded", "registers",
	"files", "filed", "sues", "sued", "probes", "fined", "approves",
	"approved", "clears", "completed", "completes", "releases", "released",
}

// marketHeadlinePhrases are infinitive headline shapes ("X to build Y").
var marketHeadlinePhrases = []string{
	"to build", "to set up", "to expand", "to invest", "to acquire",
	"to buy", "to sell", "to launch", "to open", "to start", "to commission",
	"to construct", "to add", "to raise", "to cut", "to exit", "to enter",
}

// marketHeadlineFurniture is the front-of-headline furniture news desks use.
var marketHeadlineFurniture = []string{
	"exclusive", "breaking", "update", "watch", "analysis", "opinion",
	"interview", "explainer", "video", "podcast", "live", "explained",
}

// marketPublisherBrands are publication and platform brands that appear as the
// trailing segment of a syndicated headline ("... | Reuters").
var marketPublisherBrands = []string{
	"reuters", "bloomberg", "pti", "ani", "ians", "afp", "ap", "associated press",
	"bbc", "cnn", "cnbc", "forbes", "fortune", "economist", "financial times",
	"ft", "wsj", "wall street journal", "the guardian", "guardian", "nytimes",
	"new york times", "washington post", "hindustan times", "times of india",
	"economic times", "livemint", "mint", "business standard", "the hindu",
	"ndtv", "firstpost", "moneycontrol", "oilprice", "rigzone", "upstream",
	"energy voice", "icis", "argus", "spglobal", "statista", "linkedin",
	"wikipedia", "facebook", "youtube", "twitter", "x",
}

// marketConnectorWords join a topic to a place ("oil blending in Singapore").
// They never identify a company.
var marketConnectorWords = map[string]bool{
	"in": true, "at": true, "on": true, "by": true, "to": true, "of": true,
	"for": true, "and": true, "with": true, "from": true, "per": true,
	"via": true, "across": true, "throughout": true,
}

// marketGenericNameWords do not by themselves identify a company during
// verification mention matching, and do not count as domain-affinity signals.
var marketGenericNameWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true,
	"limited": true, "ltd": true, "private": true, "pvt": true, "plc": true,
	"llc": true, "llp": true, "inc": true, "incorporated": true, "corp": true,
	"corporation": true, "company": true, "co": true, "gmbh": true, "ag": true,
	"sa": true, "srl": true, "bv": true, "nv": true, "sdn": true, "bhd": true,
	"berhad": true, "pte": true, "kk": true, "holding": true, "holdings": true,
	"group": true, "industries": true, "industrial": true, "international": true,
	"global": true, "services": true, "solutions": true, "products": true,
	"systems": true, "technologies": true, "trading": true, "enterprise": true,
	"enterprises": true, "ventures": true, "manufacturing": true,
}

// marketIndustryWords name a product category rather than a company, so they
// cannot carry a verification mention on their own.
var marketIndustryWords = map[string]bool{
	"lubricant": true, "lubricants": true, "chemical": true, "chemicals": true,
	"plastic": true, "plastics": true, "solvent": true, "solvents": true,
	"oil": true, "oils": true, "energy": true, "pharma": true, "steel": true,
	"cement": true, "power": true, "gas": true, "water": true, "food": true,
	"textile": true, "textiles": true, "paint": true, "paints": true,
	"coating": true, "coatings": true, "fertiliser": true, "fertilizer": true,
	"petrochemical": true, "petrochemicals": true, "packaging": true,
	"manufacturer": true, "manufacturers": true, "supplier": true,
	"suppliers": true, "producer": true, "producers": true, "plant": true,
}

// marketWordText lowercases a string and reduces every non-alphanumeric run to
// a single space, padding both ends. Word-boundary phrase matching then becomes
// a plain substring test: "supermarket" never contains " market ".
func marketWordText(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte(' ')
	gap := true
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			gap = false
			continue
		}
		if !gap {
			b.WriteByte(' ')
			gap = true
		}
	}
	if !gap {
		b.WriteByte(' ')
	}
	return b.String()
}

func marketContainsPhrase(wordText, phrase string) bool {
	phrase = strings.TrimSpace(phrase)
	return phrase != "" && strings.Contains(wordText, " "+phrase+" ")
}

func marketContainsAny(wordText string, phrases []string) bool {
	for _, phrase := range phrases {
		if marketContainsPhrase(wordText, phrase) {
			return true
		}
	}
	return false
}

// marketTitleSegments splits a page title into candidate name segments.
func marketTitleSegments(title string) []string {
	segments := []string{title}
	for _, separator := range marketNameSeparators {
		next := make([]string, 0, len(segments)+2)
		for _, segment := range segments {
			next = append(next, strings.Split(segment, separator)...)
		}
		segments = next
	}
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		if trimmed := strings.TrimSpace(segment); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// marketStripTitleDecoration removes list prefixes, wrapping punctuation and
// the separators left behind by segment splitting.
func marketStripTitleDecoration(segment string) string {
	trimmed := strings.TrimSpace(segment)
	trimmed = strings.Trim(trimmed, " \t|:;,.·•»«-–—")
	for _, prefix := range []string{
		"welcome to ", "home of ", "official site of ", "official website of ",
		"top 10 ", "top 5 ", "top 20 ", "top 25 ", "10 best ", "5 best ",
		"list of ", "the 10 largest ", "the 5 largest ",
	} {
		if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			trimmed = strings.TrimSpace(trimmed[len(prefix):])
		}
	}
	trimmed = strings.TrimSpace(strings.Trim(trimmed, " \t:;,.·•»«-–—"))
	return strings.Join(strings.Fields(trimmed), " ")
}

// marketDomainLabel is the concatenated registrable-ish label of a domain,
// without the public suffix and without "www." or subdomains. "apac.gulf-
// oilltd.co.uk" becomes "gulfoilltd"; "kljresources.com" stays "kljresources".
func marketDomainLabel(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimPrefix(domain, "www.")
	if domain == "" {
		return ""
	}
	labels := strings.Split(domain, ".")
	if len(labels) > 1 {
		// Drop the public suffix; handle the common two-part suffixes.
		labels = labels[:len(labels)-1]
		if len(labels) > 1 {
			switch labels[len(labels)-1] {
			case "co", "com", "org", "net", "gov", "ac", "edu":
				labels = labels[:len(labels)-1]
			}
		}
	}
	return strings.Join(labels, "")
}

// marketBrandMatchesDomain reports whether a name shares a leading brand token
// with a domain. This is the strongest available identity signal: it is what
// ties "Gulf Oil Middle East" to gulfoilltd.com and "KLJ Resources Limited" to
// kljresources.com, both of which the old containment test missed.
func marketBrandMatchesDomain(name, domain string) bool {
	label := marketDomainLabel(domain)
	if label == "" {
		return false
	}
	for _, word := range strings.Fields(marketWordText(name)) {
		if len(word) < 3 || marketGenericNameWords[word] || marketIndustryWords[word] {
			continue
		}
		// A leading token is a brand even when short ("klj" -> kljresources).
		if strings.HasPrefix(label, word) {
			return true
		}
		// Longer tokens may sit inside a concatenated label.
		if len(word) >= 5 && strings.Contains(label, word) {
			return true
		}
	}
	return false
}

// marketDocumentTitle reports whether a string names a document or a category
// of companies rather than one company.
func marketDocumentTitle(title string) bool {
	if isResearchDocumentTitle(title) {
		return true
	}
	words := marketWordText(title)
	if words == "  " {
		return false
	}
	if marketContainsAny(words, marketDocumentWords) || marketContainsAny(words, marketDocumentPhrases) {
		return true
	}
	// "Top lubricant suppliers", "Key manufacturers" - a list marker plus a
	// roster noun is a listicle whatever the word order.
	return marketContainsAny(words, marketListMarkers) && marketContainsAny(words, marketRosterWords)
}

// marketHeadlineTitle reports whether a string is shaped like a news headline
// rather than a trading name. The subject is allowed to occupy the first token;
// a news verb after it, or an infinitive plan phrase, is decisive.
func marketHeadlineTitle(title string) bool {
	words := strings.Fields(marketWordText(title))
	if len(words) < 3 {
		return marketContainsAny(marketWordText(title), marketHeadlinePhrases)
	}

	if marketNoiseHeadlineFurniture(words[0]) {
		return true
	}
	for index := 1; index < len(words); index++ {
		word := words[index]
		if isMarketHeadlineVerb(word) {
			return true
		}
		if word == "to" && index+1 < len(words) {
			if marketContainsAny(marketWordText("to "+words[index+1]), marketHeadlinePhrases) {
				return true
			}
		}
	}
	return false
}

func marketNoiseHeadlineFurniture(word string) bool {
	for _, furniture := range marketHeadlineFurniture {
		if furniture == word {
			return true
		}
	}
	return false
}

func isMarketHeadlineVerb(word string) bool {
	for _, verb := range marketHeadlineVerbs {
		if verb == word {
			return true
		}
	}
	return false
}

// marketPublisherSuffix reports whether a title's trailing segment is a
// publication/platform brand ("Sinopec opens plant | Reuters").
func marketPublisherSuffix(title string) bool {
	segments := marketTitleSegments(title)
	if len(segments) < 2 {
		return false
	}
	last := strings.ToLower(marketStripTitleDecoration(segments[len(segments)-1]))
	last = strings.TrimSuffix(last, ".com")
	if last == "" {
		return false
	}
	for _, brand := range marketPublisherBrands {
		if last == brand || strings.HasPrefix(last, brand+" ") || strings.HasSuffix(last, " "+brand) {
			return true
		}
	}
	return false
}

// marketIsTaglineSegment reports whether a segment describes a capability
// ("Industrial Lubricants Manufacturer") instead of naming an entity.
func marketIsTaglineSegment(segment string) bool {
	words := marketWordText(segment)
	return marketContainsAny(words, []string{
		"manufacturer", "manufacturers", "supplier", "suppliers", "producer",
		"producers", "distributor", "distributors", "exporter", "exporters",
		"importer", "importers", "dealer", "dealers", "manufacturer of",
		"supplier of", "producer of", "leading", "trusted", "quality",
		"industrial", "specialty", "speciality", "solutions", "official",
	})
}

// marketNameSegmentScore ranks one title segment as a roster name.
func marketNameSegmentScore(segment, domain string, index int) int {
	score := 0
	if marketBrandMatchesDomain(segment, domain) {
		score += 100
	}
	words := marketWordText(segment)
	if marketContainsAny(words, []string{
		"limited", "ltd", "llc", "llp", "inc", "plc", "gmbh", "corporation",
		"corp", "pvt", "private", "sdn", "bhd", "pte", "spa", "srl", "bv", "nv",
	}) {
		score += 20
	}
	if marketDocumentTitle(segment) || marketHeadlineTitle(segment) {
		score -= 60
	}
	if marketIsTaglineSegment(segment) {
		score -= 40
	}
	if index == 0 {
		score += 5
	}
	return score
}

// marketCanonicalName reduces a page title to the roster name it carries, or ""
// when the title carries no usable company name (a report, a listicle, a
// capability tagline with no brand). The raw title always remains in the
// evidence record, so nothing is lost — it is simply not presented as a name.
func marketCanonicalName(title, domain string) string {
	best := ""
	bestScore := 0
	for index, segment := range marketTitleSegments(title) {
		// A document segment is evidence, not a name — unless it is the
		// operator's own domain brand (a report page on acme.com).
		if marketDocumentTitle(segment) && !marketBrandMatchesDomain(segment, domain) {
			continue
		}
		cleaned := marketStripTitleDecoration(segment)
		if cleaned == "" || marketTitleNoise[strings.ToLower(cleaned)] {
			continue
		}
		score := marketNameSegmentScore(cleaned, domain, index)
		if score > bestScore || (score == bestScore && best == "") {
			best, bestScore = cleaned, score
		}
	}
	if best == "" {
		return ""
	}
	// A chosen segment that still reads as a document only survives when the
	// domain proves it is the operator's own brand (a report on acme.com about
	// "Acme Market Outlook" is still Acme).
	if (marketDocumentTitle(best) || marketHeadlineTitle(best)) && !marketBrandMatchesDomain(best, domain) {
		return ""
	}
	if len(best) < 2 {
		return ""
	}
	return best
}

// marketLeadName normalizes a name the extractor proposed. Unlike
// marketCanonicalName it may be handed a bare name rather than a page title, so
// it only trims decoration and rejects values that are not a company name.
func marketLeadName(name, domain string) string {
	cleaned := marketStripTitleDecoration(name)
	if cleaned == "" || marketTitleNoise[strings.ToLower(cleaned)] {
		return ""
	}
	if marketDocumentTitle(cleaned) || marketHeadlineTitle(cleaned) || marketPublisherSuffix(cleaned) {
		return ""
	}
	if len(cleaned) < 2 || len(cleaned) > 120 {
		return ""
	}
	if len(strings.Fields(cleaned)) > 12 {
		return ""
	}
	// "Topic in Place" is how a listicle or a headline locates a subject, not
	// how a company names itself.
	words := marketWordText(cleaned)
	if marketContainsAny(words, []string{"in", "across", "throughout"}) {
		return ""
	}
	// The extractor occasionally returns the domain label itself when the page
	// title was navigation text; that is a domain, not a name.
	if label := marketDomainLabel(domain); label != "" && label == marketNameKey(cleaned) {
		return ""
	}
	return cleaned
}

// marketNameEchoesLead rejects a proposal that is simply a lead's own title
// echoed back. No operating company's name is a full article or report title.
func marketNameEchoesLead(name string, leadTitles []string) bool {
	key := strings.TrimSpace(marketWordText(name))
	if key == "" {
		return false
	}
	for _, title := range leadTitles {
		if strings.TrimSpace(marketWordText(title)) == key {
			return true
		}
	}
	return false
}

// marketLegalSuffixWords are the corporate-form words that make two spellings
// of one company ("Acme" / "Acme Ltd." / "Acme Private Limited") the same firm.
// Product words are deliberately NOT here: "Acme Oils" and "Acme Lubricants"
// may be two different companies.
var marketLegalSuffixWords = map[string]bool{
	"limited": true, "ltd": true, "private": true, "pvt": true, "plc": true,
	"llc": true, "llp": true, "inc": true, "incorporated": true, "corp": true,
	"corporation": true, "company": true, "co": true, "gmbh": true, "ag": true,
	"sa": true, "srl": true, "bv": true, "nv": true, "sdn": true, "bhd": true,
	"berhad": true, "pte": true, "kk": true, "holding": true, "holdings": true,
	"group": true,
}

// marketNameKey folds the variants one company is spelled with — legal
// suffixes, "&"/"and", punctuation — into one dedupe key.
func marketNameKey(name string) string {
	words := strings.Fields(marketWordText(name))
	kept := make([]string, 0, len(words))
	for _, word := range words {
		if word == "and" || word == "the" || word == "of" || marketLegalSuffixWords[word] {
			continue
		}
		kept = append(kept, word)
	}
	return strings.Join(kept, "")
}

// marketDistinctiveTokens are the name words that can carry a verification
// mention: not legal boilerplate, not industry vocabulary.
func marketDistinctiveTokens(name string) []string {
	tokens := make([]string, 0, 4)
	seen := make(map[string]bool)
	for _, word := range strings.Fields(marketWordText(name)) {
		if len(word) < 4 || seen[word] || marketGenericNameWords[word] || marketIndustryWords[word] {
			continue
		}
		seen[word] = true
		tokens = append(tokens, word)
		if len(tokens) == 3 {
			break
		}
	}
	return tokens
}
