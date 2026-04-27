package studio

import (
	"context"
	"net/http"
	"time"

	"saras-tutor/internal/middleware"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HealthHandler struct {
	db        *pgxpool.Pool
	version   string
	startTime time.Time
}

func NewHealthHandler(db *pgxpool.Pool, version string) *HealthHandler {
	return &HealthHandler{
		db:        db,
		version:   version,
		startTime: time.Now(),
	}
}

type HealthResponse struct {
	Status    string           `json:"status"`
	Version   string           `json:"version"`
	Timestamp string           `json:"timestamp"`
	Uptime    string           `json:"uptime"`
	Checks    map[string]Check `json:"checks"`
}

type Check struct {
	Status  string `json:"status"`
	Latency string `json:"latency,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (h *HealthHandler) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	response := HealthResponse{
		Status:    "healthy",
		Version:   h.version,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Uptime:    time.Since(h.startTime).String(),
		Checks:    make(map[string]Check),
	}

	// Check database
	dbCheck := h.checkDatabase(ctx)
	response.Checks["database"] = dbCheck
	if dbCheck.Status != "healthy" {
		response.Status = "degraded"
	}

	statusCode := http.StatusOK
	if response.Status != "healthy" {
		statusCode = http.StatusServiceUnavailable
	}

	c.JSON(statusCode, response)
}

func (h *HealthHandler) checkDatabase(ctx context.Context) Check {
	start := time.Now()

	err := h.db.Ping(ctx)
	latency := time.Since(start)

	if err != nil {
		return Check{
			Status:  "unhealthy",
			Latency: latency.String(),
			Error:   "database connection failed",
		}
	}

	return Check{
		Status:  "healthy",
		Latency: latency.String(),
	}
}

// Liveness probe - just checks if the server is running
func (h *HealthHandler) Liveness(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "alive",
	})
}

// Readiness probe - checks if the server is ready to accept traffic
func (h *HealthHandler) Readiness(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		log := middleware.GetLogger(c)
		log.Warn().Err(err).Msg("readiness check failed - database unavailable")

		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"reason": "database unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "ready",
	})
}
