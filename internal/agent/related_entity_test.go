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

func TestNormalizeRelatedRequiresEvidenceURL(t *testing.T) {
	items := normalizeRelated([]models.RelatedCompany{
		{Name: "No Source Co", Confidence: 0.9},
		{Name: "Sourced Co", Confidence: 0.8, Evidence: []models.RelatedEntityEvidence{{URL: "https://example.com"}}},
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
