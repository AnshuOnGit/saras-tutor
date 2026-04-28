package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"saras-tutor/internal/models"
	"saras-tutor/internal/repository"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserDisabled       = errors.New("user account is disabled")
	ErrOAuthFailed        = errors.New("oauth authentication failed")
)

type AuthConfig struct {
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
	FrontendURL        string
	StateExpiry        time.Duration
}

type AuthService struct {
	config      AuthConfig
	oauthConfig *oauth2.Config
	jwtService  *JWTService
	userRepo    *repository.UserRepository
	sessionRepo *repository.SessionRepository
}

func NewAuthService(
	config AuthConfig,
	jwtService *JWTService,
	userRepo *repository.UserRepository,
	sessionRepo *repository.SessionRepository,
) *AuthService {
	oauthConfig := &oauth2.Config{
		ClientID:     config.GoogleClientID,
		ClientSecret: config.GoogleClientSecret,
		RedirectURL:  config.GoogleRedirectURL,
		Scopes: []string{
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
		},
		Endpoint: google.Endpoint,
	}

	return &AuthService{
		config:      config,
		oauthConfig: oauthConfig,
		jwtService:  jwtService,
		userRepo:    userRepo,
		sessionRepo: sessionRepo,
	}
}

// GetAuthURL generates the Google OAuth URL with state.
func (s *AuthService) GetAuthURL(ctx context.Context, redirectURL string) (string, error) {
	state, err := GenerateStateToken()
	if err != nil {
		return "", err
	}

	if err := s.sessionRepo.CreateOAuthState(ctx, state, redirectURL, s.config.StateExpiry); err != nil {
		return "", err
	}

	return s.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline), nil
}

// HandleCallback processes the OAuth callback.
func (s *AuthService) HandleCallback(ctx context.Context, code, state, userAgent, ipAddress string) (*TokenPair, *models.User, string, error) {
	// Validate state
	redirectURL, err := s.sessionRepo.ValidateAndDeleteOAuthState(ctx, state)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: invalid state", ErrOAuthFailed)
	}

	// Exchange code for token
	token, err := s.oauthConfig.Exchange(ctx, code)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: code exchange failed", ErrOAuthFailed)
	}

	// Get user info from Google
	googleUser, err := s.fetchGoogleUserInfo(ctx, token.AccessToken)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: failed to fetch user info", ErrOAuthFailed)
	}

	// Create or update user
	user, isNew, err := s.userRepo.UpsertFromGoogle(ctx, googleUser)
	if err != nil {
		return nil, nil, "", err
	}

	if !user.IsActive {
		return nil, nil, "", ErrUserDisabled
	}

	// Generate tokens
	tokenPair, err := s.jwtService.GenerateTokenPair(user)
	if err != nil {
		return nil, nil, "", err
	}

	// Create session
	session := &models.Session{
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(s.jwtService.GetRefreshTokenExpiry()),
	}
	if userAgent != "" {
		session.UserAgent = &userAgent
	}
	if ipAddress != "" {
		session.IPAddress = &ipAddress
	}

	if err := s.sessionRepo.Create(ctx, session, tokenPair.RefreshToken); err != nil {
		return nil, nil, "", err
	}

	_ = isNew // Could be used for welcome email, analytics, etc.

	return tokenPair, user, redirectURL, nil
}

// RefreshTokens generates new token pair from refresh token.
func (s *AuthService) RefreshTokens(ctx context.Context, refreshToken string) (*TokenPair, error) {
	session, err := s.sessionRepo.GetByRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	user, err := s.userRepo.GetByID(ctx, session.UserID)
	if err != nil {
		return nil, err
	}

	if !user.IsActive {
		return nil, ErrUserDisabled
	}

	if err := s.sessionRepo.UpdateLastUsed(ctx, session.ID); err != nil {
		return nil, err
	}

	tokenPair, err := s.jwtService.GenerateTokenPair(user)
	if err != nil {
		return nil, err
	}

	// Keep same refresh token
	tokenPair.RefreshToken = refreshToken

	return tokenPair, nil
}

// Logout revokes the session.
func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	session, err := s.sessionRepo.GetByRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil // Already logged out or invalid
	}
	return s.sessionRepo.Revoke(ctx, session.ID)
}

// LogoutAll revokes all sessions for a user.
func (s *AuthService) LogoutAll(ctx context.Context, userID string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user ID: %w", err)
	}
	return s.sessionRepo.RevokeAllForUser(ctx, uid)
}

func (s *AuthService) fetchGoogleUserInfo(ctx context.Context, accessToken string) (*models.GoogleUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("google api error: %s", string(body))
	}

	var userInfo models.GoogleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return nil, err
	}

	return &userInfo, nil
}
