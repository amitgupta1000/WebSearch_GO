package tools

// source_doc.go carries the normalized listing entry shared by the research
// source adapters (NFRA, SEBI, ICAI). Case-law sources return LegalSearchHit;
// these sites publish document listings and archives, which normalize here.
// The research memo's citations will carry Source, so a practitioner can see
// whether an authority came from the official register or a vendor.

// SourceDoc is one document or listing entry from a research source.
type SourceDoc struct {
	// Title is the document's own title as the source displays it.
	Title string `json:"title"`
	// URL is the document page; PDFURL is set when the full text is a PDF
	// behind the page (NFRA hosts order PDFs on its document CDN).
	URL    string `json:"url"`
	PDFURL string `json:"pdf_url,omitempty"`
	// Date is whatever the listing itself states (best effort; empty when the
	// listing carries no date — the document page or PDF may still carry one).
	Date     string `json:"date,omitempty"`
	Category string `json:"category,omitempty"`
	// Source names the site the entry came from ("nfra", "sebi", "icai").
	Source string `json:"source,omitempty"`
}
