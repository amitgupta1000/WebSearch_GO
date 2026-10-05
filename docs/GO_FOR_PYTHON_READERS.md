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

---

# Scraper Walkthrough

This section uses [`internal/tools/scraper.go`](../internal/tools/scraper.go) as a second example. Its main job is to fetch a URL, decide whether the response is usable, extract meaningful content, and return a typed `models.ScrapeResult`.

## The Scraper Type

```go
type Scraper struct {
    client         *http.Client
    browserEnabled bool
    browserTimeout time.Duration
    allowPrivate   bool
    fetchAttempts  int
    fetchBackoff   time.Duration
    renderSem      chan struct{}
    hosts          *hostLimiter
}
```

Python equivalent:

```python
@dataclass
class Scraper:
    client: httpx.Client
    browser_enabled: bool = False
    browser_timeout: float = 15.0
    allow_private: bool = False
    fetch_attempts: int = 3
    fetch_backoff: float = 0.4
    render_sem: asyncio.Semaphore | None = None
    hosts: HostLimiter | None = None
```

The Go struct stores the scraper's dependencies and policies. The fields are not automatically public: lower-case names are visible only inside package `tools`.

## Constructor and Options

```go
func NewScraper(jar *cookiejar.Jar, timeout time.Duration, opts ...Option) *Scraper {
    return NewScraperWithBrowser(jar, timeout, false, opts...)
}
```

`opts ...Option` is a variadic argument. It lets callers pass zero or more configuration functions:

```go
scraper := NewScraper(
    jar,
    15*time.Second,
    WithPrivateHostAccess(),
    WithDocumentTextExtractor(extractor),
)
```

The option type is a function:

```go
type Option func(*Scraper)

func WithPrivateHostAccess() Option {
    return func(s *Scraper) { s.allowPrivate = true }
}
```

Python often uses keyword arguments or a builder:

```python
scraper = Scraper(
    jar=jar,
    timeout=15.0,
    allow_private=True,
    document_extractor=extractor,
)
```

The Go option pattern is useful when there are many optional settings without making the constructor signature enormous.

## The Public Scrape Method

The main entry point is:

```go
func (s *Scraper) ScrapeURL(
    ctx context.Context,
    targetURL string,
) (*models.ScrapeResult, error) {
    if err := s.validateTarget(targetURL); err != nil {
        return nil, err
    }
    if s.isClosed() {
        return nil, fmt.Errorf("scraper is closed")
    }

    response, err := s.fetchHTTP(ctx, targetURL)
    if err != nil {
        return s.tryBrowser(ctx, targetURL, nil, "HTTP request failed", err)
    }

    result, err := extractHTML(targetURL, response.ContentType, response.Body, "goquery-content-root", response.Truncated)
    if err != nil {
        return s.tryBrowser(ctx, targetURL, nil, "HTTP HTML parsing failed", err)
    }

    if needsBrowserFallback(result) {
        return s.tryBrowser(ctx, targetURL, result, "low-quality or JavaScript-shell page", nil)
    }
    return result, nil
}
```

Python-shaped equivalent:

```python
async def scrape_url(self, context, target_url):
    self.validate_target(target_url)
    if self.closed:
        raise RuntimeError("scraper is closed")

    try:
        response = await self.fetch_http(context, target_url)
    except Exception as exc:
        return await self.try_browser(context, target_url, None, "HTTP request failed", exc)

    try:
        result = extract_html(target_url, response.content_type, response.body)
    except Exception as exc:
        return await self.try_browser(context, target_url, None, "HTML parsing failed", exc)

    if needs_browser_fallback(result):
        return await self.try_browser(context, target_url, result, "low-quality page", None)
    return result
```

Read `ScrapeURL` as a decision tree:

1. Reject unsafe input.
2. Reject a closed scraper.
3. Fetch with retries and host limits.
4. Parse HTML or extract a supported document.
5. If the page is blocked, too short, or looks like a JavaScript shell, try Chromium.
6. Return the usable result or an error.

The method does not launch a goroutine for each URL. The caller can run the whole research session in one goroutine, while the scraper remains easy to reason about and can still be called concurrently by separate sessions.

## URL Validation and SSRF Protection

```go
func (s *Scraper) validateTarget(targetURL string) error {
    parsed, err := url.Parse(strings.TrimSpace(targetURL))
    if err != nil {
        return fmt.Errorf("invalid target URL %q: %w", targetURL, err)
    }
    if parsed.Scheme != "http" && parsed.Scheme != "https" {
        return fmt.Errorf("refusing to scrape non-HTTP(S) URL %q", targetURL)
    }
    if parsed.Hostname() == "" {
        return fmt.Errorf("refusing to scrape URL without a host %q", targetURL)
    }
    if !s.allowPrivate && isPrivateHost(parsed.Hostname()) {
        return fmt.Errorf("refusing to scrape private or non-routable host %q", targetURL)
    }
    return nil
}
```

Python equivalent:

```python
def validate_target(self, target_url: str) -> None:
    parsed = urllib.parse.urlparse(target_url.strip())
    if parsed.scheme not in {"http", "https"}:
        raise ValueError("only HTTP(S) URLs are allowed")
    if not parsed.hostname:
        raise ValueError("URL must have a host")
    if not self.allow_private and is_private_host(parsed.hostname):
        raise ValueError("private hosts are not allowed")
```

This is an important security boundary. Search results and model output are untrusted; without this check, a scraper could be pointed at `localhost`, a private network service, or a cloud metadata endpoint.

## HTTP Client Construction

```go
client := &http.Client{
    Timeout:   timeout,
    Transport: transport,
    Jar:       jar,
    CheckRedirect: func(req *http.Request, via []*http.Request) error {
        if len(via) >= maxRedirects {
            return http.ErrUseLastResponse
        }
        return nil
    },
}
```

Python with `httpx`:

```python
client = httpx.AsyncClient(
    timeout=timeout,
    follow_redirects=True,
    max_redirects=5,
    cookies=cookies,
)
```

The Go version explicitly configures connection pooling, cookies, request timeout, and redirect policy. `http.Client` is intended to be reused rather than recreated for every request.

## Retries and `Retry-After`

```go
for attempt := 1; attempt <= attempts; attempt++ {
    if attempt > 1 {
        delay := s.fetchBackoff * time.Duration(attempt-1)
        if retryAfter > delay {
            delay = retryAfter
        }
        if err := sleepWithContext(ctx, delay); err != nil {
            return nil, err
        }
    }

    response, err := s.fetchHTTPOnce(ctx, targetURL)
    if err == nil && !isRetriableStatus(response.Status) {
        return response, nil
    }
}
```

Python equivalent:

```python
for attempt in range(1, attempts + 1):
    if attempt > 1:
        await sleep_with_context(context, backoff * (attempt - 1))

    response = await fetch_once(context, target_url)
    if response.ok or not is_retriable_status(response.status_code):
        return response

raise ScrapeError("scraping failed after all attempts")
```

This scraper retries network errors and temporary HTTP statuses such as `408`, `429`, and `5xx`. It also reads `Retry-After` and respects the server's requested delay, subject to a configured maximum.

The Go-specific part is cancellation-aware sleeping:

```go
func sleepWithContext(ctx context.Context, d time.Duration) error {
    timer := time.NewTimer(d)
    defer timer.Stop()
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-timer.C:
        return nil
    }
}
```

A normal `time.Sleep` would ignore cancellation until the sleep ended. This version stops promptly when the research deadline expires.

## Reading a Bounded Response

```go
body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
if err != nil {
    return nil, &retriableError{fmt.Errorf("failed to read HTML document: %w", err)}
}
response.Truncated = len(body) > maxBodyBytes
if response.Truncated {
    body = body[:maxBodyBytes]
}
```

Python equivalent:

```python
body = await response.aread(max_body_bytes + 1)
truncated = len(body) > max_body_bytes
if truncated:
    body = body[:max_body_bytes]
```

The extra byte is intentional. Reading exactly the limit cannot tell whether the response ended naturally or continued beyond the limit. The extra byte lets the scraper report truncation instead of silently pretending it received the whole page.

`defer resp.Body.Close()` appears immediately after a successful HTTP response. That is Go's explicit resource-cleanup pattern for response bodies.

## Parsing and Sanitizing HTML

```go
reader, err := charset.NewReader(bytes.NewReader(body), contentType)
doc, err := goquery.NewDocumentFromReader(reader)
```

This is conceptually similar to Python's BeautifulSoup or lxml:

```python
decoded = decode_using_content_type(body, content_type)
soup = BeautifulSoup(decoded, "html.parser")
```

The scraper preserves metadata before removing noise:

```go
title := strings.TrimSpace(doc.Find("title").Text())
canonicalURL := resolveURL(targetURL, canonical)
doc.Find("script[type='application/ld+json']").Each(...)
doc.Find(noiseSelector).Remove()
```

The order matters. JSON-LD is often inside a `<script>` element, so it must be collected before scripts are removed. Challenge detection and content extraction happen after noise removal, so text inside scripts, cookie banners, navigation, and advertisements cannot become evidence.

## Leaf Content Blocks

```go
contentRoot.Find("h1, h2, h3, h4, p, li, article, section").
    Not(":has(h1, h2, h3, h4, p, li, article, section)").
    Each(func(_ int, sel *goquery.Selection) {
        text := strings.TrimSpace(sel.Text())
        if utf8.RuneCountInString(text) >= minTextRunes {
            builder.WriteString(text)
            builder.WriteString("\n\n")
        }
    })
```

Python equivalent:

```python
for element in soup.select("h1, h2, h3, h4, p, li, article, section"):
    if element.select_one("h1, h2, h3, h4, p, li, article, section"):
        continue  # skip parent blocks to avoid duplicate nested text
    text = element.get_text(" ", strip=True)
    if len(text) >= MIN_TEXT_RUNES:
        blocks.append(text)
```

The `:has(...)` filter selects leaf blocks. Without it, an `<article>` and every paragraph inside it would all be emitted, duplicating the same content several times and wasting LLM context.

## Browser Fallback

```go
if needsBrowserFallback(result) {
    return s.tryBrowser(ctx, targetURL, result, reason, nil)
}
```

The fallback is used when the result is blocked, too short, or has a low quality score. The browser path is not a second scraper implementation; it renders the page and sends the resulting HTML through the same `extractHTML` function.

```python
if needs_browser_fallback(result):
    rendered_html = await browser.render(context, target_url)
    return extract_html(target_url, "text/html", rendered_html)
```

The Go implementation also uses a buffered channel as a counting semaphore:

```go
select {
case s.renderSem <- struct{}{}:
    defer func() { <-s.renderSem }()
case <-ctx.Done():
    return "", ctx.Err()
}
```

This limits concurrent browser pages. The Python equivalent is `asyncio.Semaphore`:

```python
async with self.render_sem:
    return await browser.render(context, target_url)
```

## Typed Results

The scraper returns a model rather than an unstructured dictionary:

```go
return &models.ScrapeResult{
    URL:          targetURL,
    CanonicalURL: canonicalURL,
    Title:        title,
    Content:      content,
    Headings:     headings,
    JSONLD:       jsonLD,
    QualityScore: qualityScore,
    FetchedAt:    time.Now(),
    ContentTruncated: truncated,
}, nil
```

Python equivalent:

```python
return ScrapeResult(
    url=target_url,
    canonical_url=canonical_url,
    title=title,
    content=content,
    headings=headings,
    jsonld=jsonld,
    quality_score=quality_score,
    fetched_at=datetime.now(timezone.utc),
    content_truncated=truncated,
)
```

The `, nil` at the end means “return this result and no error.” A failure returns `nil` for the result and a non-nil error.

## Scraper Mental Model

```text
target URL
   |
   v
validateTarget       reject unsafe schemes and private hosts
   |
   v
fetchHTTP            cookies, redirects, host limits, retries, size cap
   |
   +--> non-HTML document --> document extractor --> ScrapeResult
   |
   v
extractHTML          decode charset, parse DOM, collect metadata
   |
   v
remove noise         scripts, navigation, ads, cookie banners
   |
   v
detect challenge     blocked result, or continue
   |
   v
extract leaf blocks  deduplicate and truncate content
   |
   v
quality decision     return result, or render with Chromium
```

The biggest difference from a typical Python scraper is how many policies are encoded directly in the types and control flow: every operation carries cancellation, errors are returned explicitly, response bodies are closed explicitly, limits are enforced before parsing, and the browser fallback is bounded by a semaphore and context deadline.
