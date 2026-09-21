package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
	"github.com/amitgupta1000/WebSearch_GO/internal/config"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
	"github.com/amitgupta1000/WebSearch_GO/internal/web"
)

func TestHandleEventsTerminatesAfterFailedSession(t *testing.T) {
	templates, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	hub := agent.NewSessionHub()
	session := hub.Create("failed-session", "company", "Acme")
	session.Complete(nil, errors.New("company search failed"))

	server := &Server{
		cfg:  &config.Config{},
		hub:  hub,
		tmpl: templates,
	}
	request := httptest.NewRequest("GET", "/api/events?session_id=failed-session", nil)
	response := httptest.NewRecorder()
	server.handleEvents(response, request)

	body := response.Body.String()
	if !strings.Contains(body, "company search failed") {
		t.Fatalf("expected failure event in SSE response, got %q", body)
	}
	if !strings.Contains(body, "event: close") {
		t.Fatalf("expected close event in SSE response, got %q", body)
	}
}

func TestRequestedBrowserOverrideParsing(t *testing.T) {
	form := func(value string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/research", strings.NewReader("use_browser="+value))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	for _, value := range []string{"1", "true", "TRUE", "on", "yes"} {
		if enabled, explicit := requestedBrowserOverride(form(value)); !enabled || !explicit {
			t.Errorf("use_browser=%q should opt in, got enabled=%t explicit=%t", value, enabled, explicit)
		}
	}
	for _, value := range []string{"0", "false", "off", "no"} {
		if enabled, explicit := requestedBrowserOverride(form(value)); enabled || !explicit {
			t.Errorf("use_browser=%q should opt out explicitly, got enabled=%t explicit=%t", value, enabled, explicit)
		}
	}
	if _, explicit := requestedBrowserOverride(form("")); explicit {
		t.Error("an absent choice must leave the deployment default in place")
	}
}

func TestBrowserChoiceHonoursPolicyAndAvailability(t *testing.T) {
	request := func(value string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/rerun", strings.NewReader("use_browser="+value))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}

	// Toggle disabled by the operator: the client value is ignored entirely.
	disabled := &Server{allowBrowserToggle: false, browserAvailable: true}
	if enabled, explicit, err := disabled.browserChoice(request("1")); err != nil || enabled || explicit {
		t.Fatalf("a disabled toggle must ignore client input, got enabled=%t explicit=%t err=%v", enabled, explicit, err)
	}

	// Chromium missing: asking for it is an explicit error, never a silent downgrade.
	unavailable := &Server{allowBrowserToggle: true, browserAvailable: false}
	if _, _, err := unavailable.browserChoice(request("1")); err == nil {
		t.Fatal("expected an error when browser rendering is requested but unavailable")
	}
	if enabled, explicit, err := unavailable.browserChoice(request("0")); err != nil || enabled || !explicit {
		t.Fatalf("an explicit opt-out must still be accepted, got enabled=%t explicit=%t err=%v", enabled, explicit, err)
	}

	// Available: both directions are honoured.
	available := &Server{allowBrowserToggle: true, browserAvailable: true}
	if enabled, explicit, err := available.browserChoice(request("1")); err != nil || !enabled || !explicit {
		t.Fatalf("expected an explicit opt-in, got enabled=%t explicit=%t err=%v", enabled, explicit, err)
	}
	if enabled, explicit, err := available.browserChoice(request("0")); err != nil || enabled || !explicit {
		t.Fatalf("expected an explicit opt-out, got enabled=%t explicit=%t err=%v", enabled, explicit, err)
	}
	if _, explicit, err := available.browserChoice(request("")); err != nil || explicit {
		t.Fatalf("absent choice must not override the default, got explicit=%t err=%v", explicit, err)
	}
}

func TestHandleRerunWithoutSessionRendersError(t *testing.T) {
	templates, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	server := &Server{
		cfg:                &config.Config{},
		hub:                agent.NewSessionHub(),
		tmpl:               templates,
		allowBrowserToggle: true,
	}

	// httptest only parses the body as a form when the content type says so.
	post := func(body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/rerun", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}

	response := httptest.NewRecorder()
	server.handleRerun(response, post(""))
	if !strings.Contains(response.Body.String(), "research session is required") {
		t.Fatalf("expected a session-required error, got %q", response.Body.String())
	}

	// An unknown session must not start a run.
	response = httptest.NewRecorder()
	server.handleRerun(response, post("session_id=sess_missing"))
	if !strings.Contains(response.Body.String(), "no longer available") {
		t.Fatalf("expected an expired-session error, got %q", response.Body.String())
	}
}

func TestWithBrowserOverrideAttachesChoiceToContext(t *testing.T) {
	base := context.Background()
	if _, ok := tools.BrowserFallbackOverride(base); ok {
		t.Error("a default run must leave the scraper's configured setting in place")
	}
	if enabled, ok := tools.BrowserFallbackOverride(withBrowserOverride(base, true, true)); !ok || !enabled {
		t.Error("expected the opt-in to reach the scraper context")
	}
	if enabled, ok := tools.BrowserFallbackOverride(withBrowserOverride(base, false, true)); !ok || enabled {
		t.Error("expected the opt-out to reach the scraper context")
	}
	if _, ok := tools.BrowserFallbackOverride(withBrowserOverride(base, true, false)); ok {
		t.Error("a non-explicit choice must not override the deployment default")
	}
}

func TestSecurityHeadersAllowRemoteImages(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "img-src 'self' data: https:") {
		t.Errorf("the scraped-image gallery needs remote https images to render, got %q", csp)
	}
	// Relaxing img-src must not loosen anything else.
	if strings.Contains(csp, "img-src *") || strings.Contains(csp, "http:") {
		t.Errorf("img-src must stay scoped to https, got %q", csp)
	}
	for _, directive := range []string{
		"default-src 'self'",
		"style-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com",
		"connect-src 'self'",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("expected %q to remain unchanged, got %q", directive, csp)
		}
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("expected nosniff to still be set, got %q", got)
	}
	if got := response.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("referrer policy limits image tracking and must stay, got %q", got)
	}
}
