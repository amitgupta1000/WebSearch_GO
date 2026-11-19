package agent

// ontology_candidates.go ranks the nodes an operator can confirm for an
// activity. It is the suggestion half of ontology_pick.go.
//
// The order answers the question the operator actually has. On a specific
// activity ("crude palm oil") the alternatives are the news: "did you mean
// crude oil?". On an ambiguous one ("oil blending") the graph's own ranked
// variants are the news, because the alternative nouns are all "X oil" and say
// nothing about the research path. So the ambiguous case leads with variants and
// the specific case leads with the related nodes; the family list follows either
// way, filling whatever room is left.

import (
	"sort"

	"github.com/amitgupta1000/WebSearch_GO/internal/ontology"
)

// activityCandidates ranks the nodes worth confirming for this activity and
// caps the list. The first candidate is the current match.
func activityCandidates(document *ontology.Ontology, graph ontology.Resolution, activity string, limit int) []ActivityCandidate {
	if document == nil {
		return nil
	}
	if limit <= 0 {
		limit = ontologyCandidateLimit
	}
	seen := map[string]bool{}
	add := func(target *[]ActivityCandidate, node *ontology.Node, phrase string, score float64, grade string) {
		if node == nil || seen[node.ID] || node.Status != ontology.StatusApproved {
			return
		}
		seen[node.ID] = true
		candidate := ActivityCandidate{
			NodeID: node.ID, Label: node.Label, Kind: string(node.Kind),
			Phrase: phrase, Score: score, Grade: grade,
		}
		// The family the node would drive: the run searches that family, so the
		// operator chooses a research path, not just a word.
		if anchored, ok := document.ResolveNode(node.ID, ontology.DefaultOptions); ok && len(anchored.Families) > 0 {
			candidate.FamilyID = anchored.Families[0].ID
			candidate.FamilyLabel = anchored.Families[0].Label
		}
		if candidate.FamilyID == "" {
			candidate.FamilyID, candidate.FamilyLabel = node.ID, node.Label
		}
		*target = append(*target, candidate)
	}

	// What the graph matched, in its own specificity order.
	var matched []ActivityCandidate
	for _, match := range graph.Matches {
		score, grade := ontologyMatchGrade(match)
		add(&matched, document.Nodes[match.ID], match.Phrase, score, grade)
	}

	// The ranked variants of an ambiguous activity.
	var variants []ActivityCandidate
	for _, family := range graph.Families {
		score := 0.8 - 0.05*float64(family.Rank)
		if score < 0.5 {
			score = 0.5
		}
		add(&variants, document.Nodes[family.ID], family.Label, score, "variant")
	}

	// Approved nodes by shared content words: the same prefilter the alias probe
	// uses for its prompt, and the only signal when the graph matched nothing.
	var related []ActivityCandidate
	activityWords := map[string]bool{}
	for _, word := range ontologyContentWords(activity) {
		activityWords[word] = true
	}
	if len(activityWords) > 0 {
		type scored struct {
			node     *ontology.Node
			score    int
			coverage float64
		}
		var ranked []scored
		for _, id := range document.NodeIDs() {
			node := document.Nodes[id]
			if node == nil || seen[id] || node.Status != ontology.StatusApproved {
				continue
			}
			if node.Kind != ontology.KindSubstance && node.Kind != ontology.KindCategory && node.Kind != ontology.KindEndProduct {
				continue
			}
			// Score the node by its single best-fitting name: summing over every
			// alias lets a long alias list out-score a precisely matching label.
			score, coverage := 0, 0.0
			for _, name := range append([]string{node.Label}, node.Aliases...) {
				matched, own := 0, 0
				for _, word := range ontologyContentWords(name) {
					own++
					if activityWords[word] {
						matched++
					}
				}
				if own == 0 {
					continue
				}
				ratio := float64(matched) / float64(own)
				if matched > score || (matched == score && ratio > coverage) {
					score, coverage = matched, ratio
				}
			}
			if score > 0 {
				ranked = append(ranked, scored{node: node, score: score, coverage: coverage})
			}
		}
		sort.SliceStable(ranked, func(i, j int) bool {
			if ranked[i].score != ranked[j].score {
				return ranked[i].score > ranked[j].score
			}
			// Equal overlap: the candidate the activity covers more of wins.
			// "crude palm oil" covers all of "Crude oil" and half of "Base oil".
			if ranked[i].coverage != ranked[j].coverage {
				return ranked[i].coverage > ranked[j].coverage
			}
			return ranked[i].node.ID < ranked[j].node.ID
		})
		for _, candidate := range ranked {
			ratio := float64(candidate.score) / float64(len(activityWords))
			if ratio > 1 {
				ratio = 1
			}
			grade := "loose"
			if ratio >= 0.5 || candidate.coverage >= 0.5 {
				grade = "related"
			}
			add(&related, candidate.node, candidate.node.Label, 0.3+0.4*(0.6*ratio+0.4*candidate.coverage), grade)
		}
	}

	candidates := append(append([]ActivityCandidate{}, matched...), orderedTail(graph.Ambiguous, variants, related)...)
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

// orderedTail picks which group follows the match: the ranked variants for an
// ambiguous activity, the related nodes otherwise.
func orderedTail(ambiguous bool, variants, related []ActivityCandidate) []ActivityCandidate {
	if ambiguous {
		return append(append([]ActivityCandidate{}, variants...), related...)
	}
	return append(append([]ActivityCandidate{}, related...), variants...)
}
