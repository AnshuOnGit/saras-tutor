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

// --- Single unified hint prompt ---
//
// Previously there were three separate prompts for Level1/2/3. That model has
// been removed: every hint call uses the same prompt and is made slightly more
// revealing by passing the hint_count (how many hints have already been given
// for this question). The prompt explicitly asks the model to go deeper than
// the previous hints without solving the problem.

const hintUnifiedPrompt = `You are a supportive tutor helping a JEE/NEET student. The student is stuck on a problem and has asked for help.

INTERNAL CONTEXT (do NOT mention this in your reply):
- This is the %dᵗʰ hint that has been generated for this question.
- Each subsequent hint should be slightly MORE revealing than the previous one, WITHOUT ever giving the final numerical answer.

DEPTH GUIDELINES (based on hint number, but NEVER state the number):
- 1st hint: Identify ONE relevant concept/formula. Ask a guiding question. No calculations.
- 2nd hint: Name the method. Show setup / first step only. No final answer.
- 3rd hint: Walk through most steps. Stop before the final computation.
- 4th hint or later: Show everything except the last arithmetic step. Leave the final number for the student.

STRICT RULES (apply to every hint):
- NEVER reveal the final numerical answer.
- NEVER carry out ALL steps — always leave the last calculation for the student.
- NEVER write headings like "Hint #1:", "Hint 2:", "First hint:", or any label that announces the hint number. Just give the hint directly.
- NEVER write meta-greetings like "Hi there!", "Great question!", "Let me help you with this". Start straight with the substance.
- Be encouraging in tone, end with a question or call-to-action.
- Use Markdown with $ and $$ for math (NEVER \\( \\) or \\[ \\]).
- Keep it under ~250 words.

QUESTION:
%s

CRITICAL: Stop once you've helped enough for this hint number. Do NOT solve the problem.`

// GetPromptForLevel returns the unified hint prompt. The level parameter is
// kept for backward compatibility with older call sites — it is now used only
// as the hint count baked into the prompt template.
func GetPromptForLevel(level HintLevel) string {
	if level <= 0 {
		level = HintLevel1
	}
	return fmt.Sprintf(hintUnifiedPrompt, int(level), "%s")
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

// parseHintLevel reads the hint level from task metadata. Any positive
// integer is accepted — higher numbers produce progressively more revealing
// hints (guided by the unified prompt).
func parseHintLevel(metadata map[string]string) HintLevel {
	if metadata == nil {
		return HintLevel1
	}
	var n int
	if _, err := fmt.Sscanf(metadata["hint_level"], "%d", &n); err == nil && n > 0 {
		return HintLevel(n)
	}
	return HintLevel1
}
