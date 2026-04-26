package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate creates the database schema for Studio.
func Migrate(pool *pgxpool.Pool) error {
	ddl := `
CREATE TABLE IF NOT EXISTS extractions (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL DEFAULT '',
    image_url       TEXT NOT NULL,
    extracted_text  TEXT NOT NULL,
    model_id        TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- backfill user_id for existing tables created before this column was added
ALTER TABLE extractions ADD COLUMN IF NOT EXISTS user_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_extractions_session
    ON extractions(session_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_extractions_user
    ON extractions(user_id, created_at DESC);
`
	if _, err := pool.Exec(context.Background(), ddl); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
