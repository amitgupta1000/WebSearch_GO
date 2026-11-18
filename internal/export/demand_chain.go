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
	if category := strings.TrimSpace(report.ProductCategory); category != "" || len(report.CategoryResults) > 0 || report.CategoryQueries > 0 {
		b.WriteString("## category level results\n\n")
		if category != "" {
			fmt.Fprintf(&b, "Category: **%s**", category)
			if len(report.CategoryLanes) > 0 {
				if report.CategoryQueries > 0 {
					fmt.Fprintf(&b, "; discovery lanes: %s (%d queries)", strings.Join(report.CategoryLanes, ", "), report.CategoryQueries)
				} else {
					fmt.Fprintf(&b, "; discovery lanes: %s", strings.Join(report.CategoryLanes, ", "))
				}
			}
			b.WriteString(".\n\n")
		}
		if len(report.CategoryResults) == 0 {
			b.WriteString("No category-level company met the verification threshold. Category findings never enter product-level buyer results.\n\n")
		} else {
			b.WriteString("Independent downstream-category findings; these are separate from exact-product buyer results.\n\n")
			b.WriteString("| Company | Category | Role | Country | Employees | Turnover | Description | Status | Confidence |\n| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")
			for _, result := range report.CategoryResults {
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %.0f%% |\n", markdownCell(result.Name), markdownCell(result.Activity), markdownCell(result.Role), markdownCell(result.Country), markdownCell(result.Employees), markdownCell(result.Turnover), markdownCell(result.Description), markdownCell(result.Status), result.Confidence*100)
			}
			b.WriteString("\n")
		}
	}
	writeTradeSections(&b, report.Activity, report.TradeImporters, nil, markdownCell)
	b.WriteString("## Results — verified consumers\n\n")
	if len(report.Results) == 0 {
		b.WriteString("No company met the consumption-verification threshold.\n")
	} else {
		b.WriteString("| Company | Consuming stage | Presence | Employees | Turnover | Description | Country | Status | Confidence |\n| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")
		for _, result := range report.Results {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %.0f%% |\n",
				markdownCell(result.Name), markdownCell(result.Stage), markdownCell(result.Presence),
				markdownCell(result.Employees), markdownCell(result.Turnover), markdownCell(result.Description),
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
		b.WriteString("| Company | Presence | Country | Employees | Turnover | Description | Status | Confidence |\n| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")
		for _, company := range section.Companies {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %.0f%% |\n",
				markdownCell(company.Name), markdownCell(company.Presence), markdownCell(company.Country),
				markdownCell(company.Employees), markdownCell(company.Turnover), markdownCell(company.Description),
				markdownCell(company.Status), company.Confidence*100)
		}
		b.WriteString("\n")
		if len(section.GlobalCompanies) > 0 {
			b.WriteString("#### Global / cross-border (visible for audit; excluded from stage coverage)\n\n")
			b.WriteString("| Company | Country | Employees | Turnover | Description | Status | Confidence |\n| :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")
			for _, company := range section.GlobalCompanies {
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %.0f%% |\n",
					markdownCell(company.Name), markdownCell(company.Country), markdownCell(company.Employees),
					markdownCell(company.Turnover), markdownCell(company.Description), markdownCell(company.Status), company.Confidence*100)
			}
			b.WriteString("\n")
		}
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
	b.WriteString("\n## QC results\n\n")
	if trimmed(report.QCPromptSource) != "" {
		fmt.Fprintf(&b, "QC prompt source: **%s**.\n\n", markdownCell(report.QCPromptSource))
	}
	if len(report.Reviews) == 0 {
		b.WriteString("No buyer QC review was recorded for this run.\n\n")
	}
	for _, review := range report.Reviews {
		fmt.Fprintf(&b, "Verdict: **%s**; %d passed, %d failed.\n\n", markdownCell(nonEmpty(review.Verdict, "unavailable")), review.Passed, review.Failed)
		if len(review.Verdicts) > 0 {
			b.WriteString("| Company | Stage | QC tag | Reason |\n| :--- | :--- | :--- | :--- |\n")
			for _, verdict := range review.Verdicts {
				fmt.Fprintf(&b, "| %s | %s | qc %s | %s |\n", markdownCell(verdict.Name), markdownCell(verdict.Stage), markdownCell(verdict.Verdict), markdownCell(verdict.Reason))
			}
		} else {
			for _, rejected := range review.Rejected {
				fmt.Fprintf(&b, "| %s |  | qc failed | %s |\n", markdownCell(rejected.Name), markdownCell(rejected.Reason))
			}
		}
		b.WriteString("\n")
	}
	if len(report.QueryReviews) > 0 {
		b.WriteString("## Query review\n\n")
		b.WriteString("Adversarial aptness review of the planned queries before they ran.\n\n")
		b.WriteString("| Phase | Original | Verdict | Revised | Reason |\n| :--- | :--- | :--- | :--- | :--- |\n")
		for _, review := range report.QueryReviews {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", markdownCell(review.Phase), markdownCell(review.Original), review.Verdict, markdownCell(review.RevisedQuery), markdownCell(review.Reason))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n## Method summary\n\n%d discovery queries; %d category queries; %d gap/QC queries; %d raw hits; %d leads reviewed; %d entities extracted; %d rejected; %d pages enriched; %d verification queries.\n\n",
		report.QueriesUsed, report.CategoryQueries, report.GapQueries, report.SearchHits, report.LeadsReviewed, report.LeadsExtracted,
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
		Title:            demandChainReportTitle(report),
		CoverSubject:     nonEmpty(report.Activity, "Activity"),
		Subtitle:         trimmed(report.Geography),
		Kicker:           kickerDemand,
		CoverSummary:     coverSummary(report.Objective, coverSummaryLimit),
		GeneratedAt:      generatedStamp(report.CompletedAt),
		CoverPhoto:       coverPhotoForScope("buyer"),
		CoverSteps:       demandCoverSteps(report),
		SummaryCompanies: topMarketCompanies(report.Results, summaryCompanyLimit),
		Chain:            report,
	})
}

// demandCoverSteps narrates the buyer pipeline the run actually executed.
func demandCoverSteps(report *models.DemandChainReport) []CoverStep {
	steps := make([]CoverStep, 0, 7)
	if len(report.Chain) > 0 {
		steps = append(steps, CoverStep{Label: "Value-chain inference", Detail: fmt.Sprintf("%d consuming stage(s) organised the search%s", len(report.Chain), chainSourceNote(report))})
	} else {
		steps = append(steps, CoverStep{Label: "Value-chain inference", Detail: "the operator supplied the stages directly"})
	}
	if len(report.QueryReviews) > 0 {
		steps = append(steps, CoverStep{Label: "Query planning and review", Detail: fmt.Sprintf("%d planned queries were adversarially reviewed before spending a search", len(report.QueryReviews))})
	}
	steps = append(steps, CoverStep{Label: "Discovery and verification", Detail: fmt.Sprintf("%d search(es) across the lanes, %d verification search(es); %d candidates found, %d lead(s) reviewed, %d page(s) read", report.QueriesUsed, report.VerificationQueries, report.CandidatesFound, report.LeadsReviewed, report.PagesEnriched)})
	if len(report.Reviews) > 0 {
		steps = append(steps, CoverStep{Label: "Quality control", Detail: fmt.Sprintf("verdict %q; %d stage(s) below the coverage floor", report.Reviews[0].Verdict, len(report.Reviews[0].MissingStages))})
	}
	if len(report.TradeImporters) > 0 {
		steps = append(steps, CoverStep{Label: "Crystal trade DB", Detail: fmt.Sprintf("%d ranked importer(s) from first-party customs data, queried before the web lanes", len(report.TradeImporters))})
	}
	steps = append(steps, CoverStep{Label: "Export", Detail: "Markdown and PDF printed by the same Chromium that reads the sources — no second model call"})
	return steps
}

// chainSourceNote names where the value chain came from, so the cover's
// inference step is honest about its provenance.
func chainSourceNote(report *models.DemandChainReport) string {
	if report.ChainInferred {
		return " (model-inferred, adversarially reviewed)"
	}
	return " (operator-supplied or fallback)"
}

var demandChainHTMLTemplate = documentTemplate("demand-chain", `
{{if or .SummaryCompanies .Chain.TradeImporters}}<section class="summary-page">
<p class="summary-kicker">Summary — most relevant findings</p>
<h1 class="summary-title">{{.Chain.Activity}}</h1>
{{if .SummaryCompanies}}<table><thead><tr><th>Company</th><th>Role</th><th>Country</th><th>Status</th><th>Confidence</th></tr></thead><tbody>{{range .SummaryCompanies}}<tr><td>{{.Name}}</td><td>{{.Role}}</td><td>{{.Country}}</td><td>{{.Status}}</td><td>{{printf "%.2f" .Confidence}}</td></tr>{{end}}</tbody></table>{{end}}
{{if .Chain.TradeImporters}}<p class="dim">First-party customs records: {{len .Chain.TradeImporters}} ranked importer(s) — published separately; the operator decides how to weigh them.</p>{{end}}
{{if .Chain.StageCoverage}}<p class="dim">Stage coverage: {{range $i, $s := .Chain.StageCoverage}}{{if $i}} · {{end}}{{$s.Stage}} {{$s.Count}}/{{$s.Minimum}}{{end}}.</p>{{end}}
<p class="dim">The value chain, the full tables and the source appendix follow.</p>
</section>{{end}}
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
{{if or .Chain.ProductCategory .Chain.CategoryResults .Chain.CategoryQueries}}<section><h2>category level results</h2>
{{if .Chain.ProductCategory}}<p class="dim">Category: {{.Chain.ProductCategory}}{{if .Chain.CategoryLanes}} · discovery lanes: {{range .Chain.CategoryLanes}}{{.}}; {{end}}{{if .Chain.CategoryQueries}}({{.Chain.CategoryQueries}} queries){{end}}{{end}}</p>{{end}}
{{if .Chain.CategoryResults}}<p class="dim">Independent downstream-category findings; these are separate from exact-product buyer results.</p><table><thead><tr><th>Company</th><th>Category</th><th>Role</th><th>Country</th><th>Employees</th><th>Turnover</th><th>Description</th><th>Status</th><th>Confidence</th></tr></thead><tbody>{{range .Chain.CategoryResults}}<tr><td>{{.Name}}</td><td>{{.Activity}}</td><td>{{.Role}}</td><td>{{.Country}}</td><td>{{.Employees}}</td><td>{{.Turnover}}</td><td>{{.Description}}</td><td>{{.Status}}</td><td>{{printf "%.2f" .Confidence}}</td></tr>{{end}}</tbody></table>{{else}}<p>No category-level company met the verification threshold. Category findings never enter product-level buyer results.</p>{{end}}
</section>{{end}}
{{if .Chain.TradeImporters}}<section><h2>trade record importers ({{len .Chain.TradeImporters}})</h2><p class="dim">Indian importers of the product named by the Crystal trade DB. An importer may equally be a reseller (distributor, wholesaler or trader) — the operator decides; separate from verified buyer results and stage coverage.</p><table><thead><tr><th>Company</th><th>Country</th><th>Role tags</th><th>Shipments</th><th>Value (USD)</th><th>Quantity (MT)</th><th>Rank</th><th>Status</th></tr></thead><tbody>{{range .Chain.TradeImporters}}<tr><td>{{.Name}}</td><td>{{.Country}}</td><td>{{range .RoleTags}}{{.}} {{end}}</td><td>{{with .Trade}}{{.TransactionCount}}{{end}}</td><td>{{with .Trade}}{{printf "%.0f" .TotalValueUSD}}{{end}}</td><td>{{with .Trade}}{{printf "%.0f" .TotalQuantityMT}}{{end}}</td><td>{{with .Trade}}{{.Rank}}{{end}}</td><td>{{.Status}}</td></tr>{{end}}</tbody></table></section>{{end}}
<section><h2>Results — verified consumers</h2>
{{if .Chain.Results}}<table><thead><tr><th>Company</th><th>Consuming stage</th><th>Presence</th><th>Employees</th><th>Turnover</th><th>Description</th><th>Country</th><th>Status</th><th>Confidence</th></tr></thead><tbody>{{range .Chain.Results}}<tr><td>{{.Name}}</td><td>{{.Stage}}</td><td>{{.Presence}}</td><td>{{.Employees}}</td><td>{{.Turnover}}</td><td>{{.Description}}</td><td>{{.Country}}</td><td>{{.Status}}</td><td>{{printf "%.2f" .Confidence}}</td></tr>{{end}}</tbody></table>{{else}}<p>No company met the consumption-verification threshold.</p>{{end}}
</section>
<section><h2>Potential buyers by stage</h2>
<p class="dim">Every company the consuming stage's lanes surfaced, verified or not — a company operating a stage that consumes the product is a potential buyer by construction, within the two-step buyer scope (direct consumers and one derived step). Status "lead" means the evidence did not reach the verification threshold; cross-check the verified list above.</p>
{{range .Chain.StageCandidates}}<div class="dept">
	<h3>{{.Stage.Name}}{{if eq .Stage.Source "category"}} — channel category (resale){{else if eq .Stage.Distance 2}} — step 2 (derived){{end}}</h3>
	{{if .Stage.Rationale}}<p class="dim">{{.Stage.Rationale}}</p>{{end}}
	{{if .Companies}}<table><thead><tr><th>Company</th><th>Presence</th><th>Country</th><th>Employees</th><th>Turnover</th><th>Description</th><th>Status</th><th>Confidence</th></tr></thead><tbody>{{range .Companies}}<tr><td>{{.Name}}</td><td>{{.Presence}}</td><td>{{.Country}}</td><td>{{.Employees}}</td><td>{{.Turnover}}</td><td>{{.Description}}</td><td>{{.Status}}</td><td>{{printf "%.2f" .Confidence}}</td></tr>{{end}}</tbody></table>
	{{else}}<p class="dim">No stage-active companies surfaced.</p>{{end}}{{if .GlobalCompanies}}<h4>Global / cross-border (excluded from stage coverage)</h4><table><thead><tr><th>Company</th><th>Country</th><th>Employees</th><th>Turnover</th><th>Description</th><th>Status</th><th>Confidence</th></tr></thead><tbody>{{range .GlobalCompanies}}<tr><td>{{.Name}}</td><td>{{.Country}}</td><td>{{.Employees}}</td><td>{{.Turnover}}</td><td>{{.Description}}</td><td>{{.Status}}</td><td>{{printf "%.2f" .Confidence}}</td></tr>{{end}}</tbody></table>{{end}}
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
{{if .Chain.Reviews}}<section><h2>QC results</h2><p class="dim">QC prompt source: {{.Chain.QCPromptSource}}</p>{{range .Chain.Reviews}}<p><strong>{{.Verdict}}</strong> Â· {{.Passed}} passed Â· {{.Failed}} failed</p>{{if .Verdicts}}<table><thead><tr><th>Company</th><th>Stage</th><th>QC tag</th><th>Reason</th></tr></thead><tbody>{{range .Verdicts}}<tr><td>{{.Name}}</td><td>{{.Stage}}</td><td>qc {{.Verdict}}</td><td>{{.Reason}}</td></tr>{{end}}</tbody></table>{{end}}{{end}}</section>{{end}}<section><h2>Method summary</h2><p>{{.Chain.QueriesUsed}} discovery queries, {{.Chain.CategoryQueries}} category queries, {{.Chain.GapQueries}} gap/QC queries, {{.Chain.SearchHits}} raw hits, {{.Chain.LeadsReviewed}} leads reviewed, {{.Chain.LeadsExtracted}} entities extracted, {{.Chain.LeadsRejected}} rejected, {{.Chain.PagesEnriched}} pages enriched, {{.Chain.VerificationQueries}} verification queries.</p></section>
{{if .Chain.Warnings}}<section class="card">
	<h2>Honesty notes</h2>
	<ul>{{range .Chain.Warnings}}<li>{{.}}</li>{{end}}</ul>
</section>{{end}}
<section><h2>Appendix — sources and demand signals</h2>{{range .Chain.Appendix}}<p class="src">{{.Title}} — {{.URL}} ({{.Lane}}, {{.ClaimType}}, {{.Status}})</p>{{end}}</section>
<footer>{{.Chain.Activity}} &middot; Crystal AI demand chain &middot; checked {{.GeneratedAt}}</footer>
`)
