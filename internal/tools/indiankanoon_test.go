package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The fixtures below mirror the live indiankanoon.org markup shape measured in
// October 2026: <article class="result"> items whose title link carries a
// /docfragment/ id for judgments (and /doc/ for statutes), a .headline that
// often includes the judgment's PETITIONER/RESPONDENT metadata, and a
// .hlbottom footer with the court and citation tags. If the live shape drifts,
// the integration test (LEGAL_INTEGRATION=1) is what catches it.

const ikSearchFixture = `
<div class="results-list" role="list" aria-label="Search results list">
<article class="result" role="listitem">
  <h4 class="result_title"><a href="/docfragment/39169380/?formInput=acme"><b>Acme</b> Robotics Pvt Ltd vs State Of Maharashtra on 5 November, 2004</a></h4>
  <div class="headline">
    <b>Acme</b> Robotics vs State Of Maharashtra
    CASE NO.: Appeal (civil) 2582 of 1998
    PETITIONER: <b>Acme</b> Robotics Pvt Ltd
    RESPONDENT: State of Maharashtra
    DATE OF JUDGMENT: 05/11/2004
  </div>
  <div class="hlbottom">
    <span class="docsource">Supreme Court of India</span>
    <a class="cite_tag" href="/search/?formInput=cites:39169380">Cites 44</a>
    - <a class="cite_tag" href="/search/?formInput=citedby:39169380">Cited by 12</a>
  </div>
</article>
<article class="result" role="listitem">
  <h4 class="result_title"><a href="/doc/81781005/">Section 115JB in The Income Tax Act, 1961</a> [<a href="/doc/789969/">Entire Act</a>]</h4>
  <div class="headline">company against whom an application for corporate insolvency resolution process has been admitted</div>
  <div class="hlbottom">
    <span class="docsource">Union of India - Section</span>
    <a class="cite_tag" href="/search/?formInput=cites:81781005">Cites 0</a>
    - <a class="cite_tag" href="/search/?formInput=citedby:81781005">Cited by 4802</a>
  </div>
</article>
<article class="result" role="listitem">
  <h4 class="result_title"><a href="/docfragment/54321/">Bharat Traders vs Acme Robotics Ltd on 12 March, 2019</a></h4>
  <div class="headline">winding up petition filed by the operational creditor against the corporate debtor</div>
  <div class="hlbottom">
    <span class="docsource">Bombay High Court</span>
    <a class="cite_tag" href="/search/?formInput=cites:54321">Cites 2</a>
  </div>
</article>
</div>`

func TestParseIndianKanoonResults(t *testing.T) {
	hits := parseIndianKanoonResults([]byte(ikSearchFixture), 10)
	if len(hits) != 3 {
		t.Fatalf("expected 3 hits, got %d", len(hits))
	}

	first := hits[0]
	if first.DocID != "39169380" {
		t.Errorf("DocID = %q, want 39169380 (from a /docfragment/ link)", first.DocID)
	}
	if first.DocURL != "/doc/39169380/" {
		t.Errorf("DocURL = %q, want the canonical /doc/ form", first.DocURL)
	}
	if first.Title != "Acme Robotics Pvt Ltd vs State Of Maharashtra on 5 November, 2004" {
		t.Errorf("Title = %q, want the <b> highlight stripped", first.Title)
	}
	if first.DocType != "judgment" {
		t.Errorf("DocType = %q, want judgment", first.DocType)
	}
	if first.Court != "Supreme Court of India" {
		t.Errorf("Court = %q", first.Court)
	}
	if first.Petitioner != "Acme Robotics Pvt Ltd" {
		t.Errorf("Petitioner = %q, want the headline metadata value", first.Petitioner)
	}
	if first.Respondent != "State of Maharashtra" {
		t.Errorf("Respondent = %q", first.Respondent)
	}
	if first.CaseNumber == "" || !strings.Contains(first.CaseNumber, "2582 of 1998") {
		t.Errorf("CaseNumber = %q, want the headline CASE NO.", first.CaseNumber)
	}
	if first.Date != "05/11/2004" {
		t.Errorf("Date = %q, want the headline DATE OF JUDGMENT to win", first.Date)
	}
	if first.CitedBy != 12 {
		t.Errorf("CitedBy = %d, want 12", first.CitedBy)
	}

	second := hits[1]
	if second.DocType != "statute" {
		t.Errorf("statute hit DocType = %q, want statute (Section-in-Act title)", second.DocType)
	}
	if second.DocID != "81781005" {
		// The statute's second link ("Entire Act") must not take the title's place.
		t.Errorf("statute DocID = %q, want the title link's own id", second.DocID)
	}

	third := hits[2]
	if third.DocType != "judgment" {
		t.Errorf("third DocType = %q, want judgment", third.DocType)
	}
	if third.Date != "12 March, 2019" {
		t.Errorf("third Date = %q, want the title's 'on' suffix", third.Date)
	}
	if third.Petitioner != "" {
		t.Errorf("third Petitioner = %q, want empty when the headline carries no metadata block", third.Petitioner)
	}
}

func TestParseIndianKanoonResultsMaxResults(t *testing.T) {
	hits := parseIndianKanoonResults([]byte(ikSearchFixture), 2)
	if len(hits) != 2 {
		t.Fatalf("maxResults=2: got %d hits, want 2", len(hits))
	}
}

const ikDocFixture = `
<html><head><title>Acme Robotics Pvt Ltd vs State Of Maharashtra on 5 November, 2004</title></head>
<body class="docpage">
<div class="maindoc">
  <div class="docsource_main">Supreme Court of India</div>
  <div class="doc_title">Acme Robotics Pvt Ltd vs State Of Maharashtra on 5 November, 2004</div>
  <div class="judgments">
    PETITIONER: ACME ROBOTICS PRIVATE LIMITED
    RESPONDENT: STATE OF MAHARASHTRA
    DATE OF JUDGMENT: 05/11/2004
    The appeal is allowed. The assessment order is set aside.
  </div>
</div>
</body></html>`

func TestParseIndianKanoonDocument(t *testing.T) {
	document := parseIndianKanoonDocument("https://indiankanoon.org/doc/39169380/", []byte(ikDocFixture))
	if document.Title == "" || !strings.Contains(document.Title, "Acme Robotics") {
		t.Errorf("Title = %q", document.Title)
	}
	if document.Court != "Supreme Court of India" {
		t.Errorf("Court = %q", document.Court)
	}
	if !strings.Contains(document.Text, "assessment order is set aside") {
		t.Errorf("Text is missing the judgment body: %q", document.Text)
	}
	if strings.Contains(document.Text, "\n") {
		t.Errorf("Text should be whitespace-collapsed, got newlines")
	}
}

func TestParseIndianKanoonDocumentFallbacks(t *testing.T) {
	// A page without the expected containers still yields a URL-carrying
	// document rather than a parse crash — the identity pass degrades, the
	// run does not.
	document := parseIndianKanoonDocument("https://indiankanoon.org/doc/9/", []byte("<html><body><p>broken</p></body></html>"))
	if document.URL == "" {
		t.Fatal("URL must always be set")
	}
}

// ikTestClient builds a client against a test server with the politeness
// limiter disabled: unit tests do not owe the real site a one-second pacing.
func ikTestClient(server *httptest.Server) *IndianKanoonClient {
	return &IndianKanoonClient{
		baseURL:    server.URL,
		httpClient: server.Client(),
		limiter:    newHostLimiter(0, 0, 0),
	}
}

func TestIndianKanoonSearchPassthrough(t *testing.T) {
	var gotPath, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query().Get("formInput")
		_, _ = w.Write([]byte(ikSearchFixture))
	}))
	defer server.Close()

	hits, err := ikTestClient(server).Search(context.Background(), `"Acme Robotics" sortby:mostrecent`, 1, 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if gotPath != "/search/" {
		t.Errorf("request path = %q", gotPath)
	}
	if gotQuery != `"Acme Robotics" sortby:mostrecent` {
		t.Errorf("formInput = %q, want the query passed through unmodified", gotQuery)
	}
	if len(hits) != 3 {
		t.Errorf("hits = %d, want 3", len(hits))
	}
}

func TestIndianKanoonSearchPagenum(t *testing.T) {
	var fullQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fullQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`<div class="results-list"></div>`))
	}))
	defer server.Close()

	if _, err := ikTestClient(server).Search(context.Background(), "acme", 2, 5); err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if !strings.Contains(fullQuery, "pagenum=2") {
		t.Errorf("page 2 must carry the site's pagenum parameter: %q", fullQuery)
	}
}

func TestIndianKanoonSearchAddsJudgmentFilter(t *testing.T) {
	var fullQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fullQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`<div class="results-list"></div>`))
	}))
	defer server.Close()

	if _, err := ikTestClient(server).Search(context.Background(), "acme", 1, 5); err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if !strings.Contains(fullQuery, "doctype=judgments") {
		t.Errorf("search query %q must carry doctype=judgments", fullQuery)
	}
}

func TestIndianKanoonSearchEmptyQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("an empty query must not reach the network")
	}))
	defer server.Close()

	if _, err := ikTestClient(server).Search(context.Background(), "   ", 1, 5); err == nil {
		t.Fatal("an empty query must be refused")
	}
}

func TestIndianKanoonFetchDocumentNormalisesURL(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(ikDocFixture))
	}))
	defer server.Close()

	document, err := ikTestClient(server).FetchDocument(context.Background(), server.URL+"/docfragment/39169380/?formInput=acme")
	if err != nil {
		t.Fatalf("FetchDocument failed: %v", err)
	}
	if gotPath != "/doc/39169380/" {
		t.Errorf("fetched path = %q, want the normalised /doc/ form", gotPath)
	}
	if document.Text == "" {
		t.Error("document text should carry the judgment body")
	}
}

func TestIndianKanoonFetchDocumentRejectsNonDocURL(t *testing.T) {
	client := &IndianKanoonClient{baseURL: "https://indiankanoon.org", limiter: newHostLimiter(0, 0, 0)}
	if _, err := client.FetchDocument(context.Background(), "https://indiankanoon.org/search/?formInput=x"); err == nil {
		t.Fatal("a URL without a document id must be refused")
	}
}
func TestIndianKanoonSearchErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	if _, err := ikTestClient(server).Search(context.Background(), "acme", 1, 5); err == nil {
		t.Fatal("a 403 must surface as an error, not an empty hit list")
	}
}

func TestRobotsGuardBlocksDisallowedDoc(t *testing.T) {
	robots := "User-agent: *\nDisallow: /doc/1745798/\nDisallow: /cached/\n\nUser-agent: OtherBot\nDisallow: /doc/999/\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/robots.txt" {
			t.Errorf("guard fetched %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(robots))
	}))
	defer server.Close()

	guard := newRobotsGuard(server.URL + "/robots.txt")
	if guard.allows("/doc/1745798/") {
		t.Error("/doc/1745798/ is disallowed and must be refused")
	}
	if !guard.allows("/doc/1745799/") {
		t.Error("a neighbouring document must stay allowed")
	}
	if !guard.allows("/doc/999/") {
		t.Error("a rule from a specific user-agent group must not bind this client")
	}
}

func TestRobotsGuardFailsOpen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	guard := newRobotsGuard(server.URL + "/robots.txt")
	if !guard.allows("/doc/1/") {
		t.Error("an unreadable robots.txt must fail open, not blind the mode")
	}
}

func TestRobotsGuardIgnoresMalformedGroups(t *testing.T) {
	// A record with no User-Agent group is malformed per the robots exclusion
	// protocol; the guard honours the User-Agent: * group only, and the live
	// site's robots.txt carries exactly that group. A nameless Disallow is not
	// addressed to this client.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("Disallow: /doc/42/\n"))
	}))
	defer server.Close()

	guard := newRobotsGuard(server.URL + "/robots.txt")
	if !guard.allows("/doc/42/") {
		t.Error("a nameless Disallow record must not bind the client")
	}
}

// TestIndianKanoonLiveSearch pins the parser against the live site. It runs
// only with LEGAL_INTEGRATION=1, like the other opt-in integration tests, so
// the default suite stays offline.
func TestIndianKanoonLiveSearch(t *testing.T) {
	if os.Getenv("LEGAL_INTEGRATION") != "1" {
		t.Skip("LEGAL_INTEGRATION=1 not set; skipping live IndianKanoon search")
	}

	client := NewIndianKanoonClient()
	hits, err := client.Search(context.Background(), `"Tata Consultancy Services"`, 1, 10)
	if err != nil {
		t.Fatalf("live search failed: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("live search returned no hits")
	}
	judgments := 0
	for _, hit := range hits {
		if hit.DocID == "" {
			t.Errorf("hit %q carries no document id", hit.Title)
		}
		if hit.DocType == "judgment" {
			judgments++
			if hit.Court == "" {
				t.Errorf("judgment %q carries no court", hit.Title)
			}
		}
	}
	if judgments == 0 {
		t.Error("a doctype=judgments search returned no judgments")
	}
}
