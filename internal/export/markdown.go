package export

import (
	"fmt"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// renderCompanyMarkdown renders an org chart as Markdown. The output is
// self-contained: a reader who has never seen the console can follow the
// hierarchy, the departments, and where each claim came from.
func renderCompanyMarkdown(chart *models.OrgChart) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s — Company Org Chart\n\n", nonEmpty(chart.CompanyName, "Company"))
	fmt.Fprintf(&builder, "_%s · generated %s_\n\n", BrandName, generatedStamp(chart.LastUpdated))

	builder.WriteString("| Field | Value |\n| :--- | :--- |\n")
	builder.WriteString(markdownRow("Domain", chart.Domain))
	builder.WriteString(markdownRow("Headquarters", chart.Headquarters))
	builder.WriteString(markdownRow("Leadership listed", fmt.Sprintf("%d", len(chart.Leadership))))
	builder.WriteString(markdownRow("Departments", fmt.Sprintf("%d", len(chart.Departments))))
	builder.WriteString(markdownRow("Sources used", fmt.Sprintf("%d", len(chart.Sources))))
	if gate := chart.EntityGate; gate != nil {
		confirmation := "not confirmed"
		if gate.Confirmed {
			confirmation = "confirmed by the operator"
		}
		builder.WriteString(markdownRow("Entity", strings.Join(gate.Keys, ", ")+" ("+confirmation+")"))
	}
	builder.WriteString("\n")

	if overview := trimmed(chart.Overview); overview != "" {
		builder.WriteString("## Overview\n\n")
		builder.WriteString(overview)
		builder.WriteString("\n\n")
	}
	if chart.Brief != nil {
		builder.WriteString("## Company Brief\n\n")
		if description := trimmed(chart.Brief.BusinessDescription); description != "" {
			fmt.Fprintf(&builder, "**Business description:** %s\n\n", description)
		}
		if len(chart.Brief.KeyProducts) > 0 {
			builder.WriteString("**Key products:** ")
			builder.WriteString(strings.Join(chart.Brief.KeyProducts, ", "))
			builder.WriteString("\n\n")
		}
		if len(chart.Brief.KeyLocations) > 0 {
			builder.WriteString("**Key locations:** ")
			locations := make([]string, 0, len(chart.Brief.KeyLocations))
			for _, location := range chart.Brief.KeyLocations {
				locations = append(locations, strings.Trim(strings.Join([]string{location.City, location.Country}, ", "), ", "))
			}
			builder.WriteString(strings.Join(locations, "; "))
			builder.WriteString("\n\n")
		}
	}

	if len(chart.Leadership) > 0 {
		builder.WriteString("## Executive Leadership\n\n")
		builder.WriteString("| Name | Title | Location | As of | Public source |\n| :--- | :--- | :--- | :--- | :--- |\n")
		for _, person := range chart.Leadership {
			fmt.Fprintf(&builder, "| %s | %s | %s | %s | %s |\n",
				markdownCell(person.Name), markdownCell(person.Title),
				markdownCell(person.Location), markdownCell(combineNotes(person.Freshness(), person.EntityWarning())),
				markdownCell(person.PublicSource))
		}
		builder.WriteString("\n")
	}

	// Departments the run found nobody for are collapsed into a single line: fifteen
	// sections reading "no named contacts" make a thin result look busy, and the reader
	// needs to see that the run found two people, not fifteen departments.
	empty := make([]string, 0, len(chart.Departments))
	if len(chart.Departments) > 0 {
		builder.WriteString("## Departments & Functions\n\n")
		for _, department := range chart.Departments {
			if len(department.Members) == 0 {
				empty = append(empty, nonEmpty(trimmed(department.Name), "unnamed function"))
				continue
			}
			fmt.Fprintf(&builder, "### %s\n\n", nonEmpty(department.Name, "Unnamed function"))
			if head := trimmed(department.Head); head != "" {
				fmt.Fprintf(&builder, "**Head:** %s\n\n", head)
			}
			if description := trimmed(department.Description); description != "" {
				fmt.Fprintf(&builder, "%s\n\n", description)
			}
			for _, member := range department.Members {
				fmt.Fprintf(&builder, "- **%s**", nonEmpty(member.Name, "Unnamed"))
				if title := trimmed(member.Title); title != "" {
					fmt.Fprintf(&builder, " — %s", title)
				}
				if location := trimmed(member.Location); location != "" {
					fmt.Fprintf(&builder, " (%s)", location)
				}
				if source := trimmed(member.PublicSource); source != "" {
					fmt.Fprintf(&builder, " · [source](%s)", source)
				}
				if stamp := combineNotes(member.FreshnessNote(), member.EntityWarning()); stamp != "" {
					fmt.Fprintf(&builder, " · _%s_", stamp)
				}
				builder.WriteString("\n")
			}
			builder.WriteString("\n")
		}
	}

	if len(empty) > 0 {
		fmt.Fprintf(&builder, "_No named contacts found for: %s._\n\n", strings.Join(empty, ", "))
	}

	if len(chart.Locations) > 0 {
		builder.WriteString("## Locations\n\n")
		builder.WriteString("| City | Country | Address | Est. headcount |\n| :--- | :--- | :--- | :--- |\n")
		for _, location := range chart.Locations {
			fmt.Fprintf(&builder, "| %s | %s | %s | %s |\n",
				markdownCell(location.City), markdownCell(location.Country),
				markdownCell(location.Address), markdownCell(location.HeadcountEst))
		}
		builder.WriteString("\n")
	}

	builder.WriteString(renderEntityGateMarkdown(chart.EntityGate))
	builder.WriteString(renderExcludedResultsMarkdown(chart.SearchTrace))
	builder.WriteString(renderValidationMarkdown(chart.Validation))
	builder.WriteString(renderTimingsMarkdown(chart.Timings))
	builder.WriteString(renderSourcesMarkdown(chart.Sources))
	return strings.TrimRight(builder.String(), "\n") + "\n"
}

// renderEntityGateMarkdown reports what the confirmed company identity filtered, and
// lists the people it excluded. The list is the point: a short roster has to be
// explainable, and an exclusion that turns out to be wrong has to be visible.
func renderEntityGateMarkdown(gate *models.EntityGate) string {
	if gate == nil {
		return ""
	}
	if len(gate.Keys) == 0 && gate.RowsOffEntity == 0 && gate.CandidatesTagged+gate.CandidatesNone == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("## Entity gate\n\n")
	confirmation := "not confirmed — treat the identity as the operator's description"
	if gate.Confirmed {
		confirmation = "confirmed by the operator"
	}
	fmt.Fprintf(&builder, "A source had to name the company to be used: **%s** (%s).\n\n",
		strings.Join(gate.Keys, ", "), confirmation)
	builder.WriteString("| What the gate did | Count |\n| :--- | :--- |\n")
	fmt.Fprintf(&builder, "| Planned queries dropped for not naming the company | %d |\n", gate.QueriesDropped)
	fmt.Fprintf(&builder, "| Candidates that named the company | %d |\n", gate.CandidatesTagged)
	fmt.Fprintf(&builder, "| Candidates that named nothing | %d |\n", gate.CandidatesNone)
	fmt.Fprintf(&builder, "| Search snippets dropped before synthesis | %d |\n", gate.SnippetsDropped)
	fmt.Fprintf(&builder, "| People excluded from the roster | %d |\n", gate.RowsOffEntity)
	fmt.Fprintf(&builder, "| People kept because a reverse lookup tied them to the company | %d |\n", gate.RescuedByLookup)
	if gate.GapLookups > 0 {
		fmt.Fprintf(&builder, "| Company-anchored lookups run before synthesis (areas the evidence did not cover) | %d, adding %d source(s) |\n", gate.GapLookups, gate.GapEvidence)
	}
	if gate.LookupsDeferred > 0 {
		fmt.Fprintf(&builder, "| People left unchecked by the lookup pass | %d |\n", gate.LookupsDeferred)
	}

	if mode := trimmed(gate.Mode); mode != "" {
		fmt.Fprintf(&builder, "| Gate mode | %s |\n", mode)
	}
	builder.WriteString("\n")

	if len(gate.OffEntity) > 0 {
		builder.WriteString("### Unverified / other entities\n\n")
		builder.WriteString("These people were found by the searches, but no source connecting them to the company was found")
		if gate.Mode == "report" {
			builder.WriteString(" — the gate is in report mode, so they are still listed in the chart above, labelled")
		}
		builder.WriteString(":\n\n")
		builder.WriteString("| Name | Title | Where it appeared | Source | Why excluded |\n| :--- | :--- | :--- | :--- | :--- |\n")
		for _, row := range gate.OffEntity {
			fmt.Fprintf(&builder, "| %s | %s | %s | %s | %s |\n",
				markdownCell(row.Name), markdownCell(row.Title), markdownCell(row.Place),
				markdownCell(row.Source), markdownCell(row.Reason))
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

// excludedResultsPerQuery caps how many excluded hits one query lists in the
// report appendix; the export keeps the report concise.
const excludedResultsPerQuery = 8

// renderExcludedResultsMarkdown lists the search hits the identity gate excluded,
// per query. The gate exists to keep other companies' pages out of the roster,
// but its name match can miss a spelling variant — an operating name typed
// differently from the legal name ("Goyum Screwpress" vs "Goyum Screw Press") —
// so the exclusions are presented for review instead of vanishing: a reader who
// recognizes the company's own page here knows the variant needs an alias, and
// the report appendix carries the selected evidence.
func renderExcludedResultsMarkdown(trace *models.SearchTrace) string {
	if trace == nil || trace.ExcludedCount() == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("## Excluded search results\n\n")
	builder.WriteString("These search hits were returned but not used: the identity gate kept only sources that name the company, and these did not name it the way the gate spells it. A legal or operating name spelled differently (or run together) is the usual cause — skim the list before trusting the roster, and add the variant as an alias if one of these is the company's own page.\n\n")
	for _, query := range trace.Queries {
		if len(query.Excluded) == 0 {
			continue
		}
		fmt.Fprintf(&builder, "### %s\n\n", markdownCell(query.Query))
		builder.WriteString("| Result | Link |\n| :--- | :--- |\n")
		for index, hit := range query.Excluded {
			if index == excludedResultsPerQuery {
				fmt.Fprintf(&builder, "| …and %d more (additional results omitted) |  |\n", len(query.Excluded)-excludedResultsPerQuery)
				break
			}
			fmt.Fprintf(&builder, "| %s | %s |\n", markdownCell(hit.Title), markdownCell(hit.Link))
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

// renderValidationMarkdown reports what the freshness pass found: how many rows
// sit behind a recent source, which titles are claimed twice, and what the
// bounded reverse lookups changed. It is what lets a reader see that a "current"
// org chart is really a dated one.
func renderValidationMarkdown(report *models.ValidationReport) string {
	if report == nil || report.RowsChecked == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("## Data freshness\n\n")
	builder.WriteString("| Verdict | Rows |\n| :--- | :--- |\n")
	verdicts := []struct {
		label string
		count int
	}{
		{models.VerificationRecent, report.RowsRecent},
		{models.VerificationDated, report.RowsDated},
		{models.VerificationStale, report.RowsStale},
		{models.VerificationUndated, report.RowsUndated},
	}
	if report.RowsUnverified > 0 {
		// Shown, not removed: these rows stay in the chart carrying the remark.
		verdicts = append(verdicts, struct {
			label string
			count int
		}{models.VerificationCurrentUnverified + " (kept, unable to verify)", report.RowsUnverified})
	}
	for _, entry := range verdicts {
		fmt.Fprintf(&builder, "| %s | %d |\n", entry.label, entry.count)
	}
	builder.WriteString("\n_Verdicts come from the newest dated source behind each row: verified-recent means a source inside the recent window from the company's own site or from two independent domains, and stale means the newest source is older than the stale window. A dated row is a statement about the evidence, not proof of current employment._\n\n")

	for _, conflict := range report.Conflicts {
		fmt.Fprintf(&builder, "- **Conflicting title:** %s\n", conflict)
	}
	if len(report.Conflicts) > 0 {
		builder.WriteString("\n")
	}
	if len(report.ExcludedRows) > 0 {
		builder.WriteString("### Excluded from the current org chart\n\n")
		builder.WriteString("These rows were removed by current-employment checks and remain listed for auditability.\n\n")
		builder.WriteString("| Name | Title | Section | Source | Reason |\n| :--- | :--- | :--- | :--- | :--- |\n")
		for _, row := range report.ExcludedRows {
			fmt.Fprintf(&builder, "| %s | %s | %s | %s | %s |\n",
				markdownCell(row.Name), markdownCell(row.Title), markdownCell(row.Place),
				markdownCell(row.Source), markdownCell(row.Reason))
		}
		builder.WriteString("\n")
	}

	if len(report.Lookups) > 0 {
		builder.WriteString("| Person | Reverse lookup | Outcome | Newest mention |\n| :--- | :--- | :--- | :--- |\n")
		for _, lookup := range report.Lookups {
			mention := trimmed(lookup.AsOf)
			if mention == "" {
				mention = "—"
			}
			// The source URL is the provenance of the date: without it the
			// reader cannot judge whether the mention really is about this
			// person at this company.
			if source := trimmed(lookup.Source); source != "" {
				mention += fmt.Sprintf(" · <%s>", source)
			}
			fmt.Fprintf(&builder, "| %s | %s | %s | %s |\n",
				markdownCell(lookup.Person), markdownCell(lookup.Query),
				markdownCell(lookup.Status), markdownCell(mention))
		}
		builder.WriteString("\n")
	}
	if message := trimmed(report.Skipped); message != "" {
		fmt.Fprintf(&builder, "_Reverse lookups: %s._\n\n", message)
	}
	return builder.String()
}

// renderTimingsMarkdown renders the run's phase timings, which are what make a slow
// run diagnosable after the fact.
func renderTimingsMarkdown(timings []models.PhaseTiming) string {
	if len(timings) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("## Run timings\n\n")
	builder.WriteString("| Phase | Duration (s) | Detail |\n| :--- | :--- | :--- |\n")
	for _, timing := range timings {
		fmt.Fprintf(&builder, "| %s | %s | %s |\n",
			markdownCell(timing.Phase), timing.Seconds(), markdownCell(timing.Detail))
	}
	builder.WriteString("\n")
	return builder.String()
}

// generatedStamp formats the document footer timestamp.
func generatedStamp(updated time.Time) string {
	if updated.IsZero() {
		updated = time.Now()
	}
	return updated.UTC().Format("2006-01-02 15:04 UTC")
}

// renderLegalMarkdown renders a legal check as Markdown. A reader who has never
// seen the console can follow what was searched, what matched the confirmed
// identity, what only shares its name, and what each source actually answered.
func renderLegalMarkdown(report *models.LegalReport) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s — Legal Check\n\n", nonEmpty(report.CompanyName, "Company"))
	fmt.Fprintf(&builder, "_%s · generated %s_\n\n", BrandName, generatedStamp(report.LastUpdated))

	identity := "not confirmed"
	if report.EntityConfirmed {
		identity = "confirmed by the operator"
	}
	builder.WriteString("| Field | Value |\n| :--- | :--- |\n")
	builder.WriteString(markdownRow("Registration ID", report.RegistrationID))
	builder.WriteString(markdownRow("Domain", report.Domain))
	builder.WriteString(markdownRow("Aliases", strings.Join(report.Aliases, ", ")))
	builder.WriteString(markdownRow("Entity", identity))
	builder.WriteString(markdownRow("Findings", fmt.Sprintf("%d matched, %d same-named", len(report.Findings), len(report.SameNamed))))
	builder.WriteString("\n")

	if summary := trimmed(report.Summary); summary != "" {
		builder.WriteString("## Exposure summary\n\n")
		builder.WriteString(summary)
		builder.WriteString("\n\n")
	}

	if risk := report.Risk; risk != nil {
		builder.WriteString("## Legal risk\n\n")
		fmt.Fprintf(&builder, "**Level: %s**", strings.ToUpper(nonEmpty(risk.Level, "unknown")))
		if !risk.Classified {
			builder.WriteString(" _(count-derived fallback — the model was unavailable)_")
		}
		builder.WriteString("\n\n")
		if narrative := trimmed(risk.Summary); narrative != "" {
			builder.WriteString(narrative)
			builder.WriteString("\n\n")
		}
		for _, driver := range risk.Drivers {
			fmt.Fprintf(&builder, "- %s\n", markdownCell(driver))
		}
		builder.WriteString("\n")
	}

	if len(report.Sources) > 0 {
		builder.WriteString("## Sources\n\n")
		for _, source := range report.Sources {
			if source.Unavailable != "" {
				fmt.Fprintf(&builder, "- **%s** — unavailable: %s\n", source.Name, source.Unavailable)
				continue
			}
			fmt.Fprintf(&builder, "- **%s** — %d kept of %d hits in %d search(es)\n", source.Name, source.Kept, source.Hits, source.Queries)
		}
		builder.WriteString("\n")
	}

	if len(report.Dockets) > 0 {
		builder.WriteString("## Matter dockets\n\n")
		for _, docket := range report.Dockets {
			fmt.Fprintf(&builder, "### %s\n\n", docket.Name)
			counts := fmt.Sprintf("%d matter(s)", len(docket.Matters))
			if docket.Pending > 0 {
				counts += fmt.Sprintf(", %d pending", docket.Pending)
			}
			if docket.Defendant > 0 {
				counts += fmt.Sprintf(", %d with the company as respondent", docket.Defendant)
			}
			fmt.Fprintf(&builder, "_%s._\n\n", counts)
			for _, finding := range docket.Matters {
				fmt.Fprintf(&builder, "- **%s**", markdownCell(finding.CaseTitle))
				if finding.Court != "" {
					fmt.Fprintf(&builder, " — %s", markdownCell(finding.Court))
				}
				if finding.Date != "" {
					fmt.Fprintf(&builder, " (%s)", markdownCell(finding.Date))
				}
				if finding.Role != "" {
					fmt.Fprintf(&builder, " — %s", markdownCell(finding.Role))
				}
				builder.WriteString("\n")
				if finding.Snippet != "" {
					fmt.Fprintf(&builder, "  - %s\n", markdownCell(finding.Snippet))
				}
			}
			builder.WriteString("\n")
		}
	} else if len(report.Findings) > 0 {
		builder.WriteString("## Matched findings\n\n")
		builder.WriteString("| Case | Court | Date | Side | Category | Status | Records |\n| :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")
		for _, finding := range report.Findings {
			side := ""
			switch finding.Role {
			case models.LegalRoleInitiated:
				side = "company initiated"
			case models.LegalRoleFacing:
				side = "company responding"
			}
			records := "1"
			if finding.Iterations > 1 {
				records = fmt.Sprintf("%d (latest kept)", finding.Iterations)
			}
			fmt.Fprintf(&builder, "| %s | %s | %s | %s | %s | %s | %s |\n",
				markdownCell(finding.CaseTitle), markdownCell(finding.Court), markdownCell(finding.Date),
				markdownCell(side), markdownCell(finding.Category), markdownCell(finding.Status), records)
			if snippet := trimmed(finding.Snippet); snippet != "" {
				fmt.Fprintf(&builder, "\n> %s\n\n", markdownCell(snippet))
				if finding.URL != "" {
					fmt.Fprintf(&builder, "> [case record](%s)\n\n", finding.URL)
				}
			}
		}
	} else {
		builder.WriteString("## Matched findings\n\nNo case records matched the confirmed identity. Check the sources above: an empty list with an unavailable source means the search could not run, not that nothing exists.\n\n")
	}

	if len(report.SameNamed) > 0 {
		builder.WriteString("## Same-named entities — kept for review, not attributed\n\n")
		builder.WriteString("These records share part of the party name without a stronger identity key. They are listed rather than dropped, because a legal check must not silently discard a hit that merely shares a name.\n\n")
		for _, finding := range report.SameNamed {
			fmt.Fprintf(&builder, "- **%s**", markdownCell(finding.CaseTitle))
			if court := trimmed(finding.Court); court != "" {
				fmt.Fprintf(&builder, " (%s", court)
				if date := trimmed(finding.Date); date != "" {
					fmt.Fprintf(&builder, ", %s", date)
				}
				builder.WriteString(")")
			}
			if finding.URL != "" {
				fmt.Fprintf(&builder, " — [case record](%s)", finding.URL)
			}
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}

	for _, person := range report.Persons {
		fmt.Fprintf(&builder, "## Person check — %s\n\n", nonEmpty(person.Name, "unnamed person"))
		if reason := trimmed(person.SkippedReason); reason != "" {
			fmt.Fprintf(&builder, "_Not searched: %s_\n\n", reason)
			continue
		}
		if len(person.Findings) == 0 {
			builder.WriteString("No case records found.\n\n")
			continue
		}
		builder.WriteString("| Case | Court | Date | Category | Status |\n| :--- | :--- | :--- | :--- | :--- |\n")
		for _, finding := range person.Findings {
			fmt.Fprintf(&builder, "| %s | %s | %s | %s | %s |\n",
				markdownCell(finding.CaseTitle), markdownCell(finding.Court), markdownCell(finding.Date),
				markdownCell(finding.Category), markdownCell(finding.Status))
		}
		builder.WriteString("\n")
	}

	if len(report.Timings) > 0 {
		builder.WriteString(renderTimingsMarkdown(report.Timings))
		builder.WriteString("\n")
	}
	return builder.String()
}

// renderPersonMarkdown renders a dossier as Markdown. The output is
// self-contained: a reader can follow the career, the education and where every
// claim came from.
func renderPersonMarkdown(profile *models.PersonProfile) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s — Professional Dossier\n\n", nonEmpty(profile.FullName, "Person"))
	fmt.Fprintf(&builder, "_%s · generated %s_\n\n", BrandName, generatedStamp(profile.LastUpdated))

	headline := strings.Trim(strings.Join([]string{
		trimmed(profile.CurrentTitle),
		trimmed(profile.CurrentCompany),
		trimmed(profile.Location),
	}, " · "), " ·")
	if headline != "" {
		fmt.Fprintf(&builder, "**%s**\n\n", headline)
	}
	fmt.Fprintf(&builder, "Confidence: %d%%\n\n", profile.ScorePercent())

	if summary := trimmed(profile.ExecutiveSummary); summary != "" {
		builder.WriteString("## Executive summary\n\n")
		builder.WriteString(summary)
		builder.WriteString("\n\n")
	}
	if contact := profile.ContactEnrichment; contact != nil {
		builder.WriteString("## Contact details\n\n")
		if contact.LinkedInURL != "" {
			fmt.Fprintf(&builder, "- LinkedIn profile (RocketReach): %s\n", markdownCell(contact.LinkedInURL))
		}
		for _, email := range contact.Emails {
			fmt.Fprintf(&builder, "- Email: %s", markdownCell(email.Email))
			if email.Type != "" {
				fmt.Fprintf(&builder, " (%s", markdownCell(email.Type))
				if email.Grade != "" {
					fmt.Fprintf(&builder, ", grade %s", markdownCell(email.Grade))
				}
				builder.WriteString(")")
			}
			builder.WriteString("\n")
		}
		for _, phone := range contact.Phones {
			fmt.Fprintf(&builder, "- Phone: %s", markdownCell(phone.Number))
			if phone.Type != "" {
				fmt.Fprintf(&builder, " (%s)", markdownCell(phone.Type))
			}
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}

	if len(profile.Experiences) > 0 {
		builder.WriteString("## Experience\n\n")
		for _, experience := range profile.Experiences {
			fmt.Fprintf(&builder, "### %s", nonEmpty(experience.Role, "Role"))
			if company := trimmed(experience.Company); company != "" {
				fmt.Fprintf(&builder, " — %s", company)
			}
			builder.WriteString("\n\n")
			details := strings.Trim(strings.Join([]string{
				trimmed(experience.Period),
				trimmed(experience.Location),
			}, " · "), " ·")
			if details != "" {
				fmt.Fprintf(&builder, "%s\n\n", details)
			}
			if source := trimmed(experience.Source); source != "" {
				fmt.Fprintf(&builder, "[source](%s)\n\n", source)
			}
			if flag := trimmed(experience.Flag); flag != "" {
				fmt.Fprintf(&builder, "> ⚠ %s\n\n", flag)
			}
			for _, highlight := range experience.Highlights {
				if highlight = trimmed(highlight); highlight != "" {
					fmt.Fprintf(&builder, "- %s\n", highlight)
				}
			}
			builder.WriteString("\n")
		}
	}

	if len(profile.Education) > 0 {
		builder.WriteString("## Education\n\n")
		builder.WriteString("| Institution | Degree | Field | Year |\n| :--- | :--- | :--- | :--- |\n")
		for _, education := range profile.Education {
			fmt.Fprintf(&builder, "| %s | %s | %s | %s |\n",
				markdownCell(education.Institution), markdownCell(education.Degree),
				markdownCell(education.Field), markdownCell(education.Year))
		}
		builder.WriteString("\n")
	}

	builder.WriteString(renderListMarkdown("Key skills", profile.KeySkills))
	builder.WriteString(renderListMarkdown("Notable projects", profile.NotableProjects))

	if len(profile.OnlinePresence) > 0 {
		builder.WriteString("## Online presence\n\n")
		builder.WriteString("| Platform | Link |\n| :--- | :--- |\n")
		for _, platform := range sortedKeys(profile.OnlinePresence) {
			fmt.Fprintf(&builder, "| %s | %s |\n", markdownCell(platform), markdownCell(profile.OnlinePresence[platform]))
		}
		builder.WriteString("\n")
	}

	builder.WriteString(renderPersonGateMarkdown(profile.PersonGate))
	builder.WriteString(renderSameNamedMarkdown(profile.SameNamed))
	builder.WriteString(renderPersonTraceMarkdown(profile))
	builder.WriteString(renderSourcesMarkdown(profile.Sources))
	return strings.TrimRight(builder.String(), "\n") + "\n"
}

// renderSameNamedMarkdown renders the multilateral view: the evidence for the other people who
// share the confirmed name, grouped by the employer each source states, plus the material that
// names the person but states no employer. The dossier stays about the confirmed identity;
// this section exists so the operator can judge the confirmation instead of trusting it.
func renderSameNamedMarkdown(groups []models.PersonEvidenceGroup) string {
	if len(groups) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("## Same-named people\n\n")
	builder.WriteString("The dossier above is the confirmed identity's. A person search answers with several people who share the name; these are the most relevant snippets for the others, grouped by the employer each source states.\n\n")
	for _, group := range groups {
		if group.Employer == "" {
			builder.WriteString("### No employer stated (cannot be attributed)\n\n")
		} else {
			fmt.Fprintf(&builder, "### %s\n\n", markdownCell(group.Employer))
		}
		for _, snippet := range group.Snippets {
			fmt.Fprintf(&builder, "- **%s** — %s ([source](%s))\n",
				markdownCell(snippet.Title), markdownCell(snippet.Snippet), snippet.Link)
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

// renderPersonGateMarkdown renders the identity check: what the dossier held its sources to
// and what it excluded, so a same-named person's absence is an explained fact rather than a
// silent omission.
func renderPersonGateMarkdown(gate *models.PersonGate) string {
	if gate == nil {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("## Identity check\n\n")
	fmt.Fprintf(&builder, "Every source was held to **%s**", markdownCell(gate.Name))
	if gate.Company != "" {
		fmt.Fprintf(&builder, " at **%s**", markdownCell(gate.Company))
	}
	fmt.Fprintf(&builder, " — %d kept", gate.Kept)
	if gate.NameOnly > 0 {
		fmt.Fprintf(&builder, ", %d naming the person but stating no employer", gate.NameOnly)
	}
	fmt.Fprintf(&builder, ", %d excluded.\n\n", gate.Dropped)
	if len(gate.Excluded) == 0 {
		builder.WriteString("Nothing was excluded: every collected source named the person and did not contradict the confirmed employer.\n\n")
		return builder.String()
	}
	builder.WriteString("| Excluded source | Read as | Why it was dropped |\n| :--- | :--- | :--- |\n")
	for _, row := range gate.Excluded {
		fmt.Fprintf(&builder, "| %s | %s | %s |\n", markdownCell(row.Name), markdownCell(row.Title), markdownCell(row.Reason))
	}
	builder.WriteString("\n")
	return builder.String()
}

// renderPersonTraceMarkdown renders the operating results a person run collected — the queries
// it issued, where the time went, and every fetch attempt with its outcome — the counterpart
// of the scrape report and timings the company chart carries.
func renderPersonTraceMarkdown(profile *models.PersonProfile) string {
	if len(profile.SearchQueries) == 0 && len(profile.Timings) == 0 && len(profile.ScrapeReport) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("## Research trace\n\n")
	if len(profile.SearchQueries) > 0 {
		builder.WriteString("Queries issued, in order:\n\n")
		for _, query := range profile.SearchQueries {
			fmt.Fprintf(&builder, "- `%s`\n", query)
		}
		builder.WriteString("\n")
	}
	if len(profile.Timings) > 0 {
		builder.WriteString("| Phase | Time | Detail |\n| :--- | :--- | :--- |\n")
		for _, timing := range profile.Timings {
			fmt.Fprintf(&builder, "| %s | %ss | %s |\n", markdownCell(timing.Phase), timing.Seconds(), markdownCell(timing.Detail))
		}
		builder.WriteString("\n")
	}
	if len(profile.ScrapeReport) > 0 {
		builder.WriteString("| Page fetched | Kind | Outcome | Detail |\n| :--- | :--- | :--- | :--- |\n")
		for _, entry := range profile.ScrapeReport {
			fmt.Fprintf(&builder, "| %s | %s | %s | %s |\n",
				markdownCell(entry.URL), markdownCell(entry.SourceType),
				markdownCell(entry.Status), markdownCell(entry.Reason))
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

// combineNotes joins non-empty notes with a middot, so a cell can carry a freshness
// verdict and an entity warning without either crowding the other out.
func combineNotes(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, " · ")
}

// renderSourcesMarkdown renders the evidence list.
func renderSourcesMarkdown(sources []string) string {
	if len(sources) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("## Sources\n\n")
	for index, source := range sources {
		fmt.Fprintf(&builder, "%d. <%s>\n", index+1, trimmed(source))
	}
	builder.WriteString("\n")
	return builder.String()
}

// renderListMarkdown renders a simple bulleted section, skipping it when empty.
func renderListMarkdown(title string, values []string) string {
	entries := make([]string, 0, len(values))
	for _, value := range values {
		if value = trimmed(value); value != "" {
			entries = append(entries, value)
		}
	}
	if len(entries) == 0 {
		return ""
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "## %s\n\n", title)
	for _, entry := range entries {
		fmt.Fprintf(&builder, "- %s\n", entry)
	}
	builder.WriteString("\n")
	return builder.String()
}

// markdownRow renders one row of a two-column table, omitting blank values.
func markdownRow(label, value string) string {
	if value = trimmed(value); value == "" {
		return ""
	}
	return fmt.Sprintf("| %s | %s |\n", label, markdownCell(value))
}

// markdownCell makes a value safe inside a Markdown table cell: a pipe would end
// the cell and a newline would end the row.
func markdownCell(value string) string {
	replaced := strings.NewReplacer("|", "\\|", "\r", " ", "\n", " ", "\t", " ").Replace(trimmed(value))
	return strings.Join(strings.Fields(replaced), " ")
}
