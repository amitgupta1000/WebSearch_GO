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

func TestCompanyChartCollapsesImagesAndScrapeReport(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	chart := &models.OrgChart{
		CompanyName: "Acme",
		Images: []models.Image{
			{URL: "https://cdn.example.com/a.png", Alt: "Logo A"},
			{URL: "https://cdn.example.com/b.png", Alt: "Logo B"},
		},
		ScrapeReport: []models.ScrapeReportEntry{
			{URL: "https://example.com/a", Status: "success"},
			{URL: "https://example.com/b", Status: "failed", Reason: "403"},
			{URL: "https://example.com/c", Status: "skipped"},
			{
				URL: "https://example.com/d", SourceType: "professional_profile", Status: "recovered",
				WordCount: 120, HandlerStatus: "recovered", HandlerTactics: "cookie-render",
			},
			{
				URL: "https://example.com/e", SourceType: "professional_profile", Status: "skipped",
				HandlerStatus: "deferred", Reason: "public search snippet retained; the hard-URL handler could not recover the profile",
			},
		},
	}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "company_chart", chart); err != nil {
		t.Fatalf("execute company_chart: %v", err)
	}
	page := out.String()

	if got := strings.Count(page, "<summary"); got != 2 {
		t.Fatalf("expected one disclosure control per panel, got %d", got)
	}
	if strings.Contains(page, "<details open") {
		t.Error("both panels should start collapsed so the result panel stays readable")
	}
	if !strings.Contains(page, "Images from selected sources") || !strings.Contains(page, "(2)") {
		t.Error("expected the image count in the disclosure label")
	}
	if !strings.Contains(page, "Scraping Report") || !strings.Contains(page, "(5 sources)") {
		t.Error("expected the source count in the disclosure label")
	}

	// Every scrape-report row must render one cell per header column: the table
	// used to declare five headers for six cells, which pushed the OCR and
	// details diagnostics under the wrong headings.
	tableStart := strings.Index(page, "Target URL")
	if tableStart < 0 {
		t.Fatal("expected the scrape report table header")
	}
	headerStart := strings.LastIndex(page[:tableStart], "<tr")
	headerEnd := tableStart + strings.Index(page[tableStart:], "</tr>")
	header := page[headerStart:headerEnd]
	columns := strings.Count(header, "<th")
	if columns != 6 {
		t.Fatalf("expected six scrape-report columns, got %d in %q", columns, header)
	}
	table := page[tableStart:]
	table = table[:strings.Index(table, "</table>")]
	for _, row := range strings.Split(table, "<tr class=")[1:] {
		if cells := strings.Count(row, "<td"); cells != columns {
			t.Fatalf("scrape-report row renders %d cells but the header declares %d", cells, columns)
		}
	}

	// The hard-URL ladder outcome must be visible: a deferred profile (out of
	// budget or escalation slots) reads differently from a wall the ladder
	// could not pass, and a recovered one names the step that worked.
	if !strings.Contains(page, "&middot; ladder deferred") {
		t.Error("expected the deferred ladder outcome in the scrape column")
	}
	if strings.Contains(page, "ladder recovered") {
		t.Error("a recovered profile should not repeat its status as a ladder annotation")
	}
	if !strings.Contains(page, "Ladder: cookie-render") {
		t.Error("expected the ladder tactic for a handler-recovered profile")
	}

	// Collapsing is visual only: the content must stay in the DOM.
	if !strings.Contains(page, "https://cdn.example.com/a.png") {
		t.Error("image markup must remain in the document when collapsed")
	}
	if !strings.Contains(page, "https://example.com/c") {
		t.Error("scrape report rows must remain in the document when collapsed")
	}

	// Neither panel should render when there is nothing to show.
	out.Reset()
	if err := tmpl.ExecuteTemplate(&out, "company_chart", &models.OrgChart{CompanyName: "Acme"}); err != nil {
		t.Fatalf("execute empty company_chart: %v", err)
	}
	if strings.Contains(out.String(), "<summary") {
		t.Error("empty panels must not render a disclosure control")
	}
}

func TestSourcesFooterCollapses(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	chart := &models.OrgChart{
		CompanyName: "Acme",
		Sources:     []string{"https://example.com/a", "https://example.com/b", "https://example.com/c"},
	}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "company_chart", chart); err != nil {
		t.Fatalf("execute company_chart: %v", err)
	}
	page := out.String()
	if got := strings.Count(page, "<summary"); got != 1 {
		t.Fatalf("expected one disclosure control for the source list, got %d", got)
	}
	if !strings.Contains(page, "Sources") || !strings.Contains(page, "(3)") {
		t.Error("expected the source count in the disclosure label")
	}
	if strings.Contains(page, "<details open") {
		t.Error("the source list should start collapsed")
	}
	if !strings.Contains(page, "https://example.com/c") {
		t.Error("source links must remain in the document when collapsed")
	}
	if strings.Contains(page, `target="_blank" class="text-indigo-400`) {
		t.Error("external source links should carry rel=noreferrer")
	}

	// A chart without sources must not render an empty control.
	out.Reset()
	if err := tmpl.ExecuteTemplate(&out, "company_chart", &models.OrgChart{CompanyName: "Acme"}); err != nil {
		t.Fatalf("execute company_chart without sources: %v", err)
	}
	if strings.Contains(out.String(), "<summary") {
		t.Error("no disclosure control should render without sources")
	}

	// The question answer reuses the same collapsible footer.
	out.Reset()
	if err := tmpl.ExecuteTemplate(&out, "question_answer", models.QuestionAnswer{
		Question: "Who leads Acme?",
		Answer:   "Acme's leadership is listed above.",
		Sources:  []string{"https://example.com/a", "https://example.com/b"},
	}); err != nil {
		t.Fatalf("execute question_answer: %v", err)
	}
	answer := out.String()
	if !strings.Contains(answer, "<summary") || !strings.Contains(answer, "(2)") {
		t.Errorf("expected a collapsible source list on the answer, got %q", answer)
	}
}

// TestCompanyChartShowsFreshness pins the console half: a row backed by a 2021
// post must say so, and the panel must summarise the verdicts and conflicts.
func TestCompanyChartShowsFreshness(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	chart := &models.OrgChart{
		CompanyName: "KLJ Resources Limited",
		Leadership: []models.Employee{{
			Name: "Sanjiv Goyal", Title: "Chief Financial Officer",
			AsOf: "2021-02-26", Verification: models.VerificationConflicting,
			VerificationNote: `2 people claim the title "Chief Financial Officer": Suresh Chand Jalther, Sanjiv Goyal`,
		}},
		Departments: []models.Department{{
			Name: "Procurement",
			Members: []models.Employee{{
				Name: "Rajesh Zaveri", Title: "Head Of Procurement",
				AsOf: "2026-08-01", Verification: models.VerificationRecent,
			}},
		}},
		Validation: &models.ValidationReport{
			RowsChecked: 2, RowsRecent: 1, RowsStale: 1, LookupsUsed: 1,
			Conflicts: []string{`2 people claim the title "Chief Financial Officer": Suresh Chand Jalther, Sanjiv Goyal`},
			Lookups: []models.ValidationLookup{{
				Person: "Sanjiv Goyal", Query: `"Sanjiv Goyal" "KLJ Resources Limited"`,
				Status: "unchanged", Detail: "no dated mention in the results",
			}},
		},
	}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "company_chart", chart); err != nil {
		t.Fatalf("execute company_chart: %v", err)
	}
	page := out.String()
	for _, expected := range []string{
		"Data Freshness",
		"2021-02-26 · conflicting",
		"verified-recent 1",
		"stale 1",
		`2 people claim the title`,
		"2026-08-01",
		"Reverse lookup",
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("console output is missing %q", expected)
		}
	}

	// A chart from before the validation pass must not grow a new panel.
	out.Reset()
	if err := tmpl.ExecuteTemplate(&out, "company_chart", &models.OrgChart{CompanyName: "Acme"}); err != nil {
		t.Fatalf("execute empty company_chart: %v", err)
	}
	if strings.Contains(out.String(), "Data Freshness") {
		t.Error("a chart without a validation report must not render the panel")
	}
}
