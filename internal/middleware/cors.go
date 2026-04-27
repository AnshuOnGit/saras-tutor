package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"saras-tutor/internal/config"

	"github.com/gin-gonic/gin"
)

// CORS returns a Gin middleware that validates the request Origin against the
// configured allow-list and sets appropriate cross-origin headers.
func CORS(cfg config.CORSConfig) gin.HandlerFunc {
	allowAll := false
	allowedOriginsMap := make(map[string]bool)
	for _, origin := range cfg.AllowedOrigins {
		if origin == "*" {
			allowAll = true
		}
		allowedOriginsMap[strings.ToLower(origin)] = true
	}

	methodsStr := strings.Join(cfg.AllowedMethods, ", ")
	headersStr := strings.Join(cfg.AllowedHeaders, ", ")
	exposedStr := strings.Join(cfg.ExposedHeaders, ", ")
	maxAgeStr := strconv.Itoa(cfg.MaxAge)

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		if origin != "" {
			originLower := strings.ToLower(origin)

			allowed := allowAll || allowedOriginsMap[originLower]

			// Support localhost variants in development
			if !allowed && (strings.HasPrefix(originLower, "http://localhost") ||
				strings.HasPrefix(originLower, "http://127.0.0.1")) {
				for ao := range allowedOriginsMap {
					if strings.HasPrefix(ao, "http://localhost") {
						allowed = true
						break
					}
				}
			}

			if allowed {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Access-Control-Allow-Methods", methodsStr)
				c.Header("Access-Control-Allow-Headers", headersStr)
				c.Header("Access-Control-Expose-Headers", exposedStr)
				c.Header("Access-Control-Max-Age", maxAgeStr)

				if cfg.AllowCredentials {
					c.Header("Access-Control-Allow-Credentials", "true")
				}

				c.Header("Vary", "Origin")
			}
		}

		// Handle preflight requests
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
