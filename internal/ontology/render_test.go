package ontology

import (
	"bytes"
	"strings"
	"testing"
)

// TestRenderSVGDrawsEveryNodeWithoutScripts pins the artifact: one circle per node,
// one line per edge, no scripts, and links when a prefix is given.
func TestRenderSVGDrawsEveryNodeWithoutScripts(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	view := document.View(ViewOptions{Seed: "n-butanol", Hops: 2})
	svg := RenderSVG(view, SVGOptions{LinkPrefix: "/ontology?seed="})
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatalf("svg = %.80q…, want an svg element", svg)
	}
	if strings.Contains(svg, "<script") {
		t.Error("the drawing must not carry scripts")
	}
	if got, want := strings.Count(svg, `<g class="kg-node"`), len(view.Nodes); got != want {
		t.Errorf("circles = %d, want %d", got, want)
	}
	if got := strings.Count(svg, "<line "); got != len(view.Edges) {
		t.Errorf("lines = %d, want %d", got, len(view.Edges))
	}
	if !strings.Contains(svg, `href="/ontology?seed=sub:n-butanol"`) {
		t.Error("a linked node must point at its seed URL")
	}
	if !strings.Contains(svg, "n-Butanol") {
		t.Error("labels must reach the drawing")
	}
}

// TestRenderEscapesLabels pins the escaping boundary: a label carrying markup can
// never become markup, in the SVG or in the table.
func TestRenderEscapesLabels(t *testing.T) {
	document := &Ontology{
		Revision: "test",
		Nodes: map[string]*Node{
			"sub:x": {ID: "sub:x", Kind: KindSubstance, Label: `<script>alert(1)</script>`, Status: StatusApproved},
			"cat:c": {ID: "cat:c", Kind: KindCategory, Label: "Family & Co", Status: StatusApproved},
		},
	}
	document.Nodes["sub:x"].Categories = []string{"cat:c"}
	view := document.View(ViewOptions{})
	page := string(RenderPage(view, PageOptions{Action: "/ontology", LinkPrefix: "/ontology?seed="}))
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Error("the label was rendered as markup")
	}
	if !strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("the label must be escaped, not dropped")
	}
	if !strings.Contains(page, "Family &amp; Co") {
		t.Error("an ampersand must be escaped too")
	}
}

// TestRenderPageCarriesTheChrome pins what a reader needs: the seed form, the
// legend, the counts and the node table.
func TestRenderPageCarriesTheChrome(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	view := document.View(ViewOptions{Seed: "oil blending", Hops: 2})
	page := string(RenderPage(view, PageOptions{Action: "/ontology", LinkPrefix: "/ontology?seed="}))
	for _, want := range []string{
		"<!DOCTYPE html>", "<svg", `name="seed"`, `value="oil blending"`,
		"Lubricants", "legend", "node(s) filtered out",
		`href="/ontology?seed=cat:oils"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "<script") {
		t.Error("the page must not carry scripts")
	}
	// A saved file has no server to submit to, so the form is dropped.
	standalone := string(RenderPage(view, PageOptions{}))
	if strings.Contains(standalone, "<form") {
		t.Error("a standalone page must not carry a form")
	}
	if !bytes.Contains([]byte(standalone), []byte("<svg")) {
		t.Error("a standalone page still needs the drawing")
	}
}

// TestRenderSVGLabelAllAddsLabels pins the label toggle: every node has a
// discoverable label, while LabelAll makes every label visible without hover.
func TestRenderSVGLabelAllAddsLabels(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	view := document.View(ViewOptions{})
	defaultSVG := RenderSVG(view, SVGOptions{})
	allSVG := RenderSVG(view, SVGOptions{LabelAll: true})
	if got := strings.Count(defaultSVG, `<text class="kg-node-label`); got != len(view.Nodes) {
		t.Errorf("default labels = %d, want one per node (%d)", got, len(view.Nodes))
	}
	if got := strings.Count(allSVG, "kg-node-label-visible"); got != len(view.Nodes) {
		t.Errorf("visible labels = %d, want one per node (%d)", got, len(view.Nodes))
	}
	if strings.Contains(allSVG, "kg-node-label-on-hover") {
		t.Error("LabelAll must not leave labels hover-only")
	}
}

// TestRenderPageZoomAndToolbar pins the toolbar and zoom: controls render as
// links with the active step marked, and a non-100 zoom widens the SVG inside
// the scrollable viewport and says so.
func TestRenderPageZoomAndToolbar(t *testing.T) {
	document, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	view := document.View(ViewOptions{Seed: "oil blending", Hops: 2})
	page := string(RenderPage(view, PageOptions{
		Action:      "/ontology",
		SVGSize:     900,
		ZoomPercent: 200,
		Controls: []PageControl{
			{Label: "100%", Href: "/ontology"},
			{Label: "200%", Href: "/ontology?zoom=200", Active: true},
		},
	}))
	for _, want := range []string{
		`class="toolbar"`,
		`href="/ontology"`,
		`href="/ontology?zoom=200"`,
		`class="active"`,
		`style="width:1800px;max-width:none"`,
		"Zoom 200%",
		`class="viewport"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	plain := string(RenderPage(view, PageOptions{Action: "/ontology"}))
	if strings.Contains(plain, `class="toolbar"`) {
		t.Error("a page without controls must not render a toolbar")
	}
	if strings.Contains(plain, `style="width:`) {
		t.Error("a 100% page must let the SVG fit its container")
	}
}
