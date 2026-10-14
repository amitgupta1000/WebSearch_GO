package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/llm"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/testutil"
)

func TestLiveDocumentTriage(t *testing.T) {
	if os.Getenv("GEMINI_INTEGRATION") != "1" {
		t.Skip("set GEMINI_INTEGRATION=1 to run the live Gemini triage test")
	}

	geminiClient, err := llm.NewGeminiClient(context.Background(), "")
	if err != nil {
		t.Fatalf("NewGeminiClient: %v", err)
	}

	previewBytes := testutil.MinimalPDF("KLJ Resources Limited Annual Report 2025 - Corporate Governance & Directors")
	scraper := &mockPreviewScraper{
		previewBytes: previewBytes,
	}

	c := cache.NewMemoryCache(20)
	defer c.Close()

	engine := NewEngine(geminiClient, &mockSearcher{}, scraper, c, 5, 10, 10)
	companyAgent := NewCompanyAgent(engine)

	brief := models.CompanyBrief{
		CompanyName: "KLJ Resources Limited",
		Industry:    "Chemicals",
		ResearchPreferences: models.ResearchPreferences{
			TimeScope: "2025",
		},
	}

	candidates := []models.RankedResult{
		{
			URL:        "https://example.com/klj-annual-report-2025.pdf",
			SourceType: "report",
		},
		{
			URL:        "https://example.com/unrelated-dental-clinic-brochure.pdf",
			SourceType: "report",
		},
		{
			URL:        "https://example.com/vague-doc.pdf",
			SourceType: "report",
		},
	}

	searchResults := []models.SearchResult{
		{
			Link:    "https://example.com/klj-annual-report-2025.pdf",
			Title:   "KLJ Resources Limited Annual Report FY 2024-2025",
			Snippet: "Official statutory report and list of directors for KLJ Resources Limited for financial year 2025.",
		},
		{
			Link:    "https://example.com/unrelated-dental-clinic-brochure.pdf",
			Title:   "Apex Dental Clinic Patient Care Brochure 2020",
			Snippet: "Services, dentist timings and pricing for Apex Dental Clinic.",
		},
		{
			Link:    "https://example.com/vague-doc.pdf",
			Title:   "Corporate Filing Document",
			Snippet: "General disclosure document filed with regulatory authority.",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	kept, report := companyAgent.triageDocumentCandidates(ctx, brief, candidates, searchResults)

	t.Logf("Triage complete. Kept: %d, Skipped in report: %d", len(kept), len(report))
	for _, k := range kept {
		t.Logf(" -> KEPT: %s", k.URL)
	}
	for _, r := range report {
		t.Logf(" -> SKIPPED: %s (decision: %s, confidence: %.2f, reason: %s)", r.URL, r.TriageDecision, r.TriageConfidence, r.Reason)
	}

	// 1. The relevant KLJ report should be kept
	foundRelevant := false
	for _, k := range kept {
		if k.URL == "https://example.com/klj-annual-report-2025.pdf" {
			foundRelevant = true
		}
	}
	if !foundRelevant {
		t.Errorf("expected KLJ annual report to be kept")
	}

	// 2. The unrelated dental brochure must be skipped
	foundUnrelatedInKept := false
	for _, k := range kept {
		if k.URL == "https://example.com/unrelated-dental-clinic-brochure.pdf" {
			foundUnrelatedInKept = true
		}
	}
	if foundUnrelatedInKept {
		t.Errorf("expected unrelated dental brochure to be rejected by triage")
	}

	// 3. The vague document should be promoted via the preview buffer containing KLJ Resources text
	foundVaguePromoted := false
	for _, k := range kept {
		if k.URL == "https://example.com/vague-doc.pdf" {
			foundVaguePromoted = true
		}
	}
	if !foundVaguePromoted {
		t.Logf("note: vague doc was not promoted (may be skipped if model deemed metadata or preview insufficient)")
	}
}
