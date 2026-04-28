package models

import (
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	UserID           uuid.UUID  `json:"user_id" db:"user_id"`
	RefreshTokenHash string     `json:"-" db:"refresh_token_hash"`
	UserAgent        *string    `json:"user_agent,omitempty" db:"user_agent"`
	IPAddress        *string    `json:"ip_address,omitempty" db:"ip_address"`
	ExpiresAt        time.Time  `json:"expires_at" db:"expires_at"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	LastUsedAt       time.Time  `json:"last_used_at" db:"last_used_at"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty" db:"revoked_at"`
}

func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

func (s *Session) IsRevoked() bool {
	return s.RevokedAt != nil
}

func (s *Session) IsValid() bool {
	return !s.IsExpired() && !s.IsRevoked()
}

type OAuthState struct {
	State       string    `db:"state"`
	RedirectURL string    `db:"redirect_url"`
	CreatedAt   time.Time `db:"created_at"`
	ExpiresAt   time.Time `db:"expires_at"`
}
