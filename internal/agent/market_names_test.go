package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestMarketCanonicalNameFoldsPageTitlesToTheCompany is the surfacing fix: the
// roster name is the company segment of a page title, not the SEO title.
func TestMarketCanonicalNameFoldsPageTitlesToTheCompany(t *testing.T) {
	cases := []struct {
		title  string
		domain string
		want   string
	}{
		{"Industrial Lubricants Manufacturer | Gulf Oil Middle East", "gulfoilltd.com", "Gulf Oil Middle East"},
		{"KLJ Resources Limited | Specialty Chemicals & Mining", "kljresources.com", "KLJ Resources Limited"},
		{"Home | Acme Oils & Greases", "acmeoils.com", "Acme Oils & Greases"},
		{"Acme Oils - Industrial Lubricants Manufacturer", "acmeoils.com", "Acme Oils"},
		{"Acme Oils | Contact Us", "acmeoils.com", "Acme Oils"},
		{"Welcome to Peninsula Lubricants | Products", "peninsulalubricants.com", "Peninsula Lubricants"},
	}
	for _, testCase := range cases {
		if got := marketCanonicalName(testCase.title, testCase.domain); got != testCase.want {
			t.Errorf("marketCanonicalName(%q, %q) = %q, want %q", testCase.title, testCase.domain, got, testCase.want)
		}
	}
}

// TestMarketCanonicalNameDropsDocumentTitles pins the research-report leak: a
// report title carries no company segment, so it can never become a name.
func TestMarketCanonicalNameDropsDocumentTitles(t *testing.T) {
	for _, title := range []string{
		"Lubricants Market Size, Share & Trends Analysis Report 2025-2033",
		"Global Lubricant Market Growth 2025-2033",
		"Top 10 Lubricant Manufacturers in India",
		"Lubricant Industry Outlook and Forecast",
		"Best 5 Lubricant Suppliers in UAE",
		"Lubricant Market Research Report | PDF",
	} {
		if got := marketCanonicalName(title, "analyticsvendor.example"); got != "" {
			t.Errorf("marketCanonicalName(%q) = %q, want no company name", title, got)
		}
	}
}

// TestMarketHeadlineTitleCatchesNewsArticles pins the news leak: an article
// headline is shaped like a subject plus a news verb, which a trading name is
// not.
func TestMarketHeadlineTitleCatchesNewsArticles(t *testing.T) {
	headlines := []string{
		"Sinopec opens new lubricant blending plant in Singapore",
		"Shell to sell its Singapore lube plant",
		"BP announces $2bn investment in blending facility",
		"Exclusive: Gulf Oil plans plant in Fujairah",
		"Sinopec commissions new blending unit | Reuters",
		"TotalEnergies inks pact to expand lube capacity",
	}
	for _, title := range headlines {
		if !marketHeadlineTitle(title) {
			t.Errorf("marketHeadlineTitle(%q) = false, want a headline", title)
		}
	}
	names := []string{
		"Gulf Oil Middle East",
		"Acme Lubricants and Greases",
		"Open Text Corporation",
		"KLJ Resources Limited",
		"Sinopec",
	}
	for _, name := range names {
		if marketHeadlineTitle(name) {
			t.Errorf("marketHeadlineTitle(%q) = true, want a company name", name)
		}
	}
}

// TestMarketDocumentTitleIgnoresProductWordsInsideNames keeps the gate from
// over-firing on ordinary trading names and words that merely contain a marker.
func TestMarketDocumentTitleIgnoresProductWordsInsideNames(t *testing.T) {
	for _, name := range []string{
		"Gulf Oil Middle East", "Acme Oils & Greases", "Supermarket Fresh Foods",
		"Marketplace Logistics", "Peninsula Lubricants Limited", "Acme Industrial Services",
		"Gulf Distributors",
	} {
		if marketDocumentTitle(name) {
			t.Errorf("marketDocumentTitle(%q) = true, want a company name", name)
		}
	}
	for _, title := range []string{
		"Lubricant Market Overview", "Top 10 Lubricant Manufacturers",
		"Lubricant Industry Trends 2026", "Linear Alkyl Benzene (LAB) Companies",
	} {
		if !marketDocumentTitle(title) {
			t.Errorf("marketDocumentTitle(%q) = false, want a document title", title)
		}
	}
}

// TestMarketCandidateDemotesReportAndHeadlinePages is the end-to-end leak test
// at discovery: neither page may present as a company-page, whatever its domain.
func TestMarketCandidateDemotesReportAndHeadlinePages(t *testing.T) {
	query := models.MarketLandscapeQuery{Activity: "lubricant blending"}
	report := marketCandidate(models.SearchResult{
		Title:   "Lubricants Market Size, Share & Trends Analysis Report 2025-2033",
		Snippet: "Lubricant blending market demand and forecast.",
		Link:    "https://analyticsvendor.example/lubricants-market",
	}, "manufacturer facility", query)
	if report == nil || report.EvidenceType != "discovery-lead" {
		t.Fatalf("report page = %+v, want a discovery lead", report)
	}
	if marketDocumentTitle(report.Name) || marketHeadlineTitle(report.Name) {
		t.Fatalf("report title leaked into the roster name: %q", report.Name)
	}
	headline := marketCandidate(models.SearchResult{
		Title:   "Gulf Oil commissions new lubricant blending plant in Fujairah",
		Snippet: "The plant will blend lubricants for the region.",
		Link:    "https://www.gulfoilltd.com/news/fujairah-plant",
	}, "manufacturer facility", query)
	if headline == nil || headline.EvidenceType != "discovery-lead" {
		t.Fatalf("news article on a company domain = %+v, want a discovery lead", headline)
	}
	// A real company page still promotes, and its name is the brand, not the
	// full title.
	company := marketCandidate(models.SearchResult{
		Title:   "Industrial Lubricants Manufacturer | Gulf Oil Middle East",
		Snippet: "Gulf Oil Middle East blends lubricants in Fujairah.",
		Link:    "https://www.gulfoilltd.com/about",
	}, "manufacturer facility", query)
	if company == nil || company.EvidenceType != "company-page" {
		t.Fatalf("company page = %+v, want a company-page", company)
	}
	if company.Name != "Gulf Oil Middle East" {
		t.Fatalf("company name = %q, want the brand segment", company.Name)
	}
}

// TestMarketVerificationRejectsDocumentsHeadlinesAndTopicalMentions pins the
// verification gate: a document or headline name is never corroborated, and a
// single industry word is not a mention.
func TestMarketVerificationRejectsDocumentsHeadlinesAndTopicalMentions(t *testing.T) {
	hit := models.SearchResult{
		Title:   "Gulf Oil Middle East Ltd - Company Profile",
		Link:    "https://directory.example.com/gulf-oil-middle-east",
		Snippet: "Lubricant blending manufacturer of greases.",
	}
	for _, bad := range []string{
		"Lubricants Market Size, Share & Trends Analysis Report",
		"Gulf Oil opens new blending plant",
	} {
		if _, ok := marketVerificationEvidence(hit, bad, "lubricant blending", "registry trade source"); ok {
			t.Errorf("%q must not verify as a company", bad)
		}
	}
	if _, ok := marketVerificationEvidence(hit, "Gulf Oil Middle East", "lubricant blending", "registry trade source"); !ok {
		t.Error("the directory page names Gulf Oil Middle East and should verify it")
	}

	topical := models.SearchResult{
		Title:   "Latest lubricant market news and trends",
		Link:    "https://directory.example.com/roundup",
		Snippet: "Global demand for lubricants rose in 2026.",
	}
	if mentionsMarketCompany(topical, "Global Lubricants") {
		t.Error("an industry word is not a company mention")
	}
}

// TestMarketMentionsCompanyUsesBrandAndDomain keeps real companies verifiable
// when the hit names them on their own site or by two distinctive tokens.
func TestMarketMentionsCompanyUsesBrandAndDomain(t *testing.T) {
	homepage := models.SearchResult{Title: "Home", Link: "https://www.gulfoilltd.com/"}
	if !mentionsMarketCompany(homepage, "Gulf Oil Middle East") {
		t.Error("a hit on the company's own domain is a mention")
	}
	twoTokens := models.SearchResult{Title: "Gulf Oil expands Fujairah capacity", Snippet: "Gulf Oil Middle East said capacity rose."}
	if !mentionsMarketCompany(twoTokens, "Gulf Oil Middle East") {
		t.Error("two distinctive tokens in the same hit are a mention")
	}
	otherCompany := models.SearchResult{Title: "Gulf Energy quarterly results", Snippet: "The energy firm reported profit."}
	if mentionsMarketCompany(otherCompany, "Gulf Oil Middle East") {
		t.Error("a different company sharing one word is not a mention")
	}
}

// TestMarketLeadNameRejectsEchoesOfTheQueryAndPage keeps extractor output clean.
func TestMarketLeadNameRejectsEchoesOfTheQueryAndPage(t *testing.T) {
	query := models.MarketLandscapeQuery{Activity: "oil blending", Countries: []string{"Singapore"}}
	if marketLeadNameAcceptable("oil blending", query) {
		t.Error("the activity is not a company name")
	}
	if marketLeadNameAcceptable("Singapore", query) {
		t.Error("a country is not a company name")
	}
	if !marketLeadNameAcceptable("Gulf Oil Middle East", query) {
		t.Error("a real company name must survive")
	}
	leadTitles := []string{"Sinopec opens new lubricant blending plant in Singapore"}
	if !marketNameEchoesLead("Sinopec opens new lubricant blending plant in Singapore", leadTitles) {
		t.Error("a verbatim title echo must be rejected")
	}
	if marketLeadName("Sinopec opens new lubricant blending plant in Singapore", "") != "" {
		t.Error("a headline must never become a roster name")
	}
	// A listicle must not survive by dropping its list prefix.
	if marketLeadName("Top 5 Linear Alkyl Benzene (LAB) Companies", "") != "" {
		t.Error("a ranked-companies heading must never become a roster name")
	}
}

// TestMarketOnlyOperatorTiersLendTheCompanyItsDomain: a directory page hosts
// many companies, so its domain cannot become any one company's domain.
func TestMarketOnlyOperatorTiersLendTheCompanyItsDomain(t *testing.T) {
	for _, tier := range []string{"official", "regulatory"} {
		if !marketOperatorOwnedTier(tier) {
			t.Errorf("tier %q is operator-owned", tier)
		}
	}
	for _, tier := range []string{"directory", "trade", "research", "consultancy", "aggregator", "news", "publisher", "institution"} {
		if marketOperatorOwnedTier(tier) {
			t.Errorf("tier %q publishes about companies and must not lend a domain", tier)
		}
	}
}

// TestMergeMarketCompanyFoldsLegalSuffixVariants keeps one company on one row.
func TestMergeMarketCompanyFoldsLegalSuffixVariants(t *testing.T) {
	companies := make(map[string]*models.MarketLandscapeCompany)
	mergeMarketCompany(companies, &models.MarketLandscapeCompany{EntityKey: "company:acme", Name: "Acme"})
	mergeMarketCompany(companies, &models.MarketLandscapeCompany{EntityKey: "company:acmeltd", Name: "Acme Ltd."})
	if len(companies) != 1 {
		t.Fatalf("companies = %d after legal-suffix merge, want 1", len(companies))
	}
	mergeMarketCompany(companies, &models.MarketLandscapeCompany{EntityKey: "company:punjab", Name: "Punjab Chemicals"})
	mergeMarketCompany(companies, &models.MarketLandscapeCompany{EntityKey: "company:punjabcrop", Name: "Punjab Chemicals & Crop Protection Limited"})
	if len(companies) != 3 {
		t.Fatalf("companies = %d, want distinct firms kept apart", len(companies))
	}
	// A product word is part of the identity: selling oils is not selling
	// lubricants.
	mergeMarketCompany(companies, &models.MarketLandscapeCompany{EntityKey: "company:acmeoils", Name: "Acme Oils"})
	mergeMarketCompany(companies, &models.MarketLandscapeCompany{EntityKey: "company:acmelubes", Name: "Acme Lubricants"})
	if len(companies) != 5 {
		t.Fatalf("companies = %d, want Acme Oils and Acme Lubricants kept apart", len(companies))
	}
}

// TestResearchVendorDomainsNeverTierAsOfficial closes the hole that let an
// unlisted report vendor's page titles tier as "official" and auto-promote.
func TestResearchVendorDomainsNeverTierAsOfficial(t *testing.T) {
	for _, domain := range []string{
		"www.verifiedmarketresearch.com", "www.skyquestt.com",
		"www.marketresearchintellect.com", "www.businessresearchinsights.com",
	} {
		if tier := marketDiscoveryTier(domain); isQualifyingMarketTier(tier) {
			t.Errorf("marketDiscoveryTier(%q) = %q, want a non-qualifying tier", domain, tier)
		}
	}
	if tier := marketDiscoveryTier("www.gulfoilltd.com"); !isQualifyingMarketTier(tier) {
		t.Errorf("an operator domain must stay qualifying, got %q", tier)
	}
}

// TestMarketLandscapeKeepsDocumentsAndHeadlinesOutOfTheRoster is the whole-run
// regression: a corpus dominated by a report page and a news article still
// yields a roster whose only entry is the operating company, named cleanly.
func TestMarketLandscapeKeepsDocumentsAndHeadlinesOutOfTheRoster(t *testing.T) {
	searcher := &demandSearcher{hits: map[string][]models.SearchResult{
		"lubricant blending": {
			{
				Title:   "Lubricants Market Size, Share & Trends Analysis Report 2025-2033",
				Link:    "https://analyticsvendor.example/lubricants-market",
				Snippet: "Lubricant blending market demand and forecast.",
			},
			{
				Title:   "Sinopec opens new lubricant blending plant in Singapore",
				Link:    "https://www.sinopec.com/news/singapore-plant",
				Snippet: "Sinopec said the plant blends lubricants for export.",
			},
			{
				Title:   "Gulf Oil Middle East Ltd - Company Profile",
				Link:    "https://directory.example.com/gulf-oil-middle-east",
				Snippet: "Lubricant blending manufacturer of greases.",
			},
			{
				Title:   "Industrial Lubricants Manufacturer | Gulf Oil Middle East",
				Link:    "https://www.gulfoilltd.com/about",
				Snippet: "Gulf Oil Middle East blends lubricants in Fujairah.",
			},
		},
	}}
	llm := &demandLLM{candidates: []models.MarketLandscapeLeadCandidate{
		{Name: "Lubricants Market Size, Share & Trends Analysis Report 2025-2033"},
		{Name: "Sinopec opens new lubricant blending plant in Singapore"},
		{Name: "oil blending in Singapore"},
		{Name: "Gulf Oil Middle East"},
	}}
	agent := NewMarketLandscapeAgent(demandChainTestEngine(llm, searcher))
	report, err := agent.Research(context.Background(), "market-names", models.MarketLandscapeQuery{
		Activity:  "lubricant blending",
		Countries: []string{"Singapore"},
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("results = %d (%+v), want the single operating company", len(report.Results), report.Results)
	}
	result := report.Results[0]
	if result.Name != "Gulf Oil Middle East" {
		t.Fatalf("result name = %q, want the company segment of the page title", result.Name)
	}
	for _, row := range report.Results {
		if marketDocumentTitle(row.Name) || marketHeadlineTitle(row.Name) {
			t.Errorf("document or headline in the roster: %q", row.Name)
		}
		if strings.Contains(row.Name, "Sinopec") {
			t.Errorf("news headline subject leaked as a roster name: %q", row.Name)
		}
	}
	if report.LeadsRejected == 0 {
		t.Error("expected the echoed report/headline/query candidates to be rejected")
	}
}

// TestMarketPageExcerptKeepsTheCompanyTable: a report page's roster table sits
// far past any sane prefix budget, so the excerpt must find it.
func TestMarketPageExcerptKeepsTheCompanyTable(t *testing.T) {
	var page strings.Builder
	for index := 0; index < 400; index++ {
		page.WriteString("Navigation link and marketing copy. ")
	}
	page.WriteString("Top 5 Linear Alkyl Benzene (LAB) Companies. Cepsa. Indorama Ventures Public Limited. ISU Chemical. Jintung Petrochemical Corp. Ltd. Sasol. ")
	for index := 0; index < 400; index++ {
		page.WriteString("Footer legal notice. ")
	}
	content := page.String()
	if strings.Contains(truncateMarketText(content, marketCompanyPageExcerpt), "Cepsa") {
		t.Fatal("test setup: the roster must sit beyond a plain company-page excerpt")
	}
	excerpt := marketPageExcerpt(content, marketRosterPageExcerpt)
	for _, name := range []string{"Cepsa", "Indorama Ventures Public Limited", "ISU Chemical"} {
		if !strings.Contains(excerpt, name) {
			t.Errorf("excerpt lost the company table: %q missing from %d characters", name, len(excerpt))
		}
	}
	// A short page is returned untouched.
	if got := marketPageExcerpt("Short page about Cepsa.", 500); got != "Short page about Cepsa." {
		t.Errorf("short page = %q, want it unchanged", got)
	}
}

// TestMarketLeadReadPriorityReadsRosterReportsFirst: under a fixed scrape
// budget, a report that ranks companies is read before a company homepage, and
// a social page is never read.
func TestMarketLeadReadPriorityReadsRosterReportsFirst(t *testing.T) {
	report := marketLeadReadPriority("research", "Top 5 Linear Alkyl Benzene (LAB) Companies")
	official := marketLeadReadPriority("official", "Gulf Oil Middle East | Industrial Lubricants")
	if report >= official {
		t.Fatalf("report priority %d must beat company page priority %d", report, official)
	}
	if !marketLeadReadable("research", "Linear Alkyl Benzene Market Report") {
		t.Error("a plain report page is still worth reading for the names it cites")
	}
	if marketLeadReadable("social", "Acme Lubricants on LinkedIn") {
		t.Error("social pages are login walls and must not consume the scrape budget")
	}
	if !marketRosterSourceTier("research") || marketRosterSourceTier("official") {
		t.Error("roster-source classification must separate publishers from operators")
	}
}

// marketPromptLLM records the extraction prompts it sees and returns a fixed
// candidate list, so a test can prove what the extractor was given.
type marketPromptLLM struct {
	mu          sync.Mutex
	userPrompts []string
	candidates  []models.MarketLandscapeLeadCandidate
	category    string
}

func (m *marketPromptLLM) GenerateStructured(_ context.Context, _, userPrompt string, target any) error {
	m.mu.Lock()
	m.userPrompts = append(m.userPrompts, userPrompt)
	candidates := append([]models.MarketLandscapeLeadCandidate(nil), m.candidates...)
	m.mu.Unlock()
	if typed, ok := target.(*[]models.MarketLandscapeLeadCandidate); ok {
		*typed = append(*typed, candidates...)
	}
	if typed, ok := target.(*models.MarketLandscapeInference); ok {
		typed.Category = m.category
	}
	return nil
}

func (m *marketPromptLLM) prompts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.userPrompts...)
}

// marketPageScraper serves a fixed page per URL.
type marketPageScraper struct {
	pages map[string]string
}

func (m *marketPageScraper) ScrapeURL(_ context.Context, targetURL string) (*models.ScrapeResult, error) {
	content, ok := m.pages[targetURL]
	if !ok {
		return nil, fmt.Errorf("no page registered for %s", targetURL)
	}
	return &models.ScrapeResult{URL: targetURL, Title: "Page", Content: content}, nil
}

// marketRouteSearcher answers by the first route whose fragment appears in the
// query, so the most specific route must be listed first. Map iteration order
// is deliberately avoided: a verification query also contains the activity.
type marketRouteSearcher struct {
	routes []marketSearchRoute
}

type marketSearchRoute struct {
	fragment string
	hits     []models.SearchResult
}

func (m *marketRouteSearcher) Search(_ context.Context, query string, _ int) ([]models.SearchResult, error) {
	lowered := strings.ToLower(query)
	for _, route := range m.routes {
		if strings.Contains(lowered, strings.ToLower(route.fragment)) {
			return route.hits, nil
		}
	}
	return nil, nil
}

// TestMarketLandscapeHarvestsCompaniesFromAReportPage is the regression for the
// Mordor-style run: the report page is not a company, but its company table is
// read and the names it lists are extracted and independently verified.
func TestMarketLandscapeHarvestsCompaniesFromAReportPage(t *testing.T) {
	const reportURL = "https://www.mordorintelligence.com/industry-reports/linear-alkyl-benzene-lab-market/companies"
	var reportPage strings.Builder
	for index := 0; index < 300; index++ {
		reportPage.WriteString("Mordor Intelligence navigation and report marketing copy. ")
	}
	reportPage.WriteString("Top 5 Linear Alkyl Benzene (LAB) Companies. Cepsa. Indorama Ventures Public Limited. ISU Chemical. Jintung Petrochemical Corp. Ltd. Sasol. ")
	for index := 0; index < 300; index++ {
		reportPage.WriteString("Footer legal notice and cookie policy. ")
	}
	scraper := &marketPageScraper{pages: map[string]string{
		reportURL: reportPage.String(),
		"https://directory.example.com/lab-companies": "Directory of linear alkyl benzene manufacturers and suppliers, by region.",
	}}
	searcher := &marketRouteSearcher{routes: []marketSearchRoute{
		{fragment: "cepsa", hits: []models.SearchResult{{
			Title: "Cepsa - Company Profile", Link: "https://directory.example.com/cepsa",
			Snippet: "Linear alkyl benzene producer and supplier.",
		}}},
		{fragment: "indorama", hits: []models.SearchResult{{
			Title: "Indorama Ventures Public Limited - Company Profile", Link: "https://directory.example.com/indorama",
			Snippet: "Linear alkyl benzene producer and supplier.",
		}}},
		{fragment: "isu chemical", hits: []models.SearchResult{{
			Title: "ISU Chemical Co Ltd - Company Profile", Link: "https://directory.example.com/isu",
			Snippet: "Linear alkyl benzene producer and supplier.",
		}}},
		{fragment: "linear alkyl benzene", hits: []models.SearchResult{
			{Title: "Top 5 Linear Alkyl Benzene (LAB) Companies", Link: reportURL, Snippet: "Leading LAB producers ranked."},
			{Title: "Linear Alkyl Benzene Companies Directory", Link: "https://directory.example.com/lab-companies", Snippet: "Directory of LAB manufacturers."},
		}},
	}}
	llm := &marketPromptLLM{candidates: []models.MarketLandscapeLeadCandidate{
		{Name: "Top 5 Linear Alkyl Benzene (LAB) Companies"},
		{Name: "Cepsa"},
		{Name: "Indorama Ventures Public Limited"},
		{Name: "ISU Chemical"},
	}}
	engine := NewEngine(llm, searcher, scraper, cache.NewMemoryCache(32), 8, 3, 10)
	agent := NewMarketLandscapeAgent(engine)
	report, err := agent.Research(context.Background(), "market-report-harvest", models.MarketLandscapeQuery{
		Activity: "linear alkyl benzene",
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if report.PagesEnriched == 0 {
		t.Fatal("the report page was discovered but never read")
	}
	// The extractor must have been given the report's page content, not just
	// its title: that is the only way its company table reaches the roster.
	joined := strings.Join(llm.prompts(), "\n")
	for _, need := range []string{"Cepsa", "Indorama Ventures Public Limited", "ISU Chemical"} {
		if !strings.Contains(joined, need) {
			t.Errorf("extraction prompt never carried %q from the report page", need)
		}
	}
	names := make(map[string]bool, len(report.Results))
	for _, row := range report.Results {
		names[row.Name] = true
		if marketDocumentTitle(row.Name) || marketHeadlineTitle(row.Name) {
			t.Errorf("the report page itself leaked into the roster: %q", row.Name)
		}
		// The publisher's domain is not the company's: Cepsa is not
		// directory.example.com.
		if strings.Contains(row.Domain, "directory.example.com") || strings.Contains(row.Domain, "mordorintelligence") {
			t.Errorf("%q adopted its publisher's domain %q", row.Name, row.Domain)
		}
	}
	for _, want := range []string{"Cepsa", "Indorama Ventures Public Limited", "ISU Chemical"} {
		if !names[want] {
			t.Errorf("roster = %v, want the independently verified company %q", names, want)
		}
	}
	if report.LeadsRejected == 0 {
		t.Error("the report title echoed back as a name must be rejected")
	}
}

// TestMarketCategoryFallbackBroadensWeakDiscovery: when the exact producer lanes are weak, the
// validated first-pass category supplies bounded fallback searches, and the extra budget is used
// for companies rather than key-person discovery.
func TestMarketCategoryFallbackBroadensWeakDiscovery(t *testing.T) {
	hits := make([]models.SearchResult, 0, 6)
	for index := 0; index < 6; index++ {
		hits = append(hits, models.SearchResult{
			Title:   "Acme Lubricants " + string(rune('A'+index)) + " Pvt Ltd",
			Link:    "https://acmelubricants" + string(rune('a'+index)) + ".com",
			Snippet: "Lubricant blending manufacturer and operator.",
		})
	}
	searcher := &marketRouteSearcher{routes: []marketSearchRoute{{
		fragment: "lubricants manufacturers", hits: hits,
	}}}
	llm := &marketPromptLLM{category: "lubricants"}
	engine := NewEngine(llm, searcher, &mockScraper{}, cache.NewMemoryCache(16), 8, 3, 10)
	report, err := NewMarketLandscapeAgent(engine).Research(context.Background(), "category-fallback", models.MarketLandscapeQuery{Activity: "lubricant blending"}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if report.ProductCategory != "lubricants" || report.CategorySource != "model" {
		t.Errorf("category = %q/%q, want lubricants/model", report.ProductCategory, report.CategorySource)
	}
	if report.FallbackQueries == 0 {
		t.Fatal("weak discovery did not run the category fallback")
	}
	if report.Coverage == nil || !report.Coverage.Pass || report.Coverage.Companies < marketCoverageMinimum {
		t.Errorf("coverage = %+v, want at least %d companies", report.Coverage, marketCoverageMinimum)
	}
}
