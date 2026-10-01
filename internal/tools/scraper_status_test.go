package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestIsBotDenialStatusNamesTheRefusal keeps the classification narrow: only the non-standard
// code a host uses to refuse a client is treated as a refusal. 403 is a page a render may
// still win, and 429 is a rate limit that should keep its backoff.
func TestIsBotDenialStatusNamesTheRefusal(t *testing.T) {
	cases := []struct {
		status int
		want   bool
	}{
		{botDenialStatus, true},
		{http.StatusForbidden, false},
		{http.StatusTooManyRequests, false},
		{http.StatusInternalServerError, false},
		{http.StatusOK, false},
	}
	for _, testCase := range cases {
		if got := isBotDenialStatus(testCase.status); got != testCase.want {
			t.Errorf("isBotDenialStatus(%d) = %t, want %t", testCase.status, got, testCase.want)
		}
	}
}

// TestScrapeURLDoesNotRetryABotDenial is the behaviour the live profile probe exposed: HTTP
// 999 was being retried three times ("scraping failed after 3 attempts: status code: 999"),
// which is three requests against a host that has already refused us. One request, and a
// reason that names the refusal.
func TestScrapeURLDoesNotRetryABotDenial(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(botDenialStatus)
		_, _ = w.Write([]byte("<html><body>refused</body></html>"))
	}))
	defer srv.Close()

	s := NewScraper(nil, time.Second, WithPrivateHostAccess())
	_, err := s.ScrapeURL(context.Background(), srv.URL+"/in/someone")
	if err == nil {
		t.Fatal("expected the refusal to surface as an error")
	}
	if !strings.Contains(err.Error(), "999") {
		t.Errorf("expected the reason to name the refusal, got %v", err)
	}
	if strings.Contains(err.Error(), "attempts") {
		t.Errorf("a refusal must not be retried, got %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("expected exactly one request, got %d", got)
	}
}
