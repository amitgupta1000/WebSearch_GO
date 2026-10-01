# Reading Go Through a Python Lens

This guide uses [`internal/llm/gemini.go`](../internal/llm/gemini.go) as a working example. The Python examples are illustrative equivalents, not copy-paste replacements.

## File and Imports

| Go | Python idea |
| --- | --- |
| `package llm` | The module/package this file belongs to. Every `.go` file in the directory normally uses the same package. |
| `import ( ... )` | Python imports. Go requires imports to be used; unused imports fail compilation. |
| `"encoding/json"` | Python's `json` module. |
| `"context"` | Explicit request cancellation and deadlines. Python usually passes a timeout, cancellation token, or async context separately. |

```go
package llm

import (
    "context"
    "encoding/json"
    "fmt"
)
```

```python
import contextlib
import json
from dataclasses import dataclass
```

## Constants and Configuration

```go
const defaultGeminiModel = "gemini-2.5-flash-lite"

const (
    geminiMaxAttempts = 3
    geminiBaseRetry   = 500 * time.Millisecond
)
```

The closest Python form is:

```python
DEFAULT_GEMINI_MODEL = "gemini-2.5-flash-lite"
GEMINI_MAX_ATTEMPTS = 3
GEMINI_BASE_RETRY_SECONDS = 0.5
```

Go constants are commonly lower-case when they are private to the package. Capitalization controls visibility: an upper-case name is exported outside the package.

## Structs Versus Python Classes

```go
type GeminiClient struct {
    client *genai.Client
    model  string
}
```

This is a data structure with two fields. The `*` means the field stores a pointer to a `genai.Client`.

A close Python equivalent is:

```python
@dataclass
class GeminiClient:
    client: GenAIClient
    model: str
```

Go structs do not automatically provide constructors, so the project uses a normal function by convention:

```go
func NewGeminiClient(ctx context.Context, model string) (*GeminiClient, error) {
    if model == "" {
        model = defaultGeminiModel
    }

    client, err := genai.NewClient(ctx, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to initialize GenAI client: %w", err)
    }

    return &GeminiClient{
        client: client,
        model:  model,
    }, nil
}
```

The Python-shaped version would be:

```python
class GeminiClient:
    @classmethod
    def create(cls, context, model=""):
        if not model:
            model = DEFAULT_GEMINI_MODEL

        try:
            client = genai.new_client(context)
        except Exception as exc:
            raise RuntimeError("failed to initialize GenAI client") from exc

        return cls(client=client, model=model)
```

Important difference: Go returns both a value and an error. It does not normally throw an exception for expected operational failures.

## Methods and Receivers

```go
func (g *GeminiClient) GenerateContent(
    ctx context.Context,
    systemPrompt string,
    userPrompt string,
) (string, error) {
    // ...
}
```

The `(g *GeminiClient)` portion is the method receiver. It is the Go equivalent of Python's `self`:

```python
class GeminiClient:
    def generate_content(self, context, system_prompt, user_prompt):
        # ...
        return text, None
```

Go conventionally uses short receiver names such as `g`, but ordinary local variables should still have descriptive names.

## Error Handling

Go commonly handles errors explicitly:

```go
response, err := g.client.Models.GenerateContent(...)
if err != nil {
    return "", fmt.Errorf("gemini generation failed: %w", err)
}
return responseText(response)
```

The Python shape is:

```python
try:
    response = client.models.generate_content(...)
except Exception as exc:
    raise RuntimeError("gemini generation failed") from exc

return response_text(response)
```

The Go convention is:

```go
value, err := operation()
if err != nil {
    return err
}
```

The `:=` operator declares new local variables and infers their types. If a variable already exists, use `=` instead.

`%w` wraps an error while preserving the original error for later inspection. It is similar to Python's `raise ... from exc`.

## Retry Loops

From `GenerateContent`:

```go
var lastErr error
for attempt := 1; attempt <= geminiMaxAttempts; attempt++ {
    if attempt > 1 {
        if err := sleepWithContext(ctx, geminiBaseRetry*time.Duration(attempt-1)); err != nil {
            return "", err
        }
    }

    response, err := g.client.Models.GenerateContent(...)
    if err == nil {
        return responseText(response)
    }
    if !isTransientGenAIError(err) {
        return "", fmt.Errorf("gemini generation failed: %w", err)
    }
    lastErr = err
}

return "", fmt.Errorf("gemini generation failed after %d attempts: %w", geminiMaxAttempts, lastErr)
```

Python equivalent:

```python
last_error = None
for attempt in range(1, GEMINI_MAX_ATTEMPTS + 1):
    if attempt > 1:
        sleep_with_context(context, GEMINI_BASE_RETRY_SECONDS * (attempt - 1))

    try:
        response = client.models.generate_content(...)
        return response_text(response)
    except Exception as exc:
        if not is_transient_genai_error(exc):
            raise RuntimeError("gemini generation failed") from exc
        last_error = exc

raise RuntimeError("gemini generation failed after all attempts") from last_error
```

Go's `for` is its general-purpose loop. There is no separate `while` keyword; a `for` without the three clauses behaves like `while`.

## Context, Cancellation, and Deadlines

Most public operations accept a `context.Context`:

```go
func (g *GeminiClient) ExtractDocumentText(
    ctx context.Context,
    contentType string,
    body []byte,
) (string, error) {
    requestCtx, cancel := context.WithTimeout(ctx, documentRequestTimeout)
    defer cancel()

    response, err := g.client.Models.GenerateContent(requestCtx, ...)
    // ...
}
```

`context.WithTimeout` creates a child context that is cancelled after the deadline. The cancellation travels into the SDK call.

A Python async application might express the same intent as:

```python
async with asyncio.timeout(60):
    response = await client.generate_content(...)
```

The concepts are similar, but Go's context is passed explicitly through every layer. This makes cancellation visible in function signatures and lets a parent request cancel all child work.

## `defer` and Cleanup

```go
requestCtx, cancel := context.WithTimeout(ctx, documentRequestTimeout)
defer cancel()
```

`defer` runs the call when the surrounding function returns, whether it returns normally or because of an error. It is commonly used for cleanup.

Python equivalent:

```python
request_context = create_timeout_context(60)
try:
    response = await client.generate_content(...)
finally:
    request_context.cancel()
```

Other examples in this file include:

```go
defer timer.Stop()
response.Body.Close()
```

These prevent timers, HTTP response bodies, and similar resources from being left open.

## Slices, Maps, and Byte Data

A Go slice is the usual equivalent of a Python list:

```go
parts := []*genai.Part{
    genai.NewPartFromText("Return the text..."),
}
```

A typed map is similar to a typed Python dictionary:

```go
textByURL := make(map[string]string, len(items))
textByURL[item.URL] = strings.TrimSpace(item.Text)
```

The Python version is:

```python
parts: list[GenAI.Part] = [
    GenAI.Part.from_text("Return the text...")
]

text_by_url: dict[str, str] = {}
text_by_url[item.url] = item.text.strip()
```

`[]byte` means a byte slice. It is commonly used for downloaded files and HTTP bodies:

```go
func (g *GeminiClient) ExtractDocumentText(
    ctx context.Context,
    contentType string,
    body []byte,
) (string, error)
```

The Python equivalent is usually `bytes`.

## JSON and Typed Results

The project asks Gemini for JSON and decodes it into a caller-provided target:

```go
func (g *GeminiClient) GenerateStructured(
    ctx context.Context,
    systemPrompt string,
    userPrompt string,
    target any,
) error {
    rawJSON, err := g.GenerateContent(ctx, systemPrompt, userPrompt)
    if err != nil {
        return err
    }

    return decodeJSON(rawJSON, target)
}
```

`any` means the function accepts a value of any type. The caller normally supplies a pointer to a struct:

```go
var chart models.OrgChart
err := client.GenerateStructured(ctx, systemPrompt, userPrompt, &chart)
```

`decodeJSON` fills that struct:

```go
func decodeJSON(raw string, target any) error {
    cleaned := cleanJSONString(raw)
    if err := json.Unmarshal([]byte(cleaned), target); err != nil {
        return fmt.Errorf("failed to unmarshal structured output: %w", err)
    }
    return nil
}
```

Python usually creates a dictionary first, then validates it with a model:

```python
raw = await client.generate_content(...)
data = json.loads(clean_json_string(raw))
chart = OrgChart.model_validate(data)  # Pydantic-style example
```

Go's approach is more explicit: the destination type determines the expected JSON shape at compile time, while Python commonly validates at runtime.

## HTTP and Resource Validation

This section accepts only safe, bounded image downloads:

```go
parsed, err := url.Parse(image.URL)
if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
    continue
}

request, err := http.NewRequestWithContext(ctx, http.MethodGet, image.URL, nil)
if err != nil {
    continue
}
```

This is similar to:

```python
parsed = urllib.parse.urlparse(image.url)
if (
    parsed.scheme not in {"http", "https"}
    or not parsed.netloc
):
    continue

request = urllib.request.Request(image.url)
```

The Go code also limits response size and checks the content type before sending data to Gemini. `continue` skips the current loop item, just as it does in Python.

## A Useful Reading Pattern

When reading another Go file in this project, scan it in this order:

1. **Package and imports:** identify the module boundary and dependencies.
2. **Types:** find the structs and interfaces that define the data flow.
3. **Constructors:** look for `New...` functions and required dependencies.
4. **Public methods:** identify the operations other packages call.
5. **Error returns:** trace every `err` check to understand failure behavior.
6. **Context parameters:** note where timeouts and cancellation enter the call chain.
7. **Interfaces:** these are contracts, similar to Python protocols or abstract base classes.
8. **Tests:** look for the intended behavior and the edge cases the author considers important.

The key mental shift is that Go makes many things explicit that Python often leaves implicit: types, error paths, cancellation, resource cleanup, and which dependencies a component receives.
