package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/ontology"
)

// TestHandleOntologyGraphPageDrawsTheGraph pins the page: an SVG, the filters, and
// node links that keep the current filters.
func TestHandleOntologyGraphPageDrawsTheGraph(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/ontology?seed=n-butanol&hops=2", nil)
	response := httptest.NewRecorder()
	server.handleOntologyGraphPage(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Errorf("content type = %q", contentType)
	}
	page := response.Body.String()
	for _, want := range []string{"<svg", `value="n-butanol"`, "n-Butanol"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// A click must recentre *and* keep the hop filter.
	if !strings.Contains(page, `href="/ontology?hops=2&amp;seed=sub:`) {
		t.Errorf("node links must carry the current filters: %s", firstLink(page))
	}
}

// TestHandleOntologyGraphJSONServesTheSelection pins the machine-readable form.
func TestHandleOntologyGraphJSONServesTheSelection(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/api/ontology/graph?seed=oil%20blending&hops=1&kind=category", nil)
	response := httptest.NewRecorder()
	server.handleOntologyGraphJSON(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var view ontology.GraphView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Seed != "oil blending" || len(view.Nodes) == 0 || len(view.Nodes) >= 162 {
		t.Errorf("view = %q with %d node(s), want a bounded selection", view.Seed, len(view.Nodes))
	}
	for _, node := range view.Nodes {
		if node.Kind != ontology.KindCategory {
			t.Errorf("node %s kind = %s, want the category filter honoured", node.ID, node.Kind)
		}
	}
}

// TestHandleOntologyGraphUnknownSeedIsHonest pins the failure mode: a seed the graph
// cannot place draws everything and says so, rather than showing an empty canvas.
func TestHandleOntologyGraphJSONUsesReadableDefaultEdges(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/api/ontology/graph", nil)
	response := httptest.NewRecorder()
	server.handleOntologyGraphJSON(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var view ontology.GraphView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, edge := range view.Edges {
		switch edge.Type {
		case "parent", "category", "hypernym":
			t.Errorf("default graph includes taxonomy edge %q", edge.Type)
		}
	}
}

func TestHandleOntologyGraphUnknownSeedIsHonest(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/api/ontology/graph?seed=unobtainium", nil)
	response := httptest.NewRecorder()
	server.handleOntologyGraphJSON(response, request)
	var view ontology.GraphView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.Warnings) == 0 || len(view.Nodes) == 0 {
		t.Errorf("view = %d node(s) %v, want the whole graph plus a warning", len(view.Nodes), view.Warnings)
	}
}

func firstLink(page string) string {
	index := strings.Index(page, `href="/ontology?`)
	if index < 0 {
		return "(no node link)"
	}
	end := index + 80
	if end > len(page) {
		end = len(page)
	}
	return page[index:end]
}

// TestHandleOntologyGraphPageZoomAndToolbar pins the console toolbar: the zoom
// and label links preserve the filters, and the requested zoom widens the SVG.
func TestHandleOntologyGraphPageZoomAndToolbar(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/ontology?seed=n-butanol&hops=2&zoom=200", nil)
	response := httptest.NewRecorder()
	server.handleOntologyGraphPage(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	page := response.Body.String()
	for _, want := range []string{
		`class="toolbar"`,
		`href="/ontology?hops=2&amp;seed=n-butanol&amp;zoom=200"`,
		`style="width:1800px;max-width:none"`,
		"/ontology.svg?",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// TestHandleOntologyGraphSVGExportsTheDrawing pins the toolbar export: a bare
// SVG file with no page chrome.
func TestHandleOntologyGraphSVGExportsTheDrawing(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/ontology.svg?seed=n-butanol&hops=2&labels=1", nil)
	response := httptest.NewRecorder()
	server.handleOntologyGraphSVG(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "image/svg") {
		t.Errorf("content type = %q", contentType)
	}
	if disposition := response.Header().Get("Content-Disposition"); !strings.Contains(disposition, "attachment") {
		t.Errorf("disposition = %q, want an attachment", disposition)
	}
	body := response.Body.String()
	if !strings.HasPrefix(body, "<svg") || strings.Contains(body, "<html") {
		t.Errorf("body is not a bare svg: %.60q", body)
	}
}
