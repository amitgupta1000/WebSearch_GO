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
	fmt.Fprintf(&b, "\n## Method summary\n\n%d raw hits; %d leads reviewed; %d entities extracted; %d resolved; %d pages enriched; %d category fallback queries.\n\n## Appendix - sources and validation evidence\n\n", report.SearchHits, report.LeadsReviewed, report.LeadsExtracted, report.LeadsResolved, report.PagesEnriched, report.FallbackQueries)
	for _, evidence := range report.Appendix {
		fmt.Fprintf(&b, "- [%s](%s) - %s; %s; %s\n", evidence.Title, evidence.URL, evidence.SourceTier, evidence.ClaimType, evidence.Status)
	}
	return b.String()
}
