package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

const (
	// tradeDBMaxAttempts bounds one logical call. The DB answers a disconnected
	// database with a stable message, so a second attempt only covers a blip.
	tradeDBMaxAttempts = 2
	// tradeDBRetryBackoff is the linear pause between attempts.
	tradeDBRetryBackoff = 500 * time.Millisecond
	// tradeDBMaxErrorBody caps how much of an error response is reported.
	tradeDBMaxErrorBody = 512
	// defaultTradeDBTimeout is one HTTP call's budget. The DB's own frontend
	// uses 60s for large queries, so the same ceiling keeps the client safe.
	defaultTradeDBTimeout = 60 * time.Second
	// defaultTradeDBConcurrency bounds simultaneous API calls. The DB is a
	// Postgres read surface behind a Cloud Run service whose workers handle
	// requests one at a time, so a small pool is what the API can actually
	// serve — more would only queue inside WebSearch_GO.
	defaultTradeDBConcurrency = 3
	// defaultTradeDBLimit caps one record page. The DB accepts up to 10000;
	// the trade lanes only need ranked names, so a small page is enough.
	defaultTradeDBLimit = 200
)

// ErrTradeDBDisabled reports that no trade DB API URL is configured. Callers
// treat it as "capability off" rather than a failure.
var ErrTradeDBDisabled = errors.New("trade DB API is not configured")

// TradeUnavailableError reports a live API whose database is disconnected.
// It is not retriable: the API answers with a stable message, and the caller's
// correct response is to warn once and fall back to web lanes.
type TradeUnavailableError struct{ Detail string }

func (e *TradeUnavailableError) Error() string {
	if e.Detail == "" {
		return "trade DB database is not reachable"
	}
	return "trade DB database is not reachable: " + e.Detail
}

// retriableTradeError marks transient failures (network errors, 429, and 5xx
// other than the disconnected-database answer) eligible for one retry.
type retriableTradeError struct{ err error }

func (e *retriableTradeError) Error() string { return e.err.Error() }
func (e *retriableTradeError) Unwrap() error { return e.err }

// TradeDBClient queries the Crystal import-export analytics API: Indian customs
// records cleaned and standardized by the pipeline, served through a FastAPI
// read layer. The client is fail-soft by design — every method returns its
// error to the caller, and the trade lanes downgrade to web-only research when
// the DB is unreachable.
type TradeDBClient struct {
	baseURL string
	client  *http.Client
	// sem bounds simultaneous API calls. It is a buffered channel — the same
	// primitive the scraper uses to bound concurrent browser renders — so the
	// trade lanes can fan queries out without ever exceeding the configured
	// concurrency against the DB.
	sem chan struct{}
}

// NewTradeDBClient creates a client for the trade DB API. A nil client (or one
// built from an empty URL) reports ErrTradeDBDisabled from every method, so a
// deployment without the capability needs no conditional wiring at call sites.
func NewTradeDBClient(baseURL string, timeout time.Duration, concurrency int) *TradeDBClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = defaultTradeDBTimeout
	}
	if concurrency <= 0 {
		concurrency = defaultTradeDBConcurrency
	}
	return &TradeDBClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
		sem:     make(chan struct{}, concurrency),
	}
}

// Enabled reports whether the client can serve queries.
func (t *TradeDBClient) Enabled() bool {
	return t != nil && t.baseURL != ""
}

// Concurrency reports the client's in-flight call bound, for run reporting.
func (t *TradeDBClient) Concurrency() int {
	if t == nil || t.sem == nil {
		return 0
	}
	return cap(t.sem)
}

// Health reports the DB's connection state. A reachable API with a
// disconnected database is an available capability that currently cannot serve
// data — callers log the detail and continue on web lanes.
func (t *TradeDBClient) Health(ctx context.Context) (models.TradeHealth, error) {
	var health models.TradeHealth
	if err := t.call(ctx, http.MethodGet, "/api/health", nil, &health); err != nil {
		return health, err
	}
	return health, nil
}

// FilterOptions returns the DB's port and transaction-type vocabulary and the
// canonical chemical names. The canonical names come from the API's startup
// mapping files and are served even while its database is disconnected.
func (t *TradeDBClient) FilterOptions(ctx context.Context) (*models.TradeFilterOptions, error) {
	var options models.TradeFilterOptions
	if err := t.call(ctx, http.MethodGet, "/api/filters/options", nil, &options); err != nil {
		return nil, err
	}
	return &options, nil
}

// TopImporters returns the ranked Indian importers of one standardized
// chemical, optionally pinned to HS codes. Importer records are Indian by
// definition, so the result is only valid for India-scoped research.
func (t *TradeDBClient) TopImporters(ctx context.Context, product string, hsCodes []string, rankLimit int) ([]models.TradePartner, error) {
	return t.topPartners(ctx, models.TradeDirectionImporter, product, hsCodes, rankLimit)
}

// TopSuppliers returns the ranked foreign exporters of one standardized
// chemical, optionally pinned to HS codes. Supplier records span every origin
// country; callers scope them with ResolveForeignPorts before treating the
// result as a regional seller list.
func (t *TradeDBClient) TopSuppliers(ctx context.Context, product string, hsCodes []string, rankLimit int) ([]models.TradePartner, error) {
	return t.topPartners(ctx, models.TradeDirectionSupplier, product, hsCodes, rankLimit)
}

// topPartners runs one /api/analytics ranking endpoint and normalizes its rows.
func (t *TradeDBClient) topPartners(ctx context.Context, direction, product string, hsCodes []string, rankLimit int) ([]models.TradePartner, error) {
	if !t.Enabled() {
		return nil, ErrTradeDBDisabled
	}
	product = strings.TrimSpace(product)
	if product == "" && len(hsCodes) == 0 {
		return nil, fmt.Errorf("trade DB %s ranking needs a product or HS code", direction)
	}
	if rankLimit <= 0 {
		rankLimit = 10
	}
	payload := struct {
		StandardizedChemicalName string   `json:"standardized_chemical_name,omitempty"`
		HSCodes                  []string `json:"hs_codes,omitempty"`
		RankLimit                int      `json:"rank_limit,omitempty"`
	}{
		StandardizedChemicalName: product,
		HSCodes:                  hsCodes,
		RankLimit:                rankLimit,
	}
	path := "/api/analytics/top-importers"
	if direction == models.TradeDirectionSupplier {
		path = "/api/analytics/top-suppliers"
	}
	var response struct {
		Data []struct {
			Product          string  `json:"standardized_chemical_name"`
			ImporterName     string  `json:"true_importer_name"`
			SupplierName     string  `json:"true_supplier_name"`
			TransactionCount int     `json:"transaction_count"`
			TotalValueUSD    float64 `json:"total_value_usd"`
			TotalQuantityMT  float64 `json:"total_quantity_mt"`
			ImporterRank     int     `json:"importer_rank"`
			SupplierRank     int     `json:"supplier_rank"`
		} `json:"data"`
	}
	if err := t.call(ctx, http.MethodPost, path, payload, &response); err != nil {
		return nil, err
	}
	partners := make([]models.TradePartner, 0, len(response.Data))
	// The ranking endpoints answer with duplicated rows (a live probe of the
	// caustic-soda ranking returned every partner twice under total_count
	// equal to the row count), so identical name+product rows collapse here.
	// The duplicates carry identical aggregates, so keeping the first is lossless.
	seen := make(map[string]bool, len(response.Data))
	for _, row := range response.Data {
		name := row.ImporterName
		rank := row.ImporterRank
		if direction == models.TradeDirectionSupplier {
			name = row.SupplierName
			rank = row.SupplierRank
		}
		if strings.TrimSpace(name) == "" {
			continue
		}
		key := name + "\x00" + row.Product
		if seen[key] {
			continue
		}
		seen[key] = true
		partners = append(partners, models.TradePartner{
			Name:             name,
			Direction:        direction,
			Product:          row.Product,
			TransactionCount: row.TransactionCount,
			TotalValueUSD:    row.TotalValueUSD,
			TotalQuantityMT:  row.TotalQuantityMT,
			Rank:             rank,
		})
	}
	return partners, nil
}

// QueryRecords returns raw customs records for a bounded filter set. Records
// carry the origin country and both ports, so the caller can scope supplier
// records to a region after the fetch even without a port filter.
func (t *TradeDBClient) QueryRecords(ctx context.Context, query models.TradeQuery) ([]models.TradeRecord, error) {
	if !t.Enabled() {
		return nil, ErrTradeDBDisabled
	}
	if query.Limit <= 0 {
		query.Limit = defaultTradeDBLimit
	}
	payload := struct {
		ProductSearch string   `json:"product_search,omitempty"`
		HSCodes       []string `json:"hs_codes,omitempty"`
		ForeignPorts  []string `json:"foreign_ports,omitempty"`
		Limit         int      `json:"limit,omitempty"`
		Offset        int      `json:"offset,omitempty"`
		SortBy        string   `json:"sort_by,omitempty"`
		SortOrder     string   `json:"sort_order,omitempty"`
	}{
		ProductSearch: strings.TrimSpace(query.Product),
		HSCodes:       query.HSCodes,
		ForeignPorts:  query.ForeignPorts,
		Limit:         query.Limit,
		Offset:        query.Offset,
		SortBy:        "reg_date",
		SortOrder:     "desc",
	}
	var response struct {
		Data []models.TradeRecord `json:"data"`
	}
	if err := t.call(ctx, http.MethodPost, "/api/query", payload, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

// EntityNames returns the DB's typeahead suggestions for importer or supplier
// names, used to match a company's trade identity by name.
func (t *TradeDBClient) EntityNames(ctx context.Context, direction, query string, limit int) ([]string, error) {
	if !t.Enabled() {
		return nil, ErrTradeDBDisabled
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	path := "/api/entity/importers/names"
	if direction == models.TradeDirectionSupplier {
		path = "/api/entity/suppliers/names"
	}
	if limit <= 0 {
		limit = 20
	}
	var response struct {
		Names []string `json:"names"`
	}
	if err := t.call(ctx, http.MethodGet, path+"?"+url.Values{
		"query": {query},
		"limit": {fmt.Sprintf("%d", limit)},
	}.Encode(), nil, &response); err != nil {
		return nil, err
	}
	return response.Names, nil
}

// call performs one bounded, retrying API request. The concurrency semaphore
// is held for the whole logical call, so a retry cannot exceed the in-flight
// cap either.
func (t *TradeDBClient) call(ctx context.Context, method, path string, payload any, target any) error {
	if !t.Enabled() {
		return ErrTradeDBDisabled
	}
	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}

	var encoded []byte
	if payload != nil {
		marshaled, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("trade DB payload encode failed: %w", err)
		}
		encoded = marshaled
	}

	var lastErr error
	for attempt := 1; attempt <= tradeDBMaxAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(tradeDBRetryBackoff * time.Duration(attempt-1)):
			}
		}
		var body io.Reader
		if encoded != nil {
			body = bytes.NewReader(encoded)
		}
		req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, body)
		if err != nil {
			return fmt.Errorf("trade DB request build failed: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := t.client.Do(req)
		if err != nil {
			lastErr = &retriableTradeError{fmt.Errorf("trade DB request failed: %w", err)}
			continue
		}
		if resp.StatusCode == http.StatusOK {
			if target == nil {
				resp.Body.Close()
				return nil
			}
			decodeErr := json.NewDecoder(resp.Body).Decode(target)
			resp.Body.Close()
			if decodeErr != nil {
				return fmt.Errorf("trade DB response decode failed: %w", decodeErr)
			}
			return nil
		}
		bodyText, _ := io.ReadAll(io.LimitReader(resp.Body, tradeDBMaxErrorBody))
		resp.Body.Close()
		lastErr = tradeStatusError(resp.StatusCode, strings.TrimSpace(string(bodyText)))
		var retriable *retriableTradeError
		if !errors.As(lastErr, &retriable) {
			return lastErr
		}
	}
	return lastErr
}

// tradeStatusError classifies a non-200 answer. The disconnected-database
// message is stable and non-retriable — restoring the connection is an
// operator action, and no amount of retrying changes that.
func tradeStatusError(status int, bodyText string) error {
	base := fmt.Errorf("trade DB API returned status code %d: %s", status, bodyText)
	if strings.Contains(strings.ToLower(bodyText), "database connection is not ready") {
		return &TradeUnavailableError{Detail: bodyText}
	}
	if status == http.StatusTooManyRequests || status >= 500 {
		return &retriableTradeError{base}
	}
	return base
}

// tradeRegionAliases expands well-known region shorthands into the words a
// port entry is likely to carry, and tradeRegionShorthands does the reverse:
// a full region name also matches the shorthand a port entry may use. The DB
// has no origin-country column — the port carries the country — so both
// spellings must meet in the middle. ISO-2-only port entries ("JEBEL ALI, AE")
// are deliberately not matched in v1: until the live port vocabulary is
// harvested, an ISO map would be guessing.
var tradeRegionAliases = map[string][]string{
	"uae":     {"united", "arab", "emirates"},
	"uk":      {"united", "kingdom"},
	"usa":     {"united", "states"},
	"us":      {"united", "states"},
	"ksa":     {"saudi", "arabia"},
	"turkiye": {"turkey"},
}

var tradeRegionShorthands = map[string][]string{
	"united arab emirates": {"uae"},
	"united kingdom":       {"uk"},
	"united states":        {"usa", "us"},
	"saudi arabia":         {"ksa"},
	"turkey":               {"turkiye"},
}

// ResolveForeignPorts maps one origin region onto the DB's foreign-port
// vocabulary by token match. The DB has no origin-country filter — country of
// origin is derivable from the port — so a regional seller query sends the
// ports of that region. An empty result means the vocabulary does not name the
// region's ports (or no vocabulary was loaded), and the caller should query
// without a port filter and scope records client-side by origin_country.
func ResolveForeignPorts(foreignPorts []string, region string) []string {
	return resolveForeignPortsByTokens(foreignPorts, tradeRegionTokens(region))
}

// ResolveForeignPortsForCountries maps a region plus its member countries onto
// the DB's foreign-port vocabulary. A region name is not a location in this DB:
// "Middle East" matches no port, while its member countries do ("JEBEL ALI, AE",
// "SAUDI ARABIA"). The caller expands the region from the operating geography
// vocabulary, so a region-scoped query sends the ports of the region's
// countries rather than the region's own label.
func ResolveForeignPortsForCountries(foreignPorts []string, region string, countries []string) []string {
	tokens := tradeRegionTokens(region)
	for _, country := range countries {
		tokens = append(tokens, tradeRegionTokens(country)...)
	}
	return resolveForeignPortsByTokens(foreignPorts, tokens)
}

func resolveForeignPortsByTokens(foreignPorts []string, tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	matched := make([]string, 0, len(foreignPorts))
	for _, port := range foreignPorts {
		portTokens := tradeNameTokens(port)
		if len(tradeSharedTokens(portTokens, tokens)) > 0 {
			matched = append(matched, port)
		}
	}
	return matched
}

// tradeRegionTokens lowercases a region into comparable tokens, adding the
// shorthand spellings a port entry may use (and expanding a shorthand region
// into its full words). Multi-word regions keep every word, so a port entry
// naming the country matches wherever the country sits in the string.
func tradeRegionTokens(region string) []string {
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return nil
	}
	tokens := tradeNameTokens(region)
	for _, token := range tokens {
		if expansion, ok := tradeRegionAliases[token]; ok {
			tokens = append(tokens, expansion...)
		}
	}
	if shorthand, ok := tradeRegionShorthands[region]; ok {
		tokens = append(tokens, shorthand...)
	}
	return tokens
}

// tradeNameTokens lowercases a name into comparable tokens. Multi-word regions
// ("united arab emirates") keep every word, so a port entry naming the country
// matches wherever the country sits in the string.
func tradeNameTokens(name string) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil
	}
	fields := strings.FieldsFunc(name, func(r rune) bool {
		isToken := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		return !isToken
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) > 1 {
			tokens = append(tokens, field)
		}
	}
	return tokens
}

// RegionMatches reports whether a country (or origin) name shares tokens with
// the region a seller query was scoped to, alias-expanding both sides. It is
// the agent-side counterpart of ResolveForeignPorts: the port vocabulary only
// narrows a query, while the record's origin_country is the authoritative
// geography.
func RegionMatches(region, country string) bool {
	region = strings.TrimSpace(region)
	country = strings.TrimSpace(country)
	if region == "" || country == "" {
		return false
	}
	return len(tradeSharedTokens(tradeNameTokens(country), tradeRegionTokens(region))) > 0
}

// RegionMatchesCountries reports whether an origin country belongs to the
// requested region: by the region's own tokens, or by membership in the region's
// expanded country list. The expansion is what makes "Middle East" work at all —
// the region name shares no token with "SAUDI ARABIA", but the member country
// does. Without it every record of a region-scoped run is filtered out.
func RegionMatchesCountries(region, country string, countries []string) bool {
	if RegionMatches(region, country) {
		return true
	}
	for _, member := range countries {
		if RegionMatches(member, country) {
			return true
		}
	}
	return false
}

// tradeSharedTokens returns the tokens two token sets share.
func tradeSharedTokens(a, b []string) []string {
	shared := make([]string, 0)
	seen := make(map[string]bool, len(b))
	for _, token := range b {
		seen[token] = true
	}
	for _, token := range a {
		if seen[token] {
			shared = append(shared, token)
		}
	}
	return shared
}
