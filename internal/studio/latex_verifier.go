package studio

import (
	"context"
	"saras-tutor/internal/llm"
	"saras-tutor/internal/logger"
	"strings"
	"time"
)

// latexFixerModel is a small, fast model used for LaTeX correction.
const latexFixerModel = "google/gemma-3-4b-it"

// latexFixerFallback is tried if the primary model fails.
const latexFixerFallback = "microsoft/phi-4-mini-instruct"

const latexFixerPrompt = `You are a LaTeX/KaTeX syntax fixer for a math tutoring platform.

You will receive text containing Markdown with KaTeX math expressions. Your ONLY job is to fix broken LaTeX syntax. Do NOT change the meaning, wording, explanation, or structure of the text.

RULES:
1. Fix unbalanced $ or $$ delimiters — every opening must have a matching closing.
2. Fix unbalanced braces {} — every { must have a matching }.
3. Fix unbalanced \left( / \right) pairs.
4. Fix broken commands like \\frac (double backslash) → \frac (single backslash).
5. Fix bare LaTeX outside dollar delimiters — wrap them: \frac{1}{2} → $\frac{1}{2}$
6. Fix split math like $-$\frac{1}{2} → $-\frac{1}{2}$
7. Ensure display math $$ is on its own line with blank lines around it.
8. Do NOT use \begin{align}, \begin{equation}, \begin{cases}, \begin{array}, \begin{matrix}. Convert them to separate $$ lines.
9. Do NOT add, remove, or rephrase ANY non-math text. Preserve all headings, bullet points, and explanations exactly.
10. Do NOT add explanations about what you fixed. Return ONLY the corrected text.
11. If the text has no LaTeX issues, return it unchanged.

IMPORTANT: Return ONLY the corrected text. No commentary, no "Here is the fixed version", no markdown code fences wrapping the output. Just the raw corrected text.`

// llmFixLaTeX sends text to a small LLM to fix broken LaTeX expressions.
// Returns the fixed text. On failure, returns the original text unchanged.
func llmFixLaTeX(ctx context.Context, cfg latexFixerConfig, text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	// Skip if text has no math indicators at all (pure prose)
	if !strings.ContainsAny(text, "$\\{}^_") {
		return text
	}

	models := []string{latexFixerModel, latexFixerFallback}
	for _, modelID := range models {
		client := llm.NewClient(cfg.apiKey, modelID, cfg.baseURL, cfg.userID)
		client.MaxTokens = 4096

		fixCtx, cancel := context.WithTimeout(ctx, 30*time.Second)

		messages := []llm.ChatMessage{
			{Role: "system", Content: latexFixerPrompt},
			{Role: "user", Content: text},
		}

		resp, err := client.Complete(fixCtx, messages)
		cancel()
		if err != nil {
			logger.Warn().Err(err).Str("model", modelID).
				Msg("[LATEX-VERIFIER] LLM fix failed, trying next model")
			continue
		}

		fixed := strings.TrimSpace(resp.Content)
		// Guard: if LLM returned something drastically different in length,
		// it probably hallucinated — keep original
		if len(fixed) == 0 {
			logger.Warn().Str("model", modelID).Msg("[LATEX-VERIFIER] empty response, keeping original")
			continue
		}
		origLen := float64(len(text))
		fixedLen := float64(len(fixed))
		ratio := fixedLen / origLen
		if ratio < 0.5 || ratio > 2.0 {
			logger.Warn().Str("model", modelID).
				Float64("ratio", ratio).
				Msg("[LATEX-VERIFIER] output length suspicious, keeping original")
			continue
		}

		// Strip any accidental code fences the LLM might wrap around output
		fixed = strings.TrimPrefix(fixed, "```markdown")
		fixed = strings.TrimPrefix(fixed, "```md")
		fixed = strings.TrimPrefix(fixed, "```")
		fixed = strings.TrimSuffix(fixed, "```")
		fixed = strings.TrimSpace(fixed)

		logger.Info().Str("model", modelID).
			Int("original_len", len(text)).
			Int("fixed_len", len(fixed)).
			Msg("[LATEX-VERIFIER] LaTeX fix applied")
		return fixed
	}

	logger.Warn().Msg("[LATEX-VERIFIER] all models failed, returning original")
	return text
}

type latexFixerConfig struct {
	apiKey  string
	baseURL string
	userID  string
}
