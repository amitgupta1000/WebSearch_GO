package config

import (
	"os"
	"strconv"
	"time"
)

// Config stores application runtime settings and API credentials.
type Config struct {
	Port                  string
	GCPProjectID          string
	GCPRegion             string
	SerperAPIKey          string
	GeminiModel           string
	CookieFile            string
	EnableBrowserFallback bool
	AllowBrowserToggle    bool
	CacheMaxEntries       int
	ResearchTimeout       time.Duration
	// BrowserResearchTimeout is the research budget when a run renders pages in a
	// real browser, which costs several seconds per scraped page.
	BrowserResearchTimeout time.Duration
	MaxScrapesPerRun       int
	MaxSearchResults       int
	MaxSteps               int
	// HardURLHandler enables the bounded hard-URL escalation ladder for
	// challenge pages and professional profiles. Failures degrade to the
	// previous snippet-only behaviour, so the default is on.
	HardURLHandler bool
}

// Load loads configuration from environment variables with sensible defaults.
func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-2.5-flash-lite"
	}

	region := os.Getenv("GCP_REGION")
	if region == "" {
		region = "us-central1"
	}

	researchTimeout := time.Duration(envInt("RESEARCH_TIMEOUT_SECONDS", 180)) * time.Second
	browserResearchTimeout := time.Duration(envInt("BROWSER_RESEARCH_TIMEOUT_SECONDS", 300)) * time.Second
	if browserResearchTimeout < researchTimeout {
		// Opting into rendering must never leave a run less time than the default.
		browserResearchTimeout = researchTimeout
	}

	return &Config{
		Port:                  port,
		GCPProjectID:          os.Getenv("GOOGLE_CLOUD_PROJECT"),
		GCPRegion:             region,
		SerperAPIKey:          os.Getenv("SERPER_API_KEY"),
		GeminiModel:           model,
		CookieFile:            os.Getenv("COOKIE_FILE_PATH"),
		EnableBrowserFallback: envBool("ENABLE_BROWSER_FALLBACK", false),
		// AllowBrowserToggle lets the console offer a per-run "render with a real
		// browser" option, which is only shown when Chromium is actually runnable.
		AllowBrowserToggle:     envBool("ALLOW_BROWSER_TOGGLE", true),
		CacheMaxEntries:        envInt("CACHE_MAX_ENTRIES", 2048),
		ResearchTimeout:        researchTimeout,
		BrowserResearchTimeout: browserResearchTimeout,
		MaxScrapesPerRun:       envInt("MAX_SCRAPES_PER_RUN", 10),
		MaxSearchResults:       envInt("MAX_SEARCH_RESULTS", 12),
		MaxSteps:               envInt("MAX_AGENT_STEPS", 10),
		HardURLHandler:         envBool("HARD_URL_HANDLER", true),
	}
}

// ResearchTimeoutFor returns the research budget for a run. Rendered runs get the
// larger budget because each scraped page costs several extra seconds.
func (c *Config) ResearchTimeoutFor(browserRendering bool) time.Duration {
	if browserRendering && c.BrowserResearchTimeout > 0 {
		return c.BrowserResearchTimeout
	}
	return c.ResearchTimeout
}

// MaxResearchTimeout is the largest per-request research budget. It sizes the HTTP
// server's write timeout so a long research stream is never cut off mid-run.
func (c *Config) MaxResearchTimeout() time.Duration {
	if c.BrowserResearchTimeout > c.ResearchTimeout {
		return c.BrowserResearchTimeout
	}
	return c.ResearchTimeout
}

func envBool(name string, def bool) bool {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return parsed
}

// envInt reads a positive integer from an environment variable, falling back to
// def when the variable is unset or not a valid positive integer.
func envInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
