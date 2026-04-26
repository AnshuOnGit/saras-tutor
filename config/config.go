package config

import (
	"fmt"
	"os"
	"strings"
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

// Validate checks that security-sensitive URLs use HTTPS.
func (c *Config) Validate() error {
	if c.LLMBaseURL != "" && !strings.HasPrefix(c.LLMBaseURL, "https://") {
		return fmt.Errorf("config: LLM_BASE_URL must use https:// (got %q)", c.LLMBaseURL)
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
