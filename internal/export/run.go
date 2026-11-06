package export

import (
	"context"
	"encoding/json"
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
	stamp := generatedStamp(chart.LastUpdated)
	return e.exportRun(ctx, "company", chart.CompanyName,
		renderCompanyMarkdown(chart), renderCompanyHTML(chart),
		reportFooter(companyReportTitle(chart), stamp), chart.SearchTrace)
}

// ExportPerson renders and stores a finished person run's artifacts.
func (e *Exporter) ExportPerson(ctx context.Context, profile *models.PersonProfile) []models.ExportArtifact {
	if !e.Enabled() || profile == nil {
		return nil
	}
	stamp := generatedStamp(profile.LastUpdated)
	return e.exportRun(ctx, "person", profile.FullName,
		renderPersonMarkdown(profile), renderPersonHTML(profile),
		reportFooter(personReportTitle(profile), stamp), profile.SearchTrace)
}

// ExportLegal renders and stores a finished legal run's artifacts.
func (e *Exporter) ExportLegal(ctx context.Context, report *models.LegalReport) []models.ExportArtifact {
	if !e.Enabled() || report == nil {
		return nil
	}
	stamp := generatedStamp(report.LastUpdated)
	return e.exportRun(ctx, "legal", report.CompanyName,
		renderLegalMarkdown(report), renderLegalHTML(report),
		reportFooter(legalReportTitle(report), stamp), report.SearchTrace)
}

func (e *Exporter) ExportMarketLandscape(ctx context.Context, report *models.MarketLandscapeReport) []models.ExportArtifact {
	if !e.Enabled() || report == nil {
		return nil
	}
	return e.exportRun(ctx, "market-landscape", report.Activity,
		renderMarketLandscapeMarkdown(report), renderMarketLandscapeHTML(report),
		reportFooter(marketReportTitle(report), generatedStamp(report.CompletedAt)), nil)
}

// ExportDemandChain renders and stores a finished buyer-side run's artifacts.
func (e *Exporter) ExportDemandChain(ctx context.Context, report *models.DemandChainReport) []models.ExportArtifact {
	if !e.Enabled() || report == nil {
		return nil
	}
	return e.exportRun(ctx, "demand-chain", report.Activity,
		renderDemandChainMarkdown(report), renderDemandChainHTML(report),
		reportFooter(demandChainReportTitle(report), generatedStamp(report.CompletedAt)), nil)
}

// exportRun writes every enabled format, reporting one artifact per attempt: a
// failed PDF must not hide the Markdown that was stored successfully. The search
// trace — every query with its kept and excluded hits — is stored alongside them
// whenever the run recorded one, because the rendered artifacts quote the findings
// while the trace carries the findings themselves. The footer is the branded
// running footer Chromium prints on every PDF page when the renderer supports it.
func (e *Exporter) exportRun(ctx context.Context, scope, subject, markdown, html, footer string, searches *models.SearchTrace) []models.ExportArtifact {
	base := fmt.Sprintf("%s%s/%s-%s", e.prefix, scope, e.now().UTC().Format("20060102-150405"), slugify(subject))

	artifacts := make([]models.ExportArtifact, 0, 3)
	if e.formats[FormatMarkdown] {
		artifacts = append(artifacts, e.write(ctx, FormatMarkdown, base+".md", markdownContentType, []byte(markdown)))
	}
	if e.formats[FormatPDF] {
		artifacts = append(artifacts, e.writePDF(ctx, base+".pdf", html, footer))
	}
	if searches != nil && len(searches.Queries) > 0 {
		data, err := json.MarshalIndent(searches, "", "  ")
		if err != nil {
			log.Printf("[WARN] encoding the %s search trace failed: %v", scope, err)
		} else {
			artifacts = append(artifacts, e.write(ctx, FormatJSON, base+"-searches.json", searchesContentType, data))
		}
	}
	return artifacts
}

// writePDF prints the HTML document and then stores it like any other artifact.
// When the renderer can print a running footer, the branded footer (page
// numbers included) rides along; otherwise the document's own end footer is the
// only branding the pages carry.
func (e *Exporter) writePDF(ctx context.Context, objectName, html, footer string) models.ExportArtifact {
	if e.pdf == nil {
		// Markdown is still written; only the PDF needs a browser.
		return models.ExportArtifact{
			Role:   FormatPDF,
			Name:   objectName,
			Error:  "no PDF renderer is available (browser rendering is disabled)",
			Format: FormatPDF,
		}
	}
	data, err := func() ([]byte, error) {
		if footered, ok := e.pdf.(FooteredPDFRenderer); ok {
			return footered.RenderHTMLToPDFFootered(ctx, html, footer)
		}
		return e.pdf.RenderHTMLToPDF(ctx, html)
	}()
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
	if role == FormatPDF || role == FormatJSON {
		return role
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
