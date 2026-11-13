package web

import (
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

func TestTemplatesParseAndLookup(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatalf("Templates(): %v", err)
	}
	for _, name := range []string{"index.html", "company_chart", "company_result", "question_answer", "person_dossier", "error_box", "workflow_console", "log_entry", "market_landscape_report", "market_rocketreach_result", "demand_chain_report", "news_report"} {
		if tmpl.Lookup(name) == nil {
			t.Errorf("template %q not found", name)
		}
	}
}

// TestMarketLandscapeReportRendersKeyPeople keeps the RocketReach form honest:
// the hidden company_name must carry the result card's company. It once read
// the root data instead ($ is the report in this fragment), and any result
// with key people failed the whole fragment with "can't evaluate field Name
// in type *models.MarketLandscapeReport".
func TestMarketLandscapeReportRendersKeyPeople(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}

	report := &models.MarketLandscapeReport{
		Activity:  "oil blending",
		Objective: "Find active blenders and their commercial contacts",
		Results: []models.MarketLandscapeCompany{{
			Name:      "Peninsula",
			Status:    "verified",
			Activity:  "Fuel oil blending",
			KeyPeople: []string{"Jeroen De Vos", "Victor Morales"},
			Evidence: []models.MarketLandscapeEvidence{{
				Title: "Storage and blending", URL: "https://example.com/peninsula",
				Lane: "company", ClaimType: "capability", SourceTier: "primary", Status: "kept",
			}},
		}},
	}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "market_landscape_report", report); err != nil {
		t.Fatalf("execute market_landscape_report: %v", err)
	}
	rendered := out.String()
	for _, expected := range []string{
		`<h3 class="text-2xl font-bold text-white">Producer/Seller Landscape</h3>`,
		`name="company_name" value="Peninsula"`,
		`name="person_name" value="Jeroen De Vos"`,
		`name="person_name" value="Victor Morales"`,
		"Storage and blending",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("market_landscape_report is missing %q", expected)
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
		Warnings:         []string{"RocketReach was unavailable; continued with web-based research."},
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
	if !strings.Contains(out.String(), "RocketReach was unavailable") {
		t.Errorf("expected RocketReach warning, got: %s", out.String())
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
		ResearchRoster: []models.CompanyResearchRow{{
			Name:           "Jane Doe",
			LinkedInURL:    "https://www.linkedin.com/in/jane-doe",
			Designation:    "Director",
			Classification: "leadership",
			Status:         "verified",
		}},
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
	if !strings.Contains(page, "Full research results") || !strings.Contains(page, "Download CSV") || !strings.Contains(page, "Fetch RR") {
		t.Error("expected the full research roster controls to render")
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

// TestDemandChainReportFragmentRenders keeps the buyer console fragment honest:
// the value chain panel, the stage-tagged result cards, and the honesty
// warnings all render, and key-people forms carry the company (the same
// root-dot trap the market fragment once had).
func TestDemandChainReportFragmentRenders(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}

	report := &models.DemandChainReport{
		Activity: "used cooking oil", Objective: "Find refiners that buy UCO.",
		Geography:     "India",
		ChainInferred: true,
		Chain: []models.ValueChainStage{{
			Name: "biodiesel production", Rationale: "Biodiesel is produced from UCO.",
			Operators: []string{"biodiesel plant"}, Source: "model",
		}},
		QueriesUsed: 12, SearchHits: 31, VerificationQueries: 6,
		StageCandidates: []models.StageCandidates{{
			Stage:     models.ValueChainStage{Name: "biodiesel production", Rationale: "Biodiesel is produced from UCO.", Source: "model"},
			Companies: []models.MarketLandscapeCompany{{Name: "Jayant Oleochemicals", Status: "lead", Confidence: 0.45}},
		}},
		Results: []models.MarketLandscapeCompany{{
			Name: "GreenDiesel Sdn Bhd", Domain: "greendiesel.com.my", Status: "verified",
			Stage: "biodiesel production", Confidence: 0.86,
			KeyPeople: []string{"Asha Rao"},
			Evidence: []models.MarketLandscapeEvidence{{
				Title: "GreenDiesel wins state tender", URL: "https://tenderportal.example.com/uco-tender",
				Lane: "demand verification", ClaimType: "demand-tender",
			}},
		}},
		Warnings: []string{"Private procurement is invisible to web research."},
	}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "demand_chain_report", report); err != nil {
		t.Fatalf("execute demand_chain_report: %v", err)
	}
	rendered := out.String()
	for _, expected := range []string{
		"Buyer landscape",
		"Geography: India",
		"Potential buyers by stage",
		"biodiesel production", "Biodiesel is produced from UCO.",
		"GreenDiesel Sdn Bhd", "Consuming stage: biodiesel production",
		`name="company_name" value="GreenDiesel Sdn Bhd"`,
		`name="person_name" value="Asha Rao"`,
		"Private procurement is invisible",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("demand_chain_report fragment is missing %q", expected)
		}
	}
}

// TestDeepSearchReportFragmentRenders renders the deep search report with a
// fully-populated report and asserts every section makes it into the HTML.
func TestDeepSearchReportFragmentRenders(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	report := &models.DeepSearchReport{
		ResearchID: "sess-1", Question: "How are regulators responding to AI disinformation?",
		Focus: models.FocusRecency, AsOf: now, StartedAt: now, CompletedAt: now,
		Plan: models.DeepSearchPlan{
			Understanding: "The question asks about regulator responses.",
			SubQuestions:  []string{"Which regulators acted recently?"},
		},
		QueryPlan: models.DeepSearchQueryPlan{
			Entries: []models.DeepSubQuestionQueries{{
				SubQuestion: "Which regulators acted recently?",
				Queries:     []string{"regulator deepfake rules 2026", "latest election deepfake guidance"},
			}},
		},
		Rounds: []models.DeepSearchRound{
			{Round: 1, Queries: []string{"regulator deepfake rules 2026"}, Composed: 2, Candidates: 8, Selected: 1,
				SearchHits: 8, Scraped: 1, QAPairs: 2, Missing: []string{"no regulator statements"}},
			{Round: 2, Queries: []string{"election deepfake guidance"}, SearchHits: 6, Selected: 1, Scraped: 1,
				QAPairs: 2, Complete: true},
		},
		Sources: []models.DeepSearchSource{{
			URL: "https://example.com/regulator", Title: "Regulator acts on deepfakes",
			Host: "example.com", SubQuestion: "Which regulators acted recently?",
			Snippet: "The regulator proposed rules.",
			Content: "The regulator proposed rules on synthetic media.", Round: 1, FetchedAt: now,
		}},
		QAPairs: []models.DeepSearchQAPair{{
			SubQuestion: "Which regulators acted recently?", Question: "Which regulator proposed deepfake rules?",
			Answer: "A regulator proposed rules on synthetic media.", Quote: "proposed rules on synthetic media",
			URL: "https://example.com/regulator", Round: 1,
		}},
		Synthesis: models.DeepSearchSynthesis{
			Title:   "Regulators tighten deepfake rules",
			Summary: "Several regulators moved this year.",
			Report:  "## Findings\nSynthetic media rules expanded.",
			Findings: []models.DeepSearchFinding{
				{Statement: "A regulator proposed deepfake rules.", URLs: []string{"https://example.com/regulator"}},
			},
			OpenQuestions: []string{"Enforcement timelines remain unclear."},
		},
		QueriesUsed: 2, SearchHits: 14, PagesScraped: 2, RoundsRun: 2,
	}

	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "deepsearch_report", report); err != nil {
		t.Fatalf("execute deepsearch_report: %v", err)
	}
	rendered := out.String()
	for _, expected := range []string{
		"Regulators tighten deepfake rules",
		"How are regulators responding to AI disinformation?",
		"focus: recency",
		"Several regulators moved this year.",
		"Synthetic media rules expanded.",
		"A regulator proposed deepfake rules.",
		"Enforcement timelines remain unclear.",
		"Regulator acts on deepfakes",
		"Which regulators acted recently?",
		"regulator deepfake rules 2026",
		"Which regulator proposed deepfake rules?",
		"A regulator proposed rules on synthetic media.",
		"proposed rules on synthetic media",
		"Extracted question",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("deepsearch_report fragment is missing %q", expected)
		}
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

// entityCandidateStub mirrors the confirmation step's view, which lives in the server
// package. Templates read fields by name, so a mirror is enough to pin the markup that
// makes the step one click to confirm.
type entityCandidateStub struct {
	Candidate        models.EntityCandidate
	Query            string
	Industry         string
	Country          string
	City             string
	AreasOfInterest  string
	DesiredOutput    string
	TimeScope        string
	Notes            string
	UseBrowser       string
	Mode             string
	LegalPersons     string
	IncludeDirectors string
	PreSelected      bool
	Extras           []entityExtraStub
}

type entityExtraStub struct {
	Candidate models.EntityCandidate
	Value     string
	Warning   string
}

type entityCandidatesStub struct {
	Query               string
	Candidates          []entityCandidateStub
	Industry            string
	Country             string
	City                string
	AreasOfInterest     string
	DesiredOutput       string
	TimeScope           string
	Notes               string
	UseBrowser          string
	UseRocketReach      string
	SourceProfile       string
	PersonOfInterest    string
	RelatedPersonDomain string
	RelatedMaxLeaders   string
	Mode                string
	LegalPersons        string
	IncludeDirectors    string
	TypedDomain         bool
}

// personCandidateStub and personCandidatesStub mirror the person confirmation step's view,
// which lives in the server package.
type personCandidateStub struct {
	Candidate   models.PersonCandidate
	Query       string
	Company     string
	Location    string
	PreSelected bool
}

type personCandidatesStub struct {
	Query          string
	Candidates     []personCandidateStub
	Company        string
	Location       string
	TypedContext   bool
	NoCompanyMatch bool
}

// TestIndexSendsBothModesThroughTheConfirmationStep pins the flow: the form's own submit
// (which is what Enter does) goes to a confirmation step, so a run cannot start without a
// confirmed identity — a company on its domain or registration id, a person on the employer
// and place a profile states. The form's destination follows the mode, and HTMX is
// re-processed so Enter follows it too.
func TestIndexSendsBothModesThroughTheConfirmationStep(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "index.html", indexViewStub{}); err != nil {
		t.Fatalf("render index: %v", err)
	}
	page := out.String()

	if !strings.Contains(page, `hx-post="/api/resolve-entity"`) {
		t.Error("the form must submit to the company confirmation step, so Enter cannot skip it")
	}
	if !strings.Contains(page, `/api/resolve-person`) {
		t.Error("expected person mode's confirmation endpoint on the page")
	}
	if !strings.Contains(page, `hx-target="#confirmation-step"`) {
		t.Error("the confirmation step must render into its own target")
	}
	if !strings.Contains(page, `id="confirmation-step"`) {
		t.Error("expected the confirmation step target on the page")
	}
	if !strings.Contains(page, "Continue: confirm the query") || !strings.Contains(page, "Continue: confirm the person") {
		t.Error("expected both modes to say they continue to a confirmation step")
	}
	if !strings.Contains(page, "htmx-indicator") {
		t.Error("expected a loading indicator, because the lookup takes a moment")
	}
	if !strings.Contains(page, "htmx.process(form)") {
		t.Error("expected the form to be re-processed when the mode changes, or Enter keeps the old destination")
	}
}

// TestEntityCandidatesPreSelectsTheTopMatchAndOffersExtras covers what the operator sees:
// the top candidate is pre-selected and focused, a second name can be ticked into the same
// identity, and a contradictory registration id is flagged where it is ticked.
func TestEntityCandidatesPreSelectsTheTopMatchAndOffersExtras(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	view := entityCandidatesStub{
		Query: "Sanjay Chemicals",
		Candidates: []entityCandidateStub{
			{
				Candidate:   models.EntityCandidate{Domain: "sanjaychemindia.com", Score: 3, NameMatch: true},
				PreSelected: true,
				Extras: []entityExtraStub{{
					Candidate: models.EntityCandidate{Domain: "sanjaychemicals.in", Name: "Sanjay Chemicals India"},
					Value:     "sanjaychemicals.in|Sanjay Chemicals India|",
					Warning:   "a different registration id (U111 vs U222)",
				}},
			},
			{Candidate: models.EntityCandidate{Domain: "sanjaychemicals.in", Score: 1}},
		},
	}
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "entity_candidates", view); err != nil {
		t.Fatalf("render confirmation step: %v", err)
	}
	body := out.String()

	if !strings.Contains(body, `name="entity_extra" value="sanjaychemicals.in|Sanjay Chemicals India|"`) {
		t.Errorf("expected the tick to carry the whole identity, got %q", body)
	}
	if !strings.Contains(body, "a different registration id (U111 vs U222)") {
		t.Error("expected the conflicting registration id to be flagged on the tick")
	}
	if !strings.Contains(body, "autofocus") {
		t.Error("expected the pre-selected card to be focused, so confirming is one keypress")
	}
	if !strings.Contains(body, `name="entity_source" value="resolved"`) {
		t.Error("expected a dialogue pick to be recorded as resolved")
	}
	if !strings.Contains(body, "top match") {
		t.Error("expected the pre-selected candidate to be marked as the top match")
	}
	if !strings.Contains(body, "Research by name only (entity unconfirmed)") {
		t.Error("expected the name-only escape hatch, so a failed lookup is not a dead end")
	}
}

// TestPersonCandidatesPreSelectAndCarryTheConfirmedContext covers the person step: the top
// match is pre-selected and focused, the employer read off the profile is pre-filled (that is
// the disambiguation), the confirmed page travels as a seed, and a failed lookup still offers
// the name-only path.
func TestPersonCandidatesPreSelectAndCarryTheConfirmedContext(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	view := personCandidatesStub{
		Query: "Sanjay Gupta",
		Candidates: []personCandidateStub{
			{
				Candidate: models.PersonCandidate{
					Name:            "Sanjay Kumar Gupta",
					Title:           "Managing Director",
					Company:         "Sanjay Chemicals (India) Pvt Ltd",
					URL:             "https://www.linkedin.com/in/sanjay-kumar-gupta",
					EvidenceTitle:   "Sanjay Kumar Gupta - Managing Director - Sanjay Chemicals | LinkedIn",
					EvidenceSnippet: "Mumbai, Maharashtra",
					MatchesCompany:  true,
				},
				Company:     "Sanjay Chemicals (India) Pvt Ltd",
				PreSelected: true,
			},
			{
				Candidate: models.PersonCandidate{Name: "Sanjay Gupta", Directory: true, URL: "https://rocketreach.co/sanjay-gupta"},
			},
		},
	}
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "person_candidates", view); err != nil {
		t.Fatalf("render person confirmation step: %v", err)
	}
	body := out.String()

	if !strings.Contains(body, `name="person_seed_url" value="https://www.linkedin.com/in/sanjay-kumar-gupta"`) {
		t.Errorf("expected the confirmed profile to travel as a seed, got %q", body)
	}
	if !strings.Contains(body, `name="company_name" value="Sanjay Chemicals (India) Pvt Ltd"`) {
		t.Error("expected the employer read off the profile to be pre-filled")
	}
	if !strings.Contains(body, `name="person_source" value="resolved"`) {
		t.Error("expected a confirmed person to be recorded as resolved")
	}
	if !strings.Contains(body, "autofocus") || !strings.Contains(body, "top match") {
		t.Error("expected the top match to be focused and marked")
	}
	if !strings.Contains(body, "Names the company you gave.") {
		t.Error("expected a candidate matching the typed company to say so")
	}
	if !strings.Contains(body, "people-directory listing") {
		t.Error("expected a directory hit to be flagged rather than passed off as a profile")
	}
	if !strings.Contains(body, "Research on the name alone (unconfirmed)") {
		t.Error("expected the name-only escape hatch on the person step")
	}
}
