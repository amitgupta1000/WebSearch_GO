package agent

import (
	"encoding/json"
	"strings"
	"testing"

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
