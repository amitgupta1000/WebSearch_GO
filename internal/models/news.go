package models

import "time"

// NewsQuery defines one bounded current-news investigation.
type NewsQuery struct {
	Query            string   `json:"query"`
	MaxArticles      int      `json:"max_articles,omitempty"`
	MaxSearchResults int      `json:"max_search_results,omitempty"`
	AllowedHosts     []string `json:"allowed_hosts,omitempty"`
	IncludeUndated   bool     `json:"include_undated,omitempty"`
	ForceFreshSearch bool     `json:"force_fresh_search,omitempty"`
}

// NewsChunk is a bounded, source-labelled unit supplied to synthesis.
type NewsChunk struct {
	ID          string    `json:"id"`
	ArticleURL  string    `json:"article_url"`
	Source      string    `json:"source"`
	Title       string    `json:"title"`
	PublishedAt time.Time `json:"published_at,omitempty"`
	Position    int       `json:"position"`
	Text        string    `json:"text"`
	Labels      []string  `json:"labels,omitempty"`
}

// NewsArticle is one whitelist-approved article considered for the report.
type NewsArticle struct {
	URL          string      `json:"url"`
	CanonicalURL string      `json:"canonical_url,omitempty"`
	Source       string      `json:"source"`
	Title        string      `json:"title"`
	Snippet      string      `json:"snippet,omitempty"`
	PublishedAt  time.Time   `json:"published_at,omitempty"`
	UpdatedAt    time.Time   `json:"updated_at,omitempty"`
	RetrievedAt  time.Time   `json:"retrieved_at"`
	Fresh        bool        `json:"fresh"`
	Status       string      `json:"status"`
	Reason       string      `json:"reason,omitempty"`
	Content      string      `json:"content,omitempty"`
	Chunks       []NewsChunk `json:"chunks,omitempty"`
}

// NewsCitation ties a synthesized claim to supplied article evidence.
type NewsCitation struct {
	Claim    string   `json:"claim"`
	URLs     []string `json:"urls"`
	ChunkIDs []string `json:"chunk_ids,omitempty"`
}

// NewsArticleSentiment is the structured sentiment readout for one source.
type NewsArticleSentiment struct {
	URL        string   `json:"url"`
	Label      string   `json:"label"`
	Score      float64  `json:"score"`
	Confidence float64  `json:"confidence"`
	Rationale  string   `json:"rationale"`
	ChunkIDs   []string `json:"chunk_ids,omitempty"`
}

// NewsConflict records competing claims and how the editor treated them.
type NewsConflict struct {
	Claim       string   `json:"claim"`
	Versions    []string `json:"versions"`
	URLs        []string `json:"urls"`
	Resolution  string   `json:"resolution"`
	Explanation string   `json:"explanation"`
	Confidence  float64  `json:"confidence"`
}

// NewsSynthesis is the structured LLM output for a current-news report.
type NewsSynthesis struct {
	Headline          string                 `json:"headline"`
	Article           string                 `json:"article"`
	Summary           string                 `json:"summary"`
	KeyDevelopments   []string               `json:"key_developments,omitempty"`
	Sentiment         NewsArticleSentiment   `json:"sentiment"`
	ArticleSentiments []NewsArticleSentiment `json:"article_sentiments,omitempty"`
	Conflicts         []NewsConflict         `json:"conflicts,omitempty"`
	Citations         []NewsCitation         `json:"citations,omitempty"`
	Uncertainty       []string               `json:"uncertainty,omitempty"`
}

// NewsReport is the auditable result of a News Agent run.
type NewsReport struct {
	ResearchID       string        `json:"research_id"`
	Query            string        `json:"query"`
	AsOf             time.Time     `json:"as_of"`
	WindowStart      time.Time     `json:"window_start"`
	Articles         []NewsArticle `json:"articles,omitempty"`
	RejectedArticles []NewsArticle `json:"rejected_articles,omitempty"`
	Synthesis        NewsSynthesis `json:"synthesis"`
	QueriesUsed      int           `json:"queries_used"`
	SearchHits       int           `json:"search_hits"`
	ArticlesSelected int           `json:"articles_selected"`
	ArticlesScraped  int           `json:"articles_scraped"`
	ChunksCollected  int           `json:"chunks_collected"`
	Warnings         []string      `json:"warnings,omitempty"`
	StartedAt        time.Time     `json:"started_at"`
	CompletedAt      time.Time     `json:"completed_at"`
}
