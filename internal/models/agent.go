package models

import "time"

// EventType defines the phase of the ReAct agent cycle.
type EventType string

const (
	EventThinking   EventType = "thinking"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventComplete   EventType = "complete"
	EventError      EventType = "error"
	// EventTiming carries a per-phase duration summary for a finished run, so
	// where the time actually went is visible in the console.
	EventTiming EventType = "timing"
)

// AgentEvent represents a single step or output stream emitted during research.
type AgentEvent struct {
	SessionID string    `json:"session_id"`
	Type      EventType `json:"type"`
	Step      int       `json:"step"`
	Content   string    `json:"content"`
	ToolName  string    `json:"tool_name,omitempty"`
	ToolInput string    `json:"tool_input,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// FormattedTime returns a 24-hour HH:MM:SS string for UI display.
func (e AgentEvent) FormattedTime() string {
	if e.Timestamp.IsZero() {
		return time.Now().Format("15:04:05")
	}
	return e.Timestamp.Format("15:04:05")
}

// SearchResult represents a normalized web search hit from Serper API.
type SearchResult struct {
	Title    string `json:"title"`
	Link     string `json:"link"`
	Snippet  string `json:"snippet"`
	Position int    `json:"position"`
	Date     string `json:"date,omitempty"`
}

// SearchTrace captures the raw search evidence a run collected: every query issued, and the
// hits it returned split into what the identity/entity gates kept and what they excluded. It
// is attached by the agent, never by the model, and it is the data behind the dossier's
// Search results tab and the run's -searches.json artifact — the same material the cache
// persists, made downloadable per run.
type SearchTrace struct {
	Queries []QueryTrace `json:"queries"`
}

// QueryTrace is one search's collected evidence: the query, what it was for, and its hits
// before any cross-query deduplication, so the download shows what each search returned.
type QueryTrace struct {
	Query    string         `json:"query"`
	Purpose  string         `json:"purpose,omitempty"`
	Kept     []SearchResult `json:"kept,omitempty"`
	Excluded []SearchResult `json:"excluded,omitempty"`
}

// KeptCount sums the hits the gates admitted across every query in the trace.
func (t *SearchTrace) KeptCount() int {
	if t == nil {
		return 0
	}
	kept := 0
	for _, query := range t.Queries {
		kept += len(query.Kept)
	}
	return kept
}

// ExcludedCount sums the hits the gates excluded across every query in the trace.
func (t *SearchTrace) ExcludedCount() int {
	if t == nil {
		return 0
	}
	excluded := 0
	for _, query := range t.Queries {
		excluded += len(query.Excluded)
	}
	return excluded
}

// ScrapeResult represents the extracted text content of a webpage.
type ScrapeResult struct {
	URL               string    `json:"url"`
	CanonicalURL      string    `json:"canonical_url,omitempty"`
	Title             string    `json:"title"`
	Content           string    `json:"content"`
	Headings          []string  `json:"headings,omitempty"`
	JSONLD            []string  `json:"json_ld,omitempty"`
	Images            []Image   `json:"images,omitempty"`
	WordCount         int       `json:"word_count"`
	ContentType       string    `json:"content_type,omitempty"`
	Extractor         string    `json:"extractor,omitempty"`
	QualityScore      float64   `json:"quality_score"`
	ByteSize          int       `json:"byte_size"`
	FetchedAt         time.Time `json:"fetched_at"`
	Blocked           bool      `json:"blocked,omitempty"`
	BlockReason       string    `json:"block_reason,omitempty"`
	FallbackAttempted bool      `json:"fallback_attempted,omitempty"`
	FallbackStatus    string    `json:"fallback_status,omitempty"`
	FallbackReason    string    `json:"fallback_reason,omitempty"`
	FallbackError     string    `json:"fallback_error,omitempty"`
	// ContentTruncated reports that the response body exceeded the scraper's
	// size cap, so the extracted text came from an incomplete document.
	ContentTruncated bool `json:"content_truncated,omitempty"`
	// ETag and LastModified are the response validators the HTTP path recorded.
	// They let a later fetch ask "has this changed?" (If-None-Match /
	// If-Modified-Since) and refresh the cached copy for one round trip when the
	// answer is "no". Empty means the server sent no validator, so the entry can
	// only be refreshed by a full download.
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	// NotModified reports a 304 response: the caller's cached copy is still
	// current, and this result carries no content.
	NotModified bool `json:"not_modified,omitempty"`
}

// Image records webpage image metadata and optional OCR text.
type Image struct {
	URL           string `json:"url"`
	Alt           string `json:"alt,omitempty"`
	Title         string `json:"title,omitempty"`
	Caption       string `json:"caption,omitempty"`
	ExtractedText string `json:"extracted_text,omitempty"`
}
