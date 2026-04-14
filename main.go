package main

import (
	"log/slog"
	"os"
	"strconv"

	"saras-tutor/config"
	"saras-tutor/db"
	"saras-tutor/handler"
	"saras-tutor/middleware"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// Structured JSON logging for production
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Load .env if present (ignored in production)
	_ = godotenv.Load()

	cfg := config.Load()

	// Initialise Postgres connection pool
	pool, err := db.NewPool(cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Run migrations
	if err := db.Migrate(pool); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// Seed topic taxonomy (idempotent — skips if already populated)
	if err := db.Seed(pool); err != nil {
		slog.Error("failed to seed taxonomy", "error", err)
		os.Exit(1)
	}

	r := gin.Default()

	// Middleware
	r.Use(middleware.CORS())
	r.Use(middleware.RequestID())

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Experts endpoint — returns model registry for the frontend model picker
	r.GET("/experts", func(c *gin.Context) {
		difficulty := 0
		if d := c.Query("difficulty"); d != "" {
			if v, err := strconv.Atoi(d); err == nil {
				difficulty = v
			}
		}
		subject := c.Query("subject")

		recommendedID := config.SelectModelForTask(difficulty, subject, false)

		type expertResponse struct {
			ID                    string `json:"id"`
			DisplayName           string `json:"display_name"`
			Category              string `json:"category"`
			RecommendedDifficulty int    `json:"recommended_difficulty"`
			Recommended           bool   `json:"recommended"`
		}

		var experts []expertResponse
		for _, m := range config.ModelRegistry {
			experts = append(experts, expertResponse{
				ID:                    m.ID,
				DisplayName:           m.DisplayName,
				Category:              string(m.Category),
				RecommendedDifficulty: m.RecommendedDifficulty,
				Recommended:           m.ID == recommendedID,
			})
		}
		c.JSON(200, experts)
	})

	// Chat endpoint - streamable HTTP (SSE), accepts JSON or multipart/form-data
	chatHandler := handler.NewChatHandler(cfg, pool)
	r.POST("/chat", chatHandler.Handle)

	port := cfg.Port
	if port == "" {
		port = "8080"
	}
	slog.Info("saras-tutor listening", "port", port)
	if err := r.Run(":" + port); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
