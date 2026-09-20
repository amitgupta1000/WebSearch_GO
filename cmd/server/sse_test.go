package main

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
	"github.com/amitgupta1000/WebSearch_GO/internal/config"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/web"
)

func TestHandleEventsDoesNotReplayHistoryOnReconnect(t *testing.T) {
	templates, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	hub := agent.NewSessionHub()
	session := hub.Create("reconnect-session", "company", "Acme")
	session.EventCh <- models.AgentEvent{SessionID: session.ID, Type: models.EventThinking, Step: 1, Content: "one-time event"}
	session.Complete(nil, errors.New("done"))
	server := &Server{cfg: &config.Config{}, hub: hub, tmpl: templates}

	first := httptest.NewRecorder()
	server.handleEvents(first, httptest.NewRequest("GET", "/api/events?session_id=reconnect-session", nil))
	second := httptest.NewRecorder()
	server.handleEvents(second, httptest.NewRequest("GET", "/api/events?session_id=reconnect-session", nil))

	if strings.Contains(second.Body.String(), "one-time event") {
		t.Fatalf("reconnected SSE stream replayed prior history: %q", second.Body.String())
	}
	if !strings.Contains(first.Body.String(), "event: result") {
		t.Fatal("expected first connection to receive the terminal result")
	}
	if strings.Contains(second.Body.String(), "event: result") {
		t.Fatal("reconnected SSE stream replayed the terminal result")
	}
}
