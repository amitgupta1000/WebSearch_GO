package tools

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestIsRedirectLoopRecognizesBothClients pins the signature both clients produce. Go's
// http.Client and Chromium word it differently, and a redirect storm is the one failure that
// means "your session was refused" rather than "this site is broken".
func TestIsRedirectLoopRecognizesBothClients(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"go client", errors.New(`Get "https://in.linkedin.com/in/x": stopped after 10 redirects`), true},
		{"chromium", errors.New("navigate browser page: playwright: net::ERR_TOO_MANY_REDIRECTS at https://in.linkedin.com/in/x"), true},
		{"deadline", errors.New("context deadline exceeded"), false},
		{"no error", nil, false},
	}
	for _, testCase := range cases {
		if got := IsRedirectLoop(testCase.err); got != testCase.want {
			t.Errorf("%s: IsRedirectLoop = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

// TestSessionRejectedErrorOnlyWhenASessionWasSent keeps the diagnosis honest: a redirect loop
// is only a credential problem when credentials were sent. A loop on a site fetched without a
// session is a site problem, and saying otherwise would send an operator to re-capture for
// nothing.
func TestSessionRejectedErrorOnlyWhenASessionWasSent(t *testing.T) {
	target := "https://in.linkedin.com/in/someone-1234"
	loop := errors.New(`Get "https://in.linkedin.com/in/someone-1234": stopped after 10 redirects`)

	anonymous := NewScraper(nil, time.Second)
	if err := anonymous.sessionRejectedError(target, loop); errors.Is(err, ErrSessionRejected) {
		t.Fatal("a redirect loop without a session is a site problem, not a credential problem")
	}

	seeded := NewScraper(jarWithSession(t, target, "li_at", "session-value"), time.Second)
	if !seeded.HasSessionCookies(target) {
		t.Fatal("expected the seeded jar to cover the host")
	}
	annotated := seeded.sessionRejectedError(target, loop)
	if !errors.Is(annotated, ErrSessionRejected) {
		t.Fatalf("expected a rejected-session classification, got %v", annotated)
	}
	if !strings.Contains(annotated.Error(), "re-capture") {
		t.Errorf("expected the message to say what to do, got %q", annotated.Error())
	}

	// An unrelated failure stays unrelated, however the run is authenticated.
	if err := seeded.sessionRejectedError(target, errors.New("context deadline exceeded")); errors.Is(err, ErrSessionRejected) {
		t.Error("only a redirect loop may be attributed to the session")
	}
}

// TestSessionFingerprintScopesCacheEntries covers the freshness model: the fingerprint is
// empty without a session (so ordinary pages keep sharing cache entries), non-empty with one,
// different for a different session, and a hash rather than the value.
func TestSessionFingerprintScopesCacheEntries(t *testing.T) {
	target := "https://in.linkedin.com/in/someone-1234"

	anonymous := NewScraper(nil, time.Second)
	if got := anonymous.SessionFingerprint(target); got != "" {
		t.Errorf("expected no fingerprint without a session, got %q", got)
	}
	if anonymous.HasSessionCookies(target) {
		t.Error("expected no session cookies for the host")
	}

	first := NewScraper(jarWithSession(t, target, "li_at", "session-one"), time.Second).SessionFingerprint(target)
	if first == "" {
		t.Fatal("expected a fingerprint once a session applies")
	}
	if strings.Contains(first, "session-one") {
		t.Error("the fingerprint must be a hash, never the cookie value")
	}

	second := NewScraper(jarWithSession(t, target, "li_at", "session-two"), time.Second).SessionFingerprint(target)
	if second == first {
		t.Fatal("expected a different session to produce a different fingerprint")
	}

	same := NewScraper(jarWithSession(t, target, "li_at", "session-one"), time.Second).SessionFingerprint(target)
	if same != first {
		t.Error("expected the fingerprint to be stable for the same session")
	}
}

// jarWithSession builds a jar holding one cookie for the target's host.
func jarWithSession(t *testing.T, target, name, value string) *cookiejar.Jar {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create jar: %v", err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse %s: %v", target, err)
	}
	jar.SetCookies(parsed, []*http.Cookie{{Name: name, Value: value, Path: "/"}})
	return jar
}
