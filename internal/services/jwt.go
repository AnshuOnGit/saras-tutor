package services

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"saras-tutor/internal/models"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("token expired")
)

type JWTConfig struct {
	SecretKey          string
	AccessTokenExpiry  time.Duration
	RefreshTokenExpiry time.Duration
	Issuer             string
}

type JWTService struct {
	config JWTConfig
}

type Claims struct {
	jwt.RegisteredClaims
	UserID    string      `json:"user_id"`
	Email     string      `json:"email"`
	Role      models.Role `json:"role"`
	TokenType string      `json:"token_type"`
}

type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenType    string    `json:"token_type"`
}

func NewJWTService(config JWTConfig) *JWTService {
	return &JWTService{config: config}
}

func (s *JWTService) GenerateTokenPair(user *models.User) (*TokenPair, error) {
	now := time.Now()
	accessExpiry := now.Add(s.config.AccessTokenExpiry)

	// Generate access token
	accessClaims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			Issuer:    s.config.Issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(accessExpiry),
			ID:        uuid.New().String(),
		},
		UserID:    user.ID.String(),
		Email:     user.Email,
		Role:      user.Role,
		TokenType: "access",
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString([]byte(s.config.SecretKey))
	if err != nil {
		return nil, err
	}

	// Generate refresh token (random string, not JWT)
	refreshToken, err := generateRandomToken(32)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  accessTokenString,
		RefreshToken: refreshToken,
		ExpiresAt:    accessExpiry,
		TokenType:    "Bearer",
	}, nil
}

func (s *JWTService) ValidateAccessToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return []byte(s.config.SecretKey), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	if claims.TokenType != "access" {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

func (s *JWTService) GetRefreshTokenExpiry() time.Duration {
	return s.config.RefreshTokenExpiry
}

func generateRandomToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// GenerateStateToken creates a random state for OAuth.
func GenerateStateToken() (string, error) {
	return generateRandomToken(32)
}
