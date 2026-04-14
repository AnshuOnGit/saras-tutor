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
)

// ImageExtractionAgent uses a vision-capable LLM to extract question text
// from an image (photo of a textbook page, whiteboard, etc.).
type ImageExtractionAgent struct {
	llmClient *llm.Client
	store     *db.Store
}

// NewImageExtractionAgent creates the agent with a vision-capable LLM client.
// It sets MaxTokens = 4096 on the vision client to avoid truncation on complex diagrams.
func NewImageExtractionAgent(llmClient *llm.Client, store *db.Store) *ImageExtractionAgent {
	llmClient.MaxTokens = 4096
	return &ImageExtractionAgent{llmClient: llmClient, store: store}
}

// Card returns agent metadata.
func (a *ImageExtractionAgent) Card() a2a.AgentCard {
	return a2a.AgentCard{
		ID:          "image_extraction",
		Name:        "Image Extraction Agent",
		Description: "Extracts question text from images using a vision model.",
		Skills:      []string{"ocr", "image_to_text"},
	}
}

/*const imageExtractionPrompt = "You are an expert academic content extraction assistant with deep knowledge of mathematics, physics, chemistry, and engineering.\n\n" +
"TASK: Analyse the provided image and extract ALL content — both text AND graphical elements — as well-formatted Markdown.\n\n" +
"CONTENT TYPES YOU MUST HANDLE:\n" +
"• Text & equations — OCR the text faithfully.\n" +
"• Graphs / plots — describe axes, labels, curves, key points, intercepts, asymptotes. Reproduce the function if identifiable.\n" +
"• Geometric figures — describe the shape, label vertices/sides/angles, note given measurements.\n" +
"• Circuit diagrams — list components (resistors, capacitors, voltage sources) and their connections. Represent as a Mermaid flowchart.\n" +
"• Free-body / force diagrams — list forces, directions, and the body they act on.\n" +
"• Flowcharts / block diagrams — represent as a ```mermaid block.\n" +
"• Tables / matrices / matching columns — use Markdown tables or LaTeX matrices.\n\n" +
"FORMATTING RULES — you MUST follow these:\n" +
"1. Reproduce the full question/problem text faithfully — do NOT solve it.\n" +
"2. For ALL mathematical expressions use LaTeX with dollar-sign delimiters:\n" +
"   • Inline: $...$ (e.g. $x^2 + 3x + 2 = 0$)\n" +
"   • Display: $$...$$ on its own line, e.g. $$\\int_0^1 \\frac{dx}{\\sqrt{1-x^2}}$$\n" +
"3. Use proper LaTeX: \\frac, \\sqrt, \\int, \\sum, \\lim, \\vec, \\hat, \\overrightarrow, \\pi, \\theta, \\Omega, etc.\n" +
"4. For integrals include limits: $\\int_{a}^{b}$. For fractions: $\\frac{num}{den}$.\n" +
"5. Preserve structure: headings (##), bullet/numbered lists, or tables as appropriate.\n" +
"6. For diagrams that can be represented structurally, use a ```mermaid fenced block.\n" +
"7. For diagrams that CANNOT be represented in Mermaid (complex graphs, plots, geometric figures), provide a detailed textual description under a heading **## Diagram Description** including:\n" +
"   - Type of diagram (e.g. \"velocity-time graph\", \"right triangle\", \"RC circuit\")\n" +
"   - All labels, values, directions, and units visible\n" +
"   - Relationships or connections between elements\n" +
"8. For matrices: $$\\begin{pmatrix} a & b \\\\ c & d \\end{pmatrix}$$\n" +
"9. NEVER use \\\\( \\\\) or \\\\[ \\\\] delimiters — always use $ and $$ only.\n" +
"10. Do NOT solve or explain — only extract and format.\n\n" +
"RESPONSE FORMAT — you MUST respond with ONLY a JSON object (no code fences, no extra text):\n" +
"{\"confidence\": <0.0-1.0>, \"content\": \"<your extracted markdown here>\"}\n" +
"- confidence: how certain you are that you extracted ALL content correctly (1.0 = fully certain, lower if image is unclear or you may have missed something).\n" +
"- content: your full extracted markdown (escape inner quotes as needed)."*/

const imageExtractionPrompt = `You are an expert OCR system for JEE/NEET exam questions. Extract ALL content from the image as clean Markdown.

RESPOND WITH ONLY THIS JSON (no code fences, no extra text):
{"confidence": 0.0-1.0, "content": "<markdown here>"}

EXTRACTION RULES:
1. Reproduce the FULL problem statement word-for-word. Do NOT summarise, paraphrase, or add commentary.
2. Reproduce ALL answer options exactly: (A), (B), (C), (D).
3. For math use LaTeX: inline $...$ and display $$...$$. Use \frac, \sqrt, \int, \sum, \vec, \overrightarrow, \hat, etc.
4. NEVER use \( \) or \[ \] delimiters.
5. For diagrams/figures: describe under "## Diagram" with all labels, vertices, segments, angles, and measurements visible.
6. For tables: use Markdown tables.
7. Do NOT solve, explain, or interpret. Only extract.
8. Keep output concise — no preamble like "The problem statement is" or "The extracted content is".

DOUBLE-CHECK PASS — re-examine the image carefully:
- Every variable: check for a coefficient (2α, 3β), a preceding fraction (½α → $\frac{1}{2}\alpha$), or sign (+/−).
- Every \frac: verify numerator and denominator are complete (e.g. $\frac{mv^2}{2}$ not $\frac{mv}{2}$).
- Every superscript: distinguish x², x³, x⁻¹, x^{1/2}.
- Every subscript: distinguish S₁ vs S, v₀ vs v.
- Every option (A)–(D): capture the full expression including trailing terms and units.
- Vectors: use $\vec{SP}$ or $\overrightarrow{SP}$ as shown in the image.
- The zero vector: $\vec{0}$ or $\overrightarrow{0}$.
Apply any corrections directly. Do NOT add commentary about corrections.`

// ---------- Structured response types ----------

type extractionResponse struct {
	Confidence float64 `json:"confidence"`
	Content    string  `json:"content"`
}

// ---------- Convert structured extraction to presentable markdown ----------

func buildMarkdownFromExtraction(resp *extractionResponse) string {
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return "(No content extracted from image)"
	}
	return content
}

// fixLaTeXInMarkdown repairs LaTeX commands whose leading backslash was
// silently converted to a control character by json.Unmarshal.
//
// For example, if the LLM writes \frac in a JSON string without double-escaping,
// Go parses \f as form-feed (U+000C) + "rac" → this function restores it to \frac.
// Form-feed and backspace NEVER appear in valid Markdown, so replacing is safe.
// For tab and carriage-return we only replace when followed by a lowercase letter
// (heuristic: \theta, \text, \rho, \right etc.).
func fixLaTeXInMarkdown(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch {
		case ch == '\x0c': // form feed → \f  (\frac, \forall, \flat …)
			b.WriteString("\\f")
		case ch == '\x08': // backspace → \b  (\beta, \bar, \begin, \binom …)
			b.WriteString("\\b")
		case ch == '\r' && i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z':
			b.WriteString("\\r") // CR + letter → \rho, \right …
		case ch == '\t' && i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z':
			b.WriteString("\\t") // tab + letter → \theta, \text, \times …
		default:
			b.WriteRune(ch)
		}
	}
	return b.String()
}

// Handle processes an image extraction task synchronously.
func (a *ImageExtractionAgent) Handle(ctx context.Context, task *a2a.Task) (*a2a.Task, error) {
	handleStart := time.Now()
	slog.Info("image_extraction: Handle started", "task", task.ID)

	// Build multi-modal message with image parts
	var contentParts []llm.ContentPart
	contentParts = append(contentParts, llm.ContentPart{Type: "text", Text: imageExtractionPrompt})

	imageCount := 0
	var imageSizeHint int
	for _, p := range task.Input.Parts {
		if p.Type == "image" && p.ImageURL != "" {
			contentParts = append(contentParts, llm.ContentPart{
				Type:     "image_url",
				ImageURL: &llm.ImageURL{URL: p.ImageURL, Detail: "high"},
			})
			imageCount++
			imageSizeHint += len(p.ImageURL)
		}
	}
	slog.Info("image_extraction: built request",
		"images", imageCount,
		"image_data_chars", imageSizeHint,
		"prompt_len", len(imageExtractionPrompt),
		"elapsed_ms", time.Since(handleStart).Milliseconds())

	messages := []llm.ChatMessage{
		{Role: "user", Content: contentParts},
	}

	slog.Info("image_extraction: calling vision LLM...", "model", a.llmClient.Model)
	llmStart := time.Now()
	raw, err := a.llmClient.Complete(ctx, messages)
	llmDuration := time.Since(llmStart)
	if err != nil {
		slog.Error("image_extraction: LLM call failed",
			"error", err,
			"elapsed_ms", llmDuration.Milliseconds())
		task.State = a2a.TaskStateFailed
		return task, fmt.Errorf("image extraction: %w", err)
	}
	slog.Info("image_extraction: LLM responded",
		"model", raw.Model,
		"elapsed_ms", llmDuration.Milliseconds(),
		"elapsed_s", fmt.Sprintf("%.1f", llmDuration.Seconds()),
		"prompt_tokens", raw.Usage.PromptTokens,
		"completion_tokens", raw.Usage.CompletionTokens,
		"total_tokens", raw.Usage.TotalTokens,
		"response_len", len(raw.Content))

	// Strip code fences if present
	extracted := raw.Content
	extracted = strings.TrimSpace(extracted)
	if strings.HasPrefix(extracted, "```") {
		lines := strings.Split(extracted, "\n")
		if len(lines) >= 3 {
			extracted = strings.Join(lines[1:len(lines)-1], "\n")
			extracted = strings.TrimSpace(extracted)
		}
	}

	// Sanitize JSON — LLMs emit LaTeX backslash commands (\frac, \theta …)
	// without the double-escaping JSON requires, corrupting them into control
	// characters on json.Unmarshal.  extractJSONObject fixes this.
	extracted = extractJSONObject(extracted)

	// Parse structured JSON
	parseStart := time.Now()
	var resp extractionResponse
	if err := json.Unmarshal([]byte(extracted), &resp); err != nil {
		slog.Warn("image_extraction: JSON parse failed, using raw text",
			"error", err,
			"raw_prefix", truncate(extracted, 200),
			"total_elapsed_ms", time.Since(handleStart).Milliseconds())
		// Fallback: use raw text directly
		task.State = a2a.TaskStateCompleted
		task.Output = &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(extracted)},
		}
		return task, nil
	}

	slog.Info("image_extraction: parsed JSON",
		"confidence", resp.Confidence,
		"content_len", len(resp.Content),
		"parse_ms", time.Since(parseStart).Milliseconds())

	// Repair any LaTeX commands whose backslash was interpreted as a
	// JSON control character (e.g. form-feed from \frac, tab from \theta).
	if resp.Content != "" {
		resp.Content = fixLaTeXInMarkdown(resp.Content)
	}

	// Check confidence threshold
	if resp.Confidence < 0.6 {
		task.State = a2a.TaskStateFailed
		return task, &llm.ErrLowConfidence{
			Got:       resp.Confidence,
			Threshold: llm.MinConfidence,
			Agent:     "image_extraction",
		}
	}

	// Build presentable markdown from structured data
	markdown := buildMarkdownFromExtraction(&resp)
	if markdown == "" {
		markdown = "(No content extracted from image)"
	}

	slog.Info("image_extraction: complete",
		"confidence", resp.Confidence,
		"content_len", len(markdown),
		"total_elapsed_ms", time.Since(handleStart).Milliseconds(),
		"total_elapsed_s", fmt.Sprintf("%.1f", time.Since(handleStart).Seconds()))

	task.State = a2a.TaskStateCompleted
	task.Output = &a2a.Message{
		Role:  "agent",
		Parts: []a2a.Part{a2a.TextPart(markdown)},
	}
	return task, nil
}

// HandleStream delegates to Handle since extraction is a single-shot operation.
func (a *ImageExtractionAgent) HandleStream(ctx context.Context, task *a2a.Task, out chan<- a2a.StreamEvent) {
	out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateWorking}

	result, err := a.Handle(ctx, task)
	if err != nil {
		out <- a2a.StreamEvent{Type: "error", Error: err.Error()}
		return
	}

	if result.Output != nil {
		out <- a2a.StreamEvent{Type: "artifact", Message: result.Output}
	}
	out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
}
