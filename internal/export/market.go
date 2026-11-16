package export

import (
	"fmt"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// renderMarketLandscapeMarkdown renders the producer/seller report, including the validated
// first-pass category and the deterministic coverage check.
func renderMarketLandscapeMarkdown(report *models.MarketLandscapeReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s - Seller Landscape\n\n%s\n\n", report.Activity, report.Objective)
	if category := strings.TrimSpace(report.ProductCategory); category != "" {
		fmt.Fprintf(&b, "Category: %s", category)
		if rationale := strings.TrimSpace(report.CategoryRationale); rationale != "" {
			fmt.Fprintf(&b, " - %s", rationale)
		}
		b.WriteString("\n\n")
	}
	b.WriteString("## Results\n\n")
	if len(report.Results) == 0 {
		b.WriteString("No verified companies were promoted.\n")
	} else if len(report.Groups) > 0 {
		for _, group := range report.Groups {
			fmt.Fprintf(&b, "### %s (%d)\n\n", group.Label, len(group.Companies))
			if group.Presence == models.MarketPresenceGlobal {
				b.WriteString("Global/cross-border footprint only; visible for audit and excluded from local coverage.\n\n")
			}
			b.WriteString("| Company | Role | Presence | Operating geography | HQ | Employees | Turnover | Description | Status | Confidence |\n|---|---|---|---|---|---|---|---|---|---|\n")
			for _, result := range group.Companies {
				operating := result.PresenceCountry
				if result.Presence == models.MarketPresenceGlobal || result.Presence == models.MarketPresenceUnknown {
					operating = "Global / cross-border"
				} else if operating == "" {
					operating = result.Country
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %.0f%% |\n",
					result.Name, result.Role, result.Presence, operating, result.HQCountry,
					result.Employees, result.Turnover, marketMarkdownCell(result.Description), result.Status, result.Confidence*100)
			}
			b.WriteString("\n")
		}
	} else {
		b.WriteString("| Company | Role | Country | Activity | Employees | Turnover | Description | Status | Confidence |\n|---|---|---|---|---|---|---|---|---|\n")
		for _, result := range report.Results {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %.0f%% |\n", result.Name, result.Role, result.Country, result.Activity, result.Employees, result.Turnover, marketMarkdownCell(result.Description), result.Status, result.Confidence*100)
		}
	}
	if report.Coverage != nil {
		status := "FAIL"
		if report.Coverage.Pass {
			status = "PASS"
		}
		fmt.Fprintf(&b, "\nCoverage: %d/%d in-scope companies (%s)", report.Coverage.Companies, report.Coverage.Minimum, status)
		if report.Coverage.Global > 0 {
			fmt.Fprintf(&b, "; %d global/cross-border companies excluded", report.Coverage.Global)
		}
		b.WriteString(".\n")
	}
	b.WriteString("\n## QC results\n\n")
	if strings.TrimSpace(report.QCPromptSource) != "" {
		fmt.Fprintf(&b, "QC prompt source: **%s**.\n\n", report.QCPromptSource)
	}
	if len(report.Reviews) == 0 {
		b.WriteString("No QC review was recorded for this run.\n")
	}
	for _, review := range report.Reviews {
		fmt.Fprintf(&b, "Verdict: **%s**; score %.2f; %d passed, %d failed.\n\n", strings.TrimSpace(review.Verdict), review.Score, review.Passed, review.Failed)
		if len(review.Verdicts) > 0 {
			b.WriteString("| Company | QC tag | Reason |\n|---|---|---|\n")
			for _, verdict := range review.Verdicts {
				fmt.Fprintf(&b, "| %s | qc %s | %s |\n", verdict.Name, verdict.Verdict, verdict.Reason)
			}
		} else {
			for _, rejected := range review.Rejected {
				fmt.Fprintf(&b, "| %s | qc failed | %s |\n", rejected.Name, rejected.Reason)
			}
		}
		b.WriteString("\n")
	}
	for _, review := range report.Reviews {
		fmt.Fprintf(&b, "\n### Quality review\n\nVerdict: **%s**; score %.2f.%s\n\n", strings.TrimSpace(review.Verdict), review.Score, func() string {
			if strings.TrimSpace(review.Reason) == "" {
				return ""
			}
			return " " + strings.TrimSpace(review.Reason)
		}())
		for _, rejected := range review.Rejected {
			fmt.Fprintf(&b, "- Rejected **%s** - %s\n", rejected.Name, rejected.Reason)
		}
		if len(review.Missing) > 0 {
			fmt.Fprintf(&b, "- Missing: %s\n", strings.Join(review.Missing, ", "))
		}
	}
	fmt.Fprintf(&b, "\n## Method summary\n\n%d raw hits; %d leads reviewed; %d entities extracted; %d resolved; %d pages enriched; %d category fallback queries.\n\n## Appendix - sources and validation evidence\n\n", report.SearchHits, report.LeadsReviewed, report.LeadsExtracted, report.LeadsResolved, report.PagesEnriched, report.FallbackQueries)
	for _, evidence := range report.Appendix {
		fmt.Fprintf(&b, "- [%s](%s) - %s; %s; %s\n", evidence.Title, evidence.URL, evidence.SourceTier, evidence.ClaimType, evidence.Status)
	}
	return b.String()
}

func marketMarkdownCell(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.TrimSpace(value)
}
