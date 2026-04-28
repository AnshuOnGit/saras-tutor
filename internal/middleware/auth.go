package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"saras-tutor/internal/models"
	"saras-tutor/internal/services"
)

const (
	AuthUserKey   = "auth_user"
	AuthClaimsKey = "auth_claims"
)

type AuthMiddleware struct {
	jwtService *services.JWTService
}

func NewAuthMiddleware(jwtService *services.JWTService) *AuthMiddleware {
	return &AuthMiddleware{jwtService: jwtService}
}

// RequireAuth ensures the request has a valid access token.
func (m *AuthMiddleware) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "missing or invalid authorization header",
			})
			return
		}

		claims, err := m.jwtService.ValidateAccessToken(token)
		if err != nil {
			status := http.StatusUnauthorized
			message := "invalid token"

			if err == services.ErrExpiredToken {
				message = "token expired"
			}

			c.AbortWithStatusJSON(status, gin.H{
				"error":   "unauthorized",
				"message": message,
			})
			return
		}

		c.Set(AuthClaimsKey, claims)
		c.Next()
	}
}

// RequireRole ensures the user has one of the specified roles.
func (m *AuthMiddleware) RequireRole(roles ...models.Role) gin.HandlerFunc {
	roleMap := make(map[models.Role]bool)
	for _, role := range roles {
		roleMap[role] = true
	}

	return func(c *gin.Context) {
		claims, exists := c.Get(AuthClaimsKey)
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "authentication required",
			})
			return
		}

		authClaims := claims.(*services.Claims)
		if !roleMap[authClaims.Role] {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":   "forbidden",
				"message": "insufficient permissions",
			})
			return
		}

		c.Next()
	}
}

// OptionalAuth parses token if present but doesn't require it.
func (m *AuthMiddleware) OptionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			c.Next()
			return
		}

		claims, err := m.jwtService.ValidateAccessToken(token)
		if err == nil {
			c.Set(AuthClaimsKey, claims)
		}

		c.Next()
	}
}

func extractToken(c *gin.Context) string {
	// Try Authorization header first
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
			return parts[1]
		}
	}

	// Try cookie as fallback
	if token, err := c.Cookie("access_token"); err == nil {
		return token
	}

	return ""
}

// GetAuthClaims retrieves claims from context.
func GetAuthClaims(c *gin.Context) *services.Claims {
	if claims, exists := c.Get(AuthClaimsKey); exists {
		return claims.(*services.Claims)
	}
	return nil
}

// GetUserID retrieves the authenticated user's ID.
func GetUserID(c *gin.Context) (uuid.UUID, bool) {
	claims := GetAuthClaims(c)
	if claims == nil {
		return uuid.Nil, false
	}

	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		return uuid.Nil, false
	}

	return uid, true
}

// GetUserRole retrieves the authenticated user's role.
func GetUserRole(c *gin.Context) (models.Role, bool) {
	claims := GetAuthClaims(c)
	if claims == nil {
		return "", false
	}
	return claims.Role, true
}
