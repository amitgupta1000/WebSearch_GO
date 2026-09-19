package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

const (
	defaultSerperURL   = "https://google.serper.dev/search"
	serperMaxAttempts  = 3
	serperBaseRetry    = 500 * time.Millisecond
	serperMaxErrorBody = 512
)

// SerperClient handles Google Search requests via Serper.dev API.
type SerperClient struct {
	apiKey     string
	baseURL    string // overridable in tests
	httpClient *http.Client
}

// NewSerperClient creates a new Serper API client.
func NewSerperClient(apiKey string) *SerperClient {
	return &SerperClient{
		apiKey:  apiKey,
		baseURL: defaultSerperURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type serperSearchPayload struct {
	Q   string `json:"q"`
	Num int    `json:"num,omitempty"`
}

type serperResponse struct {
	Organic []struct {
		Title    string `json:"title"`
		Link     string `json:"link"`
		Snippet  string `json:"snippet"`
		Position int    `json:"position"`
		Date     string `json:"date,omitempty"`
	} `json:"organic"`
	KnowledgeGraph struct {
		Title       string `json:"title"`
		Type        string `json:"type"`
		Description string `json:"description"`
	} `json:"knowledgeGraph"`
}

// retriableError marks transient failures (network errors, 429, 5xx) that are
// eligible for retry with backoff.
type retriableError struct{ err error }

func (e *retriableError) Error() string { return e.err.Error() }
func (e *retriableError) Unwrap() error { return e.err }

// Search queries Google via Serper and returns structured results.
// Transient failures are retried with linear backoff; the response body of
// failed calls is included in the returned error for easier debugging.
func (s *SerperClient) Search(ctx context.Context, query string, maxResults int) ([]models.SearchResult, error) {
	if s.apiKey == "" {
		return nil, fmt.Errorf("SERPER_API_KEY is not configured")
	}
	if maxResults <= 0 {
		maxResults = 10
	}

	payload := serperSearchPayload{
		Q:   query,
		Num: maxResults,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode search payload: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= serperMaxAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(serperBaseRetry * time.Duration(attempt-1)):
			}
		}

		results, err := s.attempt(ctx, bodyBytes)
		if err == nil {
			return results, nil
		}
		lastErr = err

		var retriable *retriableError
		if !errors.As(err, &retriable) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("serper search failed after %d attempts: %w", serperMaxAttempts, lastErr)
}

// attempt performs a single Serper API request.
func (s *SerperClient) attempt(ctx context.Context, bodyBytes []byte) ([]models.SearchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create search request: %w", err)
	}

	req.Header.Set("X-API-KEY", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, &retriableError{fmt.Errorf("serper request failed: %w", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, serperMaxErrorBody))
		err := fmt.Errorf("serper api returned status code %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, &retriableError{err}
		}
		return nil, err
	}

	var serperResp serperResponse
	if err := json.NewDecoder(resp.Body).Decode(&serperResp); err != nil {
		return nil, fmt.Errorf("failed to decode serper response: %w", err)
	}

	results := make([]models.SearchResult, 0, len(serperResp.Organic))
	for _, item := range serperResp.Organic {
		results = append(results, models.SearchResult{
			Title:    item.Title,
			Link:     item.Link,
			Snippet:  item.Snippet,
			Position: item.Position,
			Date:     item.Date,
		})
	}

	return results, nil
}
