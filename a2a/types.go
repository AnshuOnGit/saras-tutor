// Package a2a defines types for the Agent-to-Agent (A2A) communication protocol.
//
// Architecture: Orchestration pattern
//
//	The SupervisorAgent is the single orchestrator. It receives every user
//	request, classifies intent, decides the execution plan, and dispatches
//	sub-tasks to specialised agents in the order it determines. Sub-agents
//	never call each other directly; all routing goes through the supervisor.
//
//	This gives us:
//	  - Central observability (every transition is logged in one place)
//	  - Deterministic sequencing controlled by the supervisor
//	  - Easy addition of new agents without touching existing ones
package a2a

import "time"

// --- Task lifecycle ---

// TaskState represents where a task currently sits in the A2A flow.
type TaskState string

const (
	TaskStateSubmitted   TaskState = "submitted"
	TaskStateWorking     TaskState = "working"
	TaskStateCompleted   TaskState = "completed"
	TaskStateFailed      TaskState = "failed"
	TaskStateInputNeeded TaskState = "input-needed"
)

// --- Content parts ---

// Part is a polymorphic content block inside a message.
type Part struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"`
}

// TextPart is a convenience constructor.
func TextPart(text string) Part {
	return Part{Type: "text", Text: text}
}

// ImagePart is a convenience constructor.
func ImagePart(url string) Part {
	return Part{Type: "image", ImageURL: url}
}

// --- Message ---

// Message is the A2A envelope exchanged between agents.
type Message struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

// --- Task ---

// Task is a unit of work dispatched from the supervisor to a sub-agent.
type Task struct {
	ID        string            `json:"id"`
	AgentID   string            `json:"agent_id"`
	State     TaskState         `json:"state"`
	Input     Message           `json:"input"`
	Output    *Message          `json:"output,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// --- Streaming events ---

// StreamEvent is sent over SSE while a task is in progress.
type StreamEvent struct {
	// Type is one of: "status", "artifact", "transition", "metadata", "error".
	Type    string    `json:"type"`
	State   TaskState `json:"state,omitempty"`
	Message *Message  `json:"message,omitempty"`
	Error   string    `json:"error,omitempty"`

	// Transition metadata (populated when Type == "transition")
	FromAgent string `json:"from_agent,omitempty"`
	ToAgent   string `json:"to_agent,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	Reason    string `json:"reason,omitempty"`

	// Metadata (populated when Type == "metadata")
	// Used for confidence stats, model info, token usage, etc.
	Meta map[string]interface{} `json:"meta,omitempty"`
}

// --- Agent Card (discovery) ---

// AgentCard advertises an agent's capabilities to the supervisor.
type AgentCard struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Skills      []string `json:"skills"`
}
