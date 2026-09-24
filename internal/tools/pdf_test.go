package tools

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/testutil"
)

const samplePDFLine = "Acme Holdings Limited officers and directors"

func TestIsPDFContentType(t *testing.T) {
	cases := []struct {
		contentType string
		want        bool
	}{
		{"application/pdf", true},
		{"application/pdf; charset=binary", true},
		{"application/x-pdf", true},
		{"binary/octet-stream; filename=officers.pdf", false},
		{"text/html; charset=utf-8", false},
		{"image/png", false},
		{"", false},
	}
	for _, testCase := range cases {
		if got := IsPDFContentType(testCase.contentType); got != testCase.want {
			t.Errorf("IsPDFContentType(%q) = %v, want %v", testCase.contentType, got, testCase.want)
		}
	}
}

func TestIsExtractableDocumentSniffsGenericBinaryTypes(t *testing.T) {
	pdf := testutil.MinimalPDF(samplePDFLine)
	cases := []struct {
		name        string
		contentType string
		body        []byte
		want        bool
	}{
		{"pdf media type", "application/pdf", pdf, true},
		{"octet stream with a pdf header", "application/octet-stream", pdf, true},
		{"binary stream with a pdf header", "binary/octet-stream; filename=x.pdf", pdf, true},
		{"octet stream without a pdf header", "application/octet-stream", []byte("PK\x03\x04zip"), false},
		{"image media type is never sniffed", "image/png", pdf, false},
		{"html stays on the html path", "text/html", pdf, false},
	}
	for _, testCase := range cases {
		if got := isExtractableDocument(testCase.contentType, testCase.body); got != testCase.want {
			t.Errorf("%s: isExtractableDocument(%q) = %v, want %v", testCase.name, testCase.contentType, got, testCase.want)
		}
	}
}

func TestExtractPDFTextReadsTextLayer(t *testing.T) {
	text, err := ExtractPDFText(testutil.MinimalPDF(samplePDFLine), 0)
	if err != nil {
		t.Fatalf("ExtractPDFText: %v", err)
	}
	if !strings.Contains(text, samplePDFLine) {
		t.Fatalf("expected the PDF text to be extracted, got %q", text)
	}
}

func TestExtractPDFTextRejectsNonPDFBodies(t *testing.T) {
	cases := map[string][]byte{
		"empty":        nil,
		"html error":   []byte("<html><body>not a pdf</body></html>"),
		"binary noise": bytes.Repeat([]byte{0x00, 0x01}, 512),
	}
	for name, body := range cases {
		if _, err := ExtractPDFText(body, 0); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestExtractPDFTextSurvivesMalformedInput(t *testing.T) {
	// A truncated file must produce an error, never a panic: the parser is a
	// third-party dependency that is not hardened against malformed documents.
	body := testutil.MinimalPDF(samplePDFLine)
	if _, err := ExtractPDFText(body[:len(body)/2], 0); err == nil {
		t.Fatal("expected an error for a truncated pdf")
	}
	if _, err := ExtractPDFText([]byte("%PDF-1.4\ntrailer\n<< /Size 7 >>\n%%EOF\n"), 0); err == nil {
		t.Fatal("expected an error for a pdf without pages")
	}
}

func TestExtractPDFTextReportsScannedDocument(t *testing.T) {
	// A scanned filing has page images but no text layer; that must be reported
	// rather than passed on as empty evidence.
	if _, err := ExtractPDFText(testutil.MinimalPDF(""), 0); err == nil {
		t.Fatal("expected an error for a pdf without a text layer")
	}
}

func TestDocumentTitle(t *testing.T) {
	cases := map[string]string{
		"https://example.com/filings/officers-list.pdf": "officers-list.pdf",
		"https://example.com/a%20b.pdf":                 "a b.pdf",
		"https://example.com/":                          "example.com",
		"https://example.com/download":                  "download",
	}
	for target, want := range cases {
		if got := documentTitle(target); got != want {
			t.Errorf("documentTitle(%q) = %q, want %q", target, got, want)
		}
	}
}

func TestNormalizeDocumentTextCollapsesBlankLines(t *testing.T) {
	got := normalizeDocumentText("First line   \r\n\r\n\r\n\r\nSecond line\t\n   \n")
	if got != "First line\n\nSecond line" {
		t.Fatalf("normalizeDocumentText = %q", got)
	}
}

func TestPrintableRatioRejectsMachineNoise(t *testing.T) {
	cases := []struct {
		name string
		text string
		want float64
	}{
		{"plain text", "Acme Holdings Limited", 1},
		{"newlines and tabs are readable", "a\nb\tc", 1},
		{"non-ascii text is readable", "東京 株式会社", 1},
		{"control characters are not", "\x00\x01\x02\x03", 0},
		{"replacement runes are not", "\ufffd\ufffd\ufffd", 0},
		{"empty text is unreadable", "", 0},
	}
	for _, testCase := range cases {
		if got := printableRatio(testCase.text); got != testCase.want {
			t.Errorf("%s: printableRatio = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

// TestExtractPDFTextHonoursTheDeadline covers the guardrail a stalled ingest needed. The
// parser offers no cancellation, so the deadline is checked between pages and the extractor
// says where it stopped instead of running to its page cap while the run waits on it.
func TestExtractPDFTextHonoursTheDeadline(t *testing.T) {
	body := testutil.MinimalPDF(samplePDFLine)

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	partial, err := ExtractPDFTextPartial(expired, body, 0)
	if err == nil {
		t.Fatal("expected the deadline to be reported")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected the reason to wrap the context error, got %v", err)
	}
	if !strings.Contains(err.Error(), "stopped after") {
		t.Errorf("expected the reason to say where extraction stopped, got %v", err)
	}
	if partial != "" {
		t.Errorf("expected no text before the first page, got %q", partial)
	}

	// The all-or-nothing wrapper reports it as a failure, so a caller that cannot use
	// partial text never mistakes a cut-short document for a complete one.
	if _, err := ExtractPDFTextContext(expired, body, 0); err == nil {
		t.Error("expected the wrapper to report the deadline as an error")
	}

	// A live deadline is not an error, and the text ceiling stays a success: a long filing
	// whose first pages carry the roster is still worth keeping.
	if got, err := ExtractPDFTextContext(context.Background(), body, 0); err != nil || !strings.Contains(got, samplePDFLine) {
		t.Fatalf("expected normal extraction, got %q err=%v", got, err)
	}
	if got, err := ExtractPDFTextContext(context.Background(), body, 1); err != nil || got == "" {
		t.Fatalf("expected a text-limit hit to remain a success, got %q err=%v", got, err)
	}
}

// TestScrapeURLSkipsOversizedDocumentsBeforeDownloading pins the size guardrail: a document
// above the download limit is refused from its declared length, and the failure is terminal —
// no browser render is attempted, because rendering cannot turn a huge filing into HTML.
func TestScrapeURLSkipsOversizedDocumentsBeforeDownloading(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Length", strconv.FormatInt(documentSkipBytes+1, 10))
	}))
	defer srv.Close()

	extractCalled := false
	extract := func(_ context.Context, _ string, _ []byte) (string, string, error) {
		extractCalled = true
		return "should not be reached", ExtractorPDFLocal, nil
	}

	// The browser fallback is left at its default (off): if the refusal were ever handed to
	// the renderer instead of being reported, the error would name the browser, not the
	// download limit, and this test would fail.
	s := NewScraper(nil, time.Second, WithPrivateHostAccess(),
		WithDocumentTextExtractor(extract), WithDocumentTimeout(50*time.Millisecond))

	started := time.Now()
	_, err := s.ScrapeURL(context.Background(), srv.URL+"/filings/annual-report.pdf")
	if err == nil {
		t.Fatal("expected an oversized document to be refused")
	}
	if !strings.Contains(err.Error(), "download limit") {
		t.Fatalf("expected the reason to name the download limit, got %v", err)
	}
	if extractCalled {
		t.Error("expected no extraction attempt on a document that was never downloaded")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Errorf("expected a refusal, not a stall: took %s", elapsed)
	}
}

// TestDocumentExtractionRespectsItsBudget is the end-to-end guardrail for the stall: an
// extraction that never returns on its own must fail within the document budget, so the run
// continues with a reported failure instead of waiting.
func TestDocumentExtractionRespectsItsBudget(t *testing.T) {
	body := testutil.MinimalPDF(samplePDFLine)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	called := make(chan struct{}, 1)
	extract := func(ctx context.Context, _ string, _ []byte) (string, string, error) {
		called <- struct{}{}
		<-ctx.Done()
		return "", "", ctx.Err()
	}

	s := NewScraper(nil, time.Second, WithPrivateHostAccess(),
		WithDocumentTextExtractor(extract), WithDocumentTimeout(100*time.Millisecond))

	started := time.Now()
	_, err := s.ScrapeURL(context.Background(), srv.URL+"/filings/huge.pdf")
	if err == nil {
		t.Fatal("expected the extraction to fail once its budget was spent")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected the failure to name the deadline, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("expected the document budget to bound the extraction, took %s", elapsed)
	}
	select {
	case <-called:
	default:
		t.Fatal("expected the extractor to have been called")
	}
}

func TestScrapeURLPDFUsesDocumentExtractor(t *testing.T) {
	body := testutil.MinimalPDF(samplePDFLine)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	var seenContentType string
	var seenBody []byte
	extract := func(_ context.Context, contentType string, document []byte) (string, string, error) {
		seenContentType, seenBody = contentType, document
		return "Acme Holdings Limited\nOfficers and directors: A. Patel, B. Rao\n", ExtractorPDFGemini, nil
	}

	s := NewScraper(nil, time.Second, WithPrivateHostAccess(), WithDocumentTextExtractor(extract))
	result, err := s.ScrapeURL(context.Background(), srv.URL+"/filings/officers-list.pdf")
	if err != nil {
		t.Fatalf("ScrapeURL: %v", err)
	}
	if seenContentType != "application/pdf" || !bytes.Equal(seenBody, body) {
		t.Fatalf("extractor received %q with %d bytes", seenContentType, len(seenBody))
	}
	if result.Extractor != ExtractorPDFGemini {
		t.Errorf("Extractor = %q, want %q", result.Extractor, ExtractorPDFGemini)
	}
	if !strings.Contains(result.Content, "Officers and directors: A. Patel, B. Rao") {
		t.Errorf("Content = %q, want the extracted text", result.Content)
	}
	if result.ContentType != "application/pdf" {
		t.Errorf("ContentType = %q", result.ContentType)
	}
	if result.Title != "officers-list.pdf" {
		t.Errorf("Title = %q, want the file name", result.Title)
	}
	if result.WordCount != len(strings.Fields(result.Content)) {
		t.Errorf("WordCount = %d, want %d", result.WordCount, len(strings.Fields(result.Content)))
	}
	if result.ByteSize != len(result.Content) || result.QualityScore <= 0 {
		t.Errorf("expected a sized, scored result, got %+v", result)
	}
	if result.ContentTruncated {
		t.Error("a short document must not be reported as truncated")
	}
}

func TestScrapeURLPDFWithoutExtractorIsStillRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(testutil.MinimalPDF(samplePDFLine))
	}))
	defer srv.Close()

	if _, err := NewScraper(nil, time.Second, WithPrivateHostAccess()).ScrapeURL(context.Background(), srv.URL); err == nil {
		t.Fatal("expected the default policy to reject pdf responses")
	}
}

func TestScrapeURLNonPDFWithExtractorIsStillRejected(t *testing.T) {
	// Configuring PDF extraction must not widen the policy for other media types.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
	}))
	defer srv.Close()

	extract := func(context.Context, string, []byte) (string, string, error) {
		t.Error("the document extractor must not be called for non-PDF content")
		return "", "", nil
	}
	s := NewScraper(nil, time.Second, WithPrivateHostAccess(), WithDocumentTextExtractor(extract))
	_, err := s.ScrapeURL(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "unsupported content type") {
		t.Fatalf("expected an unsupported content type error, got %v", err)
	}
}

func TestScrapeURLPDFReportsExtractorFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(testutil.MinimalPDF(samplePDFLine))
	}))
	defer srv.Close()

	cases := map[string]DocumentTextExtractor{
		"error": func(context.Context, string, []byte) (string, string, error) {
			return "", "", errors.New("provider refused the document")
		},
		"empty text": func(context.Context, string, []byte) (string, string, error) {
			return "  \n\t ", ExtractorPDFLocal, nil
		},
		"unreadable text": func(context.Context, string, []byte) (string, string, error) {
			return "\x00\x01\x02\x03\x04\x05\x06\x07", ExtractorPDFGemini, nil
		},
	}
	for name, extract := range cases {
		s := NewScraper(nil, time.Second, WithPrivateHostAccess(), WithDocumentTextExtractor(extract))
		result, err := s.ScrapeURL(context.Background(), srv.URL)
		if err == nil {
			t.Errorf("%s: expected an error, got %+v", name, result)
			continue
		}
		if !strings.Contains(err.Error(), "document") {
			t.Errorf("%s: error should mention the document, got %v", name, err)
		}
	}
}

func TestScrapeURLPDFServedAsOctetStream(t *testing.T) {
	// Some registry portals serve filings with a generic binary content type; the
	// PDF header is what identifies them.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(testutil.MinimalPDF(samplePDFLine))
	}))
	defer srv.Close()

	extract := func(context.Context, string, []byte) (string, string, error) {
		return "Acme Holdings Limited officers", ExtractorPDFLocal, nil
	}
	s := NewScraper(nil, time.Second, WithPrivateHostAccess(), WithDocumentTextExtractor(extract))
	result, err := s.ScrapeURL(context.Background(), srv.URL+"/filing.pdf")
	if err != nil {
		t.Fatalf("ScrapeURL: %v", err)
	}
	if result.Extractor != ExtractorPDFLocal || !strings.Contains(result.Content, "Acme Holdings") {
		t.Fatalf("expected the octet-stream pdf to be extracted, got %+v", result)
	}
}

func TestScrapeURLTruncatesLongDocumentText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(testutil.MinimalPDF(samplePDFLine))
	}))
	defer srv.Close()

	extract := func(context.Context, string, []byte) (string, string, error) {
		return strings.Repeat("director ", maxContentBytes), ExtractorPDFLocal, nil
	}
	s := NewScraper(nil, time.Second, WithPrivateHostAccess(), WithDocumentTextExtractor(extract))
	result, err := s.ScrapeURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ScrapeURL: %v", err)
	}
	if !result.ContentTruncated {
		t.Fatal("expected a long document to be reported as truncated")
	}
	if !strings.Contains(result.Content, "...[content truncated]...") {
		t.Fatalf("expected the truncation marker, got %q", result.Content[maxContentBytes-40:])
	}
}
