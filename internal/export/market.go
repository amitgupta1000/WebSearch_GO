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
	fmt.Fprintf(&b, "# %s - Producer/Seller Landscape\n\n%s\n\n", report.Activity, report.Objective)
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
	} else {
		b.WriteString("| Company | Country | Activity | Status | Confidence |\n|---|---|---|---|---|\n")
		for _, result := range report.Results {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %.0f%% |\n", result.Name, result.Country, result.Activity, result.Status, result.Confidence*100)
		}
	}
	if report.Coverage != nil {
		status := "FAIL"
		if report.Coverage.Pass {
			status = "PASS"
		}
		fmt.Fprintf(&b, "\nCoverage: %d/%d companies (%s).\n", report.Coverage.Companies, report.Coverage.Minimum, status)
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
