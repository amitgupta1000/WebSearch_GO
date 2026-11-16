package agent

import (
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

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

// demandTradeDataLanes are the buyer-side discovery lanes added alongside the
// existing "importer import data" lane. They ask for the record itself rather
// than only for a company description.
func demandTradeDataLanes() []demandLane {
	return []demandLane{
		{term: "importer customs shipment data"},
		{term: "buyer import records bill of lading"},
	}
}
