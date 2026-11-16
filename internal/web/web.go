// Package web embeds the server's HTML templates into the binary so it is
// fully self-contained and can be started from any working directory.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/url"
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
	"linkedinURL": func(value string) string {
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err != nil {
			return ""
		}
		host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
		if host != "linkedin.com" && !strings.HasSuffix(host, ".linkedin.com") {
			return ""
		}
		return strings.TrimSpace(value)
	},
	"mul": func(value float64, multiplier float64) float64 {
		return value * multiplier
	},
	"lower": func(value string) string {
		return strings.ToLower(strings.TrimSpace(value))
	},
	"rrLocation": func(values ...string) string {
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
		return ""
	},
	"dict": func(values ...any) (map[string]any, error) {
		if len(values)%2 != 0 {
			return nil, fmt.Errorf("dict requires an even number of arguments")
		}
		result := make(map[string]any, len(values)/2)
		for index := 0; index < len(values); index += 2 {
			key, ok := values[index].(string)
			if !ok {
				return nil, fmt.Errorf("dict key %d is not a string", index/2)
			}
			result[key] = values[index+1]
		}
		return result, nil
	},
}

// Templates parses all embedded templates into a single set. The full page is
// templates/index.html; named fragments for HTMX responses (error_box,
// company_chart, person_dossier) live in templates/fragments.html.
func Templates() (*template.Template, error) {
	return template.New("templates").Funcs(funcs).ParseFS(templatesFS, "templates/*.html")
}
