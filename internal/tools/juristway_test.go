package tools

// juristway_test.go pins the Juristway adapter's client behavior against a
// LoopBack-shaped fake: the filter shape the API expects, token caching and
// its graceful degradation, and the identity-layer extraction from the public
// case pages. The live test (RESEARCH_INTEGRATION=1) validates the same paths
// against the real API.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// juristwayFixtureRecord is one caseLaws record shaped exactly like the live
// API answers (LoopBack camelCase keys, HTML headnote).
const juristwayFixtureRecord = `{
	"id": "5d6921ccd91c770762fdcc87",
	"citation": "2017-juristway.com-36-HC(Delhi)-GST",
	"citationWithApplicant": "JK Mittal & Company-vs-Union of India & Ors.-2017-juristway.com-36-HC(Delhi)-GST",
	"citationPost": "GST",
	"caseDate": "2017-07-12",
	"caseStatus": "Good Law",
	"partyAppellant": "JK Mittal & Company",
	"partyRespondent": "Union of India & Ors.",
	"headNote": "<p style=\"text-align:justify\"><span>Juristway's headnote: the court examined input tax credit on works contract services.</span></p>",
	"slug": "jk-mittal-vs-union-of-india-delhi-hc-2017-2",
	"documentNumber": "W.P.(C) 5709/2017"
}`

func TestJuristwaySearchMapsRecords(t *testing.T) {
	var gotFilter, gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/caseLaws" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotFilter = r.URL.Query().Get("filter")
		gotToken = r.URL.Query().Get("access_token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[" + juristwayFixtureRecord + "]"))
	}))
	defer server.Close()

	client := NewJuristwayClient("", "")
	client.baseURL = server.URL
	hits, err := client.Search(context.Background(), `"JK Mittal"`, 1, 10)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if gotToken != "" {
		t.Errorf("anonymous search sent a token: %q", gotToken)
	}

	// The filter the client sends must carry the regex "like" operator with
	// options "i", the OR across the identity fields, and the planner's term
	// with the quotes stripped.
	var filter struct {
		Where  map[string]any  `json:"where"`
		Limit  int             `json:"limit"`
		Skip   int             `json:"skip"`
		Order  string          `json:"order"`
		Fields map[string]bool `json:"fields"`
	}
	if err := json.Unmarshal([]byte(gotFilter), &filter); err != nil {
		t.Fatalf("filter is not JSON: %v\n%s", err, gotFilter)
	}
	orBlock, ok := filter.Where["or"].([]any)
	if !ok || len(orBlock) != 4 {
		t.Fatalf("expected a 4-field OR where clause, got: %s", gotFilter)
	}
	first := orBlock[0].(map[string]any)["citationWithApplicant"].(map[string]any)
	if first["like"] != "JK Mittal" {
		t.Errorf("expected the planner's quotes stripped, got like=%q", first["like"])
	}
	if first["options"] != "i" {
		t.Errorf("expected options=i (ilike answers 500 live), got %v", first["options"])
	}
	if filter.Order != "caseDate DESC" || filter.Limit != 10 || filter.Skip != 0 {
		t.Errorf("unexpected order/limit/skip: %s", gotFilter)
	}
	if !filter.Fields["slug"] || !filter.Fields["headNote"] {
		t.Errorf("field projection lost slug/headNote: %s", gotFilter)
	}

	hit := hits[0]
	if hit.Title != "JK Mittal & Company-vs-Union of India & Ors.-2017-juristway.com-36-HC(Delhi)-GST" {
		t.Errorf("title = %q", hit.Title)
	}
	if hit.Court != "Delhi High Court" {
		t.Errorf("court = %q", hit.Court)
	}
	if hit.Date != "2017-07-12" || hit.CaseNumber != "W.P.(C) 5709/2017" {
		t.Errorf("date/number = %q/%q", hit.Date, hit.CaseNumber)
	}
	if hit.Petitioner != "JK Mittal & Company" || hit.Respondent != "Union of India & Ors." {
		t.Errorf("parties = %q vs %q", hit.Petitioner, hit.Respondent)
	}
	if hit.DocURL != server.URL+"/caselaw/jk-mittal-vs-union-of-india-delhi-hc-2017-2" {
		t.Errorf("doc url = %q", hit.DocURL)
	}
	if hit.DocType != "judgment" {
		t.Errorf("doc type = %q", hit.DocType)
	}
	if strings.Contains(hit.Snippet, "<p") || strings.Contains(hit.Snippet, "style=") {
		t.Errorf("snippet still carries HTML: %q", hit.Snippet)
	}
	if !strings.Contains(hit.Snippet, "input tax credit") {
		t.Errorf("snippet lost the headnote text: %q", hit.Snippet)
	}
}

func TestJuristwaySearchPaginationSkip(t *testing.T) {
	var gotSkip int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var filter struct {
			Skip int `json:"skip"`
		}
		_ = json.Unmarshal([]byte(r.URL.Query().Get("filter")), &filter)
		gotSkip = filter.Skip
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()
	client := NewJuristwayClient("", "")
	client.baseURL = server.URL
	if _, err := client.Search(context.Background(), "arbitration", 2, 10); err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if gotSkip != 10 {
		t.Errorf("page 2 expected skip=10, got %d", gotSkip)
	}
}

func TestJuristwayLoginCachesToken(t *testing.T) {
	logins, listings := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			logins++
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["email"] != "amit@test.example" || body["password"] != "pw" {
				t.Errorf("login payload = %v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"tok-1","userId":"u1","ttl":1209600,"created":"2026-10-27T10:23:04.056Z"}`))
		case "/api/caseLaws":
			listings++
			if got := r.URL.Query().Get("access_token"); got != "tok-1" {
				t.Errorf("listing sent token %q", got)
			}
			_, _ = w.Write([]byte("[]"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewJuristwayClient("amit@test.example", "pw")
	client.baseURL = server.URL
	for i := 0; i < 2; i++ {
		if _, err := client.Search(context.Background(), "arbitration", 1, 5); err != nil {
			t.Fatalf("search %d failed: %v", i+1, err)
		}
	}
	if logins != 1 {
		t.Errorf("expected one login across two searches, got %d", logins)
	}
	if listings != 2 {
		t.Errorf("expected two listings, got %d", listings)
	}
}

func TestJuristwayStaleTokenDegradesToAnonymous(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"statusCode":401}}`))
			return
		}
		if got := r.URL.Query().Get("access_token"); got != "" {
			t.Errorf("retry after 401 still sent a token: %q", got)
		}
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	client := NewJuristwayClient("amit@test.example", "pw")
	client.baseURL = server.URL
	// Pre-seed a token the server will reject, with a future expiry: the
	// client must believe it is fresh to exercise the 401 path.
	client.token = "stale"
	client.tokenExpires = time.Now().Add(time.Hour)
	if _, err := client.Search(context.Background(), "arbitration", 1, 5); err != nil {
		t.Fatalf("search should degrade to anonymous, got: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected a 401 retry, got %d calls", calls)
	}
	if client.token != "" {
		t.Errorf("the rejected token was not dropped")
	}
}

func TestJuristwayFetchDocument(t *testing.T) {
	page := `<!doctype html><html><head><title>JK Mittal vs Union of India (Delhi High Court 2017) GST | Juristway</title></head>
	<body><nav>chrome</nav><main><h1>JK Mittal vs Union of India</h1>
	<div class="case-citation">2017-juristway.com-36-HC(Delhi)-GST</div>
	<p>The Court examined the claim of input tax credit and set aside the impugned order.</p></main><footer>chrome</footer></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/caselaw/jk-mittal-vs-union-of-india-delhi-hc-2017-2":
			_, _ = w.Write([]byte(page))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewJuristwayClient("", "")
	client.baseURL = server.URL
	doc, err := client.FetchDocument(context.Background(), "jk-mittal-vs-union-of-india-delhi-hc-2017-2")
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if !strings.HasSuffix(doc.URL, "/caselaw/jk-mittal-vs-union-of-india-delhi-hc-2017-2") {
		t.Errorf("doc url = %q", doc.URL)
	}
	if doc.Title != "JK Mittal vs Union of India" {
		t.Errorf("title = %q", doc.Title)
	}
	if doc.Court != "Delhi High Court" {
		t.Errorf("court = %q", doc.Court)
	}
	if !strings.Contains(doc.Text, "input tax credit") || strings.Contains(doc.Text, "chrome") {
		t.Errorf("text extraction: %q", doc.Text)
	}

	if _, err := client.FetchDocument(context.Background(), "https://evil.example/caselaw/x"); err == nil {
		t.Errorf("off-host url was not refused")
	}
	if _, err := client.FetchDocument(context.Background(), "missing-page"); err == nil {
		t.Errorf("a 404 page was not refused")
	}
}

func TestJuristwayRobotsRefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			_, _ = w.Write([]byte("User-Agent: *\nAllow: /\nDisallow: /caselaw/removed/doc-1/\n"))
		default:
			t.Errorf("unexpected fetch of %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewJuristwayClient("", "")
	client.baseURL = server.URL
	// The guard is built on the constructor's base URL; point it at the fake
	// so the disallow list under test is the fake's. The guard matches the
	// exact path strings robots.txt disallows (the same semantics the
	// IndianKanoon client is held to), so the disallow names the document
	// path itself.
	client.robots = newRobotsGuard(server.URL + "/robots.txt")
	if _, err := client.FetchDocument(context.Background(), "/caselaw/removed/doc-1"); err == nil {
		t.Errorf("robots disallowed path was fetched")
	}
}

func TestParseJuristwayCourt(t *testing.T) {
	cases := []struct {
		citation string
		want     string
	}{
		{"2017-juristway.com-36-HC(Delhi)-GST", "Delhi High Court"},
		{"2026-juristway.com-3120-HC(Gujarat)-GST", "Gujarat High Court"},
		{"2025-juristway.com-101-SC-ARBITRATION", "Supreme Court of India"},
		{"2024-juristway.com-77-ITAT(Mumbai)-INCOME-TAX", "ITAT, Mumbai"},
		{"2019-juristway.com-12-NCLT(New Delhi)-IBC", "NCLT, New Delhi"},
		{"", ""},
		{"unparseable", ""},
	}
	for _, testCase := range cases {
		if got := parseJuristwayCourt(testCase.citation); got != testCase.want {
			t.Errorf("parseJuristwayCourt(%q) = %q, want %q", testCase.citation, got, testCase.want)
		}
	}
}

func TestJuristwaySnippetCap(t *testing.T) {
	long := strings.Repeat("x", juristwaySnippetCap+50)
	capped := capJuristwayText(long, juristwaySnippetCap)
	if len([]rune(capped)) != juristwaySnippetCap+1 { // the ellipsis
		t.Errorf("cap = %d runes, want %d", len([]rune(capped)), juristwaySnippetCap+1)
	}
}

// TestJuristwayLiveSearch runs the real API when RESEARCH_INTEGRATION=1.
// Credentials are optional: the listing answers anonymously, and when
// JURISTWAY_EMAIL/JURISTWAY_PASSWORD are set the same run also validates the
// token path.
func TestJuristwayLiveSearch(t *testing.T) {
	if os.Getenv("RESEARCH_INTEGRATION") != "1" {
		t.Skip("live juristway probe needs RESEARCH_INTEGRATION=1")
	}
	client := NewJuristwayClient(os.Getenv("JURISTWAY_EMAIL"), os.Getenv("JURISTWAY_PASSWORD"))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	hits, err := client.Search(ctx, "JK Mittal", 1, 10)
	if err != nil {
		t.Fatalf("live search failed: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("live search returned no hits")
	}
	t.Logf("live Juristway: %d hit(s), first = %q (%s, %s)", len(hits), hits[0].Title, hits[0].Court, hits[0].Date)
}
