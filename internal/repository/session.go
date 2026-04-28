package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"saras-tutor/internal/models"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionExpired  = errors.New("session expired")
	ErrSessionRevoked  = errors.New("session revoked")
	ErrInvalidState    = errors.New("invalid oauth state")
)

type SessionRepository struct {
	db *pgxpool.Pool
}

func NewSessionRepository(db *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{db: db}
}

// HashToken creates a SHA-256 hash of a token.
func HashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func (r *SessionRepository) Create(ctx context.Context, session *models.Session, refreshToken string) error {
	session.RefreshTokenHash = HashToken(refreshToken)

	query := `
		INSERT INTO sessions (user_id, refresh_token_hash, user_agent, ip_address, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, last_used_at
	`

	err := r.db.QueryRow(ctx, query,
		session.UserID,
		session.RefreshTokenHash,
		session.UserAgent,
		session.IPAddress,
		session.ExpiresAt,
	).Scan(&session.ID, &session.CreatedAt, &session.LastUsedAt)

	return err
}

func (r *SessionRepository) GetByRefreshToken(ctx context.Context, refreshToken string) (*models.Session, error) {
	hash := HashToken(refreshToken)

	query := `
		SELECT id, user_id, refresh_token_hash, user_agent, ip_address,
		       expires_at, created_at, last_used_at, revoked_at
		FROM sessions
		WHERE refresh_token_hash = $1
	`

	session := &models.Session{}
	err := r.db.QueryRow(ctx, query, hash).Scan(
		&session.ID,
		&session.UserID,
		&session.RefreshTokenHash,
		&session.UserAgent,
		&session.IPAddress,
		&session.ExpiresAt,
		&session.CreatedAt,
		&session.LastUsedAt,
		&session.RevokedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSessionNotFound
	}

	if err != nil {
		return nil, err
	}

	if session.IsRevoked() {
		return nil, ErrSessionRevoked
	}

	if session.IsExpired() {
		return nil, ErrSessionExpired
	}

	return session, nil
}

func (r *SessionRepository) UpdateLastUsed(ctx context.Context, sessionID uuid.UUID) error {
	query := `UPDATE sessions SET last_used_at = $2 WHERE id = $1`
	_, err := r.db.Exec(ctx, query, sessionID, time.Now())
	return err
}

func (r *SessionRepository) Revoke(ctx context.Context, sessionID uuid.UUID) error {
	query := `UPDATE sessions SET revoked_at = $2 WHERE id = $1`
	_, err := r.db.Exec(ctx, query, sessionID, time.Now())
	return err
}

func (r *SessionRepository) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	query := `UPDATE sessions SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`
	_, err := r.db.Exec(ctx, query, userID, time.Now())
	return err
}

func (r *SessionRepository) GetActiveSessionsForUser(ctx context.Context, userID uuid.UUID) ([]*models.Session, error) {
	query := `
		SELECT id, user_id, user_agent, ip_address, expires_at, created_at, last_used_at
		FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY last_used_at DESC
	`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*models.Session
	for rows.Next() {
		session := &models.Session{}
		err := rows.Scan(
			&session.ID,
			&session.UserID,
			&session.UserAgent,
			&session.IPAddress,
			&session.ExpiresAt,
			&session.CreatedAt,
			&session.LastUsedAt,
		)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}

	return sessions, rows.Err()
}

// OAuth State Management

func (r *SessionRepository) CreateOAuthState(ctx context.Context, state, redirectURL string, ttl time.Duration) error {
	query := `
		INSERT INTO oauth_states (state, redirect_url, expires_at)
		VALUES ($1, $2, $3)
	`
	_, err := r.db.Exec(ctx, query, state, redirectURL, time.Now().Add(ttl))
	return err
}

func (r *SessionRepository) ValidateAndDeleteOAuthState(ctx context.Context, state string) (string, error) {
	query := `
		DELETE FROM oauth_states
		WHERE state = $1 AND expires_at > NOW()
		RETURNING redirect_url
	`

	var redirectURL string
	err := r.db.QueryRow(ctx, query, state).Scan(&redirectURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalidState
	}

	return redirectURL, err
}
