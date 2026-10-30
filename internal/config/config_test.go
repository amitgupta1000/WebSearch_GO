package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Clear env
	os.Unsetenv("PORT")
	os.Unsetenv("GEMINI_MODEL")
	os.Unsetenv("GCP_REGION")
	os.Unsetenv("CACHE_MAX_ENTRIES")
	os.Unsetenv("RESEARCH_TIMEOUT_SECONDS")
	os.Unsetenv("MAX_SCRAPES_PER_RUN")
	os.Unsetenv("MAX_SEARCH_RESULTS")
	os.Unsetenv("MAX_AGENT_STEPS")
	os.Unsetenv("ENABLE_BROWSER_FALLBACK")

	cfg := Load()

	if cfg.Port != "8080" {
		t.Errorf("expected default port 8080, got %s", cfg.Port)
	}
	if cfg.GeminiModel != "gemini-2.5-flash-lite" {
		t.Errorf("expected default model gemini-2.5-flash-lite, got %s", cfg.GeminiModel)
	}
	if cfg.GCPRegion != "us-central1" {
		t.Errorf("expected default region us-central1, got %s", cfg.GCPRegion)
	}
	if cfg.CacheMaxEntries != 2048 {
		t.Errorf("expected default cache entries 2048, got %d", cfg.CacheMaxEntries)
	}
	if cfg.ResearchTimeout != 180*time.Second {
		t.Errorf("expected default timeout 180s, got %v", cfg.ResearchTimeout)
	}
	if cfg.MaxScrapesPerRun != 20 {
		t.Errorf("expected default max scrapes 20, got %d", cfg.MaxScrapesPerRun)
	}
	if cfg.MaxSearchResults != 20 {
		t.Errorf("expected default max search results 20, got %d", cfg.MaxSearchResults)
	}
	if cfg.MaxSteps != 20 {
		t.Errorf("expected default max steps 20, got %d", cfg.MaxSteps)
	}
	if cfg.EnableBrowserFallback {
		t.Error("browser fallback should be disabled by default")
	}
	if cfg.AllowProfileScraping {
		t.Error("profile scraping should be off by default")
	}
}

func TestLoadCustomEnv(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("MAX_SCRAPES_PER_RUN", "5")
	t.Setenv("MAX_SEARCH_RESULTS", "12")
	t.Setenv("MAX_AGENT_STEPS", "20")
	t.Setenv("RESEARCH_TIMEOUT_SECONDS", "60")
	t.Setenv("ENABLE_BROWSER_FALLBACK", "true")
	t.Setenv("ALLOW_PROFILE_SCRAPING", "true")

	cfg := Load()

	if cfg.Port != "9090" {
		t.Errorf("expected port 9090, got %s", cfg.Port)
	}
	if cfg.MaxScrapesPerRun != 5 {
		t.Errorf("expected max scrapes 5, got %d", cfg.MaxScrapesPerRun)
	}
	if cfg.MaxSearchResults != 12 {
		t.Errorf("expected max search results 12, got %d", cfg.MaxSearchResults)
	}
	if cfg.MaxSteps != 20 {
		t.Errorf("expected max steps 20, got %d", cfg.MaxSteps)
	}
	if cfg.ResearchTimeout != 60*time.Second {
		t.Errorf("expected timeout 60s, got %v", cfg.ResearchTimeout)
	}
	if !cfg.EnableBrowserFallback {
		t.Error("expected browser fallback to be enabled")
	}
	if !cfg.AllowProfileScraping {
		t.Error("expected profile scraping to be enabled when the env var is set")
	}
}

func TestBrowserResearchTimeoutSelection(t *testing.T) {
	os.Unsetenv("RESEARCH_TIMEOUT_SECONDS")
	os.Unsetenv("BROWSER_RESEARCH_TIMEOUT_SECONDS")

	cfg := Load()
	if cfg.BrowserResearchTimeout != 300*time.Second {
		t.Errorf("expected default rendered budget 300s, got %v", cfg.BrowserResearchTimeout)
	}
	if got := cfg.ResearchTimeoutFor(true); got != 300*time.Second {
		t.Errorf("a rendered run should get the larger budget, got %v", got)
	}
	if got := cfg.ResearchTimeoutFor(false); got != 180*time.Second {
		t.Errorf("an HTTP-only run should get the default budget, got %v", got)
	}
	if got := cfg.MaxResearchTimeout(); got != 300*time.Second {
		t.Errorf("expected the largest budget to be 300s, got %v", got)
	}
}

func TestBrowserResearchTimeoutNeverShrinksTheDefaultBudget(t *testing.T) {
	t.Setenv("RESEARCH_TIMEOUT_SECONDS", "400")
	t.Setenv("BROWSER_RESEARCH_TIMEOUT_SECONDS", "200")

	cfg := Load()
	if cfg.BrowserResearchTimeout != 400*time.Second {
		t.Errorf("opting into rendering must not shorten the budget, got %v", cfg.BrowserResearchTimeout)
	}
	if got := cfg.ResearchTimeoutFor(true); got != 400*time.Second {
		t.Errorf("expected the clamped budget, got %v", got)
	}
	if got := cfg.MaxResearchTimeout(); got != 400*time.Second {
		t.Errorf("expected the largest budget to be 400s, got %v", got)
	}
}

func TestResearchTimeoutForFallsBackWithoutBrowserBudget(t *testing.T) {
	// A directly constructed Config must never yield a zero (already expired) budget.
	cfg := &Config{ResearchTimeout: 90 * time.Second}
	if got := cfg.ResearchTimeoutFor(true); got != 90*time.Second {
		t.Errorf("expected the default budget as a fallback, got %v", got)
	}
	if got := cfg.MaxResearchTimeout(); got != 90*time.Second {
		t.Errorf("expected the default budget as the maximum, got %v", got)
	}
}
