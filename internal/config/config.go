package config

import (
	"os"
)

// Config stores application runtime settings and API credentials.
type Config struct {
	Port         string
	GCPProjectID string
	GCPRegion    string
	SerperAPIKey string
	GeminiModel  string
	CookieFile   string
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
		Port:         port,
		GCPProjectID: os.Getenv("GOOGLE_CLOUD_PROJECT"),
		GCPRegion:    region,
		SerperAPIKey: os.Getenv("SERPER_API_KEY"),
		GeminiModel:  model,
		CookieFile:   os.Getenv("COOKIE_FILE_PATH"),
	}
}
