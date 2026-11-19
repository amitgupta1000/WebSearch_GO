package agent

// ontology_pick.go is the operator's side of the ontology match: the ranked
// nodes worth confirming, and the confirmation itself.
//
// The resolver is good but it is not an oracle. "crude palm oil" is one alias of
// Palm oil and one generic alias ("crude") of Crude oil; a token position or a
// vocabulary gap decides which one wins, and only the operator knows which
// product they meant. So the match is shown with its alternatives, the operator
// confirms one, and the confirmation anchors the run's value chain directly --
// the same suggestion-first contract as the trade DB's product and entity
// pickers. Nothing resolves differently because a pick exists; a pick only says
// which node the activity means.

import (
	"fmt"
	"strings"
	"sync"

	"github.com/amitgupta1000/WebSearch_GO/internal/ontology"
)

const (
	// ontologyCandidateLimit caps the choices offered to the operator.
	ontologyCandidateLimit = 5
	// ontologyPickLimit bounds the per-process confirmation store.
	ontologyPickLimit = 64
)

// ActivityCandidate is one ontology node the operator can confirm, with the
// product family it would drive so the choice is about the research path and
// not just a label.
type ActivityCandidate struct {
	NodeID      string  `json:"node_id"`
	Label       string  `json:"label"`
	Kind        string  `json:"kind"`
	Phrase      string  `json:"phrase,omitempty"`
	FamilyID    string  `json:"family_id,omitempty"`
	FamilyLabel string  `json:"family_label,omitempty"`
	Score       float64 `json:"score"`
	Grade       string  `json:"grade"`
}

// ontologyMatchGrade scores one graph match by how it was reached: a whole label
// or alias is the activity itself, a joined spelling is the same name typed
// without spaces, and a prefix or a process search term is the loosest reading.
func ontologyMatchGrade(match ontology.Match) (float64, string) {
	switch match.Via {
	case "label", "alias", "trade-product", "operator":
		return 1, "exact"
	case "joined":
		return 0.95, "spelling"
	case "search-term":
		return 0.9, "process"
	case "leading":
		return 0.85, "prefix"
	default:
		return 0.8, "related"
	}
}

// ontologyPickStore remembers the operator's confirmation per activity for the
// life of the process, exactly like the trade-entity overrides: a restart forgets
// the picks, and persistence is a deliberate later step.
type ontologyPickStore struct {
	mu    sync.Mutex
	picks map[string]string
	order []string
}

var ontologyPicks = &ontologyPickStore{picks: map[string]string{}}

// normalizeOntologyActivity is the pick key: the activity as typed, without case
// or repeated spaces, so a re-typed activity keeps its confirmation.
func normalizeOntologyActivity(activity string) string {
	return strings.ToLower(strings.Join(strings.Fields(activity), " "))
}

// SetOntologyPick records the operator's confirmation for an activity.
func SetOntologyPick(activity, nodeID string) {
	key := normalizeOntologyActivity(activity)
	nodeID = strings.TrimSpace(nodeID)
	if key == "" || nodeID == "" {
		return
	}
	ontologyPicks.mu.Lock()
	defer ontologyPicks.mu.Unlock()
	if _, exists := ontologyPicks.picks[key]; !exists {
		ontologyPicks.order = append(ontologyPicks.order, key)
		if len(ontologyPicks.order) > ontologyPickLimit {
			delete(ontologyPicks.picks, ontologyPicks.order[0])
			ontologyPicks.order = ontologyPicks.order[1:]
		}
	}
	ontologyPicks.picks[key] = nodeID
}

// ontologyPickFor returns the confirmed node for an activity, or "".
func ontologyPickFor(activity string) string {
	key := normalizeOntologyActivity(activity)
	if key == "" {
		return ""
	}
	ontologyPicks.mu.Lock()
	defer ontologyPicks.mu.Unlock()
	return ontologyPicks.picks[key]
}

// ontologyPickNotice explains an applied confirmation in the report.
func ontologyPickNotice(activity, nodeID, label string) string {
	return fmt.Sprintf("Ontology anchor confirmed by the operator: %q means %s (%s) for this run; that node's value chain supplies the families, lanes and stages.", strings.TrimSpace(activity), label, nodeID)
}

// ontologyPickLog is the run-log line for an applied confirmation.
func ontologyPickLog(nodeID, label string) string {
	return fmt.Sprintf("Ontology: operator confirmed %s (%s)", label, nodeID)
}
