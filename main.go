package main

import (
	"log"
	"os"

	"saras-tutor/config"
	"saras-tutor/db"
	"saras-tutor/handler"
	"saras-tutor/middleware"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env if present (ignored in production)
	_ = godotenv.Load()

	cfg := config.Load()

	// Initialise Postgres connection pool
	pool, err := db.NewPool(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	// Run migrations
	if err := db.Migrate(pool); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	// Seed topic taxonomy (idempotent — skips if already populated)
	if err := db.Seed(pool); err != nil {
		log.Fatalf("failed to seed taxonomy: %v", err)
	}

	r := gin.Default()

	// Middleware
	r.Use(middleware.CORS())
	r.Use(middleware.RequestID())

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Chat endpoint - streamable HTTP (SSE), accepts JSON or multipart/form-data
	chatHandler := handler.NewChatHandler(cfg, pool)
	r.POST("/chat", chatHandler.Handle)

	port := cfg.Port
	if port == "" {
		port = "8080"
	}
	log.Printf("saras-tutor listening on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("server error: %v", err)
		os.Exit(1)
	}
}
