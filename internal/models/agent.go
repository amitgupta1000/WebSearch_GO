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

// ScrapeResult represents the extracted text content of a webpage.
type ScrapeResult struct {
	URL          string    `json:"url"`
	CanonicalURL string    `json:"canonical_url,omitempty"`
	Title        string    `json:"title"`
	Content      string    `json:"content"`
	Headings     []string  `json:"headings,omitempty"`
	JSONLD       []string  `json:"json_ld,omitempty"`
	Images       []Image   `json:"images,omitempty"`
	WordCount    int       `json:"word_count"`
	ContentType  string    `json:"content_type,omitempty"`
	Extractor    string    `json:"extractor,omitempty"`
	QualityScore float64   `json:"quality_score"`
	ByteSize     int       `json:"byte_size"`
	FetchedAt    time.Time `json:"fetched_at"`
}

// Image records image metadata discovered in a webpage without downloading the asset.
type Image struct {
	URL     string `json:"url"`
	Alt     string `json:"alt,omitempty"`
	Title   string `json:"title,omitempty"`
	Caption string `json:"caption,omitempty"`
}
