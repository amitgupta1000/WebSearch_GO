package agent

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// entity_resolve.go offers the operator a short list of candidate identities before a
// run starts.
//
// This is the cheapest place to fix an entity problem: one or two searches now, versus
// a full run's worth of results that all look relevant and belong to another company.
// "CJ Shah" is exactly why it exists — the name reads as initials plus a surname, and
// the search results mix the chemicals company with a Mumbai recruitment firm of the
// same name. A human decides; the gate then holds the run to that decision.

const (
	// entityResolveMaxCandidates bounds the list the operator chooses from.
	entityResolveMaxCandidates = 5
	// entityResolveMaxResults bounds each disambiguation search.
	entityResolveMaxResults = 10
)

// registryHosts are directories, registries and social networks: their pages are useful
// as evidence (a registry URL carries the registration number) but never as the
// candidate domain, because a run gated on linkedin.com would be gated on nothing.
var registryHosts = []string{
	"linkedin.", "facebook.", "instagram.", "twitter.", "x.com", "youtube.", "tiktok.",
	"glassdoor.", "indeed.", "ziprecruiter.", "naukri.", "ambitionbox.", "justdial.",
	"zaubacorp.", "tofler.", "thecompanycheck.", "instafinancials.", "indiafilings.",
	"rocketreach.", "zoominfo.", "datanyze.", "wiza.", "tracxn.", "apollo.io",
	"crunchbase.", "bloomberg.", "wikipedia.", "wikidata.", "moneycontrol.",
	"indiamart.", "tradeindia.", "exportersindia.", "dnb.com", "facebook.com",
}

// registrationIDPattern matches an Indian CIN, which registry pages publish and which
// is the one key that cannot collide with a same-named company.
var registrationIDPattern = regexp.MustCompile(`\b([LU]\d{5}[A-Z]{2}\d{4}[A-Z]{3}\d{6})\b`)

// ResolveEntityCandidates searches for a company name and returns the identities worth
// choosing between. Failures are tolerated: a name with no discoverable domain still
// resolves to an empty list, and the caller can run on the typed name alone.
func (ca *CompanyAgent) ResolveEntityCandidates(ctx context.Context, name, country, city string) ([]models.EntityCandidate, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("a company name is required to resolve an entity")
	}
	location := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(city), strings.TrimSpace(country)}, " "))
	queries := []string{fmt.Sprintf("%q official website", name)}
	if location != "" {
		queries = append(queries, fmt.Sprintf("%q %s company profile", name, location))
	} else {
		queries = append(queries, fmt.Sprintf("%q company profile", name))
	}

	tokens := nameTokens(name)
	candidates := make(map[string]*models.EntityCandidate)
	registrationID, registrationSource := "", ""
	for _, query := range queries {
		results, err := ca.engine.SearchCached(ctx, query, entityResolveMaxResults)
		if err != nil {
			// One failed query must not lose the other's candidates.
			continue
		}
		for _, result := range results {
			// A registry page is evidence even though it is not a candidate domain: it
			// is where the registration number comes from.
			if registrationID == "" {
				if id := firstRegistrationID(result.Link + " " + result.Title + " " + result.Snippet); id != "" {
					registrationID, registrationSource = id, result.Link
				}
			}
			host := normalizeDomain(result.Link)
			if host == "" || isRegistryHost(host) {
				continue
			}
			candidate, ok := candidates[host]
			if !ok {
				candidate = &models.EntityCandidate{
					Name:   name,
					Domain: host,
				}
				candidates[host] = candidate
			}
			candidate.Score++
			if candidate.EvidenceTitle == "" {
				candidate.EvidenceTitle = strings.TrimSpace(result.Title)
				candidate.EvidenceURL = result.Link
			}
			if candidate.RegistrationID == "" {
				candidate.RegistrationID = firstRegistrationID(result.Link + " " + result.Title + " " + result.Snippet)
			}
			// A domain that carries more of the company's name is far more likely to be
			// the company's own site than a directory that merely mentions it.
			echo := domainEchoStrength(host, tokens)
			candidate.Score += float64(echo)
			if echo > 0 {
				candidate.NameMatch = true
			}
		}
	}

	resolved := make([]models.EntityCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		resolved = append(resolved, *candidate)
	}
	sort.SliceStable(resolved, func(i, j int) bool {
		if resolved[i].Score != resolved[j].Score {
			return resolved[i].Score > resolved[j].Score
		}
		return resolved[i].Domain < resolved[j].Domain
	})
	if len(resolved) > entityResolveMaxCandidates {
		resolved = resolved[:entityResolveMaxCandidates]
	}
	// A candidate whose domain does not carry the company's name is only offered when
	// nothing better exists, and it is flagged. The reason is the gate itself: choosing
	// crisil.com or opensanctions.org as "the company" would make every page on that
	// domain count as company material, which is worse than no domain at all.
	if named := filterNameMatching(resolved); len(named) > 0 {
		resolved = named
	}
	// The operator gets the registration number together with the page it came from: a
	// CIN is the one key that cannot collide with a same-named company, and a suggestion
	// without provenance is not worth acting on.
	if len(resolved) > 0 && resolved[0].RegistrationID == "" && registrationID != "" {
		resolved[0].RegistrationID = registrationID
		resolved[0].RegistrationSource = registrationSource
	}
	return resolved, nil
}

// filterNameMatching keeps the candidates whose domain carries the company's name.
func filterNameMatching(candidates []models.EntityCandidate) []models.EntityCandidate {
	kept := make([]models.EntityCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.NameMatch {
			kept = append(kept, candidate)
		}
	}
	return kept
}

// isRegistryHost reports whether a host is a directory, registry or social network.
func isRegistryHost(host string) bool {
	for _, registry := range registryHosts {
		if strings.Contains(host, registry) {
			return true
		}
	}
	return false
}

// domainEchoStrength scores how much of the company's name a domain carries: 2 when the
// first label is the name itself ("cjshah.com" for CJ Shah, "kljresources.in" for KLJ
// Resources), 1 when it contains the name or one of its words ("cjshahco.com"), 0
// otherwise. The company's own site is usually the whole name, and the same-named firm
// next door is usually an extended one.
func domainEchoStrength(host string, tokens []string) int {
	if len(tokens) == 0 || host == "" {
		return 0
	}
	label := strings.NewReplacer("-", "", "_", "").Replace(strings.Split(host, ".")[0])
	joined := strings.Join(tokens, "")
	switch {
	case joined != "" && label == joined:
		return 2
	case joined != "" && strings.Contains(label, joined):
		return 1
	}
	for _, token := range tokens {
		if len(token) >= 3 && strings.Contains(label, token) {
			return 1
		}
	}
	return 0
}

// firstRegistrationID returns the first registry identifier in a text, or "".
func firstRegistrationID(text string) string {
	match := registrationIDPattern.FindString(text)
	return strings.ToUpper(match)
}
