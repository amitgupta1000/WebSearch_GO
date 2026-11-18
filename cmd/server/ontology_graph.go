package main

// ontology_graph.go serves the knowledge graph as a picture and as JSON.
//
// The picture is a deterministic server-side SVG: no scripts, no CDN, no layout
// engine in the browser. Every node is a link, so clicking one recentres the view
// and the whole filtered state lives in the URL — a graph view is a link an
// operator can paste into a review note. The same renderer backs
// `ontology graph`, which saves the identical document to a file.

import (
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/ontology"
)

// handleOntologyGraphPage renders the interactive page.
func (s *Server) handleOntologyGraphPage(w http.ResponseWriter, r *http.Request) {
	view, err := ontologyViewFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(ontology.RenderPage(view, ontologyPageOptions(r))); err != nil {
		log.Printf("[WARN] ontology graph page: %v", err)
	}
}

// handleOntologyGraphJSON serves the same selection for tooling.
func (s *Server) handleOntologyGraphJSON(w http.ResponseWriter, r *http.Request) {
	view, err := ontologyViewFromRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	raw, err := ontology.MarshalView(view)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(raw); err != nil {
		log.Printf("[WARN] ontology graph json: %v", err)
	}
}

// ontologyViewFromRequest selects the view from the query string: seed (an id, a
// label or an activity), hops, repeated kind/edge filters and approved=1.
func ontologyViewFromRequest(r *http.Request) (*ontology.GraphView, error) {
	document, err := ontology.Shared()
	if err != nil {
		return nil, err
	}
	query := r.URL.Query()
	hops, _ := strconv.Atoi(strings.TrimSpace(query.Get("hops")))
	kinds := make([]ontology.Kind, 0, 4)
	for _, kind := range query["kind"] {
		if strings.TrimSpace(kind) != "" {
			kinds = append(kinds, ontology.Kind(strings.TrimSpace(kind)))
		}
	}
	edges := make([]string, 0, len(ontology.EdgeTypes))
	for _, edge := range query["edge"] {
		if strings.TrimSpace(edge) != "" {
			edges = append(edges, strings.TrimSpace(edge))
		}
	}
	return document.View(ontology.ViewOptions{
		Seed:         strings.TrimSpace(query.Get("seed")),
		Hops:         hops,
		Kinds:        kinds,
		Edges:        edges,
		ApprovedOnly: query.Get("approved") == "1",
	}), nil
}

// ontologySeedPrefix rebuilds the current filters as a link prefix, so a node link
// recentres the view *and* keeps the filters. seed is appended last because the
// node id is concatenated onto it.
func ontologySeedPrefix(r *http.Request) string {
	query := url.Values{}
	for key, values := range r.URL.Query() {
		if key == "seed" {
			continue
		}
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				query.Add(key, value)
			}
		}
	}
	prefix := "/ontology?"
	if encoded := query.Encode(); encoded != "" {
		prefix = "/ontology?" + encoded + "&"
	}
	return prefix + "seed="
}

// ontologyGraphSize is the SVG viewBox side; zoom scales the rendered width from it.
const ontologyGraphSize = 900

// ontologyGraphZoom reads the zoom percent, clamped to the toolbar range. An
// unset or unparseable value means 100 (fits the container).
func ontologyGraphZoom(r *http.Request) int {
	zoom, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("zoom")))
	if err != nil || zoom < 100 {
		return 100
	}
	if zoom > 300 {
		return 300
	}
	return zoom
}

// ontologyGraphLabels reports whether every node should be labelled.
func ontologyGraphLabels(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("labels"))) {
	case "1", "all", "true":
		return true
	}
	return false
}

// ontologyGraphHref rebuilds /ontology with the current filters plus overrides.
// An empty override removes the parameter, so zoom=100 is just the bare URL.
func ontologyGraphHref(r *http.Request, overrides map[string]string) string {
	query := r.URL.Query()
	for key, value := range overrides {
		if strings.TrimSpace(value) == "" {
			query.Del(key)
			continue
		}
		query.Set(key, value)
	}
	if encoded := query.Encode(); encoded != "" {
		return "/ontology?" + encoded
	}
	return "/ontology"
}

// ontologySVGHref links the toolbar export at the bare drawing, keeping the
// filters and label choice but dropping zoom (a page concern).
func ontologySVGHref(r *http.Request) string {
	query := r.URL.Query()
	query.Del("zoom")
	if encoded := query.Encode(); encoded != "" {
		return "/ontology.svg?" + encoded
	}
	return "/ontology.svg"
}

// ontologyPageOptions builds the page: the filter-preserving toolbar plus the
// zoom and label state the SVG needs.
func ontologyPageOptions(r *http.Request) ontology.PageOptions {
	zoom := ontologyGraphZoom(r)
	labels := ontologyGraphLabels(r)
	controls := make([]ontology.PageControl, 0, 6)
	for _, level := range []int{100, 150, 200, 300} {
		value := ""
		if level != 100 {
			value = strconv.Itoa(level)
		}
		controls = append(controls, ontology.PageControl{
			Label:  strconv.Itoa(level) + "%",
			Href:   ontologyGraphHref(r, map[string]string{"zoom": value}),
			Active: zoom == level,
		})
	}
	labelValue, labelText := "1", "Labels: all"
	if labels {
		labelValue, labelText = "", "Labels: main"
	}
	controls = append(controls, ontology.PageControl{
		Label:  labelText,
		Href:   ontologyGraphHref(r, map[string]string{"labels": labelValue}),
		Active: labels,
	})
	controls = append(controls, ontology.PageControl{Label: "Download SVG", Href: ontologySVGHref(r)})
	return ontology.PageOptions{
		Title:       "Chemical value-chain graph",
		Action:      "/ontology",
		LinkPrefix:  ontologySeedPrefix(r),
		SVGSize:     ontologyGraphSize,
		ZoomPercent: zoom,
		LabelAll:    labels,
		Controls:    controls,
	}
}

// handleOntologyGraphSVG serves the bare drawing the toolbar exports: the same
// selection as the page, as a standalone file with no chrome.
func (s *Server) handleOntologyGraphSVG(w http.ResponseWriter, r *http.Request) {
	view, err := ontologyViewFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	svg := ontology.RenderSVG(view, ontology.SVGOptions{Size: ontologyGraphSize, LabelAll: ontologyGraphLabels(r)})
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="chemical-value-chain.svg"`)
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write([]byte(svg)); err != nil {
		log.Printf("[WARN] ontology graph svg: %v", err)
	}
}
