package db

import (
	"context"
	"fmt"

	"saras-tutor/internal/logger"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migration is a named SQL block executed in order.
type migration struct {
	name string
	sql  string
}

// migrations returns the ordered list of all schema migrations.
func migrations() []migration {
	return []migration{
		{
			name: "001_create_extractions_table",
			sql: `
CREATE TABLE IF NOT EXISTS extractions (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL DEFAULT '',
    image_url       TEXT NOT NULL,
    extracted_text  TEXT NOT NULL,
    model_id        TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE extractions ADD COLUMN IF NOT EXISTS user_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_extractions_session
    ON extractions(session_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_extractions_user
    ON extractions(user_id, created_at DESC);
`,
		},
		{
			name: "002_create_studio_messages_table",
			sql: `
CREATE TABLE IF NOT EXISTS studio_messages (
    id                      TEXT PRIMARY KEY,
    conversation_id         TEXT NOT NULL,
    user_id                 TEXT,
    role                    TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    intent                  TEXT NOT NULL CHECK (intent IN ('question', 'attempt', 'solve', 'hint', 'evaluate', 'followup')),
    content                 TEXT NOT NULL,
    question_extraction_id  TEXT REFERENCES extractions(id),
    attempt_extraction_id   TEXT REFERENCES extractions(id),
    meta                    JSONB DEFAULT '{}'::jsonb,
    created_at              TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_studio_messages_conversation
    ON studio_messages(conversation_id, created_at);

CREATE INDEX IF NOT EXISTS idx_studio_messages_user
    ON studio_messages(user_id, created_at DESC);
`,
		},
		{
			name: "003_create_users_table",
			sql: `
CREATE TABLE IF NOT EXISTS users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           VARCHAR(255) UNIQUE NOT NULL,
    name            VARCHAR(255) NOT NULL,
    picture_url     TEXT,
    google_id       VARCHAR(255) UNIQUE NOT NULL,
    role            VARCHAR(50) DEFAULT 'student' NOT NULL,
    is_active       BOOLEAN DEFAULT true NOT NULL,
    email_verified  BOOLEAN DEFAULT false NOT NULL,
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    updated_at      TIMESTAMPTZ DEFAULT NOW() NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_google_id ON users(google_id);
`,
		},
		{
			name: "004_create_sessions_table",
			sql: `
CREATE TABLE IF NOT EXISTS sessions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash  VARCHAR(64) UNIQUE NOT NULL,
    user_agent          TEXT,
    ip_address          VARCHAR(45),
    expires_at          TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    last_used_at        TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    revoked_at          TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_refresh_token ON sessions(refresh_token_hash);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);
`,
		},
		{
			name: "005_create_oauth_states_table",
			sql: `
CREATE TABLE IF NOT EXISTS oauth_states (
    state        VARCHAR(64) PRIMARY KEY,
    redirect_url TEXT,
    created_at   TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_oauth_states_expires_at ON oauth_states(expires_at);
`,
		},
		{
			name: "006_create_updated_at_trigger",
			sql: `
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS update_users_updated_at ON users;
CREATE TRIGGER update_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
`,
		},
		{
			name: "007_link_extractions_messages_to_users",
			sql: `
-- Link extractions to authenticated users (nullable for anonymous/legacy rows)
ALTER TABLE extractions ADD COLUMN IF NOT EXISTS auth_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_extractions_auth_user ON extractions(auth_user_id, created_at DESC);

-- Link studio_messages to authenticated users
ALTER TABLE studio_messages ADD COLUMN IF NOT EXISTS auth_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_studio_messages_auth_user ON studio_messages(auth_user_id, created_at DESC);
`,
		},
	}
}

// Migrate runs all schema migrations in order.
func Migrate(pool *pgxpool.Pool) error {
	ctx := context.Background()
	for _, m := range migrations() {
		logger.Info().Str("migration", m.name).Msg("running migration")
		if _, err := pool.Exec(ctx, m.sql); err != nil {
			return fmt.Errorf("migration %s failed: %w", m.name, err)
		}
	}
	logger.Info().Msg("all migrations completed successfully")
	return nil
}

// CleanupExpiredData removes expired sessions and OAuth states.
func CleanupExpiredData(pool *pgxpool.Pool) error {
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < NOW() OR revoked_at IS NOT NULL`); err != nil {
		return fmt.Errorf("cleanup sessions: %w", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM oauth_states WHERE expires_at < NOW()`); err != nil {
		return fmt.Errorf("cleanup oauth_states: %w", err)
	}

	return nil
}
