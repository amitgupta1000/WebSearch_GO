package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
	"github.com/amitgupta1000/WebSearch_GO/internal/web"
)

// entityMatchStub answers only the typeahead call the matcher makes; the
// embedded nil interface covers the rest of the trade-DB surface.
type entityMatchStub struct {
	agent.TradeDBSource
	names []string
}

func (s entityMatchStub) EntityNames(context.Context, string, string, int) ([]string, error) {
	return s.names, nil
}

// TestTradeEntityMatchHandlerOffersTheStoredSpellings pins the pre-run matcher:
// an imperfect company name comes back as selectable levels of stored spelling
// instead of an empty customs profile after the run.
func TestTradeEntityMatchHandlerOffersTheStoredSpellings(t *testing.T) {
	templates, err := web.Templates()
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	engine := agent.NewEngine(nil, nil, nil, nil, 0, 0, 0,
		agent.WithTradeDB(entityMatchStub{names: []string{"KLJ RESOURCES AFRICA LTD", "KLJ RESOURCES KENYA LTD"}}))
	server := &Server{engine: engine, tmpl: templates}

	response := httptest.NewRecorder()
	server.handleTradeEntityMatch(response, postForm("/api/trade/entity-match", "query=KLJ+Resources"))
	body := response.Body.String()
	for _, expected := range []string{"KLJ RESOURCES AFRICA LTD", "KLJ RESOURCES KENYA LTD", "trade_entity", "trade_entity_direction"} {
		if !strings.Contains(body, expected) {
			t.Errorf("matcher answer is missing %q: %s", expected, body)
		}
	}
}

// TestTradeEntityMatchHandlerExplainsAnUnconfiguredDB keeps the degrade path
// honest: without the capability the matcher says so instead of an empty list.
func TestTradeEntityMatchHandlerExplainsAnUnconfiguredDB(t *testing.T) {
	templates, err := web.Templates()
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	server := &Server{tmpl: templates}
	response := httptest.NewRecorder()
	server.handleTradeEntityMatch(response, postForm("/api/trade/entity-match", "query=KLJ+Resources"))
	if !strings.Contains(response.Body.String(), "not configured") {
		t.Errorf("answer = %s, want the unconfigured-DB note", response.Body.String())
	}
}
