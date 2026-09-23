package tools

import (
	"bytes"
	"fmt"
	"mime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// Extractor names reported in ScrapeResult.Extractor for document scrapes. PDFs are
// read by Gemini when it is available, because it handles compressed streams,
// embedded fonts and scanned pages that a local parser cannot; the local parser is
// the fallback used when the model call fails.
const (
	ExtractorPDFGemini = "pdf-gemini"
	ExtractorPDFLocal  = "pdf-local"
)

// MaxDocumentTextBytes caps text extracted from a non-HTML document.
const MaxDocumentTextBytes = 4 << 20

// maxPDFPages bounds how many pages the local parser walks, so a pathological
// document cannot monopolise a scrape slot.
const maxPDFPages = 200

// pdfMagic is the header every PDF file starts with.
var pdfMagic = []byte("%PDF-")

// minPrintableRatio is the share of runes in an extraction that must be printable.
// Below it the text is machine noise — typically a broken font mapping or a mislabeled
// binary payload — and is dropped rather than offered to the model as evidence.
const minPrintableRatio = 0.6

// printableRatio reports the share of runes in text that are printable. Invalid UTF-8
// bytes and replacement runes count as unreadable, so a failed decode is caught too.
func printableRatio(text string) float64 {
	total, printable := 0, 0
	for _, r := range text {
		total++
		if (unicode.IsPrint(r) || unicode.IsSpace(r)) && r != utf8.RuneError {
			printable++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(printable) / float64(total)
}

// IsPDFContentType reports whether a Content-Type header describes a PDF document.
func IsPDFContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.Contains(strings.ToLower(contentType), "pdf")
	}
	return mediaType == "application/pdf" || mediaType == "application/x-pdf"
}

// isGenericBinaryContentType reports whether the media type says nothing useful about
// the payload, which is when sniffing the body is warranted.
func isGenericBinaryContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.ToLower(strings.Split(contentType, ";")[0]))
	}
	switch mediaType {
	case "", "application/octet-stream", "binary/octet-stream", "application/binary":
		return true
	}
	return false
}

// isExtractableDocument reports whether a non-HTML response is a PDF the configured
// extractor should be given. The media type is the primary signal; registry portals
// also serve filings as a generic binary stream, so for those the PDF header decides.
func isExtractableDocument(contentType string, body []byte) bool {
	if IsPDFContentType(contentType) {
		return true
	}
	return isGenericBinaryContentType(contentType) && isPDFBody(body)
}

// isPDFBody reports whether the body carries a PDF header. The format only requires
// the header to appear within the first 1024 bytes, so leading junk is tolerated.
func isPDFBody(body []byte) bool {
	head := body
	if len(head) > 1024 {
		head = head[:1024]
	}
	return bytes.Contains(head, pdfMagic)
}

// ExtractPDFText extracts text from a PDF body using a pure-Go parser. It is the
// fallback for when the model-backed extractor is unavailable, and returns an error
// when the body is not a PDF or holds no extractable text.
func ExtractPDFText(body []byte, maxBytes int) (string, error) {
	if len(body) == 0 {
		return "", fmt.Errorf("pdf body is empty")
	}
	if !isPDFBody(body) {
		return "", fmt.Errorf("body does not carry a pdf header")
	}
	if maxBytes <= 0 {
		maxBytes = MaxDocumentTextBytes
	}

	text, err := readPDFText(body, maxBytes)
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		// Scanned filings carry page images and no text layer; nothing a text
		// parser can recover.
		return "", fmt.Errorf("pdf contains no extractable text")
	}
	return text, nil
}

// readPDFText isolates the third-party parser, which is known to panic on some
// malformed files, so a bad document surfaces as an error instead of taking down the
// request goroutine.
func readPDFText(body []byte, maxBytes int) (text string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			text, err = "", fmt.Errorf("pdf parser failed on malformed input: %v", recovered)
		}
	}()

	reader, err := pdf.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "", fmt.Errorf("pdf reader: %w", err)
	}

	pages := reader.NumPage()
	if pages > maxPDFPages {
		pages = maxPDFPages
	}

	var builder strings.Builder
	for number := 1; number <= pages; number++ {
		page := reader.Page(number)
		if page.V.IsNull() {
			continue
		}
		pageText, pageErr := page.GetPlainText(nil)
		if pageErr != nil {
			// One unreadable page must not discard the readable ones.
			continue
		}
		builder.WriteString(pageText)
		builder.WriteString("\n")
		if builder.Len() >= maxBytes {
			break
		}
	}
	return builder.String(), nil
}
