package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

const (
	newsWindow            = 24 * time.Hour
	newsMaxQueries        = 8
	newsSearchConcurrency = 3
	newsMaxArticles       = 30
	newsMaxSearchResults  = 10
	newsChunkSize         = 2400
	newsChunkOverlap      = 250
	newsMaxSynthesisChars = 110000
)

var defaultNewsHosts = []string{
	"reuters.com", "apnews.com", "bbc.com", "bbc.co.uk", "cnn.com",
	"theguardian.com", "nytimes.com", "washingtonpost.com", "wsj.com",
	"bloomberg.com", "ft.com", "cnbc.com", "npr.org", "pbs.org",
	"politico.com", "aljazeera.com", "france24.com", "dw.com",
	"euronews.com", "thehindu.com", "indianexpress.com",
	"timesofindia.indiatimes.com", "scmp.com", "channelnewsasia.com",
	"abc.net.au", "nikkei.com", "haaretz.com", "timesofisrael.com",
	"foreignpolicy.com", "foreignaffairs.com", "defensenews.com",
	"news.un.org", "state.gov",
}

// NewsAgent owns the whitelist-first, freshness-bounded current-news workflow.
type NewsAgent struct {
	engine       *Engine
	allowedHosts []string
	now          func() time.Time
}

func NewNewsAgent(engine *Engine) *NewsAgent {
	return &NewsAgent{engine: engine, allowedHosts: configuredNewsHosts(), now: time.Now}
}

func configuredNewsHosts() []string {
	raw := strings.TrimSpace(os.Getenv("NEWS_ALLOWED_HOSTS"))
	if raw == "" {
		return append([]string(nil), defaultNewsHosts...)
	}
	hosts := make([]string, 0)
	for _, host := range strings.Split(raw, ",") {
		host = normalizeNewsHost(host)
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return append([]string(nil), defaultNewsHosts...)
	}
	return uniqueStrings(hosts)
}

func (a *NewsAgent) Research(ctx context.Context, researchID string, query models.NewsQuery, eventCh chan<- models.AgentEvent) (*models.NewsReport, error) {
	query.Query = strings.TrimSpace(query.Query)
	if query.Query == "" {
		return nil, fmt.Errorf("news query is required")
	}
	if query.MaxArticles <= 0 || query.MaxArticles > newsMaxArticles {
		query.MaxArticles = newsMaxArticles
	}
	if query.MaxSearchResults <= 0 || query.MaxSearchResults > newsMaxSearchResults {
		query.MaxSearchResults = newsMaxSearchResults
	}
	hosts := a.allowedHosts
	if len(query.AllowedHosts) > 0 {
		requested := normalizedNewsHosts(query.AllowedHosts)
		hosts = make([]string, 0, len(requested))
		for _, host := range requested {
			if newsHostAllowed(host, a.allowedHosts) {
				hosts = append(hosts, host)
			}
		}
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("news whitelist is empty")
	}

	now := a.now().UTC()
	report := &models.NewsReport{
		ResearchID: researchID, Query: query.Query, AsOf: now,
		WindowStart: now.Add(-newsWindow), StartedAt: now,
	}
	queries := newsQueries(query.Query, hosts)
	var candidates []newsCandidate
	seen := make(map[string]bool)
	searches := make([]newsSearchOutcome, len(queries))
	searchLimit := newsSearchConcurrency
	if searchLimit > len(queries) {
		searchLimit = len(queries)
	}
	searchSemaphore := make(chan struct{}, searchLimit)
	var searchWaitGroup sync.WaitGroup
	for index, searchQuery := range queries {
		searchWaitGroup.Add(1)
		go func(index int, searchQuery string) {
			defer searchWaitGroup.Done()
			searchSemaphore <- struct{}{}
			defer func() { <-searchSemaphore }()
			if err := ctx.Err(); err != nil {
				searches[index].err = err
				return
			}
			emitAgentEvent(eventCh, models.EventToolCall, "Searching recent news", "search", searchQuery)
			if query.ForceFreshSearch {
				searches[index].hits, searches[index].err = a.engine.SearchFresh(ctx, searchQuery, query.MaxSearchResults)
			} else {
				searches[index].hits, searches[index].err = a.engine.SearchCached(ctx, searchQuery, query.MaxSearchResults)
			}
		}(index, searchQuery)
	}
	searchWaitGroup.Wait()
	for index := range queries {
		if searches[index].err == context.Canceled || searches[index].err == context.DeadlineExceeded {
			return nil, searches[index].err
		}
		hits, err := searches[index].hits, searches[index].err
		report.QueriesUsed++
		report.SearchHits += len(hits)
		if err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("news search failed: %v", err))
			continue
		}
		for _, hit := range hits {
			host := normalizeNewsHost(hostname(hit.Link))
			if !newsHostAllowed(host, hosts) {
				continue
			}
			key := canonicalNewsURL(hit.Link)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			candidates = append(candidates, newsCandidate{hit: hit, canonical: key, host: host, published: parseNewsTime(hit.Date, now)})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].published.IsZero() != candidates[j].published.IsZero() {
			return !candidates[i].published.IsZero()
		}
		return candidates[i].published.After(candidates[j].published)
	})
	if len(candidates) > newsMaxArticles*2 {
		candidates = candidates[:newsMaxArticles*2]
	}

	urls := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		urls = append(urls, candidate.hit.Link)
	}
	emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Scraping %d whitelist-approved news candidate(s)", len(urls)), "", "")
	outcomes := a.engine.ScrapeCachedBatch(ctx, urls)
	for index, candidate := range candidates {
		article := models.NewsArticle{
			URL: candidate.hit.Link, CanonicalURL: candidate.canonical, Source: candidate.host,
			Title: candidate.hit.Title, Snippet: candidate.hit.Snippet, RetrievedAt: now,
			PublishedAt: candidate.published,
		}
		if index >= len(outcomes) || outcomes[index].Err != nil || outcomes[index].Result == nil {
			article.Status, article.Reason = "rejected", "article could not be scraped"
			report.RejectedArticles = append(report.RejectedArticles, article)
			continue
		}
		scrape := outcomes[index].Result
		article.Title = firstNewsNonEmpty(scrape.Title, article.Title)
		if article.CanonicalURL == "" {
			article.CanonicalURL = canonicalNewsURL(scrape.CanonicalURL)
		}
		published, updated := newsDatesFromJSONLD(scrape.JSONLD)
		if published.After(article.PublishedAt) {
			article.PublishedAt = published
		} else if article.PublishedAt.IsZero() {
			article.PublishedAt = published
		}
		article.UpdatedAt = updated
		freshnessAt := article.PublishedAt
		if article.UpdatedAt.After(freshnessAt) {
			freshnessAt = article.UpdatedAt
		}
		if freshnessAt.IsZero() {
			article.Status, article.Reason = "undated", "no publication timestamp was found"
			if !query.IncludeUndated {
				report.RejectedArticles = append(report.RejectedArticles, article)
				continue
			}
		} else if freshnessAt.Before(report.WindowStart) || freshnessAt.After(now.Add(5*time.Minute)) {
			article.Status, article.Reason = "stale", "publication time is outside the 24-hour window"
			report.RejectedArticles = append(report.RejectedArticles, article)
			continue
		}
		article.Content = strings.TrimSpace(scrape.Content)
		if article.Content == "" {
			article.Status, article.Reason = "rejected", "scraped article body is empty"
			report.RejectedArticles = append(report.RejectedArticles, article)
			continue
		}
		article.Fresh, article.Status = true, "selected"
		article.Chunks = newsChunks(article, now)
		report.Articles = append(report.Articles, article)
		if len(report.Articles) >= query.MaxArticles {
			break
		}
	}
	report.ArticlesSelected = len(report.Articles)
	for _, article := range report.Articles {
		report.ArticlesScraped++
		report.ChunksCollected += len(article.Chunks)
	}
	if len(report.Articles) == 0 {
		report.Warnings = append(report.Warnings, "No whitelist-approved article with a verifiable publication time inside the last 24 hours was found.")
	} else {
		emitAgentEvent(eventCh, models.EventThinking, fmt.Sprintf("Synthesizing %d current article(s)", len(report.Articles)), "", "")
		if err := a.synthesize(ctx, report, eventCh); err != nil {
			return nil, err
		}
	}
	report.CompletedAt = a.now().UTC()
	emitAgentEvent(eventCh, models.EventComplete, fmt.Sprintf("News report complete: %d article(s), %d chunk(s)", report.ArticlesSelected, report.ChunksCollected), "", "")
	return report, nil
}

type newsCandidate struct {
	hit       models.SearchResult
	canonical string
	host      string
	published time.Time
}

type newsSearchOutcome struct {
	hits []models.SearchResult
	err  error
}

func (a *NewsAgent) synthesize(ctx context.Context, report *models.NewsReport, eventCh chan<- models.AgentEvent) error {
	contextParts := make([]string, 0)
	for _, article := range report.Articles {
		for _, chunk := range article.Chunks {
			contextParts = append(contextParts, fmt.Sprintf("[%s] %s | %s | published=%s | chunk=%s\n%s",
				article.Source, article.Title, article.URL, article.PublishedAt.Format(time.RFC3339), chunk.ID, chunk.Text))
		}
	}
	contextData := strings.Join(contextParts, "\n\n")
	if len(contextData) > newsMaxSynthesisChars {
		contextData = contextData[:newsMaxSynthesisChars]
		report.Warnings = append(report.Warnings, "Synthesis context was bounded; not all collected chunk text was sent to the model.")
	}
	system := `You are a careful current-news editor. Use only the supplied article chunks.
Return exactly one JSON object with these keys:
{
  "headline": "non-empty headline",
  "article": "non-empty synthesized news article",
  "summary": "non-empty concise summary",
  "key_developments": ["..."],
  "sentiment": {"label": "positive|negative|neutral|mixed", "score": 0, "confidence": 0, "rationale": "..."},
  "article_sentiments": [{"url": "supplied article URL", "label": "positive|negative|neutral|mixed", "score": 0, "confidence": 0, "rationale": "...", "chunk_ids": ["supplied chunk ID"]}],
  "conflicts": [{"claim": "...", "versions": ["..."], "urls": ["supplied article URL"], "resolution": "updated|unresolved|clarified", "explanation": "...", "confidence": 0}],
  "citations": [{"claim": "...", "urls": [" supplied article URL "], "chunk_ids": [" supplied chunk ID "]}],
  "uncertainty": []
}
The headline, article, and summary are required and must never be empty. Write an original
article and concise summary grounded in the supplied chunks.
Every article_sentiments entry must include a supplied URL, a valid label, score, confidence,
and rationale; omit that optional array rather than emitting an incomplete entry. Likewise,
omit incomplete optional conflicts, citations, or developments.
For conflicts separated by time, treat the later report as the current status while preserving the earlier report as historical context.
For contemporaneous conflicts, present both attributed versions and mark the conflict unresolved.
Never invent facts, dates, sources, sentiment evidence, or citations. Every citation URL must be supplied.
Sentiment is an evidence-grounded interpretation of the reported developments, not a claim that the reporting itself is true.
Sentiment labels must be positive, negative, neutral, or mixed; scores must be between -1 and 1; confidence must be between 0 and 1.`
	var synthesis models.NewsSynthesis
	if err := a.engine.GenerateStructured(ctx, system, "Query: "+report.Query+"\nAs of: "+report.AsOf.Format(time.RFC3339)+"\n\nArticle chunks:\n"+contextData, &synthesis); err != nil {
		return fmt.Errorf("news synthesis failed: %w", err)
	}
	if err := validateNewsSynthesis(synthesis, report.Articles); err != nil {
		retrySystem := system + "\nYour previous JSON failed validation: " + err.Error() +
			"\nReturn a corrected JSON object. Do not emit partial objects in optional arrays."
		var corrected models.NewsSynthesis
		if retryErr := a.engine.GenerateStructured(ctx, retrySystem, "Query: "+report.Query+"\nAs of: "+report.AsOf.Format(time.RFC3339)+"\n\nArticle chunks:\n"+contextData, &corrected); retryErr != nil {
			return fmt.Errorf("news synthesis validation failed and correction failed: %w", retryErr)
		}
		if retryErr := validateNewsSynthesis(corrected, report.Articles); retryErr != nil {
			return fmt.Errorf("news synthesis validation failed after correction: %w", retryErr)
		}
		synthesis = corrected
	}
	report.Synthesis = synthesis
	return nil
}

func validateNewsSynthesis(s models.NewsSynthesis, articles []models.NewsArticle) error {
	if strings.TrimSpace(s.Headline) == "" || strings.TrimSpace(s.Article) == "" || strings.TrimSpace(s.Summary) == "" {
		return fmt.Errorf("news synthesis returned incomplete headline, article, or summary")
	}
	if err := validateNewsSentiment(s.Sentiment); err != nil {
		return fmt.Errorf("overall news sentiment invalid: %w", err)
	}
	known := make(map[string]bool)
	for _, article := range articles {
		known[article.URL], known[article.CanonicalURL] = true, true
	}
	for _, citation := range s.Citations {
		for _, citationURL := range citation.URLs {
			if !known[citationURL] {
				return fmt.Errorf("news synthesis cited an unsupplied URL %q", citationURL)
			}
		}
	}
	for _, sentiment := range s.ArticleSentiments {
		if !known[sentiment.URL] {
			return fmt.Errorf("news sentiment cited an unsupplied URL %q", sentiment.URL)
		}
		if err := validateNewsSentiment(sentiment); err != nil {
			return fmt.Errorf("article sentiment invalid: %w", err)
		}
	}
	for _, conflict := range s.Conflicts {
		if conflict.Resolution != "updated" && conflict.Resolution != "unresolved" && conflict.Resolution != "clarified" {
			return fmt.Errorf("news conflict has invalid resolution %q", conflict.Resolution)
		}
	}
	return nil
}

func validateNewsSentiment(sentiment models.NewsArticleSentiment) error {
	switch sentiment.Label {
	case "positive", "negative", "neutral", "mixed":
	default:
		return fmt.Errorf("invalid label %q", sentiment.Label)
	}
	if sentiment.Score < -1 || sentiment.Score > 1 || sentiment.Confidence < 0 || sentiment.Confidence > 1 {
		return fmt.Errorf("score or confidence is outside its allowed range")
	}
	return nil
}

func newsQueries(query string, hosts []string) []string {
	queries := make([]string, 0, newsMaxQueries)
	for start := 0; start < len(hosts); start += 6 {
		end := start + 6
		if end > len(hosts) {
			end = len(hosts)
		}
		parts := make([]string, 0, end-start)
		for _, host := range hosts[start:end] {
			parts = append(parts, "site:"+host)
		}
		queries = append(queries, query+" news ("+strings.Join(parts, " OR ")+")")
		if len(queries) >= newsMaxQueries {
			break
		}
	}
	return queries
}

func newsHostAllowed(host string, allowed []string) bool {
	host = normalizeNewsHost(host)
	for _, candidate := range allowed {
		candidate = normalizeNewsHost(candidate)
		if host == candidate || strings.HasSuffix(host, "."+candidate) {
			return true
		}
	}
	return false
}

func normalizedNewsHosts(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		if normalized := normalizeNewsHost(host); normalized != "" {
			out = append(out, normalized)
		}
	}
	return uniqueStrings(out)
}

func normalizeNewsHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimPrefix(host, "www.")
	if parsed, err := url.Parse(host); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	return strings.TrimPrefix(host, "www.")
}

func canonicalNewsURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return ""
	}
	parsed.Fragment = ""
	parsed.Host = normalizeNewsHost(parsed.Hostname())
	parsed.RawQuery = ""
	return strings.TrimRight(parsed.String(), "/")
}

func parseNewsTime(raw string, now time.Time) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if strings.HasSuffix(strings.ToLower(raw), "ago") {
		fields := strings.Fields(strings.ToLower(raw))
		if len(fields) >= 2 {
			value, _ := strconv.Atoi(fields[0])
			if value > 0 {
				var unit time.Duration
				switch fields[1] {
				case "minute", "minutes":
					unit = time.Minute
				case "hour", "hours":
					unit = time.Hour
				case "day", "days":
					unit = 24 * time.Hour
				}
				if unit > 0 {
					return now.Add(-time.Duration(value) * unit)
				}
			}
		}
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05 -0700 MST", "2006-01-02", "Jan 2, 2006", "January 2, 2006"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func newsDatesFromJSONLD(values []string) (time.Time, time.Time) {
	var published, updated time.Time
	for _, raw := range values {
		var value any
		if json.Unmarshal([]byte(raw), &value) != nil {
			continue
		}
		var visit func(any)
		visit = func(node any) {
			switch typed := node.(type) {
			case map[string]any:
				for key, child := range typed {
					switch strings.ToLower(key) {
					case "datepublished":
						if parsed := parseNewsTime(fmt.Sprint(child), time.Now().UTC()); parsed.After(published) {
							published = parsed
						}
					case "datemodified", "dateupdated":
						if parsed := parseNewsTime(fmt.Sprint(child), time.Now().UTC()); parsed.After(updated) {
							updated = parsed
						}
					}
					visit(child)
				}
			case []any:
				for _, child := range typed {
					visit(child)
				}
			}
		}
		visit(value)
	}
	return published, updated
}

func newsChunks(article models.NewsArticle, _ time.Time) []models.NewsChunk {
	text := strings.TrimSpace(article.Content)
	if text == "" {
		return nil
	}
	chunks := make([]models.NewsChunk, 0)
	step := newsChunkSize - newsChunkOverlap
	for start, position := 0, 1; start < len(text); start, position = start+step, position+1 {
		end := start + newsChunkSize
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, models.NewsChunk{
			ID: fmt.Sprintf("%s-chunk-%d", article.CanonicalURL, position), ArticleURL: article.URL,
			Source: article.Source, Title: article.Title, PublishedAt: article.PublishedAt,
			Position: position, Text: text[start:end], Labels: []string{"news", "current", article.Source},
		})
		if end == len(text) {
			break
		}
	}
	return chunks
}

func firstNewsNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
