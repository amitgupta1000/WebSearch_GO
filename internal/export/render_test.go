package export

import (
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

func sampleChart() *models.OrgChart {
	return &models.OrgChart{
		CompanyName:  "KLJ Resources Limited",
		Domain:       "kljresources.com",
		Headquarters: "New Delhi, India",
		Overview:     "A specialty chemicals and mining group.",
		Leadership: []models.Employee{
			{Name: "Asha Rao", Title: "Chief Executive Officer", Location: "New Delhi", PublicSource: "https://kljresources.com/leadership"},
		},
		Departments: []models.Department{
			{Name: "Procurement", Head: "Ravi Menon", Description: "Sourcing and supply chain.", Members: []models.Employee{
				{Name: "Ravi Menon", Title: "VP Procurement", Location: "Mumbai", PublicSource: "https://example.com/ravi"},
			}},
			{Name: "Marketing"},
		},
		Locations:   []models.Location{{City: "New Delhi", Country: "India", HeadcountEst: "240"}},
		Sources:     []string{"https://kljresources.com/about", "https://example.com/profile"},
		Timings:     []models.PhaseTiming{{Phase: "scrape", Millis: 4200, Detail: "7 attempts -> 5 pages kept"}},
		LastUpdated: time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC),
	}
}

func sampleProfile() *models.PersonProfile {
	return &models.PersonProfile{
		FullName:         "Asha Rao",
		CurrentTitle:     "Chief Executive Officer",
		CurrentCompany:   "KLJ Resources Limited",
		Location:         "New Delhi, India",
		ExecutiveSummary: "Two decades in specialty chemicals.",
		Experiences: []models.Experience{
			{Role: "Chief Executive Officer", Company: "KLJ Resources", Period: "2019-present", Highlights: []string{"Led a refinery expansion"}},
		},
		Education:      []models.Education{{Institution: "IIT Delhi", Degree: "B.Tech", Field: "Chemical Engineering", Year: "1998"}},
		KeySkills:      []string{"M&A", "Operations"},
		OnlinePresence: map[string]string{"homepage": "https://example.com/asha"},
		Sources:        []string{"https://example.com/asha"},
		LastUpdated:    time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC),
	}
}

func TestCompanyMarkdownCoversTheChart(t *testing.T) {
	markdown := renderCompanyMarkdown(sampleChart())

	for _, expected := range []string{
		"# KLJ Resources Limited — Company Org Chart",
		"kljresources.com", "New Delhi, India",
		"## Overview", "specialty chemicals",
		"## Executive Leadership", "Asha Rao", "Chief Executive Officer",
		"## Departments & Functions", "Procurement", "Sourcing and supply chain.", "Ravi Menon",
		"## Locations", "240",
		"## Run timings", "scrape",
		"## Sources", "https://kljresources.com/about",
	} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("company markdown is missing %q", expected)
		}
	}
}

func TestPersonMarkdownCoversTheDossier(t *testing.T) {
	markdown := renderPersonMarkdown(sampleProfile())

	for _, expected := range []string{
		"# Asha Rao — Professional Dossier",
		"Chief Executive Officer", "KLJ Resources Limited",
		"Confidence:", "%",
		"## Executive summary", "specialty chemicals",
		"## Experience", "Led a refinery expansion",
		"## Education", "IIT Delhi",
		"## Key skills", "M&A",
		"## Online presence", "homepage",
		"## Sources",
	} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("person markdown is missing %q", expected)
		}
	}
}

// The identity check and the research trace are the person-mode counterparts of the entity
// gate, scrape report, and timings the company chart carries: a dossier must show what it
// held its sources to, what it excluded, and what the run actually did.
func TestPersonMarkdownCarriesTheIdentityCheckAndTrace(t *testing.T) {
	profile := sampleProfile()
	profile.PersonGate = &models.PersonGate{
		Name:     "Asha Rao",
		Company:  "KLJ Resources Limited",
		Kept:     3,
		NameOnly: 1,
		Dropped:  2,
		Excluded: []models.OffEntityRow{
			{Name: "Jane Rao - CTO - OtherCorp Ltd | LinkedIn", Title: "OtherCorp Ltd", Source: "https://www.linkedin.com/in/jane-rao", Reason: "states employer \"OtherCorp Ltd\" — a different person with the same name"},
		},
	}
	profile.ScrapeReport = []models.ScrapeReportEntry{
		{URL: "https://www.linkedin.com/in/asha-rao", SourceType: "profile", Status: "ok", Reason: "recovered by the hard-URL ladder", WordCount: 96},
		{URL: "https://paywall.example/asha", SourceType: "web", Status: "failed", Reason: "blocked by the site"},
	}
	profile.Timings = []models.PhaseTiming{
		{Phase: "primary search", Millis: 1200, Detail: "6 hits"},
		{Phase: "profile fetches", Millis: 3400, Detail: "1 page(s) read"},
	}
	profile.SearchQueries = []string{"\"Asha Rao\" \"KLJ Resources\"", "Asha Rao speaker interview articles career education"}

	markdown := renderPersonMarkdown(profile)
	for _, expected := range []string{
		"## Identity check",
		"Asha Rao", "KLJ Resources Limited",
		"3 kept", "1 naming the person but stating no employer", "2 excluded",
		"OtherCorp Ltd", "different person with the same name",
		"## Research trace",
		"Asha Rao speaker interview articles career education",
		"primary search", "profile fetches", "1 page(s) read",
		"https://www.linkedin.com/in/asha-rao", "recovered by the hard-URL ladder",
		"blocked by the site",
	} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("person markdown is missing %q", expected)
		}
	}

	// A profile without the trace — a hand-built one, or a run from before the gate — must
	// not grow empty sections.
	if bare := renderPersonMarkdown(sampleProfile()); strings.Contains(bare, "## Identity check") || strings.Contains(bare, "## Research trace") {
		t.Errorf("sections rendered without any gate or trace data:\n%s", bare)
	}
}

// A pipe or newline inside a value would otherwise break the table it is placed in,
// which is exactly what scraped content tends to contain.
func TestMarkdownEscapesTableBreakers(t *testing.T) {
	chart := sampleChart()
	chart.Headquarters = "New Delhi | India\n(multi-line)"

	markdown := renderCompanyMarkdown(chart)

	if !strings.Contains(markdown, `New Delhi \| India (multi-line)`) {
		t.Errorf("expected the pipe escaped and the newline collapsed, got:\n%s", markdown)
	}
	for _, line := range strings.Split(markdown, "\n") {
		if strings.Contains(line, "#") && strings.Contains(line, "India") && strings.Contains(line, "(multi-line)") {
			t.Errorf("a table row still contains a raw newline: %q", line)
		}
	}
}

// The HTML is what Chromium prints, and its content comes from the model and from
// scraped pages, so it must be escaped rather than trusted.
func TestHTMLDocumentIsSelfContainedAndEscaped(t *testing.T) {
	chart := sampleChart()
	chart.Overview = `Dangerous <script>alert("x")</script> text`

	document := renderCompanyHTML(chart)

	if strings.Contains(document, "<script>alert") {
		t.Error("model-supplied markup was not escaped")
	}
	if !strings.Contains(document, "&lt;script&gt;") {
		t.Error("expected the escaped form of the injected markup")
	}
	// Self-contained: no remote stylesheet or script, because the shared browser
	// context aborts those requests and the printed page would lose its styling.
	if strings.Contains(document, "cdn.") || strings.Contains(document, "<link") {
		t.Error("the printable document must not reference remote assets")
	}
	if !strings.Contains(document, "<style>") {
		t.Error("expected inline styling in the printable document")
	}
	if !strings.Contains(document, "KLJ Resources Limited") {
		t.Error("expected the company name in the document")
	}
}

func TestPersonHTMLIncludesTheHeadline(t *testing.T) {
	document := renderPersonHTML(sampleProfile())
	for _, expected := range []string{"Asha Rao", "Chief Executive Officer", "Executive summary", "IIT Delhi", "Key skills"} {
		if !strings.Contains(document, expected) {
			t.Errorf("person HTML is missing %q", expected)
		}
	}
}

func TestEmptyChartStillProducesADocument(t *testing.T) {
	// A sparse run must still yield a file rather than an empty page.
	markdown := renderCompanyMarkdown(&models.OrgChart{CompanyName: "Acme"})
	if !strings.Contains(markdown, "# Acme — Company Org Chart") {
		t.Errorf("unexpected markdown:\n%s", markdown)
	}
	document := renderCompanyHTML(&models.OrgChart{CompanyName: "Acme"})
	if !strings.Contains(document, "<!doctype html>") || !strings.Contains(document, "Acme") {
		t.Errorf("unexpected html:\n%s", document)
	}
}

func TestSlugifyProducesPortableNames(t *testing.T) {
	long := strings.Repeat("x", 100)
	cases := map[string]string{
		"KLJ Resources Limited": "klj-resources-limited",
		"  Acme, Inc. & Co.  ":  "acme-inc-co",
		"A/B Testing (2024)":    "a-b-testing-2024",
		"":                      "research",
		"---":                   "research",
		long:                    strings.Repeat("x", 60),
	}
	for input, want := range cases {
		if got := slugify(input); got != want {
			t.Errorf("slugify(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestArtifactsReportFreshness pins the output half of the validation pass: a
// reader of the Markdown or the printed PDF must be able to see that a "current"
// row rests on a five-year-old post, and that two people claim one title.
func TestArtifactsReportFreshness(t *testing.T) {
	chart := sampleChart()
	chart.Leadership[0].AsOf = "2021-02-26"
	chart.Leadership[0].Verification = models.VerificationStale
	chart.Leadership[0].VerificationNote = `2 people claim the title "Chief Financial Officer": Asha Rao, Sanjiv Goyal`
	chart.Leadership = append(chart.Leadership, models.Employee{
		Name: "Sanjiv Goyal", Title: "Chief Financial Officer",
		AsOf: "2021-02-26", Verification: models.VerificationConflicting,
	})
	chart.Departments[0].Members[0].AsOf = "2026-08-01"
	chart.Departments[0].Members[0].Verification = models.VerificationRecent
	chart.Validation = &models.ValidationReport{
		RowsChecked: 3, RowsRecent: 1, RowsDated: 0, RowsStale: 2, RowsUndated: 0,
		LookupsUsed: 1,
		Conflicts:   []string{`2 people claim the title "Chief Financial Officer": Asha Rao, Sanjiv Goyal`},
		Lookups: []models.ValidationLookup{{
			Person: "Sanjiv Goyal", Query: `"Sanjiv Goyal" "KLJ Resources Limited"`,
			Status: "promoted", AsOf: "2026-08-01", Source: "https://example.com/2026",
		}},
	}

	markdown := renderCompanyMarkdown(chart)
	for _, expected := range []string{
		"| Name | Title | Location | As of | Public source |",
		"2021-02-26 · stale",
		"## Data freshness",
		"| verified-recent | 1 |",
		"| stale | 2 |",
		"**Conflicting title:**",
		"| Sanjiv Goyal |",
		"promoted",
		// The date's provenance has to travel with it.
		"2026-08-01 · <https://example.com/2026>",
		"as of 2026-08-01 · verified-recent",
		"not proof of current employment",
	} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("markdown is missing %q", expected)
		}
	}

	document := renderCompanyHTML(chart)
	for _, expected := range []string{
		"<th>As of</th>",
		"2021-02-26 · stale",
		"Data freshness",
		"Conflicting title:",
		"promoted",
		"verified-recent",
	} {
		if !strings.Contains(document, expected) {
			t.Errorf("HTML is missing %q", expected)
		}
	}
}

// TestArtifactsOmitFreshnessWithoutAReport keeps older charts byte-compatible:
// no validation pass means no new columns or sections.
func TestArtifactsOmitFreshnessWithoutAReport(t *testing.T) {
	markdown := renderCompanyMarkdown(sampleChart())
	if strings.Contains(markdown, "Data freshness") {
		t.Error("a chart without a validation report must not gain a freshness section")
	}
	if !strings.Contains(markdown, "| Name | Title | Location | As of | Public source |") {
		t.Error("the As-of column stays in the header; only its values are empty")
	}
	if strings.Contains(markdown, "· stale") || strings.Contains(markdown, "· undated") {
		t.Error("a chart with no labels must not claim any")
	}
	document := renderCompanyHTML(sampleChart())
	if strings.Contains(document, "Data freshness") {
		t.Error("a chart without a validation report must not gain a freshness section")
	}
}

// TestMarkdownCollapsesEmptyDepartments keeps a thin result reading as thin: a run that
// found two people should not render fifteen sections that say so.
func TestMarkdownCollapsesEmptyDepartments(t *testing.T) {
	chart := &models.OrgChart{
		CompanyName: "Acme",
		Departments: []models.Department{
			{Name: "Finance", Members: []models.Employee{{Name: "Asha Mehta", Title: "CFO"}}},
			{Name: "Marketing"},
			{Name: "IT"},
		},
	}
	markdown := renderCompanyMarkdown(chart)

	if !strings.Contains(markdown, "_No named contacts found for: Marketing, IT._") {
		t.Errorf("expected the empty departments collapsed into one line, got %q", markdown)
	}
	if strings.Contains(markdown, "### Marketing") || strings.Contains(markdown, "### IT") {
		t.Error("empty departments must not get their own sections")
	}
	if !strings.Contains(markdown, "### Finance") {
		t.Error("a department with members keeps its section")
	}
}
