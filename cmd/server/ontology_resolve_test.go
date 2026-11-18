package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
)

// postJSON builds a JSON API request, the shape the resolution endpoint accepts.
func postJSON(path, body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
}

// TestHandleOntologyResolveExplainsThePath pins the endpoint's contract: a modelled
// activity answers deterministically with the families, the category lanes and the
// branch, and spends no model call (the test server has no engine).
func TestHandleOntologyResolveExplainsThePath(t *testing.T) {
	server := &Server{}
	response := httptest.NewRecorder()
	server.handleOntologyResolve(response, postJSON("/api/ontology/resolve", `{"activity":"oil blending","geography":"Middle East","mode":"seller"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Errorf("content type = %q, want JSON", contentType)
	}
	var resolution agent.ActivityResolution
	if err := json.Unmarshal(response.Body.Bytes(), &resolution); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resolution.Path != agent.ActivityPathOntology || !resolution.Matched {
		t.Errorf("path = %q matched = %v, want the ontology branch", resolution.Path, resolution.Matched)
	}
	if resolution.Category != "Lubricants" {
		t.Errorf("category = %q, want Lubricants", resolution.Category)
	}
	if len(resolution.CategoryLanes) == 0 || !strings.Contains(resolution.CategoryLanes[0], "Lubricants manufacturers") {
		t.Errorf("lanes = %v, want the lubricants category lane", resolution.CategoryLanes)
	}
	if len(resolution.Families) < 2 || resolution.Families[0].ID != "cat:lubricants" {
		t.Errorf("families = %+v, want the ranked oil variants", resolution.Families)
	}
}

// TestHandleOntologyResolveBuyerModeListsConsumingStages pins the buyer side: the
// same activity answers with the stages a buyer run would search.
func TestHandleOntologyResolveBuyerModeListsConsumingStages(t *testing.T) {
	server := &Server{}
	response := httptest.NewRecorder()
	server.handleOntologyResolve(response, postJSON("/api/ontology/resolve", `{"activity":"n-butanol","mode":"buyer"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var resolution agent.ActivityResolution
	if err := json.Unmarshal(response.Body.Bytes(), &resolution); err != nil {
		t.Fatalf("decode: %v", err)
	}
	joined := strings.ToLower(stageNamesForTest(resolution))
	if !strings.Contains(joined, "butyl acrylate production") {
		t.Errorf("stages = %q, want the graph's consuming stages", joined)
	}
	if strings.EqualFold(resolution.Category, "Alcohol solvents") {
		t.Errorf("category = %q, want the consuming family, not the product's own family", resolution.Category)
	}
}

// TestHandleOntologyResolveRejectsBadRequests pins the error contract: an empty
// activity and a malformed body are machine-readable 400s, not HTML.
func TestHandleOntologyResolveRejectsBadRequests(t *testing.T) {
	server := &Server{}
	for _, body := range []string{`{"activity":"   "}`, `{"activity":`} {
		response := httptest.NewRecorder()
		server.handleOntologyResolve(response, postJSON("/api/ontology/resolve", body))
		if response.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, response.Code)
		}
		var failure map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil || failure["error"] == "" {
			t.Errorf("body %q: error payload = %s", body, response.Body.String())
		}
	}
}

func stageNamesForTest(resolution agent.ActivityResolution) string {
	names := make([]string, 0, len(resolution.Stages))
	for _, stage := range resolution.Stages {
		names = append(names, stage.Name)
	}
	return strings.Join(names, " | ")
}
