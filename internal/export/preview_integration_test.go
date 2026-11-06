package export

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// TestBrandedPDFPreview prints the branded reports through the real Chromium so
// the cover page and the running footer can be eyeballed as PDFs. Opt-in:
//
//	PLAYWRIGHT_INTEGRATION=1 CRYSTAL_PREVIEW_DIR=%TEMP%\crystal-preview go test ./internal/export/ -run Preview -v
//
// The files land directly in the given directory: cover.pdf previews per scope.
func TestBrandedPDFPreview(t *testing.T) {
	if os.Getenv("PLAYWRIGHT_INTEGRATION") != "1" {
		t.Skip("set PLAYWRIGHT_INTEGRATION=1 to print a preview with real Chromium")
	}
	outDir := strings.TrimSpace(os.Getenv("CRYSTAL_PREVIEW_DIR"))
	if outDir == "" {
		t.Skip("set CRYSTAL_PREVIEW_DIR to choose where the preview PDFs are written")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	scraper := tools.NewScraperWithBrowser(nil, 15*time.Second, false)
	defer scraper.Close()
	if !scraper.DocumentPrinter() {
		t.Fatal("Chromium is not available, so no preview can be printed")
	}

	exporter := New(NewFilesystemDestination(outDir), scraper, "preview/", nil)
	for _, run := range []struct {
		name  string
		store func() []models.ExportArtifact
	}{
		{"company", func() []models.ExportArtifact { return exporter.ExportCompany(ctx, sampleChart()) }},
		{"person", func() []models.ExportArtifact {
			profile := sampleProfile()
			profile.ConfidenceScore = 0.82
			return exporter.ExportPerson(ctx, profile)
		}},
		{"legal", func() []models.ExportArtifact {
			return exporter.ExportLegal(ctx, &models.LegalReport{
				CompanyName: "Acme Robotics Pvt Ltd",
				Domain:      "acmerobotics.in",
				Summary:     "No case records matched the confirmed identity; two same-named records were kept for review.",
				Risk:        &models.LegalRiskAssessment{Level: "low", Summary: "Two stale matters, no pending exposure."},
			})
		}},
		{"demand-chain", func() []models.ExportArtifact {
			return exporter.ExportDemandChain(ctx, &models.DemandChainReport{
				Activity: "used cooking oil",
				Objective: "Find refiners and oleochemical plants that buy used cooking oil in Malaysia and Singapore.",
				ChainInferred: true,
				Chain: []models.ValueChainStage{
					{Name: "biodiesel production", Rationale: "Biodiesel is produced from used cooking oil feedstock.", Operators: []string{"biodiesel plant", "biorefinery"}, Source: "model"},
					{Name: "oleochemicals", Rationale: "Fatty acid and glycerine production consumes UCO derivatives.", Operators: []string{"oleochemical plant"}, Source: "model"},
				},
				Results: []models.MarketLandscapeCompany{{
					Name: "GreenDiesel Sdn Bhd", Domain: "greendiesel.com.my", Country: "Malaysia",
					Activity: "used cooking oil", Stage: "biodiesel production",
					Status: "verified", Confidence: 0.84, EvidenceType: "company-page",
					EvidenceSummary: "State e-procurement award names GreenDiesel as the UCO feedstock purchaser.",
					Evidence: []models.MarketLandscapeEvidence{{
						Title: "GreenDiesel wins state tender", URL: "https://tenderportal.example.com/uco-tender",
						Lane: "demand verification", ClaimType: models.DemandClaimTender,
						SourceTier: "official", Status: "tender or procurement record",
					}},
				}},
				QueriesUsed: 14, SearchHits: 37, LeadsReviewed: 37, LeadsExtracted: 9,
				LeadsRejected: 3, PagesEnriched: 6, VerificationQueries: 9, CandidatesFound: 11,
				Warnings: []string{
					"Private procurement is invisible to web research: this run only sees demand that surfaced publicly.",
					"Model-inferred stages are hypotheses; each result names the consuming stage it was tied to.",
				},
				Appendix: []models.MarketLandscapeEvidence{{
					Title: "UCO import record — customs mirror", URL: "https://customs-mirror.example.com/hs-code",
					Lane: "importer import data", ClaimType: models.DemandClaimImport,
					SourceTier: "official", Status: "candidate",
				}},
			})
		}},
	} {
		for _, artifact := range run.store() {
			if artifact.Failed() {
				t.Errorf("%s %s export failed: %s", run.name, artifact.Format, artifact.Error)
				continue
			}
			t.Logf("%s %s -> %s", run.name, artifact.Format, artifact.URI)
		}
	}
}
