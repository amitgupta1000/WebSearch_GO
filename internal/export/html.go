package export

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// printCSS is embedded rather than linked: the PDF is printed by a browser that
// may have no network access at all, so an artifact must never depend on a CDN.
// It is the Crystal AI report system — sage-teal accents on warm off-white,
// serif display headings over a humanist sans body, hairline rules, cream
// callout cards, and a cover page carrying the wordmark, a photograph and the
// checked stamp — modelled on the Crystal AI counterparty research deck.
const printCSS = `
  @page { size: A4; }
  /* Page margins are supplied by the print call, not @page: Chromium only draws
     the running footer in the print margins when @page leaves them unset. */
  * { box-sizing: border-box; }
  body { font-family: ` + brandSansStack + `; color: ` + brandInk + `; font-size: 10.5pt; line-height: 1.5; margin: 0; }

  /* Cover */
  .cover { break-after: page; page-break-after: always; }
  .cover-band { height: 4.5mm; background: ` + brandAccentSoft + `; border-radius: 1mm; margin-bottom: 10mm; }
  .cover-grid { display: flex; gap: 9mm; align-items: flex-start; }
  .cover-main { flex: 1 1 60%; }
  .cover-figure { flex: 0 0 35%; margin: 26mm 0 0; break-inside: avoid; page-break-inside: avoid; }
  .cover-photo { width: 100%; display: block; border-radius: 1.5mm; }
  .cover-figure figcaption { color: ` + brandSecondary + `; font-size: 8.5pt; font-style: italic; margin-top: 2mm; }
  .wordmark { color: ` + brandAccent + `; font-weight: 700; font-size: 12pt; letter-spacing: 0.4em; text-transform: uppercase; margin: 0 0 15mm; }
  .kicker { color: ` + brandAccent + `; font-weight: 700; font-size: 9.5pt; letter-spacing: 0.08em; text-transform: uppercase; margin: 0 0 4mm; }
  .cover-title { font-family: ` + brandSerifStack + `; font-weight: 400; font-size: 28pt; line-height: 1.12; margin: 0 0 6mm; }
  .cover-summary { color: ` + brandSecondary + `; font-size: 11pt; line-height: 1.55; margin: 0 0 3.5mm; }
  .cover-meta { color: ` + brandSecondary + `; font-size: 9.5pt; margin: 0 0 3mm; }
  .pill { display: inline-block; background: ` + brandCream + `; border: 1px solid ` + brandAccentSoft + `; border-radius: 999px; padding: 1.2mm 4mm; font-size: 8.5pt; font-weight: 700; letter-spacing: 0.04em; margin-right: 2mm; }
  .checked { color: ` + brandAccent + `; font-weight: 700; margin: 5mm 0 0; }

  /* Content */
  header { border-bottom: 1px solid ` + brandHairline + `; padding-bottom: 3mm; margin-bottom: 5mm; }
  h1 { font-family: ` + brandSerifStack + `; font-weight: 400; font-size: 17pt; margin: 0 0 2mm; }
  h2 { font-family: ` + brandSerifStack + `; font-weight: 400; font-size: 14pt; margin: 9mm 0 3mm; border-bottom: 1px solid ` + brandHairline + `; padding-bottom: 1.5mm; }
  h3 { color: ` + brandAccent + `; font-size: 10.5pt; font-weight: 700; margin: 6mm 0 2mm; }
  .subtitle { color: ` + brandSecondary + `; margin: 0; }
  .meta { color: ` + brandSecondary + `; font-size: 9pt; margin: 2mm 0 0; }
  .dim { color: ` + brandSecondary + `; }
  .src { color: ` + brandSecondary + `; font-size: 8.5pt; word-break: break-all; }
  .report-text { overflow-wrap: anywhere; word-break: break-word; font-family: ` + brandSansStack + `; font-size: 11pt; line-height: 1.58; }
  .report-text p { margin: 0 0 4mm; }
  .report-text h1 { margin-top: 0; }
  .report-text h2 { margin-top: 7mm; }
  table { width: 100%; border-collapse: collapse; margin: 3mm 0 5mm; }
  th, td { text-align: left; padding: 2.2mm 2.5mm; border-bottom: 1px solid #e9e2da; vertical-align: top; }
  th { background: ` + brandCream + `; border-top: 2px solid ` + brandAccentSoft + `; font-size: 8.5pt; text-transform: uppercase; letter-spacing: 0.05em; color: ` + brandAccent + `; }
  tr { break-inside: avoid; page-break-inside: avoid; }
  ul, ol { margin: 2mm 0 4mm; padding-left: 18px; }
  li { margin-bottom: 1.5mm; }
  .dept { break-inside: avoid; page-break-inside: avoid; margin-bottom: 8mm; }
  .head { margin: 0 0 2mm; font-weight: 600; }
  section.card { background: ` + brandCream + `; border-radius: 2mm; padding: 3.5mm 5mm 1.5mm; break-inside: avoid; page-break-inside: avoid; margin: 6mm 0; }
  footer { margin-top: 10mm; border-top: 1px solid ` + brandHairline + `; padding-top: 3mm; color: ` + brandSecondary + `; font-size: 8.5pt; }
`

// document is the data one printed artifact is rendered from. Company, person,
// legal and market builds use the same struct so all templates share the shell,
// including the branded cover: the kicker names the kind of report, the cover
// subject is the researched party, and the summary is the run's own narrative
// condensed to a cover paragraph.
type document struct {
	Title        string
	Subtitle     string
	CoverSubject string
	Kicker       string
	CoverSummary string
	CoverPills   []string
	GeneratedAt  string
	CoverPhoto   template.URL
	Chart        *models.OrgChart
	Profile      *models.PersonProfile
	Legal        *models.LegalReport
	Market       *models.MarketLandscapeReport
	Chain        *models.DemandChainReport
	Body         template.HTML
	Timings      []models.PhaseTiming
}

func renderMarketLandscapeHTML(report *models.MarketLandscapeReport) string {
	return executeDocument(marketLandscapeHTMLTemplate, document{
		Title:        marketReportTitle(report),
		CoverSubject: nonEmpty(report.Activity, "Producer/Seller Landscape"),
		Kicker:       kickerMarket,
		CoverSummary: coverSummary(report.Objective, coverSummaryLimit),
		GeneratedAt:  generatedStamp(report.CompletedAt),
		CoverPhoto:   coverPhotoForScope("buyer"),
		Market:       report,
	})
}

// marketReportTitle is the document title, shared by the cover, the running
// footer and the stored file name.
func marketReportTitle(report *models.MarketLandscapeReport) string {
	return nonEmpty(report.Activity, "Producer/Seller Landscape") + " — Research Report"
}

var marketLandscapeHTMLTemplate = documentTemplate("market-landscape", `
<header><h1>{{.Market.Activity}} — Producer/Seller Landscape</h1><p class="subtitle">{{.Market.Objective}}</p></header>
<section><h2>Results</h2>
{{if .Market.Results}}<table><thead><tr><th>Company</th><th>Country</th><th>Activity</th><th>Status</th><th>Confidence</th></tr></thead><tbody>{{range .Market.Results}}<tr><td>{{.Name}}</td><td>{{.Country}}</td><td>{{.Activity}}</td><td>{{.Status}}</td><td>{{printf "%.2f" .Confidence}}</td></tr>{{end}}</tbody></table>{{else}}<p>No verified companies were promoted.</p>{{end}}
</section>
<section><h2>Method summary</h2><p>{{.Market.SearchHits}} raw hits, {{.Market.LeadsReviewed}} leads reviewed, {{.Market.LeadsExtracted}} entities extracted, {{.Market.LeadsResolved}} resolved, {{.Market.PagesEnriched}} pages enriched.</p></section>
<section><h2>Appendix — sources and validation evidence</h2>{{range .Market.Appendix}}<p class="src">{{.Title}} — {{.URL}} ({{.SourceTier}}, {{.Status}})</p>{{end}}</section>
<footer>Crystal AI &middot; detailed source evidence is retained in the appendix.</footer>
`)

// documentTemplate wraps a body in the shared print shell: a branded cover page
// followed by the report body. The cover mirrors the Crystal AI deck — teal
// band, letter-spaced wordmark, kicker, serif title, summary, checked stamp and
// a photograph captioned as illustrative.
func documentTemplate(name, body string) *template.Template {
	return template.Must(template.New(name).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Title}}</title>
<style>` + printCSS + `</style>
</head>
<body>
<section class="cover">
	<div class="cover-band"></div>
	<div class="cover-grid">
		<div class="cover-main">
			<p class="wordmark">Crystal AI</p>
			{{if .Kicker}}<p class="kicker">{{.Kicker}}</p>{{end}}
			<h1 class="cover-title">{{.CoverSubject}}</h1>
			{{if .CoverSummary}}<p class="cover-summary">{{.CoverSummary}}</p>{{end}}
			{{if .Subtitle}}<p class="cover-meta">{{.Subtitle}}</p>{{end}}
			{{if .CoverPills}}<p class="pill-row">{{range .CoverPills}}<span class="pill">{{.}}</span>{{end}}</p>{{end}}
			<p class="checked">Checked {{.GeneratedAt}}</p>
		</div>
		<figure class="cover-figure">
			{{if .CoverPhoto}}<img class="cover-photo" src="{{.CoverPhoto}}" alt="Illustrative cover photograph">
			<figcaption>` + coverPhotoCaption + `</figcaption>{{end}}
		</figure>
	</div>
</section>
` + body + `
</body>
</html>`))
}

var personHTMLTemplate = documentTemplate("person", `
<header>
	<h1>{{.Profile.FullName}}</h1>
	{{if .Subtitle}}<p class="subtitle">{{.Subtitle}}</p>{{end}}
	<p class="meta">Confidence {{.Profile.ScorePercent}}%</p>
</header>
{{if .Profile.ExecutiveSummary}}<section><h2>Executive summary</h2><p>{{.Profile.ExecutiveSummary}}</p></section>{{end}}
{{if .Profile.Experiences}}<section>
	<h2>Experience</h2>
	{{range .Profile.Experiences}}
	<div class="dept">
		<h3>{{.Role}}{{if .Company}} &mdash; {{.Company}}{{end}}</h3>
		{{if or .Period .Location}}<p class="dim">{{.Period}}{{if and .Period .Location}} &middot; {{end}}{{.Location}}</p>{{end}}
		{{if .Source}}<p class="src">source: {{.Source}}</p>{{end}}
		{{if .Flag}}<p class="dim">&#9888; {{.Flag}}</p>{{end}}
		{{if .Highlights}}<ul>{{range .Highlights}}<li>{{.}}</li>{{end}}</ul>{{end}}
	</div>
	{{end}}
</section>{{end}}
{{if .Profile.Education}}<section>
	<h2>Education</h2>
	<table><thead><tr><th>Institution</th><th>Degree</th><th>Field</th><th>Year</th></tr></thead><tbody>
	{{range .Profile.Education}}<tr><td>{{.Institution}}</td><td>{{.Degree}}</td><td>{{.Field}}</td><td>{{.Year}}</td></tr>{{end}}
	</tbody></table>
</section>{{end}}
{{if .Profile.KeySkills}}<section><h2>Key skills</h2><ul>{{range .Profile.KeySkills}}<li>{{.}}</li>{{end}}</ul></section>{{end}}
{{if .Profile.NotableProjects}}<section><h2>Notable projects</h2><ul>{{range .Profile.NotableProjects}}<li>{{.}}</li>{{end}}</ul></section>{{end}}
{{if .Profile.OnlinePresence}}<section>
	<h2>Online presence</h2>
	<table><thead><tr><th>Platform</th><th>Link</th></tr></thead><tbody>
	{{range $platform, $link := .Profile.OnlinePresence}}<tr><td>{{$platform}}</td><td class="src">{{$link}}</td></tr>{{end}}
	</tbody></table>
</section>{{end}}
{{if .Profile.Sources}}<section>
	<h2>Sources</h2>
	<ol>{{range .Profile.Sources}}<li class="src">{{.}}</li>{{end}}</ol>
</section>{{end}}
{{if .Profile.SameNamed}}<section class="card">
	<h2>Same-named people</h2>
	<p class="dim">The dossier above is the confirmed identity's. A person search answers with several people who share the name; these are the most relevant snippets for the others, grouped by the employer each source states.</p>
	{{range .Profile.SameNamed}}
	<div class="dept">
		<h3>{{if .Employer}}{{.Employer}}{{else}}No employer stated (cannot be attributed){{end}}</h3>
		<ul>{{range .Snippets}}<li>{{.Title}}{{if .Snippet}} &mdash; {{.Snippet}}{{end}} <span class="src">({{.Link}})</span></li>{{end}}</ul>
	</div>
	{{end}}
</section>{{end}}
<footer>{{.Profile.FullName}} &middot; Crystal AI &middot; checked {{.GeneratedAt}}</footer>
`)

// renderCompanyHTML renders an org chart as a printable, self-contained document.
func renderCompanyHTML(chart *models.OrgChart) string {
	pills := []string(nil)
	if gate := chart.EntityGate; gate != nil {
		if gate.Confirmed {
			pills = append(pills, "Entity confirmed")
		} else {
			pills = append(pills, "Entity unconfirmed")
		}
	}
	return executeDocument(companyHTMLTemplate, document{
		Title:        companyReportTitle(chart),
		Subtitle:     joinNonEmpty([]string{chart.Domain, chart.Headquarters}, " · "),
		CoverSubject: nonEmpty(chart.CompanyName, "Company"),
		Kicker:       kickerCompany,
		CoverSummary: coverSummary(chart.Overview, coverSummaryLimit),
		CoverPills:   pills,
		GeneratedAt:  generatedStamp(chart.LastUpdated),
		CoverPhoto:   coverPhotoForScope("company"),
		Chart:        chart,
		Timings:      chart.Timings,
	})
}

// companyReportTitle is the document title: the subject plus the report kind,
// shared by the cover, the running footer and the stored file name.
func companyReportTitle(chart *models.OrgChart) string {
	return nonEmpty(chart.CompanyName, "Company") + " — Org Chart"
}

// renderPersonHTML renders a dossier as a printable, self-contained document.
func renderPersonHTML(profile *models.PersonProfile) string {
	var pills []string
	if score := profile.ScorePercent(); score > 0 {
		pills = append(pills, fmt.Sprintf("Confidence %d%%", score))
	}
	return executeDocument(personHTMLTemplate, document{
		Title:        personReportTitle(profile),
		Subtitle:     joinNonEmpty([]string{profile.CurrentTitle, profile.CurrentCompany, profile.Location}, " · "),
		CoverSubject: nonEmpty(profile.FullName, "Person"),
		Kicker:       kickerPerson,
		CoverSummary: coverSummary(profile.ExecutiveSummary, coverSummaryLimit),
		CoverPills:   pills,
		GeneratedAt:  generatedStamp(profile.LastUpdated),
		CoverPhoto:   coverPhotoForScope("person"),
		Profile:      profile,
	})
}

// personReportTitle is the document title, shared by the cover, the running
// footer and the stored file name.
func personReportTitle(profile *models.PersonProfile) string {
	return nonEmpty(profile.FullName, "Person") + " — Professional Dossier"
}

// renderLegalHTML renders a legal check as a printable, self-contained document.
func renderLegalHTML(report *models.LegalReport) string {
	subtitle := report.Domain
	if report.RegistrationID != "" {
		subtitle = joinNonEmpty([]string{subtitle, "CIN " + report.RegistrationID}, " · ")
	}
	narrative := report.Summary
	if report.Risk != nil && strings.TrimSpace(report.Risk.Summary) != "" {
		narrative = report.Risk.Summary
	}
	pills := make([]string, 0, 2)
	if report.EntityConfirmed {
		pills = append(pills, "Identity confirmed")
	} else {
		pills = append(pills, "Identity unconfirmed")
	}
	if report.Risk != nil && strings.TrimSpace(report.Risk.Level) != "" {
		pills = append(pills, "Legal risk — "+report.Risk.Level)
	}
	return executeDocument(legalHTMLTemplate, document{
		Title:        legalReportTitle(report),
		Subtitle:     subtitle,
		CoverSubject: nonEmpty(report.CompanyName, "Company"),
		Kicker:       kickerLegal,
		CoverSummary: coverSummary(narrative, coverSummaryLimit),
		CoverPills:   pills,
		GeneratedAt:  generatedStamp(report.LastUpdated),
		CoverPhoto:   coverPhotoForScope("legal"),
		Legal:        report,
	})
}

// legalReportTitle is the document title, shared by the cover, the running
// footer and the stored file name.
func legalReportTitle(report *models.LegalReport) string {
	return nonEmpty(report.CompanyName, "Company") + " — Legal Check"
}

var legalHTMLTemplate = documentTemplate("legal", `
<header>
	<h1>{{.Legal.CompanyName}} — Legal Check</h1>
	{{if .Subtitle}}<p class="subtitle">{{.Subtitle}}</p>{{end}}
	<p class="meta">{{if .Legal.EntityConfirmed}}Identity confirmed by the operator{{else}}Identity unconfirmed &mdash; matches lean on the name alone{{end}}</p>
</header>
{{if .Legal.Risk}}<section class="card">
	<h2>Legal risk — {{.Legal.Risk.Level}}</h2>
	{{if .Legal.Risk.Summary}}<p>{{.Legal.Risk.Summary}}</p>{{end}}
	{{if .Legal.Risk.Drivers}}<ul>{{range .Legal.Risk.Drivers}}<li>{{.}}</li>{{end}}</ul>{{end}}
	{{if not .Legal.Risk.Classified}}<p class="dim">Count-derived fallback: the model was unavailable for this run.</p>{{end}}
</section>{{end}}
{{if .Legal.Summary}}<section><h2>Exposure summary</h2><p>{{.Legal.Summary}}</p></section>{{end}}
{{if .Legal.Sources}}<section>
	<h2>Sources</h2>
	<ul>{{range .Legal.Sources}}
		<li>{{.Name}} &mdash; {{.Kept}} kept of {{.Hits}} hits in {{.Queries}} search(es){{if .Unavailable}} <span class="dim">({{.Unavailable}})</span>{{end}}</li>
	{{end}}</ul>
</section>{{end}}
<section>
	<h2>Matched findings</h2>
	{{if .Legal.Findings}}
	<table><thead><tr><th>Case</th><th>Court</th><th>Date</th><th>Side</th><th>Category</th><th>Status</th></tr></thead><tbody>
	{{range .Legal.Findings}}<tr><td>{{.CaseTitle}}{{if .Snippet}}<br><span class="src">{{.Snippet}}</span>{{end}}</td><td>{{.Court}}</td><td>{{.Date}}</td><td>{{if eq .Role "facing"}}responding{{else if eq .Role "initiated"}}initiated{{end}}</td><td>{{.Category}}</td><td>{{.Status}}{{if .Iterations}} <span class="dim">({{.Iterations}} records)</span>{{end}}</td></tr>{{end}}
	</tbody></table>
	{{else}}<p class="dim">No case records matched the confirmed identity.</p>{{end}}
</section>
{{if .Legal.SameNamed}}<section>
	<h2>Same-named entities</h2>
	<p class="dim">These records share part of the party name without a stronger identity key. They are listed rather than dropped, because a legal check must not silently discard a hit that merely shares a name.</p>
	<table><thead><tr><th>Case</th><th>Court</th><th>Date</th></tr></thead><tbody>
	{{range .Legal.SameNamed}}<tr><td>{{.CaseTitle}}</td><td>{{.Court}}</td><td>{{.Date}}</td></tr>{{end}}
	</tbody></table>
</section>{{end}}
{{if .Legal.Persons}}<section>
	<h2>Named persons</h2>
	{{range .Legal.Persons}}
	<div class="dept">
		<h3>{{.Name}}{{if not .Checked}} <span class="dim">(not searched)</span>{{end}}</h3>
		{{if .SkippedReason}}<p class="dim">{{.SkippedReason}}</p>{{end}}
		{{if .Findings}}<ul>{{range .Findings}}<li>{{.CaseTitle}}{{if .Court}} &mdash; {{.Court}}{{end}}{{if .Date}}, {{.Date}}{{end}}{{if .Category}} <span class="dim">({{.Category}})</span>{{end}}</li>{{end}}</ul>{{end}}
	</div>
	{{end}}
</section>{{end}}
<footer>{{.Legal.CompanyName}} &middot; Crystal AI legal check &middot; checked {{.GeneratedAt}}</footer>
`)

// executeDocument renders a template, falling back to a minimal page so a rendering
// problem degrades to a plain PDF instead of an empty file.
func executeDocument(tmpl *template.Template, data document) string {
	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, data); err != nil {
		log.Printf("[WARN] rendering %s document failed: %v", tmpl.Name(), err)
		return "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><title>" +
			template.HTMLEscapeString(data.Title) + "</title></head><body><h1>" +
			template.HTMLEscapeString(data.Title) + "</h1></body></html>"
	}
	return buffer.String()
}

var companyHTMLTemplate = documentTemplate("company", `
<header>
	<h1>{{.Chart.CompanyName}}</h1>
	{{if .Subtitle}}<p class="subtitle">{{.Subtitle}}</p>{{end}}
</header>
{{if .Chart.Overview}}<section><h2>Overview</h2><p>{{.Chart.Overview}}</p></section>{{end}}
{{if .Chart.Brief}}<section><h2>Company Brief</h2>
{{if .Chart.Brief.BusinessDescription}}<p><strong>Business description:</strong> {{.Chart.Brief.BusinessDescription}}</p>{{end}}
{{if .Chart.Brief.KeyProducts}}<p><strong>Key products:</strong> {{range $index, $product := .Chart.Brief.KeyProducts}}{{if $index}}, {{end}}{{$product}}{{end}}</p>{{end}}
{{if .Chart.Brief.KeyLocations}}<p><strong>Key locations:</strong> {{range $index, $location := .Chart.Brief.KeyLocations}}{{if $index}}; {{end}}{{$location.City}}{{if $location.Country}}, {{$location.Country}}{{end}}{{end}}</p>{{end}}
</section>{{end}}
{{if .Chart.Leadership}}<section>
	<h2>Executive Leadership</h2>
	<table><thead><tr><th>Name</th><th>Title</th><th>Location</th><th>As of</th><th>Public source</th></tr></thead><tbody>
	{{range .Chart.Leadership}}<tr><td>{{.Name}}</td><td>{{.Title}}</td><td>{{.Location}}</td><td>{{.Freshness}}</td><td class="src">{{.PublicSource}}</td></tr>{{end}}
	</tbody></table>
</section>{{end}}
{{if .Chart.Departments}}<section>
	<h2>Departments &amp; Functions</h2>
	{{range .Chart.Departments}}
	<div class="dept">
		<h3>{{.Name}}</h3>
		{{if .Head}}<p class="head">Head: {{.Head}}</p>{{end}}
		{{if .Description}}<p>{{.Description}}</p>{{end}}
		{{if .Members}}<ul>{{range .Members}}<li><strong>{{.Name}}</strong>{{if .Title}} &mdash; {{.Title}}{{end}}{{if .Location}} <span class="dim">({{.Location}})</span>{{end}}{{if .FreshnessNote}} <span class="dim">&middot; {{.FreshnessNote}}</span>{{end}}</li>{{end}}</ul>
		{{else}}<p class="dim">No named contacts found for this function.</p>{{end}}
	</div>
	{{end}}
</section>{{end}}
{{if .Chart.Locations}}<section>
	<h2>Locations</h2>
	<table><thead><tr><th>City</th><th>Country</th><th>Address</th><th>Est. headcount</th></tr></thead><tbody>
	{{range .Chart.Locations}}<tr><td>{{.City}}</td><td>{{.Country}}</td><td>{{.Address}}</td><td>{{.HeadcountEst}}</td></tr>{{end}}
	</tbody></table>
</section>{{end}}
{{if .Chart.EntityGate}}<section class="card">
	<h2>Entity gate</h2>
	<p class="dim">A source had to name the company to be used: <strong>{{range $index, $key := .Chart.EntityGate.Keys}}{{if $index}}, {{end}}{{$key}}{{end}}</strong>{{if .Chart.EntityGate.Confirmed}} (confirmed by the operator){{else}} (not confirmed — treat the identity as the operator's description){{end}}.</p>
	<table><thead><tr><th>What the gate did</th><th>Count</th></tr></thead><tbody>
	<tr><td>Planned queries dropped for not naming the company</td><td>{{.Chart.EntityGate.QueriesDropped}}</td></tr>
	<tr><td>Candidates that named the company</td><td>{{.Chart.EntityGate.CandidatesTagged}}</td></tr>
	<tr><td>Candidates that named nothing</td><td>{{.Chart.EntityGate.CandidatesNone}}</td></tr>
	<tr><td>Search snippets dropped before synthesis</td><td>{{.Chart.EntityGate.SnippetsDropped}}</td></tr>
	<tr><td>People excluded from the roster</td><td>{{.Chart.EntityGate.RowsOffEntity}}</td></tr>
	<tr><td>People kept because a reverse lookup tied them to the company</td><td>{{.Chart.EntityGate.RescuedByLookup}}</td></tr>
	{{if .Chart.EntityGate.GapLookups}}<tr><td>Company-anchored lookups run before synthesis (areas the evidence did not cover)</td><td>{{.Chart.EntityGate.GapLookups}}, adding {{.Chart.EntityGate.GapEvidence}} source(s)</td></tr>{{end}}
	{{if .Chart.EntityGate.LookupsDeferred}}<tr><td>People left unchecked by the lookup pass</td><td>{{.Chart.EntityGate.LookupsDeferred}}</td></tr>{{end}}
	{{if .Chart.EntityGate.Mode}}<tr><td>Gate mode</td><td>{{.Chart.EntityGate.Mode}}</td></tr>{{end}}
	</tbody></table>
	{{if .Chart.EntityGate.OffEntity}}<h3>Unverified / other entities</h3>
	<p class="dim">Found by the searches, but no source connecting them to the company was found. They are not part of the chart.</p>
	<table><thead><tr><th>Name</th><th>Title</th><th>Where it appeared</th><th>Source</th><th>Why excluded</th></tr></thead><tbody>
	{{range .Chart.EntityGate.OffEntity}}<tr><td>{{.Name}}</td><td>{{.Title}}</td><td>{{.Place}}</td><td class="src">{{.Source}}</td><td>{{.Reason}}</td></tr>{{end}}
	</tbody></table>{{end}}
</section>{{end}}
{{if .Chart.SearchTrace}}{{if .Chart.SearchTrace.ExcludedCount}}<section>
	<h2>Excluded search results</h2>
	<p class="dim">The searches returned hits the identity gate did not admit, because they do not name the company the way the gate spells it. An operating name typed differently from the legal name is the usual cause &mdash; skim the list before trusting the roster.</p>
	{{range .Chart.SearchTrace.QueriesWithCappedExclusions 10}}{{if .Excluded}}
	<h3>{{.Query}} <span class="dim">({{len .Excluded}} shown)</span></h3>
	<table><thead><tr><th>Result</th><th>Link</th></tr></thead><tbody>
	{{range .Excluded}}<tr><td>{{.Title}}</td><td class="src">{{.Link}}</td></tr>{{end}}
	</tbody></table>
	{{end}}{{end}}
</section>{{end}}{{end}}
{{if .Chart.Validation}}<section class="card">
	<h2>Data freshness</h2>
	<p class="dim">Verdicts come from the newest dated source behind each row: verified-recent means a source inside the recent window from the company's own site or from two independent domains. A dated row is a statement about the evidence, not proof of current employment.</p>
	<table><thead><tr><th>Verdict</th><th>Rows</th></tr></thead><tbody>
	<tr><td>verified-recent</td><td>{{.Chart.Validation.RowsRecent}}</td></tr>
	<tr><td>dated</td><td>{{.Chart.Validation.RowsDated}}</td></tr>
	<tr><td>stale</td><td>{{.Chart.Validation.RowsStale}}</td></tr>
	<tr><td>undated</td><td>{{.Chart.Validation.RowsUndated}}</td></tr>
	{{if .Chart.Validation.RowsUnverified}}<tr><td>current-unverified (kept, unable to verify)</td><td>{{.Chart.Validation.RowsUnverified}}</td></tr>{{end}}
	</tbody></table>
	{{if .Chart.Validation.Conflicts}}<ul>{{range .Chart.Validation.Conflicts}}<li><strong>Conflicting title:</strong> {{.}}</li>{{end}}</ul>{{end}}
	{{if .Chart.Validation.ExcludedRows}}<h3>Excluded from the current org chart</h3>
	<p class="dim">These rows were removed by current-employment checks and are retained here for auditability.</p>
	<table><thead><tr><th>Name</th><th>Title</th><th>Section</th><th>Source</th><th>Reason</th></tr></thead><tbody>
	{{range .Chart.Validation.ExcludedRows}}<tr><td>{{.Name}}</td><td>{{.Title}}</td><td>{{.Place}}</td><td class="src">{{.Source}}</td><td>{{.Reason}}</td></tr>{{end}}
	</tbody></table>{{end}}
	{{if .Chart.Validation.Lookups}}<table><thead><tr><th>Person</th><th>Reverse lookup</th><th>Outcome</th><th>Newest mention</th><th>Source</th></tr></thead><tbody>
	{{range .Chart.Validation.Lookups}}<tr><td>{{.Person}}</td><td class="src">{{.Query}}</td><td>{{.Status}}</td><td>{{.AsOf}}</td><td class="src">{{.Source}}</td></tr>{{end}}
	</tbody></table>{{end}}
	{{if .Chart.Validation.Skipped}}<p class="dim">Reverse lookups: {{.Chart.Validation.Skipped}}.</p>{{end}}
</section>{{end}}
{{if .Timings}}<section>
	<h2>Run timings</h2>
	<table><thead><tr><th>Phase</th><th>Duration (s)</th><th>Detail</th></tr></thead><tbody>
	{{range .Timings}}<tr><td>{{.Phase}}</td><td>{{.Seconds}}</td><td>{{.Detail}}</td></tr>{{end}}
	</tbody></table>
</section>{{end}}
{{if .Chart.Sources}}<section>
	<h2>Sources</h2>
	<ol>{{range .Chart.Sources}}<li class="src">{{.}}</li>{{end}}</ol>
</section>{{end}}
<footer>{{.Chart.CompanyName}} &middot; Crystal AI &middot; checked {{.GeneratedAt}}</footer>
`)
