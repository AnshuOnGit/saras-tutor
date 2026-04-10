package config

import (
	"os"
	"strings"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	Port        string
	DatabaseURL string

	// LLM provider keys / endpoints - each agent may use a different model.
	LLMBaseURL         string
	LLMAPIKey          string
	LLMUserID          string
	OpenAIModelDefault string

	// Vision model for image extraction agent.
	VisionModel string

	// RetryModels is the list of alternative models the user can pick
	// when the verifier gives a low score even after image retry.
	// Comma-separated in the env var, e.g. "gpt-5,gpt-4,gemini".
	RetryModels []string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	retryRaw := getEnv("RETRY_MODELS", "gpt-5,gpt-4,gemini")
	var retryModels []string
	for _, m := range strings.Split(retryRaw, ",") {
		m = strings.TrimSpace(m)
		if m != "" {
			retryModels = append(retryModels, m)
		}
	}

	return &Config{
		Port:               getEnv("PORT", "8080"),
		DatabaseURL:        getEnv("DATABASE_URL", "postgres://saras:saras@localhost:5432/saras_tutor?sslmode=disable"),
		LLMBaseURL:         getEnv("LLM_BASE_URL", "https://api.openai.com/v1"),
		LLMAPIKey:          getEnv("LLM_API_KEY", ""),
		LLMUserID:          getEnv("LLM_USER_ID", ""),
		OpenAIModelDefault: getEnv("OPENAI_MODEL_DEFAULT", "claude-sonnet-4.5"),
		VisionModel:        getEnv("VISION_MODEL", "claude-sonnet-4.5"),
		RetryModels:        retryModels,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
