package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// SerperClient handles Google Search requests via Serper.dev API.
type SerperClient struct {
	apiKey     string
	httpClient *http.Client
}

// NewSerperClient creates a new Serper API client.
func NewSerperClient(apiKey string) *SerperClient {
	return &SerperClient{
		apiKey: apiKey,
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

// Search queries Google via Serper and returns structured results.
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://google.serper.dev/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create search request: %w", err)
	}

	req.Header.Set("X-API-KEY", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("serper request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("serper api returned status code: %d", resp.StatusCode)
	}

	var serperResp serperResponse
	if err := json.NewDecoder(resp.Body).Decode(&serperResp); err != nil {
		return nil, fmt.Errorf("failed to decode serper response: %w", err)
	}

	var results []models.SearchResult
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
