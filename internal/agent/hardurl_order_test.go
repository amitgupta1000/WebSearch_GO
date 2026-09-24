package agent

import (
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// TestOrderProfileEscalationsPrefersRecoverableShapes pins the ordering that makes
// the escalation cap (HARD_URLS_PER_RUN, default 2) spend its slots on pages the
// ladder can actually read: posts and company pages first, person profiles and name
// directories last, with the ranker's order kept inside each shape.
func TestOrderProfileEscalationsPrefersRecoverableShapes(t *testing.T) {
	post := "https://www.linkedin.com/posts/someone_topic-activity-6771005891878625280-KOqH"
	company := "https://in.linkedin.com/company/some-company"
	person := "https://in.linkedin.com/in/someone-1234"
	personTwo := "https://www.linkedin.com/in/veena-shikhare-7ba623372"
	directory := "https://www.linkedin.com/pub/dir/Pinal/+/in-7150-Mumbai-Area,-India/"

	ranked := []models.RankedResult{
		{URL: person, SourceType: "professional_profile", Score: 0.95},
		{URL: "https://example.com/prices", SourceType: "external", Score: 0.9},
		{URL: directory, SourceType: "professional_profile", Score: 0.85},
		{URL: post, SourceType: "professional_profile", Score: 0.8},
		{URL: personTwo, SourceType: "professional_profile", Score: 0.75},
		{URL: company, SourceType: "professional_profile", Score: 0.7},
	}

	ordered := orderProfileEscalations(ranked)
	want := []string{post, company, person, personTwo, directory}
	if len(ordered) != len(want) {
		t.Fatalf("expected %d profile candidates, got %d (%v)", len(want), len(ordered), urlsOf(ordered))
	}
	for index, expected := range want {
		if ordered[index].URL != expected {
			t.Fatalf("escalation order = %v, want %v", urlsOf(ordered), want)
		}
	}
}

// urlsOf renders candidate URLs for failure messages.
func urlsOf(candidates []models.RankedResult) []string {
	urls := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		urls = append(urls, candidate.URL)
	}
	return urls
}
