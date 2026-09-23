package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// validate.go is the freshness pass over a finished chart.
//
// Two things are true of every roster row, and neither was recorded before:
// when it was last true, and whether anyone independent says so. A search-result
// snippet from an undated aggregator page is not the same claim as the company's
// own leadership page, but in the artifact they looked identical — which is how
// a 2021 appointment notice became a "current" CFO.
//
// The pass has two halves:
//
//  1. Local assessment (no requests). Every row is matched to the evidence that
//     supports it, the newest date among those sources is taken, and the row is
//     labelled verified-recent, dated, stale, undated or conflicting.
//  2. Bounded reverse lookups. Only the rows the local pass could not resolve —
//     undated, stale or conflicting — get one search each ("<name>"
//     "<company>"), snippet-only, capped by VALIDATION_LOOKUPS_PER_RUN and
//     gated on remaining budget. A dated recent mention promotes the row; nothing
//     is ever added to the roster from a lookup, because inventing people is not
//     validation.
const (
	// minValidationBudget is the smallest remaining run budget worth spending on
	// a reverse lookup: one search plus its bookkeeping.
	minValidationBudget = 15 * time.Second
	// minRecentDomains is how many independent domains corroborate a row that no
	// single company-owned source covers.
	minRecentDomains = 2
)

// rosterRow points at one person in the chart, wherever the model placed them.
type rosterRow struct {
	employee *models.Employee
	// context is "leadership" or the department name, used in messages.
	context string
}

// evidenceView is one collected source with the lowercase haystack the mention
// scan needs, built once per pass rather than per row.
type evidenceView struct {
	item     models.Evidence
	haystack string
}

// rowSource is one source that supports a person row.
type rowSource struct {
	URL    string
	Domain string
	AsOf   time.Time
	Signal string
	Type   string
}

// collectRosterRows flattens leadership and every department member into one
// list of addressable rows.
func collectRosterRows(chart *models.OrgChart) []rosterRow {
	rows := make([]rosterRow, 0, len(chart.Leadership))
	for index := range chart.Leadership {
		rows = append(rows, rosterRow{employee: &chart.Leadership[index], context: "leadership"})
	}
	for department := range chart.Departments {
		for member := range chart.Departments[department].Members {
			rows = append(rows, rosterRow{
				employee: &chart.Departments[department].Members[member],
				context:  chart.Departments[department].Name,
			})
		}
	}
	return rows
}

// honorificPrefixes are dropped before comparing names, so "Sh. Hemant Jain" and
// "Hemant Jain" are recognised as one person instead of two.
var honorificPrefixes = map[string]bool{
	"sh": true, "shri": true, "smt": true, "sri": true, "mr": true, "mrs": true,
	"ms": true, "dr": true, "prof": true, "capt": true, "col": true, "gen": true,
	"lt": true, "maj": true,
}

// normalizePersonName lowercases a name, drops punctuation and honorifics, and
// collapses whitespace so two spellings of one person compare equal.
func normalizePersonName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, name)
	tokens := strings.Fields(cleaned)
	kept := tokens[:0:0]
	for _, token := range tokens {
		token = strings.Trim(token, ".")
		if token == "" || honorificPrefixes[token] {
			continue
		}
		kept = append(kept, token)
	}
	return strings.Join(kept, " ")
}

// mentions reports whether an evidence item names a person. It requires every
// token of the normalised name to appear, and no corroboration at all for a
// single-token name — a first name alone matches too much to count as a second
// source.
func (view evidenceView) mentions(name string) bool {
	tokens := strings.Fields(name)
	if len(tokens) < 2 {
		return false
	}
	for _, token := range tokens {
		if !strings.Contains(view.haystack, token) {
			return false
		}
	}
	return true
}

// buildEvidenceViews prepares the mention scan once for the whole pass.
func buildEvidenceViews(evidence []models.Evidence) []evidenceView {
	views := make([]evidenceView, 0, len(evidence))
	for _, item := range evidence {
		views = append(views, evidenceView{
			item:     item,
			haystack: strings.ToLower(item.Title + "\n" + item.Content),
		})
	}
	return views
}

// gatherRowSources collects the sources that support one person: the row's own
// recorded citation first, then any evidence that names them.
func gatherRowSources(employee models.Employee, views []evidenceView) []rowSource {
	sources := make([]rowSource, 0, 4)
	seen := make(map[string]bool, 4)
	add := func(item models.Evidence) {
		key := normalizeURL(item.URL)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		sources = append(sources, rowSource{
			URL:    item.URL,
			Domain: sourceDomain(item.URL),
			AsOf:   item.AsOf,
			Signal: item.DateSignal,
			Type:   item.SourceType,
		})
	}

	// The row's own citation is the primary claim: the synthesis prompt requires
	// every person to name where they came from.
	cited := normalizeURL(employee.PublicSource)
	if cited != "" {
		for _, view := range views {
			if normalizeURL(view.item.URL) == cited {
				add(view.item)
			}
		}
	}
	if name := normalizePersonName(employee.Name); name != "" {
		for _, view := range views {
			if view.mentions(name) {
				add(view.item)
			}
		}
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].AsOf.After(sources[j].AsOf) })
	return sources
}

// freshnessVerdict labels a row from the dates and the independence of its
// sources. A dated source from the company itself, or from two unrelated
// domains, is the strongest thing this pass can establish: the claim is current
// and someone stands behind it.
func freshnessVerdict(newest time.Time, domains int, companyOwned bool, now time.Time, staleAfter, recentWithin time.Duration) string {
	switch {
	case !newest.IsZero() && newest.Before(now.Add(-staleAfter)):
		return models.VerificationStale
	case !newest.IsZero() && !newest.Before(now.Add(-recentWithin)) && (companyOwned || domains >= minRecentDomains):
		return models.VerificationRecent
	case !newest.IsZero():
		return models.VerificationDated
	default:
		return models.VerificationUndated
	}
}

// normalizeDomain reduces a domain or URL to its registrable host, so
// "kljresources.in" and "https://www.kljresources.in/x" compare equal.
func normalizeDomain(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	return sourceDomain(trimmed)
}

// companyOwnedSource reports whether a source is the researched company's own
// material: its domain, or a source the ranker already classified as official.
func companyOwnedSource(source rowSource, chartDomain string) bool {
	if source.Type == "official" || source.Type == "official_report" {
		return true
	}
	sourceHost := normalizeDomain(source.Domain)
	companyHost := normalizeDomain(chartDomain)
	if sourceHost == "" || companyHost == "" {
		return false
	}
	return sourceHost == companyHost
}

// distinctDomains counts the independent publishers behind a row's sources.
func distinctDomains(sources []rowSource) int {
	seen := make(map[string]bool, len(sources))
	for _, source := range sources {
		if source.Domain == "" {
			continue
		}
		seen[source.Domain] = true
	}
	return len(seen)
}

// newestSourceDate returns the newest date across a row's sources.
func newestSourceDate(sources []rowSource) time.Time {
	var newest time.Time
	for _, source := range sources {
		if source.AsOf.After(newest) {
			newest = source.AsOf
		}
	}
	return newest
}

// titleAbbreviations are expanded before comparing titles, so "CFO" and "Chief
// Financial Officer" are recognised as the same claim and their holders as
// conflicting.
var titleAbbreviations = map[string]string{
	"cfo":  "chief financial officer",
	"ceo":  "chief executive officer",
	"coo":  "chief operating officer",
	"cto":  "chief technology officer",
	"cio":  "chief information officer",
	"chro": "chief human resources officer",
	"md":   "managing director",
	"vp":   "vice president",
	"svp":  "senior vice president",
	"evp":  "executive vice president",
	"gm":   "general manager",
	"dgm":  "deputy general manager",
	"agm":  "assistant general manager",
	"hr":   "human resources",
}

// normalizeTitle reduces a title to a comparable claim.
func normalizeTitle(title string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, title)
	tokens := strings.Fields(cleaned)
	expanded := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if full, ok := titleAbbreviations[token]; ok {
			expanded = append(expanded, full)
			continue
		}
		expanded = append(expanded, token)
	}
	return strings.Join(expanded, " ")
}

// leadershipTitleConflicts finds leadership titles claimed by more than one
// person — the two-CFO chart that QC scored 1.00 — and returns the message for
// each affected row plus the sorted list of conflicts.
func leadershipTitleConflicts(chart *models.OrgChart) (map[int]string, []string) {
	byTitle := make(map[string][]int)
	for index := range chart.Leadership {
		title := normalizeTitle(chart.Leadership[index].Title)
		if title == "" {
			continue
		}
		byTitle[title] = append(byTitle[title], index)
	}

	rowConflict := make(map[int]string)
	conflicts := make([]string, 0, len(byTitle))
	for _, indexes := range byTitle {
		if len(indexes) < 2 {
			continue
		}
		names := make([]string, 0, len(indexes))
		for _, index := range indexes {
			names = append(names, strings.TrimSpace(chart.Leadership[index].Name))
		}
		sort.Strings(names)
		message := fmt.Sprintf("%d people claim the title %q: %s",
			len(names), strings.TrimSpace(chart.Leadership[indexes[0]].Title), strings.Join(names, ", "))
		conflicts = append(conflicts, message)
		for _, index := range indexes {
			rowConflict[index] = message
		}
	}
	sort.Strings(conflicts)
	return rowConflict, conflicts
}

// asOfLayout is how a date is written into a chart row and the artifacts.
const asOfLayout = "2006-01-02"

// validationLookupTimeout bounds one reverse lookup. It is short on purpose: a
// lookup is the last thing a run does, and a slow search must not push a
// finished chart past its budget.
const validationLookupTimeout = 12 * time.Second

// lookupCandidate is one person awaiting a reverse lookup, with every row that
// mentions them.
type lookupCandidate struct {
	name string
	rows []rosterRow
	rank int
}

// candidateRank orders lookups by how misleading the row is: a title two people
// claim first, then a stale date, then a row with no date at all.
func candidateRank(verdict string) int {
	switch verdict {
	case models.VerificationConflicting:
		return 0
	case models.VerificationStale:
		return 1
	default:
		return 2
	}
}

// unresolvedRows selects the people the local pass could not date or corroborate,
// deduplicated by name so one lookup covers every row that mentions them.
func unresolvedRows(rows []rosterRow) []lookupCandidate {
	byName := make(map[string]*lookupCandidate, 4)
	order := make([]string, 0, 4)
	for _, row := range rows {
		verdict := row.employee.Verification
		if verdict != models.VerificationUndated && verdict != models.VerificationStale && verdict != models.VerificationConflicting {
			continue
		}
		key := normalizePersonName(row.employee.Name)
		// A single-token name is not a searchable claim, and a lookup on one
		// would return unrelated people.
		if len(strings.Fields(key)) < 2 {
			continue
		}
		if existing, ok := byName[key]; ok {
			existing.rows = append(existing.rows, row)
			if rank := candidateRank(verdict); rank < existing.rank {
				existing.rank = rank
			}
			continue
		}
		byName[key] = &lookupCandidate{name: strings.TrimSpace(row.employee.Name), rows: []rosterRow{row}, rank: candidateRank(verdict)}
		order = append(order, key)
	}

	candidates := make([]lookupCandidate, 0, len(order))
	for _, key := range order {
		candidates = append(candidates, *byName[key])
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].rank < candidates[j].rank })
	return candidates
}

// hasBudgetForValidation reports whether enough of the run budget remains for a
// reverse lookup. An exhausted budget simply means the rows keep the verdict the
// local pass gave them.
func hasBudgetForValidation(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return time.Until(deadline) >= minValidationBudget
}

// validateChart runs the freshness pass over a finished chart and returns the
// report attached to it. It never fails a run: a problem is recorded, not raised.
func (ca *CompanyAgent) validateChart(ctx context.Context, sessionID string, brief models.CompanyBrief, chart *models.OrgChart, state *models.CompanyResearchContext, eventCh chan<- models.AgentEvent) *models.ValidationReport {
	report := &models.ValidationReport{}
	if chart == nil {
		return report
	}
	var evidence []models.Evidence
	if state != nil {
		evidence = state.Evidence
	}
	if len(evidence) > 0 {
		annotateEvidenceDates(evidence)
	}
	views := buildEvidenceViews(evidence)
	now := time.Now()
	staleAfter := ca.engine.ValidationStaleAfter()
	recentWithin := ca.engine.ValidationRecentWithin()

	rows := collectRosterRows(chart)
	report.RowsChecked = len(rows)
	// buckets keeps each row's *freshness* verdict while the row label may be
	// overridden by a title conflict, so the counters stay about dates.
	buckets := make([]string, len(rows))
	assessments := make([]rowAssessment, len(rows))
	position := make(map[*models.Employee]int, len(rows))
	for index, row := range rows {
		position[row.employee] = index
		sources := gatherRowSources(*row.employee, views)
		newest := newestSourceDate(sources)
		owned := false
		for _, source := range sources {
			if companyOwnedSource(source, chart.Domain) {
				owned = true
				break
			}
		}
		domains := distinctDomains(sources)
		assessments[index] = rowAssessment{domains: domains, owned: owned}
		verdict := freshnessVerdict(newest, domains, owned, now, staleAfter, recentWithin)
		buckets[index] = verdict
		row.employee.Verification = verdict
		row.employee.AsOf = formatAsOf(newest)
		row.employee.VerifiedBy = topSources(sources, 2)
	}

	// A title two people claim is the most misleading thing a chart can contain,
	// and it is visible without any request.
	titleConflicts, conflicts := leadershipTitleConflicts(chart)
	report.Conflicts = conflicts
	for index, message := range titleConflicts {
		chart.Leadership[index].Verification = models.VerificationConflicting
		chart.Leadership[index].VerificationNote = message
	}

	report.Lookups, report.Skipped = ca.runReverseLookups(ctx, brief, rows, assessments, buckets, position, state, now, staleAfter, recentWithin)
	report.LookupsUsed = len(report.Lookups)
	countVerdicts(report, buckets)

	Emit(eventCh, models.AgentEvent{
		SessionID: sessionID,
		Type:      models.EventToolResult,
		Step:      32,
		ToolName:  "evidence_validation",
		Content: fmt.Sprintf("Validated %d roster rows: %d recent, %d dated, %d stale, %d undated, %d title conflict(s); %d reverse lookup(s)",
			report.RowsChecked, report.RowsRecent, report.RowsDated, report.RowsStale, report.RowsUndated, len(report.Conflicts), report.LookupsUsed),
	})
	return report
}

// countVerdicts tallies the freshness buckets into the report.
func countVerdicts(report *models.ValidationReport, buckets []string) {
	for _, verdict := range buckets {
		switch verdict {
		case models.VerificationRecent:
			report.RowsRecent++
		case models.VerificationDated:
			report.RowsDated++
		case models.VerificationStale:
			report.RowsStale++
		default:
			report.RowsUndated++
		}
	}
}

// rowAssessment is what the local pass learned about one row, kept so a lookup
// can re-derive the verdict without rescanning the evidence.
type rowAssessment struct {
	domains int
	owned   bool
}

// runReverseLookups spends the lookup budget on the rows the local pass could not
// resolve, most misleading first. It returns the lookups it ran and, when it
// stopped early, why.
func (ca *CompanyAgent) runReverseLookups(ctx context.Context, brief models.CompanyBrief, rows []rosterRow, assessments []rowAssessment, buckets []string, position map[*models.Employee]int, state *models.CompanyResearchContext, now time.Time, staleAfter, recentWithin time.Duration) ([]models.ValidationLookup, string) {
	if !ca.engine.ValidationEnabled() {
		return nil, "validation disabled"
	}
	limit := ca.engine.ValidationLookups()
	if limit <= 0 {
		return nil, "VALIDATION_LOOKUPS_PER_RUN is 0"
	}
	candidates := unresolvedRows(rows)
	if len(candidates) == 0 {
		return nil, "every roster row already carries a dated or corroborated source"
	}

	lookups := make([]models.ValidationLookup, 0, limit)
	skipped := ""
	for _, candidate := range candidates {
		if len(lookups) >= limit {
			skipped = fmt.Sprintf("lookup cap reached (%d)", limit)
			break
		}
		if !hasBudgetForValidation(ctx) {
			skipped = "no run budget left for reverse lookups"
			break
		}

		lookup, results := ca.reverseLookup(ctx, brief, candidate.name)
		if lookup.Status == "promoted" || lookup.Status == "refreshed" {
			changed, verdict := applyLookup(&lookup, candidate, assessments, buckets, position, now, staleAfter, recentWithin)
			switch {
			case !changed:
				lookup.Status = "unchanged"
				lookup.Detail = "the dated mention is not newer than the row"
			case verdict == models.VerificationRecent:
				lookup.Status = "promoted"
			default:
				lookup.Status = "refreshed"
				lookup.Detail = fmt.Sprintf("newest mention leaves the row %s", verdict)
			}
		}
		lookups = append(lookups, lookup)

		// Lookup snippets are kept as evidence so a follow-up question can use
		// them, but they never enter the roster: validation does not introduce
		// people.
		if state != nil && len(results) > 0 {
			state.Evidence = appendUniqueEvidence(state.Evidence, lookupEvidence(results))
		}
	}
	return lookups, skipped
}

// applyLookup writes a promoted or refreshed date onto every row that mentions
// the person. It reports whether anything improved and the resulting freshness
// verdict.
func applyLookup(lookup *models.ValidationLookup, candidate lookupCandidate, assessments []rowAssessment, buckets []string, position map[*models.Employee]int, now time.Time, staleAfter, recentWithin time.Duration) (bool, string) {
	when, err := time.Parse(asOfLayout, lookup.AsOf)
	if err != nil {
		return false, ""
	}
	changed := false
	verdict := ""
	for _, row := range candidate.rows {
		index, ok := position[row.employee]
		if !ok {
			continue
		}
		current, _ := time.Parse(asOfLayout, row.employee.AsOf)
		if !when.After(current) {
			continue
		}
		assessment := assessments[index]
		verdict = freshnessVerdict(when, assessment.domains+1, assessment.owned, now, staleAfter, recentWithin)
		buckets[index] = verdict
		// A title conflict outranks freshness: the row keeps saying so.
		if row.employee.Verification != models.VerificationConflicting {
			row.employee.Verification = verdict
		}
		row.employee.AsOf = formatAsOf(when)
		row.employee.VerifiedBy = append([]string{lookup.Source}, row.employee.VerifiedBy...)
		row.employee.VerificationNote = appendNote(row.employee.VerificationNote, "reverse-validated "+lookup.AsOf)
		changed = true
	}
	return changed, verdict
}

// appendNote adds a note without losing an earlier one (a conflict note and a
// validation note can both apply to the same row).
func appendNote(existing, addition string) string {
	addition = strings.TrimSpace(addition)
	if addition == "" {
		return existing
	}
	if strings.TrimSpace(existing) == "" {
		return addition
	}
	return existing + "; " + addition
}

// formatAsOf writes a date into a row, or an empty string when there is none.
func formatAsOf(when time.Time) string {
	if when.IsZero() {
		return ""
	}
	return when.UTC().Format(asOfLayout)
}

// topSources returns the newest source URLs behind a row, most recent first.
func topSources(sources []rowSource, limit int) []string {
	if limit <= 0 {
		return nil
	}
	urls := make([]string, 0, limit)
	for _, source := range sources {
		if strings.TrimSpace(source.URL) == "" {
			continue
		}
		urls = append(urls, source.URL)
		if len(urls) >= limit {
			break
		}
	}
	return urls
}

// reverseLookup runs one bounded reverse search for a person: "<name>"
// "<company>", snippet-only — the results are never fetched, because the point is
// to find a *date*, and a LinkedIn result URL carries its own date.
func (ca *CompanyAgent) reverseLookup(ctx context.Context, brief models.CompanyBrief, name string) (models.ValidationLookup, []models.SearchResult) {
	query := fmt.Sprintf("\"%s\" \"%s\"", name, strings.TrimSpace(brief.CompanyName))
	lookup := models.ValidationLookup{Person: name, Query: query}

	searchCtx, cancel := context.WithTimeout(ctx, validationLookupTimeout)
	defer cancel()
	results, err := ca.engine.SearchFresh(searchCtx, query, ca.engine.MaxSearchResults())
	if err != nil {
		lookup.Status = "error"
		lookup.Detail = err.Error()
		return lookup, nil
	}
	if len(results) == 0 {
		lookup.Status = "no-results"
		return lookup, nil
	}

	newest, source := newestDatedResult(results)
	if newest.IsZero() {
		lookup.Status = "unchanged"
		lookup.Detail = "no dated mention in the results"
		return lookup, results
	}
	lookup.AsOf = formatAsOf(newest)
	lookup.Source = source
	if !newest.Before(time.Now().Add(-ca.engine.ValidationRecentWithin())) {
		lookup.Status = "promoted"
		lookup.Detail = "dated mention inside the recent window"
		return lookup, results
	}
	lookup.Status = "refreshed"
	return lookup, results
}

// newestDatedResult returns the newest dated result and the URL it came from.
func newestDatedResult(results []models.SearchResult) (time.Time, string) {
	var newest time.Time
	source := ""
	for _, result := range results {
		when, ok := resultContextDate(result)
		if !ok || !when.After(newest) {
			continue
		}
		newest, source = when, result.Link
	}
	return newest, source
}

// resultContextDate dates one search result from the provider's date field, the
// LinkedIn activity id in its URL, or the URL itself.
func resultContextDate(result models.SearchResult) (time.Time, bool) {
	var newest time.Time
	if when, ok := searchResultDate(result); ok {
		newest = when
	}
	if when, ok := linkedInActivityDate(result.Link); ok && when.After(newest) {
		newest = when
	}
	if when, ok := urlDate(result.Link); ok && when.After(newest) {
		newest = when
	}
	if newest.IsZero() {
		return time.Time{}, false
	}
	return newest, true
}

// lookupEvidence turns lookup results into snippet evidence, dated where the
// provider or the URL says so. Three results per lookup is plenty for a follow-up
// question and keeps the retained context small.
func lookupEvidence(results []models.SearchResult) []models.Evidence {
	const perLookup = 3
	evidence := make([]models.Evidence, 0, perLookup)
	for _, result := range results {
		if len(evidence) >= perLookup {
			break
		}
		if strings.TrimSpace(result.Snippet) == "" {
			continue
		}
		entry := models.Evidence{
			URL:          result.Link,
			Title:        result.Title,
			Content:      result.Snippet,
			Score:        0.4,
			EvidenceType: "validation_search",
			SourceType:   "validation",
			QualityScore: 0.3,
		}
		if when, ok := resultContextDate(result); ok {
			entry.AsOf = when
			entry.DateSignal = dateSignalSearch
		}
		evidence = append(evidence, entry)
	}
	return evidence
}
