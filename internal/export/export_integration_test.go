package export

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/gcs"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// TestExportWritesTheSearchTrace pins the downloadable half: a run with a search trace
// stores a third artifact, <name>-searches.json, whose payload decodes back into the trace —
// the file the Search results tab links to.
func TestExportWritesTheSearchTrace(t *testing.T) {
	destination := newRecordingDestination()
	exporter := New(destination, nil, "WebSearch_GO/", []string{FormatMarkdown})
	profile := sampleProfile()
	profile.SearchTrace = &models.SearchTrace{Queries: []models.QueryTrace{
		{Query: "Asha Rao KLJ Resources", Purpose: "identity and profile", Kept: []models.SearchResult{
			{Title: "Asha Rao — KLJ", Link: "https://example.com/asha", Snippet: "Chief Executive Officer at KLJ Resources."},
		}},
	}}

	artifacts := exporter.ExportPerson(context.Background(), profile)
	if len(artifacts) != 2 { // markdown only (no PDF renderer) + the searches JSON
		t.Fatalf("expected the markdown and the searches artifact, got %+v", artifacts)
	}
	var searches *models.ExportArtifact
	for index := range artifacts {
		if artifacts[index].Format == FormatJSON {
			searches = &artifacts[index]
		}
	}
	if searches == nil {
		t.Fatalf("expected a json search-trace artifact, got %+v", artifacts)
	}
	if !strings.HasSuffix(searches.Name, "-searches.json") {
		t.Errorf("expected the searches artifact named <run>-searches.json, got %q", searches.Name)
	}
	if searches.ContentType != "application/json; charset=utf-8" {
		t.Errorf("expected the searches artifact stored as json, got %q", searches.ContentType)
	}
	payload, ok := destination.objects[searches.Name]
	if !ok {
		t.Fatalf("expected the searches payload recorded at %q", searches.Name)
	}
	var decoded models.SearchTrace
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("the searches payload must decode as a search trace: %v", err)
	}
	if len(decoded.Queries) != 1 || decoded.Queries[0].Query != "Asha Rao KLJ Resources" || len(decoded.Queries[0].Kept) != 1 {
		t.Fatalf("expected the trace round-tripped, got %+v", decoded)
	}

	// A run that recorded no trace stores only its rendered artifacts.
	if bare := exporter.ExportPerson(context.Background(), sampleProfile()); len(bare) != 1 {
		t.Errorf("expected no searches artifact without a trace, got %+v", bare)
	}
}

// TestExportToGCSLive verifies the whole artifact path against live
// infrastructure: a real Chromium prints the PDF, and both files are uploaded to
// the real bucket. It is opt-in because it writes to a bucket and needs a browser:
//
//	OUTPUT_INTEGRATION=1 GCS_OUTPUT_BUCKET=crystal-crm-store go test ./internal/export/ -run Live -v
//
// Objects are written under a verification prefix and deleted at the end.
func TestExportToGCSLive(t *testing.T) {
	if os.Getenv("OUTPUT_INTEGRATION") != "1" {
		t.Skip("set OUTPUT_INTEGRATION=1 (with credentials) to export against a real bucket")
	}
	bucket := strings.TrimSpace(os.Getenv("GCS_OUTPUT_BUCKET"))
	if bucket == "" {
		bucket = "crystal-crm-store"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Chromium does the typesetting, so this also proves the PDF path works on the
	// host rather than only in a unit test.
	scraper := tools.NewScraperWithBrowser(nil, 15*time.Second, false)
	defer scraper.Close()
	if !scraper.DocumentPrinter() {
		t.Fatal("Chromium is not available, so PDF export cannot be verified")
	}

	client, err := gcs.NewClient(ctx, gcs.ReadWriteScope)
	if err != nil {
		t.Fatalf("build the Cloud Storage client: %v", err)
	}
	exporter := New(NewGCSDestination(client, bucket), scraper, "WebSearch_GO/.verification/", nil)

	artifacts := exporter.ExportCompany(ctx, sampleChart())
	if len(artifacts) != 2 {
		t.Fatalf("got %d artifacts, want markdown and pdf", len(artifacts))
	}
	for _, artifact := range artifacts {
		if artifact.Failed() {
			t.Fatalf("%s export failed: %s", artifact.Format, artifact.Error)
		}
		if artifact.Bytes == 0 {
			t.Errorf("%s artifact recorded no size", artifact.Format)
		}
		if want := "gs://" + bucket + "/WebSearch_GO/.verification/"; !strings.HasPrefix(artifact.URI, want) {
			t.Errorf("%s URI = %q, want it under %q", artifact.Format, artifact.URI, want)
		}
		defer client.DeleteQuietly(context.Background(), bucket, artifact.Name)
	}

	// Read both objects back: a PDF that is not a PDF (or a truncated upload) is
	// exactly the kind of failure a size check alone would miss.
	markdown, found, err := client.Download(ctx, bucket, artifacts[0].Name)
	if err != nil || !found {
		t.Fatalf("download the markdown: found=%v err=%v", found, err)
	}
	if !strings.Contains(string(markdown), "KLJ Resources Limited") {
		t.Error("the stored markdown does not contain the research result")
	}

	pdf, found, err := client.Download(ctx, bucket, artifacts[1].Name)
	if err != nil || !found {
		t.Fatalf("download the pdf: found=%v err=%v", found, err)
	}
	if !strings.HasPrefix(string(pdf), "%PDF") {
		header := string(pdf)
		if len(header) > 8 {
			header = header[:8]
		}
		t.Errorf("the stored pdf does not start with a PDF header: %q", header)
	}

	t.Logf("markdown %s (%d bytes), pdf %s (%d bytes)",
		artifacts[0].URI, len(markdown), artifacts[1].URI, len(pdf))
}
