package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
	"github.com/amitgupta1000/WebSearch_GO/internal/config"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/testutil"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
	"github.com/amitgupta1000/WebSearch_GO/internal/web"
)

func TestHandleEventsTerminatesAfterFailedSession(t *testing.T) {
	templates, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	hub := agent.NewSessionHub()
	session := hub.Create("failed-session", "company", "Acme")
	session.Complete(nil, errors.New("company search failed"))

	server := &Server{
		cfg:  &config.Config{},
		hub:  hub,
		tmpl: templates,
	}
	request := httptest.NewRequest("GET", "/api/events?session_id=failed-session", nil)
	response := httptest.NewRecorder()
	server.handleEvents(response, request)

	body := response.Body.String()
	if !strings.Contains(body, "company search failed") {
		t.Fatalf("expected failure event in SSE response, got %q", body)
	}
	if !strings.Contains(body, "event: close") {
		t.Fatalf("expected close event in SSE response, got %q", body)
	}
}

func TestRequestedBrowserOverrideParsing(t *testing.T) {
	form := func(value string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/research", strings.NewReader("use_browser="+value))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	for _, value := range []string{"1", "true", "TRUE", "on", "yes"} {
		if enabled, explicit := requestedBrowserOverride(form(value)); !enabled || !explicit {
			t.Errorf("use_browser=%q should opt in, got enabled=%t explicit=%t", value, enabled, explicit)
		}
	}
	for _, value := range []string{"0", "false", "off", "no"} {
		if enabled, explicit := requestedBrowserOverride(form(value)); enabled || !explicit {
			t.Errorf("use_browser=%q should opt out explicitly, got enabled=%t explicit=%t", value, enabled, explicit)
		}
	}
	if _, explicit := requestedBrowserOverride(form("")); explicit {
		t.Error("an absent choice must leave the deployment default in place")
	}
}

func TestBrowserChoiceHonoursPolicyAndAvailability(t *testing.T) {
	request := func(value string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/rerun", strings.NewReader("use_browser="+value))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}

	// Toggle disabled by the operator: the client value is ignored entirely.
	disabled := &Server{allowBrowserToggle: false, browserAvailable: true}
	if enabled, explicit, err := disabled.browserChoice(request("1")); err != nil || enabled || explicit {
		t.Fatalf("a disabled toggle must ignore client input, got enabled=%t explicit=%t err=%v", enabled, explicit, err)
	}

	// Chromium missing: asking for it is an explicit error, never a silent downgrade.
	unavailable := &Server{allowBrowserToggle: true, browserAvailable: false}
	if _, _, err := unavailable.browserChoice(request("1")); err == nil {
		t.Fatal("expected an error when browser rendering is requested but unavailable")
	}
	if enabled, explicit, err := unavailable.browserChoice(request("0")); err != nil || enabled || !explicit {
		t.Fatalf("an explicit opt-out must still be accepted, got enabled=%t explicit=%t err=%v", enabled, explicit, err)
	}

	// Available: both directions are honoured.
	available := &Server{allowBrowserToggle: true, browserAvailable: true}
	if enabled, explicit, err := available.browserChoice(request("1")); err != nil || !enabled || !explicit {
		t.Fatalf("expected an explicit opt-in, got enabled=%t explicit=%t err=%v", enabled, explicit, err)
	}
	if enabled, explicit, err := available.browserChoice(request("0")); err != nil || enabled || !explicit {
		t.Fatalf("expected an explicit opt-out, got enabled=%t explicit=%t err=%v", enabled, explicit, err)
	}
	if _, explicit, err := available.browserChoice(request("")); err != nil || explicit {
		t.Fatalf("absent choice must not override the default, got explicit=%t err=%v", explicit, err)
	}
}

func TestHandleRerunWithoutSessionRendersError(t *testing.T) {
	templates, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	server := &Server{
		cfg:                &config.Config{},
		hub:                agent.NewSessionHub(),
		tmpl:               templates,
		allowBrowserToggle: true,
	}

	// httptest only parses the body as a form when the content type says so.
	post := func(body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/rerun", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}

	response := httptest.NewRecorder()
	server.handleRerun(response, post(""))
	if !strings.Contains(response.Body.String(), "research session is required") {
		t.Fatalf("expected a session-required error, got %q", response.Body.String())
	}

	// An unknown session must not start a run.
	response = httptest.NewRecorder()
	server.handleRerun(response, post("session_id=sess_missing"))
	if !strings.Contains(response.Body.String(), "no longer available") {
		t.Fatalf("expected an expired-session error, got %q", response.Body.String())
	}
}

func TestWithBrowserOverrideAttachesChoiceToContext(t *testing.T) {
	base := context.Background()
	if _, ok := tools.BrowserFallbackOverride(base); ok {
		t.Error("a default run must leave the scraper's configured setting in place")
	}
	if enabled, ok := tools.BrowserFallbackOverride(withBrowserOverride(base, true, true)); !ok || !enabled {
		t.Error("expected the opt-in to reach the scraper context")
	}
	if enabled, ok := tools.BrowserFallbackOverride(withBrowserOverride(base, false, true)); !ok || enabled {
		t.Error("expected the opt-out to reach the scraper context")
	}
	if _, ok := tools.BrowserFallbackOverride(withBrowserOverride(base, true, false)); ok {
		t.Error("a non-explicit choice must not override the deployment default")
	}
}

func TestSecurityHeadersAllowRemoteImages(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "img-src 'self' data: https: http:") {
		t.Errorf("the scraped-image gallery needs remote images (http and https) to render, got %q", csp)
	}
	// Widening img-src must not loosen anything else.
	if strings.Contains(csp, "img-src *") {
		t.Errorf("img-src must stay scoped to remote image schemes, got %q", csp)
	}
	for _, directive := range []string{
		"default-src 'self'",
		"style-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com",
		"connect-src 'self'",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("expected %q to remain unchanged, got %q", directive, csp)
		}
	}
	// The image exception must not leak into fetchable or executable directives.
	if strings.Contains(csp, "connect-src 'self' http:") || strings.Contains(csp, "script-src 'self' 'unsafe-inline' http:") {
		t.Errorf("only img-src may allow plain http, got %q", csp)
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("expected nosniff to still be set, got %q", got)
	}
	if got := response.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("referrer policy limits image tracking and must stay, got %q", got)
	}
}

// stubDocumentReader stands in for the LLM client so the PDF extraction policy can be
// exercised without a live model.
type stubDocumentReader struct {
	text  string
	err   error
	calls int
}

func (r *stubDocumentReader) ExtractDocumentText(context.Context, string, []byte) (string, error) {
	r.calls++
	return r.text, r.err
}

func TestDocumentTextExtractorPrefersGemini(t *testing.T) {
	reader := &stubDocumentReader{text: "Officers: A. Patel\n"}
	text, extractor, err := documentTextExtractor(reader)(context.Background(), "application/pdf", testutil.MinimalPDF("officers"))
	if err != nil {
		t.Fatalf("documentTextExtractor: %v", err)
	}
	if extractor != tools.ExtractorPDFGemini {
		t.Errorf("extractor = %q, want %q", extractor, tools.ExtractorPDFGemini)
	}
	if text != "Officers: A. Patel\n" {
		t.Errorf("text = %q, want the model output", text)
	}
	if reader.calls != 1 {
		t.Errorf("model calls = %d, want 1", reader.calls)
	}
}

func TestDocumentTextExtractorFallsBackToLocalParser(t *testing.T) {
	// Gemini can be rate-limited or unavailable; a PDF with a text layer must still
	// become evidence, which is why the local parser is wired in behind it.
	reader := &stubDocumentReader{err: errors.New("429 rate limited")}
	text, extractor, err := documentTextExtractor(reader)(context.Background(), "application/pdf", testutil.MinimalPDF("Acme Holdings Limited officers"))
	if err != nil {
		t.Fatalf("expected the local parser to recover the text: %v", err)
	}
	if extractor != tools.ExtractorPDFLocal {
		t.Errorf("extractor = %q, want %q", extractor, tools.ExtractorPDFLocal)
	}
	if !strings.Contains(text, "Acme Holdings Limited officers") {
		t.Errorf("local text = %q, want the document text", text)
	}
}

func TestDocumentTextExtractorTreatsBlankModelOutputAsFailure(t *testing.T) {
	reader := &stubDocumentReader{text: " \n\t "}
	text, extractor, err := documentTextExtractor(reader)(context.Background(), "application/pdf", testutil.MinimalPDF("Acme Holdings Limited officers"))
	if err != nil {
		t.Fatalf("expected the local parser to recover the text: %v", err)
	}
	if extractor != tools.ExtractorPDFLocal {
		t.Errorf("extractor = %q, want a fallback to the local parser", extractor)
	}
	if !strings.Contains(text, "Acme Holdings Limited") {
		t.Errorf("text = %q, want the local text", text)
	}
}

func TestDocumentTextExtractorReportsBothFailures(t *testing.T) {
	reader := &stubDocumentReader{err: errors.New("model refused the document")}
	_, _, err := documentTextExtractor(reader)(context.Background(), "application/pdf", []byte("%PDF-1.4\nbroken"))
	if err == nil {
		t.Fatal("expected an error when both extractors fail")
	}
	for _, want := range []string{"gemini", "local parsing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got %v", want, err)
		}
	}
}

// TestIsAddrInUseClassifiesPortCollisions pins the message that keeps a port
// collision from reading like a crash: the process exits immediately after
// printing its startup line, so the fatal line has to name the cause.
func TestIsAddrInUseClassifiesPortCollisions(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"wrapped syscall errno", fmt.Errorf("listen tcp :8080: bind: %w", syscall.EADDRINUSE), true},
		{
			"windows winsock text",
			errors.New("listen tcp :8080: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted."),
			true,
		},
		{"posix text", errors.New("listen tcp :8080: bind: address already in use"), true},
		{"unrelated failure", errors.New("listen tcp: lookup localhost: no such host"), false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isAddrInUse(testCase.err); got != testCase.want {
				t.Fatalf("isAddrInUse(%v) = %t, want %t", testCase.err, got, testCase.want)
			}
		})
	}
}

// postForm builds a form-encoded request, since httptest only parses the body as one when
// the content type says so.
func postForm(path, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

// TestHandleResolveEntityWithATypedDomainSpendsNoSearch pins the cheap half of "always
// confirm": when the operator already typed the domain, the step renders that domain as the
// single pre-selected candidate. The server's agent is deliberately nil — reaching it would
// panic, which is exactly the assertion that no lookup was attempted.
func TestHandleResolveEntityWithATypedDomainSpendsNoSearch(t *testing.T) {
	templates, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	server := &Server{cfg: &config.Config{}, hub: agent.NewSessionHub(), tmpl: templates}

	response := httptest.NewRecorder()
	server.handleResolveEntity(response, postForm("/api/resolve-entity",
		"query=Sanjay+Chemicals&company_domain=https%3A%2F%2Fwww.sanjaychemindia.com%2Fabout&company_country=India"))

	body := response.Body.String()
	if !strings.Contains(body, "sanjaychemindia.com") {
		t.Fatalf("expected the typed domain on the card, got %q", body)
	}
	if strings.Contains(body, "www.sanjaychemindia.com/about") {
		t.Errorf("the pasted URL should be reduced to its host, got %q", body)
	}
	if !strings.Contains(body, "spent no search") {
		t.Errorf("expected the step to say it spent no search, got %q", body)
	}
	// One candidate card plus the name-only card: the step is a single confirmation click.
	if got := strings.Count(body, "type=\"submit\""); got != 2 {
		t.Errorf("expected 2 submit buttons, got %d", got)
	}
	if !strings.Contains(body, "autofocus") {
		t.Errorf("expected the top match to carry autofocus, got %q", body)
	}
	if !strings.Contains(body, `name="entity_source" value="resolved"`) {
		t.Errorf("a dialogue pick must be reported as resolved, got %q", body)
	}
}

// TestEntityDomainValueNormalizesWhatWasTyped keeps the gate's key clean no matter how the
// domain arrives, and treats a value that is not a host as "no domain given" so the step
// looks the company up rather than gating a run on it.
func TestEntityDomainValueNormalizesWhatWasTyped(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"plain host", "company_domain=acme.com", "acme.com"},
		{"pasted url", "company_domain=https%3A%2F%2Fwww.acme.com%2Fabout%3Fx%3D1", "acme.com"},
		{"uppercase", "company_domain=ACME.COM", "acme.com"},
		{"confirmed field wins", "entity_domain=brand.com&company_domain=acme.com", "brand.com"},
		{"absent", "", ""},
	}
	for _, tc := range cases {
		if got := entityDomainValue(postForm("/api/resolve-entity", tc.body)); got != tc.want {
			t.Errorf("%s: expected %q, got %q", tc.name, tc.want, got)
		}
	}
}

// TestConfirmEntityIdentityRefusesASecondLegalEntity covers the guardrail on multi-select:
// two different registration ids are two legal entities, and folding them into one gate
// would let a second company's people into the chart.
func TestConfirmEntityIdentityRefusesASecondLegalEntity(t *testing.T) {
	brief := models.CompanyBrief{
		CompanyName:    "Sanjay Chemicals",
		Domain:         "sanjaychemindia.com",
		RegistrationID: "U51434MH2000PTC129290",
	}

	// Same CIN, or no CIN on the extra: these are names for the same entity, so they merge.
	sameIdentity := []entityExtra{
		{Domain: "sanjaychemicals.in", Name: "Sanjay Chemicals India", RegistrationID: "U51434MH2000PTC129290"},
		{Domain: "sanjaychem.co.in", Name: "Sanjay Chem"},
	}
	merged, err := confirmEntityIdentity(brief, sameIdentity)
	if err != nil {
		t.Fatalf("expected the same entity to merge, got %v", err)
	}
	if len(merged.Domains) != 2 || merged.Domains[0] != "sanjaychemicals.in" {
		t.Fatalf("expected both extra domains on the brief, got %+v", merged.Domains)
	}
	if len(merged.Aliases) != 2 {
		t.Fatalf("expected both extra names as aliases, got %+v", merged.Aliases)
	}

	// A different CIN is a different company: refused, with both ids named.
	otherEntity := []entityExtra{{Domain: "sanjaychemicalsallied.com", Name: "Sanjay Chemicals & Allied", RegistrationID: "U24100MH1998PTC114455"}}
	_, refusal := confirmEntityIdentity(brief, otherEntity)
	if refusal == nil {
		t.Fatal("expected a different registration id to be refused")
	}
	for _, part := range []string{"sanjaychemicalsallied.com", "U24100MH1998PTC114455", "U51434MH2000PTC129290"} {
		if !strings.Contains(refusal.Error(), part) {
			t.Errorf("expected the refusal to name %q, got %q", part, refusal.Error())
		}
	}

	// The escape hatch: clearing the registration id says "one identity anyway".
	cleared := brief
	cleared.RegistrationID = ""
	if _, err := confirmEntityIdentity(cleared, otherEntity); err != nil {
		t.Fatalf("a cleared registration id must allow the merge, got %v", err)
	}
}

// TestMergeEntityExtrasAddsEachNameOnce keeps a multi-select from duplicating keys the
// operator already configured by hand.
func TestMergeEntityExtrasAddsEachNameOnce(t *testing.T) {
	brief := models.CompanyBrief{
		CompanyName: "CJ Shah",
		Domain:      "cjshahgroup.in",
		Aliases:     []string{"CJ Shah & Co"},
	}
	merged := mergeEntityExtras(brief, []entityExtra{
		{Domain: "cjshahgroup.in", Name: "CJ Shah"},
		{Domain: "cjshah.com", Name: "CJ Shah & Co"},
		{Domain: "cjshah.com", Name: "CJ Shah Group"},
	})
	if len(merged.Domains) != 1 || merged.Domains[0] != "cjshah.com" {
		t.Fatalf("expected only the new domain, got %+v", merged.Domains)
	}
	if len(merged.Aliases) != 2 || merged.Aliases[0] != "CJ Shah & Co" {
		t.Fatalf("expected only the new alias, got %+v", merged.Aliases)
	}
}

// TestParseEntityExtrasRoundTripsTheCardEncoding covers the wire format the checkbox
// carries: one tick has to submit a whole identity, because an unticked checkbox submits
// nothing and parallel hidden fields could not be kept in step with a tick.
func TestParseEntityExtrasRoundTripsTheCardEncoding(t *testing.T) {
	candidate := models.EntityCandidate{
		Domain:         "sanjaychemicals.in",
		Name:           "Sanjay Chemicals India",
		RegistrationID: "U51434MH2000PTC129290",
	}
	body := "query=Sanjay+Chemicals&entity_extra=" + url.QueryEscape(entityExtraValue(candidate)) +
		"&entity_extra=" + url.QueryEscape(entityExtraValue(models.EntityCandidate{Domain: "sanjaychem.co.in"}))
	extras := parseEntityExtras(postForm("/api/research", body))
	if len(extras) != 2 {
		t.Fatalf("expected two extras, got %+v", extras)
	}
	want := entityExtra{Domain: "sanjaychemicals.in", Name: "Sanjay Chemicals India", RegistrationID: "U51434MH2000PTC129290"}
	if extras[0] != want {
		t.Fatalf("expected the encoded identity back, got %+v", extras[0])
	}
	if extras[1].Name != "" || extras[1].RegistrationID != "" {
		t.Fatalf("expected the domain-only tick to stay minimal, got %+v", extras[1])
	}
}

// TestEntityRegistrationConflictOnlyFiresOnTwoDifferentIDs keeps the card warning honest:
// an extra without a registration id cannot contradict anything.
func TestEntityRegistrationConflictOnlyFiresOnTwoDifferentIDs(t *testing.T) {
	primary := models.EntityCandidate{Domain: "a.com", RegistrationID: "U1234567890"}
	if warning := entityRegistrationConflict(primary, models.EntityCandidate{Domain: "b.com"}); warning != "" {
		t.Errorf("expected no warning without a second id, got %q", warning)
	}
	warning := entityRegistrationConflict(primary, models.EntityCandidate{Domain: "b.com", RegistrationID: "U9999999999"})
	if !strings.Contains(warning, "separate legal entity") {
		t.Fatalf("expected the conflict to be explained, got %q", warning)
	}
}

// TestHandleResolvePersonWithTypedContextSpendsNoSearch pins the cheap half of the person
// step: when a company and a place are already given, that context pins the person, so the
// step renders one pre-selected card and never touches the agent — which is nil here, so
// reaching it would panic.
func TestHandleResolvePersonWithTypedContextSpendsNoSearch(t *testing.T) {
	templates, err := web.Templates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	server := &Server{cfg: &config.Config{}, hub: agent.NewSessionHub(), tmpl: templates}

	response := httptest.NewRecorder()
	server.handleResolvePerson(response, postForm("/api/resolve-person",
		"mode=person&query=Sanjay+Gupta&company_name=Sanjay+Chemicals&location=Mumbai"))

	body := response.Body.String()
	if !strings.Contains(body, "Sanjay Gupta") || !strings.Contains(body, "name=\"company_name\" value=\"Sanjay Chemicals\"") {
		t.Fatalf("expected the typed context on the card, got %q", body)
	}
	if !strings.Contains(body, "spent no search") {
		t.Errorf("expected the step to say it spent no search, got %q", body)
	}
	if !strings.Contains(body, "autofocus") {
		t.Errorf("expected the single card to be focused, got %q", body)
	}
	if !strings.Contains(body, `name="person_source" value="resolved"`) {
		t.Errorf("expected a confirmed person to be recorded as resolved, got %q", body)
	}
	if !strings.Contains(body, "Research on the name alone (unconfirmed)") {
		t.Errorf("expected the name-only escape hatch, got %q", body)
	}
}

// TestPersonQueryFromFormCarriesTheConfirmedIdentity covers the wire between the card and
// the run: the employer and place the operator confirmed, the seed profile the identity was
// confirmed from, and how the identity arrived.
func TestPersonQueryFromFormCarriesTheConfirmedIdentity(t *testing.T) {
	request := postForm("/api/research", "mode=person&query=Sanjay+Kumar+Gupta"+
		"&company_name=Sanjay+Chemicals+%28India%29+Pvt+Ltd&location=Mumbai"+
		"&person_confirmed=1&person_source=resolved"+
		"&person_seed_url=https%3A%2F%2Fwww.linkedin.com%2Fin%2Fsanjay-kumar-gupta"+
		"&person_seed_title=Sanjay+Kumar+Gupta+-+Managing+Director&person_seed_snippet=Mumbai%2C+Maharashtra")

	query := personQueryFromForm(request, "Sanjay Kumar Gupta")
	if query.Company != "Sanjay Chemicals (India) Pvt Ltd" || query.Location != "Mumbai" {
		t.Fatalf("expected the confirmed context, got %+v", query)
	}
	if !query.Confirmed || query.Source != "resolved" {
		t.Fatalf("expected a resolved, confirmed identity, got %+v", query)
	}
	if query.SeedURL != "https://www.linkedin.com/in/sanjay-kumar-gupta" || query.SeedSnippet != "Mumbai, Maharashtra" {
		t.Fatalf("expected the confirmed profile to travel as a seed, got %+v", query)
	}

	// A run submitted without the step is still recorded honestly as typed.
	typed := personQueryFromForm(postForm("/api/research", "mode=person&query=Sanjay+Gupta"), "Sanjay Gupta")
	if typed.Confirmed || typed.Source != "typed" || typed.Company != "" {
		t.Fatalf("expected an unconfirmed typed query, got %+v", typed)
	}
}
