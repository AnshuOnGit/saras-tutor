package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"saras-tutor/llm"
)

const intentPrompt = `You are an academic question classifier for a JEE/NEET tutoring system.

ALLOWED SUBJECTS: Mathematics, Physics, Chemistry, Biology.

TASK: Determine whether the student's input is a valid academic question from one of the allowed subjects.

RULES:
- A valid question is any problem, exercise, conceptual query, formula question, or homework task from Mathematics, Physics, Chemistry, or Biology.
- Questions can be phrased casually (e.g. "what is mitosis", "solve x^2 + 3x = 0", "explain Newton's 3rd law") — that is fine.
- Extracted image content containing a question from these subjects is valid even if it has OCR artifacts.
- If the input contains BOTH a valid academic question AND some irrelevant text, classify it as VALID (focus on the academic part).
- Greetings, chitchat, off-topic requests (coding, history, geography, literature, politics, general knowledge outside PCMB, etc.) are INVALID.

Respond with ONLY a JSON object (no code fences, no extra text):
{"valid": true/false, "subject": "<detected subject or null>", "reason": "<brief 1-line reason>"}
`

// ValidationResult holds the LLM's classification of a question.
type ValidationResult struct {
	Valid   bool   `json:"valid"`
	Subject string `json:"subject"`
	Reason  string `json:"reason"`
}

// ValidateQuestion uses a lightweight LLM call to check if the question
// belongs to an allowed academic subject (math/physics/chemistry/biology).
// Returns (result, error). On LLM failure, returns valid=true as a safe fallback
// so the student isn't blocked by transient errors.
func ValidateQuestion(ctx context.Context, client *llm.Client, question string) (*ValidationResult, error) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: intentPrompt},
		{Role: "user", Content: question},
	}

	comp, err := client.Complete(ctx, messages)
	if err != nil {
		slog.Warn("validator: LLM call failed, allowing as fallback", "error", err)
		return &ValidationResult{Valid: true, Reason: "validation skipped (LLM error)"}, nil
	}

	raw := strings.TrimSpace(comp.Content)
	// Strip code fences if present
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) >= 3 {
			raw = strings.Join(lines[1:len(lines)-1], "\n")
			raw = strings.TrimSpace(raw)
		}
	}

	var result ValidationResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		slog.Warn("validator: JSON parse failed, allowing as fallback", "error", err, "raw", raw)
		return &ValidationResult{Valid: true, Reason: "validation skipped (parse error)"}, nil
	}

	slog.Info("validator result", "valid", result.Valid, "subject", result.Subject, "reason", result.Reason, "tokens", comp.Usage.TotalTokens)
	return &result, nil
}

// InvalidQuestionMessage returns a user-friendly message when a question is rejected.
func InvalidQuestionMessage(vr *ValidationResult) string {
	return fmt.Sprintf(
		"**This doesn't look like a question I can help with.**\n\n"+
			"I'm a tutor for **Mathematics, Physics, Chemistry, and Biology** (JEE/NEET level).\n\n"+
			"_Reason: %s_\n\n"+
			"Please ask a question from one of these subjects and I'll be happy to help!",
		vr.Reason,
	)
}
