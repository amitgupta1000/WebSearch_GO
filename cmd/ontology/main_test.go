package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/ontology"
)

// TestRunGraphWritesPageSvgAndJSON pins the three artifacts the command promises,
// and that the file it writes is the same document the console serves.
func TestRunGraphWritesPageSvgAndJSON(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "graph.html")
	if err := runGraph([]string{"-activity", "oil blending", "-hops", "2", "-out", page}); err != nil {
		t.Fatalf("page: %v", err)
	}
	raw, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("read page: %v", err)
	}
	html := string(raw)
	if !strings.Contains(html, "<!DOCTYPE html>") || !strings.Contains(html, "<svg") {
		t.Error("the page must be a document carrying the drawing")
	}
	if strings.Contains(html, "<form") || strings.Contains(html, `<a href="/ontology`) {
		t.Error("a saved page must not link back to a server it has no access to")
	}
	if !strings.Contains(html, "Lubricants") {
		t.Error("the drawing must label the resolved family")
	}

	svgPath := filepath.Join(dir, "graph.svg")
	if err := runGraph([]string{"-seed", "sub:n-butanol", "-hops", "1", "-out", svgPath}); err != nil {
		t.Fatalf("svg: %v", err)
	}
	svg, err := os.ReadFile(svgPath)
	if err != nil {
		t.Fatalf("read svg: %v", err)
	}
	if !strings.HasPrefix(string(svg), "<svg") {
		t.Errorf("svg file starts with %.40q", string(svg))
	}

	jsonPath := filepath.Join(dir, "graph.json")
	if err := runGraph([]string{"-activity", "n-butanol", "-hops", "2", "-kind", "category,process", "-out", jsonPath}); err != nil {
		t.Fatalf("json: %v", err)
	}
	encoded, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	var view ontology.GraphView
	if err := json.Unmarshal(encoded, &view); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if len(view.Nodes) == 0 {
		t.Fatal("the json view is empty")
	}
	for _, node := range view.Nodes {
		if node.Kind != ontology.KindCategory && node.Kind != ontology.KindProcess {
			t.Errorf("node %s kind = %s, want the -kind filter honoured", node.ID, node.Kind)
		}
	}
}

// TestRunGraphRejectsAnUnreadableDocument pins the error path.
func TestRunGraphRejectsAnUnreadableDocument(t *testing.T) {
	if err := runGraph([]string{"-file", filepath.Join(t.TempDir(), "missing.json")}); err == nil {
		t.Error("a missing document must fail the command")
	}
}
