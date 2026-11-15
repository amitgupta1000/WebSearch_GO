package agent

import (
	"fmt"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

func marketPresenceFor(company *models.MarketLandscapeCompany, query models.MarketLandscapeQuery) (string, string, string, string) {
	hq := strings.TrimSpace(company.Country)
	if strings.EqualFold(hq, "global") || strings.EqualFold(hq, "worldwide") {
		hq = ""
	}
	text := marketWordText(company.Name + " " + company.Region + " " + company.EvidenceSummary)
	for _, evidence := range company.Evidence {
		text += " " + marketWordText(evidence.Country+" "+evidence.Title+" "+evidence.Snippet+" "+evidence.Content)
	}
	countries := marketTargetCountries(query)
	regions := marketTargetRegions(query)
	if len(countries) == 0 && len(regions) == 0 {
		return models.MarketPresenceGlobal, "", "Global scope requested; no local country filter applied.", hq
	}
	// A country field is an HQ hint, not proof of a local operation. Require an
	// operating-place phrase before counting a company as local/regional;
	// otherwise a global supplier whose extraction country was filled from the
	// query would masquerade as a local operator.
	operationEvidence := marketContainsAny(text, marketLocalOperationTerms)
	for _, country := range countries {
		if marketLocalOperationPhrase(text, country) ||
			(marketCountryMatches(hq, country) && operationEvidence) {
			return models.MarketPresenceLocal, country, fmt.Sprintf("Operating evidence names %s.", country), hq
		}
	}
	for _, region := range regions {
		if operationEvidence && (marketMentionsRegion(text, region) || marketCountryInRegion(hq, region)) {
			location := strings.TrimSpace(hq)
			if location == "" {
				location = region
			}
			return models.MarketPresenceRegional, location, fmt.Sprintf("Operating evidence is in %s but not in the requested country.", region), hq
		}
	}
	target := strings.Join(append(append([]string{}, countries...), regions...), " / ")
	if hq != "" || marketContainsAny(text, []string{"global", "worldwide", "international", "multinational", "multi country"}) {
		reason := "No " + target + " operating location found; classified as global/cross-border."
		if hq != "" {
			reason = fmt.Sprintf("No %s operating location found; HQ in %s. Classified as global/cross-border.", target, hq)
		}
		return models.MarketPresenceGlobal, "", reason, hq
	}
	return models.MarketPresenceUnknown, "", "No " + target + " operating location could be verified.", hq
}

func marketTargetCountries(query models.MarketLandscapeQuery) []string {
	out := make([]string, 0, len(query.Countries))
	for _, country := range query.Countries {
		country = strings.TrimSpace(country)
		if country != "" && !marketGlobalScopeTerm(country) {
			out = append(out, country)
		}
	}
	// The console's India/Asia scope radios land in Regions. A known country
	// name in that list must be treated as a country, not as a region, or an
	// India-scoped run would label every local operator "regional".
	for _, region := range query.Regions {
		region = strings.TrimSpace(region)
		if region != "" && marketIsCountryName(region) {
			out = append(out, region)
		}
	}
	return uniqueMarketValues(out)
}

func marketTargetRegions(query models.MarketLandscapeQuery) []string {
	out := make([]string, 0, len(query.Regions))
	for _, region := range query.Regions {
		region = strings.TrimSpace(region)
		if region != "" && !marketGlobalScopeTerm(region) && !marketIsCountryName(region) {
			out = append(out, region)
		}
	}
	return out
}

func marketIsCountryName(value string) bool {
	key := strings.ToLower(strings.TrimSpace(value))
	switch key {
	case "usa", "us", "u s", "u.s.", "united states of america", "uk", "u k", "u.k.",
		"great britain", "u.a.e.", "u a e", "ksa", "turkiye":
		return true
	}
	_, ok := marketCountryAliases[key]
	return ok
}

func uniqueMarketValues(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func marketGlobalScopeTerm(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "global", "worldwide", "international", "all regions":
		return true
	}
	return false
}

func marketHasGeographyScope(query models.MarketLandscapeQuery) bool {
	return len(marketTargetCountries(query)) > 0 || len(marketTargetRegions(query)) > 0
}

func marketMentionsGeography(text, country string) bool {
	return marketContainsAny(text, marketCountryTerms(country))
}

// marketLocalOperationPhrase requires an operating noun next to the target
// place ("plant in Gujarat", "India office"), rather than a company page that
// merely mentions a country. That distinction is what keeps a global brand's
// sales page out of the local manufacturer section.
func marketLocalOperationPhrase(text, country string) bool {
	for _, term := range marketCountryTerms(country) {
		for _, operation := range marketLocalOperationTerms {
			if marketContainsAny(text, []string{operation + " in " + term, term + " " + operation}) {
				return true
			}
		}
	}
	return false
}

func marketMentionsRegion(text, region string) bool {
	return marketContainsAny(text, marketRegionTermsFor(region))
}

func marketCountryMatches(country, target string) bool {
	if strings.TrimSpace(country) == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(country), strings.TrimSpace(target)) {
		return true
	}
	return marketMentionsGeography(marketWordText(country), target)
}

func marketCountryInRegion(country, region string) bool {
	if strings.TrimSpace(country) == "" {
		return false
	}
	return marketContainsAny(marketWordText(country), marketRegionTermsFor(region))
}

func marketCountryTerms(country string) []string {
	key := strings.ToLower(strings.TrimSpace(country))
	switch key {
	case "usa", "us", "u s", "u.s.", "united states of america":
		key = "united states"
	case "uk", "u k", "u.k.", "great britain":
		key = "united kingdom"
	case "u.a.e.", "u a e":
		key = "uae"
	case "ksa":
		key = "saudi arabia"
	case "turkiye":
		key = "turkey"
	}
	terms := []string{key}
	if aliases, ok := marketCountryAliases[key]; ok {
		terms = append(terms, aliases...)
	}
	return uniqueMarketTerms(terms)
}

func marketRegionTermsFor(region string) []string {
	key := strings.ToLower(strings.TrimSpace(region))
	terms := []string{key}
	if aliases, ok := marketRegionTerms[key]; ok {
		terms = append(terms, aliases...)
		for _, country := range aliases {
			terms = append(terms, marketCountryTerms(country)...)
		}
	}
	return uniqueMarketTerms(terms)
}

func uniqueMarketTerms(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(marketWordText(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
