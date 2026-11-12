package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestWeeDooRegistryDINFindsTheRowDIN pins the directors-table extraction: the
// DIN sits on the director's row ("Appointment | Name | DIN | Designation"),
// while dates and masked numbers never match, and only WeeDoo hits are scanned.
func TestWeeDooRegistryDINFindsTheRowDIN(t *testing.T) {
	registry := models.SearchResult{
		Title: "WeeDoo company page",
		Link:  "https://weedoo.in/company/KLJ-RESOURCES-LTD/L67120WB1986PLC041487",
		Snippet: "Directors\nAppointment\tDirector name\tDIN\tDesignation\n" +
			"10/10/1997\tDilip Kumar Karn\t00061804\tDirector\n" +
			"01/10/2005\tHemant Jain\t00506995\tManaging Director\n" +
			"16/01/2018\tSuresh Chand Jalther\t*****2690R\tCFO\n",
	}
	other := models.SearchResult{Title: "economictimes", Link: "https://economictimes.indiatimes.com/company/x", Snippet: "Hemant Jain 12345678"}
	if got := weeDooRegistryDIN([]models.SearchResult{other, registry}, "Hemant Jain"); got != "00506995" {
		t.Fatalf("expected the row DIN, got %q", got)
	}
	// Masked DINs and dates never match, even on the director's own row.
	if got := weeDooRegistryDIN([]models.SearchResult{registry}, "Suresh Chand Jalther"); got != "" {
		t.Fatalf("a masked DIN must not resolve, got %q", got)
	}
	// Non-WeeDoo hits are ignored.
	if got := weeDooRegistryDIN([]models.SearchResult{other}, "Hemant Jain"); got != "" {
		t.Fatalf("non-registry hits must not resolve a DIN, got %q", got)
	}
	// A plain-fetch extraction that strips newlines turns a whole page into one
	// line: every director's tokens match, and the first table DIN must not be
	// attributed to a director whose own row carries no DIN.
	mega := models.SearchResult{
		Title: "WeeDoo company page",
		Link:  "https://weedoo.in/company/OTHER/OTHERCIN",
		Snippet: "Directors Appointment Director name DIN Designation 10/10/1997 Dilip Kumar Karn 00061804 Director " +
			"16/01/2018 Suresh Chand Jalther *****2690R CFO",
	}
	if got := weeDooRegistryDIN([]models.SearchResult{mega}, "Suresh Chand Jalther"); got != "" {
		t.Fatalf("a distant DIN on a flattened page must not resolve, got %q", got)
	}
	if got := weeDooRegistryDIN([]models.SearchResult{mega}, "Dilip Kumar Karn"); got != "00061804" {
		t.Fatalf("expected the row DIN for the director it belongs to, got %q", got)
	}
}

func TestResolvePersonIdentityExtractsDINAndEvidence(t *testing.T) {
	query := models.RelatedEntityQuery{CompanyName: "KLJ Resources Limited"}
	identity := resolvePersonIdentity(
		models.LeadershipSubject{Name: "Sameer Jayavelu"},
		query,
		[]models.SearchResult{
			{Title: "KLJ Resources Limited director", Snippet: "Sameer Jayavelu, DIN 01234567, director of KLJ Resources Limited", Link: "https://example.test/one"},
			{Title: "Corporate filing", Snippet: "Sameer Jayavelu appointment at KLJ Resources Limited", Link: "https://example.test/two"},
		},
	)
	if identity.DIN != "01234567" {
		t.Fatalf("DIN = %q, want 01234567", identity.DIN)
	}

	if identity.Confidence < relatedEntityIdentityConfidence {
		t.Fatalf("confidence = %v, want at least %v", identity.Confidence, relatedEntityIdentityConfidence)
	}
	if len(identity.Evidence) != 2 || !strings.Contains(identity.Attributes[0], "company") {
		t.Fatalf("identity evidence/attributes = %+v", identity)
	}
}

func TestRelatedLeadershipExtractionAcceptsArray(t *testing.T) {
	var extraction relatedLeadershipExtraction
	if err := json.Unmarshal([]byte(`[{"name":"Hemant Jain","role":"Managing Director"}]`), &extraction); err != nil {
		t.Fatalf("unmarshal leadership array: %v", err)
	}
	if len(extraction.Leadership) != 1 || extraction.Leadership[0].Name != "Hemant Jain" {
		t.Fatalf("leadership = %+v", extraction.Leadership)
	}
}

func TestRocketReachEmployerMatchesRejectsFuzzyUnrelatedEmployer(t *testing.T) {
	if !rocketReachEmployerMatches("KLJ Resources", "KLJ Resources Limited") {
		t.Fatal("expected abbreviated legal suffix to match")
	}

	if rocketReachEmployerMatches("Fasaol Resources Limited", "KLJ Resources Limited") {
		t.Fatal("expected unrelated fuzzy employer to be rejected")
	}
}

// TestNormalizeLeadersMergesInitialsVariants pins the leadership rule: "K L
// Jain" and "Kanhaiya Lal Jain" are one director, not two — the fuller name
// stands as the display name, and the variant does not spawn its own branch.
func TestNormalizeLeadersMergesInitialsVariants(t *testing.T) {
	evidence := func(url string) []models.RelatedEntityEvidence {
		return []models.RelatedEntityEvidence{{URL: url, Title: "board listing", Snippet: "director of the company"}}
	}
	leaders := normalizeLeaders([]models.LeadershipSubject{
		{Name: "K L Jain", Role: "Director", Evidence: evidence("https://weedoo.in/company/KLJ-RESOURCES-LTD/L67120WB1986PLC041487")},
		{Name: "Kanhaiya Lal Jain", Role: "Director", Evidence: evidence("https://economictimes.indiatimes.com/company/klj-resources-ltd/L67120WB1986PLC041487")},
		{Name: "Hemant Jain", Role: "Managing Director", Evidence: evidence("https://weedoo.in/director/HEMANT-JAIN/00506995")},
	}, "KLJ Resources Limited", 8)

	if len(leaders) != 2 {
		t.Fatalf("expected the initials variant merged into one leader, got %+v", leaders)
	}
	if leaders[0].Name != "Kanhaiya Lal Jain" {
		t.Fatalf("expected the fuller name as the display name, got %q", leaders[0].Name)
	}
	if leaders[1].Name != "Hemant Jain" {
		t.Fatalf("expected the unrelated leader kept, got %+v", leaders[1])
	}
}

func TestNormalizeRelatedRequiresEvidenceURL(t *testing.T) {
	items := normalizeRelated([]models.RelatedCompany{
		{Name: "No Source Co", Confidence: 0.9},
		{Name: "Sourced Co", Confidence: 0.8, Evidence: []models.RelatedEntityEvidence{{URL: "https://example.com"}}},
		{Name: "India Chemicals Market Research Report", Confidence: 0.9, Evidence: []models.RelatedEntityEvidence{{URL: "https://example.com/report"}}},
	}, "Primary Co")
	if len(items) != 1 || items[0].Name != "Sourced Co" {
		t.Fatalf("expected only URL-backed relationship, got %+v", items)
	}
}

func TestResolvePersonIdentityDoesNotPromoteNameOnlyEvidence(t *testing.T) {
	identity := resolvePersonIdentity(
		models.LeadershipSubject{Name: "Sameer Jayavelu"},
		models.RelatedEntityQuery{CompanyName: "KLJ Resources Limited"},
		[]models.SearchResult{{Title: "Sameer Jayavelu", Snippet: "Professional profile", Link: "https://example.test/profile"}},
	)
	if identity.DIN != "" || identity.Confidence >= relatedEntityIdentityConfidence {
		t.Fatalf("name-only evidence was promoted: %+v", identity)
	}
}

// TestRelatedExtractionToleratesEvidenceShapes pins the decode half of the fix:
// the model may answer with the wrapped object or the bare array, and evidence
// may arrive as object arrays, plain strings, or not at all — none of which may
// fail the extraction.
func TestRelatedExtractionToleratesEvidenceShapes(t *testing.T) {
	payloads := []string{
		`{"direct_relationships":[{"name":"KLJ Organic Thailand Limited","relationship":"subsidiary","evidence":[{"url":"https://example.com/th","snippet":"subsidiary of KLJ Resources Limited"}],"confidence":"high"}]}`,
		`[{"name":"KLJ Organic Thailand Limited","relationship":"subsidiary","evidence":"subsidiary of KLJ Resources","confidence":0.8}]`,
		`[{"name":"KLJ Organic Thailand Limited","relationship":"subsidiary"}]`,
	}
	for index, payload := range payloads {
		var extraction relatedDirectExtraction
		if err := json.Unmarshal([]byte(payload), &extraction); err != nil {
			t.Fatalf("payload %d: unmarshal: %v", index+1, err)
		}
		if len(extraction.DirectRelationships) != 1 || extraction.DirectRelationships[0].Name != "KLJ Organic Thailand Limited" {
			t.Fatalf("payload %d: relationships = %+v", index+1, extraction.DirectRelationships)
		}
		if company := extraction.DirectRelationships[0].RelatedCompany(); company.Name != "KLJ Organic Thailand Limited" {
			t.Fatalf("payload %d: conversion = %+v", index+1, company)
		}
	}
}

// TestAttachEvidenceFromHitsPrefersCompanyCorroboration pins the deterministic
// rescue: a hit naming both the claim and the primary company wins, an uncited
// claim no hit mentions stays evidence-free (and is therefore dropped by the
// normalizer), and existing URL evidence is never replaced.
func TestAttachEvidenceFromHitsPrefersCompanyCorroboration(t *testing.T) {
	hits := []models.SearchResult{
		{Title: "Related firm only", Link: "https://example.com/only-related", Snippet: "KLJ Group of Industries announced results."},
		{Title: "Both companies", Link: "https://example.com/both", Snippet: "KLJ Resources Limited is the flagship company of KLJ Group of Industries."},
	}
	evidence := attachEvidenceFromHits(nil, "KLJ Group of Industries", "KLJ Resources Limited", hits)
	if len(evidence) != 1 || evidence[0].URL != "https://example.com/both" {
		t.Fatalf("expected the hit naming both companies, got %+v", evidence)
	}
	if got := attachEvidenceFromHits(nil, "Nowhere Ltd", "KLJ Resources Limited", hits); got != nil {
		t.Fatalf("a claim no hit mentions must stay evidence-free, got %+v", got)
	}
	kept := []models.RelatedEntityEvidence{{URL: "https://example.com/kept"}}
	if got := attachEvidenceFromHits(kept, "KLJ Group of Industries", "KLJ Resources Limited", hits); len(got) != 1 || got[0].URL != "https://example.com/kept" {
		t.Fatalf("existing URL evidence must be untouched, got %+v", got)
	}
}

// relatedExtractionLLM scripts the model behaviour a live KLJ Resources Limited
// run exposed: the relationships are named but the evidence citation the
// normalizer requires is missing or URL-less.
type relatedExtractionLLM struct{}

func (l *relatedExtractionLLM) GenerateStructured(_ context.Context, _, _ string, target any) error {
	switch value := target.(type) {
	case *relatedDirectExtraction:
		value.DirectRelationships = []relatedCompanyExtraction{{
			Name:         "KLJ Group of Industries",
			Relationship: "group",
			Evidence:     json.RawMessage(`"flagship company of KLJ Group of Industries"`),
		}}
	case *relatedLeadershipExtraction:
		value.Leadership = []relatedLeaderExtraction{{
			Name: "Hemant Jain",
			Role: "Managing Director",
		}}
	default:
		return fmt.Errorf("unexpected target %T", target)
	}
	return nil
}

// TestExtractRescuesEvidenceFromHitsWhenTheModelOmitsIt runs the extraction end
// to end: even when the model forgets the evidence citation, relationships the
// supplied hits actually state survive with the hit attached, and the report is
// no longer empty.
func TestExtractRescuesEvidenceFromHitsWhenTheModelOmitsIt(t *testing.T) {
	store := cache.NewMemoryCache(10)
	t.Cleanup(func() { store.Close() })
	engine := NewEngine(&relatedExtractionLLM{}, &mockSearcher{}, &mockScraper{}, store, 5, 3, 10)
	agent := NewRelatedEntityAgent(engine)
	query := models.RelatedEntityQuery{CompanyName: "KLJ Resources Limited", MaxLeaders: 4}
	report := &models.RelatedEntityReport{CompanyName: query.CompanyName, SearchTrace: &models.SearchTrace{}}
	directHits := []models.SearchResult{{
		Title:   "KLJ Resources Limited company profile",
		Link:    "https://www.example.com/klj",
		Snippet: "KLJ Resources Limited is the flagship company of KLJ Group of Industries, trading in petrochemicals.",
	}}
	leaderHits := []models.SearchResult{{
		Title:   "Mr. Hemant Jain, Managing Director of KLJ Resources Limited",
		Link:    "https://www.example.com/hemant",
		Snippet: "Mr. Hemant Jain, Managing Director of KLJ Resources Limited, spoke at the Suntek Leadership Forum 2024.",
	}}

	if err := agent.extract(context.Background(), query, directHits, leaderHits, report); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(report.DirectRelationships) != 1 || report.DirectRelationships[0].Name != "KLJ Group of Industries" {
		t.Fatalf("expected the direct relationship to survive, got %+v", report.DirectRelationships)
	}
	if len(report.DirectRelationships[0].Evidence) == 0 || !strings.Contains(report.DirectRelationships[0].Evidence[0].URL, "example.com/klj") {
		t.Fatalf("expected the rescued relationship to cite the hit URL, got %+v", report.DirectRelationships[0].Evidence)
	}
	if len(report.Leadership) != 1 || report.Leadership[0].Name != "Hemant Jain" {
		t.Fatalf("expected Hemant Jain to survive, got %+v", report.Leadership)
	}
	if len(report.Leadership[0].Evidence) == 0 || !strings.Contains(report.Leadership[0].Evidence[0].URL, "example.com/hemant") {
		t.Fatalf("expected the leader to carry rescued URL evidence, got %+v", report.Leadership[0].Evidence)
	}
}

// TestWeeDooRegistryHitsConstructsAndGates pins the related-entity registry
// enrichment: the WeeDoo company page is constructed from the CIN the run's
// hits published, its content feeds both extractions, the trace records it —
// and a fetched page that does not name the company is dropped.
func TestWeeDooRegistryHitsConstructsAndGates(t *testing.T) {
	store := cache.NewMemoryCache(10)
	t.Cleanup(func() { store.Close() })
	query := models.RelatedEntityQuery{CompanyName: "KLJ Resources Limited"}
	trace := &models.SearchTrace{Queries: []models.QueryTrace{{
		Query: `site:weedoo.in "KLJ Resources Limited"`,
		Kept: []models.SearchResult{{
			Title: "KLJ Resources Ltd", Link: "https://economictimes.indiatimes.com/company/klj-resources-ltd/L67120WB1986PLC041487", Snippet: "KLJ Resources Limited",
		}},
	}}}

	registryAgent := NewRelatedEntityAgent(NewEngine(&mockLLM{}, &mockSearcher{}, &registryScraperMock{}, store, 5, 3, 10))
	hits, network := registryAgent.weeDooRegistryHits(context.Background(), "sess_test", query, trace, nil)
	if len(hits) != 1 {
		t.Fatalf("expected the constructed registry page as a hit, got %+v", hits)
	}
	if hits[0].Link != "https://weedoo.in/company/KLJ-RESOURCES-LIMITED/L67120WB1986PLC041487" {
		t.Fatalf("unexpected constructed URL: %q", hits[0].Link)
	}
	if len(network) != 0 {
		t.Fatalf("a page without a Business Network section must not produce network hits, got %+v", network)
	}
	if len(trace.Queries) != 2 || !strings.Contains(trace.Queries[1].Purpose, "WeeDoo registry enrichment") {
		t.Fatalf("expected the enrichment recorded on the trace, got %+v", trace.Queries)
	}

	// A fetched page that does not name the company is dropped. A separate cache
	// store, so the registry agent's fetched page is not replayed from cache.
	store2 := cache.NewMemoryCache(10)
	t.Cleanup(func() { store2.Close() })
	plainAgent := NewRelatedEntityAgent(NewEngine(&mockLLM{}, &mockSearcher{}, &mockScraper{}, store2, 5, 3, 10))
	trace2 := &models.SearchTrace{Queries: append([]models.QueryTrace(nil), trace.Queries[:1]...)}
	if registry, network := plainAgent.weeDooRegistryHits(context.Background(), "sess_test", query, trace2, nil); registry != nil || network != nil {
		t.Fatalf("a page that does not name the company must stay out, got %+v / %+v", registry, network)
	}
}

// TestWeeDooDirectorHitBuildsURLOnlyWithDIN pins the per-leader enrichment:
// the director page is constructed from the resolved DIN and the director's
// name slug, and is skipped entirely without a DIN.
// TestWeeDooRegistryHitsReturnsTheBusinessNetwork pins the first-option rule:
// the Business Network's connected companies (Direct/Ultimate children with
// their LEI codes) become a network hit recorded on the trace — direct related
// entities that need no person search at all.
func TestWeeDooRegistryHitsReturnsTheBusinessNetwork(t *testing.T) {
	store := cache.NewMemoryCache(10)
	t.Cleanup(func() { store.Close() })
	query := models.RelatedEntityQuery{CompanyName: "KLJ Resources Limited"}
	trace := &models.SearchTrace{Queries: []models.QueryTrace{{
		Query: `site:weedoo.in "KLJ Resources Limited"`,
		Kept: []models.SearchResult{{
			Title: "KLJ Resources Ltd", Link: "https://economictimes.indiatimes.com/company/klj-resources-ltd/L67120WB1986PLC041487", Snippet: "KLJ Resources Limited",
		}},
	}}}

	networkScraper := networkScraperMock{}
	agent := NewRelatedEntityAgent(NewEngine(&mockLLM{}, &mockSearcher{}, &networkScraper, store, 5, 3, 10))
	hits, network := agent.weeDooRegistryHits(context.Background(), "sess_test", query, trace, nil)
	if len(hits) != 1 || len(network) != 1 {
		t.Fatalf("expected the registry page plus the Business Network hit, got %+v / %+v", hits, network)
	}
	if !strings.Contains(network[0].Snippet, "Direct: Lakeland Chemicals (india) Limited") ||
		!strings.Contains(network[0].Snippet, "Ultimate: Klj Resources Pte. Ltd.") {
		t.Fatalf("expected the connected companies in the network hit, got %q", network[0].Snippet)
	}
	if len(trace.Queries) != 3 || !strings.Contains(trace.Queries[2].Purpose, "WeeDoo business network") {
		t.Fatalf("expected the network hit recorded on the trace, got %+v", trace.Queries)
	}
}

// networkScraperMock serves a WeeDoo company page whose rendered content
// carries the directors table and the Business Network's Direct/Ultimate
// children table.
type networkScraperMock struct{}

func (m *networkScraperMock) ScrapeURL(ctx context.Context, targetURL string) (*models.ScrapeResult, error) {
	content := "KLJ Resources Limited\n" +
		"Directors\nAppointment\tDirector name\tDIN\tDesignation\n" +
		"01/10/2005\tHemant Jain\t00506995\tManaging Director\n" +
		"Business Network\nCurrent\n8\nOwnership\n3\n" +
		"Showing 11 of 11 connections. See the full lists below.\n" +
		"Directors\n8\nHemant Jain\nManaging Director\n" +
		"Subsidiaries\n3\nLakeland Chemicals (india) Limited\nDirect\n" +
		"Direct/Ultimate Children\nType\tLegal Name\tLEI code\n" +
		"Direct\tLakeland Chemicals (india) Limited\t335800VMK56Z6EJD3721\n" +
		"Ultimate\tKlj Resources Pte. Ltd.\t98450065CO9D775C9C26\n"
	return &models.ScrapeResult{URL: targetURL, Title: "KLJ Resources Ltd — WeeDoo company page", Content: content}, nil
}

func TestWeeDooDirectorHitBuildsURLOnlyWithDIN(t *testing.T) {
	store := cache.NewMemoryCache(10)
	t.Cleanup(func() { store.Close() })
	agent := NewRelatedEntityAgent(NewEngine(&mockLLM{}, &mockSearcher{}, &registryScraperMock{}, store, 5, 3, 10))

	hit := agent.weeDooDirectorHit(context.Background(), "Hemant Jain", "00506995")
	if hit == nil {
		t.Fatal("expected the director page hit")
	}
	if hit.Link != "https://weedoo.in/director/HEMANT-JAIN/00506995" {
		t.Fatalf("unexpected director page URL: %q", hit.Link)
	}
	if agent.weeDooDirectorHit(context.Background(), "Hemant Jain", "") != nil {
		t.Fatal("without a DIN there is no director page to construct")
	}
}

// TestWeeDooNetworkChildrenParsesTheConnectedCompanies pins the Business
// Network parse: the Direct/Ultimate Children table wins (name, relation, LEI),
// the Subsidiaries list is the fallback, and noise never becomes a company.
func TestWeeDooNetworkChildrenParsesTheConnectedCompanies(t *testing.T) {
	page := "Direct/Ultimate Children\nType\tLegal Name\tLEI code\n" +
		"Direct\tLakeland Chemicals (india) Limited\t335800VMK56Z6EJD3721\n" +
		"Ultimate\tKlj Resources Pte. Ltd.\t98450065CO9D775C9C26\n" +
		"Address\n8 Cammac Street, Kolkata"
	children := weeDooNetworkChildren(page)
	if len(children) != 2 ||
		children[0].Name != "Lakeland Chemicals (india) Limited" || children[0].Relation != "Direct" || children[0].LEI != "335800VMK56Z6EJD3721" ||
		children[1].Name != "Klj Resources Pte. Ltd." || children[1].Relation != "Ultimate" {
		t.Fatalf("unexpected children: %+v", children)
	}

	fallback := "Business Network\nCurrent\n8\nOwnership\n3\n" +
		"Showing 11 of 11 connections. See the full lists below.\n" +
		"Subsidiaries\n3\nLakeland Chemicals (india) Limited\nDirect\n" +
		"Klj Resources Pte. Ltd.\nUltimate\n" +
		"Latest Documents\nCategory:"
	if children := weeDooNetworkChildren(fallback); len(children) != 2 ||
		children[0].Name != "Lakeland Chemicals (india) Limited" || children[1].Relation != "Ultimate" {
		t.Fatalf("unexpected Subsidiaries fallback children: %+v", children)
	}

	if children := weeDooNetworkChildren("Business Network\nCurrent\n8\nOpen full map\nDirectors\n8\nHemant Jain\nManaging Director\n"); len(children) != 0 {
		t.Fatalf("a page without child companies must not produce children, got %+v", children)
	}
}

// TestFrozenLeaderDINFreezesTheIdentityBeforeSearching pins the freeze order:
// the directors-table row freezes the (name, DIN) pair first, a labelled DIN in
// retained evidence freezes it second, and an unfrozen leader stays unfrozen —
// name-only searching then must not run.
func TestFrozenLeaderDINFreezesTheIdentityBeforeSearching(t *testing.T) {
	registry := []models.SearchResult{{
		Title: "WeeDoo company page",
		Link:  "https://weedoo.in/company/KLJ-RESOURCES-LTD/L67120WB1986PLC041487",
		Snippet: "Directors\nAppointment\tDirector name\tDIN\tDesignation\n" +
			"12/08/2022\tKamlendra Navratansingh Chauhan\t09695105\tWhole-time director\n" +
			"16/01/2018\tSuresh Chand Jalther\t*****2690R\tCFO\n",
	}}
	if got := frozenLeaderDIN(registry, nil, "Kamlendra Navratansingh Chauhan"); got != "09695105" {
		t.Fatalf("expected the table row to freeze the DIN, got %q", got)
	}
	// The masked row freezes nothing, but a labelled DIN in retained evidence
	// does — when it sits next to the leader's name.
	retained := []models.SearchResult{{
		Title:   "Suresh Chand Jalther company appointments",
		Link:    "https://www.tofler.in/suresh-chand-jalther/din/06961204",
		Snippet: "Suresh Chand Jalther, DIN 06961204, director on two boards.",
	}}
	if got := frozenLeaderDIN(registry, retained, "Suresh Chand Jalther"); got != "06961204" {
		t.Fatalf("expected the labelled retained DIN to freeze, got %q", got)
	}
	// No table row, no labelled evidence: the leader stays unfrozen.
	if got := frozenLeaderDIN(registry, nil, "Suresh Chand Jalther"); got != "" {
		t.Fatalf("a masked row must not freeze a DIN, got %q", got)
	}
	// Anchoring puts the frozen DIN into every person query.
	anchored := anchorPersonQueries([]relatedPlannedQuery{
		{Query: `"K L Jain" "KLJ Resources Limited" appointment director`, Purpose: "person-company appointment"},
	}, "09695105")
	if len(anchored) != 1 || !strings.Contains(anchored[0].Query, `"09695105"`) {
		t.Fatalf("expected the DIN anchored into the query, got %+v", anchored)
	}
}
