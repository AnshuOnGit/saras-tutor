package models

import "time"

// Conversation groups messages for a given user + session.
type Conversation struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Message is a single chat turn persisted in the database.
type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	ContentType    string    `json:"content_type"`
	Agent          string    `json:"agent,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Image stores an uploaded image in the database.
type Image struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	MessageID      string    `json:"message_id"`
	Filename       string    `json:"filename"`
	MimeType       string    `json:"mime_type"`
	Data           []byte    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}
