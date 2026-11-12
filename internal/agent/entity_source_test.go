package agent

import (
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

func TestClassifyDiscoverySourceKeepsReportsAsDiscoveryEvidence(t *testing.T) {
	report := models.SearchResult{
		Title: "Acetic Acid Market Research Report",
		Link:  "https://www.emergenresearch.com/industry-report/acetic-acid",
	}
	if got := classifyDiscoverySource(report); got != sourceResearchReport {
		t.Fatalf("expected research report classification, got %q", got)
	}
	if !isResearchEvidenceURL(report.Link) {
		t.Fatal("expected research publisher URL to be identified")
	}
}

func TestClassifyDiscoverySourceRecognizesOperatorPages(t *testing.T) {
	operator := models.SearchResult{
		Title: "Acetic acid production | Example Chemicals",
		Link:  "https://example-chemicals.com/products/acetic-acid",
	}
	if got := classifyDiscoverySource(operator); got != sourceOperator {
		t.Fatalf("expected operator classification, got %q", got)
	}
}
