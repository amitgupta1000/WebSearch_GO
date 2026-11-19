package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/config"
)

// TestCrystalExploreRedirectsOnlyWithThePassword pins the gate: the configured
// password answers with a redirect into the Firebase app, and nothing else does.
func TestCrystalExploreRedirectsOnlyWithThePassword(t *testing.T) {
	server := &Server{cfg: &config.Config{
		CrystalDBURL:      "https://crystal-importanalyst.web.app/",
		CrystalDBPassword: "Crystal@12345",
	}}

	unlocked := httptest.NewRecorder()
	server.handleCrystalExplore(unlocked, postForm("/api/trade/explore", "password=Crystal%4012345"))
	if unlocked.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 into the app", unlocked.Code)
	}
	if got := unlocked.Header().Get("Location"); got != "https://crystal-importanalyst.web.app/" {
		t.Errorf("location = %q, want the Firebase app", got)
	}

	locked := httptest.NewRecorder()
	server.handleCrystalExplore(locked, postForm("/api/trade/explore", "password=wrong"))
	if locked.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", locked.Code)
	}
	if locked.Header().Get("Location") != "" {
		t.Error("a wrong password must not redirect")
	}
}

// TestCrystalExploreErrorDoesNotRevealTheURL keeps the link server-side: the
// retry page names the app but never its address.
func TestCrystalExploreErrorDoesNotRevealTheURL(t *testing.T) {
	server := &Server{cfg: &config.Config{
		CrystalDBURL:      "https://crystal-importanalyst.web.app/",
		CrystalDBPassword: "Crystal@12345",
	}}
	response := httptest.NewRecorder()
	server.handleCrystalExplore(response, postForm("/api/trade/explore", "password=wrong"))
	body := response.Body.String()
	if strings.Contains(body, "crystal-importanalyst.web.app") {
		t.Error("the retry page must not reveal the app URL")
	}
	if !strings.Contains(body, "does not open") || !strings.Contains(body, `action="/api/trade/explore"`) {
		t.Errorf("retry page = %s, want the message and the retry form", body)
	}
}

// TestCrystalExploreExplainsAnUnconfiguredURL keeps the degrade path honest:
// with no URL configured there is nothing to unlock, and the answer says so.
func TestCrystalExploreExplainsAnUnconfiguredURL(t *testing.T) {
	for _, server := range []*Server{
		{},
		{cfg: &config.Config{CrystalDBPassword: "Crystal@12345"}},
	} {
		response := httptest.NewRecorder()
		server.handleCrystalExplore(response, postForm("/api/trade/explore", "password=Crystal%4012345"))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", response.Code)
		}
		if !strings.Contains(response.Body.String(), "not configured") {
			t.Errorf("body = %s, want the unconfigured note", response.Body.String())
		}
	}
}
