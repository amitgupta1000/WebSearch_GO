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

func main() {
	cfg := config.Load()

	if cfg.SerperAPIKey == "" {
		log.Printf("[WARN] SERPER_API_KEY is not configured; search requests will fail until it is set")
	}

	ctx := context.Background()

	// 1. Initialize Cookie Jar (if path is provided)
	cookieJar, err := tools.LoadCookieJarFromFile(cfg.CookieFile)
	if err != nil {
		log.Printf("[WARN] Cookie jar init: %v", err)
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

		localText, localErr := tools.ExtractPDFText(body, tools.MaxDocumentTextBytes)
		if localErr != nil {
			return "", "", fmt.Errorf("gemini extraction failed (%v) and local parsing failed (%w)", err, localErr)
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
	company := strings.TrimSpace(r.FormValue("company_name"))
	location := strings.TrimSpace(r.FormValue("location"))
	companyBrief := models.CompanyBrief{
		CompanyName: query,
		Domain:      strings.TrimSpace(r.FormValue("company_domain")),
		Industry:    strings.TrimSpace(r.FormValue("company_industry")),
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
			profile, researchErr := s.personAgent.ResearchPerson(ctx, sessionID, query, company, location, nil)
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

	s.startRun(w, sessionID, mode, query, company, location, companyBrief, browserEnabled, browserExplicit)
}

// startRun launches a research session in the background and renders the live
// workflow console for HTMX.
func (s *Server) startRun(w http.ResponseWriter, sessionID, mode, query, personCompany, location string, brief models.CompanyBrief, browserEnabled, browserExplicit bool) {
	session := s.hub.Create(sessionID, mode, query)

	timeout := s.cfg.ResearchTimeoutFor(browserEnabled)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		ctx = withBrowserOverride(ctx, browserEnabled, browserExplicit)

		if mode == "person" {
			profile, err := s.personAgent.ResearchPerson(ctx, sessionID, query, personCompany, location, session.EventCh)
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
	s.startRun(w, newSessionID(), "company", brief.CompanyName, "", "", brief, browserEnabled, browserExplicit)
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

// newSessionID returns a random session identifier that is safe under concurrency.
func newSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("failed to generate session id: %v", err)
	}
	return "sess_" + hex.EncodeToString(b)
}
