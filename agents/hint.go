package agents

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

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

STRICT RULES:
- Identify the relevant topic and ONE key concept or formula needed.
- Ask the student a guiding question that leads them toward the approach.
- Do NOT show ANY calculations, steps, substitutions, or intermediate results.
- Do NOT solve the problem — not even partially.
- Do NOT expand series, evaluate integrals, or simplify expressions.
- MAXIMUM 3-5 sentences. If your response exceeds 100 words, you have said too much.
- End with an encouraging question like "Can you try applying this?" or "What do you get when you use this formula?"

FORMATTING:
- Use Markdown with $ and $$ for math (NEVER \( \) or \[ \]).
- Bold key terms.

QUESTION:
%s

CRITICAL: Your ENTIRE response must be under 100 words. You are helping them THINK, not solving for them. If you catch yourself writing a solution, STOP immediately.`

const hintLevel2Prompt = `You are a supportive tutor helping a JEE/NEET student. They've already received a gentle hint but need more help.

YOUR ROLE: Give a **Level 2 hint** — outline the approach and show the first step only.

STRICT RULES:
- Name the specific method/technique to use.
- Show ONLY the first 1-2 steps (setup/identification only).
- Do NOT carry out the full calculation.
- Do NOT reveal intermediate or final answers.
- Do NOT solve more than the first step.
- MAXIMUM 150 words.
- Ask them to complete the next step.

FORMATTING:
- Use Markdown with $ and $$ for math (NEVER \( \) or \[ \]).
- Use numbered steps.
- Bold key results.

QUESTION:
%s

CRITICAL: Stop after showing the setup. Do NOT continue into the solution. Show just enough to unblock them — nothing more.`

const hintLevel3Prompt = `You are a supportive tutor helping a JEE/NEET student. They've asked for more help after two hints.

YOUR ROLE: Give a **Level 3 hint** — a detailed walkthrough with most steps, but leave the final answer for the student.

STRICT RULES:
- Walk through the solution method step by step.
- Show all the working EXCEPT the final calculation/answer.
- STOP before computing the final numerical answer.
- Leave the last step for the student to complete.
- Clearly tell them what final step they need to do.
- Do NOT state the answer — let them compute it.
- Be encouraging.

FORMATTING:
- Use Markdown with $ and $$ for math (NEVER \( \) or \[ \]).
- Use numbered steps with headings.
- Bold the final instruction.

QUESTION:
%s

CRITICAL: Do NOT reveal the final answer. They're almost there — give them the satisfaction of finishing it.`

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

	llmStart := time.Now()
	raw, err := a.llmClient.Complete(ctx, messages)
	llmDuration := time.Since(llmStart)
	if err != nil {
		slog.Error("hint: LLM call failed",
			"model", a.llmClient.Model,
			"error", err,
			"elapsed_ms", llmDuration.Milliseconds())
		task.State = a2a.TaskStateFailed
		return task, fmt.Errorf("hint: %w", err)
	}

	slog.Info("hint: generated",
		"level", level,
		"model", raw.Model,
		"prompt_tokens", raw.Usage.PromptTokens,
		"completion_tokens", raw.Usage.CompletionTokens,
		"total_tokens", raw.Usage.TotalTokens,
		"response_len", len(raw.Content),
		"elapsed_ms", llmDuration.Milliseconds(),
		"elapsed_s", fmt.Sprintf("%.1f", llmDuration.Seconds()))

	// Strip <think>...</think> blocks from reasoning models (safety net)
	cleaned := stripThinkBlocks(raw.Content)

	task.State = a2a.TaskStateCompleted
	task.Output = &a2a.Message{
		Role:  "agent",
		Parts: []a2a.Part{a2a.TextPart(cleaned)},
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
	slog.Info("hint: streaming", "level", level, "model", a.llmClient.Model)

	// Strip <think>...</think> blocks from reasoning models (safety net).
	tf := newThinkFilter(func(text string) {
		out <- a2a.StreamEvent{
			Type: "artifact",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.TextPart(text)},
			},
		}
	})

	streamStart := time.Now()
	result, err := a.llmClient.StreamComplete(ctx, messages, func(token string) error {
		tf.Write(token)
		return nil
	})
	tf.Flush()
	streamDuration := time.Since(streamStart)
	if err != nil {
		slog.Error("hint: stream failed",
			"level", level,
			"model", a.llmClient.Model,
			"error", err,
			"elapsed_ms", streamDuration.Milliseconds())
		out <- a2a.StreamEvent{Type: "error", Error: err.Error()}
		return
	}
	if result != nil {
		slog.Info("hint: stream done",
			"level", level,
			"model", a.llmClient.Model,
			"prompt_tokens", result.Usage.PromptTokens,
			"completion_tokens", result.Usage.CompletionTokens,
			"total_tokens", result.Usage.TotalTokens,
			"elapsed_ms", streamDuration.Milliseconds(),
			"elapsed_s", fmt.Sprintf("%.1f", streamDuration.Seconds()))
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
