package export

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/gcs"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// fakePrinter stands in for the browser: it records the documents it was asked to
// print and returns configurable bytes or an error.
type fakePrinter struct {
	mu      sync.Mutex
	printed []string
	bytes   []byte
	err     error
	calls   int
}

func (p *fakePrinter) RenderHTMLToPDF(_ context.Context, html string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.printed = append(p.printed, html)
	if p.err != nil {
		return nil, p.err
	}
	if len(p.bytes) == 0 {
		return []byte("%PDF-1.4 fake"), nil
	}
	return p.bytes, nil
}

func (p *fakePrinter) stats() (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, append([]string(nil), p.printed...)
}

// recordingDestination captures what was written without touching a network or disk.
type recordingDestination struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
	failOn  func(objectName string) error
}

func newRecordingDestination() *recordingDestination {
	return &recordingDestination{objects: map[string][]byte{}, types: map[string]string{}}
}

func (d *recordingDestination) Write(_ context.Context, objectName, contentType string, data []byte) (string, string, error) {
	if d.failOn != nil {
		if err := d.failOn(objectName); err != nil {
			return "", "", err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.objects[objectName] = data
	d.types[objectName] = contentType
	return "test://" + objectName, "https://example.test/" + objectName, nil
}

func (d *recordingDestination) Describe() string { return "test destination" }

func (d *recordingDestination) get(objectName string) ([]byte, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	data, ok := d.objects[objectName]
	return data, ok
}

func TestExportCompanyWritesMarkdownAndPDF(t *testing.T) {
	destination := newRecordingDestination()
	printer := &fakePrinter{}
	exporter := New(destination, printer, "WebSearch_GO/", nil)
	exporter.now = func() time.Time { return time.Date(2026, 9, 22, 11, 23, 19, 0, time.UTC) }

	artifacts := exporter.ExportCompany(context.Background(), sampleChart())
	if len(artifacts) != 2 {
		t.Fatalf("got %d artifacts, want 2 (markdown + pdf)", len(artifacts))
	}

	markdown := artifacts[0]
	if markdown.Role != FormatMarkdown || markdown.Format != FormatMarkdown {
		t.Errorf("first artifact = %+v, want the markdown", markdown)
	}
	wantName := "WebSearch_GO/company/20260922-112319-klj-resources-limited.md"
	if markdown.Name != wantName {
		t.Errorf("markdown name = %q, want %q", markdown.Name, wantName)
	}
	if markdown.URI != "test://"+wantName || markdown.URL == "" {
		t.Errorf("markdown location = %q / %q, want a URI and a URL", markdown.URI, markdown.URL)
	}
	if markdown.Failed() {
		t.Errorf("markdown export failed: %s", markdown.Error)
	}

	content, ok := destination.get(wantName)
	if !ok {
		t.Fatal("the markdown object was not written to the destination")
	}
	if !strings.Contains(string(content), "KLJ Resources Limited") {
		t.Error("the stored markdown does not contain the company name")
	}
	if got := destination.types[wantName]; !strings.Contains(got, "text/markdown") {
		t.Errorf("markdown content type = %q", got)
	}

	pdf := artifacts[1]
	if pdf.Role != FormatPDF || pdf.Format != FormatPDF {
		t.Errorf("second artifact = %+v, want the pdf", pdf)
	}
	if want := "WebSearch_GO/company/20260922-112319-klj-resources-limited.pdf"; pdf.Name != want {
		t.Errorf("pdf name = %q, want %q", pdf.Name, want)
	}
	if pdf.Bytes == 0 {
		t.Error("expected the pdf size to be recorded")
	}
	pdfContent, ok := destination.get(pdf.Name)
	if !ok || !strings.HasPrefix(string(pdfContent), "%PDF") {
		t.Error("expected the printed PDF bytes at the destination")
	}

	calls, printed := printer.stats()
	if calls != 1 {
		t.Errorf("printer called %d times, want 1", calls)
	}
	if len(printed) != 1 || !strings.Contains(printed[0], "<!doctype html>") {
		t.Error("expected a self-contained HTML document to be printed")
	}
}

func TestExportKeepsMarkdownWhenPDFFails(t *testing.T) {
	destination := newRecordingDestination()
	printer := &fakePrinter{err: errors.New("chromium is not available")}
	exporter := New(destination, printer, "WebSearch_GO/", nil)

	artifacts := exporter.ExportCompany(context.Background(), sampleChart())
	if len(artifacts) != 2 {
		t.Fatalf("got %d artifacts, want both attempts reported", len(artifacts))
	}
	if artifacts[0].Failed() {
		t.Errorf("the markdown must still be written: %s", artifacts[0].Error)
	}
	if !artifacts[1].Failed() {
		t.Error("expected the pdf artifact to record its failure")
	}
	if artifacts[1].URI != "" {
		t.Error("a failed artifact must not claim a location")
	}
	if _, ok := destination.get(artifacts[1].Name); ok {
		t.Error("no pdf object should have been written")
	}
}

func TestExportWithoutPrinterStillWritesMarkdown(t *testing.T) {
	// A server running without Chromium can still save the Markdown.
	destination := newRecordingDestination()
	exporter := New(destination, nil, "WebSearch_GO/", nil)
	if !exporter.Enabled() {
		t.Error("markdown alone must keep the exporter enabled")
	}

	artifacts := exporter.ExportCompany(context.Background(), sampleChart())
	if len(artifacts) != 2 || artifacts[0].Failed() {
		t.Fatalf("artifacts = %+v, want a stored markdown", artifacts)
	}
	if !artifacts[1].Failed() || !strings.Contains(artifacts[1].Error, "PDF") {
		t.Errorf("pdf artifact = %+v, want a clear reason", artifacts[1])
	}
}

func TestExportSkipsDisabledFormats(t *testing.T) {
	printer := &fakePrinter{}
	exporter := New(newRecordingDestination(), printer, "WebSearch_GO/", []string{"md"})

	artifacts := exporter.ExportCompany(context.Background(), sampleChart())
	if len(artifacts) != 1 || artifacts[0].Format != FormatMarkdown {
		t.Fatalf("artifacts = %+v, want markdown only", artifacts)
	}
	if calls, _ := printer.stats(); calls != 0 {
		t.Errorf("the printer was called %d times, want 0", calls)
	}
}

func TestExporterDisabledWithoutDestination(t *testing.T) {
	exporter := New(nil, &fakePrinter{}, "WebSearch_GO/", nil)
	if exporter.Enabled() {
		t.Error("without a destination there is nothing to write")
	}
	if artifacts := exporter.ExportCompany(context.Background(), sampleChart()); len(artifacts) != 0 {
		t.Errorf("got %d artifacts, want none", len(artifacts))
	}
	if artifacts := exporter.ExportPerson(context.Background(), sampleProfile()); len(artifacts) != 0 {
		t.Errorf("got %d artifacts, want none", len(artifacts))
	}
}

func TestExportPersonUsesThePersonFolder(t *testing.T) {
	destination := newRecordingDestination()
	exporter := New(destination, &fakePrinter{}, "WebSearch_GO/", nil)
	exporter.now = func() time.Time { return time.Date(2026, 9, 22, 11, 23, 19, 0, time.UTC) }

	artifacts := exporter.ExportPerson(context.Background(), sampleProfile())
	if len(artifacts) != 2 {
		t.Fatalf("got %d artifacts, want 2", len(artifacts))
	}
	want := "WebSearch_GO/person/20260922-112319-asha-rao.md"
	if artifacts[0].Name != want {
		t.Errorf("name = %q, want %q", artifacts[0].Name, want)
	}
	content, ok := destination.get(want)
	if !ok || !strings.Contains(string(content), "Professional Dossier") {
		t.Error("expected the dossier markdown at the destination")
	}
}

func TestFilesystemDestinationWritesRealFiles(t *testing.T) {
	dir := t.TempDir()
	exporter := New(NewFilesystemDestination(dir), &fakePrinter{}, "WebSearch_GO/", []string{"md"})
	exporter.now = func() time.Time { return time.Date(2026, 9, 22, 11, 23, 19, 0, time.UTC) }

	artifacts := exporter.ExportCompany(context.Background(), sampleChart())
	if len(artifacts) != 1 || artifacts[0].Failed() {
		t.Fatalf("artifacts = %+v, want one stored file", artifacts)
	}

	expected := filepath.Join(dir, "WebSearch_GO", "company", "20260922-112319-klj-resources-limited.md")
	data, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("read the exported file: %v", err)
	}
	if !strings.Contains(string(data), "KLJ Resources Limited") {
		t.Error("the exported file does not contain the result")
	}
	if !strings.HasPrefix(artifacts[0].URL, "file:///") {
		t.Errorf("URL = %q, want a file URL", artifacts[0].URL)
	}
}

func TestGCSDestinationBuildsBucketURIs(t *testing.T) {
	var mu sync.Mutex
	uploaded := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		uploaded[r.URL.Query().Get("name")] = body
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := gcs.NewClientWithHTTPClient(server.Client(), server.URL)
	exporter := New(NewGCSDestination(client, "crystal-crm-store"), &fakePrinter{}, "WebSearch_GO/", []string{"md"})
	exporter.now = func() time.Time { return time.Date(2026, 9, 22, 11, 23, 19, 0, time.UTC) }

	artifacts := exporter.ExportCompany(context.Background(), sampleChart())
	if len(artifacts) != 1 || artifacts[0].Failed() {
		t.Fatalf("artifacts = %+v, want one uploaded object", artifacts)
	}

	object := "WebSearch_GO/company/20260922-112319-klj-resources-limited.md"
	if want := "gs://crystal-crm-store/" + object; artifacts[0].URI != want {
		t.Errorf("URI = %q, want %q", artifacts[0].URI, want)
	}
	wantURL := "https://storage.googleapis.com/crystal-crm-store/" + object
	if artifacts[0].URL != wantURL {
		t.Errorf("URL = %q, want %q", artifacts[0].URL, wantURL)
	}

	mu.Lock()
	body, ok := uploaded[object]
	mu.Unlock()
	if !ok {
		t.Fatalf("the object %q was not uploaded", object)
	}
	if !strings.Contains(string(body), "KLJ Resources Limited") {
		t.Error("the uploaded object does not contain the markdown")
	}
}

func TestExportArtifactSizeLabels(t *testing.T) {
	cases := map[int]string{0: "—", 512: "512 B", 2048: "2.0 KB", 2 << 20: "2.0 MB"}
	for size, want := range cases {
		if got := (models.ExportArtifact{Bytes: size}).SizeLabel(); got != want {
			t.Errorf("SizeLabel(%d) = %q, want %q", size, got, want)
		}
	}
}
