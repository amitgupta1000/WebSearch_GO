package agent

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

const maxPublicEmailLookups = 8

var publicEmailPattern = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)

var freeEmailHosts = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "yahoo.com": true, "outlook.com": true,
	"hotmail.com": true, "icloud.com": true, "proton.me": true, "protonmail.com": true,
}

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
	// EntityKey is how this source ties to the researched company: domain,
	// registration-id, alias or name; empty when it never names the company.
	EntityKey string
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
func gatherRowSources(employee models.Employee, views []evidenceView, matcher *entityMatcher) []rowSource {
	sources := make([]rowSource, 0, 4)
	seen := make(map[string]bool, 4)
	add := func(item models.Evidence) {
		key := normalizeURL(item.URL)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		match := models.EntityMatch{}
		if matcher != nil {
			match = matcher.match(item.URL, item.Title, item.Content)
			// A name or alias match only supports *this person* when the company is
			// named near them: a passing mention elsewhere on the page — a peer-company
			// table in an IPO story, a party list in a judgment — is how unrelated pages
			// entered the source list.
			if (match.Key == models.EntityKeyName || match.Key == models.EntityKeyAlias) &&
				!mentionsCompanyNear(item.Content, employee.Name, matcher) {
				match = models.EntityMatch{}
			}
		}
		sources = append(sources, rowSource{
			URL:       item.URL,
			Domain:    sourceDomain(item.URL),
			AsOf:      item.AsOf,
			Signal:    item.DateSignal,
			Type:      item.SourceType,
			EntityKey: match.Key,
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

// unresolvedRows selects the people one reverse lookup could help, and ranks them.
//
// It is deliberately broad — the point of the lookup is to *establish* facts the run
// could not collect, not merely to date rows it already has. Anything that is not already
// verified-recent is worth one snippet-only search: a row no source ties to the company
// (the lookup can rescue it), a title two people claim, a match that rests on the name
// alone, and every stale, undated or merely-dated row.
func unresolvedRows(rows []rosterRow, gateActive bool) []lookupCandidate {
	byName := make(map[string]*lookupCandidate, 4)
	order := make([]string, 0, 4)
	for _, row := range rows {
		rank, worth := lookupWorthiness(*row.employee, gateActive)
		if !worth {
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
			if rank < existing.rank {
				existing.rank = rank
			}
			continue
		}
		byName[key] = &lookupCandidate{name: strings.TrimSpace(row.employee.Name), rows: []rosterRow{row}, rank: rank}
		order = append(order, key)
	}

	candidates := make([]lookupCandidate, 0, len(order))
	for _, key := range order {
		candidates = append(candidates, *byName[key])
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].rank < candidates[j].rank })
	return candidates
}

// lookupWorthiness ranks how much a lookup would help a row, and reports whether it is
// worth one at all. A row already verified as current by a company-owned or corroborated
// recent source is left alone.
func lookupWorthiness(person models.Employee, gateActive bool) (int, bool) {
	if gateActive && strings.TrimSpace(person.EntityKey) == "" {
		// No source ties this person to the company: the lookup is the only thing that
		// can rescue them, so it goes first.
		return 0, true
	}
	if person.Verification == models.VerificationConflicting {
		return 1, true
	}
	if gateActive && person.EntityKey == models.EntityKeyName {
		// Tied by name alone, which a same-named company also satisfies: confirming the
		// association is the highest-value use of a lookup after a rescue.
		return 2, true
	}
	switch person.Verification {
	case models.VerificationStale:
		return 3, true
	case models.VerificationUndated:
		return 4, true
	case models.VerificationDated:
		return 5, true
	}
	return 0, false
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
func (ca *CompanyAgent) validateChart(ctx context.Context, sessionID string, brief models.CompanyBrief, chart *models.OrgChart, state *models.CompanyResearchContext, eventCh chan<- models.AgentEvent, gate *models.EntityGate) *models.ValidationReport {
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
	matcher := ca.entityMatcherFor(brief)
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
		sources := gatherRowSources(*row.employee, views, matcher)
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
		if matcher != nil {
			// How this person is tied to the company: domain beats registration id
			// beats alias beats a bare name, and an empty key is the case the gate
			// acts on.
			row.employee.EntityKey = strongestEntityKey(sources)
		}
	}

	// A title two people claim is the most misleading thing a chart can contain,
	// and it is visible without any request.
	titleConflicts, conflicts := leadershipTitleConflicts(chart)
	report.Conflicts = conflicts
	for index, message := range titleConflicts {
		chart.Leadership[index].Verification = models.VerificationConflicting
		chart.Leadership[index].VerificationNote = message
	}

	report.Lookups, report.Skipped = ca.runReverseLookups(ctx, brief, rows, assessments, buckets, position, state, now, staleAfter, recentWithin, matcher, gate)
	report.LookupsUsed = len(report.Lookups)
	ca.enrichPublicEmails(ctx, brief, rows, matcher, ca.engine.ValidationLookups()-report.LookupsUsed)
	report.ExcludedRows = applyCurrentStatusVerdicts(ca.engine.EntityGateRemoves(), chart, rows, report.Lookups)
	countVerdicts(report, buckets)

	// The entity pass runs last: it needs the labels above, and its removals must not
	// disturb the row indexes the earlier passes used.
	if matcher != nil && gate != nil {
		applyEntityVerdicts(ca.engine.EntityGateRemoves(), chart, rows, gate, matcher)
	}

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

// enrichPublicEmails performs bounded, explicit searches for work addresses that
// are already published by a source. It never constructs or infers an address.
func (ca *CompanyAgent) enrichPublicEmails(ctx context.Context, brief models.CompanyBrief, rows []rosterRow, matcher *entityMatcher, budget int) {
	if !ca.engine.ValidationEnabled() || len(rows) == 0 || budget <= 0 {
		return
	}
	lookups := 0
	for _, row := range rows {
		if lookups >= maxPublicEmailLookups || lookups >= budget || !hasBudgetForValidation(ctx) || strings.TrimSpace(row.employee.PublicEmail) != "" {
			break
		}
		name := strings.TrimSpace(row.employee.Name)
		if name == "" {
			continue
		}
		query := fmt.Sprintf(`"%s" "%s" email`, name, strings.TrimSpace(brief.CompanyName))
		searchCtx, cancel := context.WithTimeout(ctx, validationLookupTimeout)
		results, err := ca.engine.SearchFresh(searchCtx, query, ca.engine.MaxSearchResults())
		cancel()
		lookups++
		if err != nil {
			continue
		}
		for _, result := range results {
			if matcher != nil {
				match := matcher.match(result.Link, result.Title, result.Snippet)
				if !match.Matched {
					continue
				}
			}
			for _, email := range publicEmailPattern.FindAllString(result.Title+" "+result.Snippet, -1) {
				host := strings.ToLower(strings.TrimSpace(strings.SplitN(email, "@", 2)[1]))
				if freeEmailHosts[host] {
					continue
				}
				row.employee.PublicEmail = strings.ToLower(email)
				row.employee.EmailSource = result.Link
				break
			}
			if row.employee.PublicEmail != "" {
				break
			}
		}
	}
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
func (ca *CompanyAgent) runReverseLookups(ctx context.Context, brief models.CompanyBrief, rows []rosterRow, assessments []rowAssessment, buckets []string, position map[*models.Employee]int, state *models.CompanyResearchContext, now time.Time, staleAfter, recentWithin time.Duration, matcher *entityMatcher, gate *models.EntityGate) ([]models.ValidationLookup, string) {
	if !ca.engine.ValidationEnabled() {
		return nil, "validation disabled"
	}
	limit := ca.engine.ValidationLookups()
	if limit <= 0 {
		return nil, "VALIDATION_LOOKUPS_PER_RUN is 0"
	}
	candidates := unresolvedRows(rows, matcher != nil)
	if len(candidates) == 0 {
		return nil, "every roster row is already current and tied to the company by a stronger key than its name"
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
		lookup, results := ca.reverseLookup(ctx, brief, candidate.name, matcher)
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
		// Rescue: the lookup is what ties a person to the company when no collected
		// source could, and what upgrades a match resting on the name alone. It follows
		// the date handling so a rescued row keeps any date it just gained.
		if lookup.MatchedCompany && matcher != nil {
			if rescued := applyEntityRescue(&lookup, candidate, position, gate); rescued > 0 {
				if lookup.Status != "promoted" && lookup.Status != "refreshed" {
					lookup.Status = "rescued"
				}
				lookup.Detail = appendNote(lookup.Detail, "company association confirmed by reverse lookup")
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
	// A truncated pass has to say so: "0 reverse lookups" and "12 lookups, 3 candidates left
	// unchecked" are very different statements about how well a roster was checked, and the
	// aggressive policy is only worth anything if it actually ran.
	if gate != nil {
		gate.LookupsDeferred = len(candidates) - len(lookups)
	}
	if skipped != "" && gate != nil && gate.LookupsDeferred > 0 {
		skipped = fmt.Sprintf("%s; %d candidate(s) left unchecked", skipped, gate.LookupsDeferred)
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
func (ca *CompanyAgent) reverseLookup(ctx context.Context, brief models.CompanyBrief, name string, matcher *entityMatcher) (models.ValidationLookup, []models.SearchResult) {
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
	if hasHistoricalPersonStatus(results, matcher, brief.CompanyName, name) {
		lookup.Status = "historical"
		lookup.CurrentEmployerConflict = true
		lookup.CurrentStatus = "historical"
		lookup.Detail = "the person is identified as former, retired or deceased"
		return lookup, results
	}
	if hasCurrentEmployerConflict(results, matcher, brief.CompanyName, name) {
		lookup.Status = "conflicting-employer"
		lookup.CurrentEmployerConflict = true
		lookup.CurrentStatus = "historical"
		lookup.Detail = "a current-role result names another employer"
		return lookup, results
	}

	// Association first, and it is the more important fact: a result that names both the
	// person and the company ties them together, which is what rescues a row no collected
	// source supports and what upgrades a match resting on the name alone. The strongest
	// matching key wins, so a company-owned result outranks a name-only mention.
	associationSource := ""
	strength := 0
	if matcher != nil {
		for _, result := range results {
			match := matcher.match(result.Link, result.Title, result.Snippet)
			if !match.Matched || entityKeyStrength(match.Key) <= strength {
				continue
			}
			strength = entityKeyStrength(match.Key)
			lookup.MatchedCompany = true
			lookup.MatchedKey = match.Key
			associationSource = result.Link
		}
	}

	newest, source := newestDatedResult(results)
	if newest.IsZero() {
		lookup.Status = "unchanged"
		if hasCurrentCompanyEmployment(results, matcher, brief.CompanyName, name) {
			lookup.CurrentStatus = "current"
		} else {
			lookup.CurrentStatus = "unknown"
		}
		lookup.Detail = "no dated mention in the results"
		if associationSource != "" {
			lookup.Source = associationSource
		}
		return lookup, results
	}

	lookup.AsOf = formatAsOf(newest)
	lookup.Source = source
	if !newest.Before(time.Now().Add(-ca.engine.ValidationRecentWithin())) {
		lookup.Status = "promoted"
		lookup.CurrentStatus = "current"
		lookup.Detail = "dated mention inside the recent window"
		return lookup, results
	}
	lookup.Status = "refreshed"
	if hasCurrentCompanyEmployment(results, matcher, brief.CompanyName, name) {
		lookup.CurrentStatus = "current"
	} else {
		lookup.CurrentStatus = "unknown"
	}
	return lookup, results
}

// resultNamesCompany reports whether one search result names the researched
// company: through the entity matcher when the gate is active — so spelling
// variants ("Goyum Screwpress" as typed, "Goyum Screw Press" as registered, the
// bare brand "GOYUM") all count — and through a plain substring of the typed
// name when the gate is off, which is the historical behaviour.
func resultNamesCompany(matcher *entityMatcher, result models.SearchResult, company string) bool {
	if matcher != nil {
		return matcher.match(result.Link, result.Title, result.Snippet).Matched
	}
	target := strings.ToLower(strings.Join(strings.Fields(company), " "))
	text := strings.ToLower(strings.Join(strings.Fields(result.Title+" "+result.Snippet), " "))
	return strings.Contains(text, target)
}

func hasCurrentEmployerConflict(results []models.SearchResult, matcher *entityMatcher, company, person string) bool {
	name := strings.ToLower(strings.Join(strings.Fields(person), " "))
	if name == "" || (matcher == nil && strings.TrimSpace(company) == "") {
		return false
	}
	for _, result := range results {
		title := strings.ToLower(strings.Join(strings.Fields(result.Title), " "))
		text := title + " " + strings.ToLower(strings.Join(strings.Fields(result.Snippet), " "))
		if !strings.Contains(text, name) {
			continue
		}
		if hasFormerEmploymentSignal(text) && hasCurrentEmploymentSignal(text) {
			return true
		}
		if !resultNamesCompany(matcher, result, company) && hasRoleEmploymentShape(title) &&
			(hasCurrentEmploymentSignal(text) || isProfessionalProfileResult(result)) {
			return true
		}
	}
	return false
}

func hasHistoricalPersonStatus(results []models.SearchResult, matcher *entityMatcher, company, person string) bool {
	name := strings.ToLower(strings.Join(strings.Fields(person), " "))
	for _, result := range results {
		text := strings.ToLower(strings.Join(strings.Fields(result.Title+" "+result.Snippet), " "))
		if !strings.Contains(text, name) || !hasExplicitHistoricalStatus(text) {
			continue
		}
		if resultNamesCompany(matcher, result, company) || hasFormerEmploymentSignal(text) {
			return true
		}
	}
	return false
}

func hasCurrentCompanyEmployment(results []models.SearchResult, matcher *entityMatcher, company, person string) bool {
	name := strings.ToLower(strings.Join(strings.Fields(person), " "))
	target := strings.ToLower(strings.Join(strings.Fields(company), " "))
	for _, result := range results {
		title := strings.ToLower(strings.Join(strings.Fields(result.Title), " "))
		text := title + " " + strings.ToLower(strings.Join(strings.Fields(result.Snippet), " "))
		if !strings.Contains(text, name) || hasFormerEmploymentSignal(text) {
			continue
		}
		if !resultNamesCompany(matcher, result, company) {
			continue
		}
		if hasCurrentEmploymentSignal(text) || strings.Contains(text, " at "+target) ||
			hasRoleEmploymentShape(title) {
			return true
		}
	}
	return false
}

func hasExplicitHistoricalStatus(text string) bool {
	for _, marker := range []string{"retired", "retirement", "deceased", "passed away", "died ", "obituary", "in memoriam"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func isProfessionalProfileResult(result models.SearchResult) bool {
	link := strings.ToLower(result.Link)
	title := strings.ToLower(result.Title)
	return strings.Contains(link, "linkedin.com/in/") ||
		strings.Contains(link, "crunchbase.com/person/") ||
		strings.Contains(title, "linkedin")
}

func hasFormerEmploymentSignal(text string) bool {
	for _, marker := range []string{"previously", "former", "formerly", "past employer", "ex-", "left ", "before joining"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func hasCurrentEmploymentSignal(text string) bool {
	for _, marker := range []string{"currently", "current role", "now ", "present", "joined ", "managing partner at", "partner at", "director at", "officer at"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func hasRoleEmploymentShape(title string) bool {
	if !strings.Contains(title, " - ") && !strings.Contains(title, " | ") {
		return false
	}
	for _, role := range []string{"chief", "ceo", "cfo", "coo", "cto", "president", "partner", "director", "manager", "head", "officer", "vp"} {
		if strings.Contains(title, role) {
			return true
		}
	}
	return false
}

func applyCurrentStatusVerdicts(remove bool, chart *models.OrgChart, rows []rosterRow, lookups []models.ValidationLookup) []models.OffEntityRow {
	statuses := make(map[string]string)
	for _, lookup := range lookups {
		status := lookup.CurrentStatus
		if status == "" && lookup.CurrentEmployerConflict {
			status = "historical"
		}
		if status != "" {
			statuses[normalizePersonName(lookup.Person)] = status
		}
	}
	if len(statuses) == 0 {
		return nil
	}
	excluded := make(map[*models.Employee]bool)
	excludedNames := make(map[string]bool)
	removed := make([]models.OffEntityRow, 0)
	for _, row := range rows {
		key := normalizePersonName(row.employee.Name)
		status := statuses[key]
		if status == "" || status == "current" {
			continue
		}
		if status == "historical" {
			row.employee.Verification = models.VerificationHistorical
			row.employee.VerificationNote = appendNote(row.employee.VerificationNote, "historical association: former, retired or deceased status")
		} else {
			row.employee.Verification = models.VerificationCurrentUnverified
			row.employee.VerificationNote = appendNote(row.employee.VerificationNote, "current employment could not be established")
		}
		excluded[row.employee] = true
		excludedNames[key] = true
		source := row.employee.PublicSource
		if len(row.employee.VerifiedBy) > 0 && row.employee.VerifiedBy[0] != "" {
			source = row.employee.VerifiedBy[0]
		}
		removed = append(removed, models.OffEntityRow{
			Name: row.employee.Name, Title: row.employee.Title, Place: row.context,
			Source: source, Reason: row.employee.VerificationNote,
		})
	}
	if remove {
		removeRosterRows(chart, excluded, excludedNames)
	}
	return removed
}

// applyEntityRescue upgrades every row whose company association the lookup confirmed: a
// row no collected source could tie to the company stops being off-entity, and a match that
// rested on the name alone is upgraded when the result matched a stronger key. It reports
// how many rows changed, and counts them on the gate so the artifact can say how many
// people are in the chart *because* of a lookup.
func applyEntityRescue(lookup *models.ValidationLookup, candidate lookupCandidate, position map[*models.Employee]int, gate *models.EntityGate) int {
	rescued := 0
	for _, row := range candidate.rows {
		if _, ok := position[row.employee]; !ok {
			continue
		}
		if entityKeyStrength(lookup.MatchedKey) <= entityKeyStrength(row.employee.EntityKey) {
			continue
		}
		row.employee.EntityKey = lookup.MatchedKey
		if source := strings.TrimSpace(lookup.Source); source != "" {
			row.employee.VerifiedBy = append([]string{source}, row.employee.VerifiedBy...)
		}
		row.employee.VerificationNote = appendNote(row.employee.VerificationNote,
			"company association confirmed by reverse lookup")
		rescued++
	}
	if rescued > 0 && gate != nil {
		gate.RescuedByLookup += rescued
	}
	return rescued
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

// entityKeyStrength ranks how strongly a key ties a source to the company. The bare
// name is the weakest, because a same-named company matches it exactly the same way.
func entityKeyStrength(key string) int {
	switch key {
	case models.EntityKeyDomain:
		return 4
	case models.EntityKeyRegistration:
		return 3
	case models.EntityKeyAlias:
		return 2
	case models.EntityKeyName:
		return 1
	default:
		return 0
	}
}

// strongestEntityKey returns the best key across a row's sources, or "" when no source
// names the company at all.
func strongestEntityKey(sources []rowSource) string {
	strongest := ""
	for _, source := range sources {
		if entityKeyStrength(source.EntityKey) > entityKeyStrength(strongest) {
			strongest = source.EntityKey
		}
	}
	return strongest
}

// applyEntityVerdicts records every person the entity gate could not tie to the
// company and — in enforce mode — removes them from the chart.
//
// The removal is the blunt half of the gate, and the record is what keeps it honest:
// each excluded person is listed with the title, the department and the source that
// failed to name the company, so a short roster is explainable and a wrong exclusion
// is visible. Report mode keeps the rows and labels them, which is how the gate's
// effect is measured without changing the output.
func applyEntityVerdicts(remove bool, chart *models.OrgChart, rows []rosterRow, gate *models.EntityGate, matcher *entityMatcher) {
	excluded := make(map[*models.Employee]bool, 4)
	excludedNames := make(map[string]bool, 4)
	for _, row := range rows {
		// A row whose name is the company's own name is the entity, not a person: a
		// shareholding or registry page labels a promoter that way, and the model reads
		// it as a person. It is excluded even though its source does name the company.
		namesEntity := matcher.namesTheEntity(row.employee.Name)
		if !namesEntity && strings.TrimSpace(row.employee.EntityKey) != "" {
			continue
		}
		source := strings.TrimSpace(row.employee.PublicSource)
		if len(row.employee.VerifiedBy) > 0 && strings.TrimSpace(row.employee.VerifiedBy[0]) != "" {
			source = row.employee.VerifiedBy[0]
		}
		reason := "no source for this person names the researched company"
		if namesEntity {
			reason = "the row names the company itself, not a person"
		}
		gate.RowsOffEntity++
		gate.OffEntity = append(gate.OffEntity, models.OffEntityRow{
			Name:   strings.TrimSpace(row.employee.Name),
			Title:  strings.TrimSpace(row.employee.Title),
			Place:  row.context,
			Source: source,
			Reason: reason,
		})
		if !remove {
			row.employee.VerificationNote = appendNote(row.employee.VerificationNote,
				"not tied to the researched company (entity gate: report mode)")
			continue
		}
		excluded[row.employee] = true
		if name := normalizePersonName(row.employee.Name); name != "" {
			excludedNames[name] = true
		}
	}
	if !remove || len(excluded) == 0 {
		return
	}
	removeRosterRows(chart, excluded, excludedNames)
}

// removeRosterRows drops excluded people from the leadership list and from every
// department, and clears a department head who was excluded: a head no longer in the
// chart must not stay named at the top of it.
func removeRosterRows(chart *models.OrgChart, excluded map[*models.Employee]bool, excludedNames map[string]bool) {
	if len(chart.Leadership) > 0 {
		kept := chart.Leadership[:0]
		for index := range chart.Leadership {
			if excluded[&chart.Leadership[index]] {
				continue
			}
			kept = append(kept, chart.Leadership[index])
		}
		chart.Leadership = kept
	}
	for department := range chart.Departments {
		members := chart.Departments[department].Members
		if len(members) > 0 {
			kept := members[:0]
			for index := range members {
				if excluded[&members[index]] {
					continue
				}
				kept = append(kept, members[index])
			}
			chart.Departments[department].Members = kept
		}
		if head := strings.TrimSpace(chart.Departments[department].Head); head != "" && excludedNames[normalizePersonName(head)] {
			chart.Departments[department].Head = ""
		}
	}
}
