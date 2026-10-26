package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The fixture mirrors the live casemine.com result markup measured in October
// 2026 (a real search for "Tata Consultancy Services"): div.jdlist cards whose
// jdlink carries /judgement/in/<id>, a jd-title, a text-snippet with <em>
// highlights, and jd-court / jd-date / jd-cite metadata spans. The measured
// result pages run to hundreds of KB, so the fixture is wrapped in a page shell
// past the wall detector's size floor, the way a real response is. The
// integration test (CASMINE_INTEGRATION=1) is what catches live-shape drift.
var casemineSearchFixtureHead = `<!doctype html><html><head><title>Search Results — Casemine</title>
<style>` + strings.Repeat("/* shell */ .x{margin:0} ", 180) + `</style></head><body>
<div class="results-wrap">`
var casemineSearchFixtureTail = `</div><footer class="site-footer">Casemine search page</footer></body></html>`
var casemineSearchFixture = casemineSearchFixtureHead + `
<div class="jdlist card listing-card-container ovr_">
	<a id="link_5609aee0e4b0149711414fdd" target="_blank" class="jdlink" href="/judgement/in/5609aee0e4b0149711414fdd">
		<div class="jd-title h5 mb-0 ellipsis">Preeti Gupta And Another v. State Of Jharkhand And Another</div>
	</a>
	<p class="text-snippet mt-3 mb-2">
		...along with her husband who was working with <em>Acme</em> <em>Robotics</em> and was permanently residing at Mumbai...
	</p>
	<div class="jud-info text-uppercase text-truncate fs-12 mt-1">
		<span class="mr-0">Court:</span>
		<span class="jd-court">Supreme Court Of India</span>
	</div>
	<div class="jud-info text-truncate fs-12 mt-1">
		<span class="mr-2"><span class="mr-1">Date:</span><span class="jd-date">Aug 13, 2010</span></span>
		<span class="mr-2"><span class="mr-1">Cited By:</span><span class="jd-cite">2857</span></span>
	</div>
</div>
<div class="jdlist card listing-card-container ovr_">
	<a class="jdlink" href="/judgement/in/1b2c3d4e5f6a7b8c9d0e1f2a">
		<div class="jd-title h5 mb-0 ellipsis">Bharat Traders v. Acme Robotics Ltd</div>
	</a>
	<p class="text-snippet mt-3 mb-2">winding up petition filed by the operational creditor</p>
</div>` + casemineSearchFixtureTail

// casemineWallFixture is the measured wall document: 117 bytes of nginx 403.
const casemineWallFixture = "<html><head><title>403 Forbidden</title></head>\n<body>\n<center><h1>403 Forbidden</h1></center>\n</body></html>"

func TestParseCasemineResults(t *testing.T) {
	hits := parseCasemineResults(casemineSearchFixture, 10)
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}

	first := hits[0]
	if first.DocID != "5609aee0e4b0149711414fdd" {
		t.Errorf("DocID = %q", first.DocID)
	}
	if first.DocURL != "/judgement/in/5609aee0e4b0149711414fdd" {
		t.Errorf("DocURL = %q", first.DocURL)
	}
	if first.Title != "Preeti Gupta And Another v. State Of Jharkhand And Another" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.Court != "Supreme Court Of India" {
		t.Errorf("Court = %q", first.Court)
	}
	if first.Date != "Aug 13, 2010" {
		t.Errorf("Date = %q", first.Date)
	}
	if first.CitedBy != 2857 {
		t.Errorf("CitedBy = %d, want 2857", first.CitedBy)
	}
	if !strings.Contains(first.Snippet, "along with her husband") {
		t.Errorf("Snippet = %q", first.Snippet)
	}
	if strings.Contains(first.Snippet, "<em>") {
		t.Error("Snippet should be plain text with the <em> highlights stripped")
	}

	second := hits[1]
	if second.DocID != "1b2c3d4e5f6a7b8c9d0e1f2a" {
		t.Errorf("second DocID = %q", second.DocID)
	}
	if second.Court != "" || second.Date != "" {
		t.Errorf("a card without metadata must leave it empty: court=%q date=%q", second.Court, second.Date)
	}
}

func TestParseCasemineResultsMaxResults(t *testing.T) {
	hits := parseCasemineResults(casemineSearchFixture, 1)
	if len(hits) != 1 {
		t.Fatalf("maxResults=1: got %d hits", len(hits))
	}
}

func TestCasemineLooksBlocked(t *testing.T) {
	if !casemineLooksBlocked(casemineWallFixture) {
		t.Error("the measured 117-byte 403 must read as a wall")
	}
	if casemineLooksBlocked(casemineSearchFixture) {
		t.Error("a result page must not read as a wall")
	}
	if !casemineLooksBlocked("<html>tiny</html>") {
		t.Error("a suspiciously short page must read as a wall")
	}
}

// searchCountingScraper records which fetch path ran, so the ladder's order is
// pinned: plain fetch first, render only on a wall.
type searchCountingScraper struct {
	fetchCalls  int
	renderCalls int
	fetchBody   string
	fetchErr    error
	renderBody  string
	renderErr   error
}

func (s *searchCountingScraper) FetchHTML(_ context.Context, _ string) (string, error) {
	s.fetchCalls++
	return s.fetchBody, s.fetchErr
}

func (s *searchCountingScraper) RenderHTML(_ context.Context, _ string) (string, error) {
	s.renderCalls++
	return s.renderBody, s.renderErr
}

func newCountingCasemine(s *searchCountingScraper) *CasemineClient {
	return &CasemineClient{
		baseURL:    "https://www.casemine.com",
		fetchHTML:  s.FetchHTML,
		renderHTML: s.RenderHTML,
		limiter:    newHostLimiter(0, 0, 0),
	}
}

func TestCasemineSearchPageTwoIsEmpty(t *testing.T) {
	scraper := &searchCountingScraper{fetchBody: casemineSearchFixture}
	hits, err := newCountingCasemine(scraper).Search(context.Background(), `"Acme"`, 2, 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	// Measured: the site ignores the page parameter server-side, so page 2
	// answers empty and the caller's page walk ends after page 1.
	if len(hits) != 0 {
		t.Errorf("page 2 = %d hits, want 0 (single-page source)", len(hits))
	}
}

func TestCasemineSearchPlainFetchFirst(t *testing.T) {
	scraper := &searchCountingScraper{fetchBody: casemineSearchFixture}
	hits, err := newCountingCasemine(scraper).Search(context.Background(), `"Acme Robotics"`, 1, 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if scraper.fetchCalls != 1 || scraper.renderCalls != 0 {
		t.Errorf("fetch=%d render=%d, want the plain fetch to win with no render", scraper.fetchCalls, scraper.renderCalls)
	}
	if len(hits) != 2 {
		t.Errorf("hits = %d, want 2", len(hits))
	}
}

func TestCasemineSearchWallFallsBackToRender(t *testing.T) {
	scraper := &searchCountingScraper{fetchBody: casemineWallFixture, renderBody: casemineSearchFixture}
	hits, err := newCountingCasemine(scraper).Search(context.Background(), `"Acme Robotics"`, 1, 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if scraper.fetchCalls != 1 || scraper.renderCalls != 1 {
		t.Errorf("fetch=%d render=%d, want plain fetch then render fallback", scraper.fetchCalls, scraper.renderCalls)
	}
	if len(hits) != 2 {
		t.Errorf("hits = %d, want 2 from the rendered page", len(hits))
	}
}

func TestCasemineSearchRenderFailureSurfacesBoth(t *testing.T) {
	scraper := &searchCountingScraper{
		fetchBody: casemineWallFixture,
		renderErr: errors.New("browser unavailable"),
	}
	_, err := newCountingCasemine(scraper).Search(context.Background(), `"Acme Robotics"`, 1, 10)
	if err == nil {
		t.Fatal("a wall plus a failed render must surface as an error")
	}
	if !strings.Contains(err.Error(), "wall") || !strings.Contains(err.Error(), "browser unavailable") {
		t.Errorf("error should name both stages: %v", err)
	}
}

func TestCasemineSearchFetchErrorFallsBackToRender(t *testing.T) {
	scraper := &searchCountingScraper{fetchErr: errors.New("403"), renderBody: casemineSearchFixture}
	hits, err := newCountingCasemine(scraper).Search(context.Background(), `"Acme Robotics"`, 1, 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if scraper.renderCalls != 1 {
		t.Error("a fetch error must trigger the render fallback")
	}
	if len(hits) != 2 {
		t.Errorf("hits = %d, want 2", len(hits))
	}
}

func TestCasemineSearchEmptyQuery(t *testing.T) {
	scraper := &searchCountingScraper{}
	if _, err := newCountingCasemine(scraper).Search(context.Background(), "  ", 1, 5); err == nil {
		t.Fatal("an empty query must be refused")
	}
	if scraper.fetchCalls != 0 {
		t.Error("an empty query must not reach the network")
	}
}

func TestCasemineQueryEscape(t *testing.T) {
	if got := queryEscape(`"Acme Robotics" sortby`); got != `%22Acme+Robotics%22+sortby` {
		t.Errorf("queryEscape = %q", got)
	}
}
