package tools

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/llm"
)

func TestLivePetronasPDFScrape(t *testing.T) {
	if os.Getenv("GEMINI_INTEGRATION") != "1" {
		t.Skip("set GEMINI_INTEGRATION=1 to run the live PDF test")
	}

	geminiClient, err := llm.NewGeminiClient(context.Background(), "")
	if err != nil {
		t.Fatalf("NewGeminiClient: %v", err)
	}

	extractor := func(ctx context.Context, contentType string, body []byte) (string, string, error) {
		t.Logf("extractor received %d bytes, contentType: %s", len(body), contentType)
		text, err := geminiClient.ExtractDocumentText(ctx, contentType, body)
		if err == nil && len(text) > 0 {
			return text, ExtractorPDFGemini, nil
		}
		t.Logf("Gemini extraction failed: %v, trying local parser", err)
		localText, localErr := ExtractPDFTextPartial(ctx, body, MaxDocumentTextBytes)
		if localErr != nil && len(localText) == 0 {
			return "", "", localErr
		}
		return localText, ExtractorPDFLocal, nil
	}

	scraper := NewScraper(nil, 30*time.Second, WithPrivateHostAccess(), WithDocumentTextExtractor(extractor))
	targetURL := "https://www.petronas.com/pgb/sites/default/files/2023-04/PGB%20Governance%20and%20Financial%20Report%202022.pdf"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := scraper.ScrapeURL(ctx, targetURL)
	if err != nil {
		t.Fatalf("ScrapeURL failed: %v", err)
	}

	t.Logf("Scrape successful! Title: %s, WordCount: %d, Extractor: %s, Truncated: %v",
		res.Title, res.WordCount, res.Extractor, res.ContentTruncated)
}
