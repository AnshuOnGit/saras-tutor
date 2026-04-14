package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"saras-tutor/a2a"
	"saras-tutor/db"
	"saras-tutor/llm"
	"saras-tutor/models"
)

// AttemptEvaluatorAgent compares a student's submitted attempt against the
// expected intermediate goal for the current hint level and returns a
// structured rubric (models.EvaluatorResult).
type AttemptEvaluatorAgent struct {
	llmClient    *llm.Client
	visionClient *llm.Client // used when the student submits a photo
	store        *db.Store
}

// NewAttemptEvaluatorAgent creates the evaluator agent.
// visionClient is used for image-based evaluation; if nil, llmClient is used for both.
func NewAttemptEvaluatorAgent(llmClient, visionClient *llm.Client, store *db.Store) *AttemptEvaluatorAgent {
	if visionClient == nil {
		visionClient = llmClient
	}
	return &AttemptEvaluatorAgent{llmClient: llmClient, visionClient: visionClient, store: store}
}

// Card returns agent metadata.
func (a *AttemptEvaluatorAgent) Card() a2a.AgentCard {
	return a2a.AgentCard{
		ID:          "attempt_evaluator",
		Name:        "Attempt Evaluator Agent",
		Description: "Scores student work between hints and returns a structured rubric.",
		Skills:      []string{"evaluate", "rubric"},
	}
}

const evaluatorSystemPrompt = `You are an expert academic evaluator for JEE/NEET preparation.

Given:
- The ORIGINAL QUESTION the student is solving.
- The HINT LEVEL (1-3) they received before attempting.
- The STUDENT'S ATTEMPT (their working / answer). This may be typed text OR a photo of handwritten work on paper.

If the attempt is an image, carefully read the handwriting: extract equations, steps, diagrams, and the answer. Evaluate what you can read. If parts are illegible, note that in "errors" but do NOT penalize heavily — focus on what IS visible.

Your job: compare the attempt to what a correct solution at this stage should look like and produce a structured rubric.

SCORING GUIDE:
- 1.0: Fully correct — the student completed the expected step(s) accurately.
- 0.7-0.9: Mostly correct — right approach, minor arithmetic or sign errors.
- 0.4-0.6: Partially correct — understands the concept but made a significant error.
- 0.1-0.3: Shows some understanding but largely incorrect approach.
- 0.0: No meaningful progress or completely wrong approach.

WHAT COUNTS AS THE "EXPECTED INTERMEDIATE GOAL" PER HINT LEVEL:
- Hint 1: Student identifies the correct concept/formula and sets up the approach.
- Hint 2: Student completes the first 1-2 steps correctly (substitution, setup).
- Hint 3: Student completes most of the solution — only the final calculation may remain.

Respond with ONLY a JSON object (no code fences, no markdown, no explanation):
{"correct": <bool>, "score": <0.0-1.0>, "strengths": [<strings>], "errors": [<strings>], "missing_steps": [<strings>], "next_guidance": "<one sentence of tailored advice>", "hint_consumed": <int>}`

// Evaluate performs a non-streaming LLM call to assess a student attempt (text only).
func (a *AttemptEvaluatorAgent) Evaluate(ctx context.Context, question, studentAttempt string, hintLevel int) (*models.EvaluatorResult, error) {
	return a.EvaluateWithImage(ctx, question, studentAttempt, hintLevel, "")
}

// EvaluateWithImage performs evaluation with optional vision input.
// If imageDataURI is non-empty, the image is sent to the LLM as a photo of the
// student's handwritten work. studentAttempt may be empty when image-only.
func (a *AttemptEvaluatorAgent) EvaluateWithImage(ctx context.Context, question, studentAttempt string, hintLevel int, imageDataURI string) (*models.EvaluatorResult, error) {
	userText := fmt.Sprintf("ORIGINAL QUESTION:\n%s\n\nHINT LEVEL: %d", question, hintLevel)
	if studentAttempt != "" {
		userText += fmt.Sprintf("\n\nSTUDENT'S ATTEMPT (typed):\n%s", studentAttempt)
	}
	if imageDataURI != "" && studentAttempt == "" {
		userText += "\n\nSTUDENT'S ATTEMPT: See the attached image of their handwritten work."
	} else if imageDataURI != "" {
		userText += "\n\nThe student also attached an image of their handwritten work (see below)."
	}

	var messages []llm.ChatMessage
	if imageDataURI != "" {
		messages = []llm.ChatMessage{
			{Role: "system", Content: evaluatorSystemPrompt},
			{
				Role: "user",
				Content: []llm.ContentPart{
					{Type: "text", Text: userText},
					{Type: "image_url", ImageURL: &llm.ImageURL{URL: imageDataURI, Detail: "high"}},
				},
			},
		}
		slog.Info("attempt_evaluator: running with vision")
	} else {
		messages = []llm.ChatMessage{
			{Role: "system", Content: evaluatorSystemPrompt},
			{Role: "user", Content: userText},
		}
	}

	var client *llm.Client
	if imageDataURI != "" {
		client = a.visionClient
	} else {
		client = a.llmClient
	}

	llmStart := time.Now()
	comp, err := client.Complete(ctx, messages)
	llmDuration := time.Since(llmStart)
	if err != nil {
		slog.Error("attempt_evaluator: LLM call failed",
			"model", client.Model,
			"error", err,
			"elapsed_ms", llmDuration.Milliseconds())
		return nil, fmt.Errorf("evaluator LLM call failed: %w", err)
	}

	result, parseErr := parseEvaluatorJSON(comp.Content)
	if parseErr != nil {
		slog.Warn("attempt_evaluator: JSON parse failed, using fallback", "error", parseErr)
	}
	result.HintConsumed = hintLevel

	slog.Info("attempt_evaluator: result",
		"score", result.Score,
		"correct", result.Correct,
		"hint", hintLevel,
		"model", comp.Model,
		"prompt_tokens", comp.Usage.PromptTokens,
		"completion_tokens", comp.Usage.CompletionTokens,
		"total_tokens", comp.Usage.TotalTokens,
		"elapsed_ms", llmDuration.Milliseconds(),
		"elapsed_s", fmt.Sprintf("%.1f", llmDuration.Seconds()))

	return result, nil
}

// Handle processes an evaluation task synchronously (A2A interface).
func (a *AttemptEvaluatorAgent) Handle(ctx context.Context, task *a2a.Task) (*a2a.Task, error) {
	question := ""
	studentAttempt := ""
	for _, p := range task.Input.Parts {
		if p.Type == "text" {
			if question == "" {
				question = p.Text
			} else {
				studentAttempt = p.Text
			}
		}
	}

	hintLevel := 1
	if task.Metadata != nil {
		if hl := task.Metadata["hint_level"]; hl != "" {
			fmt.Sscanf(hl, "%d", &hintLevel)
		}
	}

	result, err := a.Evaluate(ctx, question, studentAttempt, hintLevel)
	if err != nil {
		task.State = a2a.TaskStateFailed
		return task, err
	}

	out, _ := json.Marshal(result)
	task.State = a2a.TaskStateCompleted
	task.Output = &a2a.Message{
		Role:  "agent",
		Parts: []a2a.Part{a2a.TextPart(string(out))},
	}
	return task, nil
}

// HandleStream streams the evaluation result.
func (a *AttemptEvaluatorAgent) HandleStream(ctx context.Context, task *a2a.Task, out chan<- a2a.StreamEvent) {
	out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateWorking}
	result, _ := a.Handle(ctx, task)
	if result.Output != nil {
		out <- a2a.StreamEvent{Type: "artifact", Message: result.Output}
	}
}

// parseEvaluatorJSON parses the evaluator's JSON response, with fallback.
func parseEvaluatorJSON(raw string) (*models.EvaluatorResult, error) {
	raw = strings.TrimSpace(raw)
	// Strip code fences if present
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) >= 3 {
			raw = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}

	var result models.EvaluatorResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		// Fallback: generous default so the flow isn't blocked
		return &models.EvaluatorResult{
			Correct:      false,
			Score:        0.5,
			Strengths:    []string{},
			Errors:       []string{"could not parse evaluator response"},
			MissingSteps: []string{},
			NextGuidance: "Try reviewing your work and submit again.",
		}, fmt.Errorf("parse evaluator JSON: %w", err)
	}
	return &result, nil
}
