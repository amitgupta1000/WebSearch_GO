package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// datedSearcher returns canned results and records the queries it was asked, so
// a test can assert both what the validation pass found and what it spent.
type datedSearcher struct {
	results []models.SearchResult
	queries []string
}

func (s *datedSearcher) Search(_ context.Context, query string, _ int) ([]models.SearchResult, error) {
	s.queries = append(s.queries, query)
	return s.results, nil
}

// activityURL builds a LinkedIn post URL whose id decodes to the given date,
// which is how the pass dates a snippet without fetching it.
func activityURL(when time.Time) string {
	return fmt.Sprintf("https://www.linkedin.com/posts/someone_topic-activity-%d-KOqH", uint64(when.UnixMilli())<<22)
}

// freshnessFixture is the shape of a real run: one row the company itself
// confirms, one that only a 2021 post supports, and one that rests on an
// undated aggregator listing.
func freshnessFixture() (*models.OrgChart, *models.CompanyResearchContext, models.CompanyBrief) {
	brief := models.CompanyBrief{CompanyName: "KLJ Resources Limited", Domain: "kljresources.in"}
	post := "https://www.linkedin.com/posts/vinod-trivedi-2835221a_announcement-activity-6771005891878625280-KOqH"
	chart := &models.OrgChart{
		CompanyName: brief.CompanyName,
		Domain:      brief.Domain,
		Leadership: []models.Employee{
			{Name: "Hemant Jain", Title: "Managing Director", PublicSource: "http://kljresources.in/management.php"},
			{Name: "Suresh Chand Jalther", Title: "Chief Financial Officer", PublicSource: post},
			{Name: "Rajesh Zaveri", Title: "Head Of Procurement", PublicSource: "https://rocketreach.co/rajesh-zaveri-email_49445374"},
		},
	}
	evidence := []models.Evidence{
		{
			URL:          "http://kljresources.in/management.php",
			Title:        "Management",
			Content:      "Hemant Jain Managing Director",
			LastModified: time.Now().UTC().Format(time.RFC1123),
		},
		{URL: post, Title: "Appointment notice", Content: "Suresh Chand Jalther"},
		{URL: "https://rocketreach.co/rajesh-zaveri-email_49445374", Title: "Rajesh Zaveri", Content: "Rajesh Zaveri Head Of Procurement"},
	}
	return chart, &models.CompanyResearchContext{Brief: brief, Evidence: evidence}, brief
}

// newValidationEngine builds an engine with the validation knobs under test.
func newValidationEngine(searcher Searcher, lookups int, enabled bool) *Engine {
	return NewEngine(&mockLLM{}, searcher, &failingScraper{}, cache.NewMemoryCache(50), 10, 5, 20,
		WithValidation(enabled, lookups, 36, 18))
}

// TestValidateChartLabelsRowsFromTheSourcesTheyAlreadyHave covers the offline
// half: a company-owned source modified today is current, a 2021 post is stale,
// and an undated aggregator listing is undated.
func TestValidateChartLabelsRowsFromTheSourcesTheyAlreadyHave(t *testing.T) {
	chart, state, brief := freshnessFixture()
	eng := newValidationEngine(&datedSearcher{}, 4, true)
	agent := NewCompanyAgent(eng)

	report := agent.validateChart(context.Background(), "sess_test", brief, chart, state, nil, nil)

	if report.RowsChecked != 3 {
		t.Fatalf("expected 3 rows checked, got %d", report.RowsChecked)
	}
	if report.RowsRecent != 1 || report.RowsStale != 1 || report.RowsUndated != 1 {
		t.Fatalf("unexpected verdict counts: %+v", report)
	}
	want := []struct {
		index   int
		verdict string
		asOf    string
	}{
		{0, models.VerificationRecent, time.Now().UTC().Format(asOfLayout)},
		{1, models.VerificationStale, "2021-02-26"},
		{2, models.VerificationUndated, ""},
	}
	for _, expected := range want {
		person := chart.Leadership[expected.index]
		if person.Verification != expected.verdict {
			t.Errorf("%s: expected verdict %s, got %s", person.Name, expected.verdict, person.Verification)
		}
		if person.AsOf != expected.asOf {
			t.Errorf("%s: expected as-of %q, got %q", person.Name, expected.asOf, person.AsOf)
		}
	}
	// The two unresolved people get one lookup each, and the stub finds nothing.
	if report.LookupsUsed != 2 {
		t.Fatalf("expected 2 lookups (stale + undated), got %d", report.LookupsUsed)
	}
	for _, lookup := range report.Lookups {
		if lookup.Status != "no-results" {
			t.Fatalf("expected no-results, got %+v", lookup)
		}
	}
}

// TestValidateChartFlagsTwoPeopleClaimingOneTitle is the two-CFO case that QC
// scored 1.00: it must be caught without any request.
func TestValidateChartFlagsTwoPeopleClaimingOneTitle(t *testing.T) {
	chart, state, brief := freshnessFixture()
	chart.Leadership = append(chart.Leadership, models.Employee{
		Name: "Sanjiv Goyal", Title: "CFO", PublicSource: "https://www.datanyze.com/companies/klj-resources/541992750",
	})
	eng := newValidationEngine(&datedSearcher{}, 0, true)
	agent := NewCompanyAgent(eng)

	report := agent.validateChart(context.Background(), "sess_test", brief, chart, state, nil, nil)

	if len(report.Conflicts) != 1 {
		t.Fatalf("expected one conflict, got %v", report.Conflicts)
	}
	for _, index := range []int{1, 3} {
		person := chart.Leadership[index]
		if person.Verification != models.VerificationConflicting {
			t.Errorf("%s: expected conflicting, got %s", person.Name, person.Verification)
		}
		if !strings.Contains(person.VerificationNote, "Chief Financial Officer") {
			t.Errorf("%s: expected the conflict note to name the title, got %q", person.Name, person.VerificationNote)
		}
	}
	if !strings.Contains(report.Skipped, "VALIDATION_LOOKUPS_PER_RUN is 0") {
		t.Fatalf("expected the zero-cap reason, got %q", report.Skipped)
	}
}

// TestValidateChartPromotesOnADatedRecentMention covers the reverse-search half:
// one snippet-only lookup finds a dated recent mention, the row is promoted, and
// the roster is untouched — validation never introduces people.
func TestValidateChartPromotesOnADatedRecentMention(t *testing.T) {
	chart, state, brief := freshnessFixture()
	recent := time.Now().UTC().AddDate(0, -1, 0)
	searcher := &datedSearcher{results: []models.SearchResult{
		{Title: "Recent post", Link: activityURL(recent), Snippet: "Rajesh Zaveri at KLJ Resources Limited"},
		{Title: "Undated directory", Link: "https://rocketreach.co/someone", Snippet: "no date here"},
	}}
	agent := NewCompanyAgent(newValidationEngine(searcher, 4, true))

	leadershipBefore := len(chart.Leadership)
	report := agent.validateChart(context.Background(), "sess_test", brief, chart, state, nil, nil)

	if len(chart.Leadership) != leadershipBefore {
		t.Fatalf("validation must not add people: %d -> %d", leadershipBefore, len(chart.Leadership))
	}

	procurement := chart.Leadership[2]
	if procurement.Verification != models.VerificationRecent {
		t.Fatalf("expected the undated row to be promoted, got %s", procurement.Verification)
	}
	if procurement.AsOf != recent.Format(asOfLayout) {
		t.Fatalf("expected as-of %s, got %s", recent.Format(asOfLayout), procurement.AsOf)
	}
	if len(procurement.VerifiedBy) == 0 || procurement.VerifiedBy[0] != activityURL(recent) {
		t.Fatalf("expected the dated mention to lead VerifiedBy, got %v", procurement.VerifiedBy)
	}
	if !strings.Contains(procurement.VerificationNote, "reverse-validated") {
		t.Fatalf("expected a validation note, got %q", procurement.VerificationNote)
	}

	if want := `"Rajesh Zaveri" "KLJ Resources Limited"`; !contains(searcher.queries, want) {
		t.Fatalf("expected the quotes-and-company query shape %s, got %v", want, searcher.queries)
	}

	// The lookup snippets are retained as evidence for follow-up questions.
	var validationEvidence int
	for _, item := range state.Evidence {
		if item.SourceType == "validation" {
			validationEvidence++
		}
	}
	if validationEvidence == 0 {
		t.Fatal("expected the lookup snippets to be retained as evidence")
	}
	if report.RowsRecent < 2 {
		t.Fatalf("expected the promoted row to be counted as recent, got %+v", report)
	}
}

// TestValidateChartRespectsTheLookupCap pins the budget: one lookup per run when
// the cap says one, and the reason is reported.
func TestValidateChartRespectsTheLookupCap(t *testing.T) {
	chart, state, brief := freshnessFixture()
	searcher := &datedSearcher{}
	agent := NewCompanyAgent(newValidationEngine(searcher, 1, true))

	report := agent.validateChart(context.Background(), "sess_test", brief, chart, state, nil, nil)

	if report.LookupsUsed != 1 {
		t.Fatalf("expected 1 lookup, got %d", report.LookupsUsed)
	}
	if len(searcher.queries) != 1 {
		t.Fatalf("expected exactly one search, got %v", searcher.queries)
	}
	if !strings.Contains(report.Skipped, "lookup cap reached (1)") {
		t.Fatalf("expected the cap reason, got %q", report.Skipped)
	}
}

// TestValidateChartKeepsLabelsWhenTheBudgetIsGone pins the degradation path: a
// run that spent its whole budget still gets every row dated and labelled, it
// just gets no reverse lookups.
func TestValidateChartKeepsLabelsWhenTheBudgetIsGone(t *testing.T) {
	chart, state, brief := freshnessFixture()
	searcher := &datedSearcher{}
	agent := NewCompanyAgent(newValidationEngine(searcher, 4, true))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report := agent.validateChart(ctx, "sess_test", brief, chart, state, nil, nil)

	if report.LookupsUsed != 0 || len(searcher.queries) != 0 {
		t.Fatalf("expected no lookups, got %d (%v)", report.LookupsUsed, searcher.queries)
	}
	if !strings.Contains(report.Skipped, "no run budget left") {
		t.Fatalf("expected the budget reason, got %q", report.Skipped)
	}
	if chart.Leadership[1].Verification != models.VerificationStale {
		t.Fatalf("expected the stale label to survive, got %s", chart.Leadership[1].Verification)
	}
}

// TestValidateChartDisabledStillLabelsRows covers the kill switch: no requests,
// but the artifact still shows what the sources say.
func TestValidateChartDisabledStillLabelsRows(t *testing.T) {
	chart, state, brief := freshnessFixture()
	searcher := &datedSearcher{}
	agent := NewCompanyAgent(newValidationEngine(searcher, 4, false))

	report := agent.validateChart(context.Background(), "sess_test", brief, chart, state, nil, nil)

	if report.Skipped != "validation disabled" {
		t.Fatalf("expected the disabled reason, got %q", report.Skipped)
	}
	if len(searcher.queries) != 0 {
		t.Fatalf("disabled validation must not search, got %v", searcher.queries)
	}
	if chart.Leadership[1].AsOf != "2021-02-26" {
		t.Fatalf("expected the offline labels, got %q", chart.Leadership[1].AsOf)
	}
}

// TestNameAndTitleNormalization backs the two comparisons the pass depends on.
func TestNameAndTitleNormalization(t *testing.T) {
	if got := normalizePersonName("Sh. Hemant Jain"); got != "hemant jain" {
		t.Fatalf("expected honorifics to be dropped, got %q", got)
	}
	if got := normalizePersonName("Smt. Kalpana Seth"); got != "kalpana seth" {
		t.Fatalf("expected honorifics to be dropped, got %q", got)
	}
	if normalizeTitle("CFO") != normalizeTitle("Chief Financial Officer") {
		t.Fatal("CFO and Chief Financial Officer must compare equal")
	}
	if normalizeTitle("Managing Director") == normalizeTitle("Head of Procurement") {
		t.Fatal("different titles must not compare equal")
	}
}

// TestCompanyOwnedSource covers the independence test that decides whether one
// source is enough for a recent verdict.
func TestCompanyOwnedSource(t *testing.T) {
	own := rowSource{URL: "https://kljresources.in/management.php", Domain: "kljresources.in"}
	if !companyOwnedSource(own, "kljresources.in") {
		t.Fatal("the company's own domain must count as company-owned")
	}
	if companyOwnedSource(rowSource{Domain: "rocketreach.co"}, "kljresources.in") {
		t.Fatal("an aggregator must not count as company-owned")
	}
	if !companyOwnedSource(rowSource{Domain: "example.com", Type: "official_report"}, "kljresources.in") {
		t.Fatal("a source the ranker marked official must count")
	}
}

// contains reports whether a slice holds a value.
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
