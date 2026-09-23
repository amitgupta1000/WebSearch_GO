#!/usr/bin/env python3
"""WebSearch_GO Project Guide deck generator (python-pptx).

Builds an executive-style presentation that mirrors docs/ARCHITECTURE.md, using a dark-title,
light-card visual system (slate headers, light content slides).

Run:
    pip install python-pptx
    python generate_project_guide_deck.py
Output:
    WebSearch_GO_Project_Guide.pptx, next to this script (generated on demand, not checked in)
"""
from pathlib import Path

from pptx import Presentation
from pptx.util import Inches, Pt
from pptx.dml.color import RGBColor
from pptx.enum.text import PP_ALIGN
from pptx.enum.shapes import MSO_SHAPE


def create_project_guide_deck():
    prs = Presentation()
    prs.slide_width = Inches(13.333)
    prs.slide_height = Inches(7.5)
    blank_layout = prs.slide_layouts[6]

    # Professional Executive Color Palette (slate headers, light cards)
    BG_DARK = RGBColor(15, 23, 42)          # Slate 900 (Deep Executive Navy)
    BG_LIGHT = RGBColor(248, 250, 252)      # Slate 50
    CARD_BG = RGBColor(255, 255, 255)       # Pure White
    CARD_BORDER = RGBColor(226, 232, 240)   # Slate 200

    PRIMARY_BLUE = RGBColor(29, 78, 216)    # Deep Royal Blue
    ACCENT_TEAL = RGBColor(13, 148, 136)    # Professional Teal
    ACCENT_INDIGO = RGBColor(79, 70, 229)   # Indigo
    ACCENT_AMBER = RGBColor(217, 119, 6)    # Warning / Insight Amber
    ACCENT_ROSE = RGBColor(225, 29, 72)     # Pitfall / Failure Red
    ACCENT_SLATE = RGBColor(71, 85, 105)    # Neutral Slate

    TEXT_DARK = RGBColor(15, 23, 42)        # Slate 900
    TEXT_BODY = RGBColor(51, 65, 85)        # Slate 700
    TEXT_MUTED = RGBColor(100, 116, 139)    # Slate 500
    TEXT_LIGHT = RGBColor(255, 255, 255)

    CATEGORY = "WEBSEARCH_GO - CODEBASE REVIEW & PROJECT GUIDE"

    def set_slide_bg(slide, color):
        bg = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, 0, 0, Inches(13.333), Inches(7.5))
        bg.fill.solid()
        bg.fill.fore_color.rgb = color
        bg.line.fill.background()
        return bg

    def add_header(slide, title_text, category_text=None, is_dark=False):
        cat_box = slide.shapes.add_textbox(Inches(0.8), Inches(0.4), Inches(11.7), Inches(0.3))
        p_c = cat_box.text_frame.paragraphs[0]
        p_c.text = (category_text or CATEGORY).upper()
        p_c.font.name = "Arial"
        p_c.font.size = Pt(10)
        p_c.font.bold = True
        p_c.font.color.rgb = PRIMARY_BLUE if not is_dark else RGBColor(147, 197, 253)

        title_box = slide.shapes.add_textbox(Inches(0.8), Inches(0.65), Inches(11.7), Inches(0.7))
        tf_t = title_box.text_frame
        tf_t.word_wrap = True
        p_t = tf_t.paragraphs[0]
        p_t.text = title_text
        p_t.font.name = "Arial"
        p_t.font.size = Pt(22)
        p_t.font.bold = True
        p_t.font.color.rgb = TEXT_LIGHT if is_dark else TEXT_DARK

        line = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(0.8), Inches(1.38), Inches(11.733), Inches(0.02))
        line.fill.solid()
        line.fill.fore_color.rgb = RGBColor(51, 65, 85) if is_dark else CARD_BORDER
        line.line.fill.background()

    def add_card(slide, left, top, width, height, title, items, top_color=PRIMARY_BLUE,
                 bg_color=CARD_BG, is_code=False, item_size=11.5):
        card = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(left), Inches(top), Inches(width), Inches(height))
        card.fill.solid()
        card.fill.fore_color.rgb = bg_color
        card.line.color.rgb = CARD_BORDER
        card.line.width = Pt(1)

        stripe = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(left), Inches(top), Inches(width), Inches(0.08))
        stripe.fill.solid()
        stripe.fill.fore_color.rgb = top_color
        stripe.line.fill.background()

        tb_h = slide.shapes.add_textbox(Inches(left + 0.25), Inches(top + 0.15), Inches(width - 0.5), Inches(0.45))
        p_h = tb_h.text_frame.paragraphs[0]
        p_h.text = title
        p_h.font.name = "Arial"
        p_h.font.size = Pt(13)
        p_h.font.bold = True
        p_h.font.color.rgb = TEXT_DARK

        tb = slide.shapes.add_textbox(Inches(left + 0.25), Inches(top + 0.65), Inches(width - 0.5), Inches(height - 0.8))
        tf = tb.text_frame
        tf.word_wrap = True

        for i, item in enumerate(items):
            p = tf.add_paragraph() if i > 0 else tf.paragraphs[0]
            if isinstance(item, tuple):
                p.text = item[0] + ": "
                p.font.bold = True
                p.font.name = "Courier New" if is_code else "Arial"
                p.font.size = Pt(min(item_size, 10) if is_code else item_size)
                p.font.color.rgb = top_color
                run = p.add_run()
                run.text = item[1]
                run.font.bold = False
                run.font.name = "Courier New" if is_code else "Arial"
                run.font.size = Pt(min(item_size, 10) if is_code else item_size)
                run.font.color.rgb = TEXT_BODY
            else:
                p.text = item
                p.font.name = "Courier New" if is_code else "Arial"
                p.font.size = Pt(min(item_size, 10) if is_code else item_size)
                p.font.color.rgb = TEXT_BODY
            p.space_after = Pt(5)

    def add_flow_box(slide, left, top, width, height, title, desc, color, title_size=11.5, desc_size=9):
        box = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(left), Inches(top), Inches(width), Inches(height))
        box.fill.solid()
        box.fill.fore_color.rgb = CARD_BG
        box.line.color.rgb = CARD_BORDER
        box.line.width = Pt(1)

        stripe = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(left), Inches(top), Inches(width), Inches(0.07))
        stripe.fill.solid()
        stripe.fill.fore_color.rgb = color
        stripe.line.fill.background()

        tb = slide.shapes.add_textbox(Inches(left + 0.15), Inches(top + 0.14), Inches(width - 0.3), Inches(height - 0.28))
        tf = tb.text_frame
        tf.word_wrap = True
        p = tf.paragraphs[0]
        p.text = title
        p.font.name = "Arial"
        p.font.bold = True
        p.font.size = Pt(title_size)
        p.font.color.rgb = TEXT_DARK

        pd = tf.add_paragraph()
        pd.text = desc
        pd.font.name = "Arial"
        pd.font.size = Pt(desc_size)
        pd.font.color.rgb = TEXT_BODY
        pd.space_before = Pt(4)

    def add_dark_pillar(slide, left, top, width, height, title, body, color,
                        title_size=14, body_size=11.5):
        card = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(left), Inches(top), Inches(width), Inches(height))
        card.fill.solid()
        card.fill.fore_color.rgb = RGBColor(30, 41, 59)
        card.line.color.rgb = RGBColor(51, 65, 85)
        card.line.width = Pt(1)

        stripe = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(left), Inches(top), Inches(width), Inches(0.07))
        stripe.fill.solid()
        stripe.fill.fore_color.rgb = color
        stripe.line.fill.background()

        tb_t = slide.shapes.add_textbox(Inches(left + 0.2), Inches(top + 0.2), Inches(width - 0.4), Inches(0.7))
        tf_t = tb_t.text_frame
        tf_t.word_wrap = True
        p_t = tf_t.paragraphs[0]
        p_t.text = title
        p_t.font.name = "Arial"
        p_t.font.bold = True
        p_t.font.size = Pt(title_size)
        p_t.font.color.rgb = TEXT_LIGHT

        tb = slide.shapes.add_textbox(Inches(left + 0.2), Inches(top + 0.95), Inches(width - 0.4), Inches(height - 1.15))
        tf = tb.text_frame
        tf.word_wrap = True
        for i, line in enumerate(body):
            p = tf.add_paragraph() if i > 0 else tf.paragraphs[0]
            p.text = line
            p.font.name = "Arial"
            p.font.size = Pt(body_size)
            p.font.color.rgb = RGBColor(203, 213, 225)
            p.space_before = Pt(6)

    # ----------------------------------------------------
    # SLIDE 1: TITLE (Executive Slate Dark)
    # ----------------------------------------------------
    s1 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s1, BG_DARK)

    tag = s1.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(0.8), Inches(1.6), Inches(4.4), Inches(0.35))
    tag.fill.solid()
    tag.fill.fore_color.rgb = RGBColor(30, 41, 59)
    tag.line.color.rgb = PRIMARY_BLUE
    tag.line.width = Pt(1)
    p_tag = tag.text_frame.paragraphs[0]
    p_tag.text = "CODEBASE REVIEW & PROJECT GUIDE"
    p_tag.alignment = PP_ALIGN.CENTER
    p_tag.font.name = "Arial"
    p_tag.font.size = Pt(9.5)
    p_tag.font.bold = True
    p_tag.font.color.rgb = RGBColor(147, 197, 253)

    tb = s1.shapes.add_textbox(Inches(0.8), Inches(2.15), Inches(11.7), Inches(2.0))
    tf = tb.text_frame
    tf.word_wrap = True
    p1 = tf.paragraphs[0]
    p1.text = "WebSearch_GO - Project Guide"
    p1.font.name = "Arial"
    p1.font.size = Pt(36)
    p1.font.bold = True
    p1.font.color.rgb = TEXT_LIGHT

    p2 = tf.add_paragraph()
    p2.text = "How the agentic web research server works: modules, files, data flow, budgets and deployment"
    p2.font.name = "Arial"
    p2.font.size = Pt(16)
    p2.font.color.rgb = RGBColor(148, 163, 184)
    p2.space_before = Pt(8)

    pillars = [
        ("Agentic Research", "Autonomous Go agents run company org-chart and person-dossier research over live Google search and web scraping.", 0.8, PRIMARY_BLUE),
        ("Gemini Structured Output", "Gemini 2.5 Flash Lite synthesizes evidence into strict JSON schemas, with cleaning and retry resilience.", 4.8, ACCENT_TEAL),
        ("Streaming, Bounded, Self-Contained", "Every agent step streams to HTMX over SSE; budgets and FIFO caches bound cost; templates embed into one binary.", 8.8, ACCENT_INDIGO),
    ]
    for title, desc, x_pos, col in pillars:
        add_dark_pillar(s1, x_pos, 4.6, 3.73, 2.0, title, [desc], col, title_size=12.5, body_size=10.5)

    # ----------------------------------------------------
    # SLIDE 2: WHAT THE PROJECT IS
    # ----------------------------------------------------
    s2 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s2, BG_LIGHT)
    add_header(s2, "One System, Two Autonomous Research Modes")

    add_card(s2, 0.8, 1.6, 3.7, 5.3, "What It Is", [
        ("Agentic server", "A self-contained Go 1.23 web server that runs LLM-driven research agents over live Google search (Serper) and web scraping."),
        ("Real-time UI", "HTMX console; every agent step streams to the browser over Server-Sent Events."),
        ("Bounded & hardened", "Strict cost budgets, FIFO-bounded caches, security headers, graceful shutdown, /healthz probe."),
        ("Module", "github.com/amitgupta1000/WebSearch_GO - one binary, templates embedded via go:embed.")
    ], PRIMARY_BLUE)

    add_card(s2, 4.8, 1.6, 3.7, 5.3, "Company Mode -> OrgChart", [
        ("Produces", "Structured OrgChart: overview, HQ, leadership, departments, locations, sources."),
        ("Pipeline", "LLM plans Google queries, results are ranked for relevance, top pages are scraped, Gemini synthesizes the chart from evidence only."),
        ("Entry point", "CompanyAgent.ResearchCompanyWithBrief(ctx, sessionID, CompanyBrief, eventCh) returns *models.OrgChart."),
        ("Drill-down", "Every rendered employee exposes an 'Open dossier' sync form.")
    ], ACCENT_TEAL)

    add_card(s2, 8.8, 1.6, 3.7, 5.3, "Person Mode -> PersonProfile", [
        ("Produces", "Professional dossier: title, company, bio, experiences, education, skills, confidence score."),
        ("Pipeline", "Disambiguated search (name + company + location), bio-oriented pages scraped, secondary career search, Gemini synthesis."),
        ("Entry point", "PersonAgent.ResearchPerson(ctx, sessionID, name, company, location, eventCh) returns *models.PersonProfile."),
        ("Reuse", "Also powers the drill-down from company leadership cards.")
    ], ACCENT_INDIGO)

    # ----------------------------------------------------
    # SLIDE 3: SIX COOPERATING LAYERS
    # ----------------------------------------------------
    s3 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s3, BG_LIGHT)
    add_header(s3, "Six Cooperating Layers Keep the Packages Decoupled")

    layers = [
        (0.8, 1.6, "1. HTTP Layer", "cmd/server + internal/web", PRIMARY_BLUE,
         ["HTMX single-page console; templates embedded via go:embed.",
          "Routes: /, POST /api/research, GET /api/events (SSE), /healthz."]),
        (4.8, 1.6, "2. Agentic Orchestration", "internal/agent", ACCENT_TEAL,
         ["Shared Engine plus company/person specialists: plan, search, rank, scrape, synthesize.",
          "SessionHub tracks live event streams per research run."]),
        (8.8, 1.6, "3. Tooling", "internal/tools", ACCENT_AMBER,
         ["Serper.dev search client with retry + backoff.",
          "Sanitized goquery HTML scraper; browser-cookie import."]),
        (0.8, 4.25, "4. LLM Layer", "internal/llm", ACCENT_INDIGO,
         ["Gemini 2.5 Flash Lite client returning structured JSON.",
          "Response cleaning + one stricter retry; temperature 0.2."]),
        (4.8, 4.25, "5. State & Caching", "internal/cache + agent/hub.go", ACCENT_SLATE,
         ["Bounded TTL cache for searches (48 h) and scrapes (7 d).",
          "Thread-safe session hub with 15-minute TTL cleanup."]),
        (8.8, 4.25, "6. Shared Vocabulary", "internal/models", PRIMARY_BLUE,
         ["Every struct that crosses a package boundary: events, briefs, plans, outputs.",
          "OrgChart, PersonProfile, AgentEvent, QueryPlan, Evidence, ..."]),
    ]
    for x, y, title, path, color, items in layers:
        add_card(s3, x, y, 3.7, 2.5, title, [path] + items, color, item_size=10.5)

    # ----------------------------------------------------
    # SLIDE 4: END-TO-END REQUEST LIFECYCLE
    # ----------------------------------------------------
    s4 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s4, BG_LIGHT)
    add_header(s4, "How a Research Request Flows End-to-End")

    add_card(s4, 0.8, 1.6, 3.7, 5.3, "Steps 1-2: Console & Submit", [
        ("Step 1 - Open /", "handleIndex renders the embedded index.html research console (Tailwind + HTMX from CDN)."),
        ("Step 2 - POST /api/research", "HTMX submits mode, query, company_name, location plus optional company-brief fields. Body capped at 1 MiB (DoS guard)."),
        ("Validation", "Empty queries are rejected with an error_box fragment; a random sess_ session ID comes from crypto/rand.")
    ], PRIMARY_BLUE)

    add_card(s4, 4.8, 1.6, 3.7, 5.3, "Steps 3-4: Session & Agent", [
        ("Step 3 - Session created", "SessionHub stores a Session with a buffered EventCh (100) and Done channel; a goroutine runs the agent under a ResearchTimeout context. workflow_console returns immediately. ?sync=1 runs inline instead."),
        ("Step 4 - Pipeline runs", "Agents emit AgentEvents (thinking / tool_call / tool_result / complete / error) via non-blocking Emit - a slow consumer can never stall research.")
    ], ACCENT_TEAL)

    add_card(s4, 8.8, 1.6, 3.7, 5.3, "Steps 5-6: Stream & Drill-down", [
        ("Step 5 - SSE /api/events", "Replays history, streams color-coded log_entry fragments, drains on Done, sends the final company_chart / person_dossier (or error_box) + status pill + close event."),
        ("Step 6 - Drill-down", "Org-chart employees embed 'Open dossier' forms posting /api/research?sync=1 (mode=person) into the chart's drill-down area."),
        ("Two entry points", "Only CompanyAgent.ResearchCompanyWithBrief and PersonAgent.ResearchPerson; nil eventCh disables emission (tests).")
    ], ACCENT_INDIGO)

    # ----------------------------------------------------
    # SLIDE 5: SYSTEM ARCHITECTURE (component bands)
    # ----------------------------------------------------
    s5 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s5, BG_LIGHT)
    add_header(s5, "System Architecture: Layered Component View")

    bands = [
        ("PRESENTATION", 1.7, PRIMARY_BLUE, [
            ("Browser console", "HTMX + Tailwind"),
            ("cmd/server routes", "index / research"),
            ("SSE handler", "/api/events"),
            ("Embedded templates", "go:embed"),
        ]),
        ("AGENTIC ORCHESTRATION", 3.05, ACCENT_TEAL, [
            ("SessionHub", "session lifecycle"),
            ("CompanyAgent", "org charts"),
            ("PersonAgent", "dossiers"),
            ("Engine", "interfaces + budgets"),
        ]),
        ("TOOLS & INTELLIGENCE", 4.4, ACCENT_AMBER, [
            ("SerperClient", "Google search"),
            ("Scraper", "fetch + clean"),
            ("GeminiClient", "structured JSON"),
            ("MemoryCache", "TTL + FIFO"),
        ]),
        ("EXTERNAL SYSTEMS", 5.75, ACCENT_SLATE, [
            ("Serper.dev API", "Google SERP"),
            ("Target websites", "public pages"),
            ("Gemini / Vertex AI", "LLM backend"),
            ("Cookie jar", "optional import"),
        ]),
    ]
    for label, y, color, boxes in bands:
        band = s5.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(0.8), Inches(y), Inches(7.6), Inches(1.15))
        band.fill.solid()
        band.fill.fore_color.rgb = RGBColor(241, 245, 249)
        band.line.color.rgb = CARD_BORDER
        band.line.width = Pt(1)
        tb_l = s5.shapes.add_textbox(Inches(0.95), Inches(y + 0.1), Inches(1.3), Inches(0.95))
        tf_l = tb_l.text_frame
        tf_l.word_wrap = True
        p_l = tf_l.paragraphs[0]
        p_l.text = label
        p_l.font.name = "Arial"
        p_l.font.size = Pt(8.5)
        p_l.font.bold = True
        p_l.font.color.rgb = color
        for i, (bt, bd) in enumerate(boxes):
            add_flow_box(s5, 2.35 + i * 1.48, y + 0.2, 1.42, 0.75, bt, bd, color, title_size=8.5, desc_size=7.5)

    add_card(s5, 8.7, 1.7, 3.83, 5.2, "Key Architectural Decisions", [
        ("Dependency inversion", "internal/agent never imports tools or llm. Engine declares Searcher / PageFetcher / LLMClient interfaces; main wires implementations - agents unit-test with plain mocks."),
        ("Shared vocabulary", "All boundary crossings use internal/models structs, keeping packages decoupled."),
        ("Composition root", "cmd/server builds the object graph in dependency order; nothing else constructs dependencies."),
        ("Streaming, not polling", "Per-session channel -> SSE handler -> HTMX swap, with history replay for (re)connects.")
    ], PRIMARY_BLUE, item_size=10.5)

    # ----------------------------------------------------
    # SLIDE 6: REPOSITORY LAYOUT
    # ----------------------------------------------------
    s6 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s6, BG_LIGHT)
    add_header(s6, "Repository Layout & the Role of Every File")

    add_card(s6, 0.8, 1.6, 5.7, 5.3, "Go Service (compiled into the binary)", [
        ("cmd/server/main.go", "Composition root: config, DI, routes, SSE, graceful shutdown"),
        ("internal/agent/", "engine.go (ReAct core), company.go, person.go, hub.go (sessions)"),
        ("internal/tools/", "serper.go (search), scraper.go (fetch+sanitize), cookies.go (jar import)"),
        ("internal/llm/", "gemini.go (structured JSON, cleaning, retry)"),
        ("internal/cache/", "cache.go (bounded TTL cache, FIFO eviction, sha256 keys)"),
        ("internal/models/", "agent.go, company.go, person.go, research.go (shared structs)"),
        ("internal/web/", "web.go (go:embed) + templates/index.html + fragments.html"),
        ("Tests", "*_test.go next to each package; no network required")
    ], PRIMARY_BLUE, is_code=True, item_size=9.5)

    add_card(s6, 6.8, 1.6, 5.7, 5.3, "Tooling, Docs & Auxiliary Files", [
        ("scripts/dev.ps1", "Windows hot-reload watcher (750 ms fingerprint, restarts go run)"),
        (".vscode/tasks.json", "'Go: Watch Server' task wrapping dev.ps1"),
        (".github/workflows/go.yml", "CI: tidy -> gofmt -> vet -> build -> test"),
        ("Dockerfile", "golang:1.24-alpine -> distroless/static-debian12:nonroot"),
        ("go.mod / go.sum", "goquery + google.golang.org/genai (Go 1.24)"),
        ("README.md / docs/ARCHITECTURE.md", "Usage quickstart / architecture & internals"),
        ("react_engine.py", "Standalone Python replica of the ReAct engine (CLI demo)"),
        ("generate_project_guide_deck.py", "python-pptx generator for this deck"),
        ("websearch.exe", "Compiled Windows binary (build artifact)")
    ], ACCENT_TEAL, is_code=True, item_size=9.5)

    # ----------------------------------------------------
    # SLIDE 7: THE AGENTIC CORE (internal/agent)
    # ----------------------------------------------------
    s7 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s7, BG_LIGHT)
    add_header(s7, "Deep Dive: internal/agent - Engine, Sessions, Budgets")

    add_card(s7, 0.8, 1.6, 3.7, 5.3, "Engine - Shared ReAct Plumbing", [
        ("Tool interfaces", "Searcher, PageFetcher, LLMClient - satisfied by SerperClient / Scraper / GeminiClient without importing them."),
        ("Cached tools", "SearchCached (48 h TTL) and ScrapeCached (7 d TTL) keyed by sha256 of query / URL."),
        ("Emit", "Non-blocking event publisher: drops (never blocks) when the 100-slot channel is full; nil channel = no-op."),
        ("Synthesis", "SynthesizeJSON / GenerateStructured delegate to the LLM; FormatSearchSnippets renders numbered markdown lists for prompts.")
    ], PRIMARY_BLUE)

    add_card(s7, 4.8, 1.6, 3.7, 5.3, "Session & SessionHub (hub.go)", [
        ("Session", "ID, Mode, Query, buffered EventCh (100), event history, Result/Err, Done closed exactly once via sync.Once."),
        ("History", "AddHistory / GetHistory are mutex-guarded; GetHistory returns a copy for safe replay."),
        ("SessionHub", "RWMutex-protected map; background sweep every 5 minutes deletes sessions older than 15 minutes."),
        ("Why it matters", "Late or reconnecting viewers replay history; abandoned sessions cannot leak memory.")
    ], ACCENT_TEAL)

    add_card(s7, 8.8, 1.6, 3.7, 5.3, "Budget Accessors (defaults)", [
        ("MaxScrapes", "3 pages per run (MAX_SCRAPES_PER_RUN)"),
        ("MaxSearchResults", "8 per query (MAX_SEARCH_RESULTS)"),
        ("MaxSteps", "10 iterations per run (MAX_AGENT_STEPS)"),
        ("Nil-safe", "Accessors fall back to package defaults whenever a configured value is <= 0."),
        ("Tested", "engine_test.go covers custom limits, defaults, and mock searcher/scraper/LLM wiring.")
    ], ACCENT_AMBER)

    # ----------------------------------------------------
    # SLIDE 8: COMPANY RESEARCH PIPELINE (5 stages)
    # ----------------------------------------------------
    s8 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s8, BG_LIGHT)
    add_header(s8, "CompanyAgent Pipeline: Plan -> Search -> Rank -> Scrape -> Synthesize")

    stages = [
        (0.8, "1. Plan", PRIMARY_BLUE,
         "One LLM call turns the CompanyBrief into a QueryPlan: up to 6 deduped queries with purpose, target_area, priority 1-5, covering leadership, departments, locations and requested areas."),
        (3.18, "2. Search", ACCENT_TEAL,
         "Each planned query runs through SearchCached. URLs deduplicate case-insensitively across queries; individual query failures are tolerated."),
        (5.56, "3. Rank", ACCENT_INDIGO,
         "LLM scores results 0-1 and marks relevance; normalizeRankedResults keeps only known, relevant URLs sorted by score. Deterministic keyword fallback if the LLM fails."),
        (7.94, "4. Scrape", ACCENT_AMBER,
         "Walks ranked URLs in score order, validates absolute URLs, scrapes at most MaxScrapes pages (>80 bytes of text), producing Evidence + sources."),
        (10.32, "5. Synthesize", ACCENT_ROSE,
         "Strict corporate-intelligence prompt: Gemini fills the OrgChart JSON using only supplied evidence. Name/domain backfilled; Sources + LastUpdated stamped."),
    ]
    for x, title, color, desc in stages:
        add_flow_box(s8, x, 1.7, 2.2, 2.85, title, desc, color, title_size=12, desc_size=8.5)

    add_card(s8, 0.8, 4.85, 11.73, 2.0, "Resilience: Every Intermediate Stage Has a Deterministic Fallback", [
        ("Plan fallback", "fallbackQueryPlan builds standard leadership / departments / locations queries (scoped by country + city) plus one query per area of interest."),
        ("Rank fallback", "deterministicRank scores 0.35 base, +0.30 company-name match, +0.05 per interest/leadership term, +0.20 official-domain match; relevant at >= 0.45."),
        ("Only synthesis has no fallback", "The run fails if no usable evidence was collected ('no usable company evidence').")
    ], PRIMARY_BLUE, item_size=10.5)

    # ----------------------------------------------------
    # SLIDE 9: PERSON RESEARCH PIPELINE
    # ----------------------------------------------------
    s9 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s9, BG_LIGHT)
    add_header(s9, "PersonAgent Pipeline: Disambiguation -> Footprint -> Dossier")

    add_card(s9, 0.8, 1.6, 5.7, 5.3, "ResearchPerson Pipeline (Steps 1-8)", [
        ("1. Query construction", "query1 = person name [+ company] [+ location] - LLM-free disambiguation constraints from the form."),
        ("2. Primary search", "SearchCached(query1); failure is fatal and emits an error event."),
        ("3. Targeted scraping", "Up to MaxScrapes pages, prioritizing URLs containing linkedin / wikipedia / about / github (or the first result); scrapes > 80 bytes are appended as source blocks."),
        ("4. Secondary search", "'<query1> speaker interview articles career education' adds career-milestone snippets; URLs go to sources without scraping."),
        ("5. Synthesis", "'Elite talent intelligence researcher' prompt fixes the exact PersonProfile JSON schema; Sources and LastUpdated stamped locally.")
    ], PRIMARY_BLUE, item_size=10.5)

    add_card(s9, 6.8, 1.6, 5.7, 5.3, "Output Shape & UI Wiring", [
        ("Confidence score", "0-1 float rendered as a whole-percent badge via PersonProfile.ScorePercent()."),
        ("Online presence", "platform -> URL map (github, twitter, homepage, ...)."),
        ("person_dossier fragment", "Name/title/company/location header, executive summary, career history, education and skill chips."),
        ("Drill-down", "company_chart embeds per-employee sync forms posting into #company-drilldown-result."),
        ("Sources", "All scraped + secondary-search URLs listed on the chart/dossier for traceability.")
    ], ACCENT_TEAL, item_size=10.5)

    # ----------------------------------------------------
    # SLIDE 10: TOOLING LAYER (internal/tools)
    # ----------------------------------------------------
    s10 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s10, BG_LIGHT)
    add_header(s10, "Tooling Layer: Search, Scrape and Cookie Import")

    add_card(s10, 0.8, 1.6, 3.7, 5.3, "SerperClient (serper.go)", [
        ("Endpoint", "POST https://google.serper.dev/search with X-API-KEY header; 10 s HTTP timeout."),
        ("Retries", "Up to 3 attempts; network errors and HTTP 429/5xx retried with 500 ms x (attempt-1) linear backoff, ctx-aware. Fatal errors return immediately."),
        ("Debuggability", "Failed responses include up to 512 bytes of body; missing key short-circuits with a clear message."),
        ("Normalization", "Organic array mapped into models.SearchResult (title, link, snippet, position, date).")
    ], ACCENT_AMBER)

    add_card(s10, 4.8, 1.6, 3.7, 5.3, "Scraper (scraper.go)", [
        ("Polite & bounded", "Browser-like headers, connection pooling, 15 s timeout, max 5 redirects, guaranteed non-nil cookie jar."),
        ("Safety guards", "HTML/XHTML only; 2 MiB body cap; rune-safe 40 KB text truncation (never splits a UTF-8 rune)."),
        ("Leaf-block extraction", "goquery strips script/style/nav/footer noise, then keeps only content blocks that contain no other content tag - killed 2-10x context duplication."),
        ("Filters", "Blocks under 25 runes dropped; body-text fallback; 3+ newlines collapsed.")
    ], PRIMARY_BLUE)

    add_card(s10, 8.8, 1.6, 3.7, 5.3, "Cookie Import (cookies.go)", [
        ("Source format", "JSON exports from Cookie-Editor / EditThisCookie browser extensions."),
        ("Expiry handling", "Skips empty and expired cookies; understands unix expirationDate and expires strings with an RFC1123 fallback for non-GMT zones."),
        ("Domains", "Strips the leading dot so cookies cover subdomains; installs per-domain under https://<domain>."),
        ("Default", "With no path configured, returns an empty (but valid) jar.")
    ], ACCENT_INDIGO)

    # ----------------------------------------------------
    # SLIDE 11: LLM, CACHE AND SHARED MODELS
    # ----------------------------------------------------
    s11 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s11, BG_LIGHT)
    add_header(s11, "Intelligence, State and the Shared Vocabulary")

    add_card(s11, 0.8, 1.6, 3.7, 5.3, "GeminiClient (llm/gemini.go)", [
        ("Auth via SDK", "GOOGLE_API_KEY for the Gemini API, or Vertex AI (GOOGLE_GENAI_USE_VERTEXAI=true + project + location, ADC). Fail-fast at startup."),
        ("Generation", "System instruction kept separate; temperature 0.2 for factual extraction; clear errors on empty / safety-blocked responses."),
        ("Structured JSON", "Strips markdown fences and surrounding prose, extracts the outermost {...}, and retries once with a stricter instruction."),
        ("Model", "gemini-2.5-flash-lite by default (GEMINI_MODEL).")
    ], ACCENT_INDIGO)

    add_card(s11, 4.8, 1.6, 3.7, 5.3, "MemoryCache (cache/cache.go)", [
        ("Keys", "cache.HashKey = prefix + sha256 hex: search:<hash> (48 h) and scrape:<hash> (7 days)."),
        ("Bounds", "FIFO eviction at CACHE_MAX_ENTRIES (default 2048); lazy expiry check on Get."),
        ("Hygiene", "Background sweep every 10 minutes; idempotent Close() stops the sweeper via a done channel."),
        ("Process-local", "Cleared on restart - a Redis/disk backend would be the natural extension point.")
    ], ACCENT_TEAL)

    add_card(s11, 8.8, 1.6, 3.7, 5.3, "Models (internal/models)", [
        ("agent.go", "5 EventTypes + AgentEvent (with FormattedTime for the UI), SearchResult, ScrapeResult."),
        ("company.go", "OrgChart, Department, Employee, Location - the synthesis JSON schema."),
        ("person.go", "PersonProfile (+ScorePercent), Experience, Education."),
        ("research.go", "ResearchPreferences, CompanyBrief, PlannedQuery/QueryPlan, RankedResult, Evidence."),
        ("Pure data", "No business logic - just the shared vocabulary crossing package boundaries.")
    ], PRIMARY_BLUE)

    # ----------------------------------------------------
    # SLIDE 12: EMBEDDED UI LAYER (internal/web)
    # ----------------------------------------------------
    s12 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s12, BG_LIGHT)
    add_header(s12, "Embedded UI: go:embed Templates and HTMX Fragments")

    add_card(s12, 0.8, 1.6, 5.7, 5.3, "web.go + index.html (the Console Page)", [
        ("go:embed", "templates/*.html compile into the binary - runs from any working directory; the distroless image needs only the executable."),
        ("Console page", "Tailwind + HTMX (with the SSE extension) from CDN; the form hx-posts to /api/research targeting #results-container."),
        ("Mode toggle", "toggleFields() inline script swaps visible fields between company mode (domain, industry, country, city, areas, output, time scope, notes) and person mode (company, location)."),
        ("Auto-escaping", "All interpolation goes through html/template; web_test.go verifies escaping.")
    ], PRIMARY_BLUE, item_size=10.5)

    add_card(s12, 6.8, 1.6, 5.7, 5.3, "fragments.html - Five HTMX Fragments", [
        ("log_entry", "Terminal-style line per AgentEvent: timestamp + color-coded badge (thinking / tool_call / tool_result / complete / error)."),
        ("workflow_console", "Live console wired via sse-connect=/api/events?session_id=..., with sse-swap targets for log, status and result, and sse-close=close."),
        ("company_chart", "Overview, HQ/domain header, leadership grid, department cards, 'Open dossier' sync forms, source links."),
        ("person_dossier", "Confidence-score pill, executive summary, career history, education, skill chips."),
        ("error_box", "Red error banner for failed runs and validation errors.")
    ], ACCENT_TEAL, item_size=10.5)

    # ----------------------------------------------------
    # SLIDE 13: CROSS-CUTTING MECHANISMS
    # ----------------------------------------------------
    s13 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s13, BG_LIGHT)
    add_header(s13, "Cross-Cutting Mechanisms: Events, Concurrency, Resilience")

    add_card(s13, 0.8, 1.6, 5.7, 2.55, "Agent Observability (Event Contract)", [
        ("AgentEvent", "SessionID, Type (5 EventTypes), Step, Content, optional ToolName/ToolInput, Timestamp."),
        ("Separation", "Agents only emit; the SSE handler consumes, persists history and renders - agents stay UI-agnostic and tests pass nil channels.")
    ], PRIMARY_BLUE, item_size=10.5)

    add_card(s13, 6.8, 1.6, 5.7, 2.55, "Concurrency Model", [
        ("One goroutine per research run", "The HTTP handler returns immediately; communication is channel-based (buffered EventCh, Done closed once)."),
        ("Small locked state", "Session history (mutex), SessionHub (RWMutex), MemoryCache (RWMutex); Engine is stateless per run - concurrent sessions are safe.")
    ], ACCENT_TEAL, item_size=10.5)

    add_card(s13, 0.8, 4.35, 5.7, 2.55, "Error Handling & Resilience", [
        ("Retries", "Serper: transient failures with linear backoff. Gemini JSON: one stricter retry."),
        ("Fallbacks", "Plan and rank stages have deterministic fallbacks; scraping skips bad pages silently; SSE drains on Done so no event is lost."),
        ("Shutdown", "In-flight research gets up to 30 s to finish on SIGTERM/SIGINT.")
    ], ACCENT_AMBER, item_size=10.5)

    add_card(s13, 6.8, 4.35, 5.7, 2.55, "Security Posture", [
        ("Output safety", "html/template auto-escaping everywhere; strict CSP; nosniff / DENY / no-referrer headers."),
        ("Input safety", "1 MiB form cap; crypto/rand session IDs; absolute-URL validation before scraping; content-type and size guards on untrusted HTML."),
        ("Runtime", "Distroless non-root image.")
    ], ACCENT_ROSE, item_size=10.5)

    # ----------------------------------------------------
    # SLIDE 14: BUDGETS & GUARDS
    # ----------------------------------------------------
    s14 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s14, BG_LIGHT)
    add_header(s14, "Budgets & Guards: Cost and Runaway Protection by Default")

    add_card(s14, 0.8, 1.6, 5.7, 5.3, "Runtime Budgets", [
        ("MAX_AGENT_STEPS", "10 - agent/tool iteration budget per run"),
        ("MAX_SCRAPES_PER_RUN", "3 - page scrape budget per run"),
        ("MAX_SEARCH_RESULTS", "8 - results per Serper query"),
        ("Query plan cap", "limitQueryPlan keeps at most 6 deduped queries"),
        ("RESEARCH_TIMEOUT_SECONDS", "180 s per-research context timeout"),
        ("WriteTimeout", "ResearchTimeout + 30 s so long runs can still stream"),
        ("Session TTL", "15 minutes, swept every 5 minutes")
    ], PRIMARY_BLUE, is_code=True, item_size=10)

    add_card(s14, 6.8, 1.6, 5.7, 5.3, "Size & Safety Guards", [
        ("Form body", "1 MiB max bytes reader (DoS protection)"),
        ("Scraped HTML body", "2 MiB io.LimitReader before parsing"),
        ("LLM text per page", "40 KB rune-safe truncation"),
        ("Scrape noise floor", "Discard pages/text under 80 bytes / 25 runes"),
        ("LLM temperature", "0.2 - low creativity for factual extraction"),
        ("HTTP timeouts", "ReadHeader 10 s / Read 30 s / Idle 90 s"),
        ("Cache entries", "2048 (CACHE_MAX_ENTRIES), FIFO-evicted")
    ], ACCENT_TEAL, is_code=True, item_size=10)

    # ----------------------------------------------------
    # SLIDE 15: CONFIGURATION & HTTP API
    # ----------------------------------------------------
    s15 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s15, BG_LIGHT)
    add_header(s15, "Configuration (env vars) and the HTTP API Surface")

    add_card(s15, 0.8, 1.6, 5.7, 5.3, "Key Environment Variables (config.go)", [
        ("PORT / GEMINI_MODEL", "8080 / gemini-2.5-flash-lite"),
        ("SERPER_API_KEY", "Required for search (warns at startup if missing)"),
        ("GOOGLE_API_KEY", "Gemini API (AI Studio) key"),
        ("GOOGLE_GENAI_USE_VERTEXAI", "true + GOOGLE_CLOUD_PROJECT + GOOGLE_CLOUD_LOCATION for Vertex AI (ADC)"),
        ("COOKIE_FILE_PATH", "Optional browser cookie export (JSON)"),
        ("CACHE_MAX_ENTRIES", "2048"),
        ("envInt", "Falls back to defaults for unset / invalid / non-positive values")
    ], PRIMARY_BLUE, is_code=True, item_size=10)

    add_card(s15, 6.8, 1.6, 5.7, 5.3, "HTTP API", [
        ("GET /", "Research console (index.html)"),
        ("POST /api/research", "mode (company/person), query, company_name, location + company brief extras; ?sync=1 renders the final fragment inline"),
        ("GET /api/events?session_id=", "SSE: log frames, then result + status pill + close"),
        ("GET /healthz", "Liveness probe -> ok"),
        ("CLI usage", "curl -X POST 'http://localhost:8080/api/research?sync=1' -d mode=company -d query=Acme")
    ], ACCENT_TEAL, is_code=True, item_size=10)

    # ----------------------------------------------------
    # SLIDE 16: TESTING, CI & DEPLOYMENT
    # ----------------------------------------------------
    s16 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s16, BG_LIGHT)
    add_header(s16, "Testing, CI and Deployment")

    add_card(s16, 0.8, 1.6, 5.7, 5.3, "Testing & CI", [
        ("go test ./...", "Dependency-injected unit tests; no network access required."),
        ("internal/agent", "Mock searcher/scraper/LLM: engine limits + defaults, fallback query plan, official-domain ranking, full fallback workflow producing an OrgChart."),
        ("Other packages", "Cache TTL + FIFO bounds, config defaults/overrides, cleanJSONString fence stripping, Serper retry via httptest, scraper extraction, cookie expiry parsing, template escaping."),
        ("CI (.github/workflows/go.yml)", "tidy -> gofmt check -> vet -> build -> test on push to main and PRs; Go version pinned by go.mod.")
    ], PRIMARY_BLUE, item_size=10.5)

    add_card(s16, 6.8, 1.6, 5.7, 5.3, "Run & Deploy", [
        ("Local", "go mod tidy; go build ./...; go test ./...; go run ./cmd/server -> http://localhost:8080"),
        ("Hot reload", "scripts/dev.ps1 (VS Code task 'Go: Watch Server') fingerprints .go/.html files every 750 ms and restarts the server."),
        ("Docker", "Two stages: golang:1.23-alpine builds a static binary (CGO_ENABLED=0, -trimpath, -ldflags '-w -s'); runtime is distroless/static-debian12:nonroot."),
        ("Image contents", "Only the single binary - templates are embedded; EXPOSE 8080; USER nonroot.")
    ], ACCENT_TEAL, item_size=10.5)

    # ----------------------------------------------------
    # SLIDE 17: PROJECT HEALTH & KNOWN ISSUES
    # ----------------------------------------------------
    s17 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s17, BG_LIGHT)
    add_header(s17, "Project Health: Verified Findings and Open Items")

    add_card(s17, 0.8, 1.6, 3.7, 5.3, "Verified in This Review", [
        ("Tests pass", "internal/agent, cache, config, web suites green; gofmt reports no issues."),
        ("Quality highlights", "Interface-driven tools (mockable), deterministic fallbacks for every intermediate LLM stage, bounded memory everywhere, disciplined HTTP hardening."),
        ("Docs", "README (usage) + docs/ARCHITECTURE.md (design, modules, deep dive).")
    ], ACCENT_TEAL, item_size=10.5)

    add_card(s17, 4.8, 1.6, 3.7, 5.3, "Known Issue: Corrupted Go Toolchain (Environment, Not Code)", [
        ("Symptom", "Local compile fails for packages importing crypto/tls (cmd/server, internal/llm, internal/tools)."),
        ("Root cause", "C:/Program Files/Go (go1.27.0) stdlib: crypto/tls/auth.go contains a stray U+000B control byte and a typo'd PSSWithSHA254 identifier."),
        ("Fix", "Reinstall Go or restore the stdlib. Project source is unaffected; CI and Docker use their own toolchain images.")
    ], ACCENT_ROSE, item_size=10.5)

    add_card(s17, 8.8, 1.6, 3.7, 5.3, "Possible Future Improvements", [
        ("External cache", "Redis/disk backend would survive restarts (cache is process-local today)."),
        ("knowledgeGraph", "SerperClient parses the knowledge graph but never uses it - could enrich company evidence."),
        ("Repo hygiene", "websearch.exe at the repo root could be removed or gitignored."),
        ("Docs", "docs/ARCHITECTURE.md is the module-by-module companion to README.md.")
    ], ACCENT_AMBER, item_size=10.5)

    # ----------------------------------------------------
    # SLIDE 18: STRATEGIC TAKEAWAYS (dark)
    # ----------------------------------------------------
    s18 = prs.slides.add_slide(blank_layout)
    set_slide_bg(s18, BG_DARK)
    add_header(s18, "Strategic Takeaways", "EXECUTIVE SUMMARY", is_dark=True)

    takeaways = [
        ("1. Interface-Driven Agents", "internal/agent never imports the tool or LLM packages - three small interfaces make the entire pipeline mockable and testable offline, while cmd/server stays the single composition root.", 0.8, PRIMARY_BLUE),
        ("2. Fallback-First Resilience", "Every intermediate LLM stage (planning, ranking) has a deterministic fallback; search retries with backoff and synthesis JSON gets a retry. Research survives flaky LLM behavior.", 4.8, ACCENT_TEAL),
        ("3. Bounded by Design", "Scrape/search/step budgets, size caps, FIFO cache, session TTLs, SSE overflow drops and graceful shutdown keep long-running, multi-user deployments safe.", 8.8, ACCENT_INDIGO),
    ]
    for title, text, x_pos, col in takeaways:
        add_dark_pillar(s18, x_pos, 1.8, 3.73, 5.0, title, [text], col, title_size=14, body_size=12)

    # Written next to this script, so the deck generator works from any checkout.
    output_path = str(Path(__file__).resolve().parent / "WebSearch_GO_Project_Guide.pptx")
    prs.save(output_path)
    print(f"Project guide presentation saved successfully to {output_path}")
    print(f"Slides: {len(prs.slides)}")

if __name__ == "__main__":
    create_project_guide_deck()