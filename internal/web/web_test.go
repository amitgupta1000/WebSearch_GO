package web

import (
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

func TestTemplatesParseAndLookup(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatalf("Templates(): %v", err)
	}
	for _, name := range []string{"index.html", "company_chart", "company_result", "question_answer", "person_dossier", "error_box", "workflow_console", "log_entry"} {
		if tmpl.Lookup(name) == nil {
			t.Errorf("template %q not found", name)
		}
	}
}

func TestIndexHasStableQueryControls(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "index.html", nil); err != nil {
		t.Fatalf("execute index template: %v", err)
	}
	if !strings.Contains(out.String(), `id="primary-query"`) {
		t.Error("expected stable primary query input id")
	}
}

func TestCompanyChartEscapesUntrustedContent(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}

	payload := `<script>alert("xss")</script>`
	chart := &models.OrgChart{
		CompanyName: payload,
		Domain:      "example.com",
		Overview:    payload,
		Leadership:  []models.Employee{{Name: payload, Title: payload, Bio: payload}},
		Departments: []models.Department{{Name: payload, Members: []models.Employee{{Name: payload, Title: payload}}}},
		Sources:     []string{payload},
	}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "company_chart", chart); err != nil {
		t.Fatalf("execute company_chart: %v", err)
	}

	if strings.Contains(out.String(), "<script>alert") {
		t.Fatal("untrusted content was not escaped in company_chart output")
	}
	if !strings.Contains(out.String(), "&lt;script&gt;") {
		t.Error("expected escaped payload in company_chart output")
	}
	if !strings.Contains(out.String(), "Open dossier") {
		t.Error("expected company chart drilldown controls")
	}
	if !strings.Contains(out.String(), `onsubmit="return submitDossier(event, this)"`) {
		t.Error("expected company chart drilldown handler")
	}
	if !strings.Contains(out.String(), `id="company-drilldown-result"`) || !strings.Contains(out.String(), "Loading dossier...") {
		t.Error("expected visible company drilldown target and loading state")
	}
}

func TestPersonDossierEscapesAndRendersScore(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}

	payload := `<img src=x onerror=alert(1)>`
	profile := &models.PersonProfile{
		FullName:         payload,
		CurrentTitle:     payload,
		ExecutiveSummary: payload,
		ConfidenceScore:  0.5,
		Experiences:      []models.Experience{{Role: payload, Company: payload}},
		Education:        []models.Education{{Institution: payload}},
		KeySkills:        []string{payload},
	}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "person_dossier", profile); err != nil {
		t.Fatalf("execute person_dossier: %v", err)
	}

	if strings.Contains(out.String(), `<img src=x`) {
		t.Fatal("untrusted content was not escaped in person_dossier output")
	}
	if !strings.Contains(out.String(), "Score: 50%") {
		t.Errorf("expected rendered confidence score, got: %s", out.String())
	}
}

func TestErrorBoxEscapesMessage(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "error_box", `<script>alert(1)</script>`); err != nil {
		t.Fatalf("execute error_box: %v", err)
	}

	if strings.Contains(out.String(), "<script>") {
		t.Fatal("error message was not escaped")
	}
	if !strings.Contains(out.String(), "&lt;script&gt;") {
		t.Error("expected escaped error message")
	}
}
