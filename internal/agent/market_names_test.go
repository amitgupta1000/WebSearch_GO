package agent

import (
	"context"
	"strings"
	"testing"

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
	} {
		if marketDocumentTitle(name) {
			t.Errorf("marketDocumentTitle(%q) = true, want a company name", name)
		}
	}
	for _, title := range []string{
		"Lubricant Market Overview", "Top 10 Lubricant Manufacturers",
		"Lubricant Industry Trends 2026",
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
