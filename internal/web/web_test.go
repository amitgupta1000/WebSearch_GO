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
	if err := tmpl.ExecuteTemplate(&out, "index.html", indexViewStub{}); err != nil {
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

// indexViewStub mirrors the console view model fields consumed by index.html.
type indexViewStub struct {
	BrowserToggle    bool
	BrowserDefaultOn bool
}

func TestIndexShowsBrowserToggleOnlyWhenAvailable(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "index.html", indexViewStub{}); err != nil {
		t.Fatalf("execute index template: %v", err)
	}
	if strings.Contains(out.String(), "use_browser") {
		t.Error("the rendering toggle must be hidden when Chromium is unavailable")
	}

	out.Reset()
	if err := tmpl.ExecuteTemplate(&out, "index.html", indexViewStub{BrowserToggle: true, BrowserDefaultOn: true}); err != nil {
		t.Fatalf("execute index template: %v", err)
	}
	page := out.String()
	if !strings.Contains(page, `name="use_browser"`) {
		t.Error("expected the rendering toggle when Chromium is available")
	}
	if !strings.Contains(page, `value="1"`) || !strings.Contains(page, "checked") {
		t.Error("the toggle must reflect a deployment default of on")
	}
}

func TestCompanyResultOffersBrowserRerun(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	chart := &models.OrgChart{
		CompanyName:  "Acme",
		ScrapeReport: []models.ScrapeReportEntry{{URL: "https://example.com/a", Status: "failed"}},
	}
	view := struct {
		Chart         *models.OrgChart
		SessionID     string
		BrowserToggle bool
	}{Chart: chart, SessionID: "sess_test", BrowserToggle: true}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "company_result", view); err != nil {
		t.Fatalf("execute company_result: %v", err)
	}
	page := out.String()
	if !strings.Contains(page, `hx-post="/api/rerun"`) {
		t.Error("expected a re-run control on the finished result")
	}
	if !strings.Contains(page, `name="session_id" value="sess_test"`) {
		t.Error("the re-run must carry the session id so the brief can be reused")
	}
	if !strings.Contains(page, "could not use 1 collected source") {
		t.Errorf("expected the unusable-source count to be surfaced, got %q", page)
	}

	// Without the capability the offer must disappear entirely.
	out.Reset()
	view.BrowserToggle = false
	if err := tmpl.ExecuteTemplate(&out, "company_result", view); err != nil {
		t.Fatalf("execute company_result: %v", err)
	}
	if strings.Contains(out.String(), "/api/rerun") {
		t.Error("no re-run control should be rendered when Chromium is unavailable")
	}
}

func TestWorkflowConsoleShowsRunBudget(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "workflow_console", map[string]string{
		"SessionID": "sess_test", "Mode": "Company", "Query": "Acme", "Budget": "300s",
	}); err != nil {
		t.Fatalf("execute workflow_console: %v", err)
	}
	if !strings.Contains(out.String(), "300s budget") {
		t.Errorf("expected the run budget in the console, got %q", out.String())
	}

	// The badge is optional, so a caller that supplies no budget still renders.
	out.Reset()
	if err := tmpl.ExecuteTemplate(&out, "workflow_console", map[string]string{
		"SessionID": "sess_test", "Mode": "Company", "Query": "Acme",
	}); err != nil {
		t.Fatalf("execute workflow_console without a budget: %v", err)
	}
	if strings.Contains(out.String(), " budget") {
		t.Error("no budget badge should render when none is supplied")
	}
}
