package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
	"google.golang.org/genai"
)

// defaultGeminiModel is used when no model is configured.
const defaultGeminiModel = "gemini-2.5-flash-lite"

// GeminiClient wraps the Google GenAI SDK for Gemini 2.5 Flash Lite.
type GeminiClient struct {
	client *genai.Client
	model  string
}

// NewGeminiClient initializes a client with Vertex AI / Google GenAI authentication.
//
// The SDK picks up credentials automatically:
//   - Gemini API backend: set GOOGLE_API_KEY
//   - Vertex AI backend: set GOOGLE_GENAI_USE_VERTEXAI=true plus
//     GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION (Application Default
//     Credentials are used for auth)
//
// On failure the returned error names the missing configuration.
func NewGeminiClient(ctx context.Context, model string) (*GeminiClient, error) {
	if model == "" {
		model = defaultGeminiModel
	}

	client, err := genai.NewClient(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize GenAI client (set GOOGLE_API_KEY for the Gemini API, or GOOGLE_GENAI_USE_VERTEXAI=true with GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION for Vertex AI): %w", err)
	}

	return &GeminiClient{
		client: client,
		model:  model,
	}, nil
}

// GenerateContent sends prompt messages to Gemini and returns text.
func (g *GeminiClient) GenerateContent(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{
				{Text: systemPrompt},
			},
		},
		Temperature: genai.Ptr(float64(0.2)),
	}

	resp, err := g.client.Models.GenerateContent(ctx, g.model, genai.Text(userPrompt), config)
	if err != nil {
		return "", fmt.Errorf("gemini generation failed: %w", err)
	}

	return responseText(resp)
}

// ExtractImageText downloads bounded image inputs and asks Gemini to transcribe
// visible text. Image failures are returned to the caller without affecting
// ordinary text research.
func (g *GeminiClient) ExtractImageText(ctx context.Context, images []models.Image) (map[string]string, error) {
	if len(images) == 0 {
		return nil, nil
	}
	const maxImages = 4
	const maxImageBytes = 4 << 20
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	parts := make([]*genai.Part, 0, maxImages+1)
	parts = append(parts, genai.NewPartFromText(`Transcribe only text visibly present in the supplied images. Do not identify people, infer objects, or add facts. Return JSON with an images array; each item must contain the exact image URL and extracted_text. Use an empty string when no readable text is present.`))
	accepted := make(map[string]bool)
	for _, image := range images {
		if len(accepted) >= maxImages || accepted[image.URL] {
			continue
		}
		parsed, err := url.Parse(image.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			continue
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, image.URL, nil)
		if err != nil {
			continue
		}
		request.Header.Set("User-Agent", "Mozilla/5.0")
		response, err := client.Do(request)
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, maxImageBytes+1))
		response.Body.Close()
		mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if mediaErr != nil {
			mediaType = strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
		}
		if readErr != nil || response.StatusCode != http.StatusOK || len(data) > maxImageBytes || !strings.HasPrefix(strings.ToLower(mediaType), "image/") || strings.EqualFold(mediaType, "image/svg+xml") {
			continue
		}
		parts = append(parts, genai.NewPartFromText(fmt.Sprintf("Image URL: %s\nAlt text: %s\nCaption: %s", image.URL, image.Alt, image.Caption)))
		parts = append(parts, genai.NewPartFromBytes(data, mediaType))
		accepted[image.URL] = true
	}
	if len(accepted) == 0 {
		return nil, fmt.Errorf("OCR skipped: no downloadable supported raster images were accepted")
	}
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "You are a careful OCR extraction service."}}},
		Temperature:       genai.Ptr(float64(0.1)),
	}
	response, err := g.client.Models.GenerateContent(ctx, g.model, []*genai.Content{{Parts: parts}}, config)
	if err != nil {
		return nil, fmt.Errorf("image OCR generation failed: %w", err)
	}
	rawResponse, err := responseText(response)
	if err != nil {
		return nil, err
	}
	items, err := decodeOCRResponse(rawResponse)
	if err != nil {
		return nil, err
	}
	textByURL := make(map[string]string, len(items))
	for _, item := range items {
		if accepted[item.URL] && strings.TrimSpace(item.Text) != "" {
			textByURL[item.URL] = strings.TrimSpace(item.Text)
		}
	}
	return textByURL, nil
}

type ocrImageResult struct {
	URL  string `json:"url"`
	Text string `json:"extracted_text"`
}

func decodeOCRResponse(raw string) ([]ocrImageResult, error) {
	cleaned := cleanJSONString(raw)
	var wrapped struct {
		Images []ocrImageResult `json:"images"`
	}
	if err := json.Unmarshal([]byte(cleaned), &wrapped); err == nil && wrapped.Images != nil {
		return wrapped.Images, nil
	}
	var items []ocrImageResult
	if err := json.Unmarshal([]byte(cleaned), &items); err == nil {
		return items, nil
	}
	// Gemini occasionally emits several JSON objects separated by commas
	// instead of wrapping them in an array.
	if strings.HasPrefix(cleaned, "{") && strings.Contains(cleaned, "},") {
		if err := json.Unmarshal([]byte("["+cleaned+"]"), &items); err == nil {
			return items, nil
		}
	}
	return nil, fmt.Errorf("image OCR response was not a valid object, array, or comma-separated object list")
}

// GenerateStructured requests JSON output and unmarshals it into target.
// The model is instructed to emit bare JSON; markdown fences or stray prose
// are cleaned before parsing, and one stricter retry is attempted if the
// first response cannot be decoded.
func (g *GeminiClient) GenerateStructured(ctx context.Context, systemPrompt, userPrompt string, target any) error {
	sysInstruction := systemPrompt + "\n\nCRITICAL: Respond ONLY with valid JSON matching the requested schema. Do not enclose in markdown code blocks, and do not add explanatory preamble."

	rawJSON, err := g.GenerateContent(ctx, sysInstruction, userPrompt)
	if err != nil {
		return err
	}

	if err := decodeJSON(rawJSON, target); err == nil {
		return nil
	}

	// Retry once: models occasionally wrap JSON in prose or fences despite the
	// instruction; a reminder after a failed attempt usually fixes it.
	retrySys := sysInstruction + "\nYour previous response was not parseable. Output ONLY the raw JSON object, starting with { and ending with }."
	rawRetry, err := g.GenerateContent(ctx, retrySys, userPrompt)
	if err != nil {
		return err
	}
	if err := decodeJSON(rawRetry, target); err != nil {
		return fmt.Errorf("structured output could not be decoded after retry: %w", err)
	}
	return nil
}

// decodeJSON cleans a raw LLM response and unmarshals it into target.
func decodeJSON(raw string, target any) error {
	cleaned := cleanJSONString(raw)
	if err := json.Unmarshal([]byte(cleaned), target); err != nil {
		return fmt.Errorf("failed to unmarshal structured output: %w (raw: %s)", err, cleaned)
	}
	return nil
}

func decodeJSONMust(raw string, target any) error {
	if err := decodeJSON(raw, target); err != nil {
		return fmt.Errorf("image OCR response could not be decoded: %w", err)
	}
	return nil
}

// responseText joins the text parts of the first candidate.
func responseText(resp *genai.GenerateContentResponse) (string, error) {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return "", fmt.Errorf("no content generated by gemini (possibly safety-blocked)")
	}

	var output strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		output.WriteString(part.Text)
	}

	if strings.TrimSpace(output.String()) == "" {
		return "", fmt.Errorf("no content generated by gemini (possibly safety-blocked)")
	}
	return output.String(), nil
}

// cleanJSONString strips markdown fences and surrounding prose, returning the
// outermost JSON object when the model wrapped it in commentary.
func cleanJSONString(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "```json"))
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "```"))
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSpace(strings.TrimSuffix(s, "```"))
	}
	// If stray text still surrounds the object, extract the outermost braces.
	if len(s) > 0 && ((s[0] != '{' && s[0] != '[') || (s[len(s)-1] != '}' && s[len(s)-1] != ']')) {
		if i := strings.IndexByte(s, '{'); i >= 0 {
			if j := strings.LastIndexByte(s, '}'); j > i {
				s = s[i : j+1]
			}
		}
	}
	return s
}
