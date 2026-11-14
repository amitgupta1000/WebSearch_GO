package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// person_qc.go is the post-synthesis gate. The identity gate holds the evidence before the
// model sees it; QC holds the model's output after. Between them the two ways a dossier ends
// up about several people are closed: no same-named source gets in, and no synthesised row
// about a different employer survives.
//
// The rule is deliberately conservative about career history: a past employer is legitimate.
// A row whose employer is not the confirmed one survives only when a bounded reverse lookup
// (`"Name" "That employer"`) ties the two together — which is what separates this person's
// career history from another person's role. Rows that cannot be tied are dropped and listed
// in the identity check; an empty section is better than an invented or blended one.

// qcPersonProfile holds the synthesised dossier to the confirmed identity and returns the rows
// it dropped, for the dossier's identity check. With no company confirmed there is nothing to
// hold rows to, so a name-alone dossier passes through untouched.
func (pa *PersonAgent) qcPersonProfile(
	ctx context.Context,
	profile *models.PersonProfile,
	gate *personGate,
) ([]models.OffEntityRow, int) {
	// The model repeats an identical row when several snippets say the same thing about it;
	// the dossier lists that fact once.
	profile.Experiences = dedupeExperienceRows(profile.Experiences)
	if gate == nil || gate.company == "" {
		// Timeline consistency does not need a confirmed employer: it is row-against-row.
		flagOverlappingTimelines(profile.Experiences)
		return nil, 0
	}
	pinnedTokens := nameTokens(gate.company)
	sharesPinned := func(value string) bool {
		for _, token := range nameTokens(value) {
			for _, pin := range pinnedTokens {
				if token == pin {
					return true
				}
			}
		}
		return false
	}

	kept := make([]models.Experience, 0, len(profile.Experiences))
	excluded := make([]models.OffEntityRow, 0)
	lookups := 0
	for _, row := range profile.Experiences {
		if sharesPinned(row.Company) {
			kept = append(kept, row)
			continue
		}
		// A RocketReach career-history row is a provider record, not a model guess. It is kept
		// even when the employer is not the confirmed one, but flagged as unverified: dropping
		// it was erasing exactly the history the run paid RocketReach to retrieve.
		if strings.EqualFold(strings.TrimSpace(row.Provider), models.ProviderRocketReach) {
			row.Flag = appendNote(row.Flag, "RocketReach career-history row; not independently verified by web evidence")
			kept = append(kept, row)
			continue
		}
		// Either this person's career history or another person's role: one bounded,
		// snippet-only reverse lookup decides which.
		if pa.engine.ValidationEnabled() && lookups < pa.engine.ValidationLookups() && hasBudgetForValidation(ctx) {
			lookups++
			if pa.rescueExperienceRow(ctx, gate.name, row) {
				kept = append(kept, row)
				continue
			}
		}
		employer := firstNonEmpty(strings.TrimSpace(row.Company), "an unnamed employer")
		excluded = append(excluded, models.OffEntityRow{
			Name:   strings.TrimSpace(strings.TrimSpace(row.Role) + " — " + employer),
			Source: strings.TrimSpace(row.Source),
			Reason: fmt.Sprintf("synthesised row dropped by QC: %q is not the confirmed employer and no reverse lookup ties it to this person", employer),
		})
	}
	profile.Experiences = kept

	// Timeline consistency: two roles at different employers claiming the same years is either
	// genuine concurrency or the blend QC exists to catch — flagged, never dropped, because
	// only the operator and the cited sources can tell those apart.
	flagOverlappingTimelines(profile.Experiences)

	// The dossier's headline employer is the operator's confirmed one: a synthesised value
	// that names someone else's company is corrected, not argued with.
	switch current := strings.TrimSpace(profile.CurrentCompany); {
	case current == "":
		profile.CurrentCompany = gate.company
	case !sharesPinned(current):
		excluded = append(excluded, models.OffEntityRow{
			Name:   "current employer stated as " + current,
			Reason: fmt.Sprintf("synthesised dossier named %q as the current employer; corrected to the confirmed %q", current, gate.company),
		})
		profile.CurrentCompany = gate.company
	}
	return excluded, lookups
}

// dedupeExperienceRows keeps one row per role-company-period combination, in first-seen
// order: several snippets saying the same thing about one role are one fact, not ten.
func dedupeExperienceRows(rows []models.Experience) []models.Experience {
	seen := make(map[string]bool, len(rows))
	kept := make([]models.Experience, 0, len(rows))
	for _, row := range rows {
		key := strings.ToLower(strings.Join([]string{
			strings.TrimSpace(row.Role), strings.TrimSpace(row.Company), strings.TrimSpace(row.Period),
		}, "|"))
		if seen[key] {
			continue
		}
		seen[key] = true
		kept = append(kept, row)
	}
	return kept
}

// timelineOverlapYears is how much two roles at different employers may share before QC flags
// them: one shared year is a normal handover ("2015-2019" then "2019-present"), two or more is
// either genuine concurrency or a blended row from a same-named person. The flag says which,
// because only the operator and the cited sources can tell those apart.
const timelineOverlapYears = 2

// parsePeriodYears reads a synthesised period string into a year range. Model-written periods
// are free text — "2019-present", "Jan 2015 – Dec 2019", "2010-2018", "Present" — so the
// parser takes every four-digit year it finds, treats an open-ended phrase as running to the
// current year, and swaps a reversed range. ok is false when no year can be read, in which
// case the row cannot take part in the overlap check.
func parsePeriodYears(period string) (start, end int, ok bool) {
	text := strings.ToLower(period)
	if strings.TrimSpace(text) == "" {
		return 0, 0, false
	}
	// Whole-word tests, not substring: "unknown" contains "now", and a row whose period is
	// "Unknown" is not open-ended.
	openEnded := containsWholeWord(text, "present") || containsWholeWord(text, "current") ||
		containsWholeWord(text, "now") || containsWholeWord(text, "onwards") ||
		containsWholeWord(text, "since") || strings.Contains(text, "till date") ||
		strings.Contains(text, "to date")
	var years []int
	for _, token := range strings.FieldsFunc(text, func(r rune) bool {
		return r < '0' || r > '9'
	}) {
		if len(token) != 4 {
			continue
		}
		value, err := strconv.Atoi(token)
		if err != nil || value < 1900 || value > time.Now().Year()+1 {
			continue
		}
		years = append(years, value)
	}
	if len(years) == 0 {
		// An open-ended period with no year ("Present", "till date") still claims to be
		// current: it overlaps anything that includes the current year, which is all the
		// overlap check can honestly say about it.
		if openEnded {
			return time.Now().Year(), time.Now().Year(), true
		}
		return 0, 0, false
	}
	start, end = years[0], years[len(years)-1]
	if start > end {
		start, end = end, start
	}
	if openEnded {
		end = time.Now().Year()
		if start > end {
			start = end
		}
	}
	return start, end, true
}

// flagOverlappingTimelines marks every kept row whose timeline overlaps another row at a
// *different* employer by more than a handover year. Two roles at one employer overlap all the
// time — a board seat alongside an executive one — so they are left alone; the blend QC exists
// to catch is two careers merged into one dossier, and that is what the flag names. It never
// drops a row: concurrency is legitimate, and the flag is what turns the ambiguity into
// something the operator can resolve against the cited sources.
func flagOverlappingTimelines(rows []models.Experience) int {
	type span struct{ start, end int }
	spans := make([]span, len(rows))
	for index, row := range rows {
		start, end, ok := parsePeriodYears(row.Period)
		if !ok {
			spans[index] = span{0, 0}
			continue
		}
		spans[index] = span{start, end}
	}
	sharesEmployer := func(a, b models.Experience) bool {
		aTokens := nameTokens(a.Company)
		if len(aTokens) == 0 || len(nameTokens(b.Company)) == 0 {
			return false
		}
		for _, token := range nameTokens(b.Company) {
			for _, pin := range aTokens {
				if token == pin {
					return true
				}
			}
		}
		return false
	}
	pairs := 0
	for i := 0; i < len(rows); i++ {
		if spans[i].start == 0 {
			continue
		}
		for j := i + 1; j < len(rows); j++ {
			if spans[j].start == 0 || sharesEmployer(rows[i], rows[j]) {
				continue
			}
			overlapEnd := spans[i].end
			if spans[j].end < overlapEnd {
				overlapEnd = spans[j].end
			}
			overlapStart := spans[i].start
			if spans[j].start > overlapStart {
				overlapStart = spans[j].start
			}
			overlap := overlapEnd - overlapStart + 1
			if overlap < timelineOverlapYears {
				continue
			}
			flag := func(other models.Experience) string {
				return fmt.Sprintf("timeline overlaps %q at %q (%s) by %d year(s) — concurrent roles or a blended row; verify against the cited sources",
					firstNonEmpty(strings.TrimSpace(other.Role), "another role"),
					firstNonEmpty(strings.TrimSpace(other.Company), "an unnamed employer"),
					other.Period, overlap)
			}
			if rows[i].Flag != "" {
				rows[i].Flag += "; " + flag(rows[j])
			} else {
				rows[i].Flag = flag(rows[j])
			}
			if rows[j].Flag != "" {
				rows[j].Flag += "; " + flag(rows[i])
			} else {
				rows[j].Flag = flag(rows[i])
			}
			pairs++
		}
	}
	return pairs
}

// countFlaggedRows reports how many surviving rows carry a QC flag, for the run's event log.
func countFlaggedRows(rows []models.Experience) int {
	flagged := 0
	for _, row := range rows {
		if strings.TrimSpace(row.Flag) != "" {
			flagged++
		}
	}
	return flagged
}

// rescueExperienceRow spends one reverse search tying the name to the row's employer in a
// snippet. It can add nothing to the dossier: it only decides whether a row the model already
// wrote survives. A lookup that ties the two is how a genuine past employer is told apart
// from another person's current role.
func (pa *PersonAgent) rescueExperienceRow(ctx context.Context, personName string, row models.Experience) bool {
	company := strings.TrimSpace(row.Company)
	if company == "" {
		return false
	}
	results, err := pa.engine.SearchCached(ctx, fmt.Sprintf("%q %q", personName, company), 5)
	if err != nil {
		return false
	}
	tokens := nameTokens(personName)
	for _, result := range results {
		haystack := strings.ToLower(result.Title + " " + result.Snippet)
		if containsAllTokens(haystack, tokens) && mentionsCompany(haystack, company) {
			return true
		}
	}
	return false
}
