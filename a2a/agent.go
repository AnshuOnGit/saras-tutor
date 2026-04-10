package a2a

import "context"

// Agent is the interface every agent in the system must implement.
// The supervisor dispatches Tasks to agents through this interface.
type Agent interface {
	// Card returns metadata about the agent (used for discovery / routing).
	Card() AgentCard

	// Handle processes a task synchronously and returns the result.
	Handle(ctx context.Context, task *Task) (*Task, error)

	// HandleStream processes a task and streams events via the channel.
	// The implementation must NOT close the channel; the caller owns its lifecycle.
	HandleStream(ctx context.Context, task *Task, out chan<- StreamEvent)
}
