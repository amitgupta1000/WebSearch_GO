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
	CacheMaxEntries       int
	ResearchTimeout       time.Duration
	MaxScrapesPerRun      int
	MaxSearchResults      int
	MaxSteps              int
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

	return &Config{
		Port:                  port,
		GCPProjectID:          os.Getenv("GOOGLE_CLOUD_PROJECT"),
		GCPRegion:             region,
		SerperAPIKey:          os.Getenv("SERPER_API_KEY"),
		GeminiModel:           model,
		CookieFile:            os.Getenv("COOKIE_FILE_PATH"),
		EnableBrowserFallback: envBool("ENABLE_BROWSER_FALLBACK", false),
		CacheMaxEntries:       envInt("CACHE_MAX_ENTRIES", 2048),
		ResearchTimeout:       time.Duration(envInt("RESEARCH_TIMEOUT_SECONDS", 180)) * time.Second,
		MaxScrapesPerRun:      envInt("MAX_SCRAPES_PER_RUN", 10),
		MaxSearchResults:      envInt("MAX_SEARCH_RESULTS", 12),
		MaxSteps:              envInt("MAX_AGENT_STEPS", 10),
	}
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
