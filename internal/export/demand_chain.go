package export

import (
	"fmt"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// demandChainReportTitle is the document title, shared by the cover, the
// running footer and the stored file name.
func demandChainReportTitle(report *models.DemandChainReport) string {
	return nonEmpty(report.Activity, "Activity") + " — Demand Chain"
}

// demandStageDistanceLabel renders a stage's buyer-scope distance: how many
// transformation steps the consuming process sits from the product. Direct
// consumers of the product as an input are step 1; consumers of a step-1
// output are step 2; anything deeper is out of buyer scope.
func demandStageDistanceLabel(stage models.ValueChainStage) string {
	switch {
	case stage.Source == "category":
		return "channel category (resale)"
	case stage.Distance == models.StageDistanceDerived:
		return "2 (derived)"
	case stage.Distance > models.StageDistanceDerived:
		return fmt.Sprintf("%d (out of buyer scope)", stage.Distance)
	default:
		return "1 (direct)"
	}
}

// renderDemandChainMarkdown renders a buyer-side run as Markdown: the inferred
// value chain, the verified consumers, the honesty notes, and the appendix.
func renderDemandChainMarkdown(report *models.DemandChainReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — Demand Chain\n\n", nonEmpty(report.Activity, "Activity"))
	fmt.Fprintf(&b, "_%s · generated %s_\n\n", BrandName, generatedStamp(report.CompletedAt))
	if trimmed(report.Objective) != "" && trimmed(report.Objective) != trimmed(report.Activity) {
		fmt.Fprintf(&b, "%s\n\n", trimmed(report.Objective))
	}
	if geo := trimmed(report.Geography); geo != "" {
		fmt.Fprintf(&b, "Geography: %s\n\n", geo)
	}
	if category := trimmed(report.ProductCategory); category != "" {
		fmt.Fprintf(&b, "Category: %s", category)
		if source := trimmed(report.CategorySource); source != "" {
			fmt.Fprintf(&b, " (%s)", source)
		}
		if rationale := trimmed(report.CategoryRationale); rationale != "" {
			fmt.Fprintf(&b, " — %s", rationale)
		}
		b.WriteString("\n\n")
	}
	chainSource := "model-inferred"
	if !report.ChainInferred {
		if len(report.Chain) > 0 {
			chainSource = report.Chain[0].Source
		} else {
			chainSource = "none"
		}
	}
	b.WriteString("## Value chain\n\n")
	if len(report.Chain) == 0 {
		b.WriteString("No consuming stages were established.\n\n")
	} else {
		fmt.Fprintf(&b, "_%s_\n\n", chainSource)
		b.WriteString("| Stage | Steps from the product | Why it consumes the product | Product families | Operator vocabulary | Source |\n| :--- | :--- | :--- | :--- | :--- | :--- |\n")
		for _, stage := range report.Chain {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
				markdownCell(stage.Name), markdownCell(demandStageDistanceLabel(stage)),
				markdownCell(stage.Rationale),
				markdownCell(strings.Join(stage.Products, "; ")),
				markdownCell(strings.Join(stage.Operators, "; ")), markdownCell(stage.Source))
		}
		b.WriteString("\n")
	}
	if report.ChainReview != nil {
		fmt.Fprintf(&b, "## Adversarial chain review\n\nVerdict: **%s**; score %.2f.%s\n\n",
			markdownCell(report.ChainReview.Verdict), report.ChainReview.Score, func() string {
				if trimmed(report.ChainReview.Reason) == "" {
					return ""
				}
				return " " + trimmed(report.ChainReview.Reason)
			}())
		for _, rejection := range report.ChainReview.RejectedStages {
			fmt.Fprintf(&b, "- Rejected **%s** — %s\n", markdownCell(rejection.Name), markdownCell(rejection.Reason))
		}
		for _, correction := range report.ChainReview.DistanceCorrections {
			fmt.Fprintf(&b, "- Corrected **%s** to distance %d\n", markdownCell(correction.Name), correction.Distance)
		}
		if len(report.ChainReview.MissingStages) > 0 {
			fmt.Fprintf(&b, "- Suggested missing stages (not added automatically): %s\n", markdownCell(strings.Join(report.ChainReview.MissingStages, ", ")))
		}
		b.WriteString("\n")
	}
	b.WriteString("## Results — verified consumers\n\n")
	if len(report.Results) == 0 {
		b.WriteString("No company met the consumption-verification threshold.\n")
	} else {
		b.WriteString("| Company | Consuming stage | Country | Status | Confidence |\n| :--- | :--- | :--- | :--- | :--- |\n")
		for _, result := range report.Results {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %.0f%% |\n",
				markdownCell(result.Name), markdownCell(result.Stage),
				markdownCell(result.Country), markdownCell(result.Status), result.Confidence*100)
		}
	}
	b.WriteString("\n## Potential buyers by stage\n\n")
	b.WriteString("_Every company the consuming stage's lanes surfaced, verified or not — a company operating a stage that consumes the product is a potential buyer by construction, within the two-step buyer scope (direct consumers and one derived step). Status \"lead\" means the evidence did not reach the verification threshold; cross-check the verified list above._\n\n")
	if len(report.StageCandidates) == 0 {
		b.WriteString("No stage-active companies surfaced.\n\n")
	}
	for _, section := range report.StageCandidates {
		heading := section.Stage.Name
		if section.Stage.Source == "category" {
			heading += " — channel category (resale)"
		} else if section.Stage.Distance == models.StageDistanceDerived {
			heading += " — step 2 (derived)"
		}
		fmt.Fprintf(&b, "### %s\n\n", heading)
		if trimmed(section.Stage.Rationale) != "" {
			fmt.Fprintf(&b, "_%s_\n\n", trimmed(section.Stage.Rationale))
		}
		if len(section.Companies) == 0 {
			b.WriteString("_No stage-active companies surfaced._\n\n")
			continue
		}
		b.WriteString("| Company | Country | Status | Confidence |\n| :--- | :--- | :--- | :--- |\n")
		for _, company := range section.Companies {
			fmt.Fprintf(&b, "| %s | %s | %s | %.0f%% |\n",
				markdownCell(company.Name), markdownCell(company.Country),
				markdownCell(company.Status), company.Confidence*100)
		}
		b.WriteString("\n")
	}
	if len(report.StageCoverage) > 0 {
		b.WriteString("\n## Stage coverage — QC minimum\n\n")
		b.WriteString("_Every stage, plus the separate distribution category, must identify at least six companies. A gap is reported, never filled with invented names._\n\n")
		b.WriteString("| Stage | Companies | Minimum | Pass |\n| :--- | :--- | :--- | :--- |\n")
		for _, item := range report.StageCoverage {
			status := "FAIL"
			if item.Pass {
				status = "PASS"
			}
			fmt.Fprintf(&b, "| %s | %d | %d | %s |\n", markdownCell(item.Stage), item.Count, item.Minimum, status)
		}
		b.WriteString("\n")
	}
	if len(report.Reviews) > 0 {
		b.WriteString("## Quality review\n\n")
		for _, review := range report.Reviews {
			verdict := trimmed(review.Verdict)
			if verdict == "" {
				verdict = "unavailable"
			}
			fmt.Fprintf(&b, "- Verdict: **%s**; score %.2f.%s\n", markdownCell(verdict), review.Score, func() string {
				if trimmed(review.Reason) == "" {
					return ""
				}
				return " " + trimmed(review.Reason)
			}())
			for _, rejected := range review.Rejected {
				fmt.Fprintf(&b, "  - Rejected: **%s** — %s\n", markdownCell(rejected.Name), markdownCell(rejected.Reason))
			}
			if len(review.MissingStages) > 0 {
				fmt.Fprintf(&b, "  - Under-covered stages: %s\n", markdownCell(strings.Join(review.MissingStages, ", ")))
			}
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n## Method summary\n\n%d discovery queries; %d gap/QC queries; %d raw hits; %d leads reviewed; %d entities extracted; %d rejected; %d pages enriched; %d verification queries.\n\n",
		report.QueriesUsed, report.GapQueries, report.SearchHits, report.LeadsReviewed, report.LeadsExtracted,
		report.LeadsRejected, report.PagesEnriched, report.VerificationQueries)
	b.WriteString(renderListMarkdown("Honesty notes", report.Warnings))
	b.WriteString("## Appendix — sources and demand signals\n\n")
	for _, evidence := range report.Appendix {
		fmt.Fprintf(&b, "- [%s](%s) — %s; %s; %s\n",
			markdownCell(evidence.Title), evidence.URL, markdownCell(evidence.Lane),
			markdownCell(evidence.ClaimType), markdownCell(evidence.Status))
	}
	return b.String()
}

// renderDemandChainHTML renders a buyer-side run as a printable, self-contained
// branded document: the cover carries the demand-chain kicker and the run's
// objective; the body opens with the value chain the search was built from.
func renderDemandChainHTML(report *models.DemandChainReport) string {
	return executeDocument(demandChainHTMLTemplate, document{
		Title:        demandChainReportTitle(report),
		CoverSubject: nonEmpty(report.Activity, "Activity"),
		Subtitle:     trimmed(report.Geography),
		Kicker:       kickerDemand,
		CoverSummary: coverSummary(report.Objective, coverSummaryLimit),
		GeneratedAt:  generatedStamp(report.CompletedAt),
		CoverPhoto:   coverPhotoForScope("buyer"),
		Chain:        report,
	})
}

var demandChainHTMLTemplate = documentTemplate("demand-chain", `
<header><h1>{{.Chain.Activity}} — Demand Chain</h1>{{if .Chain.Objective}}<p class="subtitle">{{.Chain.Objective}}</p>{{end}}{{if .Chain.ProductCategory}}<p class="dim">Category: {{.Chain.ProductCategory}}{{if .Chain.CategoryRationale}} — {{.Chain.CategoryRationale}}{{end}}</p>{{end}}</header>
{{if .Chain.Chain}}<section>
	<h2>Value chain</h2>
	<table><thead><tr><th>Stage</th><th>Steps from the product</th><th>Why it consumes the product</th><th>Product families</th><th>Operator vocabulary</th><th>Source</th></tr></thead><tbody>
	{{range .Chain.Chain}}<tr><td>{{.Name}}</td><td>{{if eq .Distance 2}}2 (derived){{else}}1 (direct){{end}}</td><td>{{.Rationale}}</td><td>{{range .Products}}{{.}}; {{end}}</td><td>{{range .Operators}}{{.}}; {{end}}</td><td>{{.Source}}</td></tr>{{end}}
	</tbody></table>
</section>{{end}}
{{if .Chain.ChainReview}}<section><h2>Adversarial chain review</h2>
<p><strong>{{.Chain.ChainReview.Verdict}}</strong> · score {{printf "%.2f" .Chain.ChainReview.Score}}{{if .Chain.ChainReview.Reason}} — {{.Chain.ChainReview.Reason}}{{end}}</p>
{{if .Chain.ChainReview.RejectedStages}}<ul>{{range .Chain.ChainReview.RejectedStages}}<li>Rejected {{.Name}} — {{.Reason}}</li>{{end}}</ul>{{end}}
{{if .Chain.ChainReview.DistanceCorrections}}<ul>{{range .Chain.ChainReview.DistanceCorrections}}<li>Corrected {{.Name}} to distance {{.Distance}}</li>{{end}}</ul>{{end}}
{{if .Chain.ChainReview.MissingStages}}<p class="dim">Suggested missing stages (not added automatically): {{range .Chain.ChainReview.MissingStages}}{{.}}; {{end}}</p>{{end}}
</section>{{end}}
<section><h2>Results — verified consumers</h2>
{{if .Chain.Results}}<table><thead><tr><th>Company</th><th>Consuming stage</th><th>Country</th><th>Status</th><th>Confidence</th></tr></thead><tbody>{{range .Chain.Results}}<tr><td>{{.Name}}</td><td>{{.Stage}}</td><td>{{.Country}}</td><td>{{.Status}}</td><td>{{printf "%.2f" .Confidence}}</td></tr>{{end}}</tbody></table>{{else}}<p>No company met the consumption-verification threshold.</p>{{end}}
</section>
<section><h2>Potential buyers by stage</h2>
<p class="dim">Every company the consuming stage's lanes surfaced, verified or not — a company operating a stage that consumes the product is a potential buyer by construction, within the two-step buyer scope (direct consumers and one derived step). Status "lead" means the evidence did not reach the verification threshold; cross-check the verified list above.</p>
{{range .Chain.StageCandidates}}<div class="dept">
	<h3>{{.Stage.Name}}{{if eq .Stage.Source "category"}} — channel category (resale){{else if eq .Stage.Distance 2}} — step 2 (derived){{end}}</h3>
	{{if .Stage.Rationale}}<p class="dim">{{.Stage.Rationale}}</p>{{end}}
	{{if .Companies}}<table><thead><tr><th>Company</th><th>Country</th><th>Status</th><th>Confidence</th></tr></thead><tbody>{{range .Companies}}<tr><td>{{.Name}}</td><td>{{.Country}}</td><td>{{.Status}}</td><td>{{printf "%.2f" .Confidence}}</td></tr>{{end}}</tbody></table>
	{{else}}<p class="dim">No stage-active companies surfaced.</p>{{end}}
</div>{{end}}
</section>
{{if .Chain.StageCoverage}}<section><h2>Stage coverage — QC minimum</h2>
<p class="dim">Every stage, plus the separate distribution category, must identify at least six companies. A gap is reported, never filled with invented names.</p>
<table><thead><tr><th>Stage</th><th>Companies</th><th>Minimum</th><th>Result</th></tr></thead><tbody>{{range .Chain.StageCoverage}}<tr><td>{{.Stage}}</td><td>{{.Count}}</td><td>{{.Minimum}}</td><td>{{if .Pass}}PASS{{else}}FAIL{{end}}</td></tr>{{end}}</tbody></table>
</section>{{end}}
{{if .Chain.Reviews}}<section><h2>Quality review</h2>
{{range .Chain.Reviews}}<p><strong>{{if .Verdict}}{{.Verdict}}{{else}}unavailable{{end}}</strong> · score {{printf "%.2f" .Score}}{{if .Reason}} — {{.Reason}}{{end}}</p>
{{if .Rejected}}<ul>{{range .Rejected}}<li>Rejected {{.Name}} — {{.Reason}}</li>{{end}}</ul>{{end}}
{{if .MissingStages}}<p class="dim">Under-covered: {{range .MissingStages}}{{.}}; {{end}}</p>{{end}}{{end}}
</section>{{end}}
<section><h2>Method summary</h2><p>{{.Chain.QueriesUsed}} discovery queries, {{.Chain.GapQueries}} gap/QC queries, {{.Chain.SearchHits}} raw hits, {{.Chain.LeadsReviewed}} leads reviewed, {{.Chain.LeadsExtracted}} entities extracted, {{.Chain.LeadsRejected}} rejected, {{.Chain.PagesEnriched}} pages enriched, {{.Chain.VerificationQueries}} verification queries.</p></section>
{{if .Chain.Warnings}}<section class="card">
	<h2>Honesty notes</h2>
	<ul>{{range .Chain.Warnings}}<li>{{.}}</li>{{end}}</ul>
</section>{{end}}
<section><h2>Appendix — sources and demand signals</h2>{{range .Chain.Appendix}}<p class="src">{{.Title}} — {{.URL}} ({{.Lane}}, {{.ClaimType}}, {{.Status}})</p>{{end}}</section>
<footer>{{.Chain.Activity}} &middot; Crystal AI demand chain &middot; checked {{.GeneratedAt}}</footer>
`)
