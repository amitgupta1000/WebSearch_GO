package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

const marketRocketReachSeniority = "5+"

type marketRocketReachGroup struct {
	Label    string
	Profiles []tools.RocketReachProfile
}

type marketRocketReachCompanyView struct {
	Company  string
	Location string
	Query    string
	Groups   []marketRocketReachGroup
	Error    string
	Warning  string
}

type marketRocketReachQuery struct {
	Label string
	Query map[string][]string
}

// marketRocketReachCompanyQueries is deliberately strict: an exact company,
// management levels and 5+ years on every query. A location is included when
// the search model provides one. Leadership is searched without a department;
// procurement/purchasing and sales/marketing get their department facets.
func marketRocketReachCompanyQueries(company, location, audience string) []marketRocketReachQuery {
	base := func() map[string][]string {
		query := map[string][]string{
			"current_employer":  {strings.TrimSpace(company)},
			"management_levels": {"cxo", "vp", "director", "manager"},
			"years_experience":  {marketRocketReachSeniority},
		}
		if location = strings.TrimSpace(location); location != "" {
			query["location"] = []string{location}
		}
		return query
	}
	leadership := marketRocketReachQuery{Label: "Leadership", Query: base()}
	if strings.EqualFold(strings.TrimSpace(audience), "buyer") {
		procurement := marketRocketReachQuery{Label: "Procurement & purchasing", Query: base()}
		procurement.Query["department"] = []string{"procurement", "purchasing"}
		supply := marketRocketReachQuery{Label: "Supply chain & logistics", Query: base()}
		supply.Query["department"] = []string{"supply chain", "logistics"}
		operations := marketRocketReachQuery{Label: "Operations & plant management", Query: base()}
		operations.Query["department"] = []string{"operations"}
		return []marketRocketReachQuery{leadership, procurement, supply, operations}
	}
	procurement := marketRocketReachQuery{Label: "Procurement & purchasing", Query: base()}
	procurement.Query["department"] = []string{"procurement", "purchasing"}
	sales := marketRocketReachQuery{Label: "Sales & marketing", Query: base()}
	sales.Query["department"] = []string{"sales", "marketing"}
	return []marketRocketReachQuery{leadership, procurement, sales}
}

func (s *Server) handleMarketRocketReachCompany(w http.ResponseWriter, r *http.Request) {
	company := strings.TrimSpace(r.FormValue("company_name"))
	location := strings.TrimSpace(r.FormValue("location"))
	audience := strings.TrimSpace(r.FormValue("audience"))
	if company == "" {
		s.render(w, "market_rocketreach_company_result", marketRocketReachCompanyView{
			Company: company, Location: location,
			Error: "A company is required for a RocketReach search.",
		})
		return
	}
	if s.rocketReach == nil || !s.rocketReach.Enabled() {
		s.render(w, "market_rocketreach_company_result", marketRocketReachCompanyView{
			Company: company, Location: location, Error: "RocketReach is not configured.",
		})
		return
	}

	roleScope := "leadership + procurement/purchasing + sales/marketing"
	if strings.EqualFold(audience, "buyer") {
		roleScope = "leadership + procurement/purchasing + supply chain/logistics + operations/plant"
	}
	view := marketRocketReachCompanyView{
		Company: company, Location: location,
		Query: roleScope + "; management levels; 5+ years",
	}
	if location != "" {
		view.Query += "; location " + location
	} else {
		view.Query += "; location not specified"
		view.Warning = "No company location was verified; RocketReach is searching the company and role filters without a location constraint."
	}
	seen := make(map[string]bool)
	var searchErrors []string
	for _, roleQuery := range marketRocketReachCompanyQueries(company, location, audience) {
		profiles, err := s.rocketReach.SearchCompany(r.Context(), roleQuery.Query)
		if err != nil {
			log.Printf("[ROCKETREACH] company search %q (%s) failed: %v", company, roleQuery.Label, err)
			searchErrors = append(searchErrors, roleQuery.Label+": "+err.Error())
			var httpErr *tools.RocketReachHTTPError
			if errors.As(err, &httpErr) {
				switch httpErr.StatusCode {
				case http.StatusUnauthorized, http.StatusForbidden:
					view.Warning += " RocketReach rejected the configured API key; verify the key has API access and is not expired."
				case http.StatusTooManyRequests:
					view.Warning += " RocketReach rate-limited the remaining role searches; retry after the provider window resets."
					break
				}
				if httpErr.StatusCode == http.StatusTooManyRequests {
					break
				}
			}
			continue
		}
		group := marketRocketReachGroup{Label: roleQuery.Label}
		for _, profile := range profiles {
			key := strings.TrimSpace(strconv.Itoa(profile.ID))
			if key == "0" || key == "" {
				key = strings.ToLower(strings.TrimSpace(profile.Name + "|" + profile.CurrentTitle + "|" + profile.CurrentEmployer))
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			group.Profiles = append(group.Profiles, profile)
		}
		if len(group.Profiles) > 0 {
			view.Groups = append(view.Groups, group)
		}
	}
	if len(view.Groups) == 0 {
		view.Error = "No RocketReach profiles matched the strict company, role, location and 5+ year filters."
		if len(searchErrors) > 0 {
			view.Error += " " + strings.Join(searchErrors, "; ")
		}
	} else if len(searchErrors) > 0 {
		view.Warning += " Some role searches failed: " + strings.Join(searchErrors, "; ")
	}
	s.render(w, "market_rocketreach_company_result", view)
}
