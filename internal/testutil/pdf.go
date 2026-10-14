// Package testutil holds fixtures shared by tests in several packages. It is not
// imported by production code.
package testutil

import (
	"bytes"
	"fmt"
	"strings"
)

// MinimalPDF builds a small but structurally valid PDF containing one line of text.
// Cross-reference offsets are computed, so parsers that trust the xref table can read
// it. The text must not contain unbalanced parentheses.
func MinimalPDF(text string) []byte {
	return MinimalMultiPagePDF(text)
}

// MinimalMultiPagePDF builds a small but structurally valid PDF containing the given pages.
func MinimalMultiPagePDF(pages ...string) []byte {
	if len(pages) == 0 {
		pages = []string{""}
	}

	numPages := len(pages)
	kids := make([]string, numPages)
	for i := 0; i < numPages; i++ {
		kids[i] = fmt.Sprintf("%d 0 R", 3+i)
	}

	fontObjID := 3 + 2*numPages
	objects := make([]string, 0, 3+2*numPages)
	objects = append(objects, "<< /Type /Catalog /Pages 2 0 R >>")
	objects = append(objects, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), numPages))

	for i := 0; i < numPages; i++ {
		contentObjID := 3 + numPages + i
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", fontObjID, contentObjID))
	}

	for _, text := range pages {
		content := fmt.Sprintf("BT /F1 18 Tf 72 700 Td (%s) Tj ET", text)
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}

	objects = append(objects, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

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
