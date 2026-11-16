package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tests for the RocketReach client's fail-soft transport behaviour and the
// diagnosability of its errors. They live in their own file because they
// exercise the retry and egress policy added after the live RR discovery
// outages, not the original request/response contract.

// TestRocketReachSearchRetriesATransportCut pins the fail-soft behaviour the
// live service needed: the search endpoint was observed cutting connections
// mid-response ("unexpected EOF") when the shared egress proxy flaked, so a
// transport cut gets exactly one retry before the error surfaces.
func TestRocketReachSearchRetriesATransportCut(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			// ErrAbortHandler drops the connection mid-response, which the
			// client observes as an unexpected EOF — the live failure shape.
			panic(http.ErrAbortHandler)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"profiles":[{"id":7,"name":"Asha Rao","current_title":"Head of Procurement"}],"pagination":{"start":1,"next":0,"total":1}}`)
	}))
	defer server.Close()
	client := NewRocketReachClient("test-key")
	client.searchURL = server.URL
	profiles, err := client.Search(context.Background(), map[string][]string{"current_employer": {"Acme"}})
	if err != nil {
		t.Fatalf("Search after a transport cut: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Name != "Asha Rao" {
		t.Fatalf("profiles = %+v, want the retried search result", profiles)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want exactly one retry", calls)
	}
}

// TestRocketReachStableRejectionsDoNotRetry keeps the quota honest: a 429 with
// a long Retry-After is the provider closing the quota window, so the error
// surfaces after a single call instead of a pointless retry.
func TestRocketReachStableRejectionsDoNotRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()
	client := NewRocketReachClient("test-key")
	client.searchURL = server.URL
	_, err := client.Search(context.Background(), map[string][]string{"current_employer": {"Acme"}})
	var httpErr *RocketReachHTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusTooManyRequests || httpErr.RetryAfter != "60" {
		t.Fatalf("error = %v, want the 429 with its Retry-After", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want no retry on a long rate-limit window", calls)
	}
}

// TestRocketReachErrorCarriesProviderDetail covers diagnosability: the
// provider's own explanation (quota exhausted, invalid facet, plan limit)
// travels on the error instead of being discarded with the response body.
func TestRocketReachErrorCarriesProviderDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"invalid facet years_experience"}`, http.StatusBadRequest)
	}))
	defer server.Close()
	client := NewRocketReachClient("test-key")
	client.searchURL = server.URL
	_, err := client.Search(context.Background(), map[string][]string{"current_employer": {"Acme"}})
	var httpErr *RocketReachHTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("error = %v, want the 400 as RocketReachHTTPError", err)
	}
	if !strings.Contains(httpErr.Detail, "invalid facet years_experience") {
		t.Errorf("detail = %q, want the provider's explanation", httpErr.Detail)
	}
	if !strings.Contains(err.Error(), "invalid facet years_experience") {
		t.Errorf("error text = %v, want the detail rendered", err)
	}
}

// TestRocketReachClientUsesDirectEgress pins the egress decision: the RR API is
// an authenticated JSON endpoint with no IP-reputation gate, and routing it
// through the shared HTTPS_PROXY produced the intermittent connection cuts, so
// the client's transport must pin Proxy to nil.
func TestRocketReachClientUsesDirectEgress(t *testing.T) {
	client := NewRocketReachClient("test-key")
	transport, ok := client.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.client.Transport)
	}
	if transport.Proxy != nil {
		t.Error("transport proxy is set; RR traffic must bypass the shared HTTPS_PROXY")
	}
	if client.client.Timeout != rocketReachTimeout {
		t.Errorf("timeout = %v, want %v", client.client.Timeout, rocketReachTimeout)
	}
}
