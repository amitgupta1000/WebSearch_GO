package tools

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestTradeDBLiveSmoke exercises the client against the deployed Crystal API.
// It spends no budget and changes nothing: one health read, one filter-options
// read, one small record query and one ranked-importer query. Enable with:
//
//	TRADE_DB_INTEGRATION=1 TRADE_DB_API_URL=<base url> go test ./internal/tools/ -run TestTradeDBLiveSmoke -v
//
// The API serves its canonical product vocabulary from startup mapping files
// even while its database is disconnected, so this test asserts that shape and
// accepts either a connected or a disconnected database for the data paths —
// the disconnected answer must arrive as a TradeUnavailableError, never as a
// decode or transport failure.
func TestTradeDBLiveSmoke(t *testing.T) {
	if os.Getenv("TRADE_DB_INTEGRATION") != "1" {
		t.Skip("TRADE_DB_INTEGRATION=1 not set; skipping live trade-DB smoke")
	}
	baseURL := os.Getenv("TRADE_DB_API_URL")
	if baseURL == "" {
		t.Skip("TRADE_DB_API_URL not set; skipping live trade-DB smoke")
	}
	client := NewTradeDBClient(baseURL, 90*time.Second, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	health, err := client.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	t.Logf("health: status=%q database=%q view_exists=%t row_count=%d detail=%q",
		health.Status, health.Database, health.ViewExists, health.RowCount, health.Detail)
	if health.Database == "" {
		t.Errorf("health database state is empty: %+v", health)
	}

	options, err := client.FilterOptions(ctx)
	if err != nil {
		t.Fatalf("FilterOptions: %v", err)
	}
	t.Logf("filters: transaction_types=%d indian_ports=%d foreign_ports=%d canonical_products=%d",
		len(options.TransactionTypes), len(options.IndianPorts), len(options.ForeignPorts), len(options.CanonicalProductNames))
	if len(options.CanonicalProductNames) == 0 {
		t.Errorf("canonical product vocabulary is empty — the API serves it from mapping files even when its DB is down")
	}

	product := "CAUSTIC SODA"
	records, err := client.QueryRecords(ctx, models.TradeQuery{Product: product, Limit: 50})
	switch {
	case err == nil:
		ports := map[string]string{}
		for _, record := range records {
			if record.ForeignPort != "" && record.OriginCountry != "" {
				ports[record.ForeignPort+"\x00"+record.OriginCountry] = record.OriginCountry
			}
		}
		t.Logf("query: %d record rows returned, %d distinct foreign_port/origin_country pairs", len(records), len(ports))
		for pair := range ports {
			t.Logf("harvest pair: %s", strings.ReplaceAll(pair, "\x00", " -> "))
		}
		if len(records) > 0 && len(ports) == 0 {
			t.Errorf("record rows carry no foreign_port/origin_country pairs — the origin scoping has nothing to read")
		}
	case errors.Is(err, ErrTradeDBDisabled):
		t.Fatalf("query reported a disabled client against a configured URL: %v", err)
	default:
		logLiveDegradation(t, "query", err)
	}

	partners, err := client.TopImporters(ctx, product, nil, 5)
	switch {
	case err == nil:
		t.Logf("top importers: %d ranked rows after dedupe", len(partners))
		for _, partner := range partners {
			if partner.Name == "" {
				t.Errorf("ranked importer row carries an empty name: %+v", partner)
			}
		}
	default:
		logLiveDegradation(t, "top importers", err)
	}

	suppliers, err := client.TopSuppliers(ctx, product, nil, 5)
	switch {
	case err == nil:
		t.Logf("top suppliers: %d ranked rows after dedupe", len(suppliers))
	default:
		logLiveDegradation(t, "top suppliers", err)
	}
}

// logLiveDegradation accepts the two live behaviors the API exhibits under
// load: the stable disconnected-database answer, and transient 5xx answers
// from the heavy ranking queries (a live probe watched /api/analytics answers
// swing between 503 in ~20s and 200 in ~45s). Both are logged, not failed —
// the fail-soft contract means the trade lanes continue on web search — while
// any other error class still fails the test because the client cannot have
// produced it from a healthy contract.
func logLiveDegradation(t *testing.T, path string, err error) {
	t.Helper()
	var unavailable *TradeUnavailableError
	if errors.As(err, &unavailable) {
		t.Logf("%s degraded as designed (database disconnected): %v", path, unavailable)
		return
	}
	var retriable *retriableTradeError
	if errors.As(err, &retriable) {
		t.Logf("%s hit a transient API failure under load (logged, fail-soft): %v", path, retriable)
		return
	}
	t.Errorf("%s failed with an unexpected error class: %v", path, err)
}
