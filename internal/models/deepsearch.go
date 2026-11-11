package models

import "time"

// Focus preferences for a deep search run. Recency narrows the decomposition
// and biases candidate selection toward fresh results; depth widens it for
// comprehensive coverage.
const (
	FocusRecency = "recency"
	FocusDepth   = "depth"
)

// DeepSearchQuery defines one open-ended deep research investigation.
type DeepSearchQuery struct {
	// Question is the free-form research question the run answers.
	Question string `json:"question"`
	// Focus is "recency" or "depth" (empty means depth).
	Focus string `json:"focus,omitempty"`
	// MaxRounds caps how many compose-search-extract-review rounds may run.
	MaxRounds int `json:"max_rounds,omitempty"`
	// MaxSearchResults caps hits returned per query.
	MaxSearchResults int `json:"max_search_results,omitempty"`
	// MaxScrapes caps total page fetches across the whole run.
	MaxScrapes int `json:"max_scrapes,omitempty"`
}

// DeepSearchPlan is the structured LLM output that decomposes the question
// into sub-questions. The planner designs the decomposition; it never answers
// the question, and nothing it invents enters the evidence corpus.
type DeepSearchPlan struct {
	Understanding string   `json:"understanding"`
	SubQuestions  []string `json:"sub_questions"`
}

// DeepSubQuestionQueries holds the custom search queries composed for one
// sub-question. Composition rephrases the sub-question into engine-friendly
// queries; it never answers anything.
type DeepSubQuestionQueries struct {
	SubQuestion string   `json:"sub_question"`
	Queries     []string `json:"queries"`
}

// DeepSearchQueryPlan maps every sub-question to its composed queries.
type DeepSearchQueryPlan struct {
	Entries []DeepSubQuestionQueries `json:"entries"`
}

// DeepSearchCandidate is one search hit offered to the relevance judge. The
// judge sees only the title, snippet, and date — never page content — and
// classifies; it never adds facts.
type DeepSearchCandidate struct {
	ID          string `json:"id"`
	SubQuestion string `json:"sub_question,omitempty"`
	Title       string `json:"title"`
	Snippet     string `json:"snippet"`
	Date        string `json:"date,omitempty"`
	URL         string `json:"url"`
}

// DeepSearchSelection is the relevance judge's verdict over the candidates.
type DeepSearchSelection struct {
	Kept []DeepSelectedHit `json:"kept"`
}

// DeepSelectedHit keeps one candidate with its relevance verdict.
type DeepSelectedHit struct {
	ID     string  `json:"id"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason,omitempty"`
}

// DeepSearchExtraction is the extractor's output over scraped page text.
type DeepSearchExtraction struct {
	Pairs []DeepSearchQAPair `json:"pairs"`
}

// DeepSearchQAPair is one extractive answer grounded in a single scraped page.
// It is the unit the final assembly consumes; nothing outside these pairs
// reaches the report.
type DeepSearchQAPair struct {
	SubQuestion string `json:"sub_question,omitempty"`
	Question    string `json:"question"`
	Answer      string `json:"answer"`
	Quote       string `json:"quote,omitempty"`
	URL         string `json:"url"`
	Round       int    `json:"round,omitempty"`
}

// DeepSearchReview is the reviewer's coverage verdict over the QA pairs so
// far, plus the rerun queries that should close the gaps it names.
type DeepSearchReview struct {
	Complete     bool     `json:"complete"`
	Missing      []string `json:"missing,omitempty"`
	RerunQueries []string `json:"rerun_queries,omitempty"`
}

// DeepSearchSource is one page the run actually read, kept as evidence.
type DeepSearchSource struct {
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Host        string    `json:"host,omitempty"`
	SubQuestion string    `json:"sub_question,omitempty"`
	Snippet     string    `json:"snippet,omitempty"`
	Content     string    `json:"content,omitempty"`
	Round       int       `json:"round"`
	FetchedAt   time.Time `json:"fetched_at"`
}

// DeepSearchFinding ties one synthesized claim to the sources that support it.
type DeepSearchFinding struct {
	Statement string   `json:"statement"`
	URLs      []string `json:"urls"`
}

// DeepSearchSynthesis is the structured LLM output for the final report. It is
// assembled from the QA pairs alone; the model's own knowledge is not an
// admissible source.
type DeepSearchSynthesis struct {
	Title         string              `json:"title"`
	Summary       string              `json:"summary"`
	Report        string              `json:"report"`
	Findings      []DeepSearchFinding `json:"findings,omitempty"`
	OpenQuestions []string            `json:"open_questions,omitempty"`
	Citations     []DeepSearchFinding `json:"citations,omitempty"`
}

// DeepSearchRound is the auditable trace of one research round.
type DeepSearchRound struct {
	Round      int      `json:"round"`
	Queries    []string `json:"queries"`
	Composed   int      `json:"composed,omitempty"`
	Candidates int      `json:"candidates,omitempty"`
	Selected   int      `json:"selected,omitempty"`
	SearchHits int      `json:"search_hits,omitempty"`
	Scraped    int      `json:"scraped,omitempty"`
	QAPairs    int      `json:"qa_pairs,omitempty"`
	Complete   bool     `json:"complete,omitempty"`
	Missing    []string `json:"missing,omitempty"`
}

// DeepSearchReport is the auditable result of a Deep Search Agent run.
type DeepSearchReport struct {
	ResearchID   string              `json:"research_id"`
	Question     string              `json:"question"`
	Focus        string              `json:"focus,omitempty"`
	AsOf         time.Time           `json:"as_of"`
	Plan         DeepSearchPlan      `json:"plan"`
	QueryPlan    DeepSearchQueryPlan `json:"query_plan,omitempty"`
	Rounds       []DeepSearchRound   `json:"rounds,omitempty"`
	Sources      []DeepSearchSource  `json:"sources,omitempty"`
	QAPairs      []DeepSearchQAPair  `json:"qa_pairs,omitempty"`
	Synthesis    DeepSearchSynthesis `json:"synthesis"`
	QueriesUsed  int                 `json:"queries_used"`
	SearchHits   int                 `json:"search_hits"`
	PagesScraped int                 `json:"pages_scraped"`
	RoundsRun    int                 `json:"rounds_run"`
	Warnings     []string            `json:"warnings,omitempty"`
	StartedAt    time.Time           `json:"started_at"`
	CompletedAt  time.Time           `json:"completed_at"`
	Exports      []ExportArtifact    `json:"exports,omitempty"`
}

// CitedURLs returns every URL the synthesis claims as support.
func (r *DeepSearchReport) CitedURLs() []string {
	urls := make([]string, 0)
	for _, finding := range r.Synthesis.Findings {
		urls = append(urls, finding.URLs...)
	}
	for _, citation := range r.Synthesis.Citations {
		urls = append(urls, citation.URLs...)
	}
	return urls
}
