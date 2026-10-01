package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// person_resolve.go finds the people one typed name could mean, so a person run starts from
// a confirmed identity the same way a company run does.
//
// The problem is the mirror image of the company one. "Sanjay Chemicals" is ambiguous about
// *which organisation*; "Sanjay Gupta" is ambiguous about *which human*, and the disambiguator
// is context — role, employer, city. That context is exactly what the run needs and what an
// operator otherwise supplies from memory, so the step reads it off a profile and the operator
// confirms it.

const (
	// personResolveMaxCandidates bounds how many people are offered. More than a handful is
	// not a choice, it is a list.
	personResolveMaxCandidates = 4
	// personResolveQueryLimit bounds the searches this step may spend for the two core passes.
	// The aggregator pass is spent on top of it, and only when the core passes came back thin.
	personResolveQueryLimit = 2
	// personCandidateScoreFloor drops hits that carry a name and nothing else: a bare name
	// in a title is not a candidate identity, it is a coincidence.
	personCandidateScoreFloor = 1.5
)

// personRoleKeywords identify the segment of a headline that states a role. Professional
// networks title their hits "<name> - <role> - <employer>", so the role segment is what
// separates a person from a company page or an article.
var personRoleKeywords = []string{
	"chief", "ceo", "cfo", "coo", "cto", "cio", "chro", "president", "vice president", "vp",
	"chairman", "chairperson", "vice chairman", "director", "managing director", "head of",
	"manager", "general manager", "lead", "founder", "co-founder", "partner", "principal",
	"engineer", "developer", "analyst", "consultant", "advisor", "officer", "scientist",
	"professor", "researcher", "doctor", "physician", "surgeon", "advocate", "journalist",
	"editor", "author", "columnist", "contributor", "correspondent", "anchor", "presenter",
	"speaker", "host", "chemist", "architect", "designer", "specialist", "supervisor",
	"proprietor", "owner", "secretary", "treasurer", "executive", "assistant", "coordinator",
	"technician", "associate", "dean", "fellow", "trainee", "intern", "apprentice", "md",
}

// personTitleNoise are title segments that name the platform rather than the person. They
// must not be mistaken for an employer: a LinkedIn hit ends with "| LinkedIn".
var personTitleNoise = map[string]bool{
	"linkedin": true, "facebook": true, "twitter": true, "instagram": true, "x": true,
	"youtube": true, "profiles": true, "profile": true, "home": true, "about": true,
	"contact": true, "zoominfo": true, "rocketreach": true,
	"signalhire": true, "contactout": true, "apollo": true, "lusha": true, "leadiq": true,
	"crunchbase": true, "theorg": true, "datanyze": true, "wikipedia": true,
	"login": true, "sign in": true, "news": true, "blog": true, "articles": true,
}

// personDirectoryHosts are the people-directories: useful evidence of a role and employer,
// but not a profile, so they rank below one and are flagged on the card.
var personDirectoryHosts = []string{
	"rocketreach.co", "zoominfo.com", "signalhire.com", "contactout.com", "apollo.io",
	"lusha.com", "leadiq.com", "snov.io", "salesql.com", "hunter.io", "datanyze.com",
	"theorg.com", "uplead.com", "clearbit.com", "peopledatalabs.com",
}

// ResolvePersonCandidates searches for the typed name and reads the people out of the hits.
// A failed search is not an error: the caller renders the step with no candidates and the
// operator runs on the name alone.
func (pa *PersonAgent) ResolvePersonCandidates(ctx context.Context, name, company, location string) ([]models.PersonCandidate, error) {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return nil, fmt.Errorf("a person name is required")
	}
	tokens := nameTokens(trimmedName)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("a usable person name is required")
	}

	context := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(company), strings.TrimSpace(location)}, " "))
	queries := []string{strings.TrimSpace(strings.Join([]string{trimmedName, context}, " "))}
	if context == "" {
		// With no context to disambiguate, ask for the pages that carry one.
		queries = append(queries, fmt.Sprintf("%s linkedin profile executive", trimmedName))
	} else {
		queries = append(queries, trimmedName)
	}

	candidates := make([]models.PersonCandidate, 0, personResolveMaxCandidates*2)
	seen := make(map[string]bool, personResolveMaxCandidates*2)
	for _, query := range queries {
		results, err := pa.engine.SearchCached(ctx, query, pa.engine.MaxSearchResults())
		if err != nil {
			continue
		}
		for _, result := range results {
			candidate, ok := parsePersonCandidate(result, tokens, company, location)
			if !ok || candidate.Score < personCandidateScoreFloor {
				continue
			}
			key := strings.ToLower(candidate.Name + "|" + candidate.Company)
			if seen[key] {
				continue
			}
			seen[key] = true
			candidates = append(candidates, candidate)
		}
	}

	// The aggregators are the stand-in for a LinkedIn profile, which is unwinnable without a
	// session: a directory page still states a title and an employer for a named person, which
	// is exactly what this step pins. The query is spent only when the first passes came back
	// thin, so the step stays at two searches for the common case.
	if len(candidates) < 2 {
		aggregatorQuery := sourceSiteQuery(trimmedName, alternativeProfileSources)
		if aggregatorQuery != "" {
			if results, err := pa.engine.SearchCached(ctx, aggregatorQuery, pa.engine.MaxSearchResults()); err == nil {
				for _, result := range results {
					candidate, ok := parsePersonCandidate(result, tokens, company, location)
					if !ok || candidate.Score < personCandidateScoreFloor {
						continue
					}
					key := strings.ToLower(candidate.Name + "|" + candidate.Company)
					if seen[key] {
						continue
					}
					seen[key] = true
					candidates = append(candidates, candidate)
				}
			}
		}
	}

	// The best candidate for *this* operator first: someone whose hit names the company or
	// city already typed outranks a bare profile, and a real profile outranks a directory.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Name < candidates[j].Name
	})
	if len(candidates) > personResolveMaxCandidates {
		candidates = candidates[:personResolveMaxCandidates]
	}
	return candidates, nil
}

// parsePersonCandidate reads a person out of one search hit: the name, and the role and
// employer when the hit states them. It refuses hits that do not carry every token of the
// typed name, because a surname alone matches too much to be an identity.
func parsePersonCandidate(result models.SearchResult, tokens []string, company, location string) (models.PersonCandidate, bool) {
	title := strings.TrimSpace(result.Title)
	if title == "" {
		return models.PersonCandidate{}, false
	}
	segments := splitPersonHeadline(title)

	name := ""
	var rest []string
	for index, segment := range segments {
		if containsAllTokens(segment, tokens) && !isPersonTitleNoise(segment) {
			var tail string
			name, tail = splitPersonNameSegment(segment, tokens)
			if tail != "" {
				rest = append(rest, tail)
			}
			rest = append(rest, segments[index+1:]...)
			break
		}
	}
	if name == "" {
		return models.PersonCandidate{}, false
	}

	candidate := models.PersonCandidate{
		Name:            name,
		URL:             result.Link,
		EvidenceTitle:   title,
		EvidenceSnippet: strings.TrimSpace(result.Snippet),
	}
	for _, segment := range rest {
		if isPersonTitleNoise(segment) {
			continue
		}
		if candidate.Title == "" {
			// "<role> at <employer>" is the headline shape most networks use, and it splits
			// even when the role itself is one this list does not know: "Associate at S V
			// chemicals" must not read as an employer called "Associate at S V chemicals".
			if role, employer, ok := splitRoleAtEmployer(segment); ok {
				candidate.Title = role
				if candidate.Company == "" {
					candidate.Company = employer
				}
				continue
			}
			if looksLikePersonRole(segment) {
				candidate.Title = segment
				continue
			}
		}
		if candidate.Company == "" {
			candidate.Company = segment
		}
	}

	haystack := strings.ToLower(title + " " + result.Snippet)
	if trimmed := strings.ToLower(strings.TrimSpace(company)); trimmed != "" && strings.Contains(haystack, trimmed) {
		candidate.MatchesCompany = true
	}
	if trimmed := strings.ToLower(strings.TrimSpace(location)); trimmed != "" && strings.Contains(haystack, trimmed) {
		candidate.MatchesLocation = true
	}
	candidate.Directory = isPersonDirectoryHost(result.Link)
	candidate.Score = scorePersonCandidate(candidate, result.Link, len(tokens))
	return candidate, true
}

// scorePersonCandidate ranks candidates the way an operator would: context that matches what
// was already typed first, then a stated role and employer, then the kind of page it is.
func scorePersonCandidate(candidate models.PersonCandidate, targetURL string, nameTokenCount int) float64 {
	score := 1.0
	if nameTokenCount > 1 {
		// A fuller name is a more specific identity: "Sanjay Gupta" matches fewer people
		// than "Sanjay".
		score += 0.5
	}
	if candidate.Title != "" {
		score++
	}
	if candidate.Company != "" {
		score++
	}
	if candidate.MatchesCompany {
		score += 2
	}
	if candidate.MatchesLocation {
		score++
	}
	if candidate.Directory {
		score--
	}
	// A professional profile or the person's own site is the page an operator wants.
	lowered := strings.ToLower(targetURL)
	switch {
	case strings.Contains(lowered, "linkedin.com/in/"):
		score++
	case strings.Contains(lowered, "github.com/"), strings.Contains(lowered, "/about"), strings.Contains(lowered, "wikipedia.org"):
		score += 0.5
	}
	return score
}

// splitPersonHeadline breaks a page title into its segments. Separators are the ones
// professional networks and news sites use between a name, a role and an employer.
func splitPersonHeadline(title string) []string {
	replaced := title
	for _, separator := range []string{" | ", " — ", " – ", " · ", " - ", "|", "—", "–"} {
		replaced = strings.ReplaceAll(replaced, separator, "\x00")
	}
	segments := make([]string, 0, 4)
	for _, segment := range strings.Split(replaced, "\x00") {
		if trimmed := strings.TrimSpace(segment); trimmed != "" {
			segments = append(segments, trimmed)
		}
	}
	return segments
}

// firstNonEmpty returns the first value that is not blank, which keeps a confirmed profile's
// label useful when the source never stated a title.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// containsAllTokens reports whether every token of the typed name appears in the segment as
// a whole word.
func containsAllTokens(segment string, tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	lowered := strings.ToLower(segment)
	for _, token := range tokens {
		if !containsWholeWord(lowered, token) {
			return false
		}
	}
	return true
}

// containsWholeWord is a whole-word containment test on already-lowered text, without the
// regex compilation the entity keys use: a headline is tested once per token.
func containsWholeWord(lowered, token string) bool {
	for offset := 0; offset < len(lowered); {
		index := strings.Index(lowered[offset:], token)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(token)
		leftOK := start == 0 || !isWordByte(lowered[start-1])
		rightOK := end >= len(lowered) || !isWordByte(lowered[end])
		if leftOK && rightOK {
			return true
		}
		offset = start + 1
	}
	return false
}

// isWordByte reports whether a byte continues a word, which is what keeps "Gupta" from
// matching inside "Guptan".
func isWordByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b >= 0x80:
		// A non-ASCII byte is part of a letter (an accented name); treating it as a
		// boundary would let a partial match through.
		return true
	default:
		return false
	}
}

// splitPersonNameSegment returns the name and, when the same segment states a role after a
// comma ("Jane Doe, Managing Director"), that role, so the card still learns it.
func splitPersonNameSegment(segment string, tokens []string) (string, string) {
	trimmed := strings.TrimSpace(segment)
	if index := strings.Index(trimmed, ","); index > 0 {
		head := strings.TrimSpace(trimmed[:index])
		if containsAllTokens(head, tokens) {
			return head, strings.TrimSpace(trimmed[index+1:])
		}
	}
	// Keep everything up to the last word that carries a name token: "Prof. Sanjay Gupta of
	// IIT" keeps the honorific and drops the trailing prose, which is not a role.
	words := strings.Fields(trimmed)
	last := -1
	for index, word := range words {
		lowered := strings.ToLower(word)
		for _, token := range tokens {
			if containsWholeWord(lowered, token) {
				last = index
				break
			}
		}
	}
	if last >= 0 && last < len(words)-1 {
		return strings.Join(words[:last+1], " "), ""
	}
	return trimmed, ""
}

// looksLikePersonRole reports whether a segment states a role.
func looksLikePersonRole(segment string) bool {
	lowered := " " + strings.ToLower(strings.TrimSpace(segment)) + " "
	for _, keyword := range personRoleKeywords {
		if strings.Contains(lowered, " "+keyword) {
			return true
		}
	}
	return false
}

// splitRoleAtEmployer splits "<role> at <employer>", the headline shape professional networks
// use.
func splitRoleAtEmployer(segment string) (string, string, bool) {
	index := strings.Index(strings.ToLower(segment), " at ")
	if index <= 0 {
		return "", "", false
	}
	role := strings.TrimSpace(segment[:index])
	employer := strings.TrimSpace(segment[index+4:])
	if role == "" || employer == "" || isPersonTitleNoise(employer) {
		return "", "", false
	}
	return role, employer, true
}

// isPersonTitleNoise reports whether a segment names a platform rather than a person or an
// employer.
func isPersonTitleNoise(segment string) bool {
	return personTitleNoise[strings.ToLower(strings.TrimSpace(segment))]
}

// isPersonDirectoryHost reports whether a URL is a people-directory listing.
func isPersonDirectoryHost(targetURL string) bool {
	lowered := strings.ToLower(targetURL)
	for _, directory := range personDirectoryHosts {
		if strings.Contains(lowered, directory) {
			return true
		}
	}
	return false
}
