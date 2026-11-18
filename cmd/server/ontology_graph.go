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
	if _, err := w.Write(ontology.RenderPage(view, ontology.PageOptions{
		Title:      "Chemical value-chain graph",
		Action:     "/ontology",
		LinkPrefix: ontologySeedPrefix(r),
	})); err != nil {
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
