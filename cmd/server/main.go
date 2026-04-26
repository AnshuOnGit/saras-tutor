package main

import (
	"context"
	"log/slog"
	"os"

	"saras-tutor/internal/config"
	"saras-tutor/internal/db"
	"saras-tutor/internal/middleware"
	"saras-tutor/internal/storage"
	"saras-tutor/internal/studio"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	_ = godotenv.Load()

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	pool, err := db.NewPool(cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := db.Migrate(pool); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// R2 storage (optional — nil means inline data URIs)
	storageSvc, storageErr := storage.NewStorageService(context.Background(), storage.R2Config{})
	if storageErr != nil {
		slog.Warn("R2 storage disabled", "error", storageErr)
		storageSvc = nil
	}

	h := studio.NewHandler(cfg, pool, storageSvc)

	r := gin.Default()
	r.Use(middleware.CORS())
	r.Use(middleware.RequestID())

	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	api := r.Group("/api")
	api.GET("/models", h.ListModels)
	api.POST("/extract", h.Extract)
	api.GET("/extractions", h.ListExtractions)
	api.POST("/chat", h.Chat)

	port := os.Getenv("PORT")
	if port == "" {
		port = os.Getenv("STUDIO_PORT")
	}
	if port == "" {
		port = "8090"
	}
	slog.Info("studio listening", "port", port)
	if err := r.Run(":" + port); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
