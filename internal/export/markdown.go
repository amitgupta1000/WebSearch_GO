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
	fmt.Fprintf(&builder, "_Generated %s by WebSearch_GO_\n\n", generatedStamp(chart.LastUpdated))

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
	for _, entry := range []struct {
		label string
		count int
	}{
		{models.VerificationRecent, report.RowsRecent},
		{models.VerificationDated, report.RowsDated},
		{models.VerificationStale, report.RowsStale},
		{models.VerificationUndated, report.RowsUndated},
	} {
		fmt.Fprintf(&builder, "| %s | %d |\n", entry.label, entry.count)
	}
	builder.WriteString("\n_Verdicts come from the newest dated source behind each row: verified-recent means a source inside the recent window from the company's own site or from two independent domains, and stale means the newest source is older than the stale window. A dated row is a statement about the evidence, not proof of current employment._\n\n")

	for _, conflict := range report.Conflicts {
		fmt.Fprintf(&builder, "- **Conflicting title:** %s\n", conflict)
	}
	if len(report.Conflicts) > 0 {
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

// renderPersonMarkdown renders a dossier as Markdown.
func renderPersonMarkdown(profile *models.PersonProfile) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s — Professional Dossier\n\n", nonEmpty(profile.FullName, "Person"))
	fmt.Fprintf(&builder, "_Generated %s by WebSearch_GO_\n\n", generatedStamp(profile.LastUpdated))

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

	builder.WriteString(renderSourcesMarkdown(profile.Sources))
	return strings.TrimRight(builder.String(), "\n") + "\n"
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
