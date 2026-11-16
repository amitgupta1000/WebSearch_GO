package agent

import (
	"net/url"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TradeSourceCustody is the tier of first-party customs records served by the
// Crystal import-export DB. Unlike the public trade sites (tier "trade"), a
// custody record names the actual importer or exporter of the shipment, so it
// is authoritative about identity — within the geography the record type is
// valid for: importers are Indian by definition, suppliers are foreign
// exporters into India.
const TradeSourceCustody = "custody"

// tradeDBHost is the synthetic host every trade-record evidence URL carries,
// so the tier helpers and the operator-facing exports recognize first-party
// records without string-matching their payload.
const tradeDBHost = "trade-db.crystal.internal"

// isCustodyDomain reports whether a host is the trade DB's synthetic host.
func isCustodyDomain(domain string) bool {
	return strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(domain), "www."), tradeDBHost)
}

// custodyEvidenceURL builds the auditable provenance URL for one trade-record
// row. It is not fetchable — it names where inside the run's evidence corpus
// the row came from, the same way a scraped hit names its page.
func custodyEvidenceURL(direction, name string) string {
	slug := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), "-"))
	return "crystal://" + tradeDBHost + "/" + url.PathEscape(direction) + "/" + url.PathEscape(slug)
}

// tradeDataSourceMarkers are public trade-data and customs-record platforms.
// They publish shipment listings; they are never the operating company itself.
// The list is deliberately explicit rather than a generic "trade" substring:
// tradeindia.com and similar B2B marketplaces are already classified by the
// shared aggregator markers and must keep that label.
var tradeDataSourceMarkers = []string{
	"volza.com",
	"trademo.com",
	"eximpedia.app",
	"seair.co.in",
	"zauba.com",
	"importgenius.",
	"panjiva.",
	"datamyne.",
	"trademap.org",
	"comtrade.un.org",
	"tradingeconomics.com",
	"worldtradescanner.com",
	"exportgenius.",
	"billiontext.",
}

// isTradeDataDomain reports whether a host belongs to a trade-data platform.
func isTradeDataDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(domain), "www."))
	if domain == "" {
		return false
	}
	for _, marker := range tradeDataSourceMarkers {
		if strings.Contains(domain, marker) {
			return true
		}
	}
	return false
}

// companyTradeDataQueries are the deterministic company-search slots that ask
// for import, export, customs and shipment records. They are prepended to the
// planner's output and also added to the deterministic fallback, so trade data
// is searched even when the planner never proposes it. Every query is anchored
// on the company name, which keeps the entity gate satisfied.
func companyTradeDataQueries(brief models.CompanyBrief) []models.PlannedQuery {
	company := strings.TrimSpace(brief.CompanyName)
	if company == "" {
		return nil
	}
	name := companyNameSearchQuery(company)
	return []models.PlannedQuery{
		{
			Query:      name + ` (import OR export) (customs OR shipment OR "bill of lading")`,
			Purpose:    "find import/export shipment and customs records for the company",
			TargetArea: "trade data",
			Priority:   1,
		},
		{
			Query:      name + ` site:volza.com OR site:trademo.com OR site:eximpedia.app OR site:seair.co.in OR site:zauba.com`,
			Purpose:    "find the company's records on public trade-data platforms",
			TargetArea: "trade data",
			Priority:   1,
		},
	}
}

// marketTradeDataLanes are the seller-side discovery lanes. The lane text is
// appended to the product and geography by the market agent, yielding queries
// such as "used cooking oil exporter manufacturer shipment customs India".
func marketTradeDataLanes() []string {
	return []string{
		"exporter manufacturer shipment customs",
		"export data bill of lading",
	}
}

// demandMarketIntelLanes are the buyer-side lanes for the operator-promoted
// market-intelligence directories (ensun, revenuebase). A generic site-scoped
// query is deliberate: an ensun or revenuebase page that mentions the product
// at all is a lead worth reading — the profiles rank manufacturers and their
// products, the read extracts the names, and the buyer verification (producer
// gate, consumption evidence) sorts buyers from sellers.
func demandMarketIntelLanes() []demandLane {
	return []demandLane{
		{term: "site:ensun.io", productAnchored: true},
		{term: "site:revenuebase.ai", productAnchored: true},
	}
}

// sellerMarketIntelLanes are the seller-side lanes for the same directories:
// the seller landscape wants producers, so the site query names the activity
// and the role the profiles are organized around.
func sellerMarketIntelLanes(activity string) []string {
	trimmed := strings.TrimSpace(activity)
	lanes := make([]string, 0, len(sourceMarketIntelMarkers))
	for _, site := range sourceMarketIntelMarkers {
		if trimmed == "" {
			lanes = append(lanes, "site:"+site+" manufacturers")
			continue
		}
		lanes = append(lanes, "site:"+site+" "+trimmed+" manufacturers")
	}
	return lanes
}

// demandTradeDataLanes are the buyer-side discovery lanes added alongside the
// existing "importer import data" lane. They ask for the record itself rather
// than only for a company description.
func demandTradeDataLanes() []demandLane {
	return []demandLane{
		{term: "importer customs shipment data"},
		{term: "buyer import records bill of lading"},
	}
}
