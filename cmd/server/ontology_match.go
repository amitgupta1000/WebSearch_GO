package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
)

// ontology_match.go serves the console's ontology confirmation picker: the same
// suggestion-first contract as the trade-product picker, applied to the value
// chain.
//
// The resolver reads the activity text, and the text is exactly what goes wrong
// ("crude palm oil" once resolved to crude oil through the generic alias
// "crude"). So the picker shows what the graph resolved, then the ranked nodes
// that could be meant -- match first, ranked variants next, closest names after
// that -- each with the product family it would drive. Confirming one anchors
// the run on that node, and the confirmation is remembered for the activity.


func (s *Server) handleOntologyMatch(w http.ResponseWriter, r *http.Request) {
	activity := strings.TrimSpace(firstNonEmpty(r.FormValue("query"), r.FormValue("activity")))
	if activity == "" {
		s.render(w, "ontology_match", ontologyMatchView{Note: "Type the activity above first - the picker ranks the graph's nodes against it."})
		return
	}
	mode := agent.NormalizeActivityMode(r.FormValue("mode"))
	geography := strings.TrimSpace(firstNonEmpty(r.FormValue("market_region_scope"), r.FormValue("demand_region_scope")))
	nodeID := strings.TrimSpace(firstNonEmpty(r.FormValue("ontology_node"), r.FormValue("market_ontology_node"), r.FormValue("demand_ontology_node")))
	ctx, cancel := context.WithTimeout(r.Context(), ontologyMatchBudget)
	defer cancel()
	resolution, err := agent.ResolveActivity(ctx, s.engine, agent.ActivityQuery{
		Activity: activity, Geography: geography, Mode: mode, NodeID: nodeID,
	})
	if err != nil {
		s.render(w, "ontology_match", ontologyMatchView{
			Query: activity,
			Note:  "The ontology could not be resolved (" + err.Error() + "); the run resolves the activity as typed.",
		})
		return
	}
	view := ontologyMatchView{
		Query: activity, Mode: string(mode), Path: resolution.Path,
		NodeID: resolution.NodeID, NodeLabel: resolution.NodeLabel,
		Picked: resolution.Picked, Matched: resolution.Matched,
		Category: resolution.Category, Candidates: resolution.Candidates,
		Families: resolution.Families, Warnings: resolution.Warnings,
	}
	if !view.Matched {
		view.Note = "The graph has no node for this activity; the model plans the path. Pick a node below to anchor the value chain instead."
	} else if view.Picked {
		view.Note = ""
	}
	s.render(w, "ontology_match", view)
}

// ontologyMatchBudget bounds the picker: the resolution may make one alias or
// family-choice model call when the graph does not know the text.
const ontologyMatchBudget = 45 * time.Second

// ontologyMatchView is one picker answer: what the activity resolves to now, and
// the nodes worth confirming instead.
type ontologyMatchView struct {
	Query      string
	Mode       string
	Path       string
	NodeID     string
	NodeLabel  string
	Picked     bool
	Matched    bool
	Category   string
	Candidates []agent.ActivityCandidate
	Families   []agent.ActivityFamilyView
	Warnings   []string
	Note       string
}
