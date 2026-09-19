package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/agent"
	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/config"
	"github.com/amitgupta1000/WebSearch_GO/internal/llm"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

type Server struct {
	cfg          *config.Config
	companyAgent *agent.CompanyAgent
	personAgent  *agent.PersonAgent
	tmpl         *template.Template
}

func main() {
	cfg := config.Load()

	ctx := context.Background()

	// 1. Initialize Cookie Jar (if path is provided)
	cookieJar, err := tools.LoadCookieJarFromFile(cfg.CookieFile)
	if err != nil {
		log.Printf("[WARN] Cookie jar init: %v", err)
	}

	// 2. Initialize Cache, Scraper and Serper
	memCache := cache.NewMemoryCache()
	scraper := tools.NewScraper(cookieJar, 15*time.Second)
	serper := tools.NewSerperClient(cfg.SerperAPIKey)

	// 3. Initialize Gemini LLM client
	gemini, err := llm.NewGeminiClient(ctx, cfg.GeminiModel)
	if err != nil {
		log.Printf("[WARN] Gemini client initialization error (check GCP credentials/API keys): %v", err)
	}

	// 4. Initialize Core Engine & Domain Agents
	eng := agent.NewEngine(gemini, serper, scraper, memCache)
	compAgent := agent.NewCompanyAgent(eng)
	persAgent := agent.NewPersonAgent(eng)

	// 5. Parse templates
	tmpl := template.Must(template.ParseGlob("web/templates/*.html"))

	srv := &Server{
		cfg:          cfg,
		companyAgent: compAgent,
		personAgent:  persAgent,
		tmpl:         tmpl,
	}

	http.HandleFunc("/", srv.handleIndex)
	http.HandleFunc("/api/research", srv.handleResearch)

	addr := ":" + cfg.Port
	log.Printf("🚀 WebSearch_GO server starting on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	_ = s.tmpl.ExecuteTemplate(w, "index.html", nil)
}

func (s *Server) handleResearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mode := r.FormValue("mode")
	query := r.FormValue("query")
	company := r.FormValue("company_name")
	location := r.FormValue("location")

	sessionID := fmt.Sprintf("sess_%d", time.Now().Unix())
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	if mode == "person" {
		profile, err := s.personAgent.ResearchPerson(ctx, sessionID, query, company, location, nil)
		if err != nil {
			fmt.Fprintf(w, `<div class="p-4 bg-red-950/50 border border-red-800 text-red-300 rounded-xl">Error: %s</div>`, err.Error())
			return
		}
		renderPersonDossier(w, profile)
	} else {
		chart, err := s.companyAgent.ResearchCompany(ctx, sessionID, query, nil)
		if err != nil {
			fmt.Fprintf(w, `<div class="p-4 bg-red-950/50 border border-red-800 text-red-300 rounded-xl">Error: %s</div>`, err.Error())
			return
		}
		renderCompanyChart(w, chart)
	}
}

func renderCompanyChart(w http.ResponseWriter, c *models.OrgChart) {
	fmt.Fprintf(w, `
	<div class="bg-slate-900 border border-slate-800 rounded-2xl p-6 space-y-6">
		<div class="flex items-start justify-between border-b border-slate-800 pb-4">
			<div>
				<h3 class="text-2xl font-bold text-white">%s</h3>
				<p class="text-xs text-slate-400 mt-1">HQ: %s • Domain: %s</p>
			</div>
			<span class="text-xs bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 px-3 py-1 rounded-full">Research Complete</span>
		</div>
		<div>
			<h4 class="text-sm font-semibold text-slate-300 mb-1">Company Overview</h4>
			<p class="text-sm text-slate-400 leading-relaxed">%s</p>
		</div>
		<div>
			<h4 class="text-sm font-semibold text-slate-300 mb-3">Executive Leadership</h4>
			<div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">`,
		c.CompanyName, c.Headquarters, c.Domain, c.Overview)

	for _, l := range c.Leadership {
		fmt.Fprintf(w, `
			<div class="bg-slate-950 border border-slate-800/80 rounded-xl p-3">
				<div class="font-medium text-white text-sm">%s</div>
				<div class="text-xs text-indigo-400">%s</div>
				<div class="text-xs text-slate-500 mt-1">%s</div>
			</div>`, l.Name, l.Title, l.Bio)
	}

	fmt.Fprintf(w, `</div></div>
		<div>
			<h4 class="text-sm font-semibold text-slate-300 mb-3">Departments & Functions</h4>
			<div class="grid grid-cols-1 md:grid-cols-2 gap-4">`)

	for _, d := range c.Departments {
		fmt.Fprintf(w, `
			<div class="bg-slate-950 border border-slate-800 rounded-xl p-4">
				<div class="flex items-center justify-between mb-2">
					<span class="font-semibold text-white text-sm">%s</span>
					<span class="text-xs text-slate-500">%d members</span>
				</div>
				<p class="text-xs text-slate-400 mb-3">%s</p>
				<div class="space-y-1">`, d.Name, len(d.Members), d.Description)

		for _, m := range d.Members {
			fmt.Fprintf(w, `<div class="text-xs text-slate-300 flex justify-between"><span>%s</span><span class="text-slate-500">%s</span></div>`, m.Name, m.Title)
		}
		fmt.Fprintf(w, `</div></div>`)
	}

	fmt.Fprintf(w, `</div></div>
		<div class="border-t border-slate-800/60 pt-3 flex flex-wrap gap-2 items-center text-xs text-slate-500">
			<span>Sources:</span>`)
	for _, s := range c.Sources {
		fmt.Fprintf(w, `<a href="%s" target="_blank" class="text-indigo-400 hover:underline truncate max-w-xs">%s</a>`, s, s)
	}
	fmt.Fprintf(w, `</div></div>`)
}

func renderPersonDossier(w http.ResponseWriter, p *models.PersonProfile) {
	fmt.Fprintf(w, `
	<div class="bg-slate-900 border border-slate-800 rounded-2xl p-6 space-y-6">
		<div class="flex items-start justify-between border-b border-slate-800 pb-4">
			<div>
				<h3 class="text-2xl font-bold text-white">%s</h3>
				<p class="text-sm text-indigo-400 mt-0.5">%s • %s</p>
				<p class="text-xs text-slate-500 mt-0.5">Location: %s</p>
			</div>
			<div class="text-right">
				<span class="text-xs bg-indigo-500/10 text-indigo-400 border border-indigo-500/20 px-3 py-1 rounded-full font-medium">Score: %.0f%%</span>
			</div>
		</div>

		<div>
			<h4 class="text-sm font-semibold text-slate-300 mb-1">Executive Summary</h4>
			<p class="text-sm text-slate-400 leading-relaxed">%s</p>
		</div>

		<div class="grid grid-cols-1 md:grid-cols-2 gap-6">
			<div>
				<h4 class="text-sm font-semibold text-slate-300 mb-3">Career History</h4>
				<div class="space-y-3">`,
		p.FullName, p.CurrentTitle, p.CurrentCompany, p.Location, p.ConfidenceScore*100, p.ExecutiveSummary)

	for _, exp := range p.Experiences {
		fmt.Fprintf(w, `
			<div class="bg-slate-950 border border-slate-800 rounded-xl p-3">
				<div class="text-sm font-medium text-white">%s</div>
				<div class="text-xs text-indigo-400">%s (%s)</div>
			</div>`, exp.Role, exp.Company, exp.Period)
	}

	fmt.Fprintf(w, `</div></div>
			<div>
				<h4 class="text-sm font-semibold text-slate-300 mb-3">Education & Skills</h4>
				<div class="space-y-3">`)

	for _, edu := range p.Education {
		fmt.Fprintf(w, `
			<div class="bg-slate-950 border border-slate-800 rounded-xl p-3">
				<div class="text-sm font-medium text-white">%s</div>
				<div class="text-xs text-slate-400">%s in %s (%s)</div>
			</div>`, edu.Institution, edu.Degree, edu.Field, edu.Year)
	}

	fmt.Fprintf(w, `<div class="flex flex-wrap gap-1.5 pt-2">`)
	for _, s := range p.KeySkills {
		fmt.Fprintf(w, `<span class="text-xs bg-slate-800 text-slate-300 px-2.5 py-1 rounded-md">%s</span>`, s)
	}

	fmt.Fprintf(w, `</div></div></div></div>`)
}
