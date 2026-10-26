package tools

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// robotsGuard caches one robots.txt and answers whether a path may be fetched.
//
// It exists for the legal sources: indiankanoon.org's robots.txt carries a long
// list of individually disallowed /doc/ paths — case-removal orders, where a
// court or the site has taken a document down. Fetching those anyway would be
// both rude and wrong, so the legal client checks this guard before every
// document fetch and reports a refusal by name.
//
// The guard honours the User-Agent: * group (the rules that apply to this
// process), loads once, and fails open: if robots.txt cannot be read, paths are
// allowed rather than the mode being silently blinded. Search paths are not
// part of any disallow list; the guard exists for documents.
type robotsGuard struct {
	robotsURL string
	client    *http.Client

	mu         sync.Mutex
	loaded     bool
	disallowed map[string]bool
}

// newRobotsGuard builds a guard for one robots.txt URL. The fetch happens on
// first use, so constructing the guard never blocks startup.
func newRobotsGuard(robotsURL string) *robotsGuard {
	return &robotsGuard{
		robotsURL: robotsURL,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

// allows reports whether path may be fetched. The path is matched as the exact
// string the robots.txt disallowed — IndianKanoon disallows whole paths like
// "/doc/1745798/", which is precisely how document URLs are formed.
func (g *robotsGuard) allows(path string) bool {
	g.load()
	normalized := strings.TrimSuffix(path, "/") + "/"
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.disallowed[path] && !g.disallowed[normalized]
}

// load fetches and parses robots.txt once. A failed fetch leaves the guard
// empty, which means "allow" — the documented fail-open.
func (g *robotsGuard) load() {
	g.mu.Lock()
	if g.loaded {
		g.mu.Unlock()
		return
	}
	g.loaded = true
	g.mu.Unlock()

	disallowed := map[string]bool{}
	req, err := http.NewRequest(http.MethodGet, g.robotsURL, nil)
	if err != nil {
		return
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return
	}

	// Parse the User-Agent: * group only: those rules apply to any client, and
	// this client's name is not in any specific group.
	groupIsStar := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lowered := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lowered, "user-agent:"):
			groupIsStar = strings.TrimSpace(line[len("user-agent:"):]) == "*"
		case strings.HasPrefix(lowered, "disallow:"):
			if !groupIsStar {
				continue
			}
			prefix := strings.TrimSpace(line[len("disallow:"):])
			if prefix != "" {
				disallowed[strings.TrimSuffix(prefix, "/")+"/"] = true
			}
		}
	}

	g.mu.Lock()
	g.disallowed = disallowed
	g.mu.Unlock()
}
