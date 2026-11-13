package agent

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// sources.go holds the non-LinkedIn places a person's title and employer can be found.
//
// They exist because LinkedIn person profiles turned out to be unwinnable: measured with a
// genuine export, one cookie-authenticated read returned the real page and the next request
// was answered with a redirect loop. A dossier still needs "who holds this role and where",
// and company/people aggregators publish exactly that — as a directory claim about a named
// individual rather than a profile the person maintains.
//
// The same domains are two different things depending on what is being researched:
//
//   - As *person* material they are a claim to corroborate, which the person path already
//     labels (personDirectoryHosts, "treat its role and employer as a claim to check").
//   - As *company* material some are directories, while source-profile slots explicitly allow
//     the selected global publishers and directories. Other low-quality hosts remain filtered
//     as noise before they are fetched.
var alternativeProfileSources = []string{
	"crunchbase.com",
	"zoominfo.com",
	"clearbit.com",
	"apollo.io",
	"hunter.io",
}

// promotedCompanyAggregators receive dedicated company-mode Google-index slots.
// Keep this list limited to sources that routinely expose company or people records
// in their public result snippets.
var promotedCompanyAggregators = []string{
	"economictimes.indiatimes.com",
	"zaubacorp.com",
	"thecompanycheck.com",
	"tracxn.com",
	"weedoo.in",
	"rocketreach.co",
}

// globalCompanySources are reserved for the global source profile. They are not
// automatically mixed into the India-focused company plan because news and business
// directories have different coverage and ranking characteristics.
var globalCompanySources = []string{
	"linkedin.com",
	"zoominfo.com",
	"apollo.io",
	"rocketreach.co",
	"theorg.com",
	"forbes.com",
	"fortune.com",
	"reuters.com",
	"craft.co",
}

const (
	SourceProfileAuto   = "auto"
	SourceProfileIndia  = "india"
	SourceProfileGlobal = "global"
)

func companySourceProfile(brief models.CompanyBrief) string {
	switch strings.ToLower(strings.TrimSpace(brief.SourceProfile)) {
	case SourceProfileIndia, SourceProfileGlobal:
		return strings.ToLower(strings.TrimSpace(brief.SourceProfile))
	}
	country := strings.ToLower(strings.TrimSpace(brief.Country))
	if strings.Contains(country, "india") || strings.HasPrefix(strings.ToUpper(strings.TrimSpace(brief.RegistrationID)), "L") || strings.HasPrefix(strings.ToUpper(strings.TrimSpace(brief.RegistrationID)), "U") {
		return SourceProfileIndia
	}
	// Preserve the established company-mode behavior when no geography is supplied;
	// users can select Global explicitly when researching an international company.
	return SourceProfileIndia
}

func companySourceHosts(brief models.CompanyBrief) []string {
	if companySourceProfile(brief) == SourceProfileIndia {
		return promotedCompanyAggregators
	}
	hosts := make([]string, 0, len(globalCompanySources)-1)
	for _, host := range globalCompanySources {
		if host != "linkedin.com" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func matchesGlobalCompanySource(targetURL string) bool {
	lowered := strings.ToLower(targetURL)
	for _, host := range globalCompanySources {
		if strings.Contains(lowered, host) {
			return true
		}
	}
	return false
}

// isRocketReachCompanyProfileURL distinguishes public company records from
// RocketReach person pages, whose contact details must not become company facts.
func isRocketReachCompanyProfileURL(targetURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	if host != "rocketreach.co" && !strings.HasSuffix(host, ".rocketreach.co") {
		return false
	}
	path := strings.ToLower(parsed.EscapedPath())
	if strings.Contains(path, "-email_") || strings.Contains(path, "-phone_") || strings.Contains(path, "/person/") {
		return false
	}
	return strings.Contains(path, "-profile") || strings.Contains(path, "/company/")
}

// companyAcceptedProfileSources returns the alternative profile hosts the company entity gate
// keeps for secondary/person corroboration.
func companyAcceptedProfileSources() []string {
	kept := make([]string, 0, len(alternativeProfileSources))
	for _, host := range alternativeProfileSources {
		if !hostIsCompanyDenied(host) {
			kept = append(kept, host)
		}
	}
	return kept
}

// hostIsCompanyDenied reports whether a host is on the company deny-list, which is what
// decides whether a company-mode query for it would be wasted.
func hostIsCompanyDenied(host string) bool {
	lowered := strings.ToLower(host)
	for _, denied := range lowQualityHosts {
		if strings.Contains(lowered, denied) {
			return true
		}
	}
	return false
}

// matchesAlternativeProfileSource reports whether a URL belongs to one of the aggregators.
func matchesAlternativeProfileSource(targetURL string) bool {
	lowered := strings.ToLower(targetURL)
	for _, host := range alternativeProfileSources {
		if strings.Contains(lowered, host) {
			return true
		}
	}
	return false
}

func matchesPromotedCompanyAggregator(targetURL string) bool {
	lowered := strings.ToLower(targetURL)
	for _, host := range promotedCompanyAggregators {
		if strings.Contains(lowered, host) {
			return true
		}
	}
	return false
}

// sourceSiteQuery renders a search that is restricted to the given hosts: the site: operator
// per host, joined with OR inside one parenthesised group. Google's OR is unreliable enough
// that this is best-effort, which is why the result is treated as one bounded query rather
// than depended on.
func sourceSiteQuery(base string, hosts []string) string {
	trimmedBase := strings.TrimSpace(base)
	if trimmedBase == "" || len(hosts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(hosts))
	for _, host := range hosts {
		parts = append(parts, "site:"+host)
	}
	return fmt.Sprintf("%s (%s)", trimmedBase, strings.Join(parts, " OR "))
}

// profileSourceHint is the sentence the query planner is given about these domains, kept next
// to the list so the two cannot drift apart.
func profileSourceHint() string {
	return "Company and people data aggregators (" + strings.Join(companyAcceptedProfileSources(), ", ") +
		") may be searched with site: to find a named person's title and employer; their pages state claims " +
		"rather than verified employment, so treat them as leads to corroborate, and they are often paywalled " +
		"so the snippet may be all that is readable."
}
