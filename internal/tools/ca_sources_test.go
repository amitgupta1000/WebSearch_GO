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
