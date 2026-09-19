// Package web embeds the server's HTML templates into the binary so it is
// fully self-contained and can be started from any working directory.
package web

import (
	"embed"
	"html/template"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Templates parses all embedded templates into a single set. The full page is
// templates/index.html; named fragments for HTMX responses (error_box,
// company_chart, person_dossier) live in templates/fragments.html.
func Templates() (*template.Template, error) {
	return template.ParseFS(templatesFS, "templates/*.html")
}
