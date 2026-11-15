package agent

import (
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

func marketCoverageFor(companies map[string]*models.MarketLandscapeCompany, query models.MarketLandscapeQuery) *models.MarketCoverage {
	coverage := &models.MarketCoverage{Minimum: marketCoverageMinimum}
	for _, company := range companies {
		if marketCoverageEligible(company, query) {
			coverage.Companies++
		} else if company.Presence == models.MarketPresenceGlobal || company.Presence == models.MarketPresenceUnknown {
			coverage.Global++
		}
	}
	coverage.Pass = coverage.Companies >= coverage.Minimum
	return coverage
}

func marketCoverageEligible(company *models.MarketLandscapeCompany, query models.MarketLandscapeQuery) bool {
	if !marketHasGeographyScope(query) {
		return true
	}
	return company.Presence == models.MarketPresenceLocal || company.Presence == models.MarketPresenceRegional
}

// marketGroupsFor turns the flat result list into the report's role sections.
// Once a geography filter is active, global/unknown-presence companies move to
// a separate bucket instead of inflating a local role section.
func marketGroupsFor(companies []models.MarketLandscapeCompany, query models.MarketLandscapeQuery) []models.MarketLandscapeGroup {
	if len(companies) == 0 {
		return nil
	}
	scoped := marketHasGeographyScope(query)
	groups := make(map[string]*models.MarketLandscapeGroup, 6)
	order := []string{
		models.MarketRoleManufacturer, models.MarketRoleDistributor,
		models.MarketRoleWholesaler, models.MarketRoleTrader,
		models.MarketRoleUnknown, "global",
	}
	for _, company := range companies {
		key := company.Role
		if key == "" {
			key = models.MarketRoleUnknown
		}
		label := marketRoleLabel(key)
		presence := ""
		if scoped && (company.Presence == models.MarketPresenceGlobal || company.Presence == models.MarketPresenceUnknown) {
			key = "global"
			label = "Global / cross-border"
			presence = models.MarketPresenceGlobal
		}
		group := groups[key]
		if group == nil {
			group = &models.MarketLandscapeGroup{Key: key, Label: label, Presence: presence}
			groups[key] = group
		}
		group.Companies = append(group.Companies, company)
	}
	out := make([]models.MarketLandscapeGroup, 0, len(groups))
	for _, key := range order {
		if group := groups[key]; group != nil && len(group.Companies) > 0 {
			out = append(out, *group)
		}
	}
	return out
}
