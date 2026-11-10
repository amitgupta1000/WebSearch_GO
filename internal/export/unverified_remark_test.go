package export

import (
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestUnverifiedRowsStayInTheRosterWithRemark pins the presentation contract: a
// current-status lookup that could not confirm a person leaves the row in the
// roster carrying an "unable to verify" remark, in the console's data and the
// saved artifacts alike.
func TestUnverifiedRowsStayInTheRosterWithRemark(t *testing.T) {
	chart := sampleChart()
	chart.Leadership = append(chart.Leadership, models.Employee{
		Name:             "Gurpreet Grewal",
		Title:            "Manager of Sales",
		Verification:     models.VerificationCurrentUnverified,
		VerificationNote: "unable to verify: current employment could not be established",
		PublicSource:     "https://www.linkedin.com/in/gurpreet-grewal",
	})

	markdown := renderCompanyMarkdown(chart)
	for _, expected := range []string{
		"Gurpreet Grewal",
		"current-unverified",
		"unable to verify",
	} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("expected the markdown roster to include %q", expected)
		}
	}

	html := renderCompanyHTML(chart)
	if !strings.Contains(html, "unable to verify") {
		t.Error("expected the HTML artifact to carry the unable-to-verify remark on the row")
	}

	// The freshness section reports the kept-but-unverified count separately.
	chart.Validation = &models.ValidationReport{RowsChecked: 3, RowsUnverified: 1}
	markdown = renderCompanyMarkdown(chart)
	if !strings.Contains(markdown, "current-unverified (kept, unable to verify)") {
		t.Errorf("expected the markdown freshness table to report the unverified count, got:\n%s", markdown)
	}
	html = renderCompanyHTML(chart)
	if !strings.Contains(html, "current-unverified (kept, unable to verify)") {
		t.Errorf("expected the HTML freshness table to report the unverified count")
	}
}