package main

import (
	"time"

	"github.com/amitgupta1000/WebSearch_GO/internal/tools"
)

// The on-demand "Fetch RR" action is a paid lookup. The profile it returns is stable enough to
// reuse for a working day, so the server keeps a small in-memory cache keyed by RocketReach's
// profile ID. The cache is deliberately process-local: it is an optimisation for repeated
// clicks and page reloads, not a durable store of contact data.
const (
	rocketReachProfileTTL = 24 * time.Hour
	rocketReachCacheLimit = 128
)

type rocketReachCacheEntry struct {
	Profile  *tools.RocketReachProfile
	CachedAt time.Time
}

// cachedRocketReachProfile returns an unexpired profile from the process cache. A missing or
// expired entry is a miss, never an error: the handler simply calls RocketReach again.
func (s *Server) cachedRocketReachProfile(id int) (*tools.RocketReachProfile, bool) {
	if s == nil || id <= 0 {
		return nil, false
	}
	s.rocketReachMu.Lock()
	defer s.rocketReachMu.Unlock()
	entry, ok := s.rocketReachCache[id]
	if !ok {
		return nil, false
	}
	if time.Since(entry.CachedAt) > rocketReachProfileTTL {
		delete(s.rocketReachCache, id)
		return nil, false
	}
	return entry.Profile, true
}

// rememberRocketReachProfile caches a profile by its RocketReach ID. An empty profile or one
// without an ID is not cached; the cache is bounded, and expired entries are swept first so a
// long-lived instance cannot grow it without limit.
func (s *Server) rememberRocketReachProfile(profile *tools.RocketReachProfile) {
	if s == nil || profile == nil || profile.ID <= 0 {
		return
	}
	s.rocketReachMu.Lock()
	defer s.rocketReachMu.Unlock()
	if s.rocketReachCache == nil {
		s.rocketReachCache = make(map[int]rocketReachCacheEntry)
	}
	now := time.Now()
	for id, entry := range s.rocketReachCache {
		if now.Sub(entry.CachedAt) > rocketReachProfileTTL {
			delete(s.rocketReachCache, id)
		}
	}
	if len(s.rocketReachCache) >= rocketReachCacheLimit {
		for id := range s.rocketReachCache {
			delete(s.rocketReachCache, id)
			break
		}
	}
	s.rocketReachCache[profile.ID] = rocketReachCacheEntry{Profile: profile, CachedAt: now}
}
