// render.go draws a GraphView as SVG and as a self-contained page. No scripts and
// no CDN: the same bytes work in the console, in a saved file and in a PDF, which
// is what makes "show me the graph" dependable.
package ontology

import (
	"fmt"
	"html"
	"math"
	"sort"
	"strings"
)

// SVGOptions controls the drawing.
type SVGOptions struct {
	// Size is the square viewBox side in user units.
	Size int
	// PixelWidth renders the drawing at this CSS width inside a scrollable canvas;
	// 0 fits the container. This is the zoom: the viewBox stays the same, the
	// picture gets bigger, so a dense graph is panned by scrolling, not by squinting.
	PixelWidth int
	// LinkPrefix wraps every node in a link to LinkPrefix + node id, which is how
	// the page recentres on a click. Empty draws plain nodes.
	LinkPrefix string
	// LabelAll labels every node; otherwise only rings 0 and 1 are labelled, plus
	// hubs, so a large graph stays readable.
	LabelAll bool
}

// kindColors is the palette the legend and the nodes share.
var kindColors = map[Kind]string{
	KindSubstance:  "#38bdf8",
	KindCategory:   "#a78bfa",
	KindProcess:    "#fbbf24",
	KindEndProduct: "#34d399",
}

var edgeColors = map[string]string{
	"parent":      "#475569",
	"category":    "#64748b",
	"derived_to":  "#22d3ee",
	"consumed_in": "#fb923c",
	"operates_on": "#a3e635",
	"produces":    "#4ade80",
	"consumes":    "#fb7185",
	"hypernym":    "#f472b6",
}

// RenderSVG draws the view.
func RenderSVG(view *GraphView, options SVGOptions) string {
	size := options.Size
	if size <= 0 {
		size = 900
	}
	centre := float64(size) / 2
	radius := centre * 0.92
	var b strings.Builder
	style := ""
	if options.PixelWidth > 0 {
		style = fmt.Sprintf(` style="width:%dpx;max-width:none"`, options.PixelWidth)
	}
	fmt.Fprintf(&b, `<svg class="kg-svg" viewBox="0 0 %d %d" role="img" aria-label="Chemical value-chain graph: %d nodes, %d edges" xmlns="http://www.w3.org/2000/svg"%s>`,
		size, size, len(view.Nodes), len(view.Edges), style)
	b.WriteString(`<g class="kg-edges">`)
	positions := make(map[string]ViewNode, len(view.Nodes))
	for _, node := range view.Nodes {
		positions[node.ID] = node
	}
	for _, edge := range view.Edges {
		from, okFrom := positions[edge.From]
		to, okTo := positions[edge.To]
		if !okFrom || !okTo {
			continue
		}
		colour := edgeColors[edge.Type]
		if colour == "" {
			colour = "#475569"
		}
		width := 1.1
		if edge.Type == "hypernym" {
			width = 2.2
		}
		fmt.Fprintf(&b, `<line class="kg-edge kg-edge-%s" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="%.1f" stroke-opacity="0.55"/>`,
			cssToken(edge.Type), centre+from.X*radius, centre+from.Y*radius, centre+to.X*radius, centre+to.Y*radius, colour, width)
	}
	b.WriteString(`</g><g class="kg-nodes">`)
	for _, node := range view.Nodes {
		colour := kindColors[node.Kind]
		if colour == "" {
			colour = "#94a3b8"
		}
		nodeRadius := 6 + math.Min(float64(node.Degree), 12)
		x, y := centre+node.X*radius, centre+node.Y*radius
		b.WriteString(`<g class="kg-node"`)
		if node.Status != StatusApproved {
			b.WriteString(` data-status="proposed"`)
		}
		b.WriteString(`>`)
		if options.LinkPrefix != "" {
			fmt.Fprintf(&b, `<a href="%s%s">`, html.EscapeString(options.LinkPrefix), html.EscapeString(node.ID))
		}
		fmt.Fprintf(&b, `<title>%s — %s (%s)%s</title>`, html.EscapeString(node.Label), html.EscapeString(string(node.Kind)), html.EscapeString(node.ID), titleSuffix(node))
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" fill="%s" fill-opacity="0.9" stroke="%s" stroke-width="%.1f"/>`,
			x, y, nodeRadius, colour, statusStroke(node.Status), statusWidth(node.Status))
		labelClass := "kg-node-label-on-hover"
		if options.LabelAll || node.Ring <= 1 || node.Degree >= 6 {
			labelClass = "kg-node-label-visible"
		}
		fmt.Fprintf(&b, `<text class="kg-node-label %s" x="%.1f" y="%.1f" text-anchor="middle" font-size="13" fill="#e2e8f0">%s</text>`,
			labelClass, x, y-nodeRadius-6, html.EscapeString(node.Label))
		if options.LinkPrefix != "" {
			b.WriteString(`</a>`)
		}
		b.WriteString(`</g>`)
	}
	b.WriteString(`</g></svg>`)
	return b.String()
}

func titleSuffix(node ViewNode) string {
	parts := make([]string, 0, 3)
	if node.Status != StatusApproved {
		parts = append(parts, "status: "+string(node.Status))
	}
	if len(node.Aliases) > 0 {
		parts = append(parts, "aliases: "+strings.Join(node.Aliases, ", "))
	}
	if len(node.HSCodes) > 0 {
		parts = append(parts, "HS: "+strings.Join(node.HSCodes, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return " [" + strings.Join(parts, "; ") + "]"
}

func statusStroke(status Status) string {
	if status == StatusApproved {
		return "#0f172a"
	}
	return "#f472b6"
}

func statusWidth(status Status) float64 {
	if status == StatusApproved {
		return 1.5
	}
	return 3
}

// cssToken makes an edge or kind name safe as a class fragment.
func cssToken(value string) string {
	replacer := strings.NewReplacer("_", "-", " ", "-")
	return replacer.Replace(value)
}

// EdgeLegend returns the edge families present, in canonical order.
func EdgeLegend(view *GraphView) []string {
	legend := make([]string, 0, len(view.EdgeCounts))
	for _, edgeType := range EdgeTypes {
		if view.EdgeCounts[edgeType] > 0 {
			legend = append(legend, edgeType)
		}
	}
	return legend
}

// KindLegend returns the kinds present, in a fixed order.
func KindLegend(view *GraphView) []Kind {
	order := []Kind{KindSubstance, KindCategory, KindProcess, KindEndProduct}
	legend := make([]Kind, 0, len(order))
	for _, kind := range order {
		if view.KindCounts[string(kind)] > 0 {
			legend = append(legend, kind)
		}
	}
	return legend
}

// PageControl is one link in the page's toolbar: a zoom level, the bare-SVG export
// or the label toggle. The caller builds the URLs, so the toolbar works for the
// console (filter-preserving links) and for tooling alike.
type PageControl struct {
	Label  string
	Href   string
	Active bool
}

// PageOptions controls the standalone page.
type PageOptions struct {
	// Title heads the page; empty uses a sensible default.
	Title string
	// Action is the form target for the filters; empty drops the form (a saved
	// file has nothing to submit to).
	Action string
	// LinkPrefix wraps nodes in links that recentre the view.
	LinkPrefix string
	// SVGSize is passed to RenderSVG.
	SVGSize int
	// ZoomPercent scales the drawing inside a scrollable canvas: 100 fits the
	// container, 200 doubles it and lets the reader pan by scrolling. 0 means 100.
	ZoomPercent int
	// LabelAll labels every node, which a dense graph needs at high zoom.
	LabelAll bool
	// Controls are the toolbar links (zoom levels, full-size SVG, labels).
	Controls []PageControl
}

// RenderPage draws the whole page: filters, legend, the SVG and the node table. It
// is the same document the console serves and the CLI saves, so a link, a file and
// a screenshot all show the same picture.
func RenderPage(view *GraphView, options PageOptions) []byte {
	title := strings.TrimSpace(options.Title)
	if title == "" {
		title = "Chemical value-chain graph"
	}
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	fmt.Fprintf(&b, "<title>%s</title>", html.EscapeString(title))
	b.WriteString(pageCSS)
	b.WriteString("</head><body>")
	b.WriteString("<header><h1>" + html.EscapeString(title) + "</h1><p class=\"meta\">")
	fmt.Fprintf(&b, "revision <code>%s</code>", html.EscapeString(view.Revision))
	fmt.Fprintf(&b, " · %d node(s) · %d edge(s)", len(view.Nodes), len(view.Edges))
	if view.Seed != "" {
		fmt.Fprintf(&b, " · centred on <strong>%s</strong> (%d hop(s))", html.EscapeString(view.SeedLabel), view.Hops)
	}
	if view.ReviewedBy != "" {
		fmt.Fprintf(&b, " · reviewed by %s", html.EscapeString(view.ReviewedBy))
	}
	b.WriteString("</p></header>")

	if options.Action != "" {
		renderViewForm(&b, view, options)
	}
	for _, warning := range view.Warnings {
		fmt.Fprintf(&b, "<p class=\"warning\">%s</p>", html.EscapeString(warning))
	}

	b.WriteString("<div class=\"legend\">")
	for _, kind := range KindLegend(view) {
		fmt.Fprintf(&b, "<span class=\"chip\"><i style=\"background:%s\"></i>%s %d</span>",
			kindColors[kind], html.EscapeString(string(kind)), view.KindCounts[string(kind)])
	}
	for _, edgeType := range EdgeLegend(view) {
		fmt.Fprintf(&b, "<span class=\"chip\"><i class=\"line\" style=\"background:%s\"></i>%s %d</span>",
			edgeColor(edgeType), html.EscapeString(edgeType), view.EdgeCounts[edgeType])
	}
	if view.HiddenNodes > 0 {
		fmt.Fprintf(&b, "<span class=\"chip subtle\">%d node(s) filtered out</span>", view.HiddenNodes)
	}
	b.WriteString("</div>")

	b.WriteString("<main><section class=\"canvas\">")
	if len(options.Controls) > 0 {
		b.WriteString("<nav class=\"toolbar\">")
		for _, control := range options.Controls {
			active := ""
			if control.Active {
				active = ` class="active"`
			}
			fmt.Fprintf(&b, "<a href=\"%s\"%s>%s</a>", html.EscapeString(control.Href), active, html.EscapeString(control.Label))
		}
		b.WriteString("</nav>")
	}
	b.WriteString("<div class=\"viewport\">")
	zoom := options.ZoomPercent
	if zoom <= 0 {
		zoom = 100
	}
	size := options.SVGSize
	if size <= 0 {
		size = 900
	}
	pixelWidth := 0
	if zoom != 100 {
		pixelWidth = size * zoom / 100
	}
	if pixelWidth > 0 {
		fmt.Fprintf(&b, "<p class=\"hint\">Zoom %d%%: the canvas scrolls in both directions.</p>", zoom)
	}
	b.WriteString(RenderSVG(view, SVGOptions{Size: size, PixelWidth: pixelWidth, LinkPrefix: options.LinkPrefix, LabelAll: options.LabelAll}))
	b.WriteString("</div></section><aside class=\"panel\">")
	b.WriteString(renderNodeTable(view, options.LinkPrefix))
	b.WriteString("</aside></main>")
	b.WriteString("<footer>Deterministic layout: rings are taxonomy depth (whole graph) or hop distance (seeded). ")
	b.WriteString("The graph is data — curate <code>internal/ontology/data/chemicals.json</code>; the renderer knows no chemistry.</footer>")
	b.WriteString("</body></html>")
	return []byte(b.String())
}

// renderViewForm draws the GET form, so every filtered view is a shareable URL.
func renderViewForm(b *strings.Builder, view *GraphView, options PageOptions) {
	fmt.Fprintf(b, "<form class=\"filters\" method=\"get\" action=\"%s\">", html.EscapeString(options.Action))
	fmt.Fprintf(b, "<label>centre on <input type=\"search\" name=\"seed\" value=\"%s\" placeholder=\"activity, label or node id (e.g. oil blending)\" size=\"40\"></label>",
		html.EscapeString(view.Seed))
	b.WriteString("<label>hops <select name=\"hops\">")
	for _, hops := range []int{1, 2, 3, 4} {
		selected := ""
		if view.Hops == hops || (view.Hops == 0 && hops == DefaultViewHops) {
			selected = " selected"
		}
		fmt.Fprintf(b, "<option value=\"%d\"%s>%d</option>", hops, selected, hops)
	}
	b.WriteString("</select></label><fieldset><legend>kinds</legend>")
	for _, kind := range sortedKinds() {
		checked := ""
		if view.KindCounts[string(kind)] > 0 {
			checked = " checked"
		}
		fmt.Fprintf(b, "<label><input type=\"checkbox\" name=\"kind\" value=\"%s\"%s> %s</label>",
			html.EscapeString(string(kind)), checked, html.EscapeString(string(kind)))
	}
	b.WriteString("</fieldset><fieldset><legend>edges</legend>")
	for _, edgeType := range EdgeTypes {
		checked := ""
		if view.EdgeCounts[edgeType] > 0 || len(view.Edges) == 0 {
			checked = " checked"
		}
		fmt.Fprintf(b, "<label><input type=\"checkbox\" name=\"edge\" value=\"%s\"%s> %s</label>",
			html.EscapeString(edgeType), checked, html.EscapeString(edgeType))
	}
	b.WriteString("</fieldset><button type=\"submit\">draw</button></form>")
}

// renderNodeTable lists the drawn nodes, hubs first, with the facts a reviewer needs.
func renderNodeTable(view *GraphView, linkPrefix string) string {
	ordered := append([]ViewNode(nil), view.Nodes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Degree != ordered[j].Degree {
			return ordered[i].Degree > ordered[j].Degree
		}
		return ordered[i].Label < ordered[j].Label
	})
	var b strings.Builder
	b.WriteString("<table><thead><tr><th>node</th><th>kind</th><th>ring</th><th>degree</th></tr></thead><tbody>")
	for _, node := range ordered {
		b.WriteString("<tr><td>")
		if linkPrefix != "" {
			fmt.Fprintf(&b, "<a href=\"%s%s\">%s</a>", html.EscapeString(linkPrefix), html.EscapeString(node.ID), html.EscapeString(node.Label))
		} else {
			b.WriteString(html.EscapeString(node.Label))
		}
		fmt.Fprintf(&b, "<span class=\"id\">%s</span>", html.EscapeString(node.ID))
		if node.Status != StatusApproved {
			b.WriteString("<span class=\"badge\">" + html.EscapeString(string(node.Status)) + "</span>")
		}
		if aliases := strings.Join(node.Aliases, ", "); aliases != "" {
			fmt.Fprintf(&b, "<span class=\"id\">aliases: %s</span>", html.EscapeString(aliases))
		}
		if codes := strings.Join(node.HSCodes, ", "); codes != "" {
			fmt.Fprintf(&b, "<span class=\"id\">HS %s</span>", html.EscapeString(codes))
		}
		fmt.Fprintf(&b, "</td><td class=\"kind-%s\">%s</td><td>%d</td><td>%d</td></tr>",
			cssToken(string(node.Kind)), html.EscapeString(string(node.Kind)), node.Ring, node.Degree)
	}
	b.WriteString("</tbody></table>")
	return b.String()
}

func edgeColor(edgeType string) string {
	if colour := edgeColors[edgeType]; colour != "" {
		return colour
	}
	return "#475569"
}

const pageCSS = `<style>
:root { color-scheme: dark; }
body { margin: 0; padding: 1.5rem; background: radial-gradient(circle at 12% -10%, rgba(30,64,175,.22), transparent 40rem), #020617; color: #e2e8f0;
  font: 14px/1.5 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif; }
h1 { margin: 0 0 .25rem; font-size: 1.35rem; letter-spacing: -.01em; }
.meta { margin: 0 0 1rem; color: #94a3b8; font-size: .82rem; }
code { background: rgba(148,163,184,.14); padding: .05rem .3rem; border-radius: .3rem; }
.filters { display: flex; flex-wrap: wrap; gap: .9rem 1.2rem; align-items: flex-end; padding: .9rem 1rem; margin-bottom: 1rem;
  background: rgba(15,23,42,.72); border: 1px solid #1e293b; border-radius: .8rem; }
.filters label { display: flex; gap: .35rem; align-items: center; color: #cbd5e1; font-size: .82rem; }
.filters input[type=search], .filters select { background: #0b1120; border: 1px solid #334155; color: #e2e8f0; border-radius: .45rem; padding: .35rem .5rem; }
.filters fieldset { border: 1px solid #1e293b; border-radius: .6rem; padding: .3rem .6rem; display: flex; gap: .7rem; flex-wrap: wrap; }
.filters legend { color: #64748b; font-size: .7rem; text-transform: uppercase; letter-spacing: .12em; padding: 0 .25rem; }
.filters button { background: #4f46e5; color: white; border: 0; border-radius: .5rem; padding: .45rem .95rem; font-weight: 600; cursor: pointer; }
.filters button:hover { background: #4338ca; }
.legend { display: flex; flex-wrap: wrap; gap: .45rem; margin-bottom: 1rem; }
.chip { display: inline-flex; align-items: center; gap: .4rem; padding: .2rem .55rem; border-radius: 999px;
  background: rgba(148,163,184,.12); color: #cbd5e1; font-size: .76rem; }
.chip.subtle { color: #64748b; }
.chip i { width: .65rem; height: .65rem; border-radius: 999px; display: inline-block; }
.chip i.line { height: .18rem; border-radius: 2px; }
main { display: grid; grid-template-columns: minmax(0, 1fr) minmax(280px, 420px); gap: 1rem; align-items: start; }
@media (max-width: 980px) { main { grid-template-columns: minmax(0, 1fr); } }
.canvas { background: rgba(2,6,23,.6); border: 1px solid #1e293b; border-radius: .9rem; padding: .5rem; }
.toolbar { display: flex; flex-wrap: wrap; gap: .35rem; margin-bottom: .55rem; }
.toolbar a { padding: .2rem .6rem; border-radius: 999px; border: 1px solid #334155; color: #cbd5e1; text-decoration: none; font-size: .76rem; background: rgba(15,23,42,.7); }
.toolbar a:hover { border-color: #6366f1; color: #e2e8f0; }
.toolbar a.active { background: #4f46e5; border-color: #4f46e5; color: #fff; }
.viewport { overflow: auto; max-height: 78vh; }
.hint { margin: 0 0 .45rem; color: #64748b; font-size: .74rem; }
.kg-svg { width: 100%; height: auto; display: block; }
.kg-node text { paint-order: stroke; stroke: #020617; stroke-width: 4px; stroke-linejoin: round; font-weight: 500; }
.kg-node-label-on-hover { opacity: 0; pointer-events: none; transition: opacity .12s ease; }
.kg-node:hover .kg-node-label-on-hover, .kg-node:focus-within .kg-node-label-on-hover { opacity: 1; }
.kg-node a:hover circle, .kg-node a:focus-visible circle { stroke: #f8fafc; stroke-width: 3; }
.kg-node a:focus-visible { outline: none; }
.kg-edge-hypernym { stroke-dasharray: 6 4; }
.panel { background: rgba(15,23,42,.72); border: 1px solid #1e293b; border-radius: .9rem; padding: .3rem .6rem .6rem; max-height: 78vh; overflow: auto; }
.panel table { width: 100%; border-collapse: collapse; font-size: .78rem; }
.panel th { text-align: left; color: #64748b; font-weight: 600; text-transform: uppercase; letter-spacing: .08em; font-size: .66rem; padding: .45rem .3rem; position: sticky; top: 0; background: #0f172a; }
.panel td { padding: .3rem; border-top: 1px solid rgba(30,41,59,.8); vertical-align: top; }
.panel a { color: #7dd3fc; text-decoration: none; }
.panel a:hover { text-decoration: underline; }
.panel .id { display: block; color: #64748b; font-size: .68rem; }
.panel .badge { display: inline-block; margin-left: .35rem; padding: 0 .35rem; border-radius: .3rem; background: rgba(244,114,182,.18); color: #f9a8d4; font-size: .64rem; }
.panel .kind-substance { color: #38bdf8; } .panel .kind-category { color: #a78bfa; }
.panel .kind-process { color: #fbbf24; } .panel .kind-end-product { color: #34d399; }
.warning { margin: 0 0 1rem; padding: .5rem .75rem; border-left: 3px solid #f472b6; background: rgba(244,114,182,.1); color: #fbcfe8; font-size: .82rem; }
footer { margin-top: 1.25rem; color: #475569; font-size: .74rem; }
</style>`

// sortedKinds is the filter order the page uses.
func sortedKinds() []Kind { return []Kind{KindSubstance, KindCategory, KindProcess, KindEndProduct} }
