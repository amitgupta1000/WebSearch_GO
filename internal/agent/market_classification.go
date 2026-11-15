package agent

import (
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// classifyMarketCompanies assigns role and geographic presence after all
// evidence has been merged. The LLM's role is only a hint; the deterministic
// evidence scan decides the report section.
func classifyMarketCompanies(companies map[string]*models.MarketLandscapeCompany, query models.MarketLandscapeQuery) {
	for _, company := range companies {
		role, tags := marketRoleFor(company)
		company.Role = role
		company.RoleTags = tags
		presence, country, reason, hq := marketPresenceFor(company, query)
		company.Presence = presence
		company.PresenceCountry = country
		company.PresenceReason = reason
		company.HQCountry = hq
	}
}

func marketRoleFor(company *models.MarketLandscapeCompany) (string, []string) {
	text := marketWordText(company.Name + " " + company.EvidenceSummary)
	for _, evidence := range company.Evidence {
		text += " " + marketWordText(evidence.Title+" "+evidence.Snippet+" "+evidence.Content)
	}
	strongManufacturer := marketContainsAny(text, []string{
		"manufactures", "manufacturing", "manufactured", "manufacturer of",
		"leading manufacturer", "production plant", "production facility",
		"processing plant", "processing facility", "refiner", "refinery", "refining",
		"factory", "factories", "smelter", "plant operator", "production site",
	})
	detected := make([]string, 0, 4)
	add := func(role string) {
		for _, existing := range detected {
			if existing == role {
				return
			}
		}
		detected = append(detected, role)
	}
	if marketContainsAny(text, []string{
		"manufacturer", "manufactures", "manufacturing", "manufactured",
		"producer", "producers", "produces", "production", "refiner", "refinery",
		"refining", "blending plant", "blending facility", "processing plant",
		"processing facility", "factory", "factories", "mill", "smelter",
		"plant operator", "production site",
	}) {
		add(models.MarketRoleManufacturer)
	}
	if marketContainsAny(text, []string{
		"distributor", "distributors", "distribution", "distributes", "authorized dealer",
		"authorised dealer", "dealer", "dealers", "reseller", "channel partner",
	}) {
		add(models.MarketRoleDistributor)
	}
	if marketContainsAny(text, []string{
		"wholesaler", "wholesalers", "wholesale", "wholesaling", "bulk supplier",
		"bulk supply", "stockist", "stockists",
	}) {
		add(models.MarketRoleWholesaler)
	}
	if marketContainsAny(text, []string{
		"trader", "traders", "trading", "imports", "exports", "importer",
		"exporter", "import export", "merchant", "broker",
	}) {
		add(models.MarketRoleTrader)
	}
	hint := marketNormalizeRole(company.Role)
	if hint != "" {
		add(hint)
	}
	primary := models.MarketRoleUnknown
	for _, role := range []string{
		models.MarketRoleManufacturer, models.MarketRoleDistributor,
		models.MarketRoleWholesaler, models.MarketRoleTrader,
	} {
		for _, candidate := range detected {
			if candidate == role {
				primary = role
				break
			}
		}
		if primary != models.MarketRoleUnknown {
			break
		}
	}
	// A distributor page routinely mentions "manufacturer" without
	// manufacturing anything. If there is no strong self-manufacturing signal,
	// an explicit distributor/wholesaler/trader phrase wins.
	if !strongManufacturer {
		for _, role := range []string{models.MarketRoleDistributor, models.MarketRoleWholesaler, models.MarketRoleTrader} {
			for _, candidate := range detected {
				if candidate == role {
					primary = role
					break
				}
			}
			if primary != models.MarketRoleManufacturer && primary != models.MarketRoleUnknown {
				break
			}
		}
	}
	// The extractor's page-level hint breaks the remaining ties.
	if hint != "" && hint != models.MarketRoleManufacturer && !strongManufacturer {
		primary = hint
	}
	return primary, detected
}

func marketNormalizeRole(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "manufacturer", "manufacturers", "producer", "producers", "production",
		"refiner", "refinery", "blender", "blending":
		return models.MarketRoleManufacturer
	case "distributor", "distributors", "dealer", "dealers", "reseller",
		"resellers", "distribution":
		return models.MarketRoleDistributor
	case "wholesaler", "wholesalers", "wholesale", "stockist", "stockists":
		return models.MarketRoleWholesaler
	case "trader", "traders", "trading", "importer", "importer-exporter",
		"exporter", "merchant", "broker":
		return models.MarketRoleTrader
	default:
		return ""
	}
}

func marketRoleLabel(role string) string {
	switch role {
	case models.MarketRoleManufacturer:
		return "Manufacturers / producers"
	case models.MarketRoleDistributor:
		return "Distributors"
	case models.MarketRoleWholesaler:
		return "Wholesalers / stockists"
	case models.MarketRoleTrader:
		return "Traders / importers-exporters"
	default:
		return "Other / unclassified"
	}
}
