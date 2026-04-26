// Package studio implements the "Studio" backend — a separate app that
// exposes NIM models in two user-facing categories (OCR & Solver), lets
// users extract text from images with any OCR model as many times as they
// want, persists every extraction, and uses a chosen Solver model to
// generate streamed solutions.
package studio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"saras-tutor/config"
	"saras-tutor/llm"
	"saras-tutor/storage"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Handler ──────────────────────────────────────────────────────────

// Handler serves all Studio REST endpoints.
type Handler struct {
	cfg     *config.Config
	pool    *pgxpool.Pool
	storage *storage.StorageService // nil if R2 not configured
}

// NewHandler constructs the Studio handler.
func NewHandler(cfg *config.Config, pool *pgxpool.Pool, storageSvc *storage.StorageService) *Handler {
	return &Handler{
		cfg:     cfg,
		pool:    pool,
		storage: storageSvc,
	}
}

// ─── GET /api/models ──────────────────────────────────────────────────

// StudioCategory is the user-facing grouping: OCR or Solver.
type StudioCategory string

const (
	StudioOCR    StudioCategory = "OCR"
	StudioSolver StudioCategory = "Solver"
)

// ProviderGroup groups models by their provider for display.
type ProviderGroup struct {
	Provider string               `json:"provider"`
	Models   []config.ModelExpert `json:"models"`
}

// StudioCategoryListing is one user-facing category with models by provider.
type StudioCategoryListing struct {
	Category    StudioCategory  `json:"category"`
	Description string          `json:"description"`
	Default     string          `json:"default"`
	Providers   []ProviderGroup `json:"providers"`
	TotalModels int             `json:"total_models"`
}

// providerFromID extracts a human-friendly provider name from a NIM model ID
// like "meta/llama-3.2-90b-vision-instruct" → "Meta".
func providerFromID(id string) string {
	parts := strings.SplitN(id, "/", 2)
	if len(parts) < 2 {
		return "Other"
	}
	providers := map[string]string{
		"meta":        "Meta",
		"google":      "Google",
		"nvidia":      "NVIDIA",
		"microsoft":   "Microsoft",
		"mistralai":   "Mistral",
		"deepseek-ai": "DeepSeek",
		"qwen":        "Qwen",
		"moonshotai":  "Moonshot",
		"openai":      "OpenAI",
	}
	if name, ok := providers[parts[0]]; ok {
		return name
	}
	return strings.Title(parts[0])
}

// groupByProvider groups a flat model list into ProviderGroups.
func groupByProvider(models []config.ModelExpert) []ProviderGroup {
	pm := make(map[string][]config.ModelExpert)
	var order []string
	for _, m := range models {
		p := providerFromID(m.ID)
		if _, seen := pm[p]; !seen {
			order = append(order, p)
		}
		pm[p] = append(pm[p], m)
	}
	out := make([]ProviderGroup, 0, len(order))
	for _, p := range order {
		out = append(out, ProviderGroup{Provider: p, Models: pm[p]})
	}
	return out
}

// ListModels returns all available NIM models in two user-facing categories:
// OCR (Vision models) and Solver (all solver tiers + hint + router).
func (h *Handler) ListModels(c *gin.Context) {
	// OCR = Vision category
	ocrModels := config.GetModelsByCategory(config.CategoryVision)

	// Solver = all solver levels + hint + router (user asked hint+verifier as solver)
	solverCats := []config.ModelCategory{
		config.CategorySolverLevel1,
		config.CategorySolverLevel2,
		config.CategorySolverLevel3,
		config.CategoryHintGenerator,
		config.CategoryRouter,
	}
	var solverModels []config.ModelExpert
	seen := make(map[string]bool)
	for _, cat := range solverCats {
		for _, m := range config.GetModelsByCategory(cat) {
			if !seen[m.ID] {
				seen[m.ID] = true
				solverModels = append(solverModels, m)
			}
		}
	}
	sort.SliceStable(solverModels, func(i, j int) bool {
		return solverModels[i].Priority < solverModels[j].Priority
	})

	ocrDefault := ""
	if d := config.GetDefault(config.CategoryVision); d != nil {
		ocrDefault = d.ID
	}
	solverDefault := ""
	if d := config.GetDefault(config.CategorySolverLevel2); d != nil {
		solverDefault = d.ID
	}

	c.JSON(http.StatusOK, gin.H{
		"categories": []StudioCategoryListing{
			{
				Category:    StudioOCR,
				Description: "Vision / OCR models for extracting text from images",
				Default:     ocrDefault,
				Providers:   groupByProvider(ocrModels),
				TotalModels: len(ocrModels),
			},
			{
				Category:    StudioSolver,
				Description: "Solver models for hints, solutions, and evaluation",
				Default:     solverDefault,
				Providers:   groupByProvider(solverModels),
				TotalModels: len(solverModels),
			},
		},
	})
}

// ─── POST /api/extract ────────────────────────────────────────────────

// Extract accepts a multipart image upload + an OCR model ID, runs
// image extraction, persists the result, and returns it.
func (h *Handler) Extract(c *gin.Context) {
	sessionID := c.PostForm("session_id")
	userID := c.PostForm("user_id")
	modelID := c.PostForm("model")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id required"})
		return
	}
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id required"})
		return
	}
	if modelID == "" {
		if d := config.GetDefault(config.CategoryVision); d != nil {
			modelID = d.ID
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "model required"})
			return
		}
	}

	fileHeader, err := c.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image file required"})
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open file"})
		return
	}
	defer file.Close()

	imageBytes, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}
	mimeType := http.DetectContentType(imageBytes)

	// Resize
	resized, resizedMime, resizeErr := resizeImage(imageBytes, mimeType)
	if resizeErr == nil {
		imageBytes = resized
		mimeType = resizedMime
	}

	// Upload to R2 (or fall back to data URI)
	var imageURL string
	if h.storage != nil {
		publicURL, upErr := h.storage.UploadImage(c.Request.Context(), imageBytes, fileHeader.Filename)
		if upErr != nil {
			slog.Error("R2 upload failed", "error", upErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "upload failed"})
			return
		}
		imageURL = publicURL
		slog.Info("image uploaded to R2", "url", imageURL)
	} else {
		b64 := base64.StdEncoding.EncodeToString(imageBytes)
		imageURL = fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
		slog.Info("image uploaded as data URI", "url", imageURL)
	}

	// Run extraction via the vision LLM
	ctx := c.Request.Context()
	extractedText, extractErr := extractTextFromImage(ctx, extractConfig{
		apiKey:  h.cfg.LLMAPIKey,
		modelID: modelID,
		baseURL: h.cfg.LLMBaseURL,
		userID:  h.cfg.LLMUserID,
	}, imageURL)
	if extractErr != nil {
		slog.Error("extraction failed", "error", extractErr, "model", modelID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "extraction failed: " + extractErr.Error()})
		return
	}

	// Persist
	extraction := &Extraction{
		ID:            uuid.New().String(),
		SessionID:     sessionID,
		UserID:        userID,
		ImageURL:      imageURL,
		ExtractedText: extractedText,
		ModelID:       modelID,
		CreatedAt:     time.Now().UTC(),
	}
	if err := h.saveExtraction(ctx, extraction); err != nil {
		slog.Error("persist extraction failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save extraction"})
		return
	}

	c.JSON(http.StatusOK, extraction)
}

// ─── GET /api/extractions ─────────────────────────────────────────────

// ListExtractions returns all extractions for a session, newest first.
func (h *Handler) ListExtractions(c *gin.Context) {
	sessionID := c.Query("session_id")
	userID := c.Query("user_id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id required"})
		return
	}
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id required"})
		return
	}
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, session_id, user_id, image_url, extracted_text, model_id, created_at
		 FROM extractions WHERE session_id = $1 AND user_id = $2 ORDER BY created_at DESC`, sessionID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()

	var list []Extraction
	for rows.Next() {
		var e Extraction
		if err := rows.Scan(&e.ID, &e.SessionID, &e.UserID, &e.ImageURL, &e.ExtractedText, &e.ModelID, &e.CreatedAt); err != nil {
			continue
		}
		list = append(list, e)
	}
	if list == nil {
		list = []Extraction{}
	}
	c.JSON(http.StatusOK, gin.H{"extractions": list})
}

// ─── POST /api/chat ───────────────────────────────────────────────────

// ChatRequest is the JSON body for POST /api/chat.
type ChatRequest struct {
	SessionID string     `json:"session_id" binding:"required"`
	UserID    string     `json:"user_id" binding:"required"`
	ModelID   string     `json:"model"`
	Intent    string     `json:"intent"`  // solve | hint | evaluate | followup
	Slots     []ChatSlot `json:"slots"`   // extraction cards with role labels
	Message   string     `json:"message"` // free-text follow-up
	// History is the prior turns in this conversation (sent by frontend).
	History []ChatTurn `json:"history"`
}

// ChatSlot is an extraction card dropped into the workspace with a role label.
type ChatSlot struct {
	ExtractionID string `json:"extraction_id"`
	Role         string `json:"role"` // question | attempt
	Text         string `json:"text"` // user-edited text (overrides DB if non-empty)
}

// ChatTurn is one user or assistant message from the conversation history.
type ChatTurn struct {
	Role    string `json:"role"` // user | assistant
	Content string `json:"content"`
}

// Chat streams a response for the configured intent using the dropped
// extraction slots and optional follow-up message. Supports multi-turn
// conversation via the history field.
func (h *Handler) Chat(c *gin.Context) {
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	modelID := req.ModelID
	if modelID == "" {
		modelID = config.DefaultSolverModel()
	}

	ctx := c.Request.Context()

	// Resolve extraction texts
	type resolvedSlot struct {
		Role string
		Text string
	}
	var slots []resolvedSlot
	for _, s := range req.Slots {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			// Fallback: fetch from DB if frontend didn't send edited text
			err := h.pool.QueryRow(ctx,
				`SELECT extracted_text FROM extractions WHERE id = $1`, s.ExtractionID).Scan(&text)
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "extraction not found: " + s.ExtractionID})
				return
			}
		}
		slots = append(slots, resolvedSlot{Role: s.Role, Text: text})
	}

	// Build system prompt based on intent
	var systemPrompt string
	var userPrompt string

	// Shared KaTeX rendering rules appended to every system prompt.
	const katexRules = `

KATEX MATH RENDERING — MANDATORY RULES
(violations will be rejected by the rendering pipeline)

DELIMITER RULES:
• Inline math: single dollar signs — $F = ma$
• Display math: double dollar signs on their OWN line with blank lines around them:

  $$E = \frac{1}{2}mv^2$$

• NEVER use \( \), \[ \], or \begin{equation}.
• Every opening $ must have a closing $. Every opening $$ must have a closing $$.
• NEVER nest dollar signs (e.g. $ a $ b $ ) — each expression gets its own delimiters.
• NEVER place math inside code blocks (backticks).
• EVERY variable, symbol, or expression — no matter how short — MUST be wrapped in $ delimiters. Never write bare m, v, F, θ, α, etc. without $ wrapping.

SYNTAX RULES:
• Keep expressions KaTeX-safe: use \frac{}{}, \sqrt{}, ^{}, _{}, \text{}, \vec{}, \hat{}, \overrightarrow{}, \sin, \cos, \tan, \log, \ln, \lim, \sum, \int, \prod.
• Balance ALL braces {} and parentheses (). No dangling \frac{, unclosed {, or stray }.
• AVOID multi-line environments: NO \begin{align}, \begin{equation}, \begin{cases}, \begin{array}, \begin{matrix}. Instead, write separate single-line $$ expressions.
• For piecewise/cases, use a bullet list with one $$ per branch.
• Do NOT use \tag{}, \label{}, \ref{}, \eqref{}, \nonumber, \notag.
• Do NOT mix HTML tags inside math delimiters.

MARKDOWN STRUCTURE RULES:
• Use ## headings on their OWN line with a blank line before and after.
• Use --- on its OWN line with blank lines before and after.
• Bullet lists: each - item on its own line.
• NEVER concatenate headings, rules, or bullets on one line (e.g. NEVER "--- ## Solution" or "text ## Answer").

CONCRETE EXAMPLES — follow these EXACTLY:

✓ GOOD (equations on own lines, every symbol wrapped):
Using conservation of energy from $A$ to $B$, with height $h_B = l$:

$$\frac{1}{2}mv_0^2 = \frac{1}{2}mv_B^2 + mgl$$

Solving for $v_B^2$:

$$v_B^2 = v_0^2 - 2gl = 5gl - 2gl = 3gl$$

Therefore the kinetic energy at $B$ is:

$$KE_B = \frac{1}{2}m(3gl) = \frac{3}{2}mgl$$

✗ BAD (bare symbols, no delimiters, stacked single-symbol lines):
v
0
2
=
5
g
l
KE
B
=
1
2
m
v
B
2

✗ BAD (equation without dollar delimiters):
v_B^2 = v_0^2 - 2gl = 3gl

✗ BAD (headings on same line):
--- ## Solution ### Step 1

PASS/FAIL POLICY — the renderer will enforce these:
✗ REJECT: bare math without $ delimiters, unbalanced braces, \begin{align/equation/cases/array/matrix}, \( \), \[ \], nested $, math in code blocks, headings not on own line.
✗ DOWNGRADE TO PLAIN TEXT: any expression with unsupported commands (\tag, \label, \DeclareMathOperator, \newcommand).
✓ ACCEPTED: single-line $ and $$ with balanced braces, standard KaTeX commands only, proper markdown structure.`

	switch req.Intent {
	case "hint":
		systemPrompt = `You are an expert JEE/NEET tutor who gives pedagogical hints WITHOUT revealing the full answer.

RULES:
1. Identify the key concept, formula, or theorem relevant to the question.
2. Ask a guiding question that nudges the student toward the right approach.
3. Do NOT show the full solution or final answer.
4. Keep it to 3-5 sentences.
5. If the student asks follow-up questions, give progressively stronger hints.` + katexRules

		var questionText string
		for _, s := range slots {
			if s.Role == "question" {
				questionText = s.Text
				break
			}
		}
		if questionText == "" && len(slots) > 0 {
			questionText = slots[0].Text
		}
		userPrompt = "Give me a hint for this question (do NOT solve it):\n\n" + questionText

	case "evaluate":
		systemPrompt = `You are an expert JEE/NEET evaluator who reviews a student's attempt against a question.

RULES:
1. Compare the student's work against the correct approach step by step.
2. Score from 0.0 to 1.0 (1.0 = perfect).
3. List specific strengths, errors, and missing steps.
4. Give a one-sentence next guidance.
5. Format as:
   ## Score: $X.X / 1.0$
   ## Strengths
   ## Errors
   ## Missing Steps
   ## Next Steps` + katexRules

		var questionText, attemptText string
		for _, s := range slots {
			if s.Role == "question" {
				questionText = s.Text
			} else if s.Role == "attempt" {
				attemptText = s.Text
			}
		}
		if questionText == "" || attemptText == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "evaluate requires both a question and an attempt extraction"})
			return
		}
		userPrompt = fmt.Sprintf("## Question\n\n%s\n\n## Student's Attempt\n\n%s\n\nEvaluate the student's attempt against this question.", questionText, attemptText)

	case "followup":
		systemPrompt = `You are an expert JEE/NEET tutor. Continue the conversation helpfully.
Use Markdown headings, bold, and bullet lists for structure.
If the student asks for more detail, provide it. If they ask a new question, solve or hint as appropriate.` + katexRules
		userPrompt = req.Message

	default: // "solve" or empty
		systemPrompt = `You are an expert tutor solving JEE/NEET-level problems step by step.

RULES:
1. Show COMPLETE worked solution with every intermediate step.
2. Bold the final answer: **Answer: $...$**
3. If the question involves a diagram described in text, acknowledge the description.
4. Be precise with units and significant figures.
5. Use Markdown headings for sections (## Given, ## Approach, ## Solution, ## Answer).` + katexRules

		var questionText string
		for _, s := range slots {
			if s.Role == "question" {
				questionText = s.Text
				break
			}
		}
		if questionText == "" && len(slots) > 0 {
			questionText = slots[0].Text
		}
		userPrompt = "Solve this question completely:\n\n" + questionText
	}

	// Build messages with history
	messages := []llm.ChatMessage{
		{Role: "system", Content: systemPrompt},
	}
	for _, turn := range req.History {
		messages = append(messages, llm.ChatMessage{
			Role:    turn.Role,
			Content: turn.Content,
		})
	}
	messages = append(messages, llm.ChatMessage{
		Role: "user",
		Content: []llm.ContentPart{
			{Type: "text", Text: userPrompt},
		},
	})

	// Stream SSE
	solverClient := llm.NewClient(h.cfg.LLMAPIKey, modelID, h.cfg.LLMBaseURL, h.cfg.LLMUserID)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	tokenCh := make(chan string, 128)
	go func() {
		defer close(tokenCh)
		_, _ = solverClient.StreamComplete(ctx, messages, func(token string) error {
			tokenCh <- token
			return nil
		})
	}()

	for token := range tokenCh {
		data, _ := json.Marshal(map[string]string{"type": "token", "text": token})
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		flusher.Flush()
	}

	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()
}

// ─── Extraction model + persistence ───────────────────────────────────

// Extraction represents one OCR extraction of an image.
type Extraction struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"session_id"`
	UserID        string    `json:"user_id"`
	ImageURL      string    `json:"image_url"`
	ExtractedText string    `json:"extracted_text"`
	ModelID       string    `json:"model_id"`
	CreatedAt     time.Time `json:"created_at"`
}

func (h *Handler) saveExtraction(ctx context.Context, e *Extraction) error {
	_, err := h.pool.Exec(ctx,
		`INSERT INTO extractions (id, session_id, user_id, image_url, extracted_text, model_id, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.ID, e.SessionID, e.UserID, e.ImageURL, e.ExtractedText, e.ModelID, e.CreatedAt)
	return err
}
