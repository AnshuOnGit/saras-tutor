package main

import (
	"context"
	"os"

	"saras-tutor/internal/config"
	"saras-tutor/internal/db"
	"saras-tutor/internal/logger"
	"saras-tutor/internal/middleware"
	"saras-tutor/internal/storage"
	"saras-tutor/internal/studio"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	cfg := config.Load()

	logger.Init(logger.Config{
		Level:       cfg.Logging.Level,
		Format:      cfg.Logging.Format,
		ServiceName: "saras-studio",
		Version:     "1.0.0",
	})

	if err := cfg.Validate(); err != nil {
		logger.Fatal().Err(err).Msg("invalid configuration")
	}

	pool, err := db.NewPool(cfg.Database.URL)
	if err != nil {
		logger.Fatal().Err(err).Msg("database connection failed")
	}
	defer pool.Close()

	if err := db.Migrate(pool); err != nil {
		logger.Fatal().Err(err).Msg("migration failed")
	}

	// R2 storage (optional — nil means inline data URIs)
	storageSvc, storageErr := storage.NewStorageService(context.Background(), storage.R2Config{})
	if storageErr != nil {
		logger.Warn().Err(storageErr).Msg("R2 storage disabled")
		storageSvc = nil
	}

	h := studio.NewHandler(cfg, pool, storageSvc)

	r := gin.Default()
	r.Use(middleware.CORS(cfg.CORS))
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())

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
	logger.Info().Str("port", port).Msg("studio listening")
	if err := r.Run(":" + port); err != nil {
		logger.Fatal().Err(err).Msg("server error")
	}
}
