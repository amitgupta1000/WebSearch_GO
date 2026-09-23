package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
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
	// AllowProfileScraping permits fetching professional-profile pages directly.
	// Off by default: such URLs normally contribute only their public search snippet.
	AllowProfileScraping bool
	CacheMaxEntries      int
	// CacheBackend selects where cached searches and scraped pages live:
	// "memory" (process-local, the default), "redis", or "gcs".
	CacheBackend string
	// RedisURL is a redis:// or rediss:// connection URL, used when
	// CacheBackend is "redis". When it is unset, the URL is assembled from
	// RedisHost, RedisPort and RedisPassword instead.
	RedisURL string
	// RedisHost, RedisPort and RedisPassword are the split connection details, for
	// deployments that inject credentials separately (REDIS_HOST / REDIS_PORT /
	// REDIS_PASSWORD).
	RedisHost     string
	RedisPort     int
	RedisPassword string
	// RedisKeyPrefix namespaces every key so a shared Redis database stays clean.
	RedisKeyPrefix string
	// GCSCacheBucket is the bucket used when CacheBackend is "gcs".
	GCSCacheBucket string
	// GCSCachePrefix is the object-name prefix inside that bucket.
	GCSCachePrefix string
	// OutputBucket is the Cloud Storage bucket finished runs are written to as
	// Markdown and PDF artifacts. Empty disables the cloud destination.
	OutputBucket string
	// OutputPrefix is the object prefix inside that bucket (the "folder" the
	// artifacts land in).
	OutputPrefix string
	// OutputLocalDir writes artifacts to a local directory instead of a bucket.
	// When set it takes precedence, because setting it is an explicit request for
	// files on disk.
	OutputLocalDir string
	// OutputFormats lists the artifact formats to write: "md" and/or "pdf".
	OutputFormats []string
	// GoogleCredentialsFile mirrors GOOGLE_APPLICATION_CREDENTIALS, the service
	// account file the Google client libraries pick up automatically. It is read
	// so startup can report which credential source is in use.
	GoogleCredentialsFile string
	ResearchTimeout       time.Duration
	// BrowserResearchTimeout is the research budget when a run renders pages in a
	// real browser, which costs several seconds per scraped page.
	BrowserResearchTimeout time.Duration
	MaxScrapesPerRun       int
	MaxSearchResults       int
	MaxSteps               int
	// ScrapeConcurrency bounds how many pages are fetched at once.
	ScrapeConcurrency int
	// ScrapeHostRate, ScrapeHostBurst and ScrapeHostConcurrency are the per-host
	// politeness budget applied to every fetch.
	ScrapeHostRate        float64
	ScrapeHostBurst       float64
	ScrapeHostConcurrency int
	// ScrapeRevalidateAfter is how long a cached page is trusted before a
	// conditional request checks whether it changed. Zero disables the check.
	ScrapeRevalidateAfter time.Duration
	// MaxRetryAfter caps how long a server's Retry-After hint may delay a retry.
	MaxRetryAfter time.Duration
	// HardURLHandler enables the bounded hard-URL escalation ladder for
	// challenge pages and professional profiles. Failures degrade to the
	// previous snippet-only behaviour, so the default is on.
	HardURLHandler bool
	// HardURLsPerRun caps how many URLs one run may escalate. The ladder's other
	// caps (per-step timeouts) are code constants, so the budget is derived from
	// them when HardURLBudget is zero.
	HardURLsPerRun int
	// HardURLBudget bounds the whole ladder for one URL. Zero derives it from the
	// ladder's own step caps.
	HardURLBudget time.Duration
}

const (
	defaultCacheBackend   = "memory"
	defaultRedisKeyPrefix = "websearch-go:"
	defaultRedisPort      = 6379
	// defaultGCSCacheBucket and defaultOutputBucket are the project's bucket: the
	// cache and the run artifacts share it, in separate folders. They are defaults
	// rather than requirements, so a deployment can point anywhere else.
	defaultGCSCacheBucket = "crystal-crm-store"
	defaultGCSCachePrefix = "WebSearch_GO/cache/"
	defaultOutputBucket   = "crystal-crm-store"
	defaultOutputPrefix   = "WebSearch_GO/"
	defaultOutputFormats  = "md,pdf"
	// defaultHardURLsPerRun mirrors the agent's own default for the hard-URL ladder.
	defaultHardURLsPerRun        = 2
	defaultScrapeConcurrency     = 4
	defaultScrapeHostRate        = 2
	defaultScrapeHostBurst       = 3
	defaultScrapeHostConcurrency = 2
	// defaultScrapeRevalidateAfter is how long a cached page is served without a
	// freshness check.
	defaultScrapeRevalidateAfter = 24 * time.Hour
	// defaultMaxRetryAfter caps a server's Retry-After hint so one unfriendly
	// host cannot consume a run's whole budget.
	defaultMaxRetryAfter = 5 * time.Second
)

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

	redisPort := envInt("REDIS_PORT", defaultRedisPort)
	if redisPort > 65535 {
		// A port outside the valid range cannot be honoured; the default is the
		// same behaviour envInt applies to any other unusable value.
		redisPort = defaultRedisPort
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
		AllowProfileScraping:   envBool("ALLOW_PROFILE_SCRAPING", false),
		CacheMaxEntries:        envInt("CACHE_MAX_ENTRIES", 2048),
		CacheBackend:           envString("CACHE_BACKEND", defaultCacheBackend),
		RedisURL:               os.Getenv("REDIS_URL"),
		RedisHost:              os.Getenv("REDIS_HOST"),
		RedisPort:              redisPort,
		RedisPassword:          os.Getenv("REDIS_PASSWORD"),
		RedisKeyPrefix:         envString("REDIS_KEY_PREFIX", defaultRedisKeyPrefix),
		GCSCacheBucket:         envString("GCS_CACHE_BUCKET", defaultGCSCacheBucket),
		GCSCachePrefix:         envString("GCS_CACHE_PREFIX", defaultGCSCachePrefix),
		OutputBucket:           envString("OUTPUT_BUCKET", defaultOutputBucket),
		OutputPrefix:           envString("OUTPUT_PREFIX", defaultOutputPrefix),
		OutputLocalDir:         envString("OUTPUT_LOCAL_DIR", ""),
		OutputFormats:          envList("OUTPUT_FORMATS", defaultOutputFormats),
		GoogleCredentialsFile:  os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
		ResearchTimeout:        researchTimeout,
		BrowserResearchTimeout: browserResearchTimeout,
		MaxScrapesPerRun:       envInt("MAX_SCRAPES_PER_RUN", 10),
		MaxSearchResults:       envInt("MAX_SEARCH_RESULTS", 12),
		MaxSteps:               envInt("MAX_AGENT_STEPS", 10),
		ScrapeConcurrency:      envInt("SCRAPE_CONCURRENCY", defaultScrapeConcurrency),
		ScrapeHostRate:         envFloat("SCRAPE_HOST_RATE_PER_SEC", defaultScrapeHostRate),
		ScrapeHostBurst:        envFloat("SCRAPE_HOST_BURST", defaultScrapeHostBurst),
		ScrapeHostConcurrency:  envInt("SCRAPE_HOST_CONCURRENCY", defaultScrapeHostConcurrency),
		ScrapeRevalidateAfter: time.Duration(envInt("SCRAPE_REVALIDATE_AFTER_SECONDS",
			int(defaultScrapeRevalidateAfter.Seconds()))) * time.Second,
		MaxRetryAfter: time.Duration(envInt("SCRAPE_MAX_RETRY_AFTER_SECONDS",
			int(defaultMaxRetryAfter.Seconds()))) * time.Second,
		HardURLHandler: envBool("HARD_URL_HANDLER", true),
		HardURLsPerRun: envInt("HARD_URLS_PER_RUN", defaultHardURLsPerRun),
		HardURLBudget:  time.Duration(envInt("HARD_URL_BUDGET_SECONDS", 0)) * time.Second,
	}
}

// CacheBackendName reports the configured cache backend in lower case, defaulting
// to "memory" so an unset or misspelled value cannot disable caching entirely.
func (c *Config) CacheBackendName() string {
	if c == nil {
		return defaultCacheBackend
	}
	backend := strings.ToLower(strings.TrimSpace(c.CacheBackend))
	if backend == "" {
		return defaultCacheBackend
	}
	return backend
}

// RedisConnectionURL returns the Redis URL to connect to: REDIS_URL exactly as it
// was supplied, or one assembled from REDIS_HOST, REDIS_PORT and REDIS_PASSWORD.
//
// It returns "" when no Redis connection information was provided at all, which
// the server reports before falling back to the in-memory cache.
func (c *Config) RedisConnectionURL() string {
	if c == nil {
		return ""
	}
	if configured := strings.TrimSpace(c.RedisURL); configured != "" {
		// A full URL wins: it can also carry a username, a database number and TLS.
		return configured
	}
	host := strings.TrimSpace(c.RedisHost)
	if host == "" {
		return ""
	}
	port := c.RedisPort
	if port <= 0 {
		port = defaultRedisPort
	}
	// UserPassword escapes anything that would otherwise break the URL, so a
	// password containing '@' or ':' still produces a valid connection string.
	credentials := ""
	if c.RedisPassword != "" {
		credentials = url.UserPassword("", c.RedisPassword).String() + "@"
	}
	// An IPv6 literal has to be bracketed before it can be used in a URL.
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("redis://%s%s:%d", credentials, host, port)
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

// envString reads a string environment variable, falling back to def when it is
// unset or blank.
func envString(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// envFloat reads a non-negative float from an environment variable, falling back
// to def when the variable is unset, unparseable, or negative.
func envFloat(name string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	parsed, err := strconv.ParseFloat(v, 64)
	if err != nil || parsed < 0 {
		return def
	}
	return parsed
}

// envList reads a comma-separated environment variable, falling back to
// defaultCSV when it is unset. Blank entries are dropped, so a trailing comma is
// harmless.
func envList(name, defaultCSV string) []string {
	parts := strings.Split(envString(name, defaultCSV), ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			values = append(values, part)
		}
	}
	return values
}

// envBool reads a boolean environment variable, falling back to def when it is
// unset or not a valid boolean.
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
