package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// tradeDBMock is the scripted stand-in for the Crystal client: calls are
// counted so the opt-in and geography gates can be asserted without a live API.
type tradeDBMock struct {
	mu                sync.Mutex
	topImportersCalls int
	topSuppliersCalls int
	filterCalls       int
	queryCalls        int
	lastQuery         models.TradeQuery
	partners          []models.TradePartner
	suppliers         []models.TradePartner
	options           *models.TradeFilterOptions
	records           []models.TradeRecord
	importersErr      error
}

func (m *tradeDBMock) TopImporters(ctx context.Context, product string, hsCodes []string, rankLimit int) ([]models.TradePartner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.topImportersCalls++
	return m.partners, m.importersErr
}

func (m *tradeDBMock) TopSuppliers(ctx context.Context, product string, hsCodes []string, rankLimit int) ([]models.TradePartner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.topSuppliersCalls++
	return m.suppliers, nil
}

func (m *tradeDBMock) FilterOptions(ctx context.Context) (*models.TradeFilterOptions, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.filterCalls++
	return m.options, nil
}

func (m *tradeDBMock) QueryRecords(ctx context.Context, query models.TradeQuery) ([]models.TradeRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queryCalls++
	m.lastQuery = query
	return m.records, nil
}

// TestTradeDBImporterScope pins the geography rule: importer records are
// Indian importers by definition, so only an India-scoped run may query them,
// and the token test must not be fooled by "West Indies".
func TestTradeDBImporterScope(t *testing.T) {
	cases := []struct {
		name      string
		countries []string
		regions   []string
		want      bool
	}{
		{"india country", []string{"India"}, nil, true},
		{"india region", nil, []string{"india"}, true},
		{"upper case", []string{"INDIA"}, nil, true},
		{"west indies", []string{"West Indies"}, nil, false},
		{"germany", []string{"Germany"}, nil, false},
		{"empty", nil, nil, false},
	}
	for _, tc := range cases {
		if got := tradeDBImporterScope(tc.countries, tc.regions); got != tc.want {
			t.Errorf("%s: tradeDBImporterScope = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestTradeSellerRegionScoping pins the supplier-side geography: exactly one
// non-India region scopes the origin filter; India runs and multi-region runs
// do not get a supplier scope.
func TestTradeSellerRegionScoping(t *testing.T) {
	if got := tradeSellerRegion([]string{"Germany"}, nil); got != "Germany" {
		t.Errorf("single country = %q, want Germany", got)
	}
	if got := tradeSellerRegion(nil, []string{"Europe"}); got != "Europe" {
		t.Errorf("single region = %q, want Europe", got)
	}
	if got := tradeSellerRegion([]string{"India"}, nil); got != "" {
		t.Errorf("India run = %q, want no supplier scope", got)
	}
	if got := tradeSellerRegion([]string{"Germany", "France"}, nil); got != "" {
		t.Errorf("two countries = %q, want no supplier scope", got)
	}
	if got := tradeSellerRegion(nil, nil); got != "" {
		t.Errorf("global run = %q, want no supplier scope", got)
	}
}

// TestTradePartnerCompanyCarriesTheDualRole covers the row mapping: the
// first-party record is the identity, the importer role travels with any
// reseller signal the name carries, and the custody provenance is auditable.
func TestTradePartnerCompanyCarriesTheDualRole(t *testing.T) {
	importer := tradePartnerCompany(models.TradePartner{
		Name: "Chemform Trading Private Limited", Direction: models.TradeDirectionImporter,
		Product: "CAUSTIC SODA", TransactionCount: 4, TotalValueUSD: 4757978, TotalQuantityMT: 9593, Rank: 4,
	}, "caustic soda")
	if importer.Role != models.MarketRoleImporter {
		t.Errorf("role = %q, want importer", importer.Role)
	}
	hasTag := func(tags []string, want string) bool {
		for _, tag := range tags {
			if tag == want {
				return true
			}
		}
		return false
	}
	if !hasTag(importer.RoleTags, models.MarketRoleImporter) || !hasTag(importer.RoleTags, models.MarketRoleTrader) {
		t.Errorf("role tags = %v, want importer plus the trader reseller signal", importer.RoleTags)
	}
	if importer.Country != "India" || importer.Presence != models.MarketPresenceLocal || !importer.InScope {
		t.Errorf("importer geography = %s/%s/%v, want India, local, in scope", importer.Country, importer.Presence, importer.InScope)
	}
	if importer.Status != "verified" || importer.Trade == nil {
		t.Fatalf("row = %+v, want verified status and the first-party record", importer)
	}
	evidence := importer.Evidence[0]
	if evidence.SourceTier != TradeSourceCustody || evidence.ClaimType != models.DemandClaimImport {
		t.Errorf("evidence tier/claim = %s/%s, want custody/import-record", evidence.SourceTier, evidence.ClaimType)
	}
	if !isCustodyDomain(hostname(evidence.URL)) {
		t.Errorf("evidence URL %q does not carry the trade-DB host", evidence.URL)
	}

	supplier := tradePartnerCompany(models.TradePartner{
		Name: "BASF SE", Direction: models.TradeDirectionSupplier,
		CountryOfOrigin: "GERMANY", TransactionCount: 2, Rank: 3,
	}, "caustic soda")
	if supplier.Country != "GERMANY" || supplier.PresenceCountry != "GERMANY" {
		t.Errorf("supplier geography = %s/%s, want the origin country", supplier.Country, supplier.PresenceCountry)
	}
	if !hasTag(supplier.RoleTags, "exporter") {
		t.Errorf("supplier role tags = %v, want the exporter signal", supplier.RoleTags)
	}
	if got := tradePartnerClaimType(models.TradePartner{Direction: models.TradeDirectionSupplier}); got != "supply-record" {
		t.Errorf("supplier claim type = %q, want supply-record", got)
	}
}

// TestDemandStatusCustodyRecordVerifiesOnItsOwn is the promotion policy the
// operator chose: a first-party customs record names the actual importer, so
// it owns identity and establishes the transaction — it verifies alone, while
// the public trade sites keep their lead-only rule.
func TestDemandStatusCustodyRecordVerifiesOnItsOwn(t *testing.T) {
	custodyOnly := &models.MarketLandscapeCompany{Name: "Acme Chemicals", Evidence: []models.MarketLandscapeEvidence{{
		URL: "crystal://trade-db.crystal.internal/importer/acme-chemicals", SourceTier: TradeSourceCustody,
		ClaimType: models.DemandClaimImport,
	}}}
	if status := demandStatus(custodyOnly); status != "verified" {
		t.Errorf("custody-only status = %q, want verified", status)
	}

	// The public trade tier keeps its corroboration-only rule: a Volza page
	// alone is a lead, exactly as before the trade DB existed.
	tradeOnly := &models.MarketLandscapeCompany{Name: "Acme Chemicals", Evidence: []models.MarketLandscapeEvidence{{
		URL: "https://www.volza.com/p/acme-chemicals/import-data", SourceTier: "trade",
		ClaimType: models.DemandClaimImport,
	}}}
	if status := demandStatus(tradeOnly); status != "lead" {
		t.Errorf("public-trade-only status = %q, want lead", status)
	}
}

// demandAttachFixture builds a demand agent around a mock trade client.
func demandAttachFixture(mock TradeDBSource) (*DemandChainAgent, chan models.AgentEvent) {
	engine := NewEngine(nil, nil, nil, cache.NewMemoryCache(16), 1, 1, 1, WithTradeDB(mock))
	return NewDemandChainAgent(engine), make(chan models.AgentEvent, 32)
}

// TestAttachTradeImportersHonoursOptInAndGeography pins the wiring gates: the
// DB is queried only for an opted-in India-scoped run, a non-India run is
// skipped with the reason, and a run without the opt-in never touches it.
func TestAttachTradeImportersHonoursOptInAndGeography(t *testing.T) {
	mock := &tradeDBMock{partners: []models.TradePartner{{
		Name: "Persistent Petrochem Private Limited", Direction: models.TradeDirectionImporter,
		Product: "CAUSTIC SODA", TransactionCount: 13, TotalValueUSD: 8728876, TotalQuantityMT: 18681, Rank: 1,
	}}}
	agent, eventCh := demandAttachFixture(mock)
	ctx := context.Background()

	// Opt-in plus India: the section is populated and the DB was queried once.
	report := &models.DemandChainReport{}
	agent.attachTradeImporters(ctx, models.DemandChainQuery{
		Activity: "caustic soda", Countries: []string{"India"}, IncludeTradeRecords: true,
	}, report, eventCh)
	if len(report.TradeImporters) != 1 || report.TradeImporters[0].Name != "Persistent Petrochem Private Limited" {
		t.Fatalf("trade importers = %+v, want the ranked importer", report.TradeImporters)
	}
	mock.mu.Lock()
	calls := mock.topImportersCalls
	mock.mu.Unlock()
	if calls != 1 {
		t.Errorf("TopImporters calls = %d, want exactly one", calls)
	}

	// Opt-in without India: the geography gate skips the query and says why.
	skipped := &models.DemandChainReport{}
	agent.attachTradeImporters(ctx, models.DemandChainQuery{
		Activity: "caustic soda", Countries: []string{"Germany"}, IncludeTradeRecords: true,
	}, skipped, eventCh)
	if len(skipped.TradeImporters) != 0 {
		t.Errorf("non-India run populated %d trade importer(s)", len(skipped.TradeImporters))
	}
	mock.mu.Lock()
	callsAfterSkip := mock.topImportersCalls
	mock.mu.Unlock()
	if callsAfterSkip != 1 {
		t.Errorf("non-India run queried the DB %d extra time(s)", callsAfterSkip-1)
	}
	if len(skipped.Warnings) != 1 || !strings.Contains(skipped.Warnings[0], "India-scoped") {
		t.Errorf("warnings = %v, want the India-scope explanation", skipped.Warnings)
	}

	// No opt-in: the lane never runs and adds no noise.
	quiet := &models.DemandChainReport{}
	agent.attachTradeImporters(ctx, models.DemandChainQuery{
		Activity: "caustic soda", Countries: []string{"India"},
	}, quiet, eventCh)
	if len(quiet.TradeImporters) != 0 || len(quiet.Warnings) != 0 {
		t.Errorf("quiet run produced section %v or warnings %v", quiet.TradeImporters, quiet.Warnings)
	}

	// Opt-in without the capability configured: one honest warning.
	bareEngine := NewEngine(nil, nil, nil, cache.NewMemoryCache(16), 1, 1, 1)
	bareAgent := NewDemandChainAgent(bareEngine)
	unconfigured := &models.DemandChainReport{}
	bareAgent.attachTradeImporters(ctx, models.DemandChainQuery{
		Activity: "caustic soda", Countries: []string{"India"}, IncludeTradeRecords: true,
	}, unconfigured, eventCh)
	if len(unconfigured.Warnings) != 1 || !strings.Contains(unconfigured.Warnings[0], "not configured") {
		t.Errorf("warnings = %v, want the capability-off warning", unconfigured.Warnings)
	}
}

// TestAttachTradePartnersScopesSuppliersByOrigin pins the seller-side rule:
// a single-region run resolves the region's foreign ports from the DB's own
// vocabulary, queries the records, and drops every row whose origin country
// does not match — origin_country is authoritative, the ports only narrow.
func TestAttachTradePartnersScopesSuppliersByOrigin(t *testing.T) {
	mock := &tradeDBMock{
		options: &models.TradeFilterOptions{ForeignPorts: []string{"HAMBURG GERMANY", "SHANGHAI CHINA"}},
		records: []models.TradeRecord{
			{Supplier: "BASF SE", OriginCountry: "GERMANY", TotalValueUSD: 1000, Quantity: 10},
			{Supplier: "BASF SE", OriginCountry: "GERMANY", TotalValueUSD: 500, Quantity: 5},
			{Supplier: "Sinopec", OriginCountry: "CHINA", TotalValueUSD: 9000, Quantity: 90},
		},
	}
	engine := NewEngine(nil, nil, nil, cache.NewMemoryCache(16), 1, 1, 1, WithTradeDB(mock))
	agent := NewMarketLandscapeAgent(engine)
	eventCh := make(chan models.AgentEvent, 32)
	report := &models.MarketLandscapeReport{}
	agent.attachTradePartners(context.Background(), models.MarketLandscapeQuery{
		Activity: "caustic soda", Countries: []string{"Germany"}, IncludeTradeRecords: true,
	}, report, eventCh)

	if len(report.TradeSuppliers) != 1 {
		t.Fatalf("trade suppliers = %+v, want only the German supplier", report.TradeSuppliers)
	}
	supplier := report.TradeSuppliers[0]
	if supplier.Name != "BASF SE" || supplier.Country != "GERMANY" || supplier.Trade.TransactionCount != 2 {
		t.Fatalf("supplier row = %+v, want BASF SE with both shipments aggregated", supplier)
	}
	if supplier.Trade.TotalValueUSD != 1500 || supplier.Trade.TotalQuantityMT != 15 || supplier.Trade.Rank != 1 {
		t.Errorf("supplier stats = %+v, want the aggregated totals at rank 1", supplier.Trade)
	}
	if mock.lastQuery.ForeignPorts == nil || len(mock.lastQuery.ForeignPorts) != 1 || mock.lastQuery.ForeignPorts[0] != "HAMBURG GERMANY" {
		t.Errorf("record query ports = %v, want the German port resolved from the vocabulary", mock.lastQuery.ForeignPorts)
	}
	dropped := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "outside the requested origin region") {
			dropped = true
		}
	}
	if !dropped {
		t.Errorf("warnings = %v, want the off-origin drop count", report.Warnings)
	}
	// A Germany run is not India-scoped: the importer section stays empty and
	// says why, and the DB was never asked for importers.
	if len(report.TradeImporters) != 0 {
		t.Errorf("non-India seller run populated %d importer(s)", len(report.TradeImporters))
	}
	mock.mu.Lock()
	calls := mock.topImportersCalls
	mock.mu.Unlock()
	if calls != 0 {
		t.Errorf("non-India seller run queried importers %d time(s)", calls)
	}
}

// TestAttachTradePartnersGlobalSuppliersAreLabelled pins the unscoped path: a
// global run takes the ranked exporters and says so, because the ranking rows
// carry no origin country.
func TestAttachTradePartnersGlobalSuppliersAreLabelled(t *testing.T) {
	mock := &tradeDBMock{suppliers: []models.TradePartner{
		{Name: "Asrar Al", Direction: models.TradeDirectionSupplier, Product: "CAUSTIC SODA", TransactionCount: 23, TotalValueUSD: 16126488, TotalQuantityMT: 37016, Rank: 1},
	}}
	engine := NewEngine(nil, nil, nil, cache.NewMemoryCache(16), 1, 1, 1, WithTradeDB(mock))
	agent := NewMarketLandscapeAgent(engine)
	eventCh := make(chan models.AgentEvent, 32)
	report := &models.MarketLandscapeReport{}
	agent.attachTradePartners(context.Background(), models.MarketLandscapeQuery{
		Activity: "caustic soda", IncludeTradeRecords: true,
	}, report, eventCh)
	if len(report.TradeSuppliers) != 1 || report.TradeSuppliers[0].Name != "Asrar Al" {
		t.Fatalf("trade suppliers = %+v, want the ranked exporter", report.TradeSuppliers)
	}
	unscoped := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "unscoped") {
			unscoped = true
		}
	}
	if !unscoped {
		t.Errorf("warnings = %v, want the unscoped note", report.Warnings)
	}
}

// TestAttachTradeImportersUnavailableDegrades pins the fail-soft contract: a
// failing DB becomes a warning, never a failed run.
func TestAttachTradeImportersUnavailableDegrades(t *testing.T) {
	mock := &tradeDBMock{importersErr: errors.New("boom")}
	agent, eventCh := demandAttachFixture(mock)
	report := &models.DemandChainReport{}
	agent.attachTradeImporters(context.Background(), models.DemandChainQuery{
		Activity: "caustic soda", Countries: []string{"India"}, IncludeTradeRecords: true,
	}, report, eventCh)
	if len(report.TradeImporters) != 0 {
		t.Fatalf("failing DB populated %d importer(s)", len(report.TradeImporters))
	}
	if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "unavailable") {
		t.Errorf("warnings = %v, want the unavailability warning", report.Warnings)
	}
}
