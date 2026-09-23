package tools

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"
)

// hostLimiter enforces politeness per host: a token bucket capping the request
// rate plus a small concurrency cap.
//
// Parallel fetching across many hosts is what makes a run fast; the limiter is
// what keeps that parallelism from concentrating on one site, which is how a
// scraper earns 429s and burned retries. Limits are per process, which is the
// right scope: the load a target site sees comes from this process.
type hostLimiter struct {
	mu    sync.Mutex
	hosts map[string]*hostState
	// ratePerSecond is the sustained request rate per host. Zero disables rate
	// limiting; burst is how many requests may start back-to-back.
	ratePerSecond float64
	burst         float64
	// perHostConcurrency caps simultaneous in-flight requests to one host. Zero
	// disables the concurrency cap.
	perHostConcurrency int
	// now is injectable so tests do not have to sleep through a refill.
	now func() time.Time
}

// hostState is one host's bucket and in-flight counter.
type hostState struct {
	tokens     float64
	lastRefill time.Time
	inFlight   int
}

// maxTrackedHosts bounds the limiter's memory. States are pruned once the map
// passes this size, so a run that touches thousands of hosts cannot grow it
// without limit.
const maxTrackedHosts = 512

// limiterPollInterval is how long a blocked caller waits before re-checking a
// host whose concurrency slot is busy.
const limiterPollInterval = 10 * time.Millisecond

// newHostLimiter builds a limiter. Non-positive values disable the corresponding
// limit, so newHostLimiter(0, 0, 0) is a no-op limiter.
func newHostLimiter(ratePerSecond, burst float64, perHostConcurrency int) *hostLimiter {
	if burst <= 0 {
		burst = 1
	}
	return &hostLimiter{
		hosts:              make(map[string]*hostState),
		ratePerSecond:      ratePerSecond,
		burst:              burst,
		perHostConcurrency: perHostConcurrency,
		now:                time.Now,
	}
}

// acquire waits for a request slot on host and returns the function that
// releases it. It returns an error only when ctx is done while waiting: a
// cancelled run must not keep queueing for a busy host.
func (l *hostLimiter) acquire(ctx context.Context, host string) (func(), error) {
	release := func() {}
	if l == nil || (l.ratePerSecond <= 0 && l.perHostConcurrency <= 0) {
		return release, nil
	}
	host = strings.ToLower(strings.TrimSpace(host))

	for {
		l.mu.Lock()
		state := l.stateFor(host)
		wait := l.reserve(state)
		if wait <= 0 {
			l.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					l.mu.Lock()
					defer l.mu.Unlock()
					state.inFlight--
				})
			}, nil
		}
		l.mu.Unlock()

		if wait < limiterPollInterval {
			wait = limiterPollInterval
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// stateFor returns the host's state, creating it and pruning idle hosts when the
// map grows past maxTrackedHosts. Callers must hold l.mu.
func (l *hostLimiter) stateFor(host string) *hostState {
	if state, exists := l.hosts[host]; exists {
		return state
	}
	if len(l.hosts) >= maxTrackedHosts {
		l.pruneIdleLocked()
	}
	state := &hostState{tokens: l.burst, lastRefill: l.now()}
	l.hosts[host] = state
	return state
}

// pruneIdleLocked drops hosts that are not in flight and hold a full bucket.
// Callers must hold l.mu.
func (l *hostLimiter) pruneIdleLocked() {
	for host, state := range l.hosts {
		if state.inFlight == 0 && state.tokens >= l.burst {
			delete(l.hosts, host)
		}
	}
}

// reserve refills the bucket and takes a slot when one is available. It returns
// how long the caller should wait before retrying, or 0 on success. Callers must
// hold l.mu.
func (l *hostLimiter) reserve(state *hostState) time.Duration {
	if l.ratePerSecond > 0 {
		elapsed := l.now().Sub(state.lastRefill).Seconds()
		if elapsed > 0 {
			state.tokens += elapsed * l.ratePerSecond
			if state.tokens > l.burst {
				state.tokens = l.burst
			}
			state.lastRefill = l.now()
		}
	}
	if l.perHostConcurrency > 0 && state.inFlight >= l.perHostConcurrency {
		// A busy host is retried on a short poll rather than queued, which keeps
		// a slow page from delaying the fetch of a different host.
		return limiterPollInterval
	}
	if l.ratePerSecond > 0 && state.tokens < 1 {
		missing := 1 - state.tokens
		return time.Duration(missing / l.ratePerSecond * float64(time.Second))
	}
	if l.ratePerSecond > 0 {
		state.tokens--
	}
	state.inFlight++
	return 0
}

// hostOf extracts the hostname a limiter keys on. An unparseable URL is grouped
// under its own empty key, which still bounds concurrency for it.
func hostOf(targetURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
