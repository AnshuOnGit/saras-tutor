package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"saras-tutor/internal/config"

	"github.com/gin-gonic/gin"
)

// CORS returns a Gin middleware that sets cross-origin headers from config.
func CORS(cfg config.CORSConfig) gin.HandlerFunc {
	origins := strings.Join(cfg.AllowedOrigins, ", ")
	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")
	exposed := strings.Join(cfg.ExposedHeaders, ", ")
	maxAge := strconv.Itoa(cfg.MaxAge)
	creds := strconv.FormatBool(cfg.AllowCredentials)

	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", origins)
		c.Writer.Header().Set("Access-Control-Allow-Methods", methods)
		c.Writer.Header().Set("Access-Control-Allow-Headers", headers)
		c.Writer.Header().Set("Access-Control-Expose-Headers", exposed)
		c.Writer.Header().Set("Access-Control-Allow-Credentials", creds)
		c.Writer.Header().Set("Access-Control-Max-Age", maxAge)

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
