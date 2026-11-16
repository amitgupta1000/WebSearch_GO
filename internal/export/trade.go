package export

import (
	"fmt"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// writeTradeSections renders the opt-in first-party customs sections shared by
// the seller and buyer exports. The sections are deliberately separate from
// the verified results: an importer may equally be a reseller, a supplier is
// an exporter into India, and the operator decides how to use the names. The
// cell helper is each report's own markdown escaper, so both files render with
// their existing conventions.
func writeTradeSections(b *strings.Builder, activity string, importers, suppliers []models.MarketLandscapeCompany, cell func(string) string) {
	if len(importers) > 0 {
		fmt.Fprintf(b, "## trade record importers (%d)\n\n", len(importers))
		fmt.Fprintf(b, "Indian importers of %s named by the Crystal trade DB. An importer may equally be a reseller (distributor, wholesaler or trader) — the operator decides; these rows are separate from verified results and the coverage floor.\n\n", activity)
		b.WriteString("| Company | Country | Role tags | Shipments | Value (USD) | Quantity (MT) | Rank | Status |\n| :--- | :--- | :--- | ---: | ---: | ---: | ---: | :--- |\n")
		for _, row := range importers {
			fmt.Fprintf(b, "| %s | %s | %s | %d | %.0f | %.0f | %d | %s |\n",
				cell(row.Name), cell(row.Country), cell(strings.Join(row.RoleTags, ", ")),
				tradeShipments(row), tradeValue(row), tradeQuantity(row), tradeRank(row), cell(row.Status))
		}
		b.WriteString("\n")
	}
	if len(suppliers) > 0 {
		fmt.Fprintf(b, "## trade record suppliers (%d)\n\n", len(suppliers))
		fmt.Fprintf(b, "Exporters of %s into India named by the Crystal trade DB, origin-scoped where the records carry a country. These rows are separate from verified seller results and the coverage floor.\n\n", activity)
		b.WriteString("| Company | Origin | Shipments | Value (USD) | Quantity (MT) | Rank | Status |\n| :--- | :--- | ---: | ---: | ---: | ---: | :--- |\n")
		for _, row := range suppliers {
			fmt.Fprintf(b, "| %s | %s | %d | %.0f | %.0f | %d | %s |\n",
				cell(row.Name), cell(row.Country),
				tradeShipments(row), tradeValue(row), tradeQuantity(row), tradeRank(row), cell(row.Status))
		}
		b.WriteString("\n")
	}
}

// tradeShipments, tradeValue, tradeQuantity and tradeRank read the first-party
// record behind a row, defaulting to zero for a row without one.
func tradeShipments(row models.MarketLandscapeCompany) int {
	if row.Trade != nil {
		return row.Trade.TransactionCount
	}
	return 0
}

func tradeValue(row models.MarketLandscapeCompany) float64 {
	if row.Trade != nil {
		return row.Trade.TotalValueUSD
	}
	return 0
}

func tradeQuantity(row models.MarketLandscapeCompany) float64 {
	if row.Trade != nil {
		return row.Trade.TotalQuantityMT
	}
	return 0
}

func tradeRank(row models.MarketLandscapeCompany) int {
	if row.Trade != nil {
		return row.Trade.Rank
	}
	return 0
}
