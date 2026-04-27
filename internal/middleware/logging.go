package middleware

import (
	"bytes"
	"io"
	"time"

	"saras-tutor/internal/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const (
	RequestIDHeader = "X-Request-ID"
	RequestIDKey    = "request_id"
	LoggerKey       = "logger"
)

type responseWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *responseWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader(RequestIDHeader)
		if requestID == "" {
			requestID = uuid.New().String()
		}

		c.Set(RequestIDKey, requestID)
		c.Header(RequestIDHeader, requestID)

		// Create request-scoped logger
		reqLogger := logger.WithRequestID(requestID)
		c.Set(LoggerKey, &reqLogger)

		c.Next()
	}
}

func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		// Capture request body for debugging (limit size)
		var requestBody []byte
		if c.Request.Body != nil && c.Request.ContentLength > 0 && c.Request.ContentLength < 10240 {
			requestBody, _ = io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		}

		// Wrap response writer to capture response
		blw := &responseWriter{body: bytes.NewBuffer(nil), ResponseWriter: c.Writer}
		c.Writer = blw

		// Process request
		c.Next()

		// Calculate latency
		latency := time.Since(start)
		statusCode := c.Writer.Status()

		// Get logger from context
		reqLogger, exists := c.Get(LoggerKey)
		var log *zerolog.Logger
		if exists {
			log = reqLogger.(*zerolog.Logger)
		} else {
			l := logger.Log
			log = &l
		}

		// Determine log level based on status code
		var event *zerolog.Event
		switch {
		case statusCode >= 500:
			event = log.Error()
		case statusCode >= 400:
			event = log.Warn()
		default:
			event = log.Info()
		}

		// Build log entry
		event.
			Str("method", c.Request.Method).
			Str("path", path).
			Str("query", query).
			Int("status", statusCode).
			Dur("latency", latency).
			Str("client_ip", c.ClientIP()).
			Str("user_agent", c.Request.UserAgent()).
			Int("body_size", c.Writer.Size())

		// Add error if present
		if len(c.Errors) > 0 {
			event.Str("errors", c.Errors.String())
		}

		event.Msg("request completed")
	}
}

// GetRequestID extracts request ID from context
func GetRequestID(c *gin.Context) string {
	if id, exists := c.Get(RequestIDKey); exists {
		return id.(string)
	}
	return ""
}

// GetLogger extracts logger from context
func GetLogger(c *gin.Context) *zerolog.Logger {
	if l, exists := c.Get(LoggerKey); exists {
		return l.(*zerolog.Logger)
	}
	return &logger.Log
}
