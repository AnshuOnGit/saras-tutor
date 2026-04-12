package config

import (
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

	// Three-tier model setup:
	//   Vision  — heavy VLM for image extraction (e.g. Qwen2-VL-72B)
	//   Solver  — math-strong model for hints + solutions (e.g. QwQ-32B)
	//   Router  — fast/cheap model for validation, parsing, verification
	VisionModel string
	SolverModel string
	RouterModel string

	// RetryModels is the list of alternative models the user can pick
	// when the verifier gives a low score even after image retry.
	// Comma-separated in the env var, e.g. "model-a,model-b".
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
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://saras:saras@localhost:5432/saras_tutor?sslmode=disable"),
		LLMBaseURL:  getEnv("LLM_BASE_URL", "https://integrate.api.nvidia.com/v1"),
		LLMAPIKey:   getEnv("LLM_API_KEY", ""),
		LLMUserID:   getEnv("LLM_USER_ID", ""),
		VisionModel: getEnv("VISION_MODEL", "meta/llama-3.2-90b-vision-instruct"),
		SolverModel: getEnv("SOLVER_MODEL", "qwen/qwen3-next-80b-a3b-instruct"),
		RouterModel: getEnv("ROUTER_MODEL", "meta/llama-3.1-70b-instruct"),
		RetryModels: retryModels,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
