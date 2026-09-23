// Package testutil holds fixtures shared by tests in several packages. It is not
// imported by production code.
package testutil

import (
	"bytes"
	"fmt"
)

// MinimalPDF builds a small but structurally valid PDF containing one line of text.
// Cross-reference offsets are computed, so parsers that trust the xref table can read
// it. The text must not contain unbalanced parentheses.
func MinimalPDF(text string) []byte {
	content := fmt.Sprintf("BT /F1 18 Tf 72 700 Td (%s) Tj ET", text)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, body := range objects {
		offsets[index+1] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", index+1, body)
	}
	xrefOffset := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objects)+1)
	buf.WriteString("0000000000 65535 f \n")
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset)
	return buf.Bytes()
}
