package agent

import (
	"context"
	"fmt"
	"strings"

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
