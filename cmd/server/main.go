package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
	"github.com/amitgupta1000/WebSearch_GO/internal/config"
	"github.com/amitgupta1000/WebSearch_GO/internal/llm"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
	"github.com/amitgupta1000/WebSearch_GO/internal/web"
)

// maxFormBytes caps the accepted request body size (DoS protection).
const maxFormBytes = 1 << 20 // 1 MiB

type Server struct {
	cfg          *config.Config
	companyAgent *agent.CompanyAgent
	personAgent  *agent.PersonAgent
	hub          *agent.SessionHub
	tmpl         *template.Template
	// browserAvailable is probed once at startup so the console never offers a
	// rendering toggle that cannot work.
	browserAvailable   bool
	allowBrowserToggle bool
}

// indexView is the data passed to the full-page console.
type indexView struct {
	// BrowserToggle is true when the console may offer per-run browser rendering.
	BrowserToggle bool
	// BrowserDefaultOn mirrors the deployment default so the checkbox reflects it.
	BrowserDefaultOn bool
}

type companyResultView struct {
	Chart     *models.OrgChart
	SessionID string
	// BrowserToggle is true when a finished result can be re-run with browser
	// rendering, which is how a user escalates a thin result.
	BrowserToggle bool
}

// entityResolveTimeout bounds the pre-run identity lookup. It is two searches, so a
// slow answer is not worth waiting for: the operator can always run on the typed name.
const entityResolveTimeout = 25 * time.Second

// entityCandidateView is one candidate identity plus the brief fields, so a card can
// post a complete research request without any client-side state.
type entityCandidateView struct {
	Candidate       models.EntityCandidate
	Query           string
	Industry        string
	Country         string
	City            string
	Domain          string
	AreasOfInterest string
	DesiredOutput   string
	TimeScope       string
	Notes           string
	UseBrowser      string
	// PreSelected marks the top candidate: it is the one the operator confirms by
	// pressing Enter, so it is rendered first with autofocus on its button.
	PreSelected bool
	// Extras are the *other* candidates, offered on this card as "also part of this
	// company". Ticking one folds its identity into the same confirmed entity: one more
	// domain for the gate to accept and one more name for it to recognise.
	Extras []entityExtraView
}

// entityExtraView is one other candidate offered as part of the same identity. Value is
// what the checkbox submits: domain|name|registration. Checkboxes submit nothing when
// unticked, so a parallel hidden field per extra would drift out of step with the tick.
type entityExtraView struct {
	Candidate models.EntityCandidate
	Value     string
	// Warning is set when this candidate contradicts the card's registration id. Two
	// different CINs are two legal entities, and folding them together would defeat the
	// point of confirming an identity at all.
	Warning string
}

// entityCandidatesView is the dialogue rendered before a run starts.
type entityCandidatesView struct {
	Query           string
	Candidates      []entityCandidateView
	Industry        string
	Country         string
	City            string
	AreasOfInterest string
	DesiredOutput   string
	TimeScope       string
	Notes           string
	UseBrowser      string
	// TypedDomain is true when the operator already gave a domain, so the step spent no
	// search and can say so rather than implying it looked the company up.
	TypedDomain bool
}

// personCandidateView is one possible person plus the run's fields, so a card can post a
// complete person request without any client-side state.
type personCandidateView struct {
	Candidate models.PersonCandidate
	Query     string
	// Company and Location are pre-filled from the candidate: reading them off a profile is
	// the point of the step. Location is only pre-filled when the operator stated one, since
	// a person-directory page rarely states a place in a machine-readable way.
	Company     string
	Location    string
	PreSelected bool
}

// personCandidatesView is the confirmation step for a person run.
type personCandidatesView struct {
	Query      string
	Candidates []personCandidateView
	Company    string
	Location   string
	// TypedContext is true when company and place were both already given, so the step spent
	// no search and says so.
	TypedContext bool
	// NoCompanyMatch is true when a company was typed and no candidate names it. The
	// pre-selection is dropped in that case, because one Enter would otherwise pin a person
	// the operator never meant.
	NoCompanyMatch bool
}

func main() {
	cfg := config.Load()

	if cfg.SerperAPIKey == "" {
		log.Printf("[WARN] SERPER_API_KEY is not configured; search requests will fail until it is set")
	}

	ctx := context.Background()

	// 1. Initialize Cookie Jar (if path is provided). The summary is logged
	// because a session that failed to load is otherwise indistinguishable from a
	// site that simply has no cookies for the host the ladder escalates — and the
	// cookie-render step is skipped silently in both cases.
	cookieJar, cookieReport, err := tools.LoadCookieJarFromFileDetailed(cfg.CookieFile)
	if err != nil {
		log.Printf("[WARN] Cookie jar init: %v", err)
	}
	switch {
	case strings.TrimSpace(cfg.CookieFile) == "":
		// The message says what to do, not just what is missing: this is the difference
		// between a person profile being readable and the run quietly coming back with a
		// snippet. Cookies also ride the plain HTTP fetch, so a session often makes the
		// profile readable without a render at all.
		log.Printf("Cookie jar: no COOKIE_FILE_PATH set, so logged-in-only pages (LinkedIn person profiles) cannot be opened. Export a logged-in session as JSON — a browser extension (Cookie-Editor / EditThisCookie) or `npx playwright open --save-storage=linkedin.json` — and set COOKIE_FILE_PATH to it")
	case cookieReport.Cookies == 0:
		log.Printf("[WARN] Cookie jar: %s produced no usable cookies (%d skipped); cookie-render will be skipped. Check that the file is a JSON export (an array, or Playwright's {\"cookies\": [...]}) whose cookies have not expired", cfg.CookieFile, cookieReport.Skipped)
	default:
		log.Printf("Cookie jar: %d cookies from %s (%d skipped, %d also registered for the parent domain): %s",
			cookieReport.Cookies, cfg.CookieFile, cookieReport.Skipped, cookieReport.WidenedToParent,
			strings.Join(cookieReport.Domains, ", "))
		// A captured session is a per-run asset, not a durable credential: measured on a live
		// LinkedIn export, one profile read worked and the next request was answered with a
		// redirect loop. Say so, so nobody configures one and expects it to keep working.
		log.Printf("Cookie session: treat this as short-lived — a handful of profile fetches, then re-capture; a fetch that reports the session was rejected means exactly that")
	}

	// 2. Initialize the Gemini LLM client. Fail fast: research cannot run without it,
	// and the scraper borrows it to read PDFs.
	gemini, err := llm.NewGeminiClient(ctx, cfg.GeminiModel)
	if err != nil {
		log.Fatalf("Gemini client initialization failed (check GCP credentials or GOOGLE_API_KEY): %v", err)
	}

	// 3. Initialize Cache, Scraper and Serper. The scraper extracts PDF text through
	// Gemini — registry filings and officer lists are often published only as PDFs —
	// falling back to a local parser when the model call fails. Its per-host limits
	// are what keep the parallel fetches below from concentrating on one site.
	store := buildCacheStore(ctx, cfg)
	defer store.Close()
	scraper := tools.NewScraperWithBrowser(cookieJar, 15*time.Second, cfg.EnableBrowserFallback,
		tools.WithDocumentTextExtractor(documentTextExtractor(gemini)),
		tools.WithDocumentTimeout(cfg.DocumentExtractTimeout),
		tools.WithHostLimits(cfg.ScrapeHostRate, cfg.ScrapeHostBurst, cfg.ScrapeHostConcurrency),
		tools.WithMaxRetryAfter(cfg.MaxRetryAfter))
	defer scraper.Close()
	serper := tools.NewSerperClient(cfg.SerperAPIKey)

	// 4. Initialize Core Engine & Domain Agents
	options := []agent.EngineOption{
		agent.WithProfileScraping(cfg.AllowProfileScraping),
		agent.WithScrapeConcurrency(cfg.ScrapeConcurrency),
		agent.WithScrapeRevalidation(cfg.ScrapeRevalidateAfter),
		agent.WithHardURLLimits(cfg.HardURLsPerRun, cfg.HardURLBudget),
		agent.WithValidation(cfg.ValidateEvidence, cfg.ValidationLookups, cfg.ValidationStaleMonths, cfg.ValidationRecentMonths),
		agent.WithEntityGate(cfg.EntityGateMode),
	}
	// Finished runs are written to the configured destination as Markdown and PDF.
	// A nil exporter simply means the runs are not saved.
	if exporter := buildExporter(ctx, cfg, scraper); exporter != nil {
		options = append(options, agent.WithExporter(exporter))
	}
	eng := agent.NewEngine(gemini, serper, scraper, store, cfg.MaxScrapesPerRun, cfg.MaxSearchResults, cfg.MaxSteps,
		options...)
	// The bounded hard-URL ladder is on by default; HARD_URL_HANDLER=0 disables it,
	// and a missing Chromium makes it report itself unavailable instead.
	eng.SetHardURLHandler(cfg.HardURLHandler)
	log.Printf("Evidence validation: %t (up to %d reverse lookups, stale after %d months, recent within %d months)",
		cfg.ValidateEvidence, cfg.ValidationLookups, cfg.ValidationStaleMonths, cfg.ValidationRecentMonths)
	log.Printf("Entity gate: %s (sources must name the company, its domain, an alias or its registration id)", cfg.EntityGateMode)
	compAgent := agent.NewCompanyAgent(eng)
	persAgent := agent.NewPersonAgent(eng)
	sessionHub := agent.NewSessionHub()

	// 5. Parse templates embedded into the binary (independent of working directory)
	tmpl, err := web.Templates()
	if err != nil {
		log.Fatalf("Template parsing failed: %v", err)
	}

	// Probe Chromium once when the console may offer per-run rendering, so the UI
	// never advertises a capability that is not installed. A successful probe also
	// leaves the browser warm for the first request.
	browserAvailable := false
	if cfg.AllowBrowserToggle || cfg.EnableBrowserFallback || cfg.HardURLHandler {
		browserAvailable = scraper.BrowserAvailable()
		if browserAvailable {
			log.Printf("Browser rendering available (per-run toggle: %t, hard-URL handler: %t)", cfg.AllowBrowserToggle, cfg.HardURLHandler)
		} else {
			log.Printf("[WARN] Browser rendering requested but Chromium could not be started; the console will not offer it, the hard-URL handler will report itself unavailable, and any fallback will report a failure")
		}
	}

	srv := &Server{
		cfg:                cfg,
		companyAgent:       compAgent,
		personAgent:        persAgent,
		hub:                sessionHub,
		tmpl:               tmpl,
		browserAvailable:   browserAvailable,
		allowBrowserToggle: cfg.AllowBrowserToggle,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", srv.handleIndex)
	mux.HandleFunc("POST /api/research", srv.handleResearch)
	mux.HandleFunc("POST /api/resolve-entity", srv.handleResolveEntity)
	mux.HandleFunc("POST /api/resolve-person", srv.handleResolvePerson)
	mux.HandleFunc("POST /api/rerun", srv.handleRerun)
	mux.HandleFunc("POST /api/question", srv.handleQuestion)
	mux.HandleFunc("GET /api/events", srv.handleEvents)
	mux.HandleFunc("GET /healthz", srv.handleHealth)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Size the write timeout for the longest possible run so a browser-rendered
		// research stream is never cut off mid-flight.
		WriteTimeout: cfg.MaxResearchTimeout() + 30*time.Second,
		IdleTimeout:  90 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM (Cloud Run sends SIGTERM on scale-in).
	serverCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("🚀 WebSearch_GO server starting on http://localhost%s", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			if isAddrInUse(err) {
				log.Fatalf("Server failed: %v\n[FATAL] %s is already in use: another process (often a previous instance) is listening there. Stop it, or start this one with a free PORT.", err, httpServer.Addr)
			}
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-serverCtx.Done()
	log.Printf("Shutting down, waiting up to 30s for in-flight research requests...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("[WARN] Graceful shutdown failed: %v", err)
	}
}

// handleIndex serves the embedded research console.
func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	view := indexView{
		BrowserToggle:    s.allowBrowserToggle && s.browserAvailable,
		BrowserDefaultOn: s.cfg.EnableBrowserFallback,
	}
	if err := s.tmpl.ExecuteTemplate(w, "index.html", view); err != nil {
		log.Printf("[ERROR] index template execution failed: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// documentReader is the slice of the LLM client that PDF extraction needs, kept narrow
// so the fallback policy can be tested without a live model.
type documentReader interface {
	ExtractDocumentText(ctx context.Context, contentType string, body []byte) (string, error)
}

// documentTextExtractor builds the extractor handed to the scraper. Gemini is tried
// first because it reads PDFs natively, including compressed streams, subsetted fonts
// and scanned filings that a text parser cannot recover. The local parser is the
// fallback for when the model is unavailable, rate-limited or refuses the document.
func documentTextExtractor(reader documentReader) tools.DocumentTextExtractor {
	return func(ctx context.Context, contentType string, body []byte) (string, string, error) {
		text, err := reader.ExtractDocumentText(ctx, contentType, body)
		if err == nil && strings.TrimSpace(text) != "" {
			return text, tools.ExtractorPDFGemini, nil
		}
		if err == nil {
			err = errors.New("gemini returned no text")
		}
		log.Printf("[WARN] Gemini PDF extraction failed; falling back to the local parser: %v", err)

		localText, localErr := tools.ExtractPDFTextPartial(ctx, body, tools.MaxDocumentTextBytes)
		if localErr != nil && strings.TrimSpace(localText) == "" {
			return "", "", fmt.Errorf("gemini extraction failed (%v) and local parsing failed (%w)", err, localErr)
		}
		if localErr != nil {
			// The parser was cut short — a very large filing, usually by the document
			// timeout. What it did read is still evidence, so it is kept and the shortfall is
			// reported rather than silently presented as the whole document.
			log.Printf("[WARN] document extraction stopped early: recovered %d bytes: %v", len(localText), localErr)
		}
		return localText, tools.ExtractorPDFLocal, nil
	}
}

// requestedBrowserOverride reads the per-run rendering choice from the console
// form. explicit reports whether the user made a choice at all.
func requestedBrowserOverride(r *http.Request) (enabled bool, explicit bool) {
	switch strings.ToLower(strings.TrimSpace(r.FormValue("use_browser"))) {
	case "1", "true", "on", "yes":
		return true, true
	case "0", "false", "off", "no":
		return false, true
	}
	return false, false
}

// browserChoice resolves the per-run rendering choice, honouring the deployment
// policy. Asking for browser rendering where Chromium is missing is rejected
// rather than silently degrading to an HTTP-only run.
func (s *Server) browserChoice(r *http.Request) (bool, bool, error) {
	if !s.allowBrowserToggle {
		return false, false, nil
	}
	enabled, explicit := requestedBrowserOverride(r)
	if enabled && !s.browserAvailable {
		return false, false, errors.New("browser rendering is not available on this deployment")
	}
	return enabled, explicit, nil
}

// withBrowserOverride applies a per-run rendering choice to ctx.
func withBrowserOverride(ctx context.Context, enabled, explicit bool) context.Context {
	if !explicit {
		return ctx
	}
	return tools.WithBrowserFallback(ctx, enabled)
}

// handleResearch initializes a live research task and renders the workflow console
// for HTMX. If sync=1 query parameter is passed, it executes synchronously.
func (s *Server) handleResearch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)

	mode := strings.TrimSpace(r.FormValue("mode"))
	query := strings.TrimSpace(r.FormValue("query"))
	companyBrief := models.CompanyBrief{
		CompanyName:     query,
		Domain:          entityDomainValue(r),
		Aliases:         splitFormList(r.FormValue("entity_aliases")),
		RegistrationID:  strings.TrimSpace(r.FormValue("entity_reg_id")),
		EntityConfirmed: strings.TrimSpace(r.FormValue("entity_confirmed")) == "1",
		EntitySource:    entitySourceValue(r),
		Industry:        strings.TrimSpace(r.FormValue("company_industry")),
		ResearchPreferences: models.ResearchPreferences{
			Country:         strings.TrimSpace(r.FormValue("company_country")),
			City:            strings.TrimSpace(r.FormValue("company_city")),
			AreasOfInterest: splitFormList(r.FormValue("areas_of_interest")),
			DesiredOutput:   splitFormList(r.FormValue("desired_output")),
			TimeScope:       strings.TrimSpace(r.FormValue("time_scope")),
			UserNotes:       strings.TrimSpace(r.FormValue("research_notes")),
		},
	}

	if query == "" {
		s.renderError(w, "Please provide a non-empty query.")
		return
	}

	// The confirmation step is where the operator can fold a second name into the same
	// identity. Those picks arrive here, and a contradictory registration id is refused:
	// the gate's whole value is that a human confirmed exactly which company this is.
	extras := parseEntityExtras(r)
	if mode != "person" && len(extras) > 0 {
		confirmed, err := confirmEntityIdentity(companyBrief, extras)
		if err != nil {
			s.renderError(w, err.Error())
			return
		}
		companyBrief = confirmed
		log.Printf("[ENTITY] confirmed %q with %d additional name(s): domains=%v aliases=%v",
			companyBrief.CompanyName, len(extras), companyBrief.Domains, companyBrief.Aliases)
	}

	browserEnabled, browserExplicit, err := s.browserChoice(r)
	if err != nil {
		s.renderError(w, err.Error())
		return
	}

	sessionID := newSessionID()

	// Synchronous mode for CLI or automated testing
	if r.URL.Query().Get("sync") == "1" {
		ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ResearchTimeoutFor(browserEnabled))
		defer cancel()
		ctx = withBrowserOverride(ctx, browserEnabled, browserExplicit)

		if mode == "person" {
			personQuery := personQueryFromForm(r, query)
			profile, researchErr := s.personAgent.ResearchPerson(ctx, sessionID, personQuery, nil)
			if researchErr != nil {
				log.Printf("[ERROR] person research failed (session %s): %v", sessionID, researchErr)
				s.renderError(w, "Person research failed: "+researchErr.Error())
				return
			}
			s.render(w, "person_dossier", profile)
			return
		}

		chart, researchErr := s.companyAgent.ResearchCompanyWithBrief(ctx, sessionID, companyBrief, nil)
		if researchErr != nil {
			log.Printf("[ERROR] company research failed (session %s): %v", sessionID, researchErr)
			s.renderError(w, "Company research failed: "+researchErr.Error())
			return
		}
		s.render(w, "company_chart", chart)
		return
	}

	s.startRun(w, sessionID, mode, query, personQueryFromForm(r, query), companyBrief, browserEnabled, browserExplicit)
}

// personQueryFromForm reads the identity a person run starts from. The confirmation step
// fills Company and Location from the profile the operator picked — a context read off a
// source instead of recalled — and carries that profile as a seed, so the run reads the page
// the identity was confirmed on first.
func personQueryFromForm(r *http.Request, name string) models.PersonQuery {
	source := strings.TrimSpace(r.FormValue("person_source"))
	if source == "" {
		source = "typed"
	}
	return models.PersonQuery{
		Name:        strings.TrimSpace(name),
		Company:     strings.TrimSpace(r.FormValue("company_name")),
		Location:    strings.TrimSpace(r.FormValue("location")),
		Confirmed:   strings.TrimSpace(r.FormValue("person_confirmed")) == "1",
		Source:      source,
		SeedURL:     strings.TrimSpace(r.FormValue("person_seed_url")),
		SeedTitle:   strings.TrimSpace(r.FormValue("person_seed_title")),
		SeedSnippet: strings.TrimSpace(r.FormValue("person_seed_snippet")),
	}
}

// startRun launches a research session in the background and renders the live
// workflow console for HTMX.
func (s *Server) startRun(w http.ResponseWriter, sessionID, mode, query string, person models.PersonQuery, brief models.CompanyBrief, browserEnabled, browserExplicit bool) {
	session := s.hub.Create(sessionID, mode, query)

	timeout := s.cfg.ResearchTimeoutFor(browserEnabled)

	go func() {
		// An async run sits outside net/http's per-request panic recovery, so a
		// panic anywhere in the agent would end the whole process. Report it as a
		// failed session instead, which the console renders as an error box.
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("[ERROR][session %s] research panicked: %v\n%s", sessionID, recovered, debug.Stack())
				session.Complete(nil, fmt.Errorf("research panicked: %v", recovered))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		ctx = withBrowserOverride(ctx, browserEnabled, browserExplicit)

		if mode == "person" {
			profile, err := s.personAgent.ResearchPerson(ctx, sessionID, person, session.EventCh)
			if err != nil {
				log.Printf("[ERROR][session %s] person research failed: %v", sessionID, err)
			} else {
				log.Printf("[RESULT][session %s] person research completed", sessionID)
			}
			session.Complete(profile, err)
			return
		}

		chart, companyContext, err := s.companyAgent.ResearchCompanyRun(ctx, sessionID, brief, session.EventCh)
		if err != nil {
			log.Printf("[ERROR][session %s] company research failed: %v", sessionID, err)
		} else {
			log.Printf("[RESULT][session %s] company=%s leadership=%d departments=%d browser=%t", sessionID, chart.CompanyName, len(chart.Leadership), len(chart.Departments), browserEnabled)
		}
		session.SetCompanyContext(companyContext)
		session.Complete(chart, err)
	}()

	displayMode := "Company"
	if mode == "person" {
		displayMode = "Person"
	}
	s.render(w, "workflow_console", map[string]string{
		"SessionID": sessionID,
		"Mode":      displayMode,
		"Query":     query,
		"Budget":    fmt.Sprintf("%ds", int(timeout.Seconds())),
	})
}

// handleRerun re-runs a completed company session's brief, optionally with
// browser rendering, so a thin result can be escalated without re-entering it.
func (s *Server) handleRerun(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)

	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	if sessionID == "" {
		s.renderError(w, "A research session is required to re-run this research.")
		return
	}
	previous, ok := s.hub.Get(sessionID)
	if !ok {
		s.renderError(w, "That research session is no longer available. Start a new research run instead.")
		return
	}
	researchContext := previous.GetCompanyContext()
	if researchContext == nil {
		s.renderError(w, "Re-running is only available for company research sessions.")
		return
	}

	browserEnabled, browserExplicit, err := s.browserChoice(r)
	if err != nil {
		s.renderError(w, err.Error())
		return
	}

	brief := researchContext.Brief
	log.Printf("[RERUN] session %s -> new session, browser=%t", sessionID, browserEnabled)
	s.startRun(w, newSessionID(), "company", brief.CompanyName, models.PersonQuery{}, brief, browserEnabled, browserExplicit)
}

// handleQuestion answers a question against a completed company session.
func (s *Server) handleQuestion(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	question := strings.TrimSpace(r.FormValue("question"))
	if sessionID == "" || question == "" {
		s.renderError(w, "A session and question are required.")
		return
	}
	if len([]rune(question)) > 500 {
		s.renderError(w, "Question is limited to 500 characters.")
		return
	}
	session, ok := s.hub.Get(sessionID)
	if !ok || session.Mode != "company" {
		s.renderError(w, "That research session is not available for company questions.")
		return
	}
	researchContext := session.GetCompanyContext()
	result, sessionErr := session.GetResult()
	chart, chartOK := result.(*models.OrgChart)
	if sessionErr != nil || researchContext == nil || !chartOK {
		s.renderError(w, "The company research session has not completed successfully.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ResearchTimeout)
	defer cancel()
	answer, err := s.companyAgent.AnswerQuestion(ctx, researchContext, chart, question)
	if err != nil {
		s.renderError(w, "Question answering failed: "+err.Error())
		return
	}
	s.render(w, "question_answer", answer)
}

// handleResolveEntity renders the confirmation step for a company name: candidate
// identities, offered before any research runs. Confirming here is what makes the entity
// gate enforceable — one or two searches now, instead of a run whose sources all look
// relevant and belong to another company.
//
// The step always renders. When the operator has already typed a domain it spends no search
// at all and renders that domain as the single, pre-filled candidate, so "always confirm"
// costs nothing on the runs where the answer is already known.
func (s *Server) handleResolveEntity(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	name := strings.TrimSpace(r.FormValue("query"))
	if name == "" {
		s.renderError(w, "Please provide a company name to resolve.")
		return
	}

	view := entityCandidatesView{
		Query:           name,
		Industry:        strings.TrimSpace(r.FormValue("company_industry")),
		Country:         strings.TrimSpace(r.FormValue("company_country")),
		City:            strings.TrimSpace(r.FormValue("company_city")),
		AreasOfInterest: strings.TrimSpace(r.FormValue("areas_of_interest")),
		DesiredOutput:   strings.TrimSpace(r.FormValue("desired_output")),
		TimeScope:       strings.TrimSpace(r.FormValue("time_scope")),
		Notes:           strings.TrimSpace(r.FormValue("research_notes")),
		UseBrowser:      strings.TrimSpace(r.FormValue("use_browser")),
	}

	var candidates []models.EntityCandidate
	if typedDomain := entityDomainValue(r); typedDomain != "" {
		candidates = []models.EntityCandidate{{
			Name:      name,
			Domain:    typedDomain,
			NameMatch: agent.DomainEchoesName(typedDomain, name),
			Score:     1,
		}}
		view.TypedDomain = true
		log.Printf("[ENTITY] confirmation step for %q uses the typed domain %s (no search spent)", name, typedDomain)
	} else {
		resolved, err := s.resolveEntityCandidates(r.Context(), name, view.Country, view.City)
		if err != nil {
			s.renderError(w, "Entity lookup failed: "+err.Error())
			return
		}
		candidates = resolved
		log.Printf("[ENTITY] resolved %d candidate(s) for %q", len(candidates), name)
	}
	// Every card carries the whole brief plus the other candidates, so the operator can
	// confirm the identity in one click — or tick a second name that belongs to the same
	// company — without re-entering anything.
	for index, candidate := range candidates {
		card := entityCandidateView{
			Candidate:       candidate,
			Query:           view.Query,
			Industry:        view.Industry,
			Country:         view.Country,
			City:            view.City,
			AreasOfInterest: view.AreasOfInterest,
			DesiredOutput:   view.DesiredOutput,
			TimeScope:       view.TimeScope,
			Notes:           view.Notes,
			UseBrowser:      view.UseBrowser,
			PreSelected:     index == 0,
		}
		for otherIndex, other := range candidates {
			if otherIndex == index {
				continue
			}
			card.Extras = append(card.Extras, entityExtraView{
				Candidate: other,
				Value:     entityExtraValue(other),
				Warning:   entityRegistrationConflict(candidate, other),
			})
		}
		view.Candidates = append(view.Candidates, card)
	}
	s.render(w, "entity_candidates", view)
}

// resolveEntityCandidates performs the bounded identity lookup behind the confirmation
// step. It is split out so the choice between "the operator already gave a domain" and
// "look it up" is a decision the handler makes explicitly rather than a side effect of the
// searched path, and it carries entityResolveTimeout with it.
func (s *Server) resolveEntityCandidates(ctx context.Context, name, country, city string) ([]models.EntityCandidate, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, entityResolveTimeout)
	defer cancel()
	return s.companyAgent.ResolveEntityCandidates(lookupCtx, name, country, city)
}

// entityExtraSeparator separates the fields inside one extra-entity checkbox value. A pipe
// cannot appear in a domain, and a company name carrying one would not survive a registry,
// so it is safe as the field separator.
const entityExtraSeparator = "|"

// entityExtraValue encodes a candidate as one checkbox value: domain|name|registration.
// Checkboxes submit nothing when unticked, so a whole identity has to travel in the value
// itself rather than in parallel hidden fields, which cannot be kept in step with a tick.
func entityExtraValue(candidate models.EntityCandidate) string {
	return strings.Join([]string{
		strings.TrimSpace(candidate.Domain),
		strings.TrimSpace(candidate.Name),
		strings.TrimSpace(candidate.RegistrationID),
	}, entityExtraSeparator)
}

// entityRegistrationConflict explains why two candidates cannot be one identity: two
// different registration numbers are two different legal entities. It returns "" when the
// candidates do not contradict each other, which includes the common case where only one of
// them has a registration id at all.
func entityRegistrationConflict(primary, other models.EntityCandidate) string {
	primaryID := strings.ToUpper(strings.TrimSpace(primary.RegistrationID))
	otherID := strings.ToUpper(strings.TrimSpace(other.RegistrationID))
	if primaryID == "" || otherID == "" || primaryID == otherID {
		return ""
	}
	return fmt.Sprintf("a different registration id (%s vs %s) — a separate legal entity, not another name for this one", otherID, primaryID)
}

// parseEntityExtras reads the extras a confirmation card submitted.
func parseEntityExtras(r *http.Request) []entityExtra {
	if err := r.ParseForm(); err != nil {
		return nil
	}
	var extras []entityExtra
	for _, raw := range r.Form["entity_extra"] {
		parts := strings.SplitN(strings.TrimSpace(raw), entityExtraSeparator, 3)
		extra := entityExtra{}
		if len(parts) > 0 {
			extra.Domain = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			extra.Name = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			extra.RegistrationID = strings.TrimSpace(parts[2])
		}
		if extra.Domain == "" && extra.Name == "" {
			continue
		}
		extras = append(extras, extra)
	}
	return extras
}

// mergeEntityExtras folds the ticked identities into the brief: their domains join Domains
// and their names join Aliases. The gate then accepts a source naming any of them, which is
// what "the same company under several names" means in practice.
//
// A ticked identity whose registration id contradicts the card's is refused rather than
// merged: two different CINs are two legal entities, and folding them into one gate would
// let a second company's people into the chart. Clearing the CIN field is the deliberate
// way to say "one identity anyway".
func confirmEntityIdentity(brief models.CompanyBrief, extras []entityExtra) (models.CompanyBrief, error) {
	primaryID := strings.ToUpper(strings.TrimSpace(brief.RegistrationID))
	for _, extra := range extras {
		otherID := strings.ToUpper(strings.TrimSpace(extra.RegistrationID))
		if primaryID == "" || otherID == "" || primaryID == otherID {
			continue
		}
		return brief, fmt.Errorf("that selection includes a separate legal entity (%s: %s, this one: %s); confirm one identity at a time, or clear the registration id if you are certain they are the same company", extra.DomainOrName(), otherID, primaryID)
	}
	return mergeEntityExtras(brief, extras), nil
}

// entityExtra is one identity ticked as part of the confirmed company.
type entityExtra struct {
	Domain         string
	Name           string
	RegistrationID string
}

// DomainOrName names an extra in an error message, preferring what the operator saw.
func (e entityExtra) DomainOrName() string {
	if domain := strings.TrimSpace(e.Domain); domain != "" {
		return domain
	}
	return strings.TrimSpace(e.Name)
}

// mergeEntityExtras folds the ticked identities into the brief: their domains join Domains
// and their names join Aliases, so the gate accepts a source naming any of them. That is
// what "the same company under several names" means in practice.
func mergeEntityExtras(brief models.CompanyBrief, extras []entityExtra) models.CompanyBrief {
	seenDomain := make(map[string]bool, len(brief.Domains)+1)
	for _, domain := range append([]string{brief.Domain}, brief.Domains...) {
		seenDomain[strings.ToLower(strings.TrimSpace(domain))] = true
	}
	seenName := map[string]bool{strings.ToLower(brief.CompanyName): true}
	for _, alias := range brief.Aliases {
		seenName[strings.ToLower(strings.TrimSpace(alias))] = true
	}
	for _, extra := range extras {
		if domain := strings.TrimSpace(extra.Domain); domain != "" && !seenDomain[strings.ToLower(domain)] {
			seenDomain[strings.ToLower(domain)] = true
			brief.Domains = append(brief.Domains, domain)
		}
		if name := strings.TrimSpace(extra.Name); name != "" && !seenName[strings.ToLower(name)] {
			seenName[strings.ToLower(name)] = true
			brief.Aliases = append(brief.Aliases, name)
		}
	}
	return brief
}

// handleResolvePerson renders the confirmation step for a person: which of the people who
// share this name is being researched. Person research is disambiguated by context — role,
// employer, city — which is exactly what a profile states and what an operator otherwise
// supplies from memory, so the step reads it off a profile and asks for one confirmation.
//
// Like the company step it always renders and never dead-ends: with nothing found, the
// operator confirms the name alone.
func (s *Server) handleResolvePerson(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	name := strings.TrimSpace(r.FormValue("query"))
	if name == "" {
		s.renderError(w, "Please provide a person's name to resolve.")
		return
	}

	view := personCandidatesView{
		Query:    name,
		Company:  strings.TrimSpace(r.FormValue("company_name")),
		Location: strings.TrimSpace(r.FormValue("location")),
	}

	var candidates []models.PersonCandidate
	if view.Company != "" && view.Location != "" {
		// Company and place already pin the person, so no search is worth spending: the
		// step still asks, with a card that agrees with what was typed.
		candidates = []models.PersonCandidate{
			{Name: name, Company: view.Company, MatchesCompany: true, MatchesLocation: true, Score: 1},
		}
		view.TypedContext = true
		log.Printf("[PERSON] confirmation step for %q uses the typed company and location (no search spent)", name)
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), entityResolveTimeout)
		defer cancel()
		resolved, err := s.personAgent.ResolvePersonCandidates(ctx, name, view.Company, view.Location)
		if err != nil {
			s.renderError(w, "Person lookup failed: "+err.Error())
			return
		}
		candidates = resolved
		log.Printf("[PERSON] resolved %d candidate(s) for %q", len(candidates), name)
	}

	// A pre-selected top match is only safe when it agrees with the context the operator
	// gave. If a company was typed and no candidate names it, the step must not hand over a
	// pre-selected card: one Enter would pin the wrong person (live case: "Sanjay Gupta"
	// with "Sanjay Chemicals" typed offered CNN's medical correspondent first).
	if view.Company != "" {
		view.NoCompanyMatch = true
		for _, candidate := range candidates {
			if candidate.MatchesCompany {
				view.NoCompanyMatch = false
				break
			}
		}
	}

	for index, candidate := range candidates {
		view.Candidates = append(view.Candidates, personCandidateView{
			Candidate:   candidate,
			Query:       view.Query,
			Company:     firstNonBlank(candidate.Company, view.Company),
			Location:    view.Location,
			PreSelected: index == 0 && !view.NoCompanyMatch,
		})
	}
	s.render(w, "person_candidates", view)
}

// firstNonBlank returns the first value that is not whitespace, which is how a card keeps a
// typed context when the profile states none.
func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// entityDomainValue resolves the domain from the confirmed-entity field, falling back to
// the free-text company_domain field the console has always offered. Either way it is
// normalized, so a pasted URL ("https://www.acme.com/about") becomes the host the gate can
// actually match on. A value that is not a domain at all yields "", which sends the
// confirmation step down the lookup path instead of gating a run on nonsense.
func entityDomainValue(r *http.Request) string {
	if domain := strings.TrimSpace(r.FormValue("entity_domain")); domain != "" {
		return agent.NormalizeDomain(domain)
	}
	return agent.NormalizeDomain(strings.TrimSpace(r.FormValue("company_domain")))
}

// entitySourceValue records where the identity came from, so an artifact never presents a
// typed name as a confirmed entity.
func entitySourceValue(r *http.Request) string {
	if source := strings.TrimSpace(r.FormValue("entity_source")); source != "" {
		return source
	}
	if strings.TrimSpace(r.FormValue("entity_confirmed")) == "1" {
		return "user"
	}
	return "typed"
}

func splitFormList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

// handleEvents streams live agent workflow events and final synthesis to HTMX via SSE.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}

	session, ok := s.hub.Get(sessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sendSSE := func(eventType, htmlContent string) {
		lines := strings.Split(htmlContent, "\n")
		var sb strings.Builder
		sb.WriteString("event: " + eventType + "\n")
		for _, line := range lines {
			sb.WriteString("data: " + line + "\n")
		}
		sb.WriteString("\n")
		_, _ = w.Write([]byte(sb.String()))
		flusher.Flush()
	}

	renderFragment := func(name string, data any) string {
		var buf bytes.Buffer
		if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
			log.Printf("[ERROR] fragment %q execution failed: %v", name, err)
			return fmt.Sprintf("<div>Render error: %v</div>", err)
		}
		return buf.String()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-session.EventCh:
			if ok {
				session.AddHistory(ev)
				sendSSE("log", renderFragment("log_entry", ev))
			}
		case <-session.Done:
			// Drain any remaining buffered events in EventCh
		drain:
			for {
				select {
				case ev, ok := <-session.EventCh:
					if ok {
						session.AddHistory(ev)
						sendSSE("log", renderFragment("log_entry", ev))
						continue
					}
				default:
				}
				break drain
			}

			// Render the terminal result once. Reconnecting SSE clients must not
			// replace a dossier or chart that the user has already opened.
			if session.ClaimFinalEvent() {
				result, sessionErr := session.GetResult()
				if sessionErr != nil {
					sendSSE("result", renderFragment("error_box", sessionErr.Error()))
					sendSSE("status", `<span class="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-medium bg-red-500/10 text-red-400 border border-red-500/20">● Failed</span>`)
				} else if session.Mode == "person" {
					if profile, ok := result.(*models.PersonProfile); ok {
						sendSSE("result", renderFragment("person_dossier", profile))
					}
					sendSSE("status", `<span class="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-medium bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">● Complete</span>`)
				} else {
					if chart, ok := result.(*models.OrgChart); ok {
						sendSSE("result", renderFragment("company_result", companyResultView{Chart: chart, SessionID: session.ID, BrowserToggle: s.allowBrowserToggle && s.browserAvailable}))
					}
					sendSSE("status", `<span class="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-medium bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">● Complete</span>`)
				}
			}
			sendSSE("close", "done")
			return
		}
	}
}

// render executes a named template fragment, escaping all interpolated data.
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("[ERROR] template %q execution failed: %v", name, err)
	}
}

func (s *Server) renderError(w http.ResponseWriter, msg string) {
	s.render(w, "error_box", msg)
}

// handleHealth serves a liveness probe for Cloud Run / load balancers.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

// securityHeaders sets baseline defensive headers on every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// The UI loads Tailwind/HTMX from CDNs and uses one inline script.
		// img-src permits remote images so the scraped-source image gallery can
		// actually render. Both https and http are allowed because legacy sources
		// (the kind of corporate site this tool researches) still serve images over
		// plain http; images are passive content and Referrer-Policy is no-referrer,
		// which limits third-party tracking.
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com https://unpkg.com; "+
				"style-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com; "+
				"connect-src 'self'; img-src 'self' data: https: http:")
		next.ServeHTTP(w, r)
	})
}

// isAddrInUse reports whether a listen failure was a port collision. It matters
// because that exit is immediate: the process prints its startup line, cannot
// bind and dies, which reads like a crash when the cause is simply another
// instance holding the port.
func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "only one usage of each socket address")
}

// newSessionID returns a random session identifier that is safe under concurrency.
func newSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("failed to generate session id: %v", err)
	}
	return "sess_" + hex.EncodeToString(b)
}
