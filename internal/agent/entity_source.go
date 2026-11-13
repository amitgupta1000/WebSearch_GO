package agent

import (
	"net/url"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// discoverySourceKind is deliberately separate from evidence quality. A
// research report may be excellent evidence for discovering a name, but it is
// never proof that the report title or publisher is the operating entity.
type discoverySourceKind string

const (
	sourceOperator       discoverySourceKind = "operator"
	sourceResearchReport discoverySourceKind = "research_report"
	sourceDirectory      discoverySourceKind = "directory"
	sourceNews           discoverySourceKind = "news"
	sourceSocial         discoverySourceKind = "social"
	sourceOther          discoverySourceKind = "other"
)

func classifyDiscoverySource(hit models.SearchResult) discoverySourceKind {
	host := ""
	if parsed, err := url.Parse(strings.TrimSpace(hit.Link)); err == nil {
		host = strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	}
	if isResearchPublisherDomain(host) || isResearchDocumentTitle(hit.Title) {
		return sourceResearchReport
	}
	// Aggregators, academic publishers, consultancies and industry bodies are
	// publishers of discovery evidence, never the operating entity.
	if sharedSourceOverride(host) != "" {
		return sourceOther
	}
	if strings.Contains(host, "linkedin.") || strings.Contains(host, "wikipedia.") ||
		strings.Contains(host, "directory") {
		return sourceDirectory
	}
	if strings.Contains(host, "facebook.") || strings.Contains(host, "youtube.") ||
		strings.Contains(host, "twitter.") || strings.Contains(host, "x.com") {
		return sourceSocial
	}
	if isNewsSourceDomain(host) {
		return sourceNews
	}
	if host == "" {
		return sourceOther
	}
	return sourceOperator
}

// Shared discovery-source vocabulary.
//
// The producer/seller and buyer landscapes both have to answer "is this page a
// company, or is it a publisher writing about companies?". They must answer it
// the same way — one list, so a new research vendor or news desk does not have
// to be taught twice. The producer side uses these markers for tiering; the
// buyer side additionally overrides the tier with the same labels.

// sourceAggregatorMarkers are tender portals, B2B marketplaces and trade
// aggregators: they publish listings, they do not operate plants.
var sourceAggregatorMarkers = []string{
	"tender", "rfp", "rfq", "eprocure", "e-procurement", "bidding",
	"indiamart", "tradeindia", "alibaba", "made-in-china", "exportersindia",
	"tradekey", "ec21", "go4worldbusiness", "exporthub", "marketplace",
}

// sourcePublisherMarkers are academic publishers and repositories:
// bibliography, not operating entities.
var sourcePublisherMarkers = []string{
	"mdpi", "pubs.acs", "wiley", "sciencedirect", "springer", "researchgate",
	"semanticscholar", "ssrn", "ieee", "journals", "scholar", "ncbi",
	"nature.com", "sciencedaily", "scribd", "slideshare", "amazon.", "books.google",
}

// sourceConsultancyMarkers are market-research vendors and consultancies: they
// write about a market, they do not participate in it.
var sourceConsultancyMarkers = []string{
	"imarc", "mordorintelligence", "statista", "credence", "grandviewresearch",
	"marketsandmarkets", "gminsights", "fortunebusinessinsights", "researchandmarkets",
	"intratec", "databridge", "alliedmarket", "persistencemarket", "techsci",
	"stratview", "nextmove", "marketresearch", "transparencymarketresearch",
	"kenresearch", "skyquest", "verifiedmarketresearch", "maximizemarketresearch",
	"businessresearchinsights", "thebusinessresearchcompany", "sphericalinsights",
	"marketscale", "expertmarketresearch", "futuremarketinsights", "industryarc",
	"factmr", "valuemarketresearch", "emergenresearch", "wiseguyreports",
	"360iresearch", "precedenceresearch", "credenceresearch", "infinitiresearch",
	"fairfieldmarketresearch", "marketresearchintellect", "reportsanddata",
}

// sourceInstitutionMarkers are conferences, associations and bodies:
// commentary and community, not companies.
var sourceInstitutionMarkers = []string{
	"conference", "confintl", "symposium", "associa", "society", "council",
	"federation", "institute", "iea.", "unep", "oecd",
}

// sourceNewsMarkers are news desks, wires and trade-news sites. An article
// hosted anywhere can still be caught by its title; this list catches the
// venues whose *domain* already answers the question.
var sourceNewsMarkers = []string{
	"reuters.", "bloomberg.", "oilprice.", "apnews.", "prnewswire.",
	"businesswire.", "globenewswire.", "einpresswire.", "accesswire.",
	"cnbc.", "forbes.", "ft.com", "wsj.com", "economictimes.", "livemint.",
	"business-standard.", "thehindu.", "hindustantimes.", "timesofindia.",
	"indiatimes.", "ndtv.", "firstpost.", "moneycontrol.", "energyvoice.",
	"upstreamonline.", "rigzone.", "offshore-technology.", "hydrocarbonprocessing.",
	"chemweek.", "platts.", "news", "newsroom", "pressrelease",
}

// sharedSourceOverride labels a non-company domain. The returned tier is never
// qualifying, so such pages stay in a run as discovery evidence and leads while
// being unable to present as companies themselves. Empty when the domain is not
// a known publisher, aggregator, consultancy or institution.
func sharedSourceOverride(domain string) string {
	domain = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(domain), "www."))
	for _, marker := range sourceAggregatorMarkers {
		if strings.Contains(domain, marker) {
			return "aggregator"
		}
	}
	for _, marker := range sourcePublisherMarkers {
		if strings.Contains(domain, marker) {
			return "publisher"
		}
	}
	for _, marker := range sourceConsultancyMarkers {
		if strings.Contains(domain, marker) {
			return "consultancy"
		}
	}
	for _, marker := range sourceInstitutionMarkers {
		if strings.Contains(domain, marker) {
			return "institution"
		}
	}
	return ""
}

func isNewsSourceDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(domain), "www."))
	for _, marker := range sourceNewsMarkers {
		if strings.Contains(domain, marker) {
			return true
		}
	}
	return false
}

func isResearchEvidenceURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	return isResearchPublisherDomain(parsed.Hostname())
}
