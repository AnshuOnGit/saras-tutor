package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	Server    ServerConfig
	Database  DatabaseConfig
	LLM       LLMConfig
	CORS      CORSConfig
	RateLimit RateLimitConfig
	Security  SecurityConfig
	Logging   LoggingConfig
	Auth      AuthConfig
	JWT       JWTConfig
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port         string
	Environment  string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

// DatabaseConfig holds Postgres connection settings.
type DatabaseConfig struct {
	URL             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// LLMConfig holds NVIDIA NIM API connection settings.
type LLMConfig struct {
	BaseURL string
	APIKey  string
	UserID  string
}

// CORSConfig holds cross-origin resource sharing settings.
type CORSConfig struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           int
}

// RateLimitConfig holds rate-limiting parameters.
type RateLimitConfig struct {
	Enabled           bool
	RequestsPerSecond float64
	Burst             int
	CleanupInterval   time.Duration
	TTL               time.Duration
}

// SecurityConfig holds security-related settings.
type SecurityConfig struct {
	TrustedProxies []string
}

// LoggingConfig holds structured logging settings.
type LoggingConfig struct {
	Level  string // debug, info, warn, error
	Format string // json, text
}

// AuthConfig holds Google OAuth 2.0 settings.
type AuthConfig struct {
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
	FrontendURL        string
	StateExpiry        time.Duration
}

// JWTConfig holds JWT token settings.
type JWTConfig struct {
	SecretKey          string
	AccessTokenExpiry  time.Duration
	RefreshTokenExpiry time.Duration
	Issuer             string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		Server: ServerConfig{
			Port:         getEnv("PORT", "8090"),
			Environment:  getEnv("ENVIRONMENT", "development"),
			ReadTimeout:  getDurationEnv("READ_TIMEOUT", 10*time.Second),
			WriteTimeout: getDurationEnv("WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:  getDurationEnv("IDLE_TIMEOUT", 60*time.Second),
		},
		Database: DatabaseConfig{
			URL:             getEnv("DATABASE_URL", "postgres://saras:saras@localhost:5432/saras_tutor?sslmode=disable"),
			MaxOpenConns:    getIntEnv("DB_MAX_OPEN_CONNS", 10),
			MaxIdleConns:    getIntEnv("DB_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime: getDurationEnv("DB_CONN_MAX_LIFETIME", 30*time.Minute),
		},
		LLM: LLMConfig{
			BaseURL: getEnv("LLM_BASE_URL", "https://integrate.api.nvidia.com/v1"),
			APIKey:  getEnv("LLM_API_KEY", ""),
			UserID:  getEnv("LLM_USER_ID", ""),
		},
		CORS: CORSConfig{
			AllowedOrigins:   getSliceEnv("CORS_ALLOWED_ORIGINS", []string{"*"}),
			AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Request-ID"},
			ExposedHeaders:   []string{"X-Request-ID", "X-Conversation-ID", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"},
			AllowCredentials: true,
			MaxAge:           86400,
		},
		RateLimit: RateLimitConfig{
			Enabled:           getBoolEnv("RATE_LIMIT_ENABLED", true),
			RequestsPerSecond: getFloatEnv("RATE_LIMIT_RPS", 10.0),
			Burst:             getIntEnv("RATE_LIMIT_BURST", 20),
			CleanupInterval:   getDurationEnv("RATE_LIMIT_CLEANUP", 1*time.Minute),
			TTL:               getDurationEnv("RATE_LIMIT_TTL", 5*time.Minute),
		},
		Security: SecurityConfig{
			TrustedProxies: getSliceEnv("TRUSTED_PROXIES", []string{}),
		},
		Logging: LoggingConfig{
			Level:  getEnv("LOG_LEVEL", "info"),
			Format: getEnv("LOG_FORMAT", "json"),
		},
		Auth: AuthConfig{
			GoogleClientID:     getEnv("GOOGLE_CLIENT_ID", ""),
			GoogleClientSecret: getEnv("GOOGLE_CLIENT_SECRET", ""),
			GoogleRedirectURL:  getEnv("GOOGLE_REDIRECT_URL", "http://localhost:8090/api/v1/auth/google/callback"),
			FrontendURL:        getEnv("FRONTEND_URL", "http://localhost:5173"),
			StateExpiry:        getDurationEnv("OAUTH_STATE_EXPIRY", 10*time.Minute),
		},
		JWT: JWTConfig{
			SecretKey:          getEnv("JWT_SECRET_KEY", "change-this-in-production-use-32-chars"),
			AccessTokenExpiry:  getDurationEnv("JWT_ACCESS_EXPIRY", 15*time.Minute),
			RefreshTokenExpiry: getDurationEnv("JWT_REFRESH_EXPIRY", 7*24*time.Hour),
			Issuer:             getEnv("JWT_ISSUER", "saras-studio"),
		},
	}
}

// Validate checks that security-sensitive URLs use HTTPS.
func (c *Config) Validate() error {
	if c.LLM.BaseURL != "" && !strings.HasPrefix(c.LLM.BaseURL, "https://") {
		return fmt.Errorf("config: LLM_BASE_URL must use https:// (got %q)", c.LLM.BaseURL)
	}
	return nil
}

// IsProduction returns true if the environment is production.
func (c *Config) IsProduction() bool {
	return c.Server.Environment == "production"
}

// IsDevelopment returns true if the environment is development.
func (c *Config) IsDevelopment() bool {
	return c.Server.Environment == "development"
}

// ── Helper functions ──────────────────────────────────────────────────

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getIntEnv(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getFloatEnv(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func getBoolEnv(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func getDurationEnv(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func getSliceEnv(key string, fallback []string) []string {
	if v := os.Getenv(key); v != "" {
		return strings.Split(v, ",")
	}
	return fallback
}
