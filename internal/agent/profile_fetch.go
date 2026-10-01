package agent

import (
	"context"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// profile_fetch.go holds the one rule both modes share about professional profiles: when the
// operator has supplied a session that covers the host, read the page the cheap way — one
// cookie-authenticated HTTP fetch — before paying for a render.
//
// Measured on a live LinkedIn export, that fetch returned the real profile (949 KB, no
// authwall) while the render sequence reached the sign-in page and tripped the site's redirect
// loop. With no session the attempt is never made: the same request answers HTTP 999 (a
// deliberate refusal, 1.5 KB, no content), so it would spend a scrape slot for nothing.

// fetchProfileWithSession returns the profile page when a session can read it, or nil when
// there is no session for that host or the page did not come back usable. Callers fall through
// to the ladder on nil, so the degradation order in both modes is: session fetch → ladder →
// public snippet.
//
// It deliberately does not consult ALLOW_PROFILE_SCRAPING. That flag gates the batch path —
// "fetch profiles even without a session" — whereas a session is explicit operator consent for
// that host, and one authenticated GET is less intrusive than the logged-out render the
// default already performs.
func fetchProfileWithSession(ctx context.Context, engine *Engine, targetURL string) *models.ScrapeResult {
	if engine == nil || !engine.SessionCookiesFor(targetURL) {
		return nil
	}
	result, err := engine.ScrapeCached(ctx, targetURL)
	if err != nil || result == nil || result.Blocked || len(result.Content) <= minScrapeContentLen {
		return nil
	}
	return result
}
