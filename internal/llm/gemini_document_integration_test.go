package llm

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/testutil"
)

// TestGeminiExtractsTextFromInlinePDF is an opt-in check that Gemini can read a PDF
// sent as inline data, which is the preferred document-extraction path.
// Enable it with GEMINI_INTEGRATION=1 (requires Gemini credentials).
func TestGeminiExtractsTextFromInlinePDF(t *testing.T) {
	if os.Getenv("GEMINI_INTEGRATION") != "1" {
		t.Skip("set GEMINI_INTEGRATION=1 to run the Gemini integration test")
	}

	client, err := NewGeminiClient(context.Background(), os.Getenv("GEMINI_MODEL"))
	if err != nil {
		t.Fatalf("NewGeminiClient: %v", err)
	}

	document := testutil.MinimalPDF("KLJ Resources Limited - Head of Procurement: Rajesh Zaveri")
	text, err := client.ExtractDocumentText(context.Background(), "application/pdf", document)
	if err != nil {
		t.Fatalf("ExtractDocumentText: %v", err)
	}
	t.Logf("extracted (%d bytes): %q", len(text), text)

	lowered := strings.ToLower(text)
	for _, want := range []string{"procurement", "zaveri"} {
		if !strings.Contains(lowered, want) {
			t.Errorf("expected %q in the extracted text, got %q", want, text)
		}
	}
}
