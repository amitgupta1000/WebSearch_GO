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
	if strings.Contains(host, "linkedin.") || strings.Contains(host, "wikipedia.") ||
		strings.Contains(host, "directory") {
		return sourceDirectory
	}
	if strings.Contains(host, "facebook.") || strings.Contains(host, "youtube.") ||
		strings.Contains(host, "twitter.") || strings.Contains(host, "x.com") {
		return sourceSocial
	}
	for _, marker := range []string{"reuters.", "bloomberg.", "news.", "oilprice."} {
		if strings.Contains(host, marker) {
			return sourceNews
		}
	}
	if host == "" {
		return sourceOther
	}
	return sourceOperator
}

func isResearchEvidenceURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	return isResearchPublisherDomain(parsed.Hostname())
}
