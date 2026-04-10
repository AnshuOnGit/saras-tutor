package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"saras-tutor/a2a"
	"saras-tutor/db"
	"saras-tutor/llm"
)

// VerificationResult is the structured output from the verifier LLM call.
type VerificationResult struct {
	Score  float64 `json:"score"`  // 0.0–1.0
	Issues string  `json:"issues"` // brief description of problems (empty if correct)
}

// VerifierAgent checks whether a generated solution is correct and complete
// by making a non-streaming LLM call that returns a quality score.
type VerifierAgent struct {
	llmClient *llm.Client
	store     *db.Store
}

// NewVerifierAgent creates the verifier agent.
func NewVerifierAgent(llmClient *llm.Client, store *db.Store) *VerifierAgent {
	return &VerifierAgent{llmClient: llmClient, store: store}
}

// Card returns agent metadata.
func (a *VerifierAgent) Card() a2a.AgentCard {
	return a2a.AgentCard{
		ID:          "verifier",
		Name:        "Verifier Agent",
		Description: "Verifies the correctness and completeness of a generated solution.",
		Skills:      []string{"verify"},
	}
}

// MinVerifierScore is the score below which a solution is considered low-quality.
const MinVerifierScore = 0.6

const verifierSystemPrompt = "You are an academic solution verifier. " +
	"Given a QUESTION and a SOLUTION, assess whether the solution is correct and useful for a student.\n\n" +
	"Focus on what matters most:\n" +
	"1. Is the final answer correct?\n" +
	"2. Are the key reasoning steps logically sound?\n" +
	"3. Does the solution address the actual question asked?\n\n" +
	"DO NOT penalize for:\n" +
	"- Formatting or style choices\n" +
	"- Reasonable assumptions stated by the solver\n" +
	"- Minor simplifications that don't affect the final answer\n" +
	"- Alternative valid approaches to solving the problem\n" +
	"- Verbose or concise explanations (either is fine)\n\n" +
	"If the question came from an image and the text description is incomplete, " +
	"give the solver the benefit of the doubt on details you cannot verify from text alone.\n\n" +
	"Respond with ONLY a JSON object (no code fences, no explanation):\n" +
	`{"score": <0.0-1.0>, "issues": "<brief description of actual errors, or empty string if correct>"}` + "\n\n" +
	"Scoring guide:\n" +
	"- 0.9-1.0: Correct final answer with sound reasoning\n" +
	"- 0.7-0.8: Correct approach and answer but with minor errors in intermediate steps\n" +
	"- 0.5-0.6: Partially correct — right approach but wrong final answer OR missing key steps\n" +
	"- 0.0-0.4: Fundamentally wrong approach or completely incorrect answer"

// Verify performs a non-streaming LLM call to assess a solution's quality.
// If imageDataURI is non-empty, a vision message is used so the verifier can
// see the original image (important for diagram/circuit/graph questions).
// Returns the VerificationResult plus LLM stats.
func (a *VerifierAgent) Verify(ctx context.Context, question, solution, imageDataURI string) (*VerificationResult, *llm.CompletionResult, error) {
	userText := fmt.Sprintf("QUESTION:\n%s\n\nSOLUTION:\n%s", question, solution)

	var messages []llm.ChatMessage
	if imageDataURI != "" {
		// Vision mode — let the verifier see the original image
		messages = []llm.ChatMessage{
			{Role: "system", Content: verifierSystemPrompt},
			{
				Role: "user",
				Content: []llm.ContentPart{
					{Type: "text", Text: userText},
					{Type: "image_url", ImageURL: &llm.ImageURL{URL: imageDataURI, Detail: "low"}},
				},
			},
		}
		log.Printf("[verifier] running with image context")
	} else {
		messages = []llm.ChatMessage{
			{Role: "system", Content: verifierSystemPrompt},
			{Role: "user", Content: userText},
		}
	}

	comp, err := a.llmClient.Complete(ctx, messages)
	if err != nil {
		return nil, nil, fmt.Errorf("verifier LLM call failed: %w", err)
	}

	result, parseErr := parseVerificationJSON(comp.Content)
	if parseErr != nil {
		log.Printf("[verifier] JSON parse warning: %v — using fallback score=1.0", parseErr)
	}

	log.Printf("[verifier] score=%.2f issues=%q model=%s tokens=%d",
		result.Score, result.Issues, comp.Model, comp.Usage.TotalTokens)

	return result, comp, nil
}

// Handle processes a verification task synchronously (A2A interface).
func (a *VerifierAgent) Handle(ctx context.Context, task *a2a.Task) (*a2a.Task, error) {
	var question, solution string
	for _, p := range task.Input.Parts {
		if p.Type == "text" {
			// First text part is question, second is solution
			if question == "" {
				question = p.Text
			} else {
				solution = p.Text
			}
		}
	}

	result, _, err := a.Verify(ctx, question, solution, "")
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

// HandleStream streams the verification result.
func (a *VerifierAgent) HandleStream(ctx context.Context, task *a2a.Task, out chan<- a2a.StreamEvent) {
	out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateWorking}
	result, _ := a.Handle(ctx, task)
	if result.Output != nil {
		out <- a2a.StreamEvent{Type: "artifact", Message: result.Output}
	}
}

// parseVerificationJSON parses the verifier's JSON response.
// Falls back to score 1.0 if JSON parsing fails.
func parseVerificationJSON(raw string) (*VerificationResult, error) {
	raw = strings.TrimSpace(raw)
	// Strip markdown code fences if the model added them
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) >= 3 {
			raw = strings.Join(lines[1:len(lines)-1], "\n")
			raw = strings.TrimSpace(raw)
		}
	}

	var result VerificationResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return &VerificationResult{Score: 1.0, Issues: ""}, fmt.Errorf("JSON parse failed: %w", err)
	}
	return &result, nil
}
