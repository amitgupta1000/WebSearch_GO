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
	// tradeRecordPageLimit is the record page the supplier aggregation reads.
	// The DB accepts up to 10000 rows; one page of a thousand recent
	// declarations is enough to rank a region's trade without paging, and the
	// report says when the page came back full so the window is never mistaken
	// for the whole history.
	tradeRecordPageLimit = 1000
)

// tradeRegionCountries expands a requested region into the places the trade DB
// can actually be scoped to. The DB has no region/location field — it carries
// foreign ports and origin countries — so "Middle East" or "Asia" must resolve to
// member countries before either the port filter or the origin filter can match
// anything. Region labels that are themselves regions ("mea", "gcc", "asia
// pacific") are skipped, and every place is alias-expanded, so "uae" also matches
// "united arab emirates" and its ports. A country-shaped or unknown scope is
// returned as itself, which keeps token matching working exactly as before.
func tradeRegionCountries(region string) []string {
	region = strings.TrimSpace(region)
	if region == "" || marketGlobalScopeTerm(region) {
		return nil
	}
	terms, known := marketRegionTerms[strings.ToLower(region)]
	if !known {
		return []string{region}
	}
	places := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		// Another region's label ("middle east", "africa", "emea") names no
		// country the DB can filter on; its own expansion covers the members.
		if _, isRegion := marketRegionTerms[term]; isRegion {
			continue
		}
		places = append(places, term)
		places = append(places, marketCountryTerms(term)...)
	}
	places = uniqueMarketValues(places)
	if len(places) == 0 {
		return []string{region}
	}
	return places
}

// tradeNameHasIndia reports whether a scope names India itself, with the same
// token test the importer gate uses (so "West Indies" never matches).
func tradeNameHasIndia(scope string) bool {
	for _, token := range strings.Fields(strings.ToLower(strings.TrimSpace(scope))) {
		if token == "india" {
			return true
		}
	}
	return false
}

// tradeScopeIsIndia reports whether the run's only scope is India itself — the
// one geography where supplier records are meaningless (they are foreign
// exporters into India).
func tradeScopeIsIndia(countries, regions []string) bool {
	scopes := make([]string, 0, len(countries)+len(regions))
	for _, scope := range append(append([]string{}, countries...), regions...) {
		if strings.TrimSpace(scope) != "" {
			scopes = append(scopes, strings.TrimSpace(scope))
		}
	}
	return len(scopes) == 1 && tradeNameHasIndia(scopes[0])
}

// tradeDBImporterScope enforces the geography rule the DB's data carries:
// importer records are Indian importers by definition, so they are only valid
// for a scope that contains India. A named region is expanded first, so "Asia"
// and "South Asia" qualify while "Middle East" does not. The token test is
// deliberate — "West Indies" shares no "india" token.
func tradeDBImporterScope(countries, regions []string) bool {
	for _, scope := range append(append([]string{}, countries...), regions...) {
		if tradeNameHasIndia(scope) {
			return true
		}
		for _, place := range tradeRegionCountries(scope) {
			if tradeNameHasIndia(place) {
				return true
			}
		}
	}
	return false
}

// tradeSellerRegion returns the single geography a supplier query can be
// origin-scoped to, or "" when the run is global, multi-region or India (the
// DB's suppliers are foreign exporters into India, so an Indian seller cannot
// come from supplier records at all). A global scope is deliberately unscoped:
// "global" is not a location in the DB, and treating it as one filtered out
// every record.
func tradeSellerRegion(countries, regions []string) string {
	scopes := make([]string, 0, len(countries)+len(regions))
	for _, scope := range append(append([]string{}, countries...), regions...) {
		if strings.TrimSpace(scope) != "" {
			scopes = append(scopes, strings.TrimSpace(scope))
		}
	}
	if len(scopes) != 1 {
		return ""
	}
	scope := scopes[0]
	if marketGlobalScopeTerm(scope) || tradeNameHasIndia(scope) {
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

// tradeRankingSummary lines up a ranking answer for the console log: the row
// count plus the head of the ranking, so a zero-row answer is as visible as a
// populated one.
func tradeRankingSummary(direction, product string, partners []models.TradePartner) string {
	if len(partners) == 0 {
		return fmt.Sprintf("0 ranked %s row(s) for product %q", direction, product)
	}
	head := make([]string, 0, 3)
	for _, partner := range partners {
		if len(head) == 3 {
			break
		}
		head = append(head, fmt.Sprintf("%s (%d shipment(s), USD %.0f)", partner.Name, partner.TransactionCount, partner.TotalValueUSD))
	}
	return fmt.Sprintf("%d ranked %s row(s) for product %q — head: %s", len(partners), direction, product, strings.Join(head, " · "))
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
// importers, capped at the report-wide partner budget. The opt-in is the
// caller's; this layer enforces the capability and the geography: importer
// records are Indian importers, so a scope that does not contain India (directly
// or through a named region such as Asia) is skipped with the reason in a
// warning rather than silently queried.
func collectTradeImporters(ctx context.Context, client TradeDBSource, activity string, limit int, countries, regions []string, eventCh chan<- models.AgentEvent) ([]*models.MarketLandscapeCompany, []string) {
	if !tradeDBImporterScope(countries, regions) {
		scope := strings.TrimSpace(strings.Join(append(append([]string{}, countries...), regions...), " / "))
		if scope == "" {
			scope = "global"
		}
		return nil, []string{fmt.Sprintf("Trade-record importers come from India's import data, so they are only queried for India-scoped runs (India, or a scope containing India such as Asia); this run's scope (%s) skipped them.", scope)}
	}
	if limit <= 0 {
		limit = defaultTradeDBPartnerLimit
	}
	tctx, cancel := context.WithTimeout(ctx, tradeCollectionTimeout)
	defer cancel()
	// The query itself is a visible tool call, and its answer a visible tool
	// result: the operator sees which name was searched and what came back,
	// including the silent zero-row case.
	emitAgentEvent(eventCh, models.EventToolCall,
		fmt.Sprintf("product=%q · scope=India · limit=%d", activity, limit),
		"trade-db", "top-importers")
	partners, err := client.TopImporters(tctx, activity, nil, limit)
	if err != nil {
		emitAgentEvent(eventCh, models.EventToolResult,
			fmt.Sprintf("query failed: %v", err), "trade-db", "top-importers")
		return nil, []string{fmt.Sprintf("Trade-record importers unavailable; the run continues on web lanes: %v", err)}
	}
	emitAgentEvent(eventCh, models.EventToolResult,
		tradeRankingSummary("importer", activity, partners), "trade-db", "top-importers")
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

// collectTradeSuppliers builds the seller-side section, capped at the
// report-wide partner budget. When the run is scoped to exactly one non-India
// region, the suppliers come from raw records so the origin country can filter
// them: the ranking endpoint does not return the origin, but every record
// carries it, and the region's foreign ports (resolved from the DB's own
// vocabulary) only narrow the query. A global run takes the product's ranked
// exporters unscoped, labelled as cross-origin. An India run skips the
// section: the DB's suppliers are foreign exporters into India, so Indian
// sellers are out of their scope by construction.
func collectTradeSuppliers(ctx context.Context, client TradeDBSource, activity string, limit int, countries, regions []string, eventCh chan<- models.AgentEvent) ([]*models.MarketLandscapeCompany, []string) {
	if limit <= 0 {
		limit = defaultTradeDBPartnerLimit
	}
	region := tradeSellerRegion(countries, regions)
	tctx, cancel := context.WithTimeout(ctx, tradeCollectionTimeout)
	defer cancel()
	if region == "" {
		if tradeScopeIsIndia(countries, regions) {
			return nil, []string{"Trade-record suppliers are foreign exporters into India; Indian sellers are out of their scope, so the section was skipped for this India run."}
		}
		emitAgentEvent(eventCh, models.EventToolCall,
			fmt.Sprintf("product=%q · scope=all origins (unscoped run) · limit=%d", activity, limit),
			"trade-db", "top-suppliers")
		partners, err := client.TopSuppliers(tctx, activity, nil, limit)
		if err != nil {
			emitAgentEvent(eventCh, models.EventToolResult,
				fmt.Sprintf("query failed: %v", err), "trade-db", "top-suppliers")
			return nil, []string{fmt.Sprintf("Trade-record suppliers unavailable; the run continues on web lanes: %v", err)}
		}
		emitAgentEvent(eventCh, models.EventToolResult,
			tradeRankingSummary("supplier", activity, partners), "trade-db", "top-suppliers")
		return tradePartnerCompanies(partners, activity), []string{"Trade-record suppliers are unscoped: no single origin region was requested, so the section carries the product's top exporters into India across all origins."}
	}
	// The DB has no region field: a region-scoped supplier query sends the ports
	// of the region's member countries and filters the records' origin countries
	// against the same expansion. Without the expansion "Middle East" matches no
	// port and no origin, and every record is dropped.
	regionCountries := tradeRegionCountries(region)
	ports := []string{}
	if options, err := client.FilterOptions(tctx); err == nil && options != nil {
		ports = tools.ResolveForeignPortsForCountries(options.ForeignPorts, region, regionCountries)
	}
	emitAgentEvent(eventCh, models.EventToolCall,
		fmt.Sprintf("product=%q · scope=%s (%d origin terms) · %d port filter(s) · limit=%d", activity, region, len(regionCountries), len(ports), tradeRecordPageLimit),
		"trade-db", "query-records")
	records, err := client.QueryRecords(tctx, models.TradeQuery{Product: activity, ForeignPorts: ports, Limit: tradeRecordPageLimit})
	if err != nil {
		emitAgentEvent(eventCh, models.EventToolResult,
			fmt.Sprintf("query failed: %v", err), "trade-db", "query-records")
		return nil, []string{fmt.Sprintf("Trade-record suppliers unavailable; the run continues on web lanes: %v", err)}
	}
	emitAgentEvent(eventCh, models.EventToolResult,
		fmt.Sprintf("%d record row(s) returned for product %q across %d port filter(s) (window: the %d most recent declaration(s))", len(records), activity, len(ports), tradeRecordPageLimit),
		"trade-db", "query-records")
	companies, warnings := aggregateTradeSuppliers(records, activity, region, regionCountries, limit)
	if windowWarning := tradeSupplierWindowWarning(activity, len(records)); windowWarning != "" {
		warnings = append(warnings, windowWarning)
	}
	if len(records) == 0 {
		// The zero-rows case needs its own word: the region warning below
		// reads as a geography miss, but a product name the DB cannot match
		// ("n-butanol" against "N BUTANOL (NBA)") empties the query before
		// the origin filter ever sees a row.
		warnings = append(warnings, fmt.Sprintf("The record query returned 0 rows for product %q — the DB matches names without punctuation (\"n-butanol\" matches nothing; \"n butanol\" matches), so a canonical product name from the trade-product matcher is what makes this lane return rows.", activity))
	}
	if len(ports) == 0 {
		warnings = append(warnings, fmt.Sprintf("Trade-record supplier query was not port-filtered: the DB's port vocabulary does not name %s's ports, so origin countries were matched client-side (%s).", region, strings.Join(regionCountries, ", ")))
	}
	return companies, warnings
}

// tradeSupplierWindowWarning explains the recency window when the record page
// came back full: the aggregation then ranks the head of the recent
// declarations, not the whole history, and the report must say so.
func tradeSupplierWindowWarning(activity string, rows int) string {
	if rows < tradeRecordPageLimit {
		return ""
	}
	return fmt.Sprintf("Trade-record suppliers: ranked by value within the %d most recent declarations of product %q at the region's ports; older shipments are outside that window, so a supplier that has not shipped recently can be missing.", tradeRecordPageLimit, activity)
}

// aggregateTradeSuppliers folds one page of records into ranked supplier rows,
// capped at the report-wide partner budget. When a region was requested, records
// whose origin country does not belong to the region are dropped — origin_country
// is the authoritative geography, the port vocabulary only narrowed the query.
// The region is expanded into its member countries before matching, because the
// DB carries origin countries, not region labels.
func aggregateTradeSuppliers(records []models.TradeRecord, activity, region string, regionCountries []string, limit int) ([]*models.MarketLandscapeCompany, []string) {
	if limit <= 0 {
		limit = defaultTradeDBPartnerLimit
	}
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
		if region != "" && !tools.RegionMatchesCountries(region, origin, regionCountries) {
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
	if len(names) > limit {
		names = names[:limit]
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
	if len(companies) == 0 && region != "" {
		warnings = append(warnings, fmt.Sprintf("Trade-record suppliers: no shipment record's origin country belonged to %s (%s). The DB stores foreign ports and origin countries, not region labels, so the region was expanded before matching; a wider scope or a country-scoped run returns rows.", region, strings.Join(regionCountries, ", ")))
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
	limit := a.engine.TradeDBPartnerLimit()
	// Same discipline as the seller run: the product name resolves first and
	// the resolution (match, miss, or operator pick) lands in the console log.
	product := resolveTradeDBProduct(ctx, client, query.Activity, query.TradeProduct, eventCh)
	companies, warnings := collectTradeImporters(ctx, client, product, limit, query.Countries, query.Regions, eventCh)
	// The cap is enforced here as well as in the request: the API's rank limit
	// is advisory, and the report budget is a hard promise.
	if len(companies) > limit {
		companies = companies[:limit]
	}
	report.Warnings = append(report.Warnings, warnings...)
	for _, company := range companies {
		report.TradeImporters = append(report.TradeImporters, *company)
	}
}

// attachTradePartners populates the seller report's first-party sections when
// the run opted in. The two collections run concurrently — the DB's ranking
// queries are slow, and each is bounded by the client's own semaphore and the
// collection timeout. The importer section needs a scope that contains India;
// the supplier section needs a single origin scope (a named region is expanded to
// its member countries) and falls back to the unscoped ranking for global or
// multi-region runs. Each section is capped at the report-wide partner budget.
func (a *MarketLandscapeAgent) attachTradePartners(ctx context.Context, query models.MarketLandscapeQuery, report *models.MarketLandscapeReport, eventCh chan<- models.AgentEvent) {
	if !query.IncludeTradeRecords {
		return
	}
	client := a.engine.TradeDB()
	if client == nil {
		report.Warnings = append(report.Warnings, "Trade records were requested, but the trade DB API is not configured on this deployment.")
		return
	}
	limit := a.engine.TradeDBPartnerLimit()
	// The product name resolves first — this is the run's first trade-DB
	// touch. The vocabulary match is what stands between the operator's
	// "n-butanol" and the DB's "N BUTANOL (NBA)"; every outcome is emitted
	// so the console log shows the exact string the customs queries carry.
	product := resolveTradeDBProduct(ctx, client, query.Activity, query.TradeProduct, eventCh)
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
		importers, importerWarn = collectTradeImporters(ctx, client, product, limit, query.Countries, query.Regions, eventCh)
	}()
	go func() {
		defer wg.Done()
		suppliers, supplierWarn = collectTradeSuppliers(ctx, client, product, limit, query.Countries, query.Regions, eventCh)
	}()
	wg.Wait()
	report.Warnings = append(report.Warnings, importerWarn...)
	report.Warnings = append(report.Warnings, supplierWarn...)
	// The cap is enforced here as well as in the requests: the API's rank
	// limit is advisory, and the report budget is a hard promise. It is a
	// REPORT-wide budget: the importer section takes the head and the supplier
	// section keeps what remains, so the sections never exceed it together.
	importers, suppliers, droppedSuppliers := trimTradeSections(importers, suppliers, limit)
	if droppedSuppliers > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf(
			"Trade-record sections share a report-wide cap of %d name(s): the importer section takes the head (%d), so the supplier section keeps %d of its %d ranked name(s). Raise TRADE_DB_MAX_PARTNERS (max 40) for a longer supplier head.",
			limit, len(importers), len(suppliers), len(suppliers)+droppedSuppliers))
	}
	for _, company := range importers {
		report.TradeImporters = append(report.TradeImporters, *company)
	}
	for _, company := range suppliers {
		report.TradeSuppliers = append(report.TradeSuppliers, *company)
	}
}

// trimTradeSections enforces the report-wide trade-DB name cap: the importer
// section takes the head of the budget and the supplier section keeps what
// remains, so the two sections never exceed the configured cap together. It
// returns the trimmed sections and how many supplier names were left out.
func trimTradeSections(importers, suppliers []*models.MarketLandscapeCompany, limit int) ([]*models.MarketLandscapeCompany, []*models.MarketLandscapeCompany, int) {
	if limit <= 0 {
		limit = defaultTradeDBPartnerLimit
	}
	if len(importers) > limit {
		importers = importers[:limit]
	}
	remaining := limit - len(importers)
	if remaining < 0 {
		remaining = 0
	}
	droppedSuppliers := 0
	if len(suppliers) > remaining {
		droppedSuppliers = len(suppliers) - remaining
		suppliers = suppliers[:remaining]
	}
	return importers, suppliers, droppedSuppliers
}

// tradePartnerCountry names where the row's entity sits: Indian importers are
// in India; suppliers carry their origin country when one was resolved.
func tradePartnerCountry(partner models.TradePartner) string {
	if partner.Direction == models.TradeDirectionImporter {
		return "India"
	}
	return partner.CountryOfOrigin
}
