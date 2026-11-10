package export

import (
	"fmt"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestCompanyMarkdownListsExcludedSearchResults pins the report appendix: the
// search hits the identity gate excluded are presented for review instead of
// vanishing, so a leaky name match is auditable in the artifact itself.
func TestCompanyMarkdownListsExcludedSearchResults(t *testing.T) {
	chart := sampleChart()
	chart.SearchTrace = &models.SearchTrace{Queries: []models.QueryTrace{
		{Query: `"goyum screwpress" Ludhiana`, Purpose: "company profile", Kept: []models.SearchResult{
			{Title: "kept hit", Link: "https://example.com/kept"},
		}, Excluded: []models.SearchResult{
			{Title: "Goyum Screw Press - Manufacturer from Ludhiana", Link: "https://www.indiamart.com/goyum-screw-press/"},
			{Title: "Goyum Screwpress, Exporter", Link: "https://www.tradeindia.com/goyum-screwpress/"},
		}},
		{Query: `"goyum screwpress" leadership`, Kept: []models.SearchResult{
			{Title: "kept hit two", Link: "https://example.com/kept-2"},
		}},
	}}

	markdown := renderCompanyMarkdown(chart)
	if !strings.Contains(markdown, "## Excluded search results") {
		t.Fatalf("expected an excluded-results appendix, got:\n%s", markdown)
	}
	for _, expected := range []string{
		"Goyum Screw Press - Manufacturer from Ludhiana",
		"https://www.indiamart.com/goyum-screw-press/",
		"Goyum Screwpress, Exporter",
	} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("expected the appendix to include %q", expected)
		}
	}
	// A query with nothing excluded does not get a section.
	if strings.Contains(markdown, `"goyum screwpress" leadership`) {
		t.Errorf("a query with no excluded hits must not appear in the appendix")
	}

	// More excluded hits than the per-query cap truncate with a pointer to the
	// searches.json artifact rather than reprinting the whole trace.
	overflow := sampleChart()
	excluded := make([]models.SearchResult, 0, excludedResultsPerQuery+2)
	for index := 0; index < excludedResultsPerQuery+2; index++ {
		excluded = append(excluded, models.SearchResult{
			Title: fmt.Sprintf("Hit %d", index+1),
			Link:  fmt.Sprintf("https://example.com/%d", index+1),
		})
	}
	overflow.SearchTrace = &models.SearchTrace{Queries: []models.QueryTrace{
		{Query: `"goyum screwpress" Ludhiana`, Excluded: excluded},
	}}
	overflowMarkdown := renderCompanyMarkdown(overflow)
	if !strings.Contains(overflowMarkdown, "…and 2 more (full list in the searches.json artifact)") {
		t.Errorf("expected a truncation note for the hits past the cap")
	}
	if strings.Contains(overflowMarkdown, "Hit 9") {
		t.Errorf("hits past the cap must not be listed")
	}

	// A run without a search trace renders no appendix at all.
	if strings.Contains(renderCompanyMarkdown(sampleChart()), "## Excluded search results") {
		t.Errorf("a chart without a search trace must not render the appendix")
	}
}

// TestCompanyHTMLListsExcludedSearchResults covers the printable document's
// appendix, including the per-query cap.
func TestCompanyHTMLListsExcludedSearchResults(t *testing.T) {
	chart := sampleChart()
	excluded := make([]models.SearchResult, 0, 12)
	for index := 0; index < 12; index++ {
		excluded = append(excluded, models.SearchResult{
			Title: fmt.Sprintf("Directory hit %d", index+1),
			Link:  fmt.Sprintf("https://example.com/directory/%d", index+1),
		})
	}
	chart.SearchTrace = &models.SearchTrace{Queries: []models.QueryTrace{
		{Query: `"goyum screwpress" Ludhiana`, Excluded: excluded},
	}}

	html := renderCompanyHTML(chart)
	for _, expected := range []string{
		"Excluded search results",
		"goyum screwpress", // the query text arrives HTML-escaped (&#34;…&#34;)
		"Directory hit 10",
		"(10 shown)",
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("expected the HTML appendix to include %q", expected)
		}
	}
	if strings.Contains(html, "Directory hit 11") {
		t.Errorf("hits past the cap must not be listed")
	}

	if strings.Contains(renderCompanyHTML(sampleChart()), "Excluded search results") {
		t.Errorf("a chart without a search trace must not render the appendix")
	}
}