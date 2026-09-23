package export

import (
	"context"
	"fmt"
	"log"
	"strings"
	"unicode"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// ExportCompany renders and stores a finished company run's artifacts.
func (e *Exporter) ExportCompany(ctx context.Context, chart *models.OrgChart) []models.ExportArtifact {
	if !e.Enabled() || chart == nil {
		return nil
	}
	return e.exportRun(ctx, "company", chart.CompanyName, renderCompanyMarkdown(chart), renderCompanyHTML(chart))
}

// ExportPerson renders and stores a finished person run's artifacts.
func (e *Exporter) ExportPerson(ctx context.Context, profile *models.PersonProfile) []models.ExportArtifact {
	if !e.Enabled() || profile == nil {
		return nil
	}
	return e.exportRun(ctx, "person", profile.FullName, renderPersonMarkdown(profile), renderPersonHTML(profile))
}

// exportRun writes every enabled format, reporting one artifact per attempt: a
// failed PDF must not hide the Markdown that was stored successfully.
func (e *Exporter) exportRun(ctx context.Context, scope, subject, markdown, html string) []models.ExportArtifact {
	base := fmt.Sprintf("%s%s/%s-%s", e.prefix, scope, e.now().UTC().Format("20060102-150405"), slugify(subject))

	artifacts := make([]models.ExportArtifact, 0, 2)
	if e.formats[FormatMarkdown] {
		artifacts = append(artifacts, e.write(ctx, FormatMarkdown, base+".md", markdownContentType, []byte(markdown)))
	}
	if e.formats[FormatPDF] {
		artifacts = append(artifacts, e.writePDF(ctx, base+".pdf", html))
	}
	return artifacts
}

// writePDF prints the HTML document and then stores it like any other artifact.
func (e *Exporter) writePDF(ctx context.Context, objectName, html string) models.ExportArtifact {
	if e.pdf == nil {
		// Markdown is still written; only the PDF needs a browser.
		return models.ExportArtifact{
			Role:   FormatPDF,
			Name:   objectName,
			Error:  "no PDF renderer is available (browser rendering is disabled)",
			Format: FormatPDF,
		}
	}
	data, err := e.pdf.RenderHTMLToPDF(ctx, html)
	if err != nil {
		log.Printf("[WARN] printing %s failed: %v", objectName, err)
		return models.ExportArtifact{Role: FormatPDF, Name: objectName, Error: err.Error(), Format: FormatPDF}
	}
	return e.write(ctx, FormatPDF, objectName, pdfContentType, data)
}

// write stores one artifact and records where it landed.
func (e *Exporter) write(ctx context.Context, role, objectName, contentType string, data []byte) models.ExportArtifact {
	artifact := models.ExportArtifact{
		Role:        role,
		Name:        objectName,
		Bytes:       len(data),
		ContentType: contentType,
		Format:      formatForRole(role),
	}
	uri, url, err := e.destination.Write(ctx, objectName, contentType, data)
	if err != nil {
		artifact.Error = err.Error()
		log.Printf("[WARN] %s export failed for %s: %v", role, objectName, err)
		return artifact
	}
	artifact.URI, artifact.URL = uri, url
	log.Printf("[EXPORT] %s -> %s (%d bytes)", role, uri, len(data))
	return artifact
}

// formatForRole maps an artifact role onto the format label used by the UI.
func formatForRole(role string) string {
	if role == FormatPDF {
		return FormatPDF
	}
	return FormatMarkdown
}

// slugify reduces a subject name to a portable file-name fragment: lower-case
// ASCII letters, digits and single hyphens.
//
// Anything else (accents, punctuation, scripts without an ASCII form) is dropped
// rather than transliterated: the slug is only a human-readable convenience, while
// the timestamp is what makes a name unique.
func slugify(value string) string {
	var builder strings.Builder
	pendingSeparator := false
	for _, char := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			if pendingSeparator && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			pendingSeparator = false
			builder.WriteRune(char)
		case unicode.IsSpace(char), char == '-', char == '_', char == '.', char == '/', char == '&', char == ',', char == '(', char == ')':
			pendingSeparator = true
		default:
			// Dropped: non-portable in file names and object keys.
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}
	if slug == "" {
		slug = "research"
	}
	return slug
}
