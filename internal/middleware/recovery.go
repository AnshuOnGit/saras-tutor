package middleware

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				stack := string(debug.Stack())

				log := GetLogger(c)
				log.Error().
					Interface("error", err).
					Str("stack", stack).
					Str("method", c.Request.Method).
					Str("path", c.Request.URL.Path).
					Msg("panic recovered")

				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error":      "internal server error",
					"message":    "an unexpected error occurred",
					"request_id": GetRequestID(c),
				})
			}
		}()

		c.Next()
	}
}
