package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate creates the database schema. Assumes a fresh database.
func Migrate(pool *pgxpool.Pool) error {
	ddl := `
-- ── Core messaging ──

CREATE TABLE IF NOT EXISTS conversations (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    session_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_conversations_user_session
    ON conversations(user_id, session_id);

CREATE TABLE IF NOT EXISTS messages (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    role            TEXT NOT NULL,
    content         TEXT NOT NULL,
    content_type    TEXT NOT NULL DEFAULT 'text',
    agent           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_messages_conversation
    ON messages(conversation_id, created_at);

CREATE TABLE IF NOT EXISTS images (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    message_id      TEXT NOT NULL REFERENCES messages(id),
    filename        TEXT NOT NULL,
    mime_type       TEXT NOT NULL,
    data            BYTEA NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_images_conversation
    ON images(conversation_id);

-- ── Topic taxonomy (JEE + NEET syllabus) ──

CREATE TABLE IF NOT EXISTS subjects (
    subject_id  BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS chapters (
    chapter_id  BIGSERIAL PRIMARY KEY,
    subject_id  BIGINT NOT NULL REFERENCES subjects(subject_id),
    name        TEXT NOT NULL,
    exam_target TEXT NOT NULL DEFAULT 'BOTH' CHECK (exam_target IN ('JEE','NEET','BOTH')),
    sort_order  INT NOT NULL DEFAULT 0,
    UNIQUE(subject_id, name)
);

CREATE TABLE IF NOT EXISTS topics (
    topic_id        BIGSERIAL PRIMARY KEY,
    chapter_id      BIGINT NOT NULL REFERENCES chapters(chapter_id),
    name            TEXT NOT NULL,
    exam_target     TEXT NOT NULL DEFAULT 'BOTH' CHECK (exam_target IN ('JEE','NEET','BOTH')),
    UNIQUE(chapter_id, name)
);

CREATE INDEX IF NOT EXISTS idx_chapters_subject ON chapters(subject_id);
CREATE INDEX IF NOT EXISTS idx_topics_chapter   ON topics(chapter_id);

-- ── Interactions: short-term memory for each question lifecycle ──

CREATE TABLE IF NOT EXISTS interactions (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    question_text   TEXT NOT NULL,
    image_id        TEXT NOT NULL DEFAULT '',
    subject_id      BIGINT REFERENCES subjects(subject_id),
    difficulty      INT  NOT NULL DEFAULT 0,
    problem_text    TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL DEFAULT 'new',
    hint_level      INT  NOT NULL DEFAULT 0,
    exit_reason     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_interactions_conv_state
    ON interactions(conversation_id, state);

-- ── Student profiles: long-term memory across sessions ──

CREATE TABLE IF NOT EXISTS student_profiles (
    user_id         TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    total_questions INT  NOT NULL DEFAULT 0,
    aggr_stats      JSONB NOT NULL DEFAULT '[]'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_student_profiles_user
    ON student_profiles(user_id);

-- ── Student attempts: tracks student work between hints ──

CREATE TABLE IF NOT EXISTS student_attempts (
    attempt_id     BIGSERIAL PRIMARY KEY,
    interaction_id TEXT NOT NULL REFERENCES interactions(id),
    user_id        TEXT NOT NULL,
    hint_index     INT  NOT NULL CHECK (hint_index BETWEEN 1 AND 3),
    student_message TEXT NOT NULL,
    evaluator_json JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_student_attempts_interaction
    ON student_attempts(interaction_id);

CREATE INDEX IF NOT EXISTS idx_student_attempts_user_time
    ON student_attempts(user_id, created_at DESC);

-- ── Many-to-many: interaction ↔ topics ──

CREATE TABLE IF NOT EXISTS interaction_topics (
    interaction_id TEXT NOT NULL REFERENCES interactions(id),
    topic_id       BIGINT NOT NULL REFERENCES topics(topic_id),
    confidence     REAL NOT NULL DEFAULT 1.0,
    PRIMARY KEY(interaction_id, topic_id)
);

CREATE INDEX IF NOT EXISTS idx_interaction_topics_topic ON interaction_topics(topic_id);
`
	if _, err := pool.Exec(context.Background(), ddl); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
