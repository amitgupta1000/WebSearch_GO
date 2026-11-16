package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// The trade-records collection is the opt-in first-party lane: it queries the
// Crystal import-export DB for the product's ranked partners and publishes
// them as their own report sections. The lane is deliberately separate from
// the web pipeline — the DB answers in tens of seconds, an importer may
// equally be a reseller, and a supplier is an exporter into India — so the
// sections never feed the verified results, the stage rosters or the coverage
// floor. The operator decides what the names mean.

const (
	// tradeCollectionTimeout bounds one trade-DB collection. The live ranking
	// endpoints answer in ~20-45s under load, so a single call fits, but a
	// hanging DB must never consume a run's whole budget: the collection
	// degrades to a warning and the run continues on web lanes.
	tradeCollectionTimeout = 90 * time.Second
	// tradePartnerRankLimit is how many ranked partners one section carries.
	// The DB ranks by shipment value; a landscape needs the head, not the tail.
	tradePartnerRankLimit = 10
	// tradeRecordPageLimit is the record page the supplier aggregation reads.
	// The DB accepts up to 10000 rows; a chemical's recent shipments fit in a
	// few hundred, and the aggregation only needs the names that repeat.
	tradeRecordPageLimit = 200
)

// tradeDBImporterScope enforces the geography rule the DB's data carries:
// importer records are Indian importers by definition, so they are only valid
// for India-scoped research. The token test is deliberate — "West Indies"
// shares no "india" token, while "India", "india" and "INDIA" all match.
func tradeDBImporterScope(countries, regions []string) bool {
	for _, scope := range append(append([]string{}, countries...), regions...) {
		for _, token := range strings.Fields(strings.ToLower(strings.TrimSpace(scope))) {
			if token == "india" {
				return true
			}
		}
	}
	return false
}

// tradeSellerRegion returns the single geography a supplier query can be
// origin-scoped to, or "" when the run is global, multi-region or India (the
// DB's suppliers are foreign exporters into India, so an Indian seller cannot
// come from supplier records at all).
func tradeSellerRegion(countries, regions []string) string {
	var scopes []string
	for _, scope := range append(append([]string{}, countries...), regions...) {
		if strings.TrimSpace(scope) != "" {
			scopes = append(scopes, strings.TrimSpace(scope))
		}
	}
	if len(scopes) != 1 {
		return ""
	}
	scope := scopes[0]
	if tradeDBImporterScope([]string{scope}, nil) {
		return ""
	}
	return scope
}

// tradeResellerTags reads the name vocabulary that signals a reseller. An
// Indian importer of a chemical may simply be the distributor, wholesaler or
// trader who moves it onward, so the trade-records rows carry both the
// importer role and whatever reseller signal their own name gives — the
// operator makes the final call.
func tradeResellerTags(name string) []string {
	lowered := strings.ToLower(name)
	tags := make([]string, 0, 2)
	for _, candidate := range []struct {
		token string
		role  string
	}{
		{"trading", models.MarketRoleTrader},
		{"traders", models.MarketRoleTrader},
		{"merchant", models.MarketRoleTrader},
		{"broker", models.MarketRoleTrader},
		{"distributor", models.MarketRoleDistributor},
		{"distribution", models.MarketRoleDistributor},
		{"dealer", models.MarketRoleDistributor},
		{"wholesaler", models.MarketRoleWholesaler},
		{"wholesale", models.MarketRoleWholesaler},
		{"stockist", models.MarketRoleWholesaler},
	} {
		if strings.Contains(lowered, candidate.token) {
			tags = append(tags, candidate.role)
		}
	}
	return tags
}

// tradePartnerCompany turns one ranked customs partner into a report row: the
// first-party record is the identity (the DB names the actual importer or
// exporter), the evidence names the provenance, and the role stays explicit —
// "importer" with any reseller signal the name carries.
func tradePartnerCompany(partner models.TradePartner, activity string) *models.MarketLandscapeCompany {
	snippet := fmt.Sprintf("Crystal trade records: %d shipment(s) of %s totaling USD %.0f (%.0f MT), rank %d in the product ranking.",
		partner.TransactionCount, tradePartnerDisplayProduct(partner), partner.TotalValueUSD, partner.TotalQuantityMT, partner.Rank)
	company := &models.MarketLandscapeCompany{
		EntityKey:       "trade:" + partner.Direction + ":" + strings.ToLower(strings.TrimSpace(partner.Name)),
		Name:            partner.Name,
		Activity:        activity,
		Status:          "verified",
		Confidence:      0.9,
		EvidenceType:    "trade-record",
		EvidenceSummary: snippet,
		Description:     snippet,
		Trade:           &partner,
		Evidence: []models.MarketLandscapeEvidence{{
			URL:         custodyEvidenceURL(partner.Direction, partner.Name),
			Title:       "Crystal trade records — " + tradePartnerDisplayProduct(partner),
			Snippet:     snippet,
			Lane:        "trade-record-" + partner.Direction,
			ClaimType:   tradePartnerClaimType(partner),
			SourceTier:  TradeSourceCustody,
			Country:     tradePartnerCountry(partner),
			RetrievedAt: time.Now(),
			Status:      "verified",
		}},
	}
	switch partner.Direction {
	case models.TradeDirectionImporter:
		// The DB is India's import data: an importer row is an Indian entity
		// by construction, and it may equally be the reseller that moves the
		// product onward — both signals travel on the row.
		company.Country = "India"
		company.Presence = models.MarketPresenceLocal
		company.PresenceCountry = "India"
		company.PresenceReason = "First-party customs record: the entity is an Indian importer of the product."
		company.InScope = true
		company.Role = models.MarketRoleImporter
		company.RoleTags = append([]string{models.MarketRoleImporter}, tradeResellerTags(partner.Name)...)
	case models.TradeDirectionSupplier:
		// A supplier row is a foreign exporter into India. Its origin country
		// scopes it to a region; it is published for what it is, never folded
		// into the verified seller roster.
		company.Country = partner.CountryOfOrigin
		company.PresenceCountry = partner.CountryOfOrigin
		company.RoleTags = []string{"exporter"}
	}
	return company
}

// tradePartnerDisplayProduct names the product a partner row was ranked for,
// falling back to the HS code and then a generic phrase when blank.
func tradePartnerDisplayProduct(partner models.TradePartner) string {
	if strings.TrimSpace(partner.Product) != "" {
		return partner.Product
	}
	if strings.TrimSpace(partner.HSCode) != "" {
		return "HS " + partner.HSCode
	}
	return "the product"
}

// tradePartnerClaimType types the custody evidence: an importer record is an
// import claim; a supplier record is the supply-side observation.
func tradePartnerClaimType(partner models.TradePartner) string {
	if partner.Direction == models.TradeDirectionImporter {
		return models.DemandClaimImport
	}
	return "supply-record"
}

// collectTradeImporters queries the DB for the product's ranked Indian
// importers. The opt-in is the caller's; this layer enforces the capability
// and the geography: importer records are Indian importers, so a non-India
// run is skipped with the reason in a warning rather than silently queried.
func collectTradeImporters(ctx context.Context, client TradeDBSource, activity string, countries, regions []string, eventCh chan<- models.AgentEvent) ([]*models.MarketLandscapeCompany, []string) {
	if !tradeDBImporterScope(countries, regions) {
		return nil, []string{"Trade-record importers come from India's import data, so they are only queried for India-scoped runs; this run's geography skipped them."}
	}
	tctx, cancel := context.WithTimeout(ctx, tradeCollectionTimeout)
	defer cancel()
	emitAgentEvent(eventCh, models.EventThinking, "Querying the Crystal trade DB for the product's ranked importers", "", "")
	partners, err := client.TopImporters(tctx, activity, nil, tradePartnerRankLimit)
	if err != nil {
		return nil, []string{fmt.Sprintf("Trade-record importers unavailable; the run continues on web lanes: %v", err)}
	}
	return tradePartnerCompanies(partners, activity), nil
}

// tradePartnerCompanies drops blank names and maps the rest to report rows.
func tradePartnerCompanies(partners []models.TradePartner, activity string) []*models.MarketLandscapeCompany {
	companies := make([]*models.MarketLandscapeCompany, 0, len(partners))
	for _, partner := range partners {
		if strings.TrimSpace(partner.Name) == "" {
			continue
		}
		companies = append(companies, tradePartnerCompany(partner, activity))
	}
	return companies
}

// collectTradeSuppliers builds the seller-side section. When the run is scoped
// to exactly one non-India region, the suppliers come from raw records so the
// origin country can filter them: the ranking endpoint does not return the
// origin, but every record carries it, and the region's foreign ports (resolved
// from the DB's own vocabulary) only narrow the query. A global run takes the
// product's ranked exporters unscoped, labelled as cross-origin. An India run
// skips the section: the DB's suppliers are foreign exporters into India, so
// Indian sellers are out of their scope by construction.
func collectTradeSuppliers(ctx context.Context, client TradeDBSource, activity string, countries, regions []string, eventCh chan<- models.AgentEvent) ([]*models.MarketLandscapeCompany, []string) {
	region := tradeSellerRegion(countries, regions)
	tctx, cancel := context.WithTimeout(ctx, tradeCollectionTimeout)
	defer cancel()
	if region == "" {
		if tradeDBImporterScope(countries, regions) {
			return nil, []string{"Trade-record suppliers are foreign exporters into India; Indian sellers are out of their scope, so the section was skipped for this India run."}
		}
		emitAgentEvent(eventCh, models.EventThinking, "Querying the Crystal trade DB for the product's ranked exporters", "", "")
		partners, err := client.TopSuppliers(tctx, activity, nil, tradePartnerRankLimit)
		if err != nil {
			return nil, []string{fmt.Sprintf("Trade-record suppliers unavailable; the run continues on web lanes: %v", err)}
		}
		return tradePartnerCompanies(partners, activity), []string{"Trade-record suppliers are unscoped: no single origin region was requested, so the section carries the product's top exporters into India across all origins."}
	}
	emitAgentEvent(eventCh, models.EventThinking, "Querying the Crystal trade DB for supplier records scoped to "+region, "", "")
	ports := []string{}
	if options, err := client.FilterOptions(tctx); err == nil && options != nil {
		ports = tools.ResolveForeignPorts(options.ForeignPorts, region)
	}
	records, err := client.QueryRecords(tctx, models.TradeQuery{Product: activity, ForeignPorts: ports, Limit: tradeRecordPageLimit})
	if err != nil {
		return nil, []string{fmt.Sprintf("Trade-record suppliers unavailable; the run continues on web lanes: %v", err)}
	}
	return aggregateTradeSuppliers(records, activity, region)
}

// aggregateTradeSuppliers folds one page of records into ranked supplier rows.
// When a region was requested, records whose origin country does not share the
// region's tokens are dropped — origin_country is the authoritative geography,
// the port vocabulary only narrowed the query.
func aggregateTradeSuppliers(records []models.TradeRecord, activity, region string) ([]*models.MarketLandscapeCompany, []string) {
	type tally struct {
		count  int
		value  float64
		qty    float64
		origin string
	}
	tallies := make(map[string]*tally)
	dropped := 0
	for _, record := range records {
		name := strings.TrimSpace(record.Supplier)
		if name == "" {
			continue
		}
		origin := strings.TrimSpace(record.OriginCountry)
		if region != "" && !tools.RegionMatches(region, origin) {
			dropped++
			continue
		}
		entry := tallies[name]
		if entry == nil {
			entry = &tally{origin: origin}
			tallies[name] = entry
		}
		entry.count++
		entry.value += record.TotalValueUSD
		entry.qty += record.Quantity
		if entry.origin == "" {
			entry.origin = origin
		}
	}
	names := make([]string, 0, len(tallies))
	for name := range tallies {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if tallies[names[i]].value == tallies[names[j]].value {
			return names[i] < names[j]
		}
		return tallies[names[i]].value > tallies[names[j]].value
	})
	if len(names) > tradePartnerRankLimit {
		names = names[:tradePartnerRankLimit]
	}
	companies := make([]*models.MarketLandscapeCompany, 0, len(names))
	var warnings []string
	for rank, name := range names {
		entry := tallies[name]
		partner := models.TradePartner{
			Name:             name,
			Direction:        models.TradeDirectionSupplier,
			Product:          activity,
			CountryOfOrigin:  entry.origin,
			TransactionCount: entry.count,
			TotalValueUSD:    entry.value,
			TotalQuantityMT:  entry.qty,
			Rank:             rank + 1,
		}
		companies = append(companies, tradePartnerCompany(partner, activity))
	}
	if dropped > 0 {
		warnings = append(warnings, fmt.Sprintf("Trade-record suppliers: %d record(s) outside the requested origin region were dropped.", dropped))
	}
	return companies, warnings
}

// attachTradeImporters populates the buyer report's first-party section when
// the run opted in. Capability off and geography invalid both degrade to a
// warning — never to a failed run.
func (a *DemandChainAgent) attachTradeImporters(ctx context.Context, query models.DemandChainQuery, report *models.DemandChainReport, eventCh chan<- models.AgentEvent) {
	if !query.IncludeTradeRecords {
		return
	}
	client := a.engine.TradeDB()
	if client == nil {
		report.Warnings = append(report.Warnings, "Trade records were requested, but the trade DB API is not configured on this deployment.")
		return
	}
	companies, warnings := collectTradeImporters(ctx, client, query.Activity, query.Countries, query.Regions, eventCh)
	report.Warnings = append(report.Warnings, warnings...)
	for _, company := range companies {
		report.TradeImporters = append(report.TradeImporters, *company)
	}
}

// attachTradePartners populates the seller report's first-party sections when
// the run opted in. The two collections run concurrently — the DB's ranking
// queries are slow, and each is bounded by the client's own semaphore and the
// collection timeout.
func (a *MarketLandscapeAgent) attachTradePartners(ctx context.Context, query models.MarketLandscapeQuery, report *models.MarketLandscapeReport, eventCh chan<- models.AgentEvent) {
	if !query.IncludeTradeRecords {
		return
	}
	client := a.engine.TradeDB()
	if client == nil {
		report.Warnings = append(report.Warnings, "Trade records were requested, but the trade DB API is not configured on this deployment.")
		return
	}
	var (
		wg           sync.WaitGroup
		importers    []*models.MarketLandscapeCompany
		suppliers    []*models.MarketLandscapeCompany
		importerWarn []string
		supplierWarn []string
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		importers, importerWarn = collectTradeImporters(ctx, client, query.Activity, query.Countries, query.Regions, eventCh)
	}()
	go func() {
		defer wg.Done()
		suppliers, supplierWarn = collectTradeSuppliers(ctx, client, query.Activity, query.Countries, query.Regions, eventCh)
	}()
	wg.Wait()
	report.Warnings = append(report.Warnings, importerWarn...)
	report.Warnings = append(report.Warnings, supplierWarn...)
	for _, company := range importers {
		report.TradeImporters = append(report.TradeImporters, *company)
	}
	for _, company := range suppliers {
		report.TradeSuppliers = append(report.TradeSuppliers, *company)
	}
}

// tradePartnerCountry names where the row's entity sits: Indian importers are
// in India; suppliers carry their origin country when one was resolved.
func tradePartnerCountry(partner models.TradePartner) string {
	if partner.Direction == models.TradeDirectionImporter {
		return "India"
	}
	return partner.CountryOfOrigin
}
