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

// HintLevel controls how much help the student receives.
// The system starts at Level1 and escalates only when the student asks.
type HintLevel int

const (
	HintLevel1        HintLevel = 1 // Gentle nudge — identify relevant concept/formula
	HintLevel2        HintLevel = 2 // Stronger hint — outline approach, first step
	HintLevel3        HintLevel = 3 // Detailed walkthrough — most of the working, student fills gaps
	HintLevelSolution HintLevel = 4 // Full solution (only when student explicitly asks)
)

// HintAgent provides progressive hints without giving away the full answer.
type HintAgent struct {
	llmClient *llm.Client
	store     *db.Store
}

// NewHintAgent creates the hint agent.
func NewHintAgent(llmClient *llm.Client, store *db.Store) *HintAgent {
	return &HintAgent{llmClient: llmClient, store: store}
}

// Card returns agent metadata.
func (a *HintAgent) Card() a2a.AgentCard {
	return a2a.AgentCard{
		ID:          "hint",
		Name:        "Hint Agent",
		Description: "Provides progressive hints for a problem without revealing the answer.",
		Skills:      []string{"hint", "guided_learning"},
	}
}

// --- Hint prompts by level ---

const hintLevel1Prompt = `You are a supportive tutor helping a JEE/NEET student. The student has asked about a problem.

YOUR ROLE: Give a **Level 1 hint** — a gentle nudge to help them think in the right direction.

RULES:
- Identify the relevant topic and key concept/formula needed.
- Ask the student a guiding question that leads them toward the approach.
- Do NOT show any calculations or steps.
- Do NOT reveal the answer.
- Keep it brief (3-5 sentences max).
- End with an encouraging question like "Can you try applying this?" or "What do you get when you use this formula?"

FORMATTING:
- Use Markdown with $ and $$ for math (NEVER \( \) or \[ \]).
- Bold key terms.

QUESTION:
%s

Remember: You are helping them THINK, not solving for them.`

const hintLevel2Prompt = `You are a supportive tutor helping a JEE/NEET student. They've already received a gentle hint but need more help.

YOUR ROLE: Give a **Level 2 hint** — outline the approach and show the first step.

RULES:
- Name the specific method/technique to use.
- Show the first 1-2 steps of the solution (setup only).
- Leave the main calculation for the student.
- Do NOT reveal the final answer.
- Ask them to complete the next step.

FORMATTING:
- Use Markdown with $ and $$ for math (NEVER \( \) or \[ \]).
- Use numbered steps.
- Bold key results.

QUESTION:
%s

Remember: Show just enough to unblock them, not everything.`

const hintLevel3Prompt = `You are a supportive tutor helping a JEE/NEET student. They've asked for more help after two hints.

YOUR ROLE: Give a **Level 3 hint** — a detailed walkthrough with most steps shown.

RULES:
- Walk through the solution method step by step.
- Show all the working EXCEPT the final calculation/answer.
- Leave the last step for the student to complete.
- Clearly tell them what final step they need to do.
- Be encouraging.

FORMATTING:
- Use Markdown with $ and $$ for math (NEVER \( \) or \[ \]).
- Use numbered steps with headings.
- Bold the final instruction.

QUESTION:
%s

Remember: They're almost there — give them the satisfaction of finishing it.`

// GetPromptForLevel returns the appropriate system prompt for the given hint level.
func GetPromptForLevel(level HintLevel) string {
	switch level {
	case HintLevel1:
		return hintLevel1Prompt
	case HintLevel2:
		return hintLevel2Prompt
	case HintLevel3:
		return hintLevel3Prompt
	default:
		return "" // Level 4 = full solution, handled by solver
	}
}

// Handle processes a hint task synchronously.
// The hint level is read from task.Metadata["hint_level"] (defaults to "1").
func (a *HintAgent) Handle(ctx context.Context, task *a2a.Task) (*a2a.Task, error) {
	level := parseHintLevel(task.Metadata)

	var parts []string
	for _, p := range task.Input.Parts {
		if p.Type == "text" {
			parts = append(parts, p.Text)
		}
	}
	question := strings.Join(parts, "\n")

	prompt := GetPromptForLevel(level)
	if prompt == "" {
		// Level 4 = full solution — this shouldn't reach hint agent
		task.State = a2a.TaskStateFailed
		return task, fmt.Errorf("hint level %d should be handled by solver", level)
	}

	messages := []llm.ChatMessage{
		{Role: "user", Content: fmt.Sprintf(prompt, question)},
	}

	raw, err := a.llmClient.Complete(ctx, messages)
	if err != nil {
		task.State = a2a.TaskStateFailed
		return task, fmt.Errorf("hint: %w", err)
	}

	slog.Info("hint generated", "level", level, "question_len", len(question), "response_len", len(raw.Content))

	task.State = a2a.TaskStateCompleted
	task.Output = &a2a.Message{
		Role:  "agent",
		Parts: []a2a.Part{a2a.TextPart(strings.TrimSpace(raw.Content))},
	}
	return task, nil
}

// HandleStream streams the hint response token-by-token.
// If the task input contains image parts, they are included as vision content
// alongside the text prompt (used for confidence-retry with image fallback).
func (a *HintAgent) HandleStream(ctx context.Context, task *a2a.Task, out chan<- a2a.StreamEvent) {
	out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateWorking}

	level := parseHintLevel(task.Metadata)

	var textParts []string
	for _, p := range task.Input.Parts {
		if p.Type == "text" {
			textParts = append(textParts, p.Text)
		}
	}
	question := strings.Join(textParts, "\n")

	prompt := GetPromptForLevel(level)
	if prompt == "" {
		out <- a2a.StreamEvent{Type: "error", Error: "invalid hint level for hint agent"}
		return
	}

	// Hints always work from extracted text — images are consumed only
	// by the image_extraction agent.
	messages := []llm.ChatMessage{
		{Role: "user", Content: fmt.Sprintf(prompt, question)},
	}
	slog.Info("hint: streaming", "level", level)

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

// parseHintLevel reads the hint level from task metadata.
func parseHintLevel(metadata map[string]string) HintLevel {
	if metadata == nil {
		return HintLevel1
	}
	switch metadata["hint_level"] {
	case "2":
		return HintLevel2
	case "3":
		return HintLevel3
	case "4":
		return HintLevelSolution
	default:
		return HintLevel1
	}
}
