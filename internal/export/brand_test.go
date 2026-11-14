package export

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// capabilityPrinter records both print paths, so a test can pin which one the
// exporter chose for a renderer that carries the optional footer capability.
type capabilityPrinter struct {
	mu       sync.Mutex
	plain    []string
	footered []string
}

func (p *capabilityPrinter) RenderHTMLToPDF(_ context.Context, html string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.plain = append(p.plain, html)
	return []byte("%PDF-1.4 fake"), nil
}

func (p *capabilityPrinter) RenderHTMLToPDFFootered(_ context.Context, html, footer string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.footered = append(p.footered, footer)
	return []byte("%PDF-1.4 fake"), nil
}

func (p *capabilityPrinter) stats() (plain, footered []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.plain...), append([]string(nil), p.footered...)
}

// TestCompanyCoverCarriesTheCrystalBrand pins the branded shell every report is
// built on: wordmark, kicker, serif title, the checked stamp, and the embedded
// photograph with its honest caption.
func TestCompanyCoverCarriesTheCrystalBrand(t *testing.T) {
	html := renderCompanyHTML(sampleChart())
	for _, expected := range []string{
		`<section class="cover">`,
		`<p class="wordmark">Crystal AI</p>`,
		`<p class="kicker">` + kickerCompany + `</p>`,
		`<h1 class="cover-title">KLJ Resources Limited</h1>`,
		`<p class="checked">Checked 2026-09-22 10:30 UTC</p>`,
		`src="data:image/jpeg;base64,`,
		`<figcaption>` + coverPhotoCaption + `</figcaption>`,
		// The palette travels in the document, not a linked stylesheet.
		brandAccent, brandAccentSoft, brandCream,
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("branded company HTML is missing %q", expected)
		}
	}
}

func TestCoverPhotoRotatesThroughTheLibrary(t *testing.T) {
	first := coverPhotoForScope("company")
	second := coverPhotoForScope("company")
	if first == "" || second == "" {
		t.Fatal("expected bundled cover photos")
	}
	if first == second {
		t.Error("successive cover-photo selections should rotate")
	}
}

// TestPersonCoverCarriesConfidencePill keeps the confidence verdict on the
// cover — and off it when the run has no confidence to claim.
func TestPersonCoverCarriesConfidencePill(t *testing.T) {
	bare := renderPersonHTML(sampleProfile())
	if strings.Contains(bare, `class="pill"`) {
		t.Error("a profile without a confidence score must not show a pill")
	}

	profile := sampleProfile()
	profile.ConfidenceScore = 0.82
	html := renderPersonHTML(profile)
	if !strings.Contains(html, `<span class="pill">Confidence 82%</span>`) {
		t.Error("expected a confidence pill on the cover")
	}
}

// TestLegalCoverCarriesIdentityAndRiskPills keeps the two verdicts a legal
// report is about — identity and exposure — on the cover.
func TestLegalCoverCarriesIdentityAndRiskPills(t *testing.T) {
	report := &models.LegalReport{
		CompanyName:     "Acme Robotics Pvt Ltd",
		Domain:          "acmerobotics.in",
		EntityConfirmed: false,
		Summary:         "No case records matched the confirmed identity.",
		Risk:            &models.LegalRiskAssessment{Level: "low", Summary: "Two stale matters, no pending exposure."},
	}
	html := renderLegalHTML(report)
	for _, expected := range []string{
		`<span class="pill">Identity unconfirmed</span>`,
		`<span class="pill">Legal risk — low</span>`,
		`<section class="card">`,
		"Two stale matters, no pending exposure.",
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("branded legal HTML is missing %q", expected)
		}
	}
}

// TestCoverSummaryStopsAtASentence keeps the cover paragraph quiet: it cuts on
// a sentence boundary inside the window rather than mid-word, and an incomplete
// cut says so with an ellipsis.
func TestCoverSummaryStopsAtASentence(t *testing.T) {
	sentence := "A specialty chemicals and mining group with plants in three states. "
	summary := coverSummary(strings.Repeat(sentence, 12), coverSummaryLimit)
	if len([]rune(summary)) > coverSummaryLimit {
		t.Errorf("summary = %d runes, want at most %d", len([]rune(summary)), coverSummaryLimit)
	}
	if !strings.HasSuffix(summary, ".") || strings.Contains(summary, "…") {
		t.Errorf("expected a complete sentence without an ellipsis, got %q", summary)
	}

	truncated := coverSummary(strings.Repeat("word ", 200), 40)
	if !strings.HasSuffix(truncated, "…") {
		t.Errorf("expected an ellipsis on a word-boundary cut, got %q", truncated)
	}
}

// TestReportFooterCarriesBrandAndPageNumbers pins the running footer: the brand
// line with the report title and the checked stamp on the left, Chromium's page
// number placeholders on the right, and hostile titles escaped.
func TestReportFooterCarriesBrandAndPageNumbers(t *testing.T) {
	footer := reportFooter("KLJ Resources Limited — Org Chart", "2026-11-01 10:30 UTC")
	for _, expected := range []string{
		BrandName + " · KLJ Resources Limited — Org Chart · checked 2026-11-01 10:30 UTC",
		`class="pageNumber"`,
		`class="totalPages"`,
	} {
		if !strings.Contains(footer, expected) {
			t.Errorf("running footer is missing %q: %q", expected, footer)
		}
	}

	if escaped := reportFooter(`<b>Acme</b> & Sons`, "2026-11-01 10:30 UTC"); strings.Contains(escaped, "<b>") {
		t.Errorf("the footer template must escape the title, got %q", escaped)
	}
}

// TestExporterPrefersTheFooteredRenderer proves the branded running footer is
// used whenever the renderer can print one — and that the plain path is still
// there for renderers without the capability.
func TestExporterPrefersTheFooteredRenderer(t *testing.T) {
	printer := &capabilityPrinter{}
	exporter := New(newRecordingDestination(), printer, "WebSearch_GO/", nil)
	exporter.now = func() time.Time { return time.Date(2026, 9, 22, 11, 23, 19, 0, time.UTC) }

	artifacts := exporter.ExportCompany(context.Background(), sampleChart())
	if len(artifacts) != 2 || artifacts[1].Failed() {
		t.Fatalf("artifacts = %+v, want markdown and a stored pdf", artifacts)
	}

	plain, footered := printer.stats()
	if len(footered) != 1 || len(plain) != 0 {
		t.Fatalf("print calls: plain=%d footered=%d, want footered only", len(plain), len(footered))
	}
	for _, expected := range []string{
		BrandName + " · KLJ Resources Limited — Org Chart · checked 2026-09-22 10:30 UTC",
		`class="pageNumber"`,
	} {
		if !strings.Contains(footered[0], expected) {
			t.Errorf("the footer passed to the renderer is missing %q: %q", expected, footered[0])
		}
	}
}

// TestMarketCoverRenders keeps the fourth scope honest: a producer/seller
// landscape run gets the same branded cover, with its objective as the summary.
func TestMarketCoverRenders(t *testing.T) {
	report := &models.MarketLandscapeReport{
		Activity:    "Crude oil trading",
		Objective:   "Map the active counterparties in the crude trading desk's patch.",
		CompletedAt: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
	}
	html := renderMarketLandscapeHTML(report)
	for _, expected := range []string{
		`<p class="kicker">` + kickerMarket + `</p>`,
		`<h1 class="cover-title">Crude oil trading</h1>`,
		"<h1>Crude oil trading — Producer/Seller Landscape</h1>",
		"Map the active counterparties",
		`<p class="checked">Checked 2026-11-01 09:00 UTC</p>`,
		`src="data:image/jpeg;base64,`,
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("branded market HTML is missing %q", expected)
		}
	}
}

// TestDemandChainMarkdownAndHTMLCarryTheChain pins the buyer-side branded
// report: the value chain leads the body, the demand claims travel with the
// evidence, and the cover carries the demand-chain kicker.
func TestDemandChainMarkdownAndHTMLCarryTheChain(t *testing.T) {
	report := &models.DemandChainReport{
		ResearchID: "research-1", Activity: "used cooking oil",
		Objective: "Find refiners and oleochemical plants that buy used cooking oil.",
		Geography: "Malaysia",
		Chain: []models.ValueChainStage{{
			Name: "biodiesel production", Rationale: "Biodiesel is produced from used cooking oil.",
			Operators: []string{"biodiesel plant"}, Source: "model",
		}},
		ChainInferred: true,
		Results: []models.MarketLandscapeCompany{{
			Name: "GreenDiesel Sdn Bhd", Domain: "greendiesel.com.my", Country: "Malaysia",
			Activity: "used cooking oil", Stage: "biodiesel production",
			Status: "verified", Confidence: 0.86, EvidenceType: "company-page",
			Evidence: []models.MarketLandscapeEvidence{{
				Title: "GreenDiesel wins state tender", URL: "https://tenderportal.example.com/uco-tender",
				Lane: "demand verification", ClaimType: models.DemandClaimTender,
				SourceTier: "official", Status: "tender or procurement record",
			}},
		}},
		QueriesUsed: 12, GapQueries: 3, SearchHits: 31, LeadsReviewed: 31, VerificationQueries: 6,
		StageCandidates: []models.StageCandidates{
			{
				Stage: models.ValueChainStage{
					Name: "biodiesel production", Rationale: "Biodiesel is produced from used cooking oil feedstock.",
					Operators: []string{"biodiesel plant"}, Source: "model",
				},
				Companies: []models.MarketLandscapeCompany{{
					Name: "Jayant Oleochemicals", Country: "India", Status: "lead", Confidence: 0.45,
					EvidenceType: "discovery-lead",
				}},
			},
			{
				Stage: models.ValueChainStage{
					Name: "Distribution and wholesale", Source: "category", Distance: models.StageDistanceDirect,
					Rationale: "Distributors buy to resell.",
				},
				Companies: []models.MarketLandscapeCompany{{
					Name: "Delta Distribution Pvt Ltd", Country: "India", Status: "lead", Confidence: 0.45,
					EvidenceType: "company-page",
				}},
			},
		},
		StageCoverage: []models.DemandStageCoverage{
			{Stage: "biodiesel production", Count: 6, Minimum: 6, Pass: true},
			{Stage: "Distribution and wholesale", Count: 5, Minimum: 6, Pass: false},
		},
		Reviews: []models.DemandChainReview{{
			Verdict: "revise", Score: 0.7, Reason: "one stage under-covered",
			Rejected:      []models.DemandRejectedName{{Name: "ACS Publications", Reason: "publisher"}},
			MissingStages: []string{"Distribution and wholesale"},
		}},
		Appendix: []models.MarketLandscapeEvidence{{
			Title: "UCO import record — customs mirror", URL: "https://customs-mirror.example.com/hs-code",
			Lane: "importer import data", ClaimType: models.DemandClaimImport,
			SourceTier: "official", Status: "candidate",
		}},
		Warnings: []string{"Private procurement is invisible to web research: this run only sees demand that surfaced publicly."},
	}

	markdown := renderDemandChainMarkdown(report)
	for _, expected := range []string{
		"# used cooking oil — Demand Chain",
		"Geography: Malaysia",
		"## Value chain", "biodiesel production", "Biodiesel is produced from used cooking oil.",
		"## Results — verified consumers", "GreenDiesel Sdn Bhd", "biodiesel production",
		"## Potential buyers by stage", "### biodiesel production", "Jayant Oleochemicals",
		"channel category (resale)", "Delta Distribution Pvt Ltd",
		"## Stage coverage — QC minimum", "Distribution and wholesale", "FAIL",
		"## Quality review", "ACS Publications", "publisher",
		"gap/QC queries", "import-record", "Private procurement is invisible",
	} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("demand-chain markdown is missing %q", expected)
		}
	}

	html := renderDemandChainHTML(report)
	for _, expected := range []string{
		`<p class="kicker">` + kickerDemand + `</p>`,
		`<h1 class="cover-title">used cooking oil</h1>`,
		`<p class="cover-meta">Malaysia</p>`,
		`src="data:image/jpeg;base64,`,
		"Value chain", "Operator vocabulary",
		"Potential buyers by stage", "Jayant Oleochemicals",
		"channel category (resale)", "Delta Distribution Pvt Ltd",
		"Stage coverage — QC minimum", "Quality review", "ACS Publications", "publisher",
		"gap/QC queries", "GreenDiesel Sdn Bhd", "biodiesel production",
		"Honesty notes", "Private procurement is invisible",
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("demand-chain HTML is missing %q", expected)
		}
	}
}

// TestPlainRendererStillPrints keeps the fallback honest: a renderer without
// the footer capability is still used, and the export still succeeds.
func TestPlainRendererStillPrints(t *testing.T) {
	printer := &fakePrinter{}
	exporter := New(newRecordingDestination(), printer, "WebSearch_GO/", []string{FormatPDF})
	exporter.now = func() time.Time { return time.Date(2026, 9, 22, 11, 23, 19, 0, time.UTC) }

	artifacts := exporter.ExportCompany(context.Background(), sampleChart())
	if len(artifacts) != 1 || artifacts[0].Failed() {
		t.Fatalf("artifacts = %+v, want one stored pdf", artifacts)
	}
	if calls, _ := printer.stats(); calls != 1 {
		t.Errorf("printer called %d times, want 1", calls)
	}
}
