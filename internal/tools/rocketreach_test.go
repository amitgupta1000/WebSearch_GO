package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestRocketReachLookupUsesExplicitIdentifiers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Api-Key") != "test-key" {
			t.Errorf("Api-Key = %q, want test-key", r.Header.Get("Api-Key"))
		}
		if got := r.URL.Query().Get("name"); got != "Jane Doe" {
			t.Errorf("name = %q, want Jane Doe", got)
		}
		if got := r.URL.Query().Get("current_employer"); got != "Acme" {
			t.Errorf("current_employer = %q, want Acme", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":123,"name":"Jane Doe","emails":[{"email":"jane@acme.test","type":"professional","grade":"A"}],"phones":[{"number":"+1-555-0100","type":"mobile"}]}`))
	}))
	defer server.Close()

	client := NewRocketReachClient("test-key")
	client.baseURL = server.URL
	profile, err := client.Lookup(context.Background(), "Jane Doe", "Acme", "")
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if profile == nil || len(profile.Emails) != 1 || len(profile.Phones) != 1 {
		t.Fatalf("profile = %+v, want one email and phone", profile)
	}
}

func TestRocketReachLookupPrefersLinkedInURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("linkedin_url") == "" || query.Get("name") != "" || query.Get("current_employer") != "" {
			t.Errorf("lookup query = %s, want only linkedin_url", url.Values(query).Encode())
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewRocketReachClient("test-key")
	client.baseURL = server.URL
	profile, err := client.Lookup(context.Background(), "Jane Doe", "Acme", "https://www.linkedin.com/in/jane-doe")
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if profile != nil {
		t.Fatalf("profile = %+v, want nil for 404", profile)
	}
}

func TestRocketReachSearchSendsStructuredDiscoveryQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Api-Key") != "test-key" {
			t.Errorf("request = %s, Api-Key=%q", r.Method, r.Header.Get("Api-Key"))
		}

		var body struct {
			Query map[string][]string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Query["current_employer"][0] != "Acme" || len(body.Query["current_title"]) != 2 {
			t.Fatalf("query = %#v", body.Query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"profiles":[{"id":7,"name":"Jane Doe","current_title":"Director","current_employer":"Acme"}]}`))
	}))
	defer server.Close()

	client := NewRocketReachClient("test-key")
	client.searchURL = server.URL
	profiles, err := client.Search(context.Background(), map[string][]string{
		"current_employer": {"Acme"},
		"current_title":    {"Director", "CFO"},
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(profiles) != 1 || profiles[0].ID != 7 {
		t.Fatalf("profiles = %+v", profiles)
	}
}

func TestRocketReachLookupDecodesStructuredProfileFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"linkedin_url":"https://www.linkedin.com/in/jane-doe",
			"location":"New Delhi",
			"job_history":[{"title":"Director","company":"Acme","start_date":"2020","end_date":"2023"}],
			"education":[{"school":"Example University","degree":"MBA","major":"Finance","year":"2019"}],
			"skills":["Strategy","Procurement"]
		}`))
	}))
	defer server.Close()

	client := NewRocketReachClient("test-key")
	client.baseURL = server.URL
	profile, err := client.Lookup(context.Background(), "Jane Doe", "Acme", "")
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if profile.Location != "New Delhi" || len(profile.JobHistory) != 1 ||
		len(profile.Education) != 1 || len(profile.Skills) != 2 {
		t.Fatalf("structured profile = %+v", profile)
	}
}
