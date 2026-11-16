package models

// Trade directions. The Crystal import-export DB is Indian customs data: an
// importer row names the Indian buyer of the product, and a supplier row names
// the foreign exporter that shipped it into India. Importer records are only
// valid for India-scoped research; supplier records span every origin country
// the foreign port names, subject to an origin filter.
const (
	// TradeDirectionImporter marks a customs record's Indian buyer.
	TradeDirectionImporter = "importer"
	// TradeDirectionSupplier marks a customs record's foreign exporter.
	TradeDirectionSupplier = "supplier"
)

// TradePartner is one company surfaced from the Crystal import-export DB with
// the shipment statistics that rank it for a product. It is an observation of
// customs records, not a verified company identity on its own — how it promotes
// and which report section it lands in is decided by the agent layer.
type TradePartner struct {
	// Name is the cleaned entity name the record carries (the DB's
	// true_importer_name or true_supplier_name column).
	Name string `json:"name"`
	// Direction is the partner's side of the transaction: importer or supplier.
	Direction string `json:"direction"`
	// Product is the standardized chemical name the shipments were filed under.
	Product string `json:"product,omitempty"`
	// HSCode is the customs code the shipments were filed under.
	HSCode string `json:"hs_code,omitempty"`
	// CountryOfOrigin is the supplier's country. It is recorded on the record
	// and derivable from the foreign port; it scopes supplier records to a
	// region and is empty for importer rows (those are Indian by definition).
	CountryOfOrigin string `json:"country_of_origin,omitempty"`
	// Port is the foreign port the shipments moved through.
	Port string `json:"port,omitempty"`
	// TransactionCount, TotalValueUSD and TotalQuantityMT are the shipment
	// aggregates that rank the partner for the product.
	TransactionCount int     `json:"transaction_count"`
	TotalValueUSD    float64 `json:"total_value_usd"`
	TotalQuantityMT  float64 `json:"total_quantity_mt"`
	// Rank is the partner's position in the product ranking (1 = top).
	Rank int `json:"rank,omitempty"`
}

// TradeRecord mirrors one cleaned customs record from the Crystal query view.
// Field names follow the API's JSON contract exactly so a record round-trips
// without renaming.
type TradeRecord struct {
	SystemID         string  `json:"system_id,omitempty"`
	RegDate          string  `json:"reg_date,omitempty"`
	HSCode           string  `json:"hs_code,omitempty"`
	Chapter          string  `json:"chapter,omitempty"`
	Description      string  `json:"product_description,omitempty"`
	Product          string  `json:"unique_product_name,omitempty"`
	Quantity         float64 `json:"quantity,omitempty"`
	UnitQuantity     string  `json:"unit_quantity,omitempty"`
	UnitPriceUSD     float64 `json:"unit_price_usd,omitempty"`
	TotalValueUSD    float64 `json:"total_value_usd_exchange_rate,omitempty"`
	ImporterID       string  `json:"importer_id,omitempty"`
	Importer         string  `json:"true_importer_name,omitempty"`
	City             string  `json:"city,omitempty"`
	Type             string  `json:"type,omitempty"`
	Supplier         string  `json:"true_supplier_name,omitempty"`
	IndianPort       string  `json:"indian_port,omitempty"`
	ForeignPort      string  `json:"foreign_port,omitempty"`
	OriginCountry    string  `json:"origin_country,omitempty"`
	StandardizedName string  `json:"standardized_chemical_name,omitempty"`
}

// TradeQuery is the bounded filter set sent to the DB's /api/query endpoint.
// Only fields the trade lanes need are exposed; the DB validates the rest.
type TradeQuery struct {
	// Product is a fuzzy product name (the DB's standardized chemical name).
	Product string `json:"product,omitempty"`
	// HSCodes pins the search to customs codes when the product is known by code.
	HSCodes []string `json:"hs_codes,omitempty"`
	// ForeignPorts restricts supplier records to the ports of one origin region.
	ForeignPorts []string `json:"foreign_ports,omitempty"`
	// Limit and Offset bound the page. The DB accepts up to 10000 rows.
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

// TradeHealth is the DB's /api/health answer. Database names the connection
// state ("connected" or "disconnected"); a degraded status means the API is
// serving but cannot reach its database.
type TradeHealth struct {
	Status     string `json:"status"`
	Database   string `json:"database"`
	ViewExists bool   `json:"view_exists"`
	RowCount   int    `json:"row_count,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// TradeFilterOptions is the subset of /api/filters/options the client consumes:
// the DB's own port and transaction-type vocabulary plus the canonical chemical
// names the pipeline standardizes products to. The canonical names are loaded
// from mapping files at API startup, so they are served even when the database
// is disconnected.
type TradeFilterOptions struct {
	TransactionTypes      []string `json:"transaction_types"`
	IndianPorts           []string `json:"indian_ports"`
	ForeignPorts          []string `json:"foreign_ports"`
	CanonicalProductNames []string `json:"canonical_product_names"`
}
