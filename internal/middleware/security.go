package middleware

import (
	"github.com/gin-gonic/gin"
)

type SecurityConfig struct {
	Environment string
}

func Security(cfg SecurityConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Prevent MIME type sniffing
		c.Header("X-Content-Type-Options", "nosniff")

		// Clickjacking protection
		c.Header("X-Frame-Options", "DENY")

		// XSS Protection (legacy browsers)
		c.Header("X-XSS-Protection", "1; mode=block")

		// Referrer policy
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

		// Permissions policy
		c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

		// Content Security Policy
		if cfg.Environment == "production" {
			c.Header("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		}

		// HSTS (only in production with HTTPS)
		if cfg.Environment == "production" {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		}

		// Remove server header
		c.Header("Server", "")

		c.Next()
	}
}

// SanitizeHeaders strips potentially dangerous headers from incoming requests.
func SanitizeHeaders() gin.HandlerFunc {
	sensitiveHeaders := []string{
		"X-Forwarded-Host",
		"X-Original-URL",
		"X-Rewrite-URL",
	}

	return func(c *gin.Context) {
		for _, header := range sensitiveHeaders {
			c.Request.Header.Del(header)
		}
		c.Next()
	}
}
