package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/ontology"
)

// activity_resolution.go is the entry point both landscape modes share: how an
// operator's activity becomes a research path.
//
//	(1)   the curated graph resolves it  → the graph guides the LLM's plan
//	(2.1) it does not resolve            → the LLM may map it to a known node as an
//	                                        alias, and then the run proceeds as (1)
//	(2.2) no match, no alias             → the LLM builds a new path; the activity is
//	                                        reported as unmodelled so curation can add it
//
// Resolution never invents chemistry: alias mapping is constrained to approved node
// IDs, and an unapproved node is reported as such instead of being used.

// activityPath records which branch resolved the activity.
type activityPath string

const (
	// pathOntology is a direct graph match.
	pathOntology activityPath = "ontology"
	// pathAlias is a model-confirmed alias of a graph node.
	pathAlias activityPath = "alias"
	// pathUnmodelled means the graph knows nothing about it: the model plans freely.
	pathUnmodelled activityPath = "unmodelled"
)

// activityResolution is the shared result: the graph view, the node that anchored
// it, and the warnings the report keeps.
type activityResolution struct {
	Activity string
	Path     activityPath
	Graph    ontology.Resolution
	NodeID   string
	NodeLabel string
	// AliasConfidence is the model's confidence for an alias mapping.
	AliasConfidence string
	Warnings        []string

	document *ontology.Ontology
}

// matched reports whether the graph (directly or through an alias) knows the activity.
func (r activityResolution) matched() bool {
	return r.Path == pathOntology || r.Path == pathAlias
}

// primaryCategory is the family label that should drive the category lane.
func (r activityResolution) primaryCategory() string {
	if len(r.Graph.Families) == 0 {
		return ""
	}
	return r.Graph.Families[0].Label
}

// primaryFamilyID is the family node that anchored the resolution.
func (r activityResolution) primaryFamilyID() string {
	if len(r.Graph.Families) == 0 {
		return ""
	}
	return r.Graph.Families[0].ID
}

// familyLabels lists the ranked variants, primary first.
func (r activityResolution) familyLabels() []string {
	labels := make([]string, 0, len(r.Graph.Families))
	for _, family := range r.Graph.Families {
		labels = append(labels, family.Label)
	}
	return labels
}

// downstreamCategory is the buyer-side category: the consuming family the graph
// names, not the product's own family (which describes the sellers). "n-butanol"
// therefore broadens on acrylates, not on alcohol solvents.
func (r activityResolution) downstreamCategory() string {
	for _, family := range r.Graph.Families {
		if family.Reason == "downstream" && strings.TrimSpace(family.Label) != "" {
			return family.Label
		}
	}
	return r.primaryCategory()
}

// ambiguousHub reports whether every family the graph returned is a hypernym —
// i.e. the activity named a family budget ("oil"), not a family.
func (r activityResolution) ambiguousHub() bool {
	if len(r.Graph.Families) < 2 {
		return false
	}
	for _, family := range r.Graph.Families {
		if family.Reason != "hypernym" {
			return false
		}
	}
	return true
}

// rankHypernymVariants orders an ambiguous hub's variants by the qualifier the
// activity carries. The graph states which families are possible ("oil" spans
// lubricants, fuel oils, crude oil and oleochemical feedstocks); the qualifier
// decides which one the operator means, so "soybean oil" is a feedstock oil even
// though "lubricants" is the hub's default rank, and "white oil" is a lubricant
// even though a bare "oil" is ambiguous. With no qualifier the graph's rank stands
// and the seller mode may ask the model to choose among the variants.
func rankHypernymVariants(resolution activityResolution) activityResolution {
	if !resolution.ambiguousHub() {
		return resolution
	}
	label, qualifier := oilQualifierFamily(resolution.Activity)
	if label == "" {
		return resolution
	}
	for index, family := range resolution.Graph.Families {
		if index == 0 || !strings.EqualFold(family.Label, label) {
			continue
		}
		reordered := make([]ontology.Family, 0, len(resolution.Graph.Families))
		reordered = append(reordered, resolution.Graph.Families[index])
		reordered = append(reordered, resolution.Graph.Families[:index]...)
		reordered = append(reordered, resolution.Graph.Families[index+1:]...)
		for rank := range reordered {
			reordered[rank].Rank = rank
		}
		resolution.Graph.Families = reordered
		resolution.Warnings = append(resolution.Warnings, fmt.Sprintf(
			"The activity's own qualifier (%s) selects %s among the hub's variants; %s drives the category lane.",
			qualifier, family.Label, family.Label))
		return resolution
	}
	return resolution
}

// oilQualifierFamily maps an oil-bearing activity onto the graph family its
// qualifier names, returning the label and the qualifier words that decided it.
//
// This is the only oil knowledge left in code, and it exists for names the graph
// has no node for ("soybean oil", "white oil"): the graph states what is possible,
// the qualifier picks. When the graph gains those names this table disappears —
// curate the graph, never extend the table.
func oilQualifierFamily(activity string) (string, string) {
	qualified := demandOilFamilies(activity)
	if len(qualified) == 0 {
		return "", ""
	}
	label, ok := activityQualifierFamilies[strings.ToLower(qualified[0])]
	if !ok {
		return "", ""
	}
	return label, strings.Join(qualified, "/")
}

// activityQualifierFamilies maps the qualifier vocabulary's family keys onto the
// graph's labels.
var activityQualifierFamilies = map[string]string{
	"lubricants":    "lubricants",
	"fuel oils":     "fuel oils",
	"crude oil":     "crude oil",
	"oleochemicals": "oleochemical feedstocks",
	"biodiesel":     "biodiesel",
}

// searchTerms returns the graph's indexed phrases for the discovery lanes, minus
// the phrases that name a *sibling* family of the one this run resolved to. A
// shared process vocabulary spans families ("blending" indexes lubricant, fuel and
// crude phrasing alike), so a fuel-oil run must not buy "lubricant blending" merely
// because both run through blending. The exclusion comes from the hub's own
// hyponyms, not from a table.
func (r activityResolution) searchTerms() []string {
	terms := r.Graph.SearchTerms
	if r.document == nil || len(r.Graph.Families) == 0 {
		return terms
	}
	siblings := r.document.SiblingVariants(r.Graph.Families[0].ID)
	if len(siblings) == 0 {
		return terms
	}
	filtered := make([]string, 0, len(terms))
	for _, term := range terms {
		lowered := strings.ToLower(term)
		conflicting := false
		for _, sibling := range siblings {
			if activityNamesFamily(lowered, sibling.Label) {
				conflicting = true
				break
			}
		}
		if !conflicting {
			filtered = append(filtered, term)
		}
	}
	return filtered
}

// activityNamesFamily reports whether a phrase names the family, tolerating the
// plural/singular difference between labels and trade phrasing ("lubricants" label
// vs "lubricant blending" phrase).
func activityNamesFamily(phrase, label string) bool {
	label = strings.ToLower(strings.TrimSpace(label))
	if label == "" {
		return false
	}
	if strings.Contains(phrase, label) || strings.Contains(phrase, strings.TrimSuffix(label, "s")) {
		return true
	}
	// Multi-word labels ("oleochemical feedstocks") also appear with the trailing
	// word pluralised away in phrases.
	words := strings.Fields(label)
	if len(words) > 1 {
		parts := make([]string, 0, len(words))
		for _, word := range words {
			parts = append(parts, strings.TrimSuffix(word, "s"))
		}
		if strings.Contains(phrase, strings.Join(parts, " ")) {
			return true
		}
	}
	return false
}


// categoryRationale explains the choice in the report's own terms.
func (r activityResolution) categoryRationale() string {
	if len(r.Graph.Families) == 0 {
		return ""
	}
	primary := r.Graph.Families[0]
	switch primary.Reason {
	case "hypernym":
		return fmt.Sprintf("%q names an ambiguous family; the ranked variants are %s, and the top variant drives the category lane.", r.Activity, strings.Join(r.familyLabels(), " > "))
	case "subject":
		return fmt.Sprintf("%q resolves to the graph's own node %q.", r.Activity, primary.Label)
	case "process-output":
		return fmt.Sprintf("%q names a process; the graph's product family for it is %q.", r.Activity, primary.Label)
	case "downstream":
		return fmt.Sprintf("%q resolves to %q, whose downstream family is %q.", r.Activity, primary.Via, primary.Label)
	default:
		return fmt.Sprintf("%q resolves through the value-chain graph to %q.", r.Activity, primary.Label)
	}
}

// resolveActivity runs the three-branch resolution described in the file header.
// It fails open: an unavailable graph makes the run model-planned, never failed.
func resolveActivity(ctx context.Context, engine *Engine, activity string, eventCh chan<- models.AgentEvent) activityResolution {
	document, err := ontology.Shared()
	if err != nil {
		return unavailableActivityResolution(strings.TrimSpace(activity), err)
	}
	return resolveActivityWithDocument(ctx, engine, document, activity, eventCh)
}

// resolveActivityWithDocument is the resolution against a chosen document: the
// runtime uses the embedded seed, the CLI and review tools use a candidate file.
func resolveActivityWithDocument(ctx context.Context, engine *Engine, document *ontology.Ontology, activity string, eventCh chan<- models.AgentEvent) activityResolution {
	resolution := activityResolution{Activity: strings.TrimSpace(activity)}
	if document == nil {
		return unavailableActivityResolution(resolution.Activity, fmt.Errorf("no value-chain document loaded"))
	}
	resolution.document = document
	resolution.Graph = document.Resolve(resolution.Activity, ontology.DefaultOptions)
	if len(resolution.Graph.Families) > 0 || len(resolution.Graph.Processes) > 0 {
		resolution.Path = pathOntology
		// An ambiguous hub ("oil") is ranked by the activity's own qualifier, so a
		// feedstock oil never inherits the petroleum default and vice versa.
		resolution = rankHypernymVariants(resolution)
		if len(resolution.Graph.Matches) > 0 {
			resolution.NodeID = resolution.Graph.Matches[0].ID
			resolution.NodeLabel = resolution.Graph.Matches[0].Label
		}
		emitAgentEvent(eventCh, models.EventThinking,
			fmt.Sprintf("Ontology path: %q resolves to %s", resolution.Activity, strings.Join(resolution.familyLabels(), ", ")),
			"ontology_resolve", "")
		return resolution
	}

	// (2.1) The graph does not know the activity verbatim: ask the model whether
	// it is an alias of an approved node. The answer is constrained to node IDs.
	node, confidence, rationale, ok := ontologyAliasMatch(ctx, engine, document, resolution.Activity)
	if ok {
		resolution.Path = pathAlias
		resolution.NodeID = node.ID
		resolution.NodeLabel = node.Label
		resolution.AliasConfidence = confidence
		resolution.Graph = document.Resolve(node.Label, ontology.DefaultOptions)
		resolution.Warnings = append(resolution.Warnings, fmt.Sprintf(
			"Activity %q is not in the value-chain graph verbatim; the model mapped it to %q (%s) as an alias at %s confidence, and that node's value chain guided this run. Add %q as an alias during curation.",
			resolution.Activity, node.Label, node.ID, confidence, resolution.Activity))
		if rationale != "" {
			resolution.Warnings = append(resolution.Warnings, "Alias rationale: "+rationale)
		}
		emitAgentEvent(eventCh, models.EventThinking,
			fmt.Sprintf("Ontology alias: %q → %s (%s, %s)", resolution.Activity, node.Label, node.ID, confidence),
			"ontology_alias", "")
		return resolution
	}

	// (2.2) Nothing in the graph applies: the model plans the path from scratch.
	resolution.Path = pathUnmodelled
	resolution.Warnings = append(resolution.Warnings, fmt.Sprintf(
		"Activity %q is unmodelled: no value-chain match and no confident alias, so the research path is model-derived. Add it to the ontology so later runs are graph-guided.",
		resolution.Activity))
	emitAgentEvent(eventCh, models.EventThinking,
		fmt.Sprintf("Ontology: %q is unmodelled; the model defines the research path", resolution.Activity),
		"ontology_unmodelled", "")
	return resolution
}

// unavailableActivityResolution keeps a run alive when the graph cannot load: the
// model plans the path and the report says why.
func unavailableActivityResolution(activity string, err error) activityResolution {
	return activityResolution{
		Activity: activity,
		Path:     pathUnmodelled,
		Warnings: []string{"Value-chain ontology unavailable (" + err.Error() + "); the research path is model-derived for this run."},
	}
}

// ontologyAliasProbe is the model's alias verdict.
type ontologyAliasProbe struct {
	MatchID    string `json:"match_id"`
	Confidence string `json:"confidence"`
	Rationale  string `json:"rationale"`
}

// ontologyAliasMatch asks the model to map an unknown activity onto an approved
// node. Only a high/medium confidence verdict is accepted, and only when the ID
// exists and is approved — a hallucinated or unapproved ID is a no.
func ontologyAliasMatch(ctx context.Context, engine *Engine, document *ontology.Ontology, activity string) (*ontology.Node, string, string, bool) {
	if engine == nil || strings.TrimSpace(activity) == "" {
		return nil, "", "", false
	}
	candidates := ontologyAliasCandidates(document, activity, 25)
	if len(candidates) == 0 {
		return nil, "", "", false
	}
	prompt := fmt.Sprintf(`The operator's activity %q is not in the curated value-chain graph verbatim.
Decide whether it is an alias, synonym, trade name or close variant of ONE node below — close enough that the node's value chain (product family, consuming processes, industrial end products) applies to the research path.
Answer with JSON {match_id, confidence, rationale}. confidence is "high", "medium" or "low". If no node genuinely applies, return match_id "" and explain why. Never stretch a match: a different chemistry is not an alias.
Nodes (id | kind | label):
%s`, activity, strings.Join(candidates, "\n"))
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var probe ontologyAliasProbe
	if err := engine.GenerateStructured(probeCtx,
		"You reconcile operator activity names with a curated chemical value-chain graph. Never invent an id, never map a different chemistry onto a node, and prefer match_id \"\" over a weak match.",
		prompt, &probe); err != nil {
		return nil, "", "", false
	}
	id := strings.TrimSpace(probe.MatchID)
	if id == "" {
		return nil, "", "", false
	}
	node := document.Nodes[id]
	if node == nil || node.Status != ontology.StatusApproved {
		return nil, "", "", false
	}
	confidence := strings.ToLower(strings.TrimSpace(probe.Confidence))
	if confidence != "high" && confidence != "medium" {
		return nil, "", "", false
	}
	return node, confidence, strings.TrimSpace(probe.Rationale), true
}

// ontologyAliasCandidates lists the nodes the model may map onto: every approved
// category and process (the families and transformations a new activity usually
// belongs to), plus up to scoredLimit substances and end products that share a
// content word with the activity. Structural nodes are never crowded out by
// scored ones, so a graph that outgrows the prompt budget still offers its
// backbone instead of an arbitrary alphabetical slice.
func ontologyAliasCandidates(document *ontology.Ontology, activity string, scoredLimit int) []string {
	activityWords := map[string]bool{}
	for _, word := range ontologyContentWords(activity) {
		activityWords[word] = true
	}
	type candidate struct {
		score int
		line  string
	}
	var structural []candidate
	var scored []candidate
	for _, id := range document.NodeIDs() {
		node := document.Nodes[id]
		if node == nil || node.Status != ontology.StatusApproved {
			continue
		}
		line := fmt.Sprintf("%s | %s | %s", id, node.Kind, node.Label)
		if node.Kind == ontology.KindCategory || node.Kind == ontology.KindProcess {
			structural = append(structural, candidate{line: line})
			continue
		}
		score := 0
		for _, name := range append([]string{node.Label}, node.Aliases...) {
			for _, word := range ontologyContentWords(name) {
				if activityWords[word] {
					score++
				}
			}
		}
		if score > 0 {
			scored = append(scored, candidate{score: score, line: line})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].line < scored[j].line
	})
	lines := make([]string, 0, len(structural)+scoredLimit)
	for _, candidate := range structural {
		lines = append(lines, candidate.line)
	}
	for index, candidate := range scored {
		if index >= scoredLimit {
			break
		}
		lines = append(lines, candidate.line)
	}
	return lines
}

// ontologyContentWords returns the comparable words of a name: lowercase, at
// least three letters, without the role words every label shares.
func ontologyContentWords(value string) []string {
	words := strings.Fields(strings.ToLower(value))
	out := make([]string, 0, len(words))
	for _, word := range words {
		word = strings.Trim(word, "(),.;:")
		if len(word) < 3 || ontologyAliasStopwords[word] {
			continue
		}
		out = append(out, word)
	}
	return out
}

// familyChoice is the model's verdict when an activity is ambiguous.
type familyChoice struct {
	FamilyID  string `json:"family_id"`
	Rationale string `json:"rationale"`
}

// chooseOntologyFamily lets the model pick among the ranked variants of an
// ambiguous activity using the run's context ("oil blending" in a refining region
// may mean fuel oils rather than lubricants). The answer is constrained to the
// listed IDs; any failure keeps the graph's rank order.
func chooseOntologyFamily(ctx context.Context, engine *Engine, resolution activityResolution, geography string, eventCh chan<- models.AgentEvent) activityResolution {
	families := resolution.Graph.Families
	if len(families) < 2 || engine == nil {
		return resolution
	}
	lines := make([]string, 0, len(families))
	for _, family := range families {
		lines = append(lines, fmt.Sprintf("%s | %s", family.ID, family.Label))
	}
	scope := strings.TrimSpace(geography)
	if scope == "" {
		scope = "not specified"
	}
	prompt := fmt.Sprintf(`The activity %q is ambiguous in the value-chain graph. These product families are the ranked candidates:
%s

Geography: %s

Choose the ONE family that best matches what an operator researching this activity in this geography means. Answer with JSON {family_id, rationale}. Choose only from the ids above; if the ranking is right, choose the first.`, resolution.Activity, strings.Join(lines, "\n"), scope)
	choiceCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var choice familyChoice
	if err := engine.GenerateStructured(choiceCtx,
		"You choose one product family from a closed list. Never invent an id, and never choose a family outside the list.",
		prompt, &choice); err != nil {
		return resolution
	}
	id := strings.TrimSpace(choice.FamilyID)
	for index, family := range families {
		if family.ID != id || index == 0 {
			continue
		}
		reordered := make([]ontology.Family, 0, len(families))
		reordered = append(reordered, families[index])
		reordered = append(reordered, families[:index]...)
		reordered = append(reordered, families[index+1:]...)
		for rank := range reordered {
			reordered[rank].Rank = rank
		}
		resolution.Graph.Families = reordered
		resolution.Warnings = append(resolution.Warnings, fmt.Sprintf(
			"Graph ambiguity resolved for this run: %s ranks first for %q overall, but %s fits the requested geography (%s); %s drives the category lane.",
			families[0].Label, resolution.Activity, family.Label, scope, family.Label))
		emitAgentEvent(eventCh, models.EventThinking,
			fmt.Sprintf("Ontology variant: %q → %s for %s", resolution.Activity, family.Label, scope),
			"ontology_variant", "")
		return resolution
	}
	return resolution
}

// ontologyAliasStopwords are the role words every label shares; they carry no
// matching signal.
var ontologyAliasStopwords = map[string]bool{
	"and": true, "the": true, "for": true, "with": true, "from": true,
	"production": true, "manufacturing": true, "manufacturer": true, "manufacturers": true,
	"plant": true, "products": true, "product": true, "based": true, "grade": true,
}



