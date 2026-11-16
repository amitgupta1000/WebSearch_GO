package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// tradeDBServer is a scripted stand-in for the Crystal API. Routes carry canned
// answers; hits count per path so tests can assert how often the client calls.
type tradeDBServer struct {
	*httptest.Server
	mu     sync.Mutex
	hits   map[string]int
	bodies map[string][]byte
	routes map[string]func(w http.ResponseWriter, r *http.Request)
}

func newTradeDBServer(t *testing.T) *tradeDBServer {
	t.Helper()
	s := &tradeDBServer{
		hits:   make(map[string]int),
		bodies: make(map[string][]byte),
		routes: make(map[string]func(w http.ResponseWriter, r *http.Request)),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		body := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(body)
		}
		s.mu.Lock()
		s.hits[key]++
		if len(body) > 0 {
			s.bodies[key] = body
		}
		handler := s.routes[key]
		s.mu.Unlock()
		if handler == nil {
			http.Error(w, "unrouted: "+key, http.StatusNotFound)
			return
		}
		handler(w, r)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *tradeDBServer) hitCount(method, path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[method+" "+path]
}

func (s *tradeDBServer) body(method, path string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies[method+" "+path]
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode canned response: %v", err)
	}
}

// TestTradeDBUnconfiguredClientIsDisabled pins the capability-off contract: a
// nil or empty-URL client reports ErrTradeDBDisabled from every method and
// never touches the network.
func TestTradeDBUnconfiguredClientIsDisabled(t *testing.T) {
	server := newTradeDBServer(t)
	for _, client := range []*TradeDBClient{nil, NewTradeDBClient("", time.Second, 2)} {
		if client.Enabled() {
			t.Fatal("an unconfigured client must report disabled")
		}
		if _, err := client.Health(context.Background()); !errors.Is(err, ErrTradeDBDisabled) {
			t.Errorf("Health err = %v, want ErrTradeDBDisabled", err)
		}
		if _, err := client.FilterOptions(context.Background()); !errors.Is(err, ErrTradeDBDisabled) {
			t.Errorf("FilterOptions err = %v, want ErrTradeDBDisabled", err)
		}
		if _, err := client.QueryRecords(context.Background(), models.TradeQuery{Product: "x"}); !errors.Is(err, ErrTradeDBDisabled) {
			t.Errorf("QueryRecords err = %v, want ErrTradeDBDisabled", err)
		}
		if _, err := client.TopImporters(context.Background(), "x", nil, 5); !errors.Is(err, ErrTradeDBDisabled) {
			t.Errorf("TopImporters err = %v, want ErrTradeDBDisabled", err)
		}
	}
	if hits := server.hitCount(http.MethodGet, "/api/health"); hits != 0 {
		t.Errorf("disabled client hit /api/health %d time(s)", hits)
	}
}

// TestTradeDBHealthReadsConnectionState covers the capability probe: a healthy
// answer parses, and a degraded one still parses with the disconnected
// database named, because the operator-facing summary needs both shapes.
func TestTradeDBHealthReadsConnectionState(t *testing.T) {
	server := newTradeDBServer(t)
	server.routes[http.MethodGet+" /api/health"] = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, models.TradeHealth{Status: "ok", Database: "connected", ViewExists: true, RowCount: 42})
	}
	client := NewTradeDBClient(server.URL, 5*time.Second, 2)
	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.Status != "ok" || health.Database != "connected" || !health.ViewExists || health.RowCount != 42 {
		t.Fatalf("health = %+v, want connected/ok/42", health)
	}

	server.routes[http.MethodGet+" /api/health"] = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, models.TradeHealth{
			Status: "degraded", Database: "disconnected", ViewExists: false,
			Detail: "Database connection is not ready. Update the DB password in the admin panel and retry.",
		})
	}
	health, err = client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health on degraded API: %v", err)
	}
	if health.Database != "disconnected" || health.ViewExists {
		t.Fatalf("degraded health = %+v, want disconnected without a view", health)
	}
}

// TestTradeDBTopImportersNormalizesRows covers the ranked-importers path: the
// API's importer rows must arrive as importer-direction partners with the
// importer's own rank, and the request must carry the product and rank limit.
func TestTradeDBTopImportersNormalizesRows(t *testing.T) {
	server := newTradeDBServer(t)
	server.routes[http.MethodPost+" /api/analytics/top-importers"] = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{
				{"standardized_chemical_name": "CAUSTIC SODA", "true_importer_name": "Acme Chemicals Ltd", "transaction_count": 12, "total_value_usd": 150000.5, "total_quantity_mt": 300.25, "importer_rank": 1},
				{"standardized_chemical_name": "CAUSTIC SODA", "true_importer_name": "Acme Chemicals Ltd", "transaction_count": 12, "total_value_usd": 150000.5, "total_quantity_mt": 300.25, "importer_rank": 1},
				{"standardized_chemical_name": "CAUSTIC SODA", "true_importer_name": "", "transaction_count": 1, "importer_rank": 2},
			},
			"total_count":       3,
			"execution_time_ms": 5.5,
		})
	}
	client := NewTradeDBClient(server.URL, 5*time.Second, 2)
	partners, err := client.TopImporters(context.Background(), "CAUSTIC SODA", nil, 5)
	if err != nil {
		t.Fatalf("TopImporters: %v", err)
	}
	if len(partners) != 1 {
		t.Fatalf("partners = %d rows, want the single named importer", len(partners))
	}
	partner := partners[0]
	if partner.Name != "Acme Chemicals Ltd" || partner.Direction != models.TradeDirectionImporter {
		t.Fatalf("partner = %+v, want the named importer direction", partner)
	}
	if partner.TransactionCount != 12 || partner.TotalValueUSD != 150000.5 || partner.TotalQuantityMT != 300.25 || partner.Rank != 1 {
		t.Fatalf("partner stats = %+v, want the ranked shipment aggregates", partner)
	}

	var request struct {
		StandardizedChemicalName string   `json:"standardized_chemical_name"`
		HSCodes                  []string `json:"hs_codes"`
		RankLimit                int      `json:"rank_limit"`
	}
	if err := json.Unmarshal(server.body(http.MethodPost, "/api/analytics/top-importers"), &request); err != nil {
		t.Fatalf("decode recorded request: %v", err)
	}
	if request.StandardizedChemicalName != "CAUSTIC SODA" || request.RankLimit != 5 {
		t.Fatalf("request = %+v, want the product and the rank limit", request)
	}
}

// TestTradeDBTopSuppliersNormalizeRows covers the supplier direction: the name
// comes from true_supplier_name and the rank from supplier_rank.
func TestTradeDBTopSuppliersNormalizeRows(t *testing.T) {
	server := newTradeDBServer(t)
	server.routes[http.MethodPost+" /api/analytics/top-suppliers"] = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{
				{"standardized_chemical_name": "CAUSTIC SODA", "true_supplier_name": "BASF SE", "transaction_count": 7, "total_value_usd": 90000, "total_quantity_mt": 210, "supplier_rank": 3},
			},
		})
	}
	client := NewTradeDBClient(server.URL, 5*time.Second, 2)
	partners, err := client.TopSuppliers(context.Background(), "CAUSTIC SODA", nil, 5)
	if err != nil {
		t.Fatalf("TopSuppliers: %v", err)
	}
	if len(partners) != 1 || partners[0].Name != "BASF SE" || partners[0].Direction != models.TradeDirectionSupplier || partners[0].Rank != 3 {
		t.Fatalf("partners = %+v, want the named supplier at rank 3", partners)
	}
}

// TestTradeDBTopPartnersRequireProductOrHS pins the guard against empty

// TestTradeDBQueryRecordsRequestAndRows covers the raw-record path: the
// request carries the filter set (with the page default applied), and record
// rows decode through the API's own field names.
func TestTradeDBQueryRecordsRequestAndRows(t *testing.T) {
	server := newTradeDBServer(t)
	server.routes[http.MethodPost+" /api/query"] = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{
				{
					"system_id": "row-1", "reg_date": "2026-05-14", "hs_code": "28251100",
					"unique_product_name": "CAUSTIC SODA LYE 48%", "standardized_chemical_name": "CAUSTIC SODA",
					"quantity": 25.5, "unit_quantity": "MTS", "total_value_usd_exchange_rate": 6100.75,
					"true_importer_name": "Acme Chemicals Ltd", "true_supplier_name": "BASF SE",
					"type": "House Consumption Shipment", "indian_port": "NHAVA SHEVA JN", "foreign_port": "ANTWERP BELGIUM",
					"origin_country": "BELGIUM",
				},
			},
			"total_rows": 1, "returned_rows": 1, "has_more": false,
		})
	}
	client := NewTradeDBClient(server.URL, 5*time.Second, 2)
	records, err := client.QueryRecords(context.Background(), models.TradeQuery{
		Product: "caustic soda", HSCodes: []string{"28251100"}, Limit: 50,
	})
	if err != nil {
		t.Fatalf("QueryRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d rows, want one", len(records))
	}
	record := records[0]
	if record.Importer != "Acme Chemicals Ltd" || record.Supplier != "BASF SE" || record.OriginCountry != "BELGIUM" {
		t.Fatalf("record parties = %+v, want both names and the origin country", record)
	}
	if record.HSCode != "28251100" || record.StandardizedName != "CAUSTIC SODA" || record.TotalValueUSD != 6100.75 {
		t.Fatalf("record fields = %+v, want the decoded row", record)
	}

	var request struct {
		ProductSearch string   `json:"product_search"`
		HSCodes       []string `json:"hs_codes"`
		ForeignPorts  []string `json:"foreign_ports"`
		Limit         int      `json:"limit"`
		SortBy        string   `json:"sort_by"`
		SortOrder     string   `json:"sort_order"`
	}
	if err := json.Unmarshal(server.body(http.MethodPost, "/api/query"), &request); err != nil {
		t.Fatalf("decode recorded request: %v", err)
	}
	if request.ProductSearch != "caustic soda" || len(request.HSCodes) != 1 || request.Limit != 50 {
		t.Fatalf("request = %+v, want the filters as sent", request)
	}
	if request.SortBy != "reg_date" || request.SortOrder != "desc" {
		t.Fatalf("request sort = %s %s, want reg_date desc", request.SortBy, request.SortOrder)
	}

	if _, err := client.QueryRecords(context.Background(), models.TradeQuery{}); err != nil {
		t.Fatalf("QueryRecords with defaults: %v", err)
	}
	var defaultRequest struct {
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(server.body(http.MethodPost, "/api/query"), &defaultRequest); err != nil {
		t.Fatalf("decode default request: %v", err)
	}
	if defaultRequest.Limit != 200 {
		t.Fatalf("default page limit = %d, want 200", defaultRequest.Limit)
	}
}

// TestTradeDBEntityNamesCoversBothDirections covers the typeahead path used to
// match a company's trade identity by name.
func TestTradeDBEntityNamesCoversBothDirections(t *testing.T) {
	server := newTradeDBServer(t)
	server.routes[http.MethodGet+" /api/entity/importers/names"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != "acme" || r.URL.Query().Get("limit") != "5" {
			http.Error(w, "unexpected query params", http.StatusBadRequest)
			return
		}
		writeJSON(t, w, map[string]any{"entity_type": "importer", "names": []string{"Acme Chemicals Ltd", "Acme Industries"}, "total_count": 2})
	}
	client := NewTradeDBClient(server.URL, 5*time.Second, 2)
	names, err := client.EntityNames(context.Background(), models.TradeDirectionImporter, "acme", 5)
	if err != nil {
		t.Fatalf("EntityNames: %v", err)
	}
	if len(names) != 2 || names[0] != "Acme Chemicals Ltd" {
		t.Fatalf("names = %v, want the typeahead suggestions", names)
	}

	server.routes[http.MethodGet+" /api/entity/suppliers/names"] = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"entity_type": "supplier", "names": []string{"BASF SE"}})
	}
	if names, err = client.EntityNames(context.Background(), models.TradeDirectionSupplier, "basf", 5); err != nil {
		t.Fatalf("EntityNames supplier: %v", err)
	}
	if len(names) != 1 || names[0] != "BASF SE" {
		t.Fatalf("supplier names = %v, want the suggestion", names)
	}

	if names, err = client.EntityNames(context.Background(), models.TradeDirectionImporter, "   ", 5); err != nil || names != nil {
		t.Fatalf("blank EntityNames query = %v, %v; want empty without a call", names, err)
	}
	if hits := server.hitCount(http.MethodGet, "/api/entity/importers/names"); hits != 1 {
		t.Errorf("blank query hit the API %d extra time(s)", hits-1)
	}
}

// TestTradeDBDisconnectedDatabaseFailsOnceWithoutRetry pins the fail-soft
// contract for the outage the live API was caught in: the disconnected-
// database answer is stable, so the client must not retry it and must surface
// a dedicated unavailable error the trade lanes can warn on once.
func TestTradeDBDisconnectedDatabaseFailsOnceWithoutRetry(t *testing.T) {
	server := newTradeDBServer(t)
	server.routes[http.MethodPost+" /api/query"] = func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "503: Database connection is not ready. Update the DB password in the admin panel and retry.", http.StatusInternalServerError)
	}
	client := NewTradeDBClient(server.URL, 5*time.Second, 2)
	_, err := client.QueryRecords(context.Background(), models.TradeQuery{Product: "caustic soda"})
	var unavailable *TradeUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("err = %v, want a TradeUnavailableError", err)
	}
	if hits := server.hitCount(http.MethodPost, "/api/query"); hits != 1 {
		t.Errorf("disconnected database was hit %d time(s), want exactly one attempt", hits)
	}
}

// TestTradeDBTransientFailureRetriesOnce covers the blip path: one 503 that
// clears on the retry succeeds, with exactly two requests spent.
func TestTradeDBTransientFailureRetriesOnce(t *testing.T) {
	server := newTradeDBServer(t)
	calls := 0
	server.routes[http.MethodGet+" /api/health"] = func(w http.ResponseWriter, r *http.Request) {
		server.mu.Lock()
		calls++
		failing := calls == 1
		server.mu.Unlock()
		if failing {
			http.Error(w, "upstream timeout", http.StatusBadGateway)
			return
		}
		writeJSON(t, w, models.TradeHealth{Status: "ok", Database: "connected", ViewExists: true})
	}
	client := NewTradeDBClient(server.URL, 5*time.Second, 2)
	if _, err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health after transient failure: %v", err)
	}
	if server.hitCount(http.MethodGet, "/api/health") != 2 {
		t.Errorf("health hits = %d, want one retry", server.hitCount(http.MethodGet, "/api/health"))
	}
}

// TestTradeDBConcurrencyBounded proves the semaphore holds: with a two-slot
// client, overlapping calls never exceed two in flight even when the caller
// fans out wider.
func TestTradeDBConcurrencyBounded(t *testing.T) {
	server := newTradeDBServer(t)
	var mu sync.Mutex
	inFlight, maxInFlight := 0, 0
	server.routes[http.MethodGet+" /api/health"] = func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()
		time.Sleep(60 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		writeJSON(t, w, models.TradeHealth{Status: "ok", Database: "connected", ViewExists: true})
	}
	client := NewTradeDBClient(server.URL, 10*time.Second, 2)
	if client.Concurrency() != 2 {
		t.Fatalf("Concurrency() = %d, want the configured 2", client.Concurrency())
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.Health(context.Background()); err != nil {
				t.Errorf("Health: %v", err)
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	peak := maxInFlight
	mu.Unlock()
	if peak > 2 {
		t.Errorf("peak in-flight calls = %d, want at most the configured 2", peak)
	}
	if peak < 2 {
		t.Errorf("peak in-flight calls = %d, want the two slots to actually overlap", peak)
	}
}

// TestResolveForeignPortsMatchesRegionTokens covers the origin-scoping helper:
// a region maps to the ports whose name carries that country token, wherever
// the token sits in the port entry, and a region with no ports stays empty.
func TestResolveForeignPortsMatchesRegionTokens(t *testing.T) {
	ports := []string{"ANTWERP BELGIUM", "SHANGHAI CHINA", "JEBEL ALI, AE", "ROTTERDAM NLD", "JEBEL ALI UAE"}
	if got := ResolveForeignPorts(ports, "China"); len(got) != 1 || got[0] != "SHANGHAI CHINA" {
		t.Errorf("ResolveForeignPorts(China) = %v, want the Chinese port", got)
	}
	if got := ResolveForeignPorts(ports, "united arab emirates"); len(got) != 1 || got[0] != "JEBEL ALI UAE" {
		t.Errorf("ResolveForeignPorts(UAE) = %v, want the spelled-out entry", got)
	}
	if got := ResolveForeignPorts(ports, "UAE"); len(got) != 1 || got[0] != "JEBEL ALI UAE" {
		t.Errorf("ResolveForeignPorts(UAE shorthand) = %v, want the spelled-out entry", got)
	}
	if got := ResolveForeignPorts(ports, "Belgium"); len(got) != 1 || got[0] != "ANTWERP BELGIUM" {
		t.Errorf("ResolveForeignPorts(Belgium) = %v, want the Antwerp entry", got)
	}
	if got := ResolveForeignPorts(ports, "Brazil"); len(got) != 0 {
		t.Errorf("ResolveForeignPorts(Brazil) = %v, want no ports", got)
	}
	if got := ResolveForeignPorts(ports, ""); got != nil {
		t.Errorf("ResolveForeignPorts(\"\") = %v, want no ports", got)
	}
}

// TestTradeDBTopPartnersRequireProductOrHS pins the guard against empty
// ranking queries: with no product and no HS code the client fails fast.
func TestTradeDBTopPartnersRequireProductOrHS(t *testing.T) {
	server := newTradeDBServer(t)
	client := NewTradeDBClient(server.URL, 5*time.Second, 2)
	if _, err := client.TopImporters(context.Background(), "  ", nil, 5); err == nil {
		t.Fatal("an empty ranking request must fail without a call")
	}
	if hits := server.hitCount(http.MethodPost, "/api/analytics/top-importers"); hits != 0 {
		t.Errorf("empty ranking request hit the API %d time(s)", hits)
	}
}
