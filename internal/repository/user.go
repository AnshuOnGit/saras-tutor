package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"saras-tutor/internal/models"
)

var (
	ErrUserNotFound = errors.New("user not found")
	ErrUserExists   = errors.New("user already exists")
)

type UserRepository struct {
	db *pgxpool.Pool
}

func NewUserRepository(db *pgxpool.Pool) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) Create(ctx context.Context, user *models.User) error {
	query := `
		INSERT INTO users (email, name, picture_url, google_id, role, email_verified)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at
	`

	err := r.db.QueryRow(ctx, query,
		user.Email,
		user.Name,
		user.PictureURL,
		user.GoogleID,
		user.Role,
		user.EmailVerified,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)

	return err
}

func (r *UserRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	query := `
		SELECT id, email, name, picture_url, google_id, role, is_active,
		       email_verified, last_login_at, created_at, updated_at
		FROM users
		WHERE id = $1
	`

	user := &models.User{}
	err := r.db.QueryRow(ctx, query, id).Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.PictureURL,
		&user.GoogleID,
		&user.Role,
		&user.IsActive,
		&user.EmailVerified,
		&user.LastLoginAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}

	return user, err
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*models.User, error) {
	query := `
		SELECT id, email, name, picture_url, google_id, role, is_active,
		       email_verified, last_login_at, created_at, updated_at
		FROM users
		WHERE email = $1
	`

	user := &models.User{}
	err := r.db.QueryRow(ctx, query, email).Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.PictureURL,
		&user.GoogleID,
		&user.Role,
		&user.IsActive,
		&user.EmailVerified,
		&user.LastLoginAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}

	return user, err
}

func (r *UserRepository) GetByGoogleID(ctx context.Context, googleID string) (*models.User, error) {
	query := `
		SELECT id, email, name, picture_url, google_id, role, is_active,
		       email_verified, last_login_at, created_at, updated_at
		FROM users
		WHERE google_id = $1
	`

	user := &models.User{}
	err := r.db.QueryRow(ctx, query, googleID).Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.PictureURL,
		&user.GoogleID,
		&user.Role,
		&user.IsActive,
		&user.EmailVerified,
		&user.LastLoginAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}

	return user, err
}

func (r *UserRepository) Update(ctx context.Context, user *models.User) error {
	query := `
		UPDATE users
		SET email = $2, name = $3, picture_url = $4, role = $5,
		    is_active = $6, email_verified = $7, last_login_at = $8
		WHERE id = $1
		RETURNING updated_at
	`

	err := r.db.QueryRow(ctx, query,
		user.ID,
		user.Email,
		user.Name,
		user.PictureURL,
		user.Role,
		user.IsActive,
		user.EmailVerified,
		user.LastLoginAt,
	).Scan(&user.UpdatedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}

	return err
}

func (r *UserRepository) UpdateLastLogin(ctx context.Context, userID uuid.UUID) error {
	query := `UPDATE users SET last_login_at = $2 WHERE id = $1`
	_, err := r.db.Exec(ctx, query, userID, time.Now())
	return err
}

func (r *UserRepository) UpsertFromGoogle(ctx context.Context, googleUser *models.GoogleUserInfo) (*models.User, bool, error) {
	// Try to find existing user by Google ID
	existingUser, err := r.GetByGoogleID(ctx, googleUser.ID)
	if err == nil {
		// Update existing user info
		existingUser.Email = googleUser.Email
		existingUser.Name = googleUser.Name
		if googleUser.Picture != "" {
			existingUser.PictureURL = &googleUser.Picture
		}
		existingUser.EmailVerified = googleUser.VerifiedEmail
		now := time.Now()
		existingUser.LastLoginAt = &now

		if err := r.Update(ctx, existingUser); err != nil {
			return nil, false, err
		}
		return existingUser, false, nil
	}

	if !errors.Is(err, ErrUserNotFound) {
		return nil, false, err
	}

	// Create new user
	var pictureURL *string
	if googleUser.Picture != "" {
		pictureURL = &googleUser.Picture
	}

	newUser := &models.User{
		Email:         googleUser.Email,
		Name:          googleUser.Name,
		PictureURL:    pictureURL,
		GoogleID:      googleUser.ID,
		Role:          models.RoleStudent,
		IsActive:      true,
		EmailVerified: googleUser.VerifiedEmail,
	}

	if err := r.Create(ctx, newUser); err != nil {
		return nil, false, err
	}

	return newUser, true, nil
}
