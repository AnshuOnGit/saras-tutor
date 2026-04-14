package config

import (
	"os"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	Port        string
	DatabaseURL string

	// LLM provider keys / endpoints.
	LLMBaseURL string
	LLMAPIKey  string
	LLMUserID  string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://saras:saras@localhost:5432/saras_tutor?sslmode=disable"),
		LLMBaseURL:  getEnv("LLM_BASE_URL", "https://integrate.api.nvidia.com/v1"),
		LLMAPIKey:   getEnv("LLM_API_KEY", ""),
		LLMUserID:   getEnv("LLM_USER_ID", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
