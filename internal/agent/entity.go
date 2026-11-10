package agent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// entity.go is the entity gate: it answers one question, deterministically, for
// every planned query, search candidate, snippet and person row — does this source
// name the company we were asked about?
//
// It exists because "relevant to the brief" is not "about the company". A live run
// for a company called CJ Shah — a name that reads as initials plus a surname —
// assembled 204 people from 24 sources, none of them on the company's own domain:
// a LinkedIn name directory, a textbook by an author named Shah, a 2016 conference
// archive, papers on Malaysian construction procurement, and a global
// wealth-management roundup. Every one of those is topically relevant to a
// procurement brief; none of them is about CJ Shah.

// entityKey is one matchable identity: the domain, the registration id, the name or
// an alias. Keys are tried strongest first, so a domain match is never reported as a
// name match.
type entityKey struct {
	kind       string
	value      string
	confidence float64
	// domain is the registrable host of a domain key.
	domain string
	// tokens are the significant words of a name or alias. Every one must appear as
	// a whole word for the key to match, which is what keeps "Janat Shah" from
	// matching "CJ Shah" (no cj) and "Rakshith CJ" from matching it either (no shah).
	tokens []*regexp.Regexp
	// tokenValues are the same words as plain strings, for proximity checks.
	tokenValues []string
	// collapsed is the name with every non-alphanumeric removed and lowercased —
	// "Goyum Screw Press" and "Goyum Screwpress" both collapse to
	// "goyumscrewpress". It is only set for names of at least two significant
	// tokens whose collapsed form is eight or more characters, so short or
	// initialism names ("CJ Shah" → "cjshah") keep the strict whole-word rule and
	// can never match inside a longer unrelated word.
	collapsed string
}

// legalSuffixes are dropped when tokenising a company name, so "KLJ Resources
// Limited" and "KLJ Resources" compare equal. "co" is deliberately not dropped:
// "CJ Shah & Co" is a different company from "CJ Shah", and collapsing them is the
// exact confusion this gate exists to surface — the artifact reports matches that
// rest on the name alone.
var legalSuffixes = map[string]bool{
	"limited": true, "ltd": true, "private": true, "pvt": true, "inc": true,
	"llc": true, "plc": true, "incorporated": true, "corp": true, "corporation": true,
}

// entityMatcher is a run's compiled identity: built once from the brief, consulted
// for every candidate, snippet and row.
type entityMatcher struct {
	brief models.CompanyBrief
	keys  []entityKey
}

// newEntityMatcher compiles the brief's identity, strongest key first: the domain,
// then the registration id, then the most specific name or alias.
func newEntityMatcher(brief models.CompanyBrief) *entityMatcher {
	matcher := &entityMatcher{brief: brief}
	var named []entityKey
	// Every confirmed domain is a key, strongest first: one company under several
	// names is still one entity, and a source naming any of its domains is on-entity.
	// The primary domain stays first so artifacts read it as the canonical one.
	for _, domain := range entityDomains(brief) {
		matcher.keys = append(matcher.keys, entityKey{
			kind: models.EntityKeyDomain, value: domain, domain: domain, confidence: 0.95,
		})
	}
	if id := normalizeRegistrationID(brief.RegistrationID); len(id) >= 8 {
		matcher.keys = append(matcher.keys, entityKey{
			kind: models.EntityKeyRegistration, value: strings.TrimSpace(brief.RegistrationID), confidence: 0.9,
		})
	}
	if key, ok := nameEntityKey(models.EntityKeyName, brief.CompanyName, 0.8); ok {
		named = append(named, key)
	}
	for _, alias := range brief.Aliases {
		if key, ok := nameEntityKey(models.EntityKeyAlias, alias, 0.7); ok {
			named = append(named, key)
		}
	}
	// More specific names first: with "CJ Shah" and "CJ Shah & Co" both configured as
	// keys, a source that says "CJ Shah & Co" must be reported as the alias match,
	// because that is the stronger, more informative attribution.
	sort.SliceStable(named, func(i, j int) bool { return len(named[i].tokens) > len(named[j].tokens) })
	matcher.keys = append(matcher.keys, named...)
	return matcher
}

// NormalizeDomain reduces what a human typed into the domain the gate can use: a pasted
// URL becomes its host, `www.` and any path are dropped, and case is folded. It returns ""
// when the value cannot be read as a host at all, so a caller can fall back to looking the
// company up instead of gating a run on something that is not a domain.
func NormalizeDomain(value string) string {
	return normalizeDomain(value)
}

// entityDomains returns the brief's confirmed domains, primary first, normalized and
// deduplicated. The primary domain stays first even when it is repeated in Domains, so the
// canonical domain is always the key the gate reports first.
func entityDomains(brief models.CompanyBrief) []string {
	var domains []string
	seen := make(map[string]bool, len(brief.Domains)+1)
	for _, candidate := range append([]string{brief.Domain}, brief.Domains...) {
		domain := normalizeDomain(candidate)
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		domains = append(domains, domain)
	}
	return domains
}

// nameEntityKey compiles a name or alias into a token-matching key. A single short
// token is refused: "CJ" on its own matches too much to be an identity, whereas
// "Anthropic" does not.
func nameEntityKey(kind, value string, confidence float64) (entityKey, bool) {
	trimmed := strings.TrimSpace(value)
	tokens := nameTokens(trimmed)
	if len(tokens) == 0 {
		return entityKey{}, false
	}
	if len(tokens) == 1 && len(tokens[0]) < 4 {
		return entityKey{}, false
	}
	compiled := make([]*regexp.Regexp, 0, len(tokens))
	for _, token := range tokens {
		compiled = append(compiled, regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(token)+`\b`))
	}
	key := entityKey{kind: kind, value: trimmed, tokens: compiled, tokenValues: tokens, confidence: confidence}
	// Sources segment the same name differently: the operator types "Goyum
	// Screwpress", the registry spells "Goyum Screw Press", a directory runs the
	// words together. The collapsed form lets those variants match, but only for
	// names long enough that the collapsed match cannot sit inside an unrelated
	// word — short or initialism names keep the strict whole-word rule.
	if len(tokens) >= 2 {
		if collapsed := collapseText(trimmed); len(collapsed) >= minCollapsedNameLength {
			key.collapsed = collapsed
		}
	}
	return key, true
}

// minCollapsedNameLength is the smallest collapsed name the spelling-variant
// fallback will match on. Below it a collapsed name can sit inside an unrelated
// word ("samson" contains "sam son"; "cjshah" is six letters), so short names
// stay on the strict whole-word path.
const minCollapsedNameLength = 8

// nameTokens lowercases a company name and reduces it to significant words:
// punctuation becomes a separator, legal suffixes are dropped, and one-character
// tokens are ignored.
func nameTokens(value string) []string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return ' '
		}
	}, value)
	tokens := make([]string, 0, 4)
	for _, token := range strings.Fields(strings.ToLower(cleaned)) {
		if len(token) < 2 || legalSuffixes[token] {
			continue
		}
		tokens = append(tokens, token)
	}
	return tokens
}

// collapseText reduces text to lowercase letters and digits only, so names that
// differ only in spacing, punctuation or segmentation compare equal: "Goyum
// Screw Press", "Goyum Screwpress" and "goyum-screw-press" all collapse to
// "goyumscrewpress".
func collapseText(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r - ('A' - 'a')
		default:
			return -1
		}
	}, value)
}

// wordTokens splits lowercase text into alphanumeric words; every separator
// (space, punctuation, URL syntax) is a boundary. It backs the spelling-variant
// fallback, where a match must span whole words on both sides.
func wordTokens(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
}

// textContainsNameRun reports whether some contiguous run of word tokens, joined,
// equals the collapsed name — "goyum"+"screwpress" and "goyum"+"screw"+"press" both
// produce "goyumscrewpress", while a longer run-together word that merely begins
// with the name ("sanjaychemicalsllp" vs "sanjay chemicals") never does, because a
// match has to consume whole words on both sides.
func textContainsNameRun(words []string, collapsed string) bool {
	for start := 0; start < len(words); start++ {
		joined := ""
		for end := start; end < len(words); end++ {
			if len(joined)+len(words[end]) > len(collapsed) {
				break
			}
			joined += words[end]
			if joined == collapsed {
				return true
			}
		}
	}
	return false
}

// normalizeRegistrationID reduces a registry identifier to comparable form: the
// CIN's separators vary between publishers ("U74999MH2005PTC152345" vs
// "U74999MH-2005-PTC-152345").
func normalizeRegistrationID(value string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - ('a' - 'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return -1
		}
	}, strings.TrimSpace(value))
	return cleaned
}

// matchText reports whether a text names the entity, returning the strongest key
// that matched.
func (m *entityMatcher) matchText(text string) (models.EntityMatch, bool) {
	if m == nil || strings.TrimSpace(text) == "" {
		return models.EntityMatch{}, false
	}
	lowered := strings.ToLower(text)
	normalizedID := ""
	var textWords []string
	for _, key := range m.keys {
		switch key.kind {
		case models.EntityKeyDomain:
			if strings.Contains(lowered, key.domain) {
				return entityMatchOf(key, key.domain), true
			}
		case models.EntityKeyRegistration:
			if normalizedID == "" {
				normalizedID = normalizeRegistrationID(text)
			}
			if strings.Contains(normalizedID, normalizeRegistrationID(key.value)) {
				return entityMatchOf(key, key.value), true
			}
		default:
			matched := true
			for _, token := range key.tokens {
				if !token.MatchString(text) {
					matched = false
					break
				}
			}
			if matched {
				return entityMatchOf(key, key.value), true
			}
			// Spelling-variant fallback: "Goyum Screwpress" (as typed) and
			// "Goyum Screw Press" (as registered) are the same company. The
			// comparison joins whole words on both sides, so any segmentation of
			// the full name matches ("goyum"+"screwpress", "goyum"+"screw"+"press")
			// while a longer run-together word that merely begins with the name
			// ("sanjaychemicalsllp" vs "sanjay chemicals") never does.
			if key.collapsed != "" {
				if textWords == nil {
					textWords = wordTokens(lowered)
				}
				if textContainsNameRun(textWords, key.collapsed) {
					return entityMatchOf(key, key.value), true
				}
			}
		}
	}
	return models.EntityMatch{Matched: false}, false
}

// matchURL ties a URL to the entity by domain — the one key that cannot be
// coincidental.
func (m *entityMatcher) matchURL(rawURL string) (models.EntityMatch, bool) {
	if m == nil {
		return models.EntityMatch{}, false
	}
	host := normalizeDomain(rawURL)
	if host == "" {
		return models.EntityMatch{}, false
	}
	for _, key := range m.keys {
		if key.kind != models.EntityKeyDomain {
			continue
		}
		if host == key.domain || strings.HasSuffix(host, "."+key.domain) {
			return entityMatchOf(key, host), true
		}
	}
	return models.EntityMatch{}, false
}

// match ties a source to the entity, preferring the URL over the text: a page on the
// company's own domain is about the company even when the snippet never spells the
// name.
func (m *entityMatcher) match(targetURL, title, content string) models.EntityMatch {
	if m == nil {
		return models.EntityMatch{Matched: false}
	}
	if found, ok := m.matchURL(targetURL); ok {
		return found
	}
	if found, ok := m.matchText(strings.Join([]string{title, targetURL, content}, "\n")); ok {
		return found
	}
	return models.EntityMatch{Matched: false}
}

// companyMentionWindow is how close a company-name mention must be to a person's name for
// a page to count as evidence *about that person*.
//
// A whole-page co-occurrence is not enough. Verified on a live run: a MoneyControl article
// about Chemcon Speciality Chemicals' IPO contains "Sanjay Chemicals" twice and the word
// "chemicals" 96 times, and an Indian Kanoon judgment mentions it four times — both passed
// the old rule and entered the source list. A person whose name appears elsewhere on such
// a page is not evidence about the company.
const companyMentionWindow = 300

// nameKeyTokens returns every token of the name and alias keys, for proximity checks.
func (m *entityMatcher) nameKeyTokens() []string {
	if m == nil {
		return nil
	}
	tokens := make([]string, 0, 4)
	for _, key := range m.keys {
		if key.kind != models.EntityKeyName && key.kind != models.EntityKeyAlias {
			continue
		}
		tokens = append(tokens, key.tokenValues...)
	}
	return tokens
}

// nameKeyCollapsedValues returns the collapsed spelling-variant forms of the name
// and alias keys, for proximity checks that must tolerate the same variants the
// snippet gate does.
func (m *entityMatcher) nameKeyCollapsedValues() []string {
	if m == nil {
		return nil
	}
	values := make([]string, 0, 2)
	for _, key := range m.keys {
		if (key.kind == models.EntityKeyName || key.kind == models.EntityKeyAlias) && key.collapsed != "" {
			values = append(values, key.collapsed)
		}
	}
	return values
}

// mentionsCompanyNear reports whether the company is named within companyMentionWindow
// runes of the person's name in this text.
//
// It anchors on the person's surname (with the first token as a fallback) and requires
// every word of the company's name inside that window, so "Ravi Menon, VP Procurement at
// Sanjay Chemicals" qualifies while "Sanjay Chemicals" in a peer-company table on a page
// that also happens to name someone does not. The window is measured in bytes so it is
// slightly stricter than the rune count for non-ASCII text, which is the safe direction.
func mentionsCompanyNear(text, personName string, matcher *entityMatcher) bool {
	if matcher == nil {
		return true
	}
	personTokens := nameTokens(personName)
	companyTokens := matcher.nameKeyTokens()
	companyCollapsed := matcher.nameKeyCollapsedValues()
	if len(personTokens) == 0 || len(companyTokens) == 0 {
		return false
	}
	anchors := []string{personTokens[len(personTokens)-1]}
	if len(personTokens) > 1 {
		anchors = append(anchors, personTokens[0])
	}
	lowered := strings.ToLower(text)
	for _, anchor := range anchors {
		searchFrom := 0
		for {
			index := strings.Index(lowered[searchFrom:], anchor)
			if index < 0 {
				break
			}
			index += searchFrom
			start := index - companyMentionWindow
			if start < 0 {
				start = 0
			}
			end := index + companyMentionWindow
			if end > len(lowered) {
				end = len(lowered)
			}
			window := lowered[start:end]
			complete := true
			for _, token := range companyTokens {
				if !strings.Contains(window, token) {
					complete = false
					break
				}
			}
			if !complete {
				// The mention may spell the company with different segmentation
				// ("Goyum Screw Press" for a "Goyum Screwpress" brief); the run
				// still has to consume whole words inside the window.
				windowWords := wordTokens(window)
				for _, collapsedKey := range companyCollapsed {
					if textContainsNameRun(windowWords, collapsedKey) {
						complete = true
						break
					}
				}
			}
			if complete {
				return true
			}
			searchFrom = index + 1
		}
	}
	return false
}

// namesTheEntity reports whether a roster name is the company's own name or an alias
// rather than a person's. Registry and shareholding pages label a promoter with the
// company name ("CJ SHAH" as a shareholder), and the model can mistake that for a person;
// the row is then the entity itself.
func (m *entityMatcher) namesTheEntity(personName string) bool {
	if m == nil || strings.TrimSpace(personName) == "" {
		return false
	}
	for _, key := range m.keys {
		if key.kind != models.EntityKeyName && key.kind != models.EntityKeyAlias {
			continue
		}
		matched := len(key.tokens) > 0
		for _, token := range key.tokens {
			if !token.MatchString(personName) {
				matched = false
				break
			}
		}
		if !matched && key.collapsed != "" && textContainsNameRun(wordTokens(strings.ToLower(personName)), key.collapsed) {
			// A registry row carrying the company's name in the other spelling
			// ("Goyum Screw Press Pvt Ltd" for a "Goyum Screwpress" brief) is the
			// entity, not a person.
			matched = true
		}
		if matched {
			return true
		}
	}
	return false
}

// covers reports whether a planned query is anchored on the entity. A query that is
// not is never run: the point is to stop industry-generic results — which look
// relevant and belong to someone else — from being fetched at all.
func (m *entityMatcher) covers(query string) bool {
	_, ok := m.matchText(query)
	return ok
}

// entityMatchOf packs a matched key.
func entityMatchOf(key entityKey, value string) models.EntityMatch {
	return models.EntityMatch{Matched: true, Key: key.kind, Value: value, Confidence: key.confidence}
}

// describe lists the identity keys, strongest first, for prompts and reports.
func (m *entityMatcher) describe() []string {
	if m == nil {
		return nil
	}
	labels := make([]string, 0, len(m.keys))
	for _, key := range m.keys {
		labels = append(labels, fmt.Sprintf("%s:%s", key.kind, key.value))
	}
	return labels
}

// filterEntitySnippets keeps only the search snippets that name the company, and
// reports how many it dropped.
//
// This is the gate that closes the widest hole: the synthesiser is handed the search
// snippets *and* the scraped evidence, and before this filter every snippet from every
// planned query reached it. A name and a title in a snippet is all it takes — which is
// how a global wealth-management roundup became a marketing hire, and how a 2016
// summit page produced five procurement vice presidents.
// splitEntitySnippets splits results into the ones the entity matcher admits and the ones it
// excludes, keeping both halves: filterEntitySnippets only needs the kept side, but the run's
// search trace shows the excluded side too — that is what makes a short roster auditable.
func splitEntitySnippets(results []models.SearchResult, matcher *entityMatcher) (kept, excluded []models.SearchResult) {
	if matcher == nil {
		return results, nil
	}
	kept = make([]models.SearchResult, 0, len(results))
	excluded = make([]models.SearchResult, 0)
	for _, result := range results {
		if matcher.match(result.Link, result.Title, result.Snippet).Matched {
			kept = append(kept, result)
			continue
		}
		excluded = append(excluded, result)
	}
	return kept, excluded
}

// filterEntitySnippets results: only snippets that name the company reach the synthesiser.
func filterEntitySnippets(results []models.SearchResult, matcher *entityMatcher) ([]models.SearchResult, int) {
	if matcher == nil {
		return results, 0
	}
	kept, _ := splitEntitySnippets(results, matcher)
	return kept, len(results) - len(kept)
}

// snippetNamesEntity reports whether one snippet is tied to the company. It is used
// where a candidate falls back to its public snippet as evidence: the snippet is only
// worth keeping if it names the company, because otherwise the person it describes
// works somewhere else.
func snippetNamesEntity(result models.SearchResult, matcher *entityMatcher) bool {
	if matcher == nil {
		return true
	}
	return matcher.match(result.Link, result.Title, result.Snippet).Matched
}

// promptLine states the identity for a prompt, so the model is told exactly what the
// gate enforces.
func (m *entityMatcher) promptLine() string {
	if m == nil || len(m.keys) == 0 {
		return "Company name: (none given)"
	}
	parts := make([]string, 0, len(m.keys))
	for _, key := range m.keys {
		parts = append(parts, fmt.Sprintf("%s (%s)", key.value, key.kind))
	}
	confirmed := "typed by the operator, not confirmed"
	if m.brief.EntityConfirmed {
		confirmed = "confirmed by the operator"
	}
	return fmt.Sprintf("The company is: %s — %s", strings.Join(parts, ", "), confirmed)
}
