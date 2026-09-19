package models

import "time"

// EventType defines the phase of the ReAct agent cycle.
type EventType string

const (
	EventThinking    EventType = "thinking"
	EventToolCall    EventType = "tool_call"
	EventToolResult  EventType = "tool_result"
	EventDisambiguate EventType = "disambiguate"
	EventComplete    EventType = "complete"
	EventError       EventType = "error"
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

// ResearchRequest holds the input parameters for a research task.
type ResearchRequest struct {
	Mode        string `json:"mode"` // "company" or "person"
	Query       string `json:"query"`
	CompanyName string `json:"company_name,omitempty"` // for person disambiguation
	Location    string `json:"location,omitempty"`     // for person disambiguation
	MaxSteps    int    `json:"max_steps,omitempty"`
}

// SearchResult represents a normalized web search hit from Serper API.
type SearchResult struct {
	Title       string `json:"title"`
	Link        string `json:"link"`
	Snippet     string `json:"snippet"`
	Position    int    `json:"position"`
	Date        string `json:"date,omitempty"`
}

// ScrapeResult represents the extracted text content of a webpage.
type ScrapeResult struct {
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	ByteSize    int       `json:"byte_size"`
	FetchedAt   time.Time `json:"fetched_at"`
}
