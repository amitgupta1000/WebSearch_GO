package main

import (
	"context"
	"log"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/config"
	"github.com/amitgupta1000/WebSearch_GO/internal/export"
	"github.com/amitgupta1000/WebSearch_GO/internal/gcs"
)

// documentPrinter is what the server needs from the scraper to print PDFs.
// *tools.Scraper satisfies it.
type documentPrinter interface {
	RenderHTMLToPDF(ctx context.Context, html string) ([]byte, error)
}

// buildExporter wires the run-output destination: a local directory when one is
// configured, otherwise the Cloud Storage bucket, otherwise nothing.
//
// Exporting is optional infrastructure, so a failure here degrades to "no
// artifacts" with a clear log line rather than stopping the server: a research run
// is still useful without a saved copy of it.
func buildExporter(ctx context.Context, cfg *config.Config, printer documentPrinter) (*export.Exporter, *gcs.Client) {
	if len(cfg.OutputFormats) == 0 {
		log.Printf("Output artifacts: disabled (OUTPUT_FORMATS is empty)")
		return nil, nil
	}

	var destination export.Destination
	var outputClient *gcs.Client
	switch {
	case strings.TrimSpace(cfg.OutputLocalDir) != "":
		// An explicit directory wins: setting it is a request for files on disk.
		destination = export.NewFilesystemDestination(cfg.OutputLocalDir)
	case strings.TrimSpace(cfg.OutputBucket) != "":
		client, err := gcs.NewClient(ctx, gcs.ReadWriteScope)
		if err != nil {
			log.Printf("[WARN] output artifacts disabled: %v", err)
			return nil, nil
		}
		outputClient = client
		destination = export.NewGCSDestinationWithDownloadURL(client, cfg.OutputBucket, "/api/exports")
	default:
		log.Printf("Output artifacts: disabled (set OUTPUT_BUCKET or OUTPUT_LOCAL_DIR)")
		return nil, nil
	}

	var pdfRenderer export.PDFRenderer
	if printer != nil {
		pdfRenderer = printer
	}
	exporter := export.New(destination, pdfRenderer, cfg.OutputPrefix, cfg.OutputFormats)
	if !exporter.Enabled() {
		log.Printf("Output artifacts: disabled (no usable format)")
		return nil, nil
	}
	log.Printf("Output artifacts: %s, formats %s", exporter.Describe(), strings.Join(cfg.OutputFormats, "+"))
	return exporter, outputClient
}
