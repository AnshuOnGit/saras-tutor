package agents

import (
	"context"
	"fmt"
	"log"
	"strings"

	"saras-tutor/a2a"
	"saras-tutor/db"
	"saras-tutor/llm"
)

// SolverAgent takes a question (text) and streams a step-by-step solution.
type SolverAgent struct {
	llmClient *llm.Client
	store     *db.Store
}

// NewSolverAgent creates the solver agent.
func NewSolverAgent(llmClient *llm.Client, store *db.Store) *SolverAgent {
	return &SolverAgent{llmClient: llmClient, store: store}
}

// Card returns agent metadata.
func (a *SolverAgent) Card() a2a.AgentCard {
	return a2a.AgentCard{
		ID:          "solver",
		Name:        "Solver Agent",
		Description: "Solves academic questions step-by-step.",
		Skills:      []string{"solve", "explain"},
	}
}

const solverSystemPrompt = "You are an expert academic tutor. Solve the given question step-by-step.\n" +
	"Be clear and concise. Show your working. If the question is ambiguous, state your assumptions.\n\n" +
	"FORMATTING RULES — you MUST follow these:\n" +
	"- Write your response in well-structured Markdown.\n" +
	"- Use headings (##, ###) to separate major steps.\n" +
	"- For ALL mathematical expressions use LaTeX with dollar-sign delimiters:\n" +
	"  • Inline math: $...$ (e.g. $x = \\frac{-b \\pm \\sqrt{b^2-4ac}}{2a}$)\n" +
	"  • Display math: $$...$$ on its own line (e.g. $$\\int_0^1 x^2\\,dx = \\frac{1}{3}$$)\n" +
	"- NEVER use \\( \\) or \\[ \\] delimiters — always use $ and $$ only.\n" +
	"- For diagrams or flowcharts use fenced Mermaid blocks: ```mermaid ... ```\n" +
	"- Use numbered lists for sequential steps.\n" +
	"- Use bold for key results and boxed answers: **Answer: $...$**\n" +
	"- Do NOT wrap your response in JSON or code fences. Output clean Markdown directly."

// Handle processes a task synchronously (collects full response).
func (a *SolverAgent) Handle(ctx context.Context, task *a2a.Task) (*a2a.Task, error) {
	var parts []string
	for _, p := range task.Input.Parts {
		if p.Type == "text" {
			parts = append(parts, p.Text)
		}
	}

	messages := []llm.ChatMessage{
		{Role: "system", Content: solverSystemPrompt},
		{Role: "user", Content: strings.Join(parts, "\n")},
	}

	raw, err := a.llmClient.Complete(ctx, messages)
	if err != nil {
		task.State = a2a.TaskStateFailed
		return task, fmt.Errorf("solver: %w", err)
	}

	task.State = a2a.TaskStateCompleted
	task.Output = &a2a.Message{
		Role:  "agent",
		Parts: []a2a.Part{a2a.TextPart(strings.TrimSpace(raw.Content))},
	}
	return task, nil
}

// HandleStream streams the solution token-by-token.
// The router builds the input (text ± image) and this method streams the LLM response.
// NOTE: does NOT close `out`; the caller (router) owns the channel.
func (a *SolverAgent) HandleStream(ctx context.Context, task *a2a.Task, out chan<- a2a.StreamEvent) {
	out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateWorking}

	var textParts []string
	var imageURL string
	for _, p := range task.Input.Parts {
		if p.Type == "text" {
			textParts = append(textParts, p.Text)
		}
		if p.Type == "image" && p.ImageURL != "" {
			imageURL = p.ImageURL
		}
	}
	question := strings.Join(textParts, "\n")

	// --- Stream the full solution ---
	var messages []llm.ChatMessage
	if imageURL != "" {
		// Vision-enhanced solving: system prompt + text + image
		messages = []llm.ChatMessage{
			{Role: "system", Content: solverSystemPrompt},
			{
				Role: "user",
				Content: []llm.ContentPart{
					{Type: "text", Text: question},
					{Type: "image_url", ImageURL: &llm.ImageURL{URL: imageURL, Detail: "high"}},
				},
			},
		}
		log.Printf("[solver] streaming solution with image (vision mode)")
	} else {
		messages = []llm.ChatMessage{
			{Role: "system", Content: solverSystemPrompt},
			{Role: "user", Content: question},
		}
		log.Printf("[solver] streaming solution text-only")
	}

	err := a.llmClient.StreamComplete(ctx, messages, func(token string) error {
		out <- a2a.StreamEvent{
			Type: "artifact",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.TextPart(token)},
			},
		}
		return nil
	})

	if err != nil {
		out <- a2a.StreamEvent{Type: "error", Error: err.Error()}
	}
}
