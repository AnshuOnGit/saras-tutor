package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"saras-tutor/models"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides data-access methods backed by Postgres.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new Store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// --- Conversations ---

// CreateConversation inserts a new conversation row.
func (s *Store) CreateConversation(ctx context.Context, c *models.Conversation) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO conversations (id, user_id, session_id, created_at) VALUES ($1, $2, $3, $4)`,
		c.ID, c.UserID, c.SessionID, c.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert conversation: %w", err)
	}
	return nil
}

// GetConversation retrieves the latest conversation for a user+session pair.
func (s *Store) GetConversation(ctx context.Context, userID, sessionID string) (*models.Conversation, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, user_id, session_id, created_at
		 FROM conversations
		 WHERE user_id = $1 AND session_id = $2
		 ORDER BY created_at DESC LIMIT 1`,
		userID, sessionID,
	)
	var c models.Conversation
	if err := row.Scan(&c.ID, &c.UserID, &c.SessionID, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// --- Messages ---

// SaveMessage persists a single message.
func (s *Store) SaveMessage(ctx context.Context, m *models.Message) error {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO messages (id, conversation_id, role, content, content_type, agent, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		m.ID, m.ConversationID, m.Role, m.Content, m.ContentType, m.Agent, m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	return nil
}

// GetMessages returns up to `limit` messages for a conversation, oldest first.
func (s *Store) GetMessages(ctx context.Context, conversationID string, limit int) ([]models.Message, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, conversation_id, role, content, content_type, agent, created_at
		 FROM messages
		 WHERE conversation_id = $1
		 ORDER BY created_at ASC
		 LIMIT $2`,
		conversationID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var msgs []models.Message
	for rows.Next() {
		var m models.Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.ContentType, &m.Agent, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// --- Images ---

// SaveImage persists an uploaded image as a BYTEA blob.
func (s *Store) SaveImage(ctx context.Context, img *models.Image) error {
	if img.CreatedAt.IsZero() {
		img.CreatedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO images (id, conversation_id, message_id, filename, mime_type, data, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		img.ID, img.ConversationID, img.MessageID, img.Filename, img.MimeType, img.Data, img.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert image: %w", err)
	}
	return nil
}

// GetImage retrieves an image by ID.
func (s *Store) GetImage(ctx context.Context, id string) (*models.Image, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, conversation_id, message_id, filename, mime_type, data, created_at
		 FROM images WHERE id = $1`, id,
	)
	var img models.Image
	if err := row.Scan(&img.ID, &img.ConversationID, &img.MessageID, &img.Filename, &img.MimeType, &img.Data, &img.CreatedAt); err != nil {
		return nil, err
	}
	return &img, nil
}

// ── Taxonomy lookups ──

// LookupSubjectID returns the subject_id for a subject name, or nil if not found.
func (s *Store) LookupSubjectID(ctx context.Context, name string) *int64 {
	if name == "" {
		return nil
	}
	var id int64
	err := s.pool.QueryRow(ctx,
		`SELECT subject_id FROM subjects WHERE LOWER(name) = LOWER($1)`, name,
	).Scan(&id)
	if err != nil {
		return nil
	}
	return &id
}

// LookupTopicIDs returns topic_ids for a list of topic names.
// Topics may span multiple chapters; each name is matched globally.
func (s *Store) LookupTopicIDs(ctx context.Context, names []string) []int64 {
	if len(names) == 0 {
		return nil
	}
	var ids []int64
	for _, name := range names {
		var id int64
		err := s.pool.QueryRow(ctx,
			`SELECT topic_id FROM topics WHERE LOWER(name) = LOWER($1) LIMIT 1`, name,
		).Scan(&id)
		if err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// ── Interactions (short-term memory) ──

// CreateInteraction inserts a new interaction and its topic links.
func (s *Store) CreateInteraction(ctx context.Context, i *models.Interaction) error {
	if i.CreatedAt.IsZero() {
		i.CreatedAt = time.Now().UTC()
	}
	i.UpdatedAt = i.CreatedAt
	_, err := s.pool.Exec(ctx,
		`INSERT INTO interactions (id, conversation_id, question_text, image_id, subject_id, difficulty, problem_text, state, hint_level, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		i.ID, i.ConversationID, i.QuestionText, i.ImageID, i.SubjectID, i.Difficulty, i.ProblemText, i.State, i.HintLevel, i.CreatedAt, i.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert interaction: %w", err)
	}

	// Insert topic links into interaction_topics
	for _, topicID := range i.TopicIDs {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO interaction_topics (interaction_id, topic_id, confidence) VALUES ($1, $2, 1.0)
			 ON CONFLICT (interaction_id, topic_id) DO NOTHING`,
			i.ID, topicID,
		); err != nil {
			return fmt.Errorf("insert interaction_topic: %w", err)
		}
	}

	return nil
}

// GetActiveInteraction returns the most recent non-terminal interaction for a conversation.
// Returns nil, nil if there is no active interaction.
func (s *Store) GetActiveInteraction(ctx context.Context, conversationID string) (*models.Interaction, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, conversation_id, question_text, image_id, subject_id, difficulty, problem_text, state, hint_level, exit_reason, created_at, updated_at
		 FROM interactions
		 WHERE conversation_id = $1 AND state NOT IN ('solved', 'closed')
		 ORDER BY created_at DESC
		 LIMIT 1`,
		conversationID,
	)
	var i models.Interaction
	var exitReason *string
	if err := row.Scan(&i.ID, &i.ConversationID, &i.QuestionText, &i.ImageID, &i.SubjectID, &i.Difficulty, &i.ProblemText, &i.State, &i.HintLevel, &exitReason, &i.CreatedAt, &i.UpdatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get active interaction: %w", err)
	}
	if exitReason != nil {
		er := models.ExitReason(*exitReason)
		i.ExitReason = &er
	}

	// Load topic IDs from interaction_topics
	rows, err := s.pool.Query(ctx,
		`SELECT topic_id FROM interaction_topics WHERE interaction_id = $1`, i.ID,
	)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var tid int64
			if err := rows.Scan(&tid); err == nil {
				i.TopicIDs = append(i.TopicIDs, tid)
			}
		}
	}

	return &i, nil
}

// UpdateInteraction updates an interaction's state and hint_level.
func (s *Store) UpdateInteraction(ctx context.Context, id string, state models.InteractionState, hintLevel int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE interactions SET state = $1, hint_level = $2, updated_at = now() WHERE id = $3`,
		state, hintLevel, id,
	)
	if err != nil {
		return fmt.Errorf("update interaction: %w", err)
	}
	return nil
}

// CloseInteraction marks an interaction as terminal with an exit reason.
func (s *Store) CloseInteraction(ctx context.Context, id string, state models.InteractionState, reason models.ExitReason) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE interactions SET state = $1, exit_reason = $2, updated_at = now() WHERE id = $3`,
		state, reason, id,
	)
	if err != nil {
		return fmt.Errorf("close interaction: %w", err)
	}
	return nil
}

// CloseAllActive closes all non-terminal interactions for a conversation (used when a new question arrives).
func (s *Store) CloseAllActive(ctx context.Context, conversationID string, reason models.ExitReason) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE interactions SET state = 'closed', exit_reason = $1, updated_at = now()
		 WHERE conversation_id = $2 AND state NOT IN ('solved', 'closed')`,
		reason, conversationID,
	)
	if err != nil {
		return fmt.Errorf("close all active: %w", err)
	}
	return nil
}

// ── Student Profiles (long-term memory) ──

// GetOrCreateProfile returns the student profile, creating one if it doesn't exist.
func (s *Store) GetOrCreateProfile(ctx context.Context, userID string) (*models.StudentProfile, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT name, user_id, total_questions, aggr_stats
		 FROM student_profiles WHERE user_id = $1`, userID,
	)
	var p models.StudentProfile
	var aggrStatsRaw []byte
	if err := row.Scan(&p.Name, &p.UserID, &p.TotalQuestions, &aggrStatsRaw); err != nil {
		if err == pgx.ErrNoRows {
			// Create a new profile
			p = models.StudentProfile{
				Name:           fmt.Sprintf("prof_%s", userID),
				UserID:         userID,
				TotalQuestions: 0,
				AggrStats:      []models.ConversationAggrStat{},
			}
			_, err2 := s.pool.Exec(ctx,
				`INSERT INTO student_profiles (name, user_id, total_questions, aggr_stats) VALUES ($1, $2, $3, $4::jsonb)`,
				p.Name, p.UserID, p.TotalQuestions, `[]`,
			)
			if err2 != nil {
				return nil, fmt.Errorf("create profile: %w", err2)
			}
			return &p, nil
		}
		return nil, fmt.Errorf("get profile: %w", err)
	}
	if err := json.Unmarshal(aggrStatsRaw, &p.AggrStats); err != nil || p.AggrStats == nil {
		p.AggrStats = []models.ConversationAggrStat{}
	}
	return &p, nil
}

// IncrementProfileQuestions bumps total_questions by 1.
func (s *Store) IncrementProfileQuestions(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE student_profiles SET total_questions = total_questions + 1 WHERE user_id = $1`,
		userID,
	)
	return err
}

// AppendProfileQuestionStat appends one question under the conversation entry in aggr_stats.
func (s *Store) AppendProfileQuestionStat(ctx context.Context, userID, conversationID, chapter string, topics []string, difficulty int) error {
	topicSubtopics := make([]string, 0, len(topics))
	for _, topic := range topics {
		if topic == "" {
			continue
		}
		topicSubtopics = append(topicSubtopics, chapter+":"+topic)
	}
	if len(topicSubtopics) == 0 && chapter != "" {
		topicSubtopics = append(topicSubtopics, chapter+":")
	}

	entry := models.ProfileQuestionStat{
		TopicSubtopics:  topicSubtopics,
		DifficultyLevel: difficulty,
		HintLevel:       0,
		SelfSolved:      nil,
	}

	p, err := s.GetOrCreateProfile(ctx, userID)
	if err != nil {
		return fmt.Errorf("get profile for aggr_stats append: %w", err)
	}

	conversationFound := false
	for i := range p.AggrStats {
		if p.AggrStats[i].ConversationID == conversationID {
			p.AggrStats[i].Questions = append(p.AggrStats[i].Questions, entry)
			conversationFound = true
			break
		}
	}
	if !conversationFound {
		p.AggrStats = append(p.AggrStats, models.ConversationAggrStat{
			ConversationID: conversationID,
			Questions:      []models.ProfileQuestionStat{entry},
		})
	}

	aggrJSON, err := json.Marshal(p.AggrStats)
	if err != nil {
		return fmt.Errorf("marshal aggr_stats: %w", err)
	}

	_, err = s.pool.Exec(ctx,
		`UPDATE student_profiles
		 SET total_questions = total_questions + 1,
		     aggr_stats = $1::jsonb
		 WHERE user_id = $2`,
		string(aggrJSON), userID,
	)
	if err != nil {
		return fmt.Errorf("append aggr_stats entry: %w", err)
	}
	return nil
}

// UpdateProfileQuestionOutcome updates the latest unresolved question in a conversation.
func (s *Store) UpdateProfileQuestionOutcome(ctx context.Context, userID, conversationID string, hintsUsed int, selfSolved bool) error {
	p, err := s.GetOrCreateProfile(ctx, userID)
	if err != nil {
		return fmt.Errorf("get profile for aggr_stats outcome: %w", err)
	}

	conversationIdx := -1
	for i := range p.AggrStats {
		if p.AggrStats[i].ConversationID == conversationID {
			conversationIdx = i
			break
		}
	}
	if conversationIdx == -1 || len(p.AggrStats[conversationIdx].Questions) == 0 {
		return nil
	}

	questionIdx := -1
	for i := len(p.AggrStats[conversationIdx].Questions) - 1; i >= 0; i-- {
		if p.AggrStats[conversationIdx].Questions[i].SelfSolved == nil {
			questionIdx = i
			break
		}
	}
	if questionIdx == -1 {
		questionIdx = len(p.AggrStats[conversationIdx].Questions) - 1
	}

	p.AggrStats[conversationIdx].Questions[questionIdx].HintLevel = hintsUsed
	p.AggrStats[conversationIdx].Questions[questionIdx].SelfSolved = &selfSolved

	aggrJSON, err := json.Marshal(p.AggrStats)
	if err != nil {
		return fmt.Errorf("marshal aggr_stats outcome: %w", err)
	}

	_, err = s.pool.Exec(ctx,
		`UPDATE student_profiles
		 SET aggr_stats = $1::jsonb
		 WHERE user_id = $2`,
		string(aggrJSON), userID,
	)
	if err != nil {
		return fmt.Errorf("update aggr_stats outcome: %w", err)
	}
	return nil
}

// ── Student Attempts ──

// SaveStudentAttempt persists a student attempt with its evaluator rubric.
func (s *Store) SaveStudentAttempt(ctx context.Context, a *models.StudentAttempt) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	evalJSON, err := json.Marshal(a.EvaluatorJSON)
	if err != nil {
		return fmt.Errorf("marshal evaluator_json: %w", err)
	}
	row := s.pool.QueryRow(ctx,
		`INSERT INTO student_attempts (interaction_id, user_id, hint_index, student_message, evaluator_json, created_at)
		 VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		 RETURNING attempt_id`,
		a.InteractionID, a.UserID, a.HintIndex, a.StudentMessage, string(evalJSON), a.CreatedAt,
	)
	return row.Scan(&a.AttemptID)
}

// GetAttemptsByInteraction returns all attempts for a given interaction, oldest first.
func (s *Store) GetAttemptsByInteraction(ctx context.Context, interactionID string) ([]models.StudentAttempt, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT attempt_id, interaction_id, user_id, hint_index, student_message, evaluator_json, created_at
		 FROM student_attempts
		 WHERE interaction_id = $1
		 ORDER BY created_at ASC`,
		interactionID,
	)
	if err != nil {
		return nil, fmt.Errorf("query student_attempts: %w", err)
	}
	defer rows.Close()

	var attempts []models.StudentAttempt
	for rows.Next() {
		var a models.StudentAttempt
		var evalRaw []byte
		if err := rows.Scan(&a.AttemptID, &a.InteractionID, &a.UserID, &a.HintIndex, &a.StudentMessage, &evalRaw, &a.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(evalRaw, &a.EvaluatorJSON); err != nil {
			return nil, fmt.Errorf("unmarshal evaluator_json: %w", err)
		}
		attempts = append(attempts, a)
	}
	return attempts, nil
}
