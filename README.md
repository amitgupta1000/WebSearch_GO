# WebSearch_GO

Agentic web research server in Go. It runs autonomous research flows over two
tracks and synthesizes structured results with Gemini:

- **Company mode** — discovers leadership pages, builds an org chart with
  departments and locations.
- **Person mode** — disambiguates a person via optional company/location
  constraints and compiles a professional dossier.

The HTTP layer serves an HTMX UI from templates embedded in the binary
(`internal/web`); the agents stream-step through search (Serper API) → scrape →
LLM synthesis, with TTL caching and bounded memory.

## Prerequisites

- Go 1.23+
- A Serper API key ([serper.dev](https://serper.dev))
- Gemini credentials, one of:
  - `GOOGLE_API_KEY` (Gemini API / AI Studio key), **or**
  - Vertex AI with Application Default Credentials:
    `GOOGLE_GENAI_USE_VERTEXAI=true`, `GOOGLE_CLOUD_PROJECT`, `GOOGLE_CLOUD_LOCATION`

## Quickstart

```bash
go mod tidy    # generates go.sum — required before building or using Docker
go build ./...
go test ./...
go run ./cmd/server
# open http://localhost:8080
```

For Windows development, run the auto-reloading server instead:

```powershell
.\scripts\dev.ps1
```

It watches Go and embedded template files under `cmd` and `internal`, then
restarts `go run ./cmd/server` after changes. The same watcher is available in
VS Code as the `Go: Watch Server` task.

## Environment variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `PORT` | `8080` | HTTP listen port |
| `GEMINI_MODEL` | `gemini-2.5-flash-lite` | Gemini model name |
| `SERPER_API_KEY` | — (required for search) | Serper.dev API key |
| `GOOGLE_API_KEY` | — | Gemini API key (AI Studio) |
| `GOOGLE_GENAI_USE_VERTEXAI` | unset | Set to `true` to use Vertex AI backend |
| `GOOGLE_CLOUD_PROJECT` / `GOOGLE_CLOUD_LOCATION` | — | Vertex AI project & region |
| `COOKIE_FILE_PATH` | — | Optional JSON cookie export (Cookie-Editor / EditThisCookie format) |
| `CACHE_MAX_ENTRIES` | `2048` | Max entries in the in-memory TTL cache |
| `RESEARCH_TIMEOUT_SECONDS` | `180` | Per-request research budget |
| `MAX_SCRAPES_PER_RUN` | `3` | Max web pages scraped per research session |
| `MAX_SEARCH_RESULTS` | `8` | Max search results fetched per search query |
| `MAX_AGENT_STEPS` | `10` | Max agent/tool iterations per research session |

## Endpoints

| Method | Path | Description |
| :--- | :--- | :--- |
| `GET` | `/` | Research console (HTMX UI) |
| `POST` | `/api/research` | Form fields: `mode` (`company`/`person`), `query`, `company_name`, `location`, plus optional company scope fields `company_country`, `company_city`, `company_domain`, `company_industry`, `areas_of_interest`, `desired_output`, `time_scope`, and `research_notes`. Company research performs up to 3 QC reviews. Returns the live workflow console. |
| `POST` | `/api/question` | Form fields: `session_id`, `question`. Answers a question from retained evidence or runs a fresh focused search when needed. |
| `GET` | `/api/events` | Server-Sent Events (SSE) stream for live step logs & final synthesis. |
| `GET` | `/healthz` | Liveness probe |

## Docker

```bash
go mod tidy   # ensure go.sum exists first
docker build -t websearch-go .
docker run --rm -p 8080:8080 -e SERPER_API_KEY=... -e GOOGLE_API_KEY=... websearch-go
```

The runtime image is distroless (`nonroot`); templates are embedded so the
image contains only the static binary. See `DESIGN.md` for the system design.