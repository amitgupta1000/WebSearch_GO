package agent

import (
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestEvidenceDatesCoverTheSignalsThatAppearInRealSources pins the dating rules
// against URLs taken from live runs: a LinkedIn post that made a 2021
// appointment look current, a 2020 board PDF, and the undated aggregator pages
// the roster used to rest on.
func TestEvidenceDatesCoverTheSignalsThatAppearInRealSources(t *testing.T) {
	cases := []struct {
		name   string
		item   models.Evidence
		want   string
		signal string
	}{
		{
			name:   "linkedin post id decodes to its post date",
			item:   models.Evidence{URL: "https://www.linkedin.com/posts/vinod-trivedi-2835221a_we-are-happy-activity-6771005891878625280-KOqH"},
			want:   "2021-02-26",
			signal: dateSignalLinkedIn,
		},
		{
			name:   "board composition pdf names its own date",
			item:   models.Evidence{URL: "http://www.kljresources.in/investor_pdf/Composition-of-Board-of-Directors-1july2020.pdf"},
			want:   "2020-07-01",
			signal: dateSignalURL,
		},
		{
			name:   "month day year inside a file name",
			item:   models.Evidence{URL: "https://www.crisilratings.com/mnt/winshare/KLJResourcesLimited_October%2024_%202025_RR_377790.html"},
			want:   "2025-10-24",
			signal: dateSignalURL,
		},
		{
			name: "a month range is dated at its end",
			// nov-dec_2023 covers November and December; the newest date the URL
			// proves is December, and newest wins.
			item:   models.Evidence{URL: "https://www.coatingstech-digital.org/coatingstech/nov-dec_2023/MobilePagedArticle.action"},
			want:   "2023-12-01",
			signal: dateSignalURL,
		},
		{
			name: "json-ld publication date",
			item: models.Evidence{
				URL:    "https://example.com/about",
				JSONLD: []string{`{"@type":"Organization","dateModified":"2025-03-06T09:00:00Z"}`},
			},
			want:   "2025-03-06",
			signal: dateSignalJSONLD,
		},
		{
			name: "last-modified is the fallback when nothing else dates the page",
			item: models.Evidence{
				URL:          "https://example.com/leadership",
				LastModified: "Wed, 01 Oct 2025 10:00:00 GMT",
			},
			want:   "2025-10-01",
			signal: dateSignalModified,
		},
		{
			name: "a named time zone in last-modified still parses",
			// http.ParseTime alone rejects this form, and servers do send it.
			item: models.Evidence{
				URL:          "https://example.com/team",
				LastModified: "Wed, 01 Oct 2025 10:00:00 UTC",
			},
			want:   "2025-10-01",
			signal: dateSignalModified,
		},
		{
			name: "copyright line is the weakest signal",
			item: models.Evidence{
				URL:     "https://example.com/team",
				Content: "Our team. Copyright 2024 Example Pvt Ltd.",
			},
			want:   "2024-01-01",
			signal: dateSignalContent,
		},
		{
			name:   "an undated aggregator listing stays undated",
			item:   models.Evidence{URL: "https://rocketreach.co/klj-resources-ltd-management_b6a4ceb0c87f174d", Content: "Contact details"},
			want:   "",
			signal: "",
		},
		{
			name:   "a registry identifier's year is not a publication date",
			item:   models.Evidence{URL: "https://www.zaubacorp.com/KLJ-RESOURCES-LTD-L67120WB1986PLC041487"},
			want:   "",
			signal: "",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			when, signal, ok := evidenceDate(testCase.item)
			if testCase.want == "" {
				if ok {
					t.Fatalf("expected no date, got %s via %s", when.Format(asOfLayout), signal)
				}
				return
			}
			if !ok {
				t.Fatalf("expected %s via %s, got no date", testCase.want, testCase.signal)
			}
			if got := when.Format(asOfLayout); got != testCase.want {
				t.Fatalf("expected %s, got %s", testCase.want, got)
			}
			if signal != testCase.signal {
				t.Fatalf("expected signal %s, got %s", testCase.signal, signal)
			}
		})
	}
}

// TestContentDateBeatsLastModified pins the ordering rule: a re-uploaded 2020
// filing is still a 2020 filing, so a fresh Last-Modified header must not
// launder it into a current source.
func TestContentDateBeatsLastModified(t *testing.T) {
	item := models.Evidence{
		URL:          "https://www.kljresources.in/investor_pdf/Composition-of-Board-of-Directors-1july2020.pdf",
		LastModified: time.Now().UTC().Format(time.RFC1123),
	}
	when, signal, ok := evidenceDate(item)
	if !ok {
		t.Fatal("expected a date")
	}
	if got := when.Format(asOfLayout); got != "2020-07-01" {
		t.Fatalf("expected the document date 2020-07-01, got %s", got)
	}
	if signal != dateSignalURL {
		t.Fatalf("expected the url signal, got %s", signal)
	}
}

// TestAnnotateEvidenceDatesKeepsTheNewest covers the in-place pass the run uses
// before synthesis.
func TestAnnotateEvidenceDatesKeepsTheNewest(t *testing.T) {
	evidence := []models.Evidence{
		{URL: "https://example.com/a", JSONLD: []string{`{"datePublished":"2019-05-05"}`}},
		{URL: "https://www.linkedin.com/posts/x_y-activity-7094625874183958528-KOqH"},
	}
	annotateEvidenceDates(evidence)
	if got := evidence[0].AsOf.Format(asOfLayout); got != "2019-05-05" {
		t.Fatalf("expected 2019-05-05, got %s", got)
	}
	if got := evidence[1].AsOf.Format(asOfLayout); got != "2023-08-08" {
		t.Fatalf("expected 2023-08-08, got %s", got)
	}
}

// TestSerperDateParsesProviderShapes covers the relative strings a search
// provider returns alongside news results.
func TestSerperDateParsesProviderShapes(t *testing.T) {
	if when, ok := serperDate("3 days ago"); !ok || time.Since(when) < 60*time.Hour {
		t.Fatalf("expected a date about three days ago, got %v (ok=%t)", when, ok)
	}
	if when, ok := serperDate("Jan 5, 2025"); !ok || when.Format(asOfLayout) != "2025-01-05" {
		t.Fatalf("expected 2025-01-05, got %v (ok=%t)", when, ok)
	}
	if _, ok := serperDate(""); ok {
		t.Fatal("an empty date must not parse")
	}
}
