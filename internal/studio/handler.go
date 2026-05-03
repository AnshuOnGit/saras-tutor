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
	"net/http"
	"saras-tutor/internal/logger"
	"sort"
	"strings"
	"time"

	"saras-tutor/internal/config"
	"saras-tutor/internal/llm"
	"saras-tutor/internal/middleware"
	"saras-tutor/internal/storage"

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
	modelID := c.PostForm("model")

	uid, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	userID := uid.String()

	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id required"})
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
			logger.Error().Err(upErr).Msg("R2 upload failed")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "upload failed"})
			return
		}
		imageURL = publicURL
		logger.Info().Str("url", imageURL).Msg("image uploaded to R2")
	} else {
		b64 := base64.StdEncoding.EncodeToString(imageBytes)
		imageURL = fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
		logger.Info().Str("url", imageURL).Msg("image uploaded as data URI")
	}

	// Run extraction via the vision LLM
	ctx := c.Request.Context()
	extractedText, extractErr := extractTextFromImage(ctx, extractConfig{
		apiKey:  h.cfg.LLM.APIKey,
		modelID: modelID,
		baseURL: h.cfg.LLM.BaseURL,
		userID:  h.cfg.LLM.UserID,
	}, imageURL)
	if extractErr != nil {
		logger.Error().Err(extractErr).Str("model", modelID).Msg("extraction failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "extraction failed: " + extractErr.Error()})
		return
	}

	// Persist immediately so user sees the result without waiting for verification
	extraction := &Extraction{
		ID:            uuid.New().String(),
		SessionID:     sessionID,
		UserID:        userID,
		ImageURL:      imageURL,
		ExtractedText: extractedText,
		ModelID:       modelID,
		LatexVerified: false,
		CreatedAt:     time.Now().UTC(),
	}
	if err := h.saveExtraction(ctx, extraction); err != nil {
		logger.Error().Err(err).Msg("persist extraction failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save extraction"})
		return
	}

	c.JSON(http.StatusOK, extraction)

	// Background: LLM LaTeX verification — update DB when done
	go func(extID, text string) {
		vCtx, vCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer vCancel()
		lfCfg := latexFixerConfig{
			apiKey:  h.cfg.LLM.APIKey,
			baseURL: h.cfg.LLM.BaseURL,
			userID:  h.cfg.LLM.UserID,
		}
		verified := llmFixLaTeX(vCtx, lfCfg, text)
		uCtx, uCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer uCancel()
		_, err := h.pool.Exec(uCtx,
			`UPDATE extractions SET extracted_text = $1, latex_verified = true WHERE id = $2`,
			verified, extID)
		if err != nil {
			logger.Error().Err(err).Str("extraction_id", extID).Msg("latex verification DB update failed")
		} else {
			logger.Info().Str("extraction_id", extID).Msg("latex verification complete")
		}
	}(extraction.ID, extractedText)
}

// ─── GET /api/extractions ─────────────────────────────────────────────

// ListExtractions returns all extractions for a session, newest first.
func (h *Handler) ListExtractions(c *gin.Context) {
	uid, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	userID := uid.String()
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, session_id, user_id, image_url, extracted_text, model_id, COALESCE(latex_verified, false), created_at
		 FROM extractions WHERE user_id = $1 ORDER BY created_at DESC LIMIT 10`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()

	var list []Extraction
	for rows.Next() {
		var e Extraction
		if err := rows.Scan(&e.ID, &e.SessionID, &e.UserID, &e.ImageURL, &e.ExtractedText, &e.ModelID, &e.LatexVerified, &e.CreatedAt); err != nil {
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
	SessionID      string     `json:"session_id" binding:"required"`
	UserID         string     `json:"user_id"`  // ignored; overridden from JWT
	ConversationID string     `json:"conversation_id" binding:"required"`
	ModelID        string     `json:"model"`
	Intent         string     `json:"intent"`  // solve | hint | evaluate | followup
	Slots          []ChatSlot `json:"slots"`   // extraction cards with role labels
	Message        string     `json:"message"` // free-text follow-up
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

	// Override user_id from JWT claims (prevent impersonation)
	uid, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	req.UserID = uid.String()

	modelID := req.ModelID
	if modelID == "" {
		modelID = config.DefaultSolverModel()
	}

	ctx := c.Request.Context()

	// Validate follow-up message: check relevance to conversation, not PCMB subject filter
	if req.Intent == "followup" && req.Message != "" {
		// Build context from slots for relevance check
		var slotContext string
		for _, s := range req.Slots {
			text := strings.TrimSpace(s.Text)
			if text != "" {
				slotContext += s.Role + ": " + text + "\n"
			}
		}
		// Include recent history for context
		for _, h := range req.History {
			if len(slotContext) > 2000 {
				break
			}
			slotContext += h.Role + ": " + h.Content + "\n"
		}
		gkCfg := gatekeeperConfig{
			apiKey:  h.cfg.LLM.APIKey,
			baseURL: h.cfg.LLM.BaseURL,
			userID:  h.cfg.LLM.UserID,
		}
		relevance := ValidateFollowUpRelevance(ctx, gkCfg, slotContext, req.Message)
		if !relevance.Safe {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "Your follow-up doesn't seem related to the current question. Please ask something about the problem you're working on.",
				"reason": relevance.Reason,
			})
			return
		}
	} else if req.Message != "" {
		// Non-followup messages with text still get PCMB check
		gkCfg := gatekeeperConfig{
			apiKey:  h.cfg.LLM.APIKey,
			baseURL: h.cfg.LLM.BaseURL,
			userID:  h.cfg.LLM.UserID,
		}
		safety := ValidateContent(ctx, gkCfg, req.Message)
		if !safety.Safe {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "Your message contains unsupported content. Saras only handles Physics, Chemistry, Math, and Biology questions.",
				"reason": safety.Reason,
			})
			return
		}
	}

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

	// Validate all slot texts (catches user-edited extractions with non-PCMB content)
	gkCfg := gatekeeperConfig{
		apiKey:  h.cfg.LLM.APIKey,
		baseURL: h.cfg.LLM.BaseURL,
		userID:  h.cfg.LLM.UserID,
	}
	for _, s := range slots {
		safety := ValidateContent(ctx, gkCfg, s.Text)
		if !safety.Safe {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "Your " + s.Role + " contains unsupported content. Saras only handles Physics, Chemistry, Math, and Biology questions.",
				"reason": safety.Reason,
			})
			return
		}
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
✓ ACCEPTED: single-line $ and $$ with balanced braces, standard KaTeX commands only, proper markdown structure.

CONTENT ACCURACY RULES:
• NEVER use placeholder text like "image", "figure", "picture", or "diagram" as a variable subscript or symbol. If you cannot identify a symbol, use a reasonable variable name (e.g. $a$, $v_0$, $F_{\text{net}}$).
• Read ALL subscripts, superscripts, and symbols carefully from the provided question text. If a subscript looks like a number or letter, use that exact character.
• If truly ambiguous, state the ambiguity explicitly in words — do NOT silently substitute a placeholder.`

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

	// ── Phase A: Pre-stream — save user inputs to studio_messages ──
	for _, s := range req.Slots {
		var resolvedText string
		for _, rs := range slots {
			if rs.Role == s.Role {
				resolvedText = rs.Text
				break
			}
		}
		intent := s.Role // "question" or "attempt"
		var qExtID, aExtID *string
		if s.Role == "question" {
			qExtID = &s.ExtractionID
		} else {
			aExtID = &s.ExtractionID
		}
		if err := h.saveMessage(ctx, req.ConversationID, req.UserID, "user", intent, resolvedText, qExtID, aExtID, nil); err != nil {
			logger.Error().Err(err).Msg("save user slot message")
		}
	}
	if req.Intent == "followup" && req.Message != "" {
		if err := h.saveMessage(ctx, req.ConversationID, req.UserID, "user", "followup", req.Message, nil, nil, nil); err != nil {
			logger.Error().Err(err).Msg("save followup message")
		}
	}

	// ── Phase B: Stream SSE + buffer full response ──
	solverClient := llm.NewClient(h.cfg.LLM.APIKey, modelID, h.cfg.LLM.BaseURL, h.cfg.LLM.UserID)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	const firstByteTimeout = 2 * time.Minute
	const extendedTimeout = 5 * time.Minute

	var fullResponse strings.Builder
	tokenCh := make(chan string, 128)
	streamErrCh := make(chan error, 1)
	go func() {
		defer close(tokenCh)
		_, err := solverClient.StreamComplete(ctx, messages, func(token string) error {
			tokenCh <- token
			return nil
		})
		if err != nil {
			streamErrCh <- err
		}
	}()

	gotFirstToken := false
	firstByteTimer := time.NewTimer(firstByteTimeout)
	defer firstByteTimer.Stop()
	extendedTimer := time.NewTimer(firstByteTimeout + extendedTimeout)
	defer extendedTimer.Stop()
	warningSent := false

	streamLoop:
	for {
		select {
		case token, ok := <-tokenCh:
			if !ok {
				break streamLoop
			}
			if !gotFirstToken {
				gotFirstToken = true
				firstByteTimer.Stop()
			}
			fullResponse.WriteString(token)
			data, _ := json.Marshal(map[string]string{"type": "token", "text": token})
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			flusher.Flush()

		case <-firstByteTimer.C:
			if !gotFirstToken && !warningSent {
				warningSent = true
				// Send a warning event — the model is still thinking
				warnMsg := map[string]string{
					"type":    "warning",
					"text":    "⏳ **" + modelID + "** is still thinking — reasoning models can take a while on complex problems. You can open another chat tab and try a different solver (e.g. DeepSeek V4 Pro or Qwen3-Next 80B) while this one continues. This connection will stay alive for 5 more minutes.",
					"modelId": modelID,
				}
				warnData, _ := json.Marshal(warnMsg)
				fmt.Fprintf(c.Writer, "data: %s\n\n", warnData)
				flusher.Flush()
				logger.Warn().Str("model", modelID).Msg("solver: first-byte timeout (2m), warning sent to client")
			}

		case <-extendedTimer.C:
			// Final timeout — give up
			timeoutMsg := map[string]string{
				"type":    "error",
				"text":    "⏱️ **" + modelID + "** did not respond within 7 minutes. Please try a different solver model — DeepSeek V4 Pro and Qwen3-Next 80B are usually faster.",
				"modelId": modelID,
			}
			timeoutData, _ := json.Marshal(timeoutMsg)
			fmt.Fprintf(c.Writer, "data: %s\n\n", timeoutData)
			fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
			flusher.Flush()
			logger.Error().Str("model", modelID).Msg("solver: extended timeout (7m), aborting stream")
			return

		case <-ctx.Done():
			break streamLoop
		}
	}

	// Post-process: fix Unicode math symbols and unbalanced $ in solver output
	correctedResponse := fixLaTeXInMarkdown(fullResponse.String())

	// Send corrected full text so frontend can replace its buffered version
	corrData, _ := json.Marshal(map[string]string{"type": "full_text", "text": correctedResponse})
	fmt.Fprintf(c.Writer, "data: %s\n\n", corrData)
	flusher.Flush()

	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()

	// ── Phase C: Post-stream — save assistant response immediately ──
	saveCtx, saveCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer saveCancel()

	assistantIntent := req.Intent
	if assistantIntent == "" {
		assistantIntent = "solve"
	}
	msgID := uuid.New().String()
	meta := map[string]string{"model_id": modelID, "session_id": req.SessionID}
	metaJSON, _ := json.Marshal(meta)
	_, saveErr := h.pool.Exec(saveCtx,
		`INSERT INTO studio_messages (id, conversation_id, user_id, role, intent, content, question_extraction_id, attempt_extraction_id, meta, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		msgID, req.ConversationID, req.UserID, "assistant", assistantIntent, correctedResponse, nil, nil, metaJSON, time.Now())
	if saveErr != nil {
		logger.Error().Err(saveErr).Msg("save assistant message")
	}

	// Background: LLM LaTeX verification — update DB when done
	go func(savedMsgID, text string) {
		vCtx, vCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer vCancel()
		lfCfg := latexFixerConfig{
			apiKey:  h.cfg.LLM.APIKey,
			baseURL: h.cfg.LLM.BaseURL,
			userID:  h.cfg.LLM.UserID,
		}
		verified := llmFixLaTeX(vCtx, lfCfg, text)
		uCtx, uCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer uCancel()
		_, err := h.pool.Exec(uCtx,
			`UPDATE studio_messages SET content = $1, meta = meta || '{"latex_verified":"true"}'::jsonb WHERE id = $2`,
			verified, savedMsgID)
		if err != nil {
			logger.Error().Err(err).Str("msg_id", savedMsgID).Msg("latex verification DB update failed")
		} else {
			logger.Info().Str("msg_id", savedMsgID).Msg("solver latex verification complete")
		}
	}(msgID, correctedResponse)
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
	LatexVerified bool      `json:"latex_verified"`
	CreatedAt     time.Time `json:"created_at"`
}

func (h *Handler) saveExtraction(ctx context.Context, e *Extraction) error {
	_, err := h.pool.Exec(ctx,
		`INSERT INTO extractions (id, session_id, user_id, image_url, extracted_text, model_id, latex_verified, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		e.ID, e.SessionID, e.UserID, e.ImageURL, e.ExtractedText, e.ModelID, e.LatexVerified, e.CreatedAt)
	return err
}

func (h *Handler) saveMessage(ctx context.Context, conversationID, userID, role, intent, content string, qExtID, aExtID *string, meta map[string]string) error {
	metaJSON, _ := json.Marshal(meta)
	if meta == nil {
		metaJSON = []byte("{}")
	}
	_, err := h.pool.Exec(ctx,
		`INSERT INTO studio_messages (id, conversation_id, user_id, role, intent, content, question_extraction_id, attempt_extraction_id, meta, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		uuid.New().String(), conversationID, userID, role, intent, content, qExtID, aExtID, metaJSON, time.Now())
	return err
}
