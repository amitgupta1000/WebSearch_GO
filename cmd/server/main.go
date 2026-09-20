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
	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
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
}

type companyResultView struct {
	Chart     *models.OrgChart
	SessionID string
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

	// 2. Initialize Cache, Scraper and Serper
	memCache := cache.NewMemoryCache(cfg.CacheMaxEntries)
	defer memCache.Close()
	scraper := tools.NewScraper(cookieJar, 15*time.Second)
	serper := tools.NewSerperClient(cfg.SerperAPIKey)

	// 3. Initialize Gemini LLM client. Fail fast: research cannot run without it.
	gemini, err := llm.NewGeminiClient(ctx, cfg.GeminiModel)
	if err != nil {
		log.Fatalf("Gemini client initialization failed (check GCP credentials or GOOGLE_API_KEY): %v", err)
	}

	// 4. Initialize Core Engine & Domain Agents
	eng := agent.NewEngine(gemini, serper, scraper, memCache, cfg.MaxScrapesPerRun, cfg.MaxSearchResults, cfg.MaxSteps)
	compAgent := agent.NewCompanyAgent(eng)
	persAgent := agent.NewPersonAgent(eng)
	sessionHub := agent.NewSessionHub()

	// 5. Parse templates embedded into the binary (independent of working directory)
	tmpl, err := web.Templates()
	if err != nil {
		log.Fatalf("Template parsing failed: %v", err)
	}

	srv := &Server{
		cfg:          cfg,
		companyAgent: compAgent,
		personAgent:  persAgent,
		hub:          sessionHub,
		tmpl:         tmpl,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", srv.handleIndex)
	mux.HandleFunc("POST /api/research", srv.handleResearch)
	mux.HandleFunc("POST /api/question", srv.handleQuestion)
	mux.HandleFunc("GET /api/events", srv.handleEvents)
	mux.HandleFunc("GET /healthz", srv.handleHealth)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      cfg.ResearchTimeout + 30*time.Second,
		IdleTimeout:       90 * time.Second,
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
	if err := s.tmpl.ExecuteTemplate(w, "index.html", nil); err != nil {
		log.Printf("[ERROR] index template execution failed: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// handleResearch runs a research task and renders the result as an HTML fragment
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

	sessionID := newSessionID()

	// Synchronous mode for CLI or automated testing
	if r.URL.Query().Get("sync") == "1" {
		ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ResearchTimeout)
		defer cancel()

		if mode == "person" {
			profile, err := s.personAgent.ResearchPerson(ctx, sessionID, query, company, location, nil)
			if err != nil {
				log.Printf("[ERROR] person research failed (session %s): %v", sessionID, err)
				s.renderError(w, "Person research failed: "+err.Error())
				return
			}
			s.render(w, "person_dossier", profile)
			return
		}

		chart, err := s.companyAgent.ResearchCompanyWithBrief(ctx, sessionID, companyBrief, nil)
		if err != nil {
			log.Printf("[ERROR] company research failed (session %s): %v", sessionID, err)
			s.renderError(w, "Company research failed: "+err.Error())
			return
		}
		s.render(w, "company_chart", chart)
		return
	}

	// Interactive mode: create session and start research in background
	session := s.hub.Create(sessionID, mode, query)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ResearchTimeout)
		defer cancel()

		if mode == "person" {
			profile, err := s.personAgent.ResearchPerson(ctx, sessionID, query, company, location, session.EventCh)
			if err != nil {
				log.Printf("[ERROR][session %s] person research failed: %v", sessionID, err)
			} else {
				log.Printf("[RESULT][session %s] person research completed", sessionID)
			}
			session.Complete(profile, err)
		} else {
			chart, companyContext, err := s.companyAgent.ResearchCompanyRun(ctx, sessionID, companyBrief, session.EventCh)
			if err != nil {
				log.Printf("[ERROR][session %s] company research failed: %v", sessionID, err)
			} else {
				log.Printf("[RESULT][session %s] company=%s leadership=%d departments=%d", sessionID, chart.CompanyName, len(chart.Leadership), len(chart.Departments))
			}
			session.SetCompanyContext(companyContext)
			session.Complete(chart, err)
		}
	}()

	displayMode := "Company"
	if mode == "person" {
		displayMode = "Person"
	}

	s.render(w, "workflow_console", map[string]string{
		"SessionID": sessionID,
		"Mode":      displayMode,
		"Query":     query,
	})
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

	// Stream any events that were already recorded before SSE connected
	for _, ev := range session.GetHistory() {
		sendSSE("log", renderFragment("log_entry", ev))
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

			// Render final result
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
					sendSSE("result", renderFragment("company_result", companyResultView{Chart: chart, SessionID: session.ID}))
				}
				sendSSE("status", `<span class="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-medium bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">● Complete</span>`)
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
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com https://unpkg.com; "+
				"style-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com; "+
				"connect-src 'self'; img-src 'self' data:")
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
