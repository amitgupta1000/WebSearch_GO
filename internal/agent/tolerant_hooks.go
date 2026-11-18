package agent

// tolerant_hooks.go wires the agent package's own LLM target types through the
// models tolerant decoder (models.DecodeTolerant): the same drift contract as
// the models-package targets -- one ill-typed field costs that field, never the
// synthesis. Anonymous response structs declared inline at their call sites
// cannot carry methods; their named element types are hooked instead, and the
// models engine walks those elements generically.
//
// The related-entity extraction types keep their bespoke methods
// (related_entity.go): they also normalise relationship labels and evidence
// citations, which the generic walker must not replace.

import "github.com/amitgupta1000/WebSearch_GO/internal/models"

// Legal mode: director extraction and the case classification.
func (target *directorExtractionTarget) UnmarshalJSON(data []byte) error {
	return models.DecodeTolerant(data, target)
}

func (target *legalClassificationTarget) UnmarshalJSON(data []byte) error {
	return models.DecodeTolerant(data, target)
}

// Company mode: document triage decisions.
func (decision *documentTriageDecision) UnmarshalJSON(data []byte) error {
	return models.DecodeTolerant(data, decision)
}

func (info *documentPeriodInfo) UnmarshalJSON(data []byte) error {
	return models.DecodeTolerant(data, info)
}
