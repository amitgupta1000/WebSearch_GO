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

// document is the data one printed artifact is rendered from. Company and person
// builds use the same struct so both templates share the shell.
type document struct {
	Title       string
	Subtitle    string
	GeneratedAt string
	Chart       *models.OrgChart
	Profile     *models.PersonProfile
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
	<table><thead><tr><th>Name</th><th>Title</th><th>Location</th><th>Public source</th></tr></thead><tbody>
	{{range .Chart.Leadership}}<tr><td>{{.Name}}</td><td>{{.Title}}</td><td>{{.Location}}</td><td class="src">{{.PublicSource}}</td></tr>{{end}}
	</tbody></table>
</section>{{end}}
{{if .Chart.Departments}}<section>
	<h2>Departments &amp; Functions</h2>
	{{range .Chart.Departments}}
	<div class="dept">
		<h3>{{.Name}}</h3>
		{{if .Head}}<p class="head">Head: {{.Head}}</p>{{end}}
		{{if .Description}}<p>{{.Description}}</p>{{end}}
		{{if .Members}}<ul>{{range .Members}}<li><strong>{{.Name}}</strong>{{if .Title}} &mdash; {{.Title}}{{end}}{{if .Location}} <span class="dim">({{.Location}})</span>{{end}}</li>{{end}}</ul>
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
