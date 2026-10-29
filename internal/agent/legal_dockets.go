package agent

// legal_dockets.go groups the surfaced, clubbed matters into the report's
// dockets and derives the overall risk position deterministically. The stages
// are: surfacing (the searches), clubbing (foldCaseIterations), classification
// (the model's category/status labels), and then grouping + reporting — with
// no model narrative after that.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// Docket families.
const (
	docketCompanyLaw = "company-law"
	docketCriminal   = "criminal"
	docketTax        = "tax"
	docketOther      = "other"
)

var (
	// companyLawMarkers name the venues and subjects that make a matter a
	// company-law matter: NCLT and CIRP/insolvency, SEBI and its appellate
	// tribunal, ROC/MCA and Companies Act proceedings, NCLAT and IBBI.
	companyLawMarkers = []string{
		"nclt", "national company law", "insolvency and bankruptcy", "insolvency",
		"winding up", "oppression and mismanagement", "sebi", "securities and exchange board",
		"securities appellate tribunal", "registrar of companies", "ministry of corporate affairs",
		"nclat", "ibbi", "insolvency and bankruptcy board",
	}
	// criminalMarkers name the criminal-law signals in a title or venue.
	criminalMarkers = []string{"criminal", "complaint case", "fir ", " ipc", " bns", "police"}
	// taxMarkers name the income-tax, GST and allied-tax signals.
	taxMarkers = []string{
		"itat", "income tax appellate", "income tax", "income-tax", "tax",
		"gst", "goods and services", "cestat", "customs", "central excise", "service tax", "tds",
	}
)

// containsAnyMarker reports whether the haystack carries one of the markers.
func containsAnyMarker(haystack string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(haystack, marker) {
			return true
		}
	}
	return false
}

// legalFamily routes a surfaced matter into its docket. The mapping is
// deterministic: the classification stage's category label is respected, and
// the court/venue and title text supply the markers the label may have missed.
func legalFamily(finding models.LegalFinding) string {
	court := strings.ToLower(finding.Court)
	title := strings.ToLower(finding.CaseTitle)
	category := strings.ToLower(finding.Category)

	if category == "criminal" || containsAnyMarker(court, criminalMarkers) || containsAnyMarker(title, criminalMarkers) {
		return docketCriminal
	}
	if category == "insolvency" || category == "sebi" || containsAnyMarker(court, companyLawMarkers) || containsAnyMarker(title, companyLawMarkers) {
		return docketCompanyLaw
	}
	if category == "tax" || containsAnyMarker(court, taxMarkers) || containsAnyMarker(title, taxMarkers) {
		return docketTax
	}
	return docketOther
}

// docketLabels are the display names of the dockets, in report order.
var docketLabels = map[string]string{
	docketCompanyLaw: "Company law (NCLT, SEBI, insolvency)",
	docketCriminal:   "Criminal law",
	docketTax:        "Income tax & other tax",
	docketOther:      "Other matters",
}

// buildDockets groups the surfaced, clubbed matters into the report's dockets.
// The same-named bucket stays out: those records are not attributed to the
// researched party and are reported separately.
func buildDockets(findings []models.LegalFinding) []models.LegalDocket {
	order := []string{docketCompanyLaw, docketCriminal, docketTax, docketOther}
	byFamily := make(map[string]*models.LegalDocket, len(order))
	for _, family := range order {
		byFamily[family] = &models.LegalDocket{Name: docketLabels[family], Category: family}
	}
	for _, finding := range findings {
		docket := byFamily[legalFamily(finding)]
		docket.Matters = append(docket.Matters, finding)
		if strings.EqualFold(finding.Status, "pending") {
			docket.Pending++
		}
		if docket.Category == docketCompanyLaw && finding.Role == models.LegalRoleFacing {
			docket.Defendant++
		}
	}
	dockets := make([]models.LegalDocket, 0, len(order))
	for _, family := range order {
		if docket := byFamily[family]; len(docket.Matters) > 0 {
			dockets = append(dockets, *docket)
		}
	}
	return dockets
}

// reportSummary is the deterministic docket overview: what was surfaced,
// clubbed where, and what the risk position weighs — no model narrative. When
// classification did not run, the summary says so, because the dockets then
// carry identity labels only.
func reportSummary(dockets []models.LegalDocket, matched, sameNamed, uncheckedPersons int, classified bool) string {
	total := 0
	parts := make([]string, 0, len(dockets))
	for _, docket := range dockets {
		total += len(docket.Matters)
		part := fmt.Sprintf("%s: %d matter(s)", docket.Name, len(docket.Matters))
		if docket.Pending > 0 || (docket.Category == docketCompanyLaw && docket.Defendant > 0) {
			part += " ("
			if docket.Pending > 0 {
				part += fmt.Sprintf("%d pending", docket.Pending)
				if docket.Defendant > 0 {
					part += ", "
				}
			}
			if docket.Category == docketCompanyLaw && docket.Defendant > 0 {
				part += fmt.Sprintf("%d with the company as respondent", docket.Defendant)
			}
			part += ")"
		}
		parts = append(parts, part)
	}
	summary := fmt.Sprintf("Surfaced %d matched matter(s) clubbed into %d docket(s) — %s.", matched, len(dockets), strings.Join(parts, "; "))
	if sameNamed > 0 {
		summary += fmt.Sprintf(" %d same-named record(s) kept for review, not attributed.", sameNamed)
	}
	if uncheckedPersons > 0 {
		summary += fmt.Sprintf(" %d person(s) left unchecked by the query budget.", uncheckedPersons)
	}
	if !classified {
		summary += " Classification was unavailable for this run, so matters carry identity labels only."
	}
	return summary
}

// buildRiskPosition derives the overall risk position deterministically from
// the clubbed dockets: only company-law matters where the company is clearly
// on the responding side (role facing) and inside the ten-year window feed the
// level. Criminal matters — often breach-of-contract or recovery complaints,
// sometimes filed by the company itself — and tax matters are reported in
// their own dockets and do not drive the level. There is no model call here:
// surfacing, clubbing, classification, and then this.
func buildRiskPosition(dockets []models.LegalDocket, now time.Time) *models.LegalRiskAssessment {
	cutoff := now.AddDate(-analysisRelevanceYears, 0, 0)
	total := 0
	var facing []models.LegalFinding
	for _, docket := range dockets {
		total += len(docket.Matters)
		if docket.Category != docketCompanyLaw {
			continue
		}
		for _, finding := range docket.Matters {
			if finding.Role != models.LegalRoleFacing {
				continue // "clearly marked as a defendant" — no parse, no weight
			}
			if date, dated := legalCaseDate(finding); dated && date.Before(cutoff) {
				continue
			}
			facing = append(facing, finding)
		}
	}

	position := &models.LegalRiskAssessment{Classified: false}
	facingPending, facingPendingHighImpact := 0, 0
	drivers := make([]string, 0, len(facing))
	for _, finding := range facing {
		if !strings.EqualFold(finding.Status, "pending") {
			continue
		}
		facingPending++
		label := finding.CaseTitle
		if finding.Court != "" {
			label += " (" + finding.Court
			if finding.Date != "" {
				label += ", " + finding.Date
			}
			label += ")"
		}
		drivers = append(drivers, label)
		lowered := strings.ToLower(finding.Court + " " + finding.CaseTitle + " " + finding.Category)
		if containsAnyMarker(lowered, []string{"insolv", "sebi", "nclt", "national company law"}) {
			facingPendingHighImpact++
		}
	}
	switch {
	case facingPendingHighImpact > 0:
		position.Level = "high"
	case facingPending > 0:
		position.Level = "moderate"
	case len(facing) > 0:
		position.Level = "low"
	default:
		position.Level = "none"
	}
	if len(drivers) > 3 {
		drivers = drivers[:3]
	}
	position.Drivers = drivers
	position.Summary = fmt.Sprintf("%d matter(s) surfaced across the dockets; the overall position weighs only company-law matters where the company is clearly on the responding side — %d such matter(s) (%d pending). Criminal and tax matters are reported in their own dockets and do not drive this level.", total, len(facing), facingPending)
	position.Note = "Deterministic position: surfacing, clubbing and classification only — no model narrative."
	return position
}

// regulatoryAuthorities are the order-passing bodies the reserved Serper
// searches cover, one anchored query each: `SEBI order "Acme Ltd"`.
var regulatoryAuthorities = []string{"SEBI", "NCLT", "NCLAT", "IBBI"}

// regulatoryMaxResults bounds each authority's Serper search.
const regulatoryMaxResults = 8

// regulatoryOrdersScan runs the reserved authority searches and surfaces the
// results that name the confirmed company or an alias. A hit becomes a
// company-law finding — the court field carries the authority ("SEBI",
// "NCLT"…) so the docket classifier routes it — and the role is facing when
// the text places the company after an order marker: "SEBI order against
// Acme" is exposure; "Acme wins appeal against SEBI" is the company pursuing
// its rights. Results are deduplicated by URL.
func (la *LegalAgent) regulatoryOrdersScan(ctx context.Context, sessionID string, identity models.CompanyBrief, report *models.LegalSourceReport, trace *models.SearchTrace, eventCh chan<- models.AgentEvent) []models.LegalFinding {
	name := strings.TrimSpace(identity.CompanyName)
	if name == "" {
		return nil
	}
	quoted := `"` + name + `"`
	aliases := make([]string, 0, len(identity.Aliases))
	for _, alias := range identity.Aliases {
		if alias = strings.TrimSpace(alias); alias != "" {
			aliases = append(aliases, alias)
		}
	}

	matched := make([]models.LegalFinding, 0)
	seen := map[string]bool{}
	for _, authority := range regulatoryAuthorities {
		query := fmt.Sprintf("%s order %s", authority, quoted)
		Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolCall,
			ToolName: authority, Content: query})
		results, err := la.engine.SearchCached(ctx, query, regulatoryMaxResults)
		report.Queries++
		if err != nil {
			if report.Unavailable == "" {
				report.Unavailable = err.Error()
			}
			Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolResult,
				ToolName: authority, Content: fmt.Sprintf("unavailable: %v", err)})
			trace.Queries = append(trace.Queries, models.QueryTrace{Query: query, Purpose: "regulatory order scan — " + authority})
			continue
		}
		report.Hits += len(results)
		keptForAuthority := make([]models.SearchResult, 0, len(results))
		for _, result := range results {
			if result.Link == "" || seen[result.Link] {
				continue
			}
			seen[result.Link] = true
			haystack := strings.ToLower(result.Title + " " + result.Snippet)
			matchKey := models.LegalKeyName
			needle := strings.ToLower(name)
			party := name
			if !strings.Contains(haystack, needle) {
				aliasHit := ""
				for _, alias := range aliases {
					if strings.Contains(haystack, strings.ToLower(alias)) {
						aliasHit = alias
						matchKey = models.LegalKeyAlias
						break
					}
				}
				if aliasHit == "" {
					continue // a regulatory scan hit must name the researched party
				}
				needle = strings.ToLower(aliasHit)
				party = aliasHit
			}
			role := ""
			// The order or proceeding is against the company when the text
			// places it after an order marker — the defendant side of a
			// regulatory action. Anything else stays unmarked and is reported
			// without feeding the risk position.
			for _, marker := range []string{"against", "in the matter of", "show cause"} {
				if idx := strings.Index(haystack, marker); idx >= 0 && strings.Index(haystack, needle) > idx {
					role = models.LegalRoleFacing
					break
				}
			}
			title := strings.TrimSpace(result.Title)
			if title == "" {
				title = result.Link
			}
			matched = append(matched, models.LegalFinding{
				CaseTitle:   title,
				Court:       authority,
				Date:        result.Date,
				URL:         result.Link,
				Snippet:     result.Snippet,
				MatchKey:    matchKey,
				Source:      "regulatory",
				Involvement: models.LegalPartyTo,
				Role:        role,
				Party:       party,
				PartyKind:   "company",
				Iterations:  1,
			})
			report.Kept++
			keptForAuthority = append(keptForAuthority, models.SearchResult{
				Title: title, Link: result.Link, Snippet: result.Snippet, Date: result.Date,
			})
		}
		if len(keptForAuthority) > 0 {
			trace.Queries = append(trace.Queries, models.QueryTrace{
				Query: query, Purpose: "regulatory order scan — " + authority, Kept: keptForAuthority,
			})
		} else {
			trace.Queries = append(trace.Queries, models.QueryTrace{
				Query: query, Purpose: "regulatory order scan — " + authority,
			})
		}
		Emit(eventCh, models.AgentEvent{SessionID: sessionID, Type: models.EventToolResult,
			ToolName: authority, Content: fmt.Sprintf("%d hit(s)", len(results))})
	}
	return matched
}
