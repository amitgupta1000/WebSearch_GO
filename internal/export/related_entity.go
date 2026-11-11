package export

import (
	"fmt"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

func relatedEntityReportTitle(report *models.RelatedEntityReport) string {
	return nonEmpty(report.CompanyName, "Related Entities") + " — Related Entities"
}

func renderRelatedEntityMarkdown(report *models.RelatedEntityReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n_%s · generated %s_\n\n", relatedEntityReportTitle(report), BrandName, generatedStamp(report.CompletedAt))
	fmt.Fprintf(&b, "**Company:** %s\n\n", report.CompanyName)
	if report.Domain != "" {
		fmt.Fprintf(&b, "**Domain:** %s\n\n", report.Domain)
	}
	writeRelatedCompaniesMarkdown(&b, "Direct relationships", report.DirectRelationships)
	writeRelatedCompaniesMarkdown(&b, "Leadership-derived relationships", report.LeadershipRelations)
	if len(report.Leadership) > 0 {
		b.WriteString("## Leadership\n\n")
		for _, leader := range report.Leadership {
			fmt.Fprintf(&b, "- **%s**", leader.Name)
			if leader.Role != "" {
				fmt.Fprintf(&b, " — %s", leader.Role)
			}
			if leader.Company != "" {
				fmt.Fprintf(&b, " at %s", leader.Company)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(report.Warnings) > 0 {
		writeStringList(&b, "Warnings", report.Warnings)
	}
	fmt.Fprintf(&b, "## Run metrics\n\n- Queries: %d planned, %d used\n- Search hits: %d\n",
		report.QueriesPlanned, report.QueriesUsed, report.SearchHits)
	return b.String()
}

func writeRelatedCompaniesMarkdown(b *strings.Builder, title string, companies []models.RelatedCompany) {
	if len(companies) == 0 {
		return
	}
	fmt.Fprintf(b, "## %s\n\n", title)
	for _, company := range companies {
		fmt.Fprintf(b, "- **%s** — %s", company.Name, company.Relationship)
		if company.Person != "" {
			fmt.Fprintf(b, " via %s", company.Person)
		}
		if company.Status != "" {
			fmt.Fprintf(b, " (%s)", company.Status)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func renderRelatedEntityHTML(report *models.RelatedEntityReport) string {
	return renderResearchHTML(
		relatedEntityReportTitle(report),
		nonEmpty(report.CompanyName, "Related entities"),
		renderRelatedEntityMarkdown(report),
	)
}
