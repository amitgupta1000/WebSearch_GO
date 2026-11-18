package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestTradeDBEntityProfilesAndProducts pins the entity-profile family against
// a stub shaped like the live API: importer rows carry true_importer_name and
// supplier_count, supplier rows true_supplier_name and importer_count, the
// product breakdown answers for any name, and an unknown name answers empty
// rather than erroring.
func TestTradeDBEntityProfilesAndProducts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/entity/importers" && r.URL.Query().Get("name_exact") == "true":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"importers":[{"true_importer_name":"KLJ RESOURCES LTD","global_rank":1,"transaction_count":4692,"product_count":20,"supplier_count":0,"total_value_usd":1400347748,"total_quantity_mt":520000}],"total_count":1,"execution_time_ms":55}`))
		case r.URL.Path == "/api/entity/suppliers" && r.URL.Query().Get("name_search") == "SABIC":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"suppliers":[{"true_supplier_name":"SABIC","global_rank":1,"transaction_count":22119,"product_count":20,"importer_count":0,"total_value_usd":3819127618,"total_quantity_mt":900000}],"total_count":1,"execution_time_ms":55}`))
		case r.URL.Path == "/api/entity/importers/KLJ RESOURCES LTD/products":
			if got := r.URL.Query().Get("limit"); got != "5" {
				t.Errorf("products limit = %q, want 5", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"entity_name":"KLJ RESOURCES LTD","entity_type":"importer","products":[{"standardized_chemical_name":"TOLUENE","transaction_count":760,"total_value_usd":260442574,"total_quantity_mt":284962,"entity_rank":1},{"standardized_chemical_name":"ACETIC ACID","transaction_count":439,"total_value_usd":79173636,"total_quantity_mt":202558,"entity_rank":2}],"total_count":5,"execution_time_ms":24}`))
		case r.URL.Path == "/api/entity/importers/UNOBTAINIUM/products":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"entity_name":"UNOBTAINIUM","entity_type":"importer","products":[],"total_count":0,"execution_time_ms":2}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewTradeDBClient(server.URL, 10*time.Second, 2)
	ctx := context.Background()

	profiles, err := client.EntityProfiles(ctx, models.TradeDirectionImporter, "KLJ RESOURCES LTD", true, 5)
	if err != nil {
		t.Fatalf("EntityProfiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Name != "KLJ RESOURCES LTD" || profiles[0].GlobalRank != 1 || profiles[0].TransactionCount != 4692 {
		t.Fatalf("profiles = %+v, want the KLJ profile", profiles)
	}

	suppliers, err := client.EntityProfiles(ctx, models.TradeDirectionSupplier, "SABIC", false, 5)
	if err != nil {
		t.Fatalf("EntityProfiles(supplier): %v", err)
	}
	if len(suppliers) != 1 || suppliers[0].Name != "SABIC" || suppliers[0].CounterpartyCount != 0 {
		t.Fatalf("supplier profiles = %+v, want the SABIC row", suppliers)
	}

	breakdown, err := client.EntityProducts(ctx, models.TradeDirectionImporter, "KLJ RESOURCES LTD", 5)
	if err != nil {
		t.Fatalf("EntityProducts: %v", err)
	}
	if breakdown.EntityName != "KLJ RESOURCES LTD" || len(breakdown.Products) != 2 || breakdown.Products[0].Name != "TOLUENE" || breakdown.Products[0].Rank != 1 {
		t.Fatalf("breakdown = %+v, want the KLJ product mix", breakdown)
	}

	empty, err := client.EntityProducts(ctx, models.TradeDirectionImporter, "UNOBTAINIUM", 5)
	if err != nil {
		t.Fatalf("EntityProducts(unknown): %v", err)
	}
	if empty.TotalCount != 0 || len(empty.Products) != 0 {
		t.Errorf("unknown name must answer empty, got %+v", empty)
	}
}
