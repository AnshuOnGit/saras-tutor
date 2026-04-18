package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"saras-tutor/a2a"
	"saras-tutor/agents"
	"saras-tutor/config"
	"saras-tutor/db"
	"saras-tutor/llm"
	"saras-tutor/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Valid actions the frontend can send.
const (
	ActionNewQuestion    = "new_question"
	ActionExtractProceed = "extract_proceed" // user confirmed extracted text, continue with validation + hint
	ActionMoreHelp       = "more_help"
	ActionShowSolution   = "show_solution"
	ActionRetryModel     = "retry_model"
	ActionSubmitAttempt  = "submit_attempt"
	ActionClose          = "close"
)

// ChatRequest is the JSON body expected on POST /chat (application/json).
type ChatRequest struct {
	UserID    string    `json:"user_id" binding:"required"`
	SessionID string    `json:"session_id" binding:"required"`
	Action    string    `json:"action"`   // new_question | more_help | show_solution | retry_model | close
	Model     string    `json:"model"`    // for retry_model: the model to use
	Category  string    `json:"category"` // for retry_model: which category (Solver:LevelN | HintGenerator | Vision)
	Message   ChatInput `json:"message"`
}

// ChatInput represents the user's input content.
type ChatInput struct {
	ContentType string `json:"content_type"` // "text" | "image_url"
	Text        string `json:"text,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
}

// chatParsed is the normalised input regardless of content-type.
type chatParsed struct {
	UserID    string
	SessionID string
	Action    string // new_question | more_help | show_solution | retry_model | close
	Model     string // for retry_model: the model to use
	Category  string // for retry_model: which category to retry
	Text      string
	ImageURI  string // data URI or remote URL
	ImageData []byte // raw bytes (only when file was uploaded)
	ImageMime string
	ImageName string
}

// ChatHandler serves the POST /chat endpoint with SSE streaming.
type ChatHandler struct {
	cfg    *config.Config
	store  *db.Store
	router a2a.Agent
}

// NewChatHandler constructs the handler and wires all agents together.
func NewChatHandler(cfg *config.Config, pool *pgxpool.Pool) *ChatHandler {
	store := db.NewStore(pool)

	// Build LLM clients — four tiers from the model registry:
	//   visionLLM  → image extraction (heavy VLM)
	//   solverLLM  → solution agents (Solver:Level2)
	//   hintLLM    → hint generation (HintGenerator)
	//   routerLLM  → validator, parser, verifier, evaluator (fast/cheap)
	visionLLM := llm.NewClient(cfg.LLMAPIKey, config.DefaultVisionModel(), cfg.LLMBaseURL, cfg.LLMUserID)
	solverLLM := llm.NewClient(cfg.LLMAPIKey, config.DefaultSolverModel(), cfg.LLMBaseURL, cfg.LLMUserID)
	hintLLM := llm.NewClient(cfg.LLMAPIKey, config.DefaultHintModel(), cfg.LLMBaseURL, cfg.LLMUserID)
	routerLLM := llm.NewClient(cfg.LLMAPIKey, config.DefaultRouterModel(), cfg.LLMBaseURL, cfg.LLMUserID)

	// Create sub-agents
	imgAgent := agents.NewImageExtractionAgent(visionLLM, store)
	solverAgent := agents.NewSolverAgent(solverLLM, store)
	verifierAgent := agents.NewVerifierAgent(routerLLM, store)
	hintAgent := agents.NewHintAgent(hintLLM, store)
	evaluatorAgent := agents.NewAttemptEvaluatorAgent(routerLLM, visionLLM, store)

	subAgents := map[string]a2a.Agent{
		imgAgent.Card().ID:       imgAgent,
		solverAgent.Card().ID:    solverAgent,
		verifierAgent.Card().ID:  verifierAgent,
		hintAgent.Card().ID:      hintAgent,
		evaluatorAgent.Card().ID: evaluatorAgent,
	}

	// Deterministic router — uses routerLLM for lightweight utility calls (validation, parsing)
	router := agents.NewRouter(store, cfg, routerLLM, subAgents)

	return &ChatHandler{
		cfg:    cfg,
		store:  store,
		router: router,
	}
}

// Handle is the Gin handler for POST /chat.
//
// Accepts EITHER:
//   - application/json  — { user_id, session_id, message: { text, image_url? } }
//   - multipart/form-data — fields: user_id, session_id, text  +  file: image
//
// When a file is uploaded it is stored in the DB and converted to a base64 data
// URI so the vision model can consume it.
func (h *ChatHandler) Handle(c *gin.Context) {
	parsed, err := h.parseRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	// Ensure a conversation exists for this user + session
	conv, err := h.store.GetConversation(ctx, parsed.UserID, parsed.SessionID)
	if err != nil {
		conv = &models.Conversation{
			ID:        uuid.New().String(),
			UserID:    parsed.UserID,
			SessionID: parsed.SessionID,
			CreatedAt: time.Now().UTC(),
		}
		if err := h.store.CreateConversation(ctx, conv); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create conversation"})
			return
		}
		slog.Info("new conversation", "id", conv.ID, "user", conv.UserID, "session", conv.SessionID)
	}

	// Persist the user message
	contentType := "text"
	content := parsed.Text
	if parsed.ImageURI != "" {
		contentType = "image_url"
		if content == "" {
			content = "[image]"
		}
	}
	// For button actions without typed text, record the action itself
	if content == "" {
		switch parsed.Action {
		case ActionMoreHelp:
			content = "[more_help]"
		case ActionShowSolution:
			content = "[show_solution]"
		case ActionExtractProceed:
			content = "[extract_proceed]"
		case ActionRetryModel:
			content = fmt.Sprintf("[retry_model:%s]", parsed.Model)
		case ActionSubmitAttempt:
			content = "[submit_attempt]"
		case ActionClose:
			content = "[close]"
		}
	}
	userMsg := &models.Message{
		ID:             uuid.New().String(),
		ConversationID: conv.ID,
		Role:           "user",
		Content:        content,
		ContentType:    contentType,
		CreatedAt:      time.Now().UTC(),
	}
	if err := h.store.SaveMessage(ctx, userMsg); err != nil {
		slog.Warn("failed to save user message", "error", err)
	}

	// If an image file was uploaded, store it in the DB
	var imageID string
	if len(parsed.ImageData) > 0 {
		img := &models.Image{
			ID:             uuid.New().String(),
			ConversationID: conv.ID,
			MessageID:      userMsg.ID,
			Filename:       parsed.ImageName,
			MimeType:       parsed.ImageMime,
			Data:           parsed.ImageData,
			CreatedAt:      time.Now().UTC(),
		}
		if err := h.store.SaveImage(ctx, img); err != nil {
			slog.Error("failed to save image", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store image"})
			return
		}
		imageID = img.ID
		slog.Info("stored image", "id", img.ID, "size", len(parsed.ImageData), "mime", parsed.ImageMime)
	}

	// Build A2A task
	var parts []a2a.Part
	if parsed.Text != "" {
		parts = append(parts, a2a.TextPart(parsed.Text))
	}
	if parsed.ImageURI != "" {
		parts = append(parts, a2a.ImagePart(parsed.ImageURI))
	}

	metadata := map[string]string{
		"user_id":         parsed.UserID,
		"session_id":      parsed.SessionID,
		"conversation_id": conv.ID,
		"action":          parsed.Action,
	}
	if imageID != "" {
		metadata["image_id"] = imageID
	}
	if parsed.Model != "" {
		metadata["model"] = parsed.Model
	}
	if parsed.Category != "" {
		metadata["category"] = parsed.Category
	}

	task := &a2a.Task{
		ID:        uuid.New().String(),
		AgentID:   "router",
		State:     a2a.TaskStateSubmitted,
		Input:     a2a.Message{Role: "user", Parts: parts},
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
	}

	// Stream SSE
	eventCh := make(chan a2a.StreamEvent, 128)
	go func() {
		h.router.HandleStream(ctx, task, eventCh)
		close(eventCh)
	}()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Conversation-ID", conv.ID)
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	var fullResponse string

	for ev := range eventCh {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		flusher.Flush()

		if ev.Message != nil {
			for _, p := range ev.Message.Parts {
				if p.Type == "text" && ev.Type == "artifact" {
					fullResponse += p.Text
				}
			}
		}
	}

	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()

	// Persist the full assistant response
	if fullResponse != "" {
		assistantMsg := &models.Message{
			ID:             uuid.New().String(),
			ConversationID: conv.ID,
			Role:           "assistant",
			Content:        fullResponse,
			ContentType:    "text",
			Agent:          "router",
			CreatedAt:      time.Now().UTC(),
		}
		if err := h.store.SaveMessage(ctx, assistantMsg); err != nil {
			slog.Warn("failed to save assistant message", "error", err)
		}
	}
}

// parseRequest detects content-type and returns a normalised chatParsed.
func (h *ChatHandler) parseRequest(c *gin.Context) (*chatParsed, error) {
	ct := c.ContentType()

	// --- multipart/form-data ---
	if strings.HasPrefix(ct, "multipart/form-data") {
		userID := c.PostForm("user_id")
		sessionID := c.PostForm("session_id")
		text := c.PostForm("text")

		if userID == "" || sessionID == "" {
			return nil, fmt.Errorf("user_id and session_id are required")
		}

		parsed := &chatParsed{
			UserID:    userID,
			SessionID: sessionID,
			Action:    c.PostForm("action"),
			Model:     c.PostForm("model"),
			Category:  c.PostForm("category"),
			Text:      text,
		}

		// Check for uploaded image
		fileHeader, err := c.FormFile("image")
		if err == nil {
			file, err := fileHeader.Open()
			if err != nil {
				return nil, fmt.Errorf("failed to open uploaded file: %w", err)
			}
			defer file.Close()

			mimeType := ""
			// Read raw bytes first to detect mime
			imageBytes, err := io.ReadAll(file)
			if err != nil {
				return nil, fmt.Errorf("failed to read uploaded file: %w", err)
			}
			mimeType = http.DetectContentType(imageBytes)

			// Resize if too large (max 1568px on longest side)
			resizedBytes, resizedMime, resizeErr := resizeImage(imageBytes, mimeType)
			if resizeErr != nil {
				slog.Warn("image resize failed, using original", "error", resizeErr)
				// Fall through with original bytes
			} else {
				imageBytes = resizedBytes
				mimeType = resizedMime
			}

			b64 := base64.StdEncoding.EncodeToString(imageBytes)

			parsed.ImageURI = fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
			parsed.ImageData = imageBytes
			parsed.ImageMime = mimeType
			parsed.ImageName = fileHeader.Filename
		}

		// For close/more_help/show_solution/retry_model/extract_proceed actions, text is optional
		if parsed.Text == "" && parsed.ImageURI == "" {
			switch parsed.Action {
			case ActionClose, ActionMoreHelp, ActionShowSolution, ActionRetryModel, ActionExtractProceed:
				// No text needed — backend reads from DB
			case ActionSubmitAttempt:
				// submit_attempt needs text OR image (photo of handwritten work)
				return nil, fmt.Errorf("submit_attempt requires text or an image of your work")
			default:
				return nil, fmt.Errorf("provide at least text or an image")
			}
		}
		if parsed.Text == "" && parsed.ImageURI != "" {
			// For submit_attempt with image-only, don't override with "solve this question"
			if parsed.Action != ActionSubmitAttempt {
				parsed.Text = "solve this question"
			}
		}
		if parsed.Action == "" {
			parsed.Action = ActionNewQuestion
		}

		return parsed, nil
	}

	// --- application/json (default) ---
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return nil, err
	}

	action := req.Action
	if action == "" {
		action = ActionNewQuestion
	}

	parsed := &chatParsed{
		UserID:    req.UserID,
		SessionID: req.SessionID,
		Action:    action,
		Model:     req.Model,
		Category:  req.Category,
		Text:      req.Message.Text,
		ImageURI:  req.Message.ImageURL,
	}
	return parsed, nil
}
