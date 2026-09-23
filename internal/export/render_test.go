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
