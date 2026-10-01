package tools

import (
	"net/http"
	"strings"
	"testing"
)

// TestHardURLShapeOf pins the classification the escalation order and the step
// policy both depend on, using URLs from real runs.
func TestHardURLShapeOf(t *testing.T) {
	cases := []struct {
		url  string
		want HardURLShape
	}{
		{"https://in.linkedin.com/in/suresh-chand-jalther-9b103052", ShapeLinkedInPerson},
		{"https://www.linkedin.com/in/veena-shikhare-7ba623372", ShapeLinkedInPerson},
		{"https://www.linkedin.com/pub/dir/Pinal/+/in-7150-Mumbai-Area,-India/", ShapeLinkedInDirectory},
		{"https://www.linkedin.com/posts/vinod-trivedi-2835221a_announcement-activity-6771005891878625280-KOqH", ShapeLinkedInPost},
		{"https://www.linkedin.com/pulse/some-article", ShapeLinkedInPost},
		{"https://in.linkedin.com/company/klj-group-india", ShapeLinkedInCompany},
		{"https://www.linkedin.com/school/some-school/", ShapeLinkedInCompany},
		{"https://www.linkedin.com/jobs/view/1234", ShapeOther},
		{"https://kljresources.in/management.php", ShapeOther},
		{"https://rocketreach.co/klj-resources-ltd-management_b6a4ceb0c87f174d", ShapeOther},
	}
	for _, testCase := range cases {
		if got := HardURLShapeOf(testCase.url); got != testCase.want {
			t.Errorf("HardURLShapeOf(%s) = %s, want %s", testCase.url, got, testCase.want)
		}
	}
}

// TestEscalationPriorityPrefersRecoverableShapes pins the ordering contract: the
// shapes that render on the ladder's first step come before the auth-walled ones.
func TestEscalationPriorityPrefersRecoverableShapes(t *testing.T) {
	if !(ShapeLinkedInPost.EscalationPriority() < ShapeLinkedInCompany.EscalationPriority()) {
		t.Error("a public post must outrank a company page")
	}
	if !(ShapeLinkedInCompany.EscalationPriority() < ShapeLinkedInPerson.EscalationPriority()) {
		t.Error("a company page must outrank a person profile")
	}
	if !(ShapeLinkedInPerson.EscalationPriority() < ShapeLinkedInDirectory.EscalationPriority()) {
		t.Error("a person profile must outrank a name directory")
	}
	if !ShapeLinkedInPerson.AuthWalled() || !ShapeLinkedInDirectory.AuthWalled() {
		t.Error("person profiles and directories are the auth-walled shapes")
	}
	if ShapeLinkedInPost.AuthWalled() || ShapeLinkedInCompany.AuthWalled() || ShapeOther.AuthWalled() {
		t.Error("public shapes must not be treated as auth-walled")
	}
}

// TestHardLadderSteps pins the step policy, including the skip that keeps 20 s of
// extended settling from being spent on an authwall that time cannot clear.
func TestHardLadderSteps(t *testing.T) {
	person := "https://in.linkedin.com/in/someone-1234"
	directory := "https://www.linkedin.com/pub/dir/Pinal/+/in-7150-Mumbai-Area,-India/"
	post := "https://www.linkedin.com/posts/someone_topic-activity-6771005891878625280-KOqH"
	company := "https://in.linkedin.com/company/some-company"
	other := "https://example.com/prices"
	session := []*http.Cookie{{Name: "li_at", Value: "token", Domain: "linkedin.com", Path: "/"}}

	cases := []struct {
		name      string
		target    string
		cookies   []*http.Cookie
		rendered  bool
		wantSteps string
	}{
		// A session is the only thing that clears an authwall, so it leads — and the
		// logged-out render that cannot help is dropped rather than paid for. Measured live:
		// that render reached the sign-in page and was the step that triggered the redirect
		// loop on a real profile.
		{"logged-in profile goes straight to the session", person, session, false, "cookie-render → extended-settle"},
		{"logged-in directory too", directory, session, false, "cookie-render → extended-settle"},
		{"logged-out profile tries one step only", person, nil, false, "plain-render"},
		{"logged-out directory tries one step only", directory, nil, false, "plain-render"},
		{"logged-out post still gets the extended settle", post, nil, false, "plain-render → extended-settle"},
		{"logged-out company page too", company, nil, false, "plain-render → extended-settle"},
		// A public shape keeps the cheap step first: it often needs no session at all, and
		// the session is there if the page turns out to be blocked.
		{"logged-in post tries the plain render first", post, session, false, "plain-render → cookie-render → extended-settle"},
		{"non-LinkedIn URL is unaffected", other, nil, false, "plain-render → extended-settle"},
		{"already rendered blocked page skips the plain render", other, nil, true, "extended-settle"},
		{"already rendered profile with no session has nothing left", person, nil, true, ""},
		{"already rendered profile still uses the session", person, session, true, "cookie-render → extended-settle"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := stepNames(hardLadderSteps(testCase.target, testCase.cookies, testCase.rendered))
			if got != testCase.wantSteps {
				t.Fatalf("steps = %q, want %q", got, testCase.wantSteps)
			}
		})
	}
}

// TestNoCookieHint states the diagnostic the failure message now carries: the step
// list shows cookie-render was skipped, and the hint says why.
func TestNoCookieHint(t *testing.T) {
	if hint := noCookieHint("https://in.linkedin.com/in/someone", nil); hint == "" {
		t.Fatal("expected a hint when the jar has no cookies for the host")
	} else if !strings.Contains(hint, "in.linkedin.com") || !strings.Contains(hint, "COOKIE_FILE_PATH") {
		t.Fatalf("hint must name the host and the setting, got %q", hint)
	}
	if hint := noCookieHint("https://in.linkedin.com/in/someone", []*http.Cookie{{Name: "li_at", Value: "x"}}); hint != "" {
		t.Fatalf("no hint is needed when cookies were bridged, got %q", hint)
	}
}

// stepNames renders a step list for assertions.
func stepNames(steps []ladderStep) string {
	names := make([]string, 0, len(steps))
	for _, step := range steps {
		names = append(names, step.name)
	}
	return strings.Join(names, " → ")
}
