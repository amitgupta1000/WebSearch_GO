package tools

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Fixtures mirror the shapes measured live in October 2026; the live
// integration test (RESEARCH_INTEGRATION=1) re-checks each source against
// reality, which is what catches markup drift.

// The CCI thread fixture mirrors the measured thread-card shape and is wrapped
// in a page shell past the client's wall-size floor, the way a real thread
// page (tens of KB) is.
var cciThreadFixture = `
<html><head><title>Applicability of RCM in import of services - Forum</title>
<style>` + strings.Repeat("/* shell */ .x{margin:0} ", 180) + `</style></head>
<body>
<div class="thread-card row g-0 align-items-center">
	<div class="thread-info col">
		<h3 class="thread-title"><a href='applicability-of-rcm-import-services-616199.asp'>Applicability of RCM in import of services</a></h3>
		<div class="thread-meta"><span class="thread-author">Started by <a href="/profile.asp?member_id=1">Deepika Garg</a></span></div>
	</div>
</div>
<div class="thread-post"><div class="post-content">Our company imported services from a foreign vendor. Is RCM applicable and at what rate?</div></div>
<div class="thread-post"><div class="post-content">Yes, import of service under section 2(1)(l) attracts RCM. Refer Notification 10/2017.</div></div>
</body></html>`

func TestParseCCIThread(t *testing.T) {
	thread := parseCCIThread("https://www.caclubindia.com/forum/applicability-of-rcm-import-services-616199.asp", cciThreadFixture)
	if thread.Title != "Applicability of RCM in import of services" {
		t.Errorf("Title = %q", thread.Title)
	}
	if !strings.Contains(thread.Text, "Notification 10/2017") {
		t.Errorf("Text should carry the discussion posts: %q", thread.Text)
	}
	if thread.URL == "" {
		t.Error("URL must always be set")
	}
}

func TestCCIClientThreadResolvesRelative(t *testing.T) {
	var got string
	client := &CCIClient{
		baseURL: "https://www.caclubindia.com",
		fetchHTML: func(_ context.Context, target string) (string, error) {
			got = target
			return cciThreadFixture, nil
		},
		limiter: newHostLimiter(0, 0, 0),
	}
	thread, err := client.Thread(context.Background(), "applicability-of-rcm-import-services-616199.asp")
	if err != nil {
		t.Fatalf("Thread failed: %v", err)
	}
	if got != "https://www.caclubindia.com/applicability-of-rcm-import-services-616199.asp" {
		t.Errorf("fetched URL = %q, want the relative thread resolved against the base", got)
	}
	if thread.Title == "" {
		t.Error("thread title should parse from the fixture")
	}
}

func TestCCIClientRefusesEmptyBody(t *testing.T) {
	client := &CCIClient{
		baseURL: "https://www.caclubindia.com",
		fetchHTML: func(_ context.Context, _ string) (string, error) {
			return "<html><body>x</body></html>", nil
		},
		limiter: newHostLimiter(0, 0, 0),
	}
	if _, err := client.Thread(context.Background(), "some-thread-1.asp"); err == nil {
		t.Fatal("a wall/empty page must be refused by name")
	}
}

const nfraOrdersFixture = `
<html><body>
<nav><ul class="nav">
<li><a href="https://cdnbbsr.s3waas.gov.in/uploads/2024/07/menu-report.pdf">Report on Valuation Methodology of AT-1 Bonds</a></li>
<li><a href="/document/about-nfra/">About NFRA</a></li>
</ul></nav>
<div class="listing">
<article><h3><a href="https://cdnbbsr.s3waas.gov.in/uploads/2025/01/nfra-order-001.pdf">Order under Section 132(4) in the matter of M/s XYZ Associates</a></h3><span>15 January, 2025</span></article>
<article><h3><a href="/document/order-in-the-matter-of-abc-llp/">Order in the matter of ABC LLP</a></h3></article>
<article><h3><a href="https://cdnbbsr.s3waas.gov.in/uploads/2025/02/nfra-order-002.pdf">Order under Section 132(4) in the matter of M/s PQR and Co</a></h3></article>
</div>
</body></html>`

func TestParseNFRAOrders(t *testing.T) {
	docs := parseNFRAOrders("https://nfra.gov.in", nfraOrdersFixture)
	if len(docs) != 3 {
		t.Fatalf("docs = %d, want 3 (nav PDF and nav page excluded)", len(docs))
	}
	first := docs[0]
	if first.Title != "Order under Section 132(4) in the matter of M/s XYZ Associates" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.PDFURL == "" || !strings.Contains(first.PDFURL, "nfra-order-001.pdf") {
		t.Errorf("PDFURL = %q", first.PDFURL)
	}
	if first.Source != "nfra" || first.Category != "orders" {
		t.Errorf("source/category = %q/%q", first.Source, first.Category)
	}
	second := docs[1]
	if second.URL == "" || second.PDFURL != "" {
		t.Errorf("a /document/ page entry carries URL, not PDFURL: %+v", second)
	}
}

func TestNFRAWalkStopsOnEmptyPage(t *testing.T) {
	calls := 0
	client := &NFRAClient{
		baseURL: "https://nfra.gov.in",
		fetchHTML: func(_ context.Context, _ string) (string, error) {
			calls++
			return nfraOrdersFixture, nil
		},
		limiter: newHostLimiter(0, 0, 0),
		page:    3,
	}
	docs, err := client.Orders(context.Background())
	if err != nil {
		t.Fatalf("Orders failed: %v", err)
	}
	if calls != 2 {
		t.Errorf("archive pages fetched = %d, want 2 (page 2 fetched, found nothing new, walk stopped)", calls)
	}
	if len(docs) != 3 {
		t.Errorf("docs = %d, want 3 (dedupe across pages)", len(docs))
	}
}

const sebiSitemapFixture = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc>https://www.sebi.gov.in/enforcement/orders/sep-2025/adjudication-order-in-the-matter-of-sar-televenture-limited_96425.html</loc></url>
<url><loc>https://www.sebi.gov.in/enforcement/orders/jun-2024/order-in-respect-of-raghunandan-capital-private-ltd_84808.html</loc></url>
<url><loc>https://www.sebi.gov.in/enforcement/recovery-proceedings/jul-2024/prohibitory-order-no-1012-2024_84809.html</loc></url>
<url><loc>https://www.sebi.gov.in/legal/circulars/mar-2022/revision-in-orders-per-second-limit_56977.html</loc></url>
</urlset>`

func TestParseSEBISitemap(t *testing.T) {
	docs := parseSEBISitemap(sebiSitemapFixture)
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2 (orders family only)", len(docs))
	}
	first := docs[0]
	if first.Title != "Adjudication Order In The Matter Of Sar Televenture Limited" {
		t.Errorf("Title = %q, want the slug-derived title without the id", first.Title)
	}
	if first.Date != "sep 2025" {
		t.Errorf("Date = %q, want the URL's month-year", first.Date)
	}
}

const icaiCategoryFixture = `
<html><body>
<nav><a href="/category/overview">Overview</a><a href="/category/about-icai">About ICAI</a></nav>
<div class="content">
<article><h3><a href="https://www.icai.org/post/disciplinary-committee-order-2024-11-15.pdf">Disciplinary Committee Order dated 15th November 2024 in the matter of a member</a></h3></article>
<article><h3><a href="/post.html?post_id=12345">Board of Discipline Order in the matter of professional misconduct</a></h3></article>
</div>
</body></html>`

func TestParseICAICategory(t *testing.T) {
	docs := parseICAICategory("https://www.icai.org", "disciplinary", icaiCategoryFixture)
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2 (nav categories excluded)", len(docs))
	}
	first := docs[0]
	if first.Title != "Disciplinary Committee Order dated 15th November 2024 in the matter of a member" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.PDFURL == "" || first.URL != "" {
		t.Errorf("a PDF entry carries PDFURL only: %+v", first)
	}
	second := docs[1]
	if second.URL != "https://www.icai.org/post.html?post_id=12345" {
		t.Errorf("post URL = %q", second.URL)
	}
}

// --- Live integration (RESEARCH_INTEGRATION=1) ------------------------------

func researchScraper(t *testing.T) *Scraper {
	t.Helper()
	scraper := NewScraperWithBrowser(nil, 30*time.Second, true)
	t.Cleanup(func() { scraper.Close() })
	return scraper
}

func TestCaclubindiaLiveThread(t *testing.T) {
	if os.Getenv("RESEARCH_INTEGRATION") != "1" {
		t.Skip("RESEARCH_INTEGRATION=1 not set; skipping live Caclubindia thread")
	}
	client := NewCCIClient(researchScraper(t))
	thread, err := client.Thread(context.Background(),
		"applicability-of-fcra-to-article-processing-fees-apc-received-from-foreign-author-in-journal-616198.asp")
	if err != nil {
		t.Fatalf("live thread fetch failed: %v", err)
	}
	if thread.Title == "" {
		t.Error("live thread carries no title")
	}
	t.Logf("live thread: %q, %d chars of discussion", thread.Title, len(thread.Text))
}

func TestNFRALiveOrders(t *testing.T) {
	if os.Getenv("RESEARCH_INTEGRATION") != "1" {
		t.Skip("RESEARCH_INTEGRATION=1 not set; skipping live NFRA orders")
	}
	client := NewNFRAClient(researchScraper(t), 2)
	docs, err := client.Orders(context.Background())
	if err != nil {
		t.Fatalf("live orders fetch failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("live NFRA archive returned no entries")
	}
	t.Logf("live NFRA: %d entries, first = %q", len(docs), docs[0].Title)
}

func TestSEBILiveOrderIndex(t *testing.T) {
	if os.Getenv("RESEARCH_INTEGRATION") != "1" {
		t.Skip("RESEARCH_INTEGRATION=1 not set; skipping live SEBI order index")
	}
	client := NewSEBIClient(researchScraper(t))
	docs, err := client.OrderIndex(context.Background())
	if err != nil {
		t.Fatalf("live sitemap fetch failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("live SEBI sitemap yielded no order entries")
	}
	for _, doc := range docs {
		if !strings.Contains(doc.URL, "/enforcement/orders/") {
			t.Errorf("entry outside the orders family: %q", doc.URL)
		}
	}
	t.Logf("live SEBI: %d order entries, first = %q (%s)", len(docs), docs[0].Title, docs[0].Date)
}

func TestICALiveCategory(t *testing.T) {
	if os.Getenv("RESEARCH_INTEGRATION") != "1" {
		t.Skip("RESEARCH_INTEGRATION=1 not set; skipping live ICAI category")
	}
	client := NewICAIClient(researchScraper(t))
	docs, err := client.Category(context.Background(), "disciplinary")
	if err != nil {
		t.Fatalf("live category fetch failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("live ICAI category returned no entries")
	}
	t.Logf("live ICAI: %d entries, first = %q", len(docs), docs[0].Title)
}
