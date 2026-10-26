package agent

import (
	"fmt"
	"strings"
)

// person_plan.go replaces person mode's three hardcoded searches with a plan, the way company
// mode plans its queries. The old searches asked one broad question and two afterthoughts; the
// plan asks the same confirmed identity from several directions — profile, role, appointments,
// career history, and the people-data aggregators — every one of them anchored on the confirmed
// name and company, because an unanchored query is how another person's pages used to reach the
// synthesiser.

// personPlanMaxQueries caps the plan. Social discovery has its own bounded searches and is
// kept separate from employment evidence.
const personPlanMaxQueries = 9

// personDirectorySection is the heading the aggregator snippets are written under, unchanged
// from when they were a separate ad-hoc search.
const personDirectorySection = "DIRECTORY CLAIMS (aggregator snippets: a stated title and employer, not verified employment)"

// personPlanEntry is one planned search. Section is the synthesis-context heading the kept
// snippets are written under, so the model can tell which question a snippet answers.
type personPlanEntry struct {
	section string
	query   string
	// snippetsOnly marks results that are never fetched: the people-data aggregators state a
	// title and employer but are usually paywalled past the snippet, so their URLs would only
	// spend scrape budget failing.
	snippetsOnly    bool
	personalization bool
}

// buildPersonPlan returns the run's searches, anchored on the confirmed identity. With no
// company confirmed the plan cannot anchor on an employer, so it asks for the pages that
// carry one; the gate still holds whatever comes back to the name.
func buildPersonPlan(name, company, location string) []personPlanEntry {
	quotedName := fmt.Sprintf("%q", strings.TrimSpace(name))
	if strings.TrimSpace(company) == "" {
		plan := []personPlanEntry{
			{section: "PRIMARY PROFILE SNIPPETS", query: fmt.Sprintf("%s (linkedin OR profile OR leadership OR career)", quotedName)},
			{section: "APPOINTMENTS AND CAREER SNIPPETS", query: fmt.Sprintf("%s (interview OR appointment OR speaker OR award OR education)", quotedName)},
		}
		if aggregator := sourceSiteQuery(quotedName, alternativeProfileSources); aggregator != "" {
			plan = append(plan, personPlanEntry{section: personDirectorySection, query: aggregator, snippetsOnly: true})
		}
		plan = append(plan, publicPersonalizationQueries(quotedName, company, location)...)
		return limitPersonPlan(plan)
	}
	base := strings.TrimSpace(strings.Join([]string{quotedName, strings.TrimSpace(company), strings.TrimSpace(location)}, " "))
	plan := []personPlanEntry{
		{section: "PRIMARY PROFILE SNIPPETS", query: base},
		{section: "ROLE AND POSITION SNIPPETS", query: base + " (leadership OR management OR team OR founder OR director OR promoter)"},
		{section: "APPOINTMENTS AND NEWS SNIPPETS", query: base + " (appointment OR promoted OR joins OR interview OR keynote OR award)"},
		{section: "CAREER AND EDUCATION SNIPPETS", query: base + " (career OR experience OR education OR alumni OR university)"},
	}
	if aggregator := sourceSiteQuery(base, alternativeProfileSources); aggregator != "" {
		plan = append(plan, personPlanEntry{section: personDirectorySection, query: aggregator, snippetsOnly: true})
	}
	plan = append(plan, publicPersonalizationQueries(quotedName, company, location)...)
	return limitPersonPlan(plan)
}

func publicPersonalizationQueries(name, company, location string) []personPlanEntry {
	context := strings.TrimSpace(strings.Join([]string{company, location}, " "))
	base := strings.TrimSpace(strings.Join([]string{name, context}, " "))
	return []personPlanEntry{
		{section: "PUBLIC PERSONALIZATION — FACEBOOK", query: "site:facebook.com " + base, snippetsOnly: true, personalization: true},
		{section: "PUBLIC PERSONALIZATION — INSTAGRAM", query: "site:instagram.com " + base, snippetsOnly: true, personalization: true},
		{section: "PUBLIC PERSONALIZATION — X/TWITTER", query: "(site:x.com OR site:twitter.com) " + base, snippetsOnly: true, personalization: true},
	}
}

// limitPersonPlan trims the plan to its cap and drops duplicate queries — the deterministic
// safety net, the same one company mode applies to its planner's output.
func limitPersonPlan(plan []personPlanEntry) []personPlanEntry {
	seen := make(map[string]bool, len(plan))
	limited := make([]personPlanEntry, 0, personPlanMaxQueries)
	for _, entry := range plan {
		key := strings.ToLower(strings.TrimSpace(entry.query))
		if entry.query == "" || seen[key] || len(limited) >= personPlanMaxQueries {
			continue
		}
		seen[key] = true
		limited = append(limited, entry)
	}
	return limited
}
