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
	if cfg.MaxScrapesPerRun != 6 {
		t.Errorf("expected default max scrapes 6, got %d", cfg.MaxScrapesPerRun)
	}
	if cfg.MaxSearchResults != 12 {
		t.Errorf("expected default max search results 12, got %d", cfg.MaxSearchResults)
	}
	if cfg.MaxSteps != 10 {
		t.Errorf("expected default max steps 10, got %d", cfg.MaxSteps)
	}
}

func TestLoadCustomEnv(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("MAX_SCRAPES_PER_RUN", "5")
	t.Setenv("MAX_SEARCH_RESULTS", "12")
	t.Setenv("MAX_AGENT_STEPS", "20")
	t.Setenv("RESEARCH_TIMEOUT_SECONDS", "60")

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
}
