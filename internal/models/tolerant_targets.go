package models

// tolerant_targets.go wires every structured-output model through the shared
// tolerant decoder (tolerant_decode.go). The rule of this file: the structs the
// agents decode straight from LLM output are listed here, one line each, so a
// model drifting on any single field costs that field -- not the synthesis.
//
// Structs with bespoke decoding keep their own methods: Location and
// CompanyProfile (location strings, numeric metrics), and the demand-chain
// reviews (which additionally convert bare-name rejections and stage lists).
// Everything else uses the reflective engine, which needs no per-field code.

// Company research. OrgChart keeps its bespoke method (company.go): it also
// accepts the alternate org-chart shape and formats a location object, and it
// calls the tolerant engine first.
func (employee *Employee) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, employee)
}
func (department *Department) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, department)
}
func (image *Image) UnmarshalJSON(data []byte) error        { return decodeTolerantStruct(data, image) }
func (plan *QueryPlan) UnmarshalJSON(data []byte) error     { return decodeTolerantStruct(data, plan) }
func (query *PlannedQuery) UnmarshalJSON(data []byte) error { return decodeTolerantStruct(data, query) }
func (result *RankedResult) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, result)
}

// Person research.
func (profile *PersonProfile) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, profile)
}
func (experience *Experience) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, experience)
}
func (education *Education) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, education)
}
func (account *PublicAccount) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, account)
}
func (evidence *PersonalizationEvidence) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, evidence)
}
func (signal *SocialSignal) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, signal)
}

// Legal research.
func (report *LegalReport) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, report)
}
func (finding *LegalFinding) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, finding)
}
func (risk *LegalRiskAssessment) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, risk)
}
func (source *LegalSourceReport) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, source)
}
func (person *LegalPersonResult) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, person)
}

// News research.
func (synthesis *NewsSynthesis) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, synthesis)
}
func (sentiment *NewsArticleSentiment) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, sentiment)
}
func (conflict *NewsConflict) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, conflict)
}
func (citation *NewsCitation) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, citation)
}

// Deep search.
func (plan *DeepSearchPlan) UnmarshalJSON(data []byte) error { return decodeTolerantStruct(data, plan) }
func (pair *DeepSearchQAPair) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, pair)
}
func (review *DeepSearchReview) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, review)
}
func (synthesis *DeepSearchSynthesis) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, synthesis)
}
func (finding *DeepSearchFinding) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, finding)
}

// Landscape and buyer modes.
func (stage *ValueChainStage) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, stage)
}
func (inference *MarketLandscapeInference) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, inference)
}
func (review *MarketQueryReview) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, review)
}
func (review *MarketLandscapeReview) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, review)
}
func (company *MarketLandscapeCompany) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, company)
}

// Reviews, answers and the deep-search judge. QualityReview keeps its bespoke
// method (research.go): it also converts bare follow-up strings, and it calls
// the tolerant engine first.
func (answer *QuestionAnswer) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, answer)
}
func (selection *DeepSearchSelection) UnmarshalJSON(data []byte) error {
	return decodeTolerantStruct(data, selection)
}
