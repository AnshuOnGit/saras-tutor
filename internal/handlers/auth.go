package handlers

import (
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"

	"saras-tutor/internal/middleware"
	"saras-tutor/internal/repository"
	"saras-tutor/internal/services"
)

type AuthHandler struct {
	authService  *services.AuthService
	userRepo     *repository.UserRepository
	frontendURL  string
	cookieDomain string
	secureCookie bool
}

func NewAuthHandler(
	authService *services.AuthService,
	userRepo *repository.UserRepository,
	frontendURL string,
	cookieDomain string,
	secureCookie bool,
) *AuthHandler {
	return &AuthHandler{
		authService:  authService,
		userRepo:     userRepo,
		frontendURL:  frontendURL,
		cookieDomain: cookieDomain,
		secureCookie: secureCookie,
	}
}

// GoogleLogin initiates Google OAuth flow.
func (h *AuthHandler) GoogleLogin(c *gin.Context) {
	redirectURL := c.Query("redirect")
	if redirectURL == "" {
		redirectURL = h.frontendURL
	}

	if !h.isValidRedirect(redirectURL) {
		redirectURL = h.frontendURL
	}

	authURL, err := h.authService.GetAuthURL(c.Request.Context(), redirectURL)
	if err != nil {
		log := middleware.GetLogger(c)
		log.Error().Err(err).Msg("failed to generate auth URL")

		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "failed to initiate authentication",
		})
		return
	}

	c.Redirect(http.StatusTemporaryRedirect, authURL)
}

// GoogleCallback handles OAuth callback.
func (h *AuthHandler) GoogleCallback(c *gin.Context) {
	log := middleware.GetLogger(c)

	if errParam := c.Query("error"); errParam != "" {
		log.Warn().
			Str("error", errParam).
			Str("description", c.Query("error_description")).
			Msg("OAuth error from Google")

		h.redirectWithError(c, "authentication_cancelled", "Authentication was cancelled")
		return
	}

	code := c.Query("code")
	state := c.Query("state")

	if code == "" || state == "" {
		h.redirectWithError(c, "invalid_request", "Missing code or state")
		return
	}

	tokenPair, user, redirectURL, err := h.authService.HandleCallback(
		c.Request.Context(),
		code,
		state,
		c.Request.UserAgent(),
		c.ClientIP(),
	)

	if err != nil {
		log.Error().Err(err).Msg("OAuth callback failed")

		switch err {
		case services.ErrUserDisabled:
			h.redirectWithError(c, "account_disabled", "Your account has been disabled")
		default:
			h.redirectWithError(c, "authentication_failed", "Authentication failed")
		}
		return
	}

	log.Info().
		Str("user_id", user.ID.String()).
		Str("email", user.Email).
		Msg("user authenticated successfully")

	h.setAuthCookies(c, tokenPair)

	finalURL := h.buildSuccessRedirect(redirectURL, tokenPair)
	c.Redirect(http.StatusTemporaryRedirect, finalURL)
}

// RefreshToken refreshes the access token.
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}

	if err := c.ShouldBindJSON(&req); err != nil || req.RefreshToken == "" {
		var err error
		req.RefreshToken, err = c.Cookie("refresh_token")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_request",
				"message": "refresh token required",
			})
			return
		}
	}

	tokenPair, err := h.authService.RefreshTokens(c.Request.Context(), req.RefreshToken)
	if err != nil {
		status := http.StatusUnauthorized
		message := "invalid refresh token"

		if err == services.ErrUserDisabled {
			message = "account disabled"
		}

		c.JSON(status, gin.H{
			"error":   "unauthorized",
			"message": message,
		})
		return
	}

	h.setAuthCookies(c, tokenPair)

	c.JSON(http.StatusOK, gin.H{
		"access_token": tokenPair.AccessToken,
		"token_type":   tokenPair.TokenType,
		"expires_at":   tokenPair.ExpiresAt,
	})
}

// Logout revokes the current session.
func (h *AuthHandler) Logout(c *gin.Context) {
	refreshToken, _ := c.Cookie("refresh_token")

	if refreshToken != "" {
		_ = h.authService.Logout(c.Request.Context(), refreshToken)
	}

	h.clearAuthCookies(c)

	c.JSON(http.StatusOK, gin.H{
		"message": "logged out successfully",
	})
}

// LogoutAll revokes all sessions for the user.
func (h *AuthHandler) LogoutAll(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "authentication required",
		})
		return
	}

	if err := h.authService.LogoutAll(c.Request.Context(), userID.String()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "failed to logout",
		})
		return
	}

	h.clearAuthCookies(c)

	c.JSON(http.StatusOK, gin.H{
		"message": "logged out from all devices",
	})
}

// GetProfile returns the current user's profile.
func (h *AuthHandler) GetProfile(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "authentication required",
		})
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		if err == repository.ErrUserNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "user not found",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "failed to fetch profile",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user": user.ToProfile(),
	})
}

func (h *AuthHandler) isValidRedirect(redirectURL string) bool {
	parsed, err := url.Parse(redirectURL)
	if err != nil {
		return false
	}

	frontendParsed, _ := url.Parse(h.frontendURL)

	return parsed.Host == frontendParsed.Host || parsed.Host == ""
}

func (h *AuthHandler) setAuthCookies(c *gin.Context, tokenPair *services.TokenPair) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(
		"access_token",
		tokenPair.AccessToken,
		int(time.Until(tokenPair.ExpiresAt).Seconds()),
		"/",
		h.cookieDomain,
		h.secureCookie,
		true,
	)

	c.SetCookie(
		"refresh_token",
		tokenPair.RefreshToken,
		60*60*24*7, // 7 days
		"/api/v1/auth",
		h.cookieDomain,
		h.secureCookie,
		true,
	)
}

func (h *AuthHandler) clearAuthCookies(c *gin.Context) {
	c.SetCookie("access_token", "", -1, "/", h.cookieDomain, h.secureCookie, true)
	c.SetCookie("refresh_token", "", -1, "/api/v1/auth", h.cookieDomain, h.secureCookie, true)
}

func (h *AuthHandler) redirectWithError(c *gin.Context, errorCode, message string) {
	redirectURL := h.frontendURL + "/auth/error?" +
		"error=" + url.QueryEscape(errorCode) +
		"&message=" + url.QueryEscape(message)

	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

func (h *AuthHandler) buildSuccessRedirect(baseURL string, tokenPair *services.TokenPair) string {
	if baseURL == "" {
		baseURL = h.frontendURL
	}

	return baseURL + "/auth/callback#" +
		"access_token=" + url.QueryEscape(tokenPair.AccessToken) +
		"&token_type=" + tokenPair.TokenType +
		"&expires_at=" + url.QueryEscape(tokenPair.ExpiresAt.Format(time.RFC3339))
}
