package agent

import (
	"fmt"
	"strings"
	"time"
)

// person_plan.go replaces person mode's three hardcoded searches with a plan, the way company
// mode plans its queries. The old searches asked one broad question and two afterthoughts; the
// plan asks the same confirmed identity from several directions — profile, role, appointments,
// career history, and the people-data aggregators — every one of them anchored on the confirmed
// name and company, because an unanchored query is how another person's pages used to reach the
// synthesiser.

// personPlanMaxQueries caps the plan. Social discovery has its own bounded searches and is
// kept separate from employment evidence.
const personPlanMaxQueries = 12
const personSearchConcurrency = 4

// personSocialSignalCap bounds the flagged social section. A person's public footprint can
// span hundreds of posts; the section is a handful of attributable leads, not an archive.
const personSocialSignalCap = 12

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
	// spend scrape budget failing. Social sections are snippets-only too: a social page is
	// outreach context, not employment evidence, and fetching it would put unverified social
	// content one step away from the dossier.
	snippetsOnly    bool
	personalization bool
	// platform names the social platform for a personalization entry, so the flagged social
	// section can label a signal without re-parsing its URL.
	platform string
}

// buildPersonPlan returns the run's searches, anchored on the confirmed identity. With no
// company confirmed the plan cannot anchor on an employer, so it asks for the pages that
// carry one; the gate still holds whatever comes back to the name.
func buildPersonPlan(name, company, location, country string) []personPlanEntry {
	quotedName := fmt.Sprintf("%q", strings.TrimSpace(name))
	if strings.TrimSpace(company) == "" {
		plan := []personPlanEntry{
			{section: "PRIMARY PROFILE SNIPPETS", query: fmt.Sprintf("%s (linkedin OR profile OR leadership OR career)", quotedName)},
			{section: "APPOINTMENTS AND CAREER SNIPPETS", query: fmt.Sprintf("%s (interview OR appointment OR speaker OR award OR education)", quotedName)},
		}
		if aggregator := personDirectoryQuery(quotedName, location, country); aggregator != "" {
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
	if aggregator := personDirectoryQuery(base, location, country); aggregator != "" {
		plan = append(plan, personPlanEntry{section: personDirectorySection, query: aggregator, snippetsOnly: true})
	}
	plan = append(plan, publicPersonalizationQueries(quotedName, company, location)...)
	return limitPersonPlan(plan)
}

// publicPersonalizationQueries asks each public platform a bounded question. The results are
// deliberately kept in their own context block and rendered under a separate "may not be
// accurate" heading: a social account is outreach context, never identity or employment
// proof. Every query stays anchored on the confirmed name and one corroborating token so a
// common name does not turn the section into a list of strangers.
func publicPersonalizationQueries(name, company, location string) []personPlanEntry {
	context := strings.TrimSpace(strings.Join([]string{company, location}, " "))
	base := strings.TrimSpace(strings.Join([]string{name, context}, " "))
	recent := time.Now().AddDate(-2, 0, 0).Format("2006-01-02")
	return []personPlanEntry{
		{section: "PUBLIC PERSONALIZATION — X/TWITTER", platform: "X/Twitter", query: "(site:x.com OR site:twitter.com) " + base + " (interest OR opinion OR conference OR industry) after:" + recent, snippetsOnly: true, personalization: true},
		{section: "PUBLIC PERSONALIZATION — FACEBOOK", platform: "Facebook", query: "site:facebook.com " + base + " (about OR founder OR director OR profile) after:" + recent, snippetsOnly: true, personalization: true},
		{section: "PUBLIC PERSONALIZATION — INSTAGRAM", platform: "Instagram", query: "site:instagram.com " + base + " (work OR project OR event OR conference) after:" + recent, snippetsOnly: true, personalization: true},
		{section: "PUBLIC PERSONALIZATION — LINKEDIN POSTS", platform: "LinkedIn posts", query: "(site:linkedin.com/posts OR site:linkedin.com/pulse) " + base + " (insight OR announcement OR conference OR opinion) after:" + recent, snippetsOnly: true, personalization: true},
		{section: "PUBLIC PERSONALIZATION — YOUTUBE", platform: "YouTube", query: "site:youtube.com " + base + " (interview OR talk OR channel OR about) after:" + recent, snippetsOnly: true, personalization: true},
	}
}

func personDirectoryQuery(base, location, country string) string {
	hosts := append([]string(nil), alternativeProfileSources...)
	if strings.Contains(strings.ToLower(location+" "+country), "india") {
		hosts = append(hosts, promotedCompanyAggregators...)
	}
	return sourceSiteQuery(base, hosts)
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
