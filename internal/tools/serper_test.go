package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSerperSearchParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-KEY"); got != "test-key" {
			t.Errorf("X-API-KEY header = %q, want %q", got, "test-key")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"organic":[{"title":"T1","link":"https://a.example","snippet":"S1","position":1,"date":"2025-01-01"}]}`)
	}))
	defer srv.Close()

	s := NewSerperClient("test-key")
	s.baseURL = srv.URL

	results, err := s.Search(context.Background(), "acme", 5)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Title != "T1" || results[0].Link != "https://a.example" || results[0].Position != 1 {
		t.Errorf("unexpected result: %+v", results[0])
	}
}

func TestSerperSearchRetriesTransientErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"temporary failure"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"organic":[]}`)
	}))
	defer srv.Close()

	s := NewSerperClient("test-key")
	s.baseURL = srv.URL

	results, err := s.Search(context.Background(), "acme", 5)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty result set, got %d", len(results))
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("expected 3 attempts, got %d", got)
	}
}

func TestSerperSearchIncludesErrorBodyInError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Invalid API key"}`))
	}))
	defer srv.Close()

	s := NewSerperClient("test-key")
	s.baseURL = srv.URL

	_, err := s.Search(context.Background(), "acme", 5)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("error should include status and response body, got: %v", err)
	}
}

func TestSerperSearchRequiresAPIKey(t *testing.T) {
	s := NewSerperClient("")
	if _, err := s.Search(context.Background(), "acme", 5); err == nil {
		t.Fatal("expected error when API key is missing")
	}
}
