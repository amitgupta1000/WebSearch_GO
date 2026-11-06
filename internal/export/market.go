package export

import (
	"fmt"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

func renderMarketLandscapeMarkdown(report *models.MarketLandscapeReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — Producer/Seller Landscape\n\n%s\n\n## Results\n\n", report.Activity, report.Objective)
	if len(report.Results) == 0 {
		b.WriteString("No verified companies were promoted.\n")
	} else {
		b.WriteString("| Company | Country | Activity | Status | Confidence |\n|---|---|---|---|---|\n")
		for _, result := range report.Results {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %.0f%% |\n", result.Name, result.Country, result.Activity, result.Status, result.Confidence*100)
		}
	}
	fmt.Fprintf(&b, "\n## Method summary\n\n%d raw hits; %d leads reviewed; %d entities extracted; %d resolved; %d pages enriched.\n\n## Appendix — sources and validation evidence\n\n", report.SearchHits, report.LeadsReviewed, report.LeadsExtracted, report.LeadsResolved, report.PagesEnriched)
	for _, evidence := range report.Appendix {
		fmt.Fprintf(&b, "- [%s](%s) — %s; %s; %s\n", evidence.Title, evidence.URL, evidence.SourceTier, evidence.ClaimType, evidence.Status)
	}
	return b.String()
}
