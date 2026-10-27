package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// caclubindia.go is the client for caclubindia.com, India's largest chartered-
// accountancy community — the practice-knowledge layer of the research corpus.
//
// Measured (October 2026):
//
//   - The site is server-rendered classic ASP, and thread pages, category
//     listings and the forum index are public without login (posting and Pro
//     features are what accounts gate). robots.txt disallows /api, /community,
//     /cart and a few app paths — the forum paths are allowed.
//   - Its built-in search (search.asp / search_results.asp) renders a shell and
//     loads results client-side, so a plain-HTTP query path does not exist.
//     Query access therefore rides the search engine with site: anchoring in
//     the agent layer (the same pattern as the Casemine snippet track), while
//     this client fetches the server-rendered thread pages themselves.
//   - Thread listings render as <div class="thread-card"> blocks whose titles
//     link <slug>-<numeric-id>.asp pages.
//
// The client is deliberately polite: one request a second, one in flight. It
// extracts the thread title and the visible text of the discussion — the memo
// layer classifies what the thread means (practice signal, unverified) and
// never cites it as authority.

const (
	defaultCaclubindiaURL  = "https://www.caclubindia.com"
	caclubindiaMaxDocBytes = 2 << 20
)

// CCIThread is one forum discussion: the question a practitioner raised and
// the discussion around it, as visible text.
type CCIThread struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	// Text is the visible discussion text (question and replies), flattened
	// like the scraper's text extraction. Classification of what the thread
	// means is the agent layer's job, not the client's.
	Text string `json:"text,omitempty"`
	// Category is the forum section the page names, when it does.
	Category string `json:"category,omitempty"`
}

// CCIClient fetches Caclubindia threads.
type CCIClient struct {
	baseURL   string // overridable in tests
	fetchHTML func(ctx context.Context, targetURL string) (string, error)
	limiter   *hostLimiter
}

// NewCCIClient builds the client on a scraper's plain-HTML fetch (browser-like
// headers, cookie jar).
func NewCCIClient(scraper *Scraper) *CCIClient {
	if scraper == nil {
		return &CCIClient{baseURL: defaultCaclubindiaURL, limiter: newHostLimiter(1.0, 1, 1)}
	}
	return &CCIClient{
		baseURL:   defaultCaclubindiaURL,
		fetchHTML: scraper.FetchHTML,
		limiter:   newHostLimiter(1.0, 1, 1),
	}
}

// Thread fetches one discussion page and extracts its title and visible text.
// The URL may be absolute or the thread's own relative form; anything that does
// not look like a Caclubindia thread (or a page that answers a wall) is
// refused by name rather than parsed into an empty result.
func (c *CCIClient) Thread(ctx context.Context, threadURL string) (*CCIThread, error) {
	trimmed := strings.TrimSpace(threadURL)
	if trimmed == "" {
		return nil, fmt.Errorf("caclubindia thread url is empty")
	}
	target := trimmed
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		target = c.baseURL + "/" + strings.TrimPrefix(trimmed, "/")
	}

	if _, err := c.limiter.acquire(ctx, hostOf(target)); err != nil {
		return nil, err
	}
	body, err := c.fetchHTML(ctx, target)
	if err != nil {
		return nil, err
	}
	if len(body) < casemineWallMinBytes {
		return nil, fmt.Errorf("caclubindia returned a wall or an empty page for %s", target)
	}
	return parseCCIThread(target, body), nil
}

// parseCCIThread extracts the thread title and visible discussion text. The
// thread page's <title> and the thread-card title class are both known shapes;
// the discussion body is the visible text of the page minus the site chrome,
// extracted defensively because thread-post markup varies between the forum's
// generations. A parse that finds nothing still returns the URL-carrying
// thread, so the memo can link it even when the text could not be extracted.
func parseCCIThread(threadURL string, body string) *CCIThread {
	parsed, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return &CCIThread{URL: threadURL}
	}

	thread := &CCIThread{URL: threadURL}
	thread.Title = collapseIndianKanoonSpace(parsed.Find(".thread-title").First().Text())
	if thread.Title == "" {
		thread.Title = collapseIndianKanoonSpace(parsed.Find("h1").First().Text())
	}
	if thread.Title == "" {
		thread.Title = collapseIndianKanoonSpace(parsed.Find("title").First().Text())
	}
	thread.Category = collapseIndianKanoonSpace(parsed.Find(".thread-category, .forum-name").First().Text())

	// The discussion: post containers when the markup cooperates, otherwise
	// the page's visible text with the boilerplate classes removed.
	posts := parsed.Find(".thread-post, .post-content, .forum-post, .comment-content")
	if posts.Length() > 0 {
		var builder strings.Builder
		posts.Each(func(_ int, post *goquery.Selection) {
			text := collapseIndianKanoonSpace(post.Text())
			if text != "" {
				builder.WriteString(text)
				builder.WriteString("\n\n")
			}
		})
		thread.Text = strings.TrimSpace(builder.String())
	}
	if thread.Text == "" {
		parsed.Find("nav, header, footer, aside, script, style, noscript, form").Remove()
		thread.Text = collapseIndianKanoonSpace(parsed.Find("body").Text())
	}
	return thread
}
