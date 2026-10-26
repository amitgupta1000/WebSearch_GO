package export

import (
	"bytes"
	"html/template"
	"log"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// printCSS is embedded rather than linked: the PDF is printed by a browser that may
// have no network access at all, so an artifact must never depend on a CDN.
const printCSS = `
  @page { size: A4; margin: 16mm 14mm; }
  * { box-sizing: border-box; }
  body { font-family: "Segoe UI", Roboto, Helvetica, Arial, sans-serif; color: #101828; font-size: 10.5pt; line-height: 1.45; margin: 0; }
  header { border-bottom: 2px solid #1d2939; padding-bottom: 8px; margin-bottom: 18px; }
  h1 { font-size: 19pt; margin: 0 0 4px; }
  h2 { font-size: 13pt; margin: 20px 0 8px; border-bottom: 1px solid #d0d5dd; padding-bottom: 3px; }
  h3 { font-size: 11pt; margin: 14px 0 4px; }
  .subtitle { color: #475467; margin: 0; }
  .meta { color: #667085; font-size: 9pt; margin: 4px 0 0; }
  .dim { color: #667085; }
  .src { color: #475467; font-size: 8.5pt; word-break: break-all; }
  table { width: 100%; border-collapse: collapse; margin: 6px 0 10px; }
  th, td { text-align: left; padding: 4px 6px; border-bottom: 1px solid #eaecf0; vertical-align: top; }
  th { background: #f9fafb; font-size: 9pt; text-transform: uppercase; letter-spacing: 0.03em; color: #475467; }
  ul, ol { margin: 4px 0 10px; padding-left: 18px; }
  li { margin-bottom: 2px; }
  .dept { break-inside: avoid; page-break-inside: avoid; margin-bottom: 10px; }
  .head { margin: 0 0 4px; font-weight: 600; }
  footer { margin-top: 22px; border-top: 1px solid #d0d5dd; padding-top: 6px; color: #667085; font-size: 8.5pt; }
`

// document is the data one printed artifact is rendered from. Company, person
// and legal builds use the same struct so all templates share the shell.
type document struct {
	Title       string
	Subtitle    string
	GeneratedAt string
	Chart       *models.OrgChart
	Profile     *models.PersonProfile
	Legal       *models.LegalReport
	Timings     []models.PhaseTiming
}

// documentTemplate wraps a body in the shared print shell.
func documentTemplate(name, body string) *template.Template {
	return template.Must(template.New(name).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Title}}</title>
<style>` + printCSS + `</style>
</head>
<body>
` + body + `
</body>
</html>`))
}

var personHTMLTemplate = documentTemplate("person", `
<header>
	<h1>{{.Profile.FullName}}</h1>
	{{if .Subtitle}}<p class="subtitle">{{.Subtitle}}</p>{{end}}
	<p class="meta">Generated {{.GeneratedAt}} by WebSearch_GO &middot; confidence {{.Profile.ScorePercent}}%</p>
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
{{if .Profile.SameNamed}}<section>
	<h2>Same-named people</h2>
	<p class="dim">The dossier above is the confirmed identity's. A person search answers with several people who share the name; these are the most relevant snippets for the others, grouped by the employer each source states.</p>
	{{range .Profile.SameNamed}}
	<div class="dept">
		<h3>{{if .Employer}}{{.Employer}}{{else}}No employer stated (cannot be attributed){{end}}</h3>
		<ul>{{range .Snippets}}<li>{{.Title}}{{if .Snippet}} &mdash; {{.Snippet}}{{end}} <span class="src">({{.Link}})</span></li>{{end}}</ul>
	</div>
	{{end}}
</section>{{end}}
<footer>{{.Profile.FullName}} &middot; generated {{.GeneratedAt}} by WebSearch_GO</footer>
`)

// renderCompanyHTML renders an org chart as a printable, self-contained document.
func renderCompanyHTML(chart *models.OrgChart) string {
	return executeDocument(companyHTMLTemplate, document{
		Title:       nonEmpty(chart.CompanyName, "Company") + " — Org Chart",
		Subtitle:    joinNonEmpty([]string{chart.Domain, chart.Headquarters}, " · "),
		GeneratedAt: generatedStamp(chart.LastUpdated),
		Chart:       chart,
		Timings:     chart.Timings,
	})
}

// renderPersonHTML renders a dossier as a printable, self-contained document.
func renderPersonHTML(profile *models.PersonProfile) string {
	return executeDocument(personHTMLTemplate, document{
		Title:       nonEmpty(profile.FullName, "Person") + " — Professional Dossier",
		Subtitle:    joinNonEmpty([]string{profile.CurrentTitle, profile.CurrentCompany, profile.Location}, " · "),
		GeneratedAt: generatedStamp(profile.LastUpdated),
		Profile:     profile,
	})
}

// renderLegalHTML renders a legal check as a printable, self-contained document.
func renderLegalHTML(report *models.LegalReport) string {
	subtitle := report.Domain
	if report.RegistrationID != "" {
		subtitle = joinNonEmpty([]string{subtitle, "CIN " + report.RegistrationID}, " · ")
	}
	return executeDocument(legalHTMLTemplate, document{
		Title:       nonEmpty(report.CompanyName, "Company") + " — Legal Check",
		Subtitle:    subtitle,
		GeneratedAt: generatedStamp(report.LastUpdated),
		Legal:       report,
	})
}

var legalHTMLTemplate = documentTemplate("legal", `
<header>
	<h1>{{.Legal.CompanyName}} — Legal Check</h1>
	{{if .Subtitle}}<p class="subtitle">{{.Subtitle}}</p>{{end}}
	<p class="meta">Generated {{.GeneratedAt}} by WebSearch_GO &middot;
	{{if .Legal.EntityConfirmed}}identity confirmed by the operator{{else}}identity unconfirmed &mdash; matches lean on the name alone{{end}}</p>
</header>
{{if .Legal.Risk}}<section>
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
<footer>{{.Legal.CompanyName}} &middot; legal check &middot; generated {{.GeneratedAt}} by WebSearch_GO</footer>
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
	<p class="meta">Generated {{.GeneratedAt}} by WebSearch_GO</p>
</header>
{{if .Chart.Overview}}<section><h2>Overview</h2><p>{{.Chart.Overview}}</p></section>{{end}}
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
{{if .Chart.EntityGate}}<section>
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
{{if .Chart.Validation}}<section>
	<h2>Data freshness</h2>
	<p class="dim">Verdicts come from the newest dated source behind each row: verified-recent means a source inside the recent window from the company's own site or from two independent domains. A dated row is a statement about the evidence, not proof of current employment.</p>
	<table><thead><tr><th>Verdict</th><th>Rows</th></tr></thead><tbody>
	<tr><td>verified-recent</td><td>{{.Chart.Validation.RowsRecent}}</td></tr>
	<tr><td>dated</td><td>{{.Chart.Validation.RowsDated}}</td></tr>
	<tr><td>stale</td><td>{{.Chart.Validation.RowsStale}}</td></tr>
	<tr><td>undated</td><td>{{.Chart.Validation.RowsUndated}}</td></tr>
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
<footer>{{.Chart.CompanyName}} &middot; generated {{.GeneratedAt}} by WebSearch_GO</footer>
`)
