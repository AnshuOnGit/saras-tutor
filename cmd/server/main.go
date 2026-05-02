package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"saras-tutor/internal/config"
	"saras-tutor/internal/db"
	"saras-tutor/internal/handlers"
	"saras-tutor/internal/logger"
	"saras-tutor/internal/middleware"
	"saras-tutor/internal/models"
	"saras-tutor/internal/repository"
	"saras-tutor/internal/services"
	"saras-tutor/internal/storage"
	"saras-tutor/internal/studio"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

const Version = "1.0.0"

func main() {
	_ = godotenv.Load()

	cfg := config.Load()

	logger.Init(logger.Config{
		Level:       cfg.Logging.Level,
		Format:      cfg.Logging.Format,
		ServiceName: "saras-studio",
		Version:     Version,
	})

	if err := cfg.Validate(); err != nil {
		logger.Fatal().Err(err).Msg("invalid configuration")
	}

	// Set Gin mode
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
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

	// ── Repositories ──────────────────────────────────────────────────
	userRepo := repository.NewUserRepository(pool)
	sessionRepo := repository.NewSessionRepository(pool)

	// ── Services ──────────────────────────────────────────────────────
	jwtService := services.NewJWTService(services.JWTConfig{
		SecretKey:          cfg.JWT.SecretKey,
		AccessTokenExpiry:  cfg.JWT.AccessTokenExpiry,
		RefreshTokenExpiry: cfg.JWT.RefreshTokenExpiry,
		Issuer:             cfg.JWT.Issuer,
	})

	authService := services.NewAuthService(
		services.AuthConfig{
			GoogleClientID:     cfg.Auth.GoogleClientID,
			GoogleClientSecret: cfg.Auth.GoogleClientSecret,
			GoogleRedirectURL:  cfg.Auth.GoogleRedirectURL,
			FrontendURL:        cfg.Auth.FrontendURL,
			StateExpiry:        cfg.Auth.StateExpiry,
		},
		jwtService,
		userRepo,
		sessionRepo,
	)

	// ── Middleware ─────────────────────────────────────────────────────
	authMiddleware := middleware.NewAuthMiddleware(jwtService)

	// ── Handlers ──────────────────────────────────────────────────────
	h := studio.NewHandler(cfg, pool, storageSvc)
	hh := studio.NewHealthHandler(pool, Version)
	authHandler := handlers.NewAuthHandler(
		authService,
		userRepo,
		cfg.Auth.FrontendURL,
		cfg.IsProduction(),
	)

	// ── Router ────────────────────────────────────────────────────────
	r := gin.New()

	// Configure trusted proxies
	if len(cfg.Security.TrustedProxies) > 0 {
		r.SetTrustedProxies(cfg.Security.TrustedProxies)
	} else {
		r.SetTrustedProxies(nil)
	}

	r.Use(middleware.Recovery())
	r.Use(middleware.SanitizeHeaders())
	r.Use(middleware.Security(middleware.SecurityConfig{Environment: cfg.Server.Environment}))
	r.Use(middleware.CORS(cfg.CORS))
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())

	if cfg.RateLimit.Enabled {
		rl := middleware.NewRateLimiter(middleware.RateLimitConfig{
			RequestsPerSecond: cfg.RateLimit.RequestsPerSecond,
			Burst:             cfg.RateLimit.Burst,
			TTL:               cfg.RateLimit.TTL,
			CleanupInterval:   cfg.RateLimit.CleanupInterval,
		})
		r.Use(rl.Middleware())
		logger.Info().
			Float64("rps", cfg.RateLimit.RequestsPerSecond).
			Int("burst", cfg.RateLimit.Burst).
			Msg("rate limiting enabled")
	}

	// ── Health / Liveness / Readiness ──────────────────────────────────
	r.GET("/", hh.Liveness)
	r.HEAD("/", hh.Liveness)
	r.GET("/health", hh.Health)
	r.GET("/liveness", hh.Liveness)
	r.GET("/readiness", hh.Readiness)

	// ── Studio API (existing) ─────────────────────────────────────────
	api := r.Group("/api")
	api.GET("/models", authMiddleware.RequireAuth(), h.ListModels)
	api.POST("/extract", authMiddleware.RequireAuth(), h.Extract)
	api.GET("/extractions", authMiddleware.RequireAuth(), h.ListExtractions)
	api.POST("/chat", authMiddleware.RequireAuth(), h.Chat)

	// ── Auth API (v1) ─────────────────────────────────────────────────
	v1 := r.Group("/api/v1")
	{
		auth := v1.Group("/auth")
		{
			auth.GET("/google", authHandler.GoogleLogin)
			auth.GET("/google/callback", authHandler.GoogleCallback)
			auth.POST("/refresh", authHandler.RefreshToken)
			auth.POST("/logout", authHandler.Logout)
			auth.POST("/logout-all", authMiddleware.RequireAuth(), authHandler.LogoutAll)
		}

		users := v1.Group("/users")
		users.Use(authMiddleware.RequireAuth())
		{
			users.GET("/me", authHandler.GetProfile)
		}

		admin := v1.Group("/admin")
		admin.Use(authMiddleware.RequireAuth())
		admin.Use(authMiddleware.RequireRole(models.RoleAdmin))
		{
			admin.GET("/stats", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"message": "admin stats endpoint"})
			})
		}
	}

	// ── Cleanup job ───────────────────────────────────────────────────
	go startCleanupJob(pool)

	// ── HTTP server with graceful shutdown ─────────────────────────────
	// WriteTimeout is set to 0 because SSE streaming endpoints (e.g. /api/chat)
	// can run for minutes. Go's WriteTimeout is a hard deadline from when headers
	// are sent, which kills long-lived SSE connections.
	server := &http.Server{
		Addr:        ":" + cfg.Server.Port,
		Handler:     r,
		ReadTimeout: cfg.Server.ReadTimeout,
		IdleTimeout: cfg.Server.IdleTimeout,
	}

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		logger.Info().Msg("shutting down server...")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Error().Err(err).Msg("server shutdown error")
		}
	}()

	logger.Info().
		Str("port", cfg.Server.Port).
		Str("environment", cfg.Server.Environment).
		Msg("server listening")

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatal().Err(err).Msg("server error")
	}

	logger.Info().Msg("server stopped")
}

func startCleanupJob(pool *pgxpool.Pool) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		_ = ctx // CleanupExpiredData uses background context internally
		if err := db.CleanupExpiredData(pool); err != nil {
			logger.Error().Err(err).Msg("cleanup job failed")
		} else {
			logger.Debug().Msg("cleanup job completed")
		}
		cancel()
	}
}
