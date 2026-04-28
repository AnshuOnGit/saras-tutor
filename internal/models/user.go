package models

import (
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleStudent Role = "student"
	RoleTeacher Role = "teacher"
	RoleAdmin   Role = "admin"
)

type User struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	Email         string     `json:"email" db:"email"`
	Name          string     `json:"name" db:"name"`
	PictureURL    *string    `json:"picture_url,omitempty" db:"picture_url"`
	GoogleID      string     `json:"-" db:"google_id"`
	Role          Role       `json:"role" db:"role"`
	IsActive      bool       `json:"is_active" db:"is_active"`
	EmailVerified bool       `json:"email_verified" db:"email_verified"`
	LastLoginAt   *time.Time `json:"last_login_at,omitempty" db:"last_login_at"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}

type UserProfile struct {
	ID            uuid.UUID `json:"id"`
	Email         string    `json:"email"`
	Name          string    `json:"name"`
	PictureURL    *string   `json:"picture_url,omitempty"`
	Role          Role      `json:"role"`
	EmailVerified bool      `json:"email_verified"`
}

func (u *User) ToProfile() *UserProfile {
	return &UserProfile{
		ID:            u.ID,
		Email:         u.Email,
		Name:          u.Name,
		PictureURL:    u.PictureURL,
		Role:          u.Role,
		EmailVerified: u.EmailVerified,
	}
}

// GoogleUserInfo represents the user info from Google's API.
type GoogleUserInfo struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
	Name          string `json:"name"`
	GivenName     string `json:"given_name"`
	FamilyName    string `json:"family_name"`
	Picture       string `json:"picture"`
}
