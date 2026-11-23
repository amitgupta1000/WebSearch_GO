// Package ontology holds the curated chemical value-chain graph: the product
// families, canonical substances, processes and industrial end-products the
// landscape modes expand an operator activity through.
//
// The JSON document in data/ is the single source of truth. Nothing in this
// package invents chemistry: the loader applies the domain reviewer's
// corrections, the linter gates the result, and the agents may only reference
// node IDs that exist. A reviewer returns either the whole edited document or a
// corrections-only patch; Merge folds that patch onto the reviewed base so the
// curated version — not the seed — is what the runtime reads.
package ontology

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// SchemaVersion is the document contract. A file declaring another version is
// refused rather than guessed at: the reviewer's tooling must agree with the
// runtime about what a field means.
const SchemaVersion = 1

// Kind is the node type. The four kinds are the operator's entry tiers plus the
// value chain's leaves.
type Kind string

const (
	// KindSubstance is a canonical chemical or feedstock name an operator can
	// type: the trade DB's canonical product names, their allied names and
	// aliases.
	KindSubstance Kind = "substance"
	// KindCategory is a petrochemical family or sub-family (aromatics, solvents,
	// esters, amines, plasticizers, …), nestable through parent.
	KindCategory Kind = "category"
	// KindProcess is a transformation operation (blending, refining,
	// esterification, transesterification, polymerization, …).
	KindProcess Kind = "process"
	// KindEndProduct is a value-chain leaf: an industrial article or compound
	// whose producers/consumers are the companies a landscape run is looking
	// for. Its operator_phrases are the lane vocabulary for those companies.
	KindEndProduct Kind = "end-product"
)

// Status is the review state of one node. Only approved nodes resolve at
// runtime; proposed nodes are visible to the review tooling and excluded by the
// runtime (rejected nodes are kept for the audit trail and never resolve).
type Status string

const (
	StatusProposed Status = "proposed"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
)

// Source records where a relation came from. "seed" means authored from the
// in-repo vocabulary and not yet verified; "llm-review" means a machine review
// proposed or corrected the row (still pending a domain expert); the reviewer
// upgrades a row to hs/echa/operator/trade-db as they confirm it.
const (
	SourceSeed      = "seed"
	SourceLLMReview = "llm-review"
	SourceHS        = "hs"
	SourceECHA      = "echa"
	SourceOperator  = "operator"
	SourceTradeDB   = "trade-db"
)

// Confidence is the reviewer's (or seed's) certainty in a node's relations.
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// Node is one graph vertex. Not every field applies to every kind; the linter
// enforces which do (see lint.go).
type Node struct {
	// ID is the node's map key, filled by the loader. It is not part of the JSON
	// contract: the key in the nodes object is the ID.
	ID    string `json:"-"`
	Kind  Kind   `json:"kind"`
	Label string `json:"label"`

	// Aliases are additional entry points for the same node. Alias uniqueness
	// across the graph is a lint error: two nodes answering to one name is the
	// shared-word failure this graph exists to prevent.
	Aliases []string `json:"aliases,omitempty"`
	// HSCodes are the customs headings the product is filed under.
	HSCodes []string `json:"hs_codes,omitempty"`

	// Parent nests a category (sub-category of …).
	Parent string `json:"parent,omitempty"`
	// Categories are a substance's family memberships (a substance may belong
	// to several: toluene is an aromatic and a solvent).
	Categories []string `json:"categories,omitempty"`

	// DerivedTo is the downstream direction of the chemistry chain:
	// n-butanol → butyl acrylate.
	DerivedTo []string `json:"derived_to,omitempty"`
	// ConsumedIn names the process or end-product that takes this node as an
	// input. It is the value-chain edge.
	ConsumedIn []string `json:"consumed_in,omitempty"`

	// OperatesOn / Produces describe a process.
	OperatesOn []string `json:"operates_on,omitempty"`
	Produces   []string `json:"produces,omitempty"`
	// Consumes describes an end-product's inputs.
	Consumes []string `json:"consumes,omitempty"`

	// OperatorPhrases are the company/plant phrases a landscape lane searches
	// for ("pvc compounder", "grease manufacturer").
	OperatorPhrases []string `json:"operator_phrases,omitempty"`
	// SearchTerms are indexed phrases for a process (process × family).
	SearchTerms []string `json:"search_terms,omitempty"`

	// Hypernym marks a category whose name is ambiguous on its own ("oil").
	// Hyponyms are its ranked variants; order is the rank, first is dominant.
	Hypernym bool     `json:"hypernym,omitempty"`
	Hyponyms []string `json:"hyponyms,omitempty"`
	// IsIntermediate marks a salable family that is itself consumed onward.
	IsIntermediate bool `json:"is_intermediate,omitempty"`

	Status     Status `json:"status"`
	Source     string `json:"source,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	ExpertNote string `json:"expert_note,omitempty"`
}

// Correction is one reviewer edit. A present field replaces that field on the
// base node wholesale (slices are replaced, not appended) — the reviewer always
// sends the list they want to end up with. A correction whose ID is not in the
// base is an addition and must carry kind and label.
type Correction struct {
	Kind  *Kind   `json:"kind,omitempty"`
	Label *string `json:"label,omitempty"`
	// Delete removes the node. Dangling references are caught by the linter.
	Delete bool `json:"delete,omitempty"`

	Aliases         *[]string `json:"aliases,omitempty"`
	HSCodes         *[]string `json:"hs_codes,omitempty"`
	Parent          *string   `json:"parent,omitempty"`
	Categories      *[]string `json:"categories,omitempty"`
	DerivedTo       *[]string `json:"derived_to,omitempty"`
	ConsumedIn      *[]string `json:"consumed_in,omitempty"`
	OperatesOn      *[]string `json:"operates_on,omitempty"`
	Produces        *[]string `json:"produces,omitempty"`
	Consumes        *[]string `json:"consumes,omitempty"`
	OperatorPhrases *[]string `json:"operator_phrases,omitempty"`
	SearchTerms     *[]string `json:"search_terms,omitempty"`
	Hypernym        *bool     `json:"hypernym,omitempty"`
	Hyponyms        *[]string `json:"hyponyms,omitempty"`
	IsIntermediate  *bool     `json:"is_intermediate,omitempty"`

	Status     *Status `json:"status,omitempty"`
	Source     *string `json:"source,omitempty"`
	Confidence *string `json:"confidence,omitempty"`
	ExpertNote *string `json:"expert_note,omitempty"`
}

// Ontology is the whole document: metadata, nodes and the reviewer's patch.
type Ontology struct {
	SchemaVersion int                    `json:"schema_version"`
	Revision      string                 `json:"revision"`
	ReviewedBy    string                 `json:"reviewed_by,omitempty"`
	Notes         string                 `json:"notes,omitempty"`
	Nodes         map[string]*Node       `json:"nodes"`
	Corrections   map[string]*Correction `json:"corrections,omitempty"`
}

//go:embed data/Chem_KG.json
var embeddedSeed []byte

// Seed returns the reviewed document shipped with the binary, without applying
// corrections or linting. Callers that need the runtime view should use Load.
func Seed() (*Ontology, error) {
	return parse(embeddedSeed)
}

// Load returns the embedded document with corrections applied and linted; a
// lint error fails the load instead of degrading quietly.
func Load() (*Ontology, error) {
	document, err := Seed()
	if err != nil {
		return nil, err
	}
	if err := ApplyCorrections(document, document.Corrections); err != nil {
		return nil, err
	}
	document.Corrections = nil
	if err := Validate(document); err != nil {
		return nil, err
	}
	return document, nil
}

// LoadFile reads, strictly decodes and applies the corrections of one document.
// Unknown fields are an error: a reviewer typo ("hs_code") must not be silently
// dropped from the curated file.
func LoadFile(path string) (*Ontology, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	document, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := ApplyCorrections(document, document.Corrections); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	document.Corrections = nil
	return document, nil
}

// Shared returns the embedded document loaded and linted once per process. The
// document is read-only at runtime (resolution never mutates it), so one copy
// serves every run.
func Shared() (*Ontology, error) {
	sharedOnce.Do(func() {
		sharedDoc, sharedErr = Load()
	})
	return sharedDoc, sharedErr
}

var (
	sharedOnce sync.Once
	sharedDoc  *Ontology
	sharedErr  error
)

// LoadFileRaw decodes a document without applying its corrections. It is how a
// corrections-only patch is read: a patch cannot be applied to itself, so Merge
// applies it to the reviewed base instead.
func LoadFileRaw(path string) (*Ontology, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	document, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return document, nil
}

func parse(raw []byte) (*Ontology, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document Ontology
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode ontology: %w", err)
	}
	if document.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("ontology schema_version %d, this build supports %d", document.SchemaVersion, SchemaVersion)
	}
	if document.Nodes == nil {
		document.Nodes = map[string]*Node{}
	}
	for id, node := range document.Nodes {
		if node != nil {
			node.ID = id
		}
	}
	return &document, nil
}

// Merge folds a reviewer's document onto a base document. The patch's nodes
// override the base's, and the patch's corrections are applied afterwards, so a
// reviewer can send either the full edited file or a corrections-only file.
func Merge(base, patch *Ontology) (*Ontology, error) {
	if base == nil || patch == nil {
		return nil, fmt.Errorf("merge needs two documents")
	}
	result := &Ontology{
		SchemaVersion: SchemaVersion,
		Revision:      base.Revision,
		ReviewedBy:    base.ReviewedBy,
		Notes:         base.Notes,
		Nodes:         make(map[string]*Node, len(base.Nodes)+len(patch.Nodes)),
	}
	for id, node := range base.Nodes {
		result.Nodes[id] = node.clone()
	}
	for id, node := range patch.Nodes {
		if node == nil {
			continue
		}
		result.Nodes[id] = node.clone()
	}
	if patch.Revision != "" {
		result.Revision = patch.Revision
	}
	if patch.ReviewedBy != "" {
		result.ReviewedBy = patch.ReviewedBy
	}
	if patch.Notes != "" {
		result.Notes = patch.Notes
	}
	if err := ApplyCorrections(result, patch.Corrections); err != nil {
		return nil, err
	}
	return result, nil
}

// ApplyCorrections mutates the document with the reviewer's edits, in a stable
// ID order so a merge is reproducible.
func ApplyCorrections(document *Ontology, corrections map[string]*Correction) error {
	if len(corrections) == 0 {
		return nil
	}
	ids := make([]string, 0, len(corrections))
	for id := range corrections {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		correction := corrections[id]
		if correction == nil {
			continue
		}
		node := document.Nodes[id]
		if correction.Delete {
			if node == nil {
				return fmt.Errorf("correction %q deletes an unknown node", id)
			}
			delete(document.Nodes, id)
			continue
		}
		if node == nil {
			if correction.Kind == nil || correction.Label == nil {
				return fmt.Errorf("correction %q adds a node without kind and label", id)
			}
			node = &Node{ID: id, Kind: *correction.Kind, Label: *correction.Label, Status: StatusProposed}
			document.Nodes[id] = node
		}
		node.apply(correction)
	}
	return nil
}

func (n *Node) apply(c *Correction) {
	if c.Kind != nil {
		n.Kind = *c.Kind
	}
	if c.Label != nil {
		n.Label = *c.Label
	}
	if c.Aliases != nil {
		n.Aliases = *c.Aliases
	}
	if c.HSCodes != nil {
		n.HSCodes = *c.HSCodes
	}
	if c.Parent != nil {
		n.Parent = *c.Parent
	}
	if c.Categories != nil {
		n.Categories = *c.Categories
	}
	if c.DerivedTo != nil {
		n.DerivedTo = *c.DerivedTo
	}
	if c.ConsumedIn != nil {
		n.ConsumedIn = *c.ConsumedIn
	}
	if c.OperatesOn != nil {
		n.OperatesOn = *c.OperatesOn
	}
	if c.Produces != nil {
		n.Produces = *c.Produces
	}
	if c.Consumes != nil {
		n.Consumes = *c.Consumes
	}
	if c.OperatorPhrases != nil {
		n.OperatorPhrases = *c.OperatorPhrases
	}
	if c.SearchTerms != nil {
		n.SearchTerms = *c.SearchTerms
	}
	if c.Hypernym != nil {
		n.Hypernym = *c.Hypernym
	}
	if c.Hyponyms != nil {
		n.Hyponyms = *c.Hyponyms
	}
	if c.IsIntermediate != nil {
		n.IsIntermediate = *c.IsIntermediate
	}
	if c.Status != nil {
		n.Status = *c.Status
	}
	if c.Source != nil {
		n.Source = *c.Source
	}
	if c.Confidence != nil {
		n.Confidence = *c.Confidence
	}
	if c.ExpertNote != nil {
		n.ExpertNote = *c.ExpertNote
	}
}

func (n *Node) clone() *Node {
	if n == nil {
		return nil
	}
	copied := *n
	copied.Aliases = cloneStrings(n.Aliases)
	copied.HSCodes = cloneStrings(n.HSCodes)
	copied.Categories = cloneStrings(n.Categories)
	copied.DerivedTo = cloneStrings(n.DerivedTo)
	copied.ConsumedIn = cloneStrings(n.ConsumedIn)
	copied.OperatesOn = cloneStrings(n.OperatesOn)
	copied.Produces = cloneStrings(n.Produces)
	copied.Consumes = cloneStrings(n.Consumes)
	copied.OperatorPhrases = cloneStrings(n.OperatorPhrases)
	copied.SearchTerms = cloneStrings(n.SearchTerms)
	copied.Hyponyms = cloneStrings(n.Hyponyms)
	return &copied
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

// NodeIDs returns the document's IDs in a stable order.
func (o *Ontology) NodeIDs() []string {
	ids := make([]string, 0, len(o.Nodes))
	for id := range o.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// NodesOfKind returns the IDs of one kind in a stable order.
func (o *Ontology) NodesOfKind(kind Kind) []string {
	var ids []string
	for id, node := range o.Nodes {
		if node != nil && node.Kind == kind {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Lookup resolves an entry name to node IDs by ID, label or alias (exact after
// normalization: lowercase, punctuation collapsed). The resolution API built in
// the next step uses this; keeping it here lets the review tooling report which
// entered names are already covered.
func (o *Ontology) Lookup(name string) []string {
	key := normalize(name)
	if key == "" {
		return nil
	}
	var matches []string
	for id, node := range o.Nodes {
		if node == nil {
			continue
		}
		if normalize(id) == key || normalize(node.Label) == key {
			matches = append(matches, id)
			continue
		}
		for _, alias := range node.Aliases {
			if normalize(alias) == key {
				matches = append(matches, id)
				break
			}
		}
	}
	sort.Strings(matches)
	return matches
}

// normalize reduces a name to its comparable form: lowercase alphanumerics with
// single spaces. "n-Butanol", "N Butanol" and "n butanol" collapse to one key,
// which is what makes alias uniqueness meaningful.
func normalize(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	gap := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			gap = false
		default:
			if !gap && b.Len() > 0 {
				b.WriteByte(' ')
				gap = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}
