package main

import (
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
// an explicit person location, management levels and 5+ years on every query.
// Leadership is searched without a department; procurement/purchasing and
// sales/marketing get their department facets.
func marketRocketReachCompanyQueries(company, location string) []marketRocketReachQuery {
	base := func() map[string][]string {
		return map[string][]string{
			"current_employer":  {strings.TrimSpace(company)},
			"management_levels": {"cxo", "vp", "director", "manager"},
			"years_experience":  {marketRocketReachSeniority},
			"location":          {strings.TrimSpace(location)},
		}
	}
	leadership := marketRocketReachQuery{Label: "Leadership", Query: base()}
	procurement := marketRocketReachQuery{Label: "Procurement & purchasing", Query: base()}
	procurement.Query["department"] = []string{"procurement", "purchasing"}
	sales := marketRocketReachQuery{Label: "Sales & marketing", Query: base()}
	sales.Query["department"] = []string{"sales", "marketing"}
	return []marketRocketReachQuery{leadership, procurement, sales}
}

func (s *Server) handleMarketRocketReachCompany(w http.ResponseWriter, r *http.Request) {
	company := strings.TrimSpace(r.FormValue("company_name"))
	location := strings.TrimSpace(r.FormValue("location"))
	if company == "" || location == "" {
		s.render(w, "market_rocketreach_company_result", marketRocketReachCompanyView{
			Company: company, Location: location,
			Error: "A company and an explicit person location are required for a strict RocketReach search.",
		})
		return
	}
	if s.rocketReach == nil || !s.rocketReach.Enabled() {
		s.render(w, "market_rocketreach_company_result", marketRocketReachCompanyView{
			Company: company, Location: location, Error: "RocketReach is not configured.",
		})
		return
	}

	view := marketRocketReachCompanyView{
		Company: company, Location: location,
		Query: "leadership + procurement/purchasing + sales/marketing; management levels; 5+ years; location " + location,
	}
	seen := make(map[string]bool)
	var searchErrors []string
	for _, roleQuery := range marketRocketReachCompanyQueries(company, location) {
		profiles, err := s.rocketReach.SearchCompany(r.Context(), roleQuery.Query)
		if err != nil {
			log.Printf("[ROCKETREACH] company search %q (%s) failed: %v", company, roleQuery.Label, err)
			searchErrors = append(searchErrors, roleQuery.Label+": "+err.Error())
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
		view.Warning = "Some role searches failed: " + strings.Join(searchErrors, "; ")
	}
	s.render(w, "market_rocketreach_company_result", view)
}
