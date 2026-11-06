// brand.go carries the Crystal AI report identity: the palette, the type stacks,
// the wordmark and the cover photograph every branded PDF is built from.
//
// The identity mirrors the Crystal AI counterparty research deck — a sage-teal
// accent on warm off-white paper, serif display headings over a humanist sans
// body, hairline rules and cream callout cards, and a cover whose photograph is
// explicitly captioned as illustrative. Rendering stays deterministic and
// offline: the photograph is embedded in the binary and inlined as a data URI,
// because the print browser blocks remote assets.

package export

import (
	"embed"
	"encoding/base64"
	"html"
	"html/template"
	"sort"
	"strings"
	"sync/atomic"
)

// BrandName is the wordmark shown on the cover and in the running footer.
const BrandName = "Crystal AI"

// The palette, extracted from the brand deck.
const (
	brandInk        = "#251f21" // warm near-black: headings and body text
	brandAccent     = "#4e7a6e" // sage teal: kickers, labels, the checked stamp
	brandAccentSoft = "#73a89a" // lighter teal: the cover band and table accents
	brandSecondary  = "#585254" // warm grey: summaries, sources, footers
	brandCream      = "#f4efec" // warm paper: callout cards and table headers
	brandHairline   = "#ddd6cf" // rules under headings
)

// Type stacks that reproduce the deck on every host: display headings are serif
// (the deck embeds DejaVu Serif, the Linux alias of this stack) and the body is
// a humanist sans (Liberation Sans, the Linux alias of Arial).
const (
	brandSerifStack = `Georgia, "Times New Roman", "Liberation Serif", "DejaVu Serif", serif`
	brandSansStack  = `Arial, "Liberation Sans", "DejaVu Sans", "Segoe UI", Helvetica, sans-serif`
)

// Scope kickers, in the deck's small-caps voice; the CSS uppercases them.
const (
	kickerCompany = "Company research - Org chart"
	kickerPerson  = "Professional dossier"
	kickerLegal   = "Legal and regulatory check"
	kickerMarket  = "Producer/Seller Landscape research"
	kickerDemand  = "Demand chain research"
)

// coverPhotoCaption is printed under the cover photograph, in the deck's voice:
// the picture sets the register of the report, it does not claim to depict the
// researched party.
const coverPhotoCaption = "Illustrative image, not a specific facility."

// coverSummaryLimit bounds the cover paragraph: the deck's summary is a few
// quiet lines, not a wall of text.
const coverSummaryLimit = 320

// assetsFS is the bundled cover-photo library. The images are deliberately
// shared across modes and selected in rotation so repeated reports do not all
// carry the same cover.
//
//go:embed assets
var assetsFS embed.FS

var coverPhotoIndex uint64

// coverPhotoForScope returns the next embedded photograph in deterministic
// filename order. scopeKeyword remains part of the signature because callers
// render different report scopes, but the library is intentionally shared.
func coverPhotoForScope(scopeKeyword string) template.URL {
	_ = scopeKeyword
	entries, err := assetsFS.ReadDir("assets")
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		lower := strings.ToLower(entry.Name())
		if !strings.HasPrefix(lower, "cover-research-") ||
			(!strings.HasSuffix(lower, ".jpg") && !strings.HasSuffix(lower, ".jpeg")) {
			continue
		}
		names = append(names, entry.Name())
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	index := atomic.AddUint64(&coverPhotoIndex, 1) - 1
	name := names[index%uint64(len(names))]
	data, err := assetsFS.ReadFile("assets/" + name)
	if err != nil {
		return ""
	}
	return photoDataURI(data)
}

// photoDataURI inlines photograph bytes as a data URI, typed as template.URL
// so html/template's contextual URL escaping cannot percent-encode the payload.
func photoDataURI(data []byte) template.URL {
	return template.URL("data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data))
}

// coverSummary condenses a narrative into a cover paragraph: whitespace
// collapsed, and when the text is longer than the limit it is cut on the last
// complete sentence inside the window (falling back to the last word) — never
// mid-word, and an incomplete cut says so with an ellipsis.
func coverSummary(text string, limit int) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	runes := []rune(collapsed)
	if len(runes) <= limit {
		return collapsed
	}
	window := string(runes[:limit])
	if end := strings.LastIndexAny(window, ".!?"); end >= limit/2 {
		return strings.TrimSpace(window[:end+1])
	}
	if end := strings.LastIndex(window, " "); end > 0 {
		window = window[:end]
	}
	return strings.TrimRight(window, " ,;:-") + "…"
}

// reportFooter builds the running footer Chromium prints on every page: the
// brand line on the left, the page numbers on the right. Chromium renders the
// template outside the document, so it must carry inline styles and only the
// classes Chromium substitutes (pageNumber, totalPages).
func reportFooter(title, stamp string) string {
	brand := BrandName + " · " + html.EscapeString(strings.TrimSpace(title))
	if stamp = strings.TrimSpace(stamp); stamp != "" {
		brand += " · checked " + html.EscapeString(stamp)
	}
	return `<div style="width:100%;padding:0 14mm;display:flex;justify-content:space-between;align-items:flex-end;font-family:Arial,'Liberation Sans','DejaVu Sans',sans-serif;font-size:8px;color:#585254;">` +
		`<span>` + brand + `</span>` +
		`<span>Page <span class="pageNumber"></span> of <span class="totalPages"></span></span>` +
		`</div>`
}
