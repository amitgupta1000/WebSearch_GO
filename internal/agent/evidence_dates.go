package agent

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// evidence_dates.go derives the date a source itself proves about its content.
//
// Without this a person row is undated: a 2020 board-composition PDF and last
// month's appointment notice look identical to the synthesiser, which is how an
// eight-year-old director ends up in a "current" org chart. Every signal here is
// already in hand — the page's Last-Modified header, the JSON-LD the extractor
// kept, the URL itself — so dating costs no extra requests.
//
// Signals are ranked by what they say about the *content*: a LinkedIn post id, a
// JSON-LD datePublished/dateModified and a dated URL all describe the document,
// while Last-Modified only describes the server's copy, so it is a fallback.
// Among content signals the newest wins.

// dateSignal names the origin of an extracted date. They are stable strings so
// the report and the artifacts can show why a row is considered fresh.
const (
	dateSignalLinkedIn = "linkedin-activity"
	dateSignalJSONLD   = "json-ld"
	dateSignalURL      = "url"
	dateSignalModified = "last-modified"
	dateSignalSearch   = "search-result"
	dateSignalContent  = "content"
)

var (
	// linkedInActivityPattern matches the activity id LinkedIn puts in post and
	// feed URLs (…-activity-7094625874183958528-KOqH).
	linkedInActivityPattern = regexp.MustCompile(`(?i)activity-(\d{6,})`)
	// jsonLDDatePattern matches the publication and update fields of JSON-LD.
	jsonLDDatePattern = regexp.MustCompile(`(?i)"(?:datePublished|dateModified|uploadDate)"\s*:\s*"([^"]{4,40})"`)
	// isoURLDatePattern matches 2025-03-06 and 2025_03_06 in a URL or file name.
	isoURLDatePattern = regexp.MustCompile(`(20\d{2}|19[9]\d)[-_/](0[1-9]|1[0-2])[-_/](0[1-9]|[12]\d|3[01])`)
	// dayMonthYearPattern matches 1july2020, 06-march-2025 and 1 july 2020. The
	// boundaries are "not alphanumeric" rather than \b because file names glue
	// dates to words with underscores (…_October24_2025_RR…).
	dayMonthYearPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(\d{1,2})[\s_-]*(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*[\s_-]*(19[9]\d|20\d{2})(?:[^0-9]|$)`)
	// monthDayYearPattern matches October 24, 2025 and March 06_ 2025 — the shape
	// credit-rating and filing file names use.
	monthDayYearPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*[\s_-]+(\d{1,2})(?:st|nd|rd|th)?,?[\s_-]+(19[9]\d|20\d{2})(?:[^0-9]|$)`)
	// monthYearPattern matches july2020, nov-dec_2023 and march-2024.
	monthYearPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*[\s_-]*(19[9]\d|20\d{2})(?:[^0-9]|$)`)
	// bareYearPattern is the weakest URL fallback: a standalone year. The 1995
	// floor keeps registry identifiers out (the CIN L67120WB1986PLC041487 carries
	// 1986), and a bare year is only ever a gross hint.
	bareYearPattern = regexp.MustCompile(`(?:^|[^0-9])(19[9]\d|20\d{2})(?:[^0-9]|$)`)
	// contentYearPattern catches "as of 2024", "last updated 2025-01" and
	// copyright lines. It is deliberately the last resort.
	contentYearPattern = regexp.MustCompile(`(?i)(?:as of|last updated|updated on|copyright|©|\bupdated)\D{0,24}(20\d{2})`)
	// relativeDatePattern matches "3 days ago", which is how a search provider
	// usually reports the age of a news result.
	relativeDatePattern = regexp.MustCompile(`(?i)(\d+)\s*(minute|hour|day|week|month|year)s?\s+ago`)
)

// monthNumber maps a three-letter month prefix to its month.
func monthNumber(name string) time.Month {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	if len(trimmed) < 3 {
		return time.January
	}
	switch trimmed[:3] {
	case "jan":
		return time.January
	case "feb":
		return time.February
	case "mar":
		return time.March
	case "apr":
		return time.April
	case "may":
		return time.May
	case "jun":
		return time.June
	case "jul":
		return time.July
	case "aug":
		return time.August
	case "sep":
		return time.September
	case "oct":
		return time.October
	case "nov":
		return time.November
	default:
		return time.December
	}
}

// plausibleDate rejects dates that cannot describe a live web source: anything
// from before the web looked like this, and anything in the future. It is what
// stops a truncated or unrelated number from becoming a confident date.
func plausibleDate(when time.Time) bool {
	if when.IsZero() || when.Year() < 1995 {
		return false
	}
	return !when.After(time.Now().AddDate(0, 0, 1))
}

// urlUnescape decodes percent escapes without pulling in a URL parser for a
// string that may not be a valid URL at all.
func urlUnescape(raw string) string {
	if !strings.Contains(raw, "%") {
		return raw
	}
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}
	return raw
}

// linkedInActivityDate decodes the timestamp LinkedIn encodes in an activity id:
// the id's upper bits are milliseconds since the epoch, so a profile or post URL
// that would otherwise be an undated citation becomes a dated one.
func linkedInActivityDate(raw string) (time.Time, bool) {
	match := linkedInActivityPattern.FindStringSubmatch(raw)
	if match == nil {
		return time.Time{}, false
	}
	id, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	when := time.UnixMilli(int64(id >> 22)).UTC()
	if !plausibleDate(when) {
		return time.Time{}, false
	}
	return when, true
}

// parseFlexibleDate accepts the shapes dates arrive in: RFC 3339, a calendar
// date, and a month-and-year pair.
func parseFlexibleDate(raw string) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
		"2006-01", "January 2, 2006", "Jan 2, 2006", "2 January 2006", "Jan 2006",
		"January 2006", "2006/01/02", "01/02/2006",
	} {
		if when, err := time.Parse(layout, trimmed); err == nil && plausibleDate(when) {
			return when.UTC(), true
		}
	}
	return time.Time{}, false
}

// jsonLDDate reads publication and update dates out of the JSON-LD payloads the
// extractor preserved. The newest field wins: on a page that has both,
// dateModified supersedes datePublished.
func jsonLDDate(payloads []string) (time.Time, bool) {
	var newest time.Time
	for _, payload := range payloads {
		for _, match := range jsonLDDatePattern.FindAllStringSubmatch(payload, -1) {
			when, ok := parseFlexibleDate(match[1])
			if ok && when.After(newest) {
				newest = when
			}
		}
	}
	if newest.IsZero() {
		return time.Time{}, false
	}
	return newest, true
}

// lastModifiedDate reads the HTTP validator the fetch path already recorded. The
// layout list covers what servers actually send: the GMT form net/http documents
// first, then RFC 1123 with its zone abbreviation ("… UTC", "… EST"), RFC 850 and
// ANSIC. http.ParseTime alone rejects the UTC form, which some servers use.
func lastModifiedDate(raw string) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{http.TimeFormat, time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC} {
		when, err := time.Parse(layout, trimmed)
		if err != nil || !plausibleDate(when) {
			continue
		}
		return when.UTC(), true
	}
	return time.Time{}, false
}

// urlDate extracts the newest date written into a URL or file name, which is how
// filings and dated articles announce their own vintage:
// Composition-of-Board-of-Directors-1july2020.pdf, …_March 06_ 2025_RR_…,
// /coatingstech/nov-dec_2023/.
func urlDate(raw string) (time.Time, bool) {
	decoded := urlUnescape(raw)
	var newest time.Time
	consider := func(when time.Time) {
		if plausibleDate(when) && when.After(newest) {
			newest = when
		}
	}

	for _, match := range isoURLDatePattern.FindAllStringSubmatch(decoded, -1) {
		year, _ := strconv.Atoi(match[1])
		month, _ := strconv.Atoi(match[2])
		day, _ := strconv.Atoi(match[3])
		consider(time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC))
	}
	for _, match := range dayMonthYearPattern.FindAllStringSubmatch(decoded, -1) {
		day, _ := strconv.Atoi(match[1])
		year, _ := strconv.Atoi(match[3])
		consider(time.Date(year, monthNumber(match[2]), day, 0, 0, 0, 0, time.UTC))
	}
	for _, match := range monthDayYearPattern.FindAllStringSubmatch(decoded, -1) {
		day, _ := strconv.Atoi(match[2])
		year, _ := strconv.Atoi(match[3])
		consider(time.Date(year, monthNumber(match[1]), day, 0, 0, 0, 0, time.UTC))
	}
	for _, match := range monthYearPattern.FindAllStringSubmatch(decoded, -1) {
		year, _ := strconv.Atoi(match[2])
		consider(time.Date(year, monthNumber(match[1]), 1, 0, 0, 0, 0, time.UTC))
	}
	if newest.IsZero() {
		for _, match := range bareYearPattern.FindAllStringSubmatch(decoded, -1) {
			year, _ := strconv.Atoi(match[1])
			consider(time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC))
		}
	}
	if newest.IsZero() {
		return time.Time{}, false
	}
	return newest, true
}

// contentYearDate is the weakest signal: prose that says when it was written.
func contentYearDate(content string) (time.Time, bool) {
	match := contentYearPattern.FindStringSubmatch(content)
	if match == nil {
		return time.Time{}, false
	}
	year, err := strconv.Atoi(match[1])
	if err != nil {
		return time.Time{}, false
	}
	when := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	if !plausibleDate(when) {
		return time.Time{}, false
	}
	return when, true
}

// evidenceDate returns the newest date an evidence item proves about its content
// and the signal that produced it. Content signals beat Last-Modified, because a
// re-uploaded 2020 filing is still a 2020 filing.
func evidenceDate(item models.Evidence) (time.Time, string, bool) {
	var newest time.Time
	signal := ""
	consider := func(when time.Time, name string, ok bool) {
		if ok && when.After(newest) {
			newest, signal = when, name
		}
	}

	when, ok := linkedInActivityDate(item.URL)
	consider(when, dateSignalLinkedIn, ok)
	if !ok {
		when, ok = linkedInActivityDate(item.Title)
		consider(when, dateSignalLinkedIn, ok)
	}
	when, ok = jsonLDDate(item.JSONLD)
	consider(when, dateSignalJSONLD, ok)
	when, ok = urlDate(item.URL)
	consider(when, dateSignalURL, ok)
	when, ok = contentYearDate(item.Content)
	consider(when, dateSignalContent, ok)

	if newest.IsZero() {
		when, ok = lastModifiedDate(item.LastModified)
		consider(when, dateSignalModified, ok)
	}
	if newest.IsZero() {
		return time.Time{}, "", false
	}
	return newest, signal, true
}

// annotateEvidenceDates dates every collected source in place. It runs after
// scraping and before synthesis, so the model sees when each source was written
// and can prefer the recent one — and so the validation pass has dates to judge
// without any extra request.
func annotateEvidenceDates(evidence []models.Evidence) {
	for index := range evidence {
		when, signal, ok := evidenceDate(evidence[index])
		if !ok {
			continue
		}
		if evidence[index].AsOf.IsZero() || when.After(evidence[index].AsOf) {
			evidence[index].AsOf = when
			evidence[index].DateSignal = signal
		}
	}
}

// searchResultDate dates a search result: the provider's own date field when it
// has one (Serper returns both "3 days ago" and "Jan 5, 2025" shapes), the URL's
// embedded date otherwise.
func searchResultDate(result models.SearchResult) (time.Time, bool) {
	if when, ok := serperDate(result.Date); ok {
		return when, true
	}
	return urlDate(result.Link)
}

// serperDate parses the relative and absolute date strings a search provider
// returns.
func serperDate(raw string) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, false
	}
	if relative := relativeDatePattern.FindStringSubmatch(trimmed); relative != nil {
		count, err := strconv.Atoi(relative[1])
		if err != nil {
			return time.Time{}, false
		}
		now := time.Now().UTC()
		switch strings.ToLower(relative[2])[:3] {
		case "min":
			return now.Add(-time.Duration(count) * time.Minute), true
		case "hou":
			return now.Add(-time.Duration(count) * time.Hour), true
		case "day":
			return now.AddDate(0, 0, -count), true
		case "wee":
			return now.AddDate(0, 0, -7*count), true
		case "mon":
			return now.AddDate(0, -count, 0), true
		default:
			return now.AddDate(-count, 0, 0), true
		}
	}
	return parseFlexibleDate(trimmed)
}
