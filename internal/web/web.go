// Package web embeds the server's HTML templates into the binary so it is
// fully self-contained and can be started from any working directory.
package web

import (
	"embed"
	"html/template"
	"strings"
)

//go:embed templates/*.html
var templatesFS embed.FS

// funcs are the helpers the fragments use beyond html/template's built-ins.
var funcs = template.FuncMap{
	// hasScheme reports whether a URL already carries its own scheme, so a
	// relative document path (/doc/…) can be prefixed with its source host.
	"hasScheme": func(value string) bool {
		lowered := strings.ToLower(strings.TrimSpace(value))
		return strings.HasPrefix(lowered, "http://") || strings.HasPrefix(lowered, "https://")
	},
}

// Templates parses all embedded templates into a single set. The full page is
// templates/index.html; named fragments for HTMX responses (error_box,
// company_chart, person_dossier) live in templates/fragments.html.
func Templates() (*template.Template, error) {
	return template.New("templates").Funcs(funcs).ParseFS(templatesFS, "templates/*.html")
}
