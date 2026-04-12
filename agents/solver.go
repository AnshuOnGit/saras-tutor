package agents

import (
	"context"
	"fmt"
	"log/slog"
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

const solverSystemPrompt = `You are an expert JEE Advanced / NEET tutor and problem solver. Solve the given question with full mathematical rigour.

SOLVING APPROACH:
1. **Identify** the core concept, relevant laws/theorems, and what is being asked.
2. **Set up** the problem: define variables, draw free-body diagrams (describe them), write governing equations.
3. **Solve** step by step. Show every algebraic/calculus manipulation. Do not skip steps.
4. **Verify** your answer:
   - Substitute back to check consistency.
   - Check dimensions/units.
   - Check limiting/special cases (e.g. if a parameter → 0 or → ∞, does the answer make sense?).
   - For MCQ: confirm your answer matches one of the options.
5. **State the final answer** clearly.

MATH RIGOUR:
- Justify each step (cite the theorem, identity, or rule you use).
- For vector problems: be explicit about coordinate systems and sign conventions.
- For calculus: state substitution variables and limits of integration.
- For combinatorics/probability: define the sample space.
- If the question is ambiguous, state your assumption before proceeding.

FORMATTING RULES (STRICT):
- Well-structured Markdown with ## and ### headings.
- ALL math in LaTeX with $ (inline) and $$ (display). NEVER use \( \) or \[ \].
- Numbered steps for the solution.
- Bold the final answer: **Answer: $...$**
- Do NOT wrap output in JSON or code fences.
- Keep explanations concise but complete — no hand-waving.`

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
	for _, p := range task.Input.Parts {
		if p.Type == "text" {
			textParts = append(textParts, p.Text)
		}
	}
	question := strings.Join(textParts, "\n")

	// Solver always works from extracted text — images are consumed only
	// by the image_extraction agent.
	messages := []llm.ChatMessage{
		{Role: "system", Content: solverSystemPrompt},
		{Role: "user", Content: question},
	}
	slog.Info("solver: streaming")

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
