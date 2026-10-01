package agent

import (
	"fmt"
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
}

func newPersonGate(query models.PersonQuery) *personGate {
	name := strings.TrimSpace(query.Name)
	return &personGate{
		name:     name,
		tokens:   nameTokens(name),
		company:  strings.TrimSpace(query.Company),
		location: strings.TrimSpace(query.Location),
		seedURL:  strings.TrimSpace(query.SeedURL),
	}
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

// classify decides whether one source is about the confirmed person.
func (g *personGate) classify(targetURL, title, text string) personVerdict {
	haystack := strings.ToLower(title + " " + text)
	if g.seedURL != "" && strings.EqualFold(strings.TrimSpace(targetURL), g.seedURL) {
		return personVerdict{Status: personVerdictConfirmed}
	}
	if !containsAllTokens(haystack, g.tokens) {
		return personVerdict{Status: personVerdictUnrelated}
	}
	if g.company != "" && strings.Contains(haystack, strings.ToLower(g.company)) {
		return personVerdict{Status: personVerdictMatched}
	}
	// The headline is where a source states who this person works for. A different employer
	// stated there is the contradiction; an employer in the body is a career history, which is
	// legitimate for the right person.
	if stated := statedHeadlineEmployer(title, g.tokens); stated != "" && g.company != "" && !strings.Contains(strings.ToLower(stated), strings.ToLower(g.company)) {
		return personVerdict{Status: personVerdictConflicting, Employer: stated}
	}
	if g.location != "" && strings.Contains(haystack, strings.ToLower(g.location)) {
		return personVerdict{Status: personVerdictMatched}
	}
	return personVerdict{Status: personVerdictNameOnly}
}

// statedHeadlineEmployer reads the employer a headline states for the person, or "" when it
// states none. Only an employer in a role context counts — "<role> at <X>", or a segment that
// follows a recognised role — because a court-case or news headline that merely carries the
// name must not read as a stated employer, or the gate would drop real evidence. It reuses the
// confirmation step's headline pieces, so both agree about what a source claims.
func statedHeadlineEmployer(title string, tokens []string) string {
	segments := splitPersonHeadline(title)
	for index, segment := range segments {
		if !containsAllTokens(segment, tokens) || isPersonTitleNoise(segment) {
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
		if verdict.Status == personVerdictConflicting {
			reason = fmt.Sprintf("states employer %q — a different person with the same name", verdict.Employer)
		} else if verdict.Status == personVerdictNameOnly {
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

// personReportSourceType labels a scrape-report row the way the dossier's source list does.
func personReportSourceType(link string) string {
	if isProfileSourceURL(link) {
		return "profile"
	}
	return "web"
}
