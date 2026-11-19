package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/web"
)

// TestOntologyMatchRendersCandidates pins the picker's contract: the current
// match is stated, the candidates are clickable chips that carry their family,
// and a confirmation is echoed as confirmed.
func TestOntologyMatchRendersCandidates(t *testing.T) {
	tmpl, err := web.Templates()
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	server := &Server{tmpl: tmpl}
	response := httptest.NewRecorder()
	server.handleOntologyMatch(response, postForm("/api/ontology/match", "query=crude+palm+oil&mode=market_landscape"))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{"Resolves to", "Palm oil", "Oleochemical feedstocks", `hx-post="/api/ontology/match"`, `hx-vals='{"ontology_node":"sub:palm-oil"}'`} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q:\n%s", want, body)
		}
	}
	// Confirming a node for this activity is echoed as confirmed and remembered.
	confirmed := httptest.NewRecorder()
	server.handleOntologyMatch(confirmed, postForm("/api/ontology/match", "query=cpo+pick+fixture&mode=demand_chain&ontology_node=sub:palm-oil"))
	if !strings.Contains(confirmed.Body.String(), "Confirmed:") {
		t.Fatalf("confirmed body = %s", confirmed.Body.String())
	}
	// The pick is remembered: the same activity with no node field stays anchored.
	remembered := httptest.NewRecorder()
	server.handleOntologyMatch(remembered, postForm("/api/ontology/match", "query=cpo+pick+fixture&mode=demand_chain"))
	if !strings.Contains(remembered.Body.String(), "Confirmed:") {
		t.Errorf("remembered body = %s, want the remembered confirmation", remembered.Body.String())
	}
}
