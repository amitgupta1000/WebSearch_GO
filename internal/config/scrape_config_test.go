package config

import (
	"testing"
	"time"
)

func TestScrapeAndCacheDefaults(t *testing.T) {
	cfg := Load()

	if got := cfg.CacheBackendName(); got != "redis" {
		t.Errorf("CacheBackendName() = %q, want %q", got, "redis")
	}
	if cfg.ScrapeConcurrency != defaultScrapeConcurrency {
		t.Errorf("ScrapeConcurrency = %d, want %d", cfg.ScrapeConcurrency, defaultScrapeConcurrency)
	}
	if cfg.ScrapeHostRate != defaultScrapeHostRate {
		t.Errorf("ScrapeHostRate = %v, want %v", cfg.ScrapeHostRate, defaultScrapeHostRate)
	}
	if cfg.ScrapeHostBurst != defaultScrapeHostBurst {
		t.Errorf("ScrapeHostBurst = %v, want %v", cfg.ScrapeHostBurst, defaultScrapeHostBurst)
	}
	if cfg.ScrapeHostConcurrency != defaultScrapeHostConcurrency {
		t.Errorf("ScrapeHostConcurrency = %d, want %d", cfg.ScrapeHostConcurrency, defaultScrapeHostConcurrency)
	}
	if cfg.ScrapeRevalidateAfter != defaultScrapeRevalidateAfter {
		t.Errorf("ScrapeRevalidateAfter = %v, want %v", cfg.ScrapeRevalidateAfter, defaultScrapeRevalidateAfter)
	}
	if cfg.MaxRetryAfter != defaultMaxRetryAfter {
		t.Errorf("MaxRetryAfter = %v, want %v", cfg.MaxRetryAfter, defaultMaxRetryAfter)
	}
	if cfg.RedisKeyPrefix != defaultRedisKeyPrefix {
		t.Errorf("RedisKeyPrefix = %q, want %q", cfg.RedisKeyPrefix, defaultRedisKeyPrefix)
	}
	if cfg.GCSCachePrefix != defaultGCSCachePrefix {
		t.Errorf("GCSCachePrefix = %q, want %q", cfg.GCSCachePrefix, defaultGCSCachePrefix)
	}
}

func TestLoadReadsCacheAndScrapeSettings(t *testing.T) {
	t.Setenv("CACHE_BACKEND", "Redis")
	t.Setenv("REDIS_URL", "redis://cache.internal:6379")
	t.Setenv("REDIS_KEY_PREFIX", "team:")
	t.Setenv("GCS_CACHE_BUCKET", "research-cache")
	t.Setenv("SCRAPE_CONCURRENCY", "8")
	t.Setenv("SCRAPE_HOST_RATE_PER_SEC", "0.5")
	t.Setenv("SCRAPE_HOST_BURST", "5")
	t.Setenv("SCRAPE_HOST_CONCURRENCY", "3")
	t.Setenv("SCRAPE_REVALIDATE_AFTER_SECONDS", "3600")
	t.Setenv("SCRAPE_MAX_RETRY_AFTER_SECONDS", "30")

	cfg := Load()

	// The backend name is lower-cased so configuration is case-insensitive.
	if got := cfg.CacheBackendName(); got != "redis" {
		t.Errorf("CacheBackendName() = %q, want %q", got, "redis")
	}
	if cfg.RedisURL != "redis://cache.internal:6379" {
		t.Errorf("RedisURL = %q", cfg.RedisURL)
	}
	if cfg.RedisKeyPrefix != "team:" {
		t.Errorf("RedisKeyPrefix = %q, want %q", cfg.RedisKeyPrefix, "team:")
	}
	if cfg.GCSCacheBucket != "research-cache" {
		t.Errorf("GCSCacheBucket = %q", cfg.GCSCacheBucket)
	}
	if cfg.ScrapeConcurrency != 8 {
		t.Errorf("ScrapeConcurrency = %d, want 8", cfg.ScrapeConcurrency)
	}
	if cfg.ScrapeHostRate != 0.5 {
		t.Errorf("ScrapeHostRate = %v, want 0.5", cfg.ScrapeHostRate)
	}
	if cfg.ScrapeHostBurst != 5 {
		t.Errorf("ScrapeHostBurst = %v, want 5", cfg.ScrapeHostBurst)
	}
	if cfg.ScrapeHostConcurrency != 3 {
		t.Errorf("ScrapeHostConcurrency = %d, want 3", cfg.ScrapeHostConcurrency)
	}
	if cfg.ScrapeRevalidateAfter != time.Hour {
		t.Errorf("ScrapeRevalidateAfter = %v, want 1h", cfg.ScrapeRevalidateAfter)
	}
	if cfg.MaxRetryAfter != 30*time.Second {
		t.Errorf("MaxRetryAfter = %v, want 30s", cfg.MaxRetryAfter)
	}
}

func TestInvalidScrapeSettingsFallBackToDefaults(t *testing.T) {
	t.Setenv("SCRAPE_HOST_RATE_PER_SEC", "not-a-number")
	if got := Load().ScrapeHostRate; got != defaultScrapeHostRate {
		t.Errorf("ScrapeHostRate = %v, want the default %v", got, defaultScrapeHostRate)
	}

	t.Setenv("SCRAPE_HOST_RATE_PER_SEC", "-4")
	if got := Load().ScrapeHostRate; got != defaultScrapeHostRate {
		t.Errorf("a negative rate must fall back to the default, got %v", got)
	}

	t.Setenv("SCRAPE_CONCURRENCY", "0")
	if got := Load().ScrapeConcurrency; got != defaultScrapeConcurrency {
		t.Errorf("a zero concurrency must fall back to the default, got %d", got)
	}
}

// TestDocumentExtractTimeoutDefaultsAndOverrides covers the guardrail knob: a document's
// extraction is bounded by default, and an operator can tighten or loosen it.
func TestDocumentExtractTimeoutDefaultsAndOverrides(t *testing.T) {
	if got := Load().DocumentExtractTimeout; got != defaultDocumentExtractTimeout {
		t.Errorf("DocumentExtractTimeout = %v, want %v", got, defaultDocumentExtractTimeout)
	}
	t.Setenv("DOCUMENT_EXTRACT_TIMEOUT_SECONDS", "15")
	if got := Load().DocumentExtractTimeout; got != 15*time.Second {
		t.Errorf("DocumentExtractTimeout = %v, want 15s", got)
	}
}

func TestHardURLLimitDefaultsAndOverrides(t *testing.T) {
	cfg := Load()

	if !cfg.HardURLHandler {
		t.Error("expected the hard-URL ladder to be on by default")
	}
	if cfg.HardURLsPerRun != defaultHardURLsPerRun {
		t.Errorf("HardURLsPerRun = %d, want %d", cfg.HardURLsPerRun, defaultHardURLsPerRun)
	}
	// Zero means "derive it from the ladder's own step caps", so raising a step
	// timeout can never leave the budget shorter than the ladder needs.
	if cfg.HardURLBudget != 0 {
		t.Errorf("HardURLBudget = %v, want 0 (derived)", cfg.HardURLBudget)
	}

	t.Setenv("HARD_URL_HANDLER", "0")
	t.Setenv("HARD_URLS_PER_RUN", "4")
	t.Setenv("HARD_URL_BUDGET_SECONDS", "40")

	configured := Load()
	if configured.HardURLHandler {
		t.Error("expected HARD_URL_HANDLER=0 to disable the ladder")
	}
	if configured.HardURLsPerRun != 4 {
		t.Errorf("HardURLsPerRun = %d, want the configured 4", configured.HardURLsPerRun)
	}
	if configured.HardURLBudget != 40*time.Second {
		t.Errorf("HardURLBudget = %v, want the configured 40s", configured.HardURLBudget)
	}
}

func TestBlankCacheBackendFallsBackToMemory(t *testing.T) {
	t.Setenv("CACHE_BACKEND", "   ")
	if got := Load().CacheBackendName(); got != "redis" {
		t.Errorf("CacheBackendName() = %q, want %q", got, "redis")
	}
}

func TestRedisConnectionURLAssembledFromParts(t *testing.T) {
	// Deployments commonly inject the pieces rather than a URL, so a password
	// containing URL metacharacters must still produce a usable connection string.
	t.Setenv("REDIS_URL", "")
	t.Setenv("REDIS_HOST", "cache.internal")
	t.Setenv("REDIS_PORT", "6380")
	t.Setenv("REDIS_PASSWORD", "p@ss:word")

	cfg := Load()
	if cfg.RedisHost != "cache.internal" || cfg.RedisPort != 6380 || cfg.RedisPassword != "p@ss:word" {
		t.Fatalf("split Redis settings were not read: host=%q port=%d password=%q",
			cfg.RedisHost, cfg.RedisPort, cfg.RedisPassword)
	}

	want := "redis://:p%40ss%3Aword@cache.internal:6380"
	if got := cfg.RedisConnectionURL(); got != want {
		t.Errorf("RedisConnectionURL() = %q, want %q", got, want)
	}
}

func TestRedisConnectionURLDefaultsThePort(t *testing.T) {
	// Clear the other inputs so an ambient deployment environment cannot change
	// what this asserts.
	t.Setenv("REDIS_URL", "")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_PORT", "")
	t.Setenv("REDIS_HOST", "localhost")
	if got := Load().RedisConnectionURL(); got != "redis://localhost:6379" {
		t.Errorf("RedisConnectionURL() = %q, want %q", got, "redis://localhost:6379")
	}
}

func TestRedisConnectionURLPrefersAnExplicitURL(t *testing.T) {
	// A full URL can also carry a username, a database index and TLS, so it wins.
	t.Setenv("REDIS_HOST", "ignored.internal")
	t.Setenv("REDIS_URL", "rediss://user:secret@example.com:1234/2")
	if got := Load().RedisConnectionURL(); got != "rediss://user:secret@example.com:1234/2" {
		t.Errorf("RedisConnectionURL() = %q, want the explicit REDIS_URL", got)
	}
}

func TestRedisConnectionURLEmptyWithoutConfiguration(t *testing.T) {
	t.Setenv("REDIS_URL", "")
	t.Setenv("REDIS_HOST", "")
	t.Setenv("REDIS_PORT", "")
	t.Setenv("REDIS_PASSWORD", "")
	if got := Load().RedisConnectionURL(); got != "" {
		t.Errorf("RedisConnectionURL() = %q, want an empty string", got)
	}
}

func TestRedisConnectionURLBracketsIPv6Hosts(t *testing.T) {
	t.Setenv("REDIS_URL", "")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_PORT", "")
	t.Setenv("REDIS_HOST", "2001:db8::1")
	if got := Load().RedisConnectionURL(); got != "redis://[2001:db8::1]:6379" {
		t.Errorf("RedisConnectionURL() = %q, want the IPv6 host bracketed", got)
	}
}

func TestRedisPortOutsideRangeFallsBackToDefault(t *testing.T) {
	t.Setenv("REDIS_HOST", "localhost")
	t.Setenv("REDIS_PORT", "70000")
	if got := Load().RedisPort; got != defaultRedisPort {
		t.Errorf("RedisPort = %d, want the default %d", got, defaultRedisPort)
	}
}

func TestGoogleCredentialsFileIsRead(t *testing.T) {
	// The Google client libraries honour this variable themselves; reading it is
	// for the startup log, so an operator can see which identity is in use.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "C:\\secrets\\cache-sa.json")
	if got := Load().GoogleCredentialsFile; got != "C:\\secrets\\cache-sa.json" {
		t.Errorf("GoogleCredentialsFile = %q", got)
	}
}

func TestOutputArtifactDefaults(t *testing.T) {
	cfg := Load()

	// The project's bucket and folder are the defaults, so run artifacts work
	// without further configuration.
	if cfg.OutputBucket != defaultOutputBucket {
		t.Errorf("OutputBucket = %q, want %q", cfg.OutputBucket, defaultOutputBucket)
	}
	if cfg.OutputPrefix != defaultOutputPrefix {
		t.Errorf("OutputPrefix = %q, want %q", cfg.OutputPrefix, defaultOutputPrefix)
	}
	if cfg.GCSCacheBucket != defaultGCSCacheBucket {
		t.Errorf("GCSCacheBucket = %q, want %q", cfg.GCSCacheBucket, defaultGCSCacheBucket)
	}
	if cfg.GCSCachePrefix != defaultGCSCachePrefix {
		t.Errorf("GCSCachePrefix = %q, want %q", cfg.GCSCachePrefix, defaultGCSCachePrefix)
	}
	if len(cfg.OutputFormats) != 2 || cfg.OutputFormats[0] != "md" || cfg.OutputFormats[1] != "pdf" {
		t.Errorf("OutputFormats = %v, want [md pdf]", cfg.OutputFormats)
	}
	if cfg.OutputLocalDir != "" {
		t.Errorf("OutputLocalDir = %q, want empty so the bucket is used", cfg.OutputLocalDir)
	}
}

func TestOutputArtifactSettingsAreOverridable(t *testing.T) {
	t.Setenv("OUTPUT_BUCKET", "another-bucket")
	t.Setenv("OUTPUT_PREFIX", "reports/2026/")
	t.Setenv("OUTPUT_LOCAL_DIR", "C:\\exports")
	t.Setenv("OUTPUT_FORMATS", " md , ")
	t.Setenv("GCS_CACHE_BUCKET", "cache-bucket")
	t.Setenv("GCS_CACHE_PREFIX", "cache/")

	cfg := Load()

	if cfg.OutputBucket != "another-bucket" || cfg.OutputPrefix != "reports/2026/" {
		t.Errorf("output settings = %q / %q", cfg.OutputBucket, cfg.OutputPrefix)
	}
	if cfg.OutputLocalDir != "C:\\exports" {
		t.Errorf("OutputLocalDir = %q", cfg.OutputLocalDir)
	}
	// Blank entries are dropped, so trailing separators are harmless.
	if len(cfg.OutputFormats) != 1 || cfg.OutputFormats[0] != "md" {
		t.Errorf("OutputFormats = %v, want [md]", cfg.OutputFormats)
	}
	if cfg.GCSCacheBucket != "cache-bucket" || cfg.GCSCachePrefix != "cache/" {
		t.Errorf("cache settings = %q / %q", cfg.GCSCacheBucket, cfg.GCSCachePrefix)
	}
}

func TestValidationSettingsDefaultsAndOverrides(t *testing.T) {
	cfg := Load()
	if !cfg.ValidateEvidence {
		t.Error("validation should be on by default: the offline half costs nothing")
	}
	if cfg.ValidationLookups != defaultValidationLookups {
		t.Errorf("ValidationLookups = %d, want %d", cfg.ValidationLookups, defaultValidationLookups)
	}
	if cfg.ValidationStaleMonths != defaultValidationStaleMonths {
		t.Errorf("ValidationStaleMonths = %d, want %d", cfg.ValidationStaleMonths, defaultValidationStaleMonths)
	}
	if cfg.ValidationRecentMonths != defaultValidationRecentMonths {
		t.Errorf("ValidationRecentMonths = %d, want %d", cfg.ValidationRecentMonths, defaultValidationRecentMonths)
	}

	t.Setenv("VALIDATE_EVIDENCE", "0")
	t.Setenv("VALIDATION_LOOKUPS_PER_RUN", "7")
	t.Setenv("VALIDATION_STALE_AFTER_MONTHS", "48")
	t.Setenv("VALIDATION_RECENT_WITHIN_MONTHS", "12")

	overridden := Load()
	if overridden.ValidateEvidence {
		t.Error("VALIDATE_EVIDENCE=0 must disable the pass")
	}
	if overridden.ValidationLookups != 7 {
		t.Errorf("ValidationLookups = %d, want 7", overridden.ValidationLookups)
	}
	if overridden.ValidationStaleMonths != 48 || overridden.ValidationRecentMonths != 12 {
		t.Errorf("windows = %d/%d, want 48/12", overridden.ValidationStaleMonths, overridden.ValidationRecentMonths)
	}
}
