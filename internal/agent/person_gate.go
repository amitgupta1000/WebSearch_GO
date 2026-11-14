package agent

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// person_gate.go is the identity check person mode was missing.
//
// Company mode holds a run to an entity at five points (rank prune, quality floor, snippet
// filter, row gate, freshness pass). Person mode held the run to nothing: every snippet the
// search engine returned went to the synthesiser unfiltered, so a same-named person at another
// employer — found because the query matched on the name alone — was mixed into the dossier.
// The pinned company and location were search terms only, never an acceptance rule.
//
// The gate applies the rule the confirmation step implies: a source reaches the dossier when
// it names the person, and — when a company was confirmed — only when it does not contradict
// that employer. A source whose headline states a *different* employer for the name is the
// same-named-different-person case, and it is excluded with its stated employer in the reason.
// A source that names the person and states no employer is kept but counted as name-only: a
// career history legitimately lists past employers in the body, so absence of the company is
// ambiguity, not contradiction.

const (
	personVerdictConfirmed   = "confirmed"
	personVerdictMatched     = "matched"
	personVerdictNameOnly    = "name-only"
	personVerdictConflicting = "conflicting"
	personVerdictUnrelated   = "unrelated"
)

// personGate carries the identity the operator confirmed, so every snippet and page can be
// held to it.
type personGate struct {
	name     string
	tokens   []string
	company  string
	location string
	seedURL  string
	din      string
}

func newPersonGate(query models.PersonQuery) *personGate {
	name := strings.TrimSpace(query.Name)
	return &personGate{
		name:     name,
		tokens:   personNameTokens(name),
		company:  strings.TrimSpace(query.Company),
		location: strings.TrimSpace(query.Location),
		seedURL:  strings.TrimSpace(query.SeedURL),
	}
}

// personNameTokens keeps every significant token of a person's name, including
// single-letter initials — "K L Jain" keeps k, l and jain, where the company
// tokenizer would drop the initials as noise and any Jain would then match.
func personNameTokens(name string) []string {
	return strings.Fields(normalizePersonName(name))
}

// personVerdict is the gate's decision for one source.
type personVerdict struct {
	Status   string
	Employer string // the employer a conflicting source states, for the reason
}

// usable reports whether the verdict admits the source into the dossier at all.
func (v personVerdict) usable() bool {
	return v.Status == personVerdictConfirmed || v.Status == personVerdictMatched || v.Status == personVerdictNameOnly
}

// mentionsCompany reports whether the company appears in the haystack. It is token-based
// rather than a substring test because sources punctuate and abbreviate legal names
// differently from how the operator typed them — "Sanjay Chemicals (India) Pvt. Ltd." must
// match a pinned "Sanjay Chemicals (India) Pvt Ltd", and an exact substring test rejects the
// right person's own evidence over a period. Sources also drop the qualifier a legal name
// carries — the same company is published as "Sanjay Chemicals Pvt" — so the name core (the
// leading tokens) matches too. Requiring the core whole keeps a namesake from matching:
// "Sanjay Enterprises" lacks "chemicals", and "… (India) …" alone matches nothing.
func mentionsCompany(haystack, company string) bool {
	tokens := nameTokens(company)
	if len(tokens) == 0 {
		return false
	}
	if containsAllTokens(haystack, tokens) {
		return true
	}
	core := tokens
	if len(core) > 2 {
		core = core[:2]
	}
	return containsAllTokens(haystack, core)
}

// anchorIdentity freezes the person's DIN once the registry director pages
// (WeeDoo, zaubacorp) reveal it: from then on, a source that carries the
// frozen DIN is about this person by construction, whatever employer its
// headline states — a director page legitimately lists other companies.
func (g *personGate) anchorIdentity(din string) {
	if strings.TrimSpace(din) != "" {
		g.din = strings.TrimSpace(din)
	}
}

// classify decides whether one source is about the confirmed person.
func (g *personGate) classify(targetURL, title, text string) personVerdict {
	haystack := strings.ToLower(title + " " + text)
	if g.seedURL != "" && strings.EqualFold(strings.TrimSpace(targetURL), g.seedURL) {
		return personVerdict{Status: personVerdictConfirmed}
	}
	// The frozen DIN is the strongest identity signal: a source whose URL or
	// text carries it is about this person by construction — registry
	// director pages list every company the person serves, so an employer in
	// the headline is a directorship, not a contradiction.
	if g.din != "" && strings.Contains(strings.ToLower(targetURL)+" "+haystack, g.din) {
		return personVerdict{Status: personVerdictConfirmed}
	}
	if !g.namedIn(haystack) {
		return personVerdict{Status: personVerdictUnrelated}
	}
	if g.company != "" && mentionsCompany(haystack, g.company) {
		return personVerdict{Status: personVerdictMatched}
	}
	// The headline is where a source states who this person works for. A different employer
	// stated there is the contradiction; an employer in the body is a career history, which is
	// legitimate for the right person.
	if stated := statedHeadlineEmployer(title, g.tokens); stated != "" && g.company != "" && !mentionsCompany(stated, g.company) {
		return personVerdict{Status: personVerdictConflicting, Employer: stated}
	}
	if g.location != "" && strings.Contains(haystack, strings.ToLower(g.location)) {
		return personVerdict{Status: personVerdictMatched}
	}
	return personVerdict{Status: personVerdictNameOnly}
}

// namedIn reports whether the haystack names the confirmed person: every name
// token appears, or the name is written with initials that align to the full
// name's leading words — "K L Jain" names a source about "Kanhaiya Lal Jain".
// For an initials-form name the alignment check is the ONLY acceptance rule:
// single-letter tokens as substrings would match almost any page.
func (g *personGate) namedIn(haystack string) bool {
	if g.initialsForm() {
		return personNamedInText(haystack, g.tokens)
	}
	long := g.longTokens()
	if len(long) > 0 && containsAllTokens(haystack, long) {
		return true
	}
	return personNamedInText(haystack, g.tokens)
}

// initialsForm reports whether every non-surname token is an initial run
// ("K L Jain", "Kl Jain") — at least one such token must exist.
func (g *personGate) initialsForm() bool {
	if len(g.tokens) < 2 {
		return false
	}
	for _, token := range g.tokens[:len(g.tokens)-1] {
		if len(token) > 2 {
			return false
		}
	}
	return true
}

// longTokens keeps the full-word tokens (three letters or more, including the
// surname) — the ones a substring test can meaningfully require.
func (g *personGate) longTokens() []string {
	long := make([]string, 0, len(g.tokens))
	for _, token := range g.tokens {
		if len(token) > 2 {
			long = append(long, token)
		}
	}
	return long
}

// personNamedInText matches an initials rendering of the person inside free
// text: the surname appears as a word, and the words immediately before it
// carry the person's initials, in order — "Kanhaiya Lal Jain" carries the
// initials k and l before "jain"; the single word "Kl" carries both. Full
// given words must appear as words; initials as word prefixes. A two-letter
// initial run ("Kl") counts as two letters.
func personNamedInText(haystack string, tokens []string) bool {
	if len(tokens) < 2 {
		return false
	}
	surname := tokens[len(tokens)-1]
	positions := personNamePositions(tokens)
	words := strings.Fields(haystack)
	// Expand two-letter words ("kl" → k, l) so an initials run written as one
	// word aligns with its positions; other words pass through untouched.
	expanded := make([]string, 0, len(words)+4)
	for _, word := range words {
		w := strings.Trim(word, ".,'\")")
		if len(w) == 2 {
			expanded = append(expanded, w[:1], w[1:])
			continue
		}
		expanded = append(expanded, w)
	}
	for index, word := range expanded {
		if word != surname || index < len(positions) {
			continue
		}
		window := expanded[index-len(positions) : index]
		matched := true
		for position, token := range positions {
			w := window[position]
			if len(token) > 2 {
				if w != token {
					matched = false
					break
				}
				continue
			}
			if !strings.HasPrefix(w, token) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// personNamePositions expands two-letter initial runs into single letters, so
// "Kl" aligns as the two positions k and l.
func personNamePositions(tokens []string) []string {
	positions := make([]string, 0, len(tokens)+2)
	for _, token := range tokens[:len(tokens)-1] {
		if len(token) == 2 {
			positions = append(positions, token[:1], token[1:])
			continue
		}
		positions = append(positions, token)
	}
	return positions
}

// statedHeadlineEmployer reads the employer a headline states for the person, or "" when it
// states none. Only an employer in a role context counts — "<role> at <X>", or a segment that
// follows a recognised role — because a court-case or news headline that merely carries the
// name must not read as a stated employer, or the gate would drop real evidence. It reuses the
// confirmation step's headline pieces, so both agree about what a source claims.
func statedHeadlineEmployer(title string, tokens []string) string {
	segments := splitPersonHeadline(title)
	for index, segment := range segments {
		if (!containsAllTokens(segment, tokens) && !personNamedInText(strings.ToLower(segment), tokens)) || isPersonTitleNoise(segment) {
			continue
		}
		_, tail := splitPersonNameSegment(segment, tokens)
		rest := make([]string, 0, 1+len(segments)-index)
		if tail != "" {
			rest = append(rest, tail)
		}
		rest = append(rest, segments[index+1:]...)
		roleSeen := false
		for _, part := range rest {
			if isPersonTitleNoise(part) {
				continue
			}
			if _, employer, ok := splitRoleAtEmployer(part); ok {
				return employer
			}
			if looksLikePersonRole(part) {
				roleSeen = true
				continue
			}
			if roleSeen {
				return part
			}
			return ""
		}
		return ""
	}
	return ""
}

// snippetFilter results: when a company was confirmed, a snippet is a free-floating assertion,
// so it must do more than name the person — it must name the confirmed company or the pinned
// location, or come from the confirmed profile. A snippet that names the person and states no
// employer is exactly how a same-named person slips in. When no company was confirmed there is
// nothing to hold the snippets to, so they are kept (a name-alone run is the operator's choice).
func (g *personGate) filterSnippets(results []models.SearchResult) (kept, excluded []models.SearchResult) {
	for _, result := range results {
		verdict := g.classify(result.Link, result.Title, result.Snippet)
		if verdict.usable() && (g.company == "" || verdict.Status != personVerdictNameOnly) {
			kept = append(kept, result)
			continue
		}
		excluded = append(excluded, result)
	}
	return kept, excluded
}

// filterPersonalization admits only public social results that name the person and state a
// corroborating token. Unlike employment evidence, these results do not need to mention the
// company — they are explicitly labelled as possible personalization context and never prove
// identity, employment, or role. They do, however, need more than the name: a common name
// matches thousands of accounts, and a section that presents those as the person's would be
// worse than no section at all. The admission rules live in personalizationVerdict so the
// agent can label each admitted signal with the same basis the gate used.
func (g *personGate) filterPersonalization(results []models.SearchResult) (kept, excluded []models.SearchResult) {
	for _, result := range results {
		if _, _, ok := g.personalizationVerdict(result); ok {
			kept = append(kept, result)
			continue
		}
		excluded = append(excluded, result)
	}
	return kept, excluded
}

// personalizationVerdict decides whether one public social hit is attributable enough for the
// flagged social section, and returns the identity basis and confidence the section displays.
// The order is deliberate: a confirmed employer is the strongest tie; a different employer in
// the headline is a namesake and is refused outright; otherwise the confirmed place or a
// stated role is enough to offer the hit as a possible account, never as a fact.
func (g *personGate) personalizationVerdict(result models.SearchResult) (basis string, confidence float64, ok bool) {
	if !isPublicSocialURL(result.Link) {
		return "", 0, false
	}
	haystack := strings.ToLower(result.Title + " " + result.Snippet)
	if !g.namedIn(haystack) {
		return "", 0, false
	}
	// A headline that states another employer is the same-named-different-person case, exactly
	// as it is for employment evidence. The section's warning does not excuse a contradiction
	// the gate can already see.
	if g.company != "" {
		if stated := statedHeadlineEmployer(result.Title, g.tokens); stated != "" && !mentionsCompany(stated, g.company) {
			return "", 0, false
		}
	}
	if g.company != "" && mentionsCompany(haystack, g.company) {
		return "name plus confirmed company", 0.7, true
	}
	if g.location != "" && strings.Contains(haystack, strings.ToLower(g.location)) {
		if g.company != "" {
			return "name plus confirmed location; company not stated", 0.4, true
		}
		return "name plus confirmed location", 0.5, true
	}
	if looksLikePersonRole(haystack) {
		if g.company != "" {
			return "name plus stated role; company not stated", 0.3, true
		}
		return "name plus stated role; possible account", 0.35, true
	}
	return "", 0, false
}

func isPublicSocialURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	path := strings.ToLower(parsed.Path)
	// LinkedIn posts and articles are social content too. They are public and readable, but
	// they are still personalization context, not employment evidence.
	linkedInPost := (host == "linkedin.com" || strings.HasSuffix(host, ".linkedin.com")) &&
		(strings.HasPrefix(path, "/posts/") || strings.HasPrefix(path, "/pulse/") || strings.HasPrefix(path, "/feed/update/"))
	return host == "facebook.com" || strings.HasSuffix(host, ".facebook.com") ||
		host == "instagram.com" || strings.HasSuffix(host, ".instagram.com") ||
		host == "twitter.com" || strings.HasSuffix(host, ".twitter.com") ||
		host == "x.com" || strings.HasSuffix(host, ".x.com") ||
		host == "youtube.com" || strings.HasSuffix(host, ".youtube.com") || host == "youtu.be" ||
		linkedInPost
}

func publicPlatform(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "social"
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	path := strings.ToLower(parsed.Path)
	switch {
	case strings.Contains(host, "facebook."):
		return "Facebook"
	case strings.Contains(host, "instagram."):
		return "Instagram"
	case strings.Contains(host, "twitter."), strings.Contains(host, "x.com"):
		return "X/Twitter"
	case strings.Contains(host, "youtube."), host == "youtu.be":
		return "YouTube"
	case strings.Contains(host, "linkedin.") &&
		(strings.HasPrefix(path, "/posts/") || strings.HasPrefix(path, "/pulse/") || strings.HasPrefix(path, "/feed/update/")):
		return "LinkedIn posts"
	default:
		return "social"
	}
}

// sortStableByName keeps exclusion listings deterministic.
func sortStableByName(rows []models.OffEntityRow) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
}

// exclusionsFrom converts the sources the gate dropped into the rows the dossier lists, with
// the reason each one failed.
func (g *personGate) exclusionsFrom(excluded []models.SearchResult) []models.OffEntityRow {
	rows := make([]models.OffEntityRow, 0, len(excluded))
	for _, result := range excluded {
		verdict := g.classify(result.Link, result.Title, result.Snippet)
		reason := "does not name " + g.name
		switch verdict.Status {
		case personVerdictConflicting:
			reason = fmt.Sprintf("states employer %q — a different person with the same name", verdict.Employer)
		case personVerdictNameOnly:
			reason = "names the person but states no employer; unconfirmed for this dossier"
		}
		rows = append(rows, models.OffEntityRow{
			Name:   result.Title,
			Title:  firstNonEmpty(verdict.Employer, strings.TrimSpace(result.Snippet)),
			Source: result.Link,
			Reason: reason,
		})
	}
	sortStableByName(rows)
	return rows
}

// dedupeResults keeps one row per URL, because a source can be excluded at two gates (the
// snippet filter and the fetch selection) and the dossier should list it once.
func dedupeResults(results []models.SearchResult) []models.SearchResult {
	seen := make(map[string]bool, len(results))
	kept := make([]models.SearchResult, 0, len(results))
	for _, result := range results {
		key := strings.ToLower(strings.TrimSpace(result.Link))
		if key == "" {
			key = result.Title
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		kept = append(kept, result)
	}
	return kept
}

// Grouping caps: the multilateral view shows the most relevant snippets per identity and the
// strongest identities first, so the section stays readable on a name with dozens of matches.
const (
	personGroupCap        = 8
	personGroupSnippetCap = 4
)

// sameNamedGroups turns the sources the identity gate excluded into the multilateral view: one
// group per employer a same-named person's source states — each is another person with the
// same name — plus one group for material that names the person but states no employer and
// therefore cannot be attributed. Sources that never name the person stay out: they are
// nobody's evidence.
func (g *personGate) sameNamedGroups(excluded []models.SearchResult) []models.PersonEvidenceGroup {
	type accumulator struct {
		snippets []models.SearchResult
	}
	groups := make(map[string]*accumulator)
	order := make([]string, 0, 4)
	for _, result := range excluded {
		verdict := g.classify(result.Link, result.Title, result.Snippet)
		var key string
		switch verdict.Status {
		case personVerdictConflicting:
			key = verdict.Employer
		case personVerdictNameOnly:
			key = ""
		default:
			continue
		}
		acc, ok := groups[key]
		if !ok {
			if len(order) >= personGroupCap {
				continue
			}
			acc = &accumulator{}
			groups[key] = acc
			order = append(order, key)
		}
		if len(acc.snippets) >= personGroupSnippetCap {
			continue
		}
		acc.snippets = append(acc.snippets, result)
	}
	// The identity with the most evidence leads; the unattributed material closes the list,
	// because it cannot be claimed by anyone.
	sort.SliceStable(order, func(i, j int) bool {
		emptyI, emptyJ := order[i] == "", order[j] == ""
		if emptyI != emptyJ {
			return emptyJ
		}
		if len(groups[order[i]].snippets) != len(groups[order[j]].snippets) {
			return len(groups[order[i]].snippets) > len(groups[order[j]].snippets)
		}
		return order[i] < order[j]
	})
	out := make([]models.PersonEvidenceGroup, 0, len(order))
	for _, key := range order {
		snippets := groups[key].snippets
		// This identity's own headline — the one carrying its full name — leads its group.
		sort.SliceStable(snippets, func(i, j int) bool {
			return containsAllTokens(strings.ToLower(snippets[i].Title), g.tokens) &&
				!containsAllTokens(strings.ToLower(snippets[j].Title), g.tokens)
		})
		out = append(out, models.PersonEvidenceGroup{Employer: key, Snippets: snippets})
	}
	return out
}

// personReportSourceType labels a scrape-report row the way the dossier's source list does.
func personReportSourceType(link string) string {
	if isProfileSourceURL(link) {
		return "profile"
	}
	return "web"
}
