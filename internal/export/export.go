// Package export renders finished research into branded, durable output files
// (Markdown and PDF) and stores them where they can be found later.
//
// Rendering is deterministic and local — the Markdown and HTML are built from the
// research models, and the PDF is printed by the same Chromium that the scraper
// already ships — so an artifact never depends on a second model call.
package export

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/gcs"
)

// Content types for the formats this package produces.
const (
	markdownContentType = "text/markdown; charset=utf-8"
	pdfContentType      = "application/pdf"
	searchesContentType = "application/json; charset=utf-8"

	FormatMarkdown = "md"
	FormatPDF      = "pdf"
	// FormatJSON is the raw search-evidence artifact (-searches.json): every query the run
	// issued with its kept and excluded hits. Unlike md and pdf it is not format-gated by
	// configuration — a run's findings are always downloadable, which is the point of the
	// Search results tab.
	FormatJSON = "json"
)

// Destination stores one rendered artifact and reports where it landed.
type Destination interface {
	// Write stores data under objectName and returns the canonical URI plus a
	// browser-openable URL when the destination has one.
	Write(ctx context.Context, objectName, contentType string, data []byte) (uri, url string, err error)
	// Describe identifies the destination in logs (bucket or directory).
	Describe() string
}

// GCSDestination writes artifacts to a Cloud Storage bucket.
type GCSDestination struct {
	client          *gcs.Client
	bucket          string
	downloadBaseURL string
}

// NewGCSDestination builds a bucket destination.
func NewGCSDestination(client *gcs.Client, bucket string) *GCSDestination {
	return &GCSDestination{client: client, bucket: strings.TrimSpace(bucket)}
}

// NewGCSDestinationWithDownloadURL uses an application download endpoint for
// browser access while retaining GCS for server-side storage.
func NewGCSDestinationWithDownloadURL(client *gcs.Client, bucket, downloadBaseURL string) *GCSDestination {
	destination := NewGCSDestination(client, bucket)
	destination.downloadBaseURL = strings.TrimRight(strings.TrimSpace(downloadBaseURL), "?&")
	return destination
}

// Write uploads the artifact.
func (d *GCSDestination) Write(ctx context.Context, objectName, contentType string, data []byte) (string, string, error) {
	if d.client == nil {
		return "", "", fmt.Errorf("no Cloud Storage client is configured")
	}
	if d.bucket == "" {
		return "", "", fmt.Errorf("no Cloud Storage bucket is configured")
	}
	if err := d.client.Upload(ctx, d.bucket, objectName, contentType, data); err != nil {
		return "", "", err
	}
	downloadURL := gcs.HTTPSURL(d.bucket, objectName)
	if d.downloadBaseURL != "" {
		downloadURL = d.downloadBaseURL + "?object=" + url.QueryEscape(objectName)
	}
	return gcs.URI(d.bucket, objectName), downloadURL, nil
}

// Describe identifies the bucket for logs.
func (d *GCSDestination) Describe() string { return "gs://" + d.bucket }

// FilesystemDestination writes artifacts into a local directory. It exists for
// development, where inspecting the files directly is more useful than uploading.
type FilesystemDestination struct {
	dir string
}

// NewFilesystemDestination builds a directory destination.
func NewFilesystemDestination(dir string) *FilesystemDestination {
	return &FilesystemDestination{dir: strings.TrimSpace(dir)}
}

// Write saves the artifact, creating subdirectories as needed.
func (d *FilesystemDestination) Write(_ context.Context, objectName, _ string, data []byte) (string, string, error) {
	if d.dir == "" {
		return "", "", fmt.Errorf("no output directory is configured")
	}
	target := filepath.Join(d.dir, filepath.FromSlash(objectName))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", "", fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", "", fmt.Errorf("write %s: %w", target, err)
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		absolute = target
	}
	return absolute, "file:///" + strings.ReplaceAll(absolute, "\\", "/"), nil
}

// Describe identifies the directory for logs.
func (d *FilesystemDestination) Describe() string { return "directory " + d.dir }

// PDFRenderer prints a self-contained HTML document to PDF. *tools.Scraper
// satisfies it, which is what keeps PDF generation on the Chromium the server
// already runs rather than on an extra dependency.
type PDFRenderer interface {
	RenderHTMLToPDF(ctx context.Context, html string) ([]byte, error)
}

// FooteredPDFRenderer is an optional capability of a PDF renderer: printing a
// running footer on every page (Chromium's footerTemplate), which carries the
// brand line and the page numbers a document's own footer element cannot. An
// exporter uses it when available and degrades to the plain render otherwise,
// the same way a scraper treats its own optional capabilities.
type FooteredPDFRenderer interface {
	RenderHTMLToPDFFootered(ctx context.Context, html, footerHTML string) ([]byte, error)
}

// Exporter renders and stores the artifacts for finished runs.
type Exporter struct {
	destination Destination
	pdf         PDFRenderer
	prefix      string
	formats     map[string]bool
	now         func() time.Time
}

// New builds an exporter. A nil destination disables it, and an empty format list
// defaults to Markdown plus PDF.
func New(destination Destination, renderer PDFRenderer, prefix string, formats []string) *Exporter {
	if len(formats) == 0 {
		formats = []string{FormatMarkdown, FormatPDF}
	}
	enabled := make(map[string]bool, len(formats))
	for _, format := range formats {
		switch normalized := strings.ToLower(strings.TrimSpace(format)); normalized {
		case FormatMarkdown, FormatPDF:
			enabled[normalized] = true
		default:
			log.Printf("[WARN] ignoring unsupported output format %q (supported: md, pdf)", format)
		}
	}
	if prefix == "" {
		prefix = "WebSearch_GO/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &Exporter{
		destination: destination,
		pdf:         renderer,
		prefix:      prefix,
		formats:     enabled,
		now:         time.Now,
	}
}

// Enabled reports whether the exporter will store anything. A run must be able to
// check this, because exporting is optional infrastructure.
func (e *Exporter) Enabled() bool {
	if e == nil || e.destination == nil {
		return false
	}
	if len(e.formats) == 0 {
		return false
	}
	// PDF needs a renderer; Markdown does not.
	return e.formats[FormatMarkdown] || (e.formats[FormatPDF] && e.pdf != nil)
}

// Describe identifies the destination for the startup log.
func (e *Exporter) Describe() string {
	if e == nil || e.destination == nil {
		return "disabled"
	}
	return e.destination.Describe() + ", prefix " + e.prefix
}
