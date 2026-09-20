package main

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
	"github.com/amitgupta1000/WebSearch_GO/internal/config"
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
