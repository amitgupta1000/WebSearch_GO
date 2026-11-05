package agent

import (
	"sync"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// Session tracks an in-flight or recently completed research workflow.
type Session struct {
	ID             string
	Mode           string
	Query          string
	EventCh        chan models.AgentEvent
	Events         []models.AgentEvent
	Result         any
	Err            error
	CompanyContext *models.CompanyResearchContext
	Done           chan struct{}
	CreatedAt      time.Time
	finalSent      bool
	mu             sync.Mutex
	doneOnce       sync.Once
}

// AddHistory appends an event to the session history.
func (s *Session) AddHistory(event models.AgentEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Events = append(s.Events, event)
}

// GetHistory returns a copy of all emitted events so far.
func (s *Session) GetHistory() []models.AgentEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make([]models.AgentEvent, len(s.Events))
	copy(copied, s.Events)
	return copied
}

// Complete marks the session as finished and stores the final result or error.
func (s *Session) Complete(result any, err error) {
	s.mu.Lock()
	s.Result = result
	s.Err = err
	s.mu.Unlock()

	s.doneOnce.Do(func() {
		close(s.Done)
	})
}

// SetCompanyContext stores the completed company evidence used by QC and Q&A.
func (s *Session) SetCompanyContext(context *models.CompanyResearchContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CompanyContext = context
}

// GetCompanyContext returns the retained company research context.
func (s *Session) GetCompanyContext() *models.CompanyResearchContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.CompanyContext
}

// GetResult returns the completed session result and error.
func (s *Session) GetResult() (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Result, s.Err
}

// ClaimFinalEvent allows only one SSE connection to render the terminal result.
func (s *Session) ClaimFinalEvent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finalSent {
		return false
	}
	s.finalSent = true
	return true
}

// SessionHub provides thread-safe lifecycle tracking for active SSE research sessions.
type SessionHub struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionHub creates a new SessionHub with automatic background cleanup.
func NewSessionHub() *SessionHub {
	hub := &SessionHub{
		sessions: make(map[string]*Session),
	}
	go hub.startCleanup(15 * time.Minute)
	return hub
}

// Create initializes and stores a new research session.
func (h *SessionHub) Create(id, mode, query string) *Session {
	h.mu.Lock()
	defer h.mu.Unlock()

	s := &Session{
		ID:        id,
		Mode:      mode,
		Query:     query,
		EventCh:   make(chan models.AgentEvent, 1024),
		Done:      make(chan struct{}),
		CreatedAt: time.Now(),
	}
	h.sessions[id] = s
	return s
}

// Get retrieves a session by ID.
func (h *SessionHub) Get(id string) (*Session, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s, ok := h.sessions[id]
	return s, ok
}

func (h *SessionHub) startCleanup(ttl time.Duration) {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		h.mu.Lock()
		now := time.Now()
		for id, s := range h.sessions {
			if now.Sub(s.CreatedAt) > ttl {
				delete(h.sessions, id)
			}
		}
		h.mu.Unlock()
	}
}
