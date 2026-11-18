package main

// ontology_resolve.go exposes the activity resolution as JSON: which branch an
// activity takes (graph match, model-confirmed alias, or unmodelled) and the
// research path that follows (product families, category lanes, discovery terms,
// and for the buyer mode the consuming stages). The console can call it before a
// run spends a search, and curation tooling can call it after editing a candidate
// document.
//
// It starts no research. The only model call is the alias probe, and it runs only
// when the graph does not know the activity verbatim.

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
)

// maxOntologyResolveBytes bounds the request body: a resolution request is three
// short strings.
const maxOntologyResolveBytes = 4 << 10

func (s *Server) handleOntologyResolve(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOntologyResolveBytes)
	var query agent.ActivityQuery
	if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(query.Activity) == "" {
		writeJSONError(w, http.StatusBadRequest, "activity is required")
		return
	}
	resolution, err := agent.ResolveActivity(r.Context(), s.engine, query)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(resolution); err != nil {
		log.Printf("[WARN] ontology resolve response: %v", err)
	}
}

// writeJSONError keeps the API's failures machine-readable instead of the HTML the
// console's form handlers render.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
