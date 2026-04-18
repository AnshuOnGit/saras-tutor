package agents

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"saras-tutor/a2a"
	"saras-tutor/config"
	"saras-tutor/db"
	"saras-tutor/llm"
	"saras-tutor/models"

	"github.com/google/uuid"
)

// ── Router ─────────────────────────────────────────────────────────────
// Deterministic dispatcher — no LLM call for routing.
//
// The frontend sends an explicit "action" in task.Metadata["action"]:
//
//   new_question   — student types a question or uploads an image
//   more_help      — student wants the next hint
//   show_solution  — student wants the full solution now
//   close          — student is satisfied, dismiss the interaction
//   retry_model    — student picked an alternative model after verifier failure
//
// The router reads the current interaction state from the DB and
// dispatches to the appropriate agent.

// Router replaces the old LLM-based supervisor. It implements a2a.Agent.
type Router struct {
	store     *db.Store
	cfg       *config.Config
	llmClient *llm.Client // lightweight client for utility calls (validation, etc.)
	subAgents map[string]a2a.Agent

	// hintsByInteraction caches the raw hint text emitted for each interaction
	// in the order they were generated. The solver receives these hints as
	// extra context so its solution flows naturally from what the student has
	// already been told. Cleared when an interaction is closed.
	hintsMu            sync.Mutex
	hintsByInteraction map[string][]string
}

// NewRouter creates the deterministic router.
func NewRouter(store *db.Store, cfg *config.Config, llmClient *llm.Client, subAgents map[string]a2a.Agent) *Router {
	return &Router{
		store:              store,
		cfg:                cfg,
		llmClient:          llmClient,
		subAgents:          subAgents,
		hintsByInteraction: make(map[string][]string),
	}
}

// recordHint appends a generated hint to the per-interaction cache so the
// solver can later be primed with everything the student has already seen.
func (r *Router) recordHint(interactionID, hint string) {
	hint = strings.TrimSpace(hint)
	if interactionID == "" || hint == "" {
		return
	}
	r.hintsMu.Lock()
	defer r.hintsMu.Unlock()
	r.hintsByInteraction[interactionID] = append(r.hintsByInteraction[interactionID], hint)
}

// getHints returns a copy of the hints cached for an interaction.
func (r *Router) getHints(interactionID string) []string {
	r.hintsMu.Lock()
	defer r.hintsMu.Unlock()
	src := r.hintsByInteraction[interactionID]
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// clearHints drops cached hints for an interaction (called on close).
func (r *Router) clearHints(interactionID string) {
	r.hintsMu.Lock()
	defer r.hintsMu.Unlock()
	delete(r.hintsByInteraction, interactionID)
}

// Card satisfies a2a.Agent.
func (r *Router) Card() a2a.AgentCard {
	return a2a.AgentCard{
		ID:          "router",
		Name:        "Router",
		Description: "Deterministic dispatcher based on action + interaction state.",
		Skills:      []string{"routing"},
	}
}

// Handle satisfies a2a.Agent (sync wrapper).
func (r *Router) Handle(ctx context.Context, task *a2a.Task) (*a2a.Task, error) {
	ch := make(chan a2a.StreamEvent, 128)
	go func() {
		r.HandleStream(ctx, task, ch)
		close(ch)
	}()
	var lastMsg *a2a.Message
	for ev := range ch {
		if ev.Message != nil {
			lastMsg = ev.Message
		}
		if ev.Type == "error" {
			task.State = a2a.TaskStateFailed
			task.Output = &a2a.Message{Role: "agent", Parts: []a2a.Part{a2a.TextPart(ev.Error)}}
			return task, fmt.Errorf("%s", ev.Error)
		}
	}
	task.State = a2a.TaskStateCompleted
	task.Output = lastMsg
	return task, nil
}

// HandleStream is the primary entry point.
func (r *Router) HandleStream(ctx context.Context, task *a2a.Task, out chan<- a2a.StreamEvent) {
	out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateWorking}

	action := "new_question" // default
	if task.Metadata != nil && task.Metadata["action"] != "" {
		action = task.Metadata["action"]
	}

	convID := ""
	userID := ""
	if task.Metadata != nil {
		convID = task.Metadata["conversation_id"]
		userID = task.Metadata["user_id"]
	}

	slog.Info("router dispatch", "action", action, "conv", convID, "user", userID, "task", task.ID)

	switch action {
	case "new_question":
		r.handleNewQuestion(ctx, task, convID, userID, out)
	case "extract_proceed":
		r.handleExtractProceed(ctx, task, convID, userID, out)
	case "more_help":
		r.handleMoreHelp(ctx, task, convID, userID, out)
	case "show_solution":
		r.handleShowSolution(ctx, task, convID, userID, out)
	case "retry_model":
		r.handleRetryModel(ctx, task, convID, userID, out)
	case "submit_attempt":
		r.handleSubmitAttempt(ctx, task, convID, userID, out)
	case "close":
		r.handleClose(ctx, task, convID, userID, out)
	default:
		r.handleNewQuestion(ctx, task, convID, userID, out)
	}
}

// ── Action handlers ──────────────────────────────────────────────────

func (r *Router) handleNewQuestion(ctx context.Context, task *a2a.Task, convID, userID string, out chan<- a2a.StreamEvent) {
	// 1. Close any active interaction (student moved on)
	if convID != "" {
		if err := r.store.CloseAllActive(ctx, convID, models.ExitNewQuestion); err != nil {
			slog.Warn("router: close active interactions", "error", err)
		}
	}

	// Gather text from input
	questionText := extractText(task.Input)
	hasImage := hasImageInput(task.Input)

	// 2. Image extraction path — extract only, then gate on user confirmation.
	if hasImage {
		// Create the extracting-state interaction up-front so the image_id is
		// persisted BEFORE we attempt extraction. This way, even if extraction
		// fails, a retry_model click can find the interaction, look up the
		// stored image_id, and re-run extraction with a different vision model.
		imageID := ""
		if task.Metadata != nil {
			imageID = task.Metadata["image_id"]
		}
		interaction := &models.Interaction{
			ID:             uuid.New().String(),
			ConversationID: convID,
			QuestionText:   "",
			ImageID:        imageID,
			State:          models.InteractionExtracting,
			HintLevel:      0,
			CreatedAt:      time.Now().UTC(),
		}
		if err := r.store.CreateInteraction(ctx, interaction); err != nil {
			slog.Warn("router: create extracting interaction", "error", err)
		}

		extracted, err := r.extractImage(ctx, task, out)
		if err != nil {
			slog.Warn("router: image extraction failed, offering retry", "error", err)
			r.offerFailureRetry(out, config.CategoryVision, config.DefaultVisionModel(), interaction.ID,
				"Image extraction failed: "+err.Error())
			return
		}
		r.showExtractedAndGate(ctx, task, convID, userID, extracted, out)
		return
	}

	if questionText == "" {
		out <- a2a.StreamEvent{Type: "error", Error: "no question text provided"}
		return
	}

	// Text-only path — no extraction gate needed, proceed straight to validation.
	r.validateParseAndHint(ctx, task, convID, userID, questionText, "", out)
}

// showExtractedAndGate persists the extracted text in a new interaction with
// state "extracting" and emits an extraction-gate picker so the user can
// either confirm (extract_proceed) or retry with a different vision model.
func (r *Router) showExtractedAndGate(ctx context.Context, task *a2a.Task, convID, userID, extracted string, out chan<- a2a.StreamEvent) {
	visionModel := config.DefaultVisionModel()

	// Show the extracted content as an artifact
	out <- a2a.StreamEvent{
		Type: "artifact",
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf("📄 **Extracted from image** _(model: %s)_:\n\n%s\n\n---\n", visionModel, extracted))},
		},
	}

	// Load or create the extracting-state interaction for this conversation.
	// We reuse it across Vision retries so Proceed always acts on the latest
	// extracted text.
	imageID := ""
	if task.Metadata != nil {
		imageID = task.Metadata["image_id"]
	}

	interaction, _ := r.store.GetActiveInteraction(ctx, convID)
	if interaction == nil || interaction.State != models.InteractionExtracting {
		interaction = &models.Interaction{
			ID:             uuid.New().String(),
			ConversationID: convID,
			QuestionText:   extracted,
			ImageID:        imageID,
			State:          models.InteractionExtracting,
			HintLevel:      0,
			CreatedAt:      time.Now().UTC(),
		}
		if err := r.store.CreateInteraction(ctx, interaction); err != nil {
			slog.Warn("router: create extracting interaction", "error", err)
		}
	} else {
		// Refresh extracted text on retries
		interaction.QuestionText = extracted
		if err := r.store.UpdateInteractionQuestion(ctx, interaction.ID, extracted); err != nil {
			slog.Warn("router: update interaction question", "error", err)
		}
	}

	// Emit extraction gate picker: Proceed is primary, alternatives re-run extraction.
	r.emitAlternativesGated(out, config.CategoryVision, visionModel, interaction.ID, "extract_proceed",
		"Does the extracted text look correct? If not, try a different vision model.")
}

// validateParseAndHint runs validation + parsing on the question text, creates
// a fresh interaction (or upgrades the extracting one), and dispatches the
// first hint. Used for text-only new questions and for extract_proceed.
func (r *Router) validateParseAndHint(ctx context.Context, task *a2a.Task, convID, userID, questionText, existingInteractionID string, out chan<- a2a.StreamEvent) {
	// 1. Validate the question is about an allowed subject
	routerModel := config.DefaultRouterModel()
	out <- a2a.StreamEvent{
		Type: "artifact",
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf("⏳ **Validating & analysing question…** _(model: %s | purpose: validation + parsing)_\n\n", routerModel))},
		},
	}
	validateStart := time.Now()
	vr, _ := ValidateQuestion(ctx, r.llmClient, questionText)
	slog.Info("router: validation done", "elapsed_ms", time.Since(validateStart).Milliseconds())
	if vr != nil && !vr.Valid {
		slog.Info("router: question rejected", "reason", vr.Reason)
		out <- a2a.StreamEvent{
			Type: "artifact",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.TextPart(InvalidQuestionMessage(vr))},
			},
		}
		// If we had a pending extracting interaction, close it.
		if existingInteractionID != "" {
			_ = r.store.CloseInteraction(ctx, existingInteractionID, models.InteractionClosed, models.ExitNewQuestion)
		}
		out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
		return
	}

	// 2. Parse
	parseStart := time.Now()
	parsedQ, _ := ParseQuestion(ctx, r.llmClient, questionText)
	slog.Info("router: parsing done", "elapsed_ms", time.Since(parseStart).Milliseconds())
	var subjectName, chapterName string
	var topicNames []string
	difficulty := 0
	problemText := questionText
	if parsedQ != nil {
		subjectName = parsedQ.Subject
		chapterName = parsedQ.Chapter
		topicNames = parsedQ.Topics
		difficulty = parsedQ.Difficulty
		if parsedQ.Question != "" {
			problemText = parsedQ.Question
		}
	}
	slog.Info("router: parsed metadata", "subject", subjectName, "chapter", chapterName, "topics", topicNames, "difficulty", difficulty)

	subjectID := r.store.LookupSubjectID(ctx, subjectName)
	topicIDs := r.store.LookupTopicIDs(ctx, topicNames)

	// 3. Upgrade or create interaction
	var interaction *models.Interaction
	if existingInteractionID != "" {
		interaction, _ = r.store.GetInteractionByID(ctx, existingInteractionID)
	}
	if interaction == nil {
		imageID := ""
		if task.Metadata != nil {
			imageID = task.Metadata["image_id"]
		}
		interaction = &models.Interaction{
			ID:             uuid.New().String(),
			ConversationID: convID,
			QuestionText:   questionText,
			ImageID:        imageID,
			SubjectID:      subjectID,
			TopicIDs:       topicIDs,
			Difficulty:     difficulty,
			ProblemText:    problemText,
			State:          models.InteractionNew,
			HintLevel:      0,
			CreatedAt:      time.Now().UTC(),
		}
		if err := r.store.CreateInteraction(ctx, interaction); err != nil {
			slog.Warn("router: create interaction", "error", err)
		}
	} else {
		// Upgrade the extracting-state interaction in place
		interaction.QuestionText = questionText
		interaction.SubjectID = subjectID
		interaction.TopicIDs = topicIDs
		interaction.Difficulty = difficulty
		interaction.ProblemText = problemText
		interaction.State = models.InteractionNew
		if err := r.store.EnrichInteraction(ctx, interaction); err != nil {
			slog.Warn("router: enrich interaction", "error", err)
		}
	}

	// 4. Student profile housekeeping
	if userID != "" {
		if _, err := r.store.GetOrCreateProfile(ctx, userID); err != nil {
			slog.Warn("router: get/create profile", "error", err)
		}
		if err := r.store.IncrementProfileQuestions(ctx, userID); err != nil {
			slog.Warn("router: increment questions", "error", err)
		}
	}

	// 5. Dispatch first hint
	out <- a2a.StreamEvent{
		Type: "artifact",
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf("💡 **Generating hint…** _(model: %s | purpose: hint generation)_\n\n", config.DefaultHintModel()))},
		},
	}
	r.dispatchHint(ctx, task, interaction, 1, out)
}

// handleExtractProceed is called when the user confirms the extracted text
// from the Vision gate. It runs validation + parsing on the stored extract
// and dispatches the first hint.
func (r *Router) handleExtractProceed(ctx context.Context, task *a2a.Task, convID, userID string, out chan<- a2a.StreamEvent) {
	interaction, err := r.store.GetActiveInteraction(ctx, convID)
	if err != nil || interaction == nil {
		out <- a2a.StreamEvent{Type: "error", Error: "no pending extraction to proceed from"}
		return
	}
	if interaction.State != models.InteractionExtracting {
		// Not in extracting state — already proceeded or something else; re-emit hint flow
		slog.Info("router: extract_proceed on non-extracting interaction, dispatching next hint", "state", interaction.State)
		r.dispatchHint(ctx, task, interaction, interaction.HintLevel+1, out)
		return
	}
	r.validateParseAndHint(ctx, task, convID, userID, interaction.QuestionText, interaction.ID, out)
}

func (r *Router) handleMoreHelp(ctx context.Context, task *a2a.Task, convID, userID string, out chan<- a2a.StreamEvent) {
	interaction, err := r.store.GetActiveInteraction(ctx, convID)
	if err != nil || interaction == nil {
		// No active interaction — treat typed text as new question
		slog.Info("router: no active interaction for more_help, treating as new question")
		r.handleNewQuestion(ctx, task, convID, userID, out)
		return
	}

	nextLevel := interaction.HintLevel + 1
	// Soft cap at 5 to prevent runaway hinting. Beyond this, show the full
	// solution instead. Hint levels are no longer gated at 1/2/3 — each call
	// produces a progressively deeper hint via the unified prompt.
	if nextLevel > 5 {
		slog.Info("router: hint cap reached, escalating to full solution", "hint_level", interaction.HintLevel)
		r.handleShowSolution(ctx, task, convID, userID, out)
		return
	}

	r.dispatchHint(ctx, task, interaction, nextLevel, out)
}

func (r *Router) handleShowSolution(ctx context.Context, task *a2a.Task, convID, userID string, out chan<- a2a.StreamEvent) {
	interaction, err := r.store.GetActiveInteraction(ctx, convID)
	if err != nil || interaction == nil {
		slog.Info("router: no active interaction for show_solution, treating as new question")
		r.handleNewQuestion(ctx, task, convID, userID, out)
		return
	}

	solverModel := config.DefaultSolverModel()
	slog.Info("router: dispatching solver", "interaction", interaction.ID, "model", solverModel)
	emitTransition(out, "router", "solver", task.ID,
		fmt.Sprintf("full solution requested | model: %s | purpose: solver", solverModel))

	// Dispatch solver with the original question (+ image if verifier low)
	completed := r.dispatchSolver(ctx, task, interaction, out)

	if completed {
		// Mark interaction as solved only if fully completed (not awaiting model picker)
		if err := r.store.CloseInteraction(ctx, interaction.ID, models.InteractionSolved, models.ExitNeededSolution); err != nil {
			slog.Warn("router: close interaction", "error", err)
		}
		r.clearHints(interaction.ID)

	} else {
		slog.Info("router: solver awaiting model picker", "interaction", interaction.ID)
	}
}

func (r *Router) handleClose(ctx context.Context, task *a2a.Task, convID, userID string, out chan<- a2a.StreamEvent) {
	interaction, err := r.store.GetActiveInteraction(ctx, convID)
	if err != nil || interaction == nil {
		out <- a2a.StreamEvent{
			Type: "artifact",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.TextPart("No active question to close. Feel free to ask a new one!")},
			},
		}
		out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
		return
	}

	// Determine exit reason
	exitReason := models.ExitReasonForState(interaction.State)

	if err := r.store.CloseInteraction(ctx, interaction.ID, models.InteractionClosed, exitReason); err != nil {
		slog.Warn("router: close interaction", "error", err)
	}
	r.clearHints(interaction.ID)

	slog.Info("router: interaction closed", "id", interaction.ID, "exit", exitReason, "hints", interaction.HintLevel)

	out <- a2a.StreamEvent{
		Type: "artifact",
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart("Great job working through it! 🎯 Feel free to ask another question whenever you're ready.")},
		},
	}
	out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
}

func (r *Router) handleSubmitAttempt(ctx context.Context, task *a2a.Task, convID, userID string, out chan<- a2a.StreamEvent) {
	interaction, err := r.store.GetActiveInteraction(ctx, convID)
	if err != nil || interaction == nil {
		slog.Info("router: no active interaction for submit_attempt, treating as new question")
		r.handleNewQuestion(ctx, task, convID, userID, out)
		return
	}

	studentText := extractText(task.Input)
	hasImage := hasImageInput(task.Input)

	if studentText == "" && !hasImage {
		out <- a2a.StreamEvent{Type: "error", Error: "submit_attempt requires your work as text or a photo"}
		return
	}

	// Extract text from image via OCR (same pipeline as new_question)
	if hasImage {
		extracted, err := r.extractImage(ctx, task, out)
		if err != nil {
			slog.Warn("router: attempt image extraction failed, offering retry", "error", err)
			r.offerFailureRetry(out, config.CategoryVision, config.DefaultVisionModel(), interaction.ID,
				"Could not read your image: "+err.Error())
			return
		}
		if studentText != "" {
			studentText = studentText + "\n\n[Extracted from image]\n" + extracted
		} else {
			studentText = extracted
		}
	}

	hintLevel := interaction.HintLevel
	if hintLevel < 1 {
		hintLevel = 1
	}

	// Dispatch evaluator
	evaluator, ok := r.subAgents["attempt_evaluator"]
	if !ok {
		out <- a2a.StreamEvent{Type: "error", Error: "attempt_evaluator agent not registered"}
		return
	}

	evalAgent, isEvalAgent := evaluator.(*AttemptEvaluatorAgent)
	if !isEvalAgent {
		out <- a2a.StreamEvent{Type: "error", Error: "attempt_evaluator has wrong type"}
		return
	}

	evalModel := config.DefaultRouterModel()
	emitTransition(out, "router", "attempt_evaluator", task.ID,
		fmt.Sprintf("evaluating student attempt at hint level %d | model: %s | purpose: attempt evaluation", hintLevel, evalModel))

	result, evalErr := evalAgent.Evaluate(ctx, interaction.QuestionText, studentText, hintLevel)
	if evalErr != nil {
		slog.Error("router: evaluator error, offering retry", "error", evalErr)
		r.offerFailureRetry(out, config.CategoryRouter, evalModel, interaction.ID,
			"Failed to evaluate your attempt: "+evalErr.Error())
		return
	}

	emitTransition(out, "attempt_evaluator", "router", task.ID,
		fmt.Sprintf("evaluation complete — score %.0f%% | model: %s", result.Score*100, evalModel))

	// Persist the attempt — for image-only, record a placeholder in student_message
	attemptMessage := studentText
	if attemptMessage == "" {
		attemptMessage = "[image submission]"
	}
	attempt := &models.StudentAttempt{
		InteractionID:  interaction.ID,
		UserID:         userID,
		HintIndex:      hintLevel,
		StudentMessage: attemptMessage,
		EvaluatorJSON:  *result,
	}
	if err := r.store.SaveStudentAttempt(ctx, attempt); err != nil {
		slog.Warn("router: save student attempt", "error", err)
	}

	// Emit evaluator metadata
	out <- a2a.StreamEvent{
		Type: "metadata",
		Meta: map[string]interface{}{
			"agent":         "attempt_evaluator",
			"score":         result.Score,
			"correct":       result.Correct,
			"hint_consumed": result.HintConsumed,
		},
	}

	// Compute best-of-all score across every attempt on this interaction,
	// including the one we just saved.
	attemptsAll, _ := r.store.GetAttemptsByInteraction(ctx, interaction.ID)
	bestScore := result.Score
	bestCorrect := result.Correct
	for _, a := range attemptsAll {
		if a.EvaluatorJSON.Score > bestScore {
			bestScore = a.EvaluatorJSON.Score
			bestCorrect = a.EvaluatorJSON.Correct
		}
	}

	r.emitAttemptFeedback(out, result, bestScore, bestCorrect, evalModel)

	// Decide next state based on BEST result across all evaluations.
	if bestCorrect && bestScore >= 0.8 {
		exitReason := models.ExitReasonForState(interaction.State)
		if err := r.store.CloseInteraction(ctx, interaction.ID, models.InteractionClosed, exitReason); err != nil {
			slog.Warn("router: close interaction after correct attempt", "error", err)
		}
		r.clearHints(interaction.ID)
		out <- a2a.StreamEvent{
			Type: "artifact",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.TextPart("\n---\n🎯 **Well done!** You solved it. Ask another question whenever you're ready.")},
			},
		}
		out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
		slog.Info("router: student attempt correct", "interaction", interaction.ID, "best_score", bestScore)
		return
	}

	// Not fully correct yet — keep in waiting_for_attempt and offer evaluator alternatives.
	if err := r.store.UpdateInteraction(ctx, interaction.ID, models.InteractionWaitingForAttempt, hintLevel); err != nil {
		slog.Warn("router: update interaction to waiting_for_attempt", "error", err)
	}
	r.emitAlternatives(out, config.CategoryRouter, evalModel, interaction.ID, true,
		fmt.Sprintf("Evaluator %s scored %.0f%% (best: %.0f%%). Try a different evaluator?", evalModel, result.Score*100, bestScore*100))
	out <- a2a.StreamEvent{
		Type:  "status",
		State: a2a.TaskStateInputNeeded,
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf(`{"hint_level":%d,"attempt_evaluated":true,"score":%.2f,"best_score":%.2f}`, hintLevel, result.Score, bestScore))},
		},
	}
	slog.Info("router: attempt scored, waiting", "score", result.Score, "best_score", bestScore, "interaction", interaction.ID)
}

// ── Dispatchers ──────────────────────────────────────────────────────

func (r *Router) dispatchHint(ctx context.Context, task *a2a.Task, interaction *models.Interaction, level int, out chan<- a2a.StreamEvent) {
	r.dispatchHintWithModel(ctx, task, interaction, level, "", out)
}

// dispatchHintWithModel streams a hint at the given level. If modelOverride
// is non-empty, a temporary HintAgent is created using that model; otherwise
// the default HintGenerator model is used.
func (r *Router) dispatchHintWithModel(ctx context.Context, task *a2a.Task, interaction *models.Interaction, level int, modelOverride string, out chan<- a2a.StreamEvent) {
	hintStart := time.Now()
	hintAgent, ok := r.subAgents["hint"]
	if !ok {
		out <- a2a.StreamEvent{Type: "error", Error: "hint agent not registered"}
		return
	}

	hintTaskID := uuid.New().String()
	hintModel := config.DefaultHintModel()
	if modelOverride != "" {
		altClient := llm.NewClient(r.cfg.LLMAPIKey, modelOverride, r.cfg.LLMBaseURL, r.cfg.LLMUserID)
		hintAgent = NewHintAgent(altClient, r.store)
		hintModel = modelOverride
		slog.Info("router: using override hint model", "model", modelOverride)
	}
	emitTransition(out, "router", "hint", hintTaskID,
		fmt.Sprintf("hint level %d | model: %s | purpose: hint generation", level, hintModel))

	// All hints use extracted text only — images are consumed solely by
	// the image_extraction agent to avoid redundant vision-model costs.
	inputParts := []a2a.Part{a2a.TextPart(interaction.QuestionText)}
	slog.Info("router: dispatching hint", "level", level, "model", hintModel)

	hintTask := &a2a.Task{
		ID:      hintTaskID,
		AgentID: "hint",
		State:   a2a.TaskStateSubmitted,
		Input: a2a.Message{
			Role:  "user",
			Parts: inputParts,
		},
		Metadata: map[string]string{
			"hint_level": fmt.Sprintf("%d", level),
		},
		CreatedAt: time.Now().UTC(),
	}

	// Stream hint to client, intercepting any error events so we can offer
	// a HintGenerator model-picker instead of letting the task abort. We also
	// capture the rendered hint text so the solver can be primed with the
	// hints the student has already seen.
	hintText, hintErr := r.streamWithErrorCapture(ctx, hintAgent, hintTask, out)
	hintDuration := time.Since(hintStart)
	if hintErr != "" {
		slog.Warn("router: hint stream failed, offering retry", "error", hintErr, "model", hintModel)
		r.offerFailureRetry(out, config.CategoryHintGenerator, hintModel, interaction.ID,
			"Hint generation failed: "+hintErr)
		return
	}
	r.recordHint(interaction.ID, hintText)
	emitTransition(out, "hint", "router", hintTaskID,
		fmt.Sprintf("hint level %d generated (%.1fs) | model: %s", level, hintDuration.Seconds(), hintModel))
	slog.Info("router: hint streamed",
		"level", level,
		"elapsed_ms", hintDuration.Milliseconds(),
		"elapsed_s", fmt.Sprintf("%.1f", hintDuration.Seconds()))

	// First set the hint state, then move to waiting_for_attempt
	var hintState models.InteractionState
	switch level {
	case 1:
		hintState = models.InteractionHint1
	case 2:
		hintState = models.InteractionHint2
	case 3:
		hintState = models.InteractionHint3
	default:
		hintState = models.InteractionHint1
	}
	if err := r.store.UpdateInteraction(ctx, interaction.ID, hintState, level); err != nil {
		slog.Warn("router: update interaction state", "error", err)
	}
	// Transition to waiting_for_attempt so we know the student has been invited to try
	if err := r.store.UpdateInteraction(ctx, interaction.ID, models.InteractionWaitingForAttempt, level); err != nil {
		slog.Warn("router: set waiting_for_attempt", "error", err)
	}

	// Append encouragement
	var prompt string
	switch level {
	case 1:
		prompt = "\n\n---\n💡 **Try solving it!** Submit your attempt, or ask for more help."
	case 2:
		prompt = "\n\n---\n💡 **Give it a try!** Submit your work, ask for another hint, or ask for the full solution."
	case 3:
		prompt = "\n\n---\n💡 **You're almost there!** Submit your final step, or ask for the solution."
	}
	out <- a2a.StreamEvent{
		Type: "artifact",
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(prompt)},
		},
	}

	// Offer optional HintGenerator alternatives so the student can regenerate
	// the hint with a different model if this one didn't click.
	r.emitAlternatives(out, config.CategoryHintGenerator, hintModel, interaction.ID, true,
		fmt.Sprintf("Hint generated with %s. Want to try another hint style?", hintModel))

	// Emit input-needed with hint_level so frontend shows action buttons (including submit_attempt)
	out <- a2a.StreamEvent{
		Type:  "status",
		State: a2a.TaskStateInputNeeded,
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf(`{"hint_level":%d,"awaiting_attempt":true}`, level))},
		},
	}
	slog.Info("router: hint delivered, waiting for attempt", "level", level, "interaction", interaction.ID)
}

// dispatchSolver returns true if the solution was accepted/completed,
// false if the model picker was shown (interaction should stay open).
func (r *Router) dispatchSolver(ctx context.Context, task *a2a.Task, interaction *models.Interaction, out chan<- a2a.StreamEvent) bool {
	return r.dispatchSolverWithModel(ctx, task, interaction, "", out)
}

// dispatchSolverWithModel streams a solution and then verifies it.
// If modelOverride is non-empty, a temporary SolverAgent is created with that model.
//
// Flow:
//  1. Stream text-only solution — capture full text
//  2. Verify via VerifierAgent (text-only)
//  3. If score < threshold → emit input-needed with model options for user to pick
//
// Images are consumed only by the image_extraction agent; all downstream
// agents work exclusively from the extracted text.
func (r *Router) dispatchSolverWithModel(ctx context.Context, task *a2a.Task, interaction *models.Interaction, modelOverride string, out chan<- a2a.StreamEvent) bool {
	solver, ok := r.subAgents["solver"]
	if !ok {
		out <- a2a.StreamEvent{Type: "error", Error: "solver agent not registered"}
		return true
	}

	// If modelOverride is set, create a temporary solver with that model
	if modelOverride != "" {
		altClient := llm.NewClient(r.cfg.LLMAPIKey, modelOverride, r.cfg.LLMBaseURL, r.cfg.LLMUserID)
		solver = NewSolverAgent(altClient, r.store)
		slog.Info("router: using override model", "model", modelOverride)
	}

	verifier, hasVerifier := r.subAgents["verifier"]

	solveTaskID := uuid.New().String()
	modelLabel := config.DefaultSolverModel()
	if modelOverride != "" {
		modelLabel = modelOverride
	}
	emitTransition(out, "router", "solver", solveTaskID,
		fmt.Sprintf("solving question | model: %s | purpose: step-by-step solution", modelLabel))

	// All downstream agents (solver, verifier) work with extracted text only.
	// Images are consumed solely by the image_extraction agent.

	// ── Pass 1: text-only solve ──
	// Compose the solver input: question text plus any hints already shown
	// to the student (so the solution flows from where the student is, and
	// doesn't contradict earlier guidance).
	solverInput := buildSolverInput(interaction.QuestionText, r.getHints(interaction.ID))
	slog.Info("router: solver input composed",
		"question_chars", len(interaction.QuestionText),
		"hint_count", len(r.getHints(interaction.ID)),
		"total_chars", len(solverInput))
	inputParts := []a2a.Part{a2a.TextPart(solverInput)}
	solveTask := &a2a.Task{
		ID:        solveTaskID,
		AgentID:   "solver",
		State:     a2a.TaskStateSubmitted,
		Input:     a2a.Message{Role: "user", Parts: inputParts},
		Metadata:  task.Metadata,
		CreatedAt: time.Now().UTC(),
	}

	fullSolution, solveErr := r.streamAndCapture(ctx, solver, solveTask, out)
	if solveErr != "" {
		slog.Warn("router: solver stream failed, offering retry", "error", solveErr, "model", modelLabel)
		r.offerFailureRetry(out, config.CategorySolverLevel2, modelLabel, interaction.ID,
			"Solver failed: "+solveErr)
		return false
	}
	emitTransition(out, "solver", "router", solveTaskID,
		fmt.Sprintf("solver pass 1 finished | model: %s", modelLabel))

	// ── Verify pass 1 ──
	if !hasVerifier {
		slog.Warn("router: no verifier registered, accepting solution")
		out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
		return true
	}
	va, isVerifierAgent := verifier.(*VerifierAgent)
	if !isVerifierAgent {
		slog.Warn("router: verifier is not VerifierAgent, accepting solution")
		out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
		return true
	}

	verifierModel := config.DefaultRouterModel()
	emitTransition(out, "router", "verifier", solveTaskID,
		fmt.Sprintf("verifying solution quality | model: %s | purpose: verification", verifierModel))
	vResult, vComp, vErr := va.Verify(ctx, interaction.QuestionText, fullSolution, "")
	if vErr != nil {
		slog.Warn("router: verifier error, accepting solution", "error", vErr)
		out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
		return true
	}
	emitVerifierMetadata(out, vResult, vComp, 1)

	if vResult.Score >= MinVerifierScore {
		slog.Info("router: verifier pass 1 accepted", "score", vResult.Score, "threshold", MinVerifierScore)
		// Offer optional solver alternatives so the student can retry with a
		// different model if they want a second opinion, or just proceed.
		r.emitAlternatives(out, config.CategorySolverLevel2, modelLabel, interaction.ID, true,
			fmt.Sprintf("Solved with %s (verifier %.0f%%). Not satisfied? Try another model.", modelLabel, vResult.Score*100))
		out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
		return true
	}

	slog.Info("router: verifier pass 1 low, offering model picker", "score", vResult.Score, "threshold", MinVerifierScore)

	// No image retry — images are only used during extraction.
	// Offer the user a model picker to try a different LLM.
	r.emitModelPicker(out, vResult, interaction)
	return false
}

// buildSolverInput composes the prompt payload for the solver. It always
// starts with the extracted question text and, if any hints have already
// been shown to the student, appends them under a clearly-labelled section
// so the solver knows what guidance the student has seen and can build on
// it rather than contradict it.
func buildSolverInput(questionText string, hints []string) string {
	var b strings.Builder
	b.WriteString("QUESTION:\n")
	b.WriteString(strings.TrimSpace(questionText))
	if len(hints) > 0 {
		b.WriteString("\n\n---\nHINTS ALREADY SHOWN TO THE STUDENT (in order — your solution should be consistent with these):\n")
		for i, h := range hints {
			fmt.Fprintf(&b, "\nHint %d:\n%s\n", i+1, strings.TrimSpace(h))
		}
	}
	return b.String()
}

// streamAndCapture streams a solver task to the output channel while also
// returns any error message emitted via `{Type: "error"}` events, which the
// caller can use to decide whether to offer a model-retry picker instead of
// forwarding the failure to the client.
func (r *Router) streamAndCapture(ctx context.Context, solver a2a.Agent, task *a2a.Task, out chan<- a2a.StreamEvent) (string, string) {
	// Use an intermediate channel to intercept events
	intermediate := make(chan a2a.StreamEvent, 128)
	go func() {
		solver.HandleStream(ctx, task, intermediate)
		close(intermediate)
	}()

	var captured strings.Builder
	var errMsg string
	for ev := range intermediate {
		// Swallow error events — the caller will turn them into a retry picker.
		if ev.Type == "error" {
			if errMsg == "" {
				errMsg = ev.Error
			}
			continue
		}
		// Forward everything else to the real output
		out <- ev
		// Capture artifact text
		if ev.Type == "artifact" && ev.Message != nil {
			for _, p := range ev.Message.Parts {
				if p.Type == "text" {
					captured.WriteString(p.Text)
				}
			}
		}
	}
	return captured.String(), errMsg
}

// streamWithErrorCapture forwards all non-error events from a streaming agent
// to `out` and returns the full captured artifact text together with any
// error string (empty if the stream ended cleanly). Used for hint streaming
// where we want a model picker on failure instead of aborting the whole
// task, and where we also want to remember what was said so the solver can
// be primed with it later.
func (r *Router) streamWithErrorCapture(ctx context.Context, agent a2a.Agent, task *a2a.Task, out chan<- a2a.StreamEvent) (string, string) {
	intermediate := make(chan a2a.StreamEvent, 128)
	go func() {
		agent.HandleStream(ctx, task, intermediate)
		close(intermediate)
	}()
	var captured strings.Builder
	var errMsg string
	for ev := range intermediate {
		if ev.Type == "error" {
			if errMsg == "" {
				errMsg = ev.Error
			}
			continue
		}
		out <- ev
		if ev.Type == "artifact" && ev.Message != nil {
			for _, p := range ev.Message.Parts {
				if p.Type == "text" {
					captured.WriteString(p.Text)
				}
			}
		}
	}
	return captured.String(), errMsg
}

// emitVerifierMetadata sends a metadata event with verifier scores.
func emitVerifierMetadata(out chan<- a2a.StreamEvent, vr *VerificationResult, comp *llm.CompletionResult, pass int) {
	meta := map[string]interface{}{
		"agent":     "verifier",
		"score":     vr.Score,
		"threshold": MinVerifierScore,
		"passed":    vr.Score >= MinVerifierScore,
		"pass":      pass,
		"issues":    vr.Issues,
	}
	if comp != nil {
		meta["model"] = comp.Model
		meta["prompt_tokens"] = comp.Usage.PromptTokens
		meta["completion_tokens"] = comp.Usage.CompletionTokens
		meta["total_tokens"] = comp.Usage.TotalTokens
	}
	out <- a2a.StreamEvent{Type: "metadata", Meta: meta}
}

// emitModelPicker is the legacy verifier-failure picker (mandatory retry).
// It wraps emitAlternatives with optional=false so the frontend knows the
// student must pick a model (or explicitly dismiss) before continuing.
func (r *Router) emitModelPicker(out chan<- a2a.StreamEvent, vr *VerificationResult, interaction *models.Interaction) {
	out <- a2a.StreamEvent{
		Type: "artifact",
		Message: &a2a.Message{
			Role: "agent",
			Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf(
				"\n\n---\n⚠️ *Verifier score: %.0f%% — the solution may have issues: %s*\n\nWould you like to try with a different model?",
				vr.Score*100, vr.Issues))},
		},
	}
	r.emitAlternatives(out, config.CategorySolverLevel2, config.DefaultSolverModel(), interaction.ID, false,
		fmt.Sprintf("Verifier score %.0f%% — try a different solver", vr.Score*100))
}

// emitAlternatives sends an optional "try another model" picker. The caller
// indicates whether the picker blocks progress (optional=false) and may
// supply a proceedAction string that the frontend's primary button will
// dispatch (e.g. extract_proceed).
//
// Payload shape sent over SSE as a status/input-needed event:
//
//	{
//	  "model_picker":    true,
//	  "category":        "Vision",
//	  "current":         "meta/llama-3.2-90b-vision-instruct",
//	  "optional":        true,
//	  "reason":          "Extraction complete. …",
//	  "proceed_action":  "extract_proceed",
//	  "interaction_id":  "…",
//	  "models":          [ {id, display_name, notes, priority}, … ]
//	}
func (r *Router) emitAlternatives(
	out chan<- a2a.StreamEvent,
	category config.ModelCategory,
	currentModel string,
	interactionID string,
	optional bool,
	reason string,
) {
	r.emitPickerPayload(out, category, currentModel, interactionID, optional, "", reason)
}

// emitAlternativesGated emits a mandatory (blocking) picker with a primary
// "Proceed" button that dispatches proceedAction. Used for the extraction
// gate where the student must either accept the current output or try a
// different model before continuing.
func (r *Router) emitAlternativesGated(
	out chan<- a2a.StreamEvent,
	category config.ModelCategory,
	currentModel string,
	interactionID string,
	proceedAction string,
	reason string,
) {
	r.emitPickerPayload(out, category, currentModel, interactionID, false, proceedAction, reason)
}

// offerFailureRetry emits a user-visible error note plus a Vision/Hint/Router/Solver
// model picker so the student can retry the failed step with a different model,
// instead of having the whole task abort. Used whenever an agent returns an
// error or low-confidence result.
func (r *Router) offerFailureRetry(
	out chan<- a2a.StreamEvent,
	category config.ModelCategory,
	currentModel string,
	interactionID string,
	humanMessage string,
) {
	// Friendly artifact message
	out <- a2a.StreamEvent{
		Type: "artifact",
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf("\n⚠️ **%s**\n\n", humanMessage))},
		},
	}
	// Mandatory picker (no proceed action, not optional) — student must pick another model
	r.emitPickerPayload(out, category, currentModel, interactionID, false, "",
		humanMessage+" Please pick a different model to retry.")
}

func (r *Router) emitPickerPayload(
	out chan<- a2a.StreamEvent,
	category config.ModelCategory,
	currentModel string,
	interactionID string,
	optional bool,
	proceedAction string,
	reason string,
) {
	models := config.GetModelsByCategory(category)

	type altModel struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Notes       string `json:"notes,omitempty"`
		Priority    int    `json:"priority"`
	}
	alts := make([]altModel, 0, len(models))
	for _, m := range models {
		if m.ID == currentModel {
			continue
		}
		alts = append(alts, altModel{
			ID:          m.ID,
			DisplayName: m.DisplayName,
			Notes:       m.Notes,
			Priority:    m.Priority,
		})
	}
	// When there are no alternatives AND no proceed action, nothing to show.
	if len(alts) == 0 && proceedAction == "" {
		return
	}

	payload := map[string]interface{}{
		"model_picker":   true,
		"category":       string(category),
		"current":        currentModel,
		"optional":       optional,
		"reason":         reason,
		"interaction_id": interactionID,
		"models":         alts,
	}
	if proceedAction != "" {
		payload["proceed_action"] = proceedAction
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("router: marshal alternatives", "error", err)
		return
	}

	out <- a2a.StreamEvent{
		Type:  "status",
		State: a2a.TaskStateInputNeeded,
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(string(raw))},
		},
	}
}

// handleRetryModel is called when the user picks an alternative model after
// a task (solver, hint, or vision) completes.
//
// Routing is driven by the "category" metadata field. If it is omitted we
// default to the solver category for backwards compatibility with the
// original verifier-failure flow.
func (r *Router) handleRetryModel(ctx context.Context, task *a2a.Task, convID, userID string, out chan<- a2a.StreamEvent) {
	interaction, err := r.store.GetActiveInteraction(ctx, convID)
	if err != nil || interaction == nil {
		slog.Info("router: no active interaction for retry_model, treating as new question")
		r.handleNewQuestion(ctx, task, convID, userID, out)
		return
	}

	modelName := ""
	category := ""
	if task.Metadata != nil {
		modelName = task.Metadata["model"]
		category = task.Metadata["category"]
	}
	if modelName == "" {
		out <- a2a.StreamEvent{Type: "error", Error: "retry_model requires a 'model' in metadata"}
		return
	}

	slog.Info("router: retry_model", "model", modelName, "category", category, "interaction", interaction.ID)

	switch config.ModelCategory(category) {
	case config.CategoryVision:
		// Re-extract the ORIGINAL image using the chosen vision model.
		// The stored image_id links us back to the raw bytes.
		if interaction.ImageID == "" {
			out <- a2a.StreamEvent{Type: "error", Error: "no image associated with this interaction"}
			return
		}
		emitTransition(out, "router", "image_extraction", task.ID,
			fmt.Sprintf("retry extraction with alternate vision model | model: %s", modelName))

		extracted, err := r.extractImageWithModel(ctx, interaction.ImageID, modelName, out)
		if err != nil {
			slog.Warn("router: re-extraction failed, offering retry", "error", err, "model", modelName)
			r.offerFailureRetry(out, config.CategoryVision, modelName, interaction.ID,
				"Re-extraction with "+modelName+" failed: "+err.Error())
			return
		}
		// Update interaction with new extraction + re-gate
		interaction.QuestionText = extracted
		if err := r.store.UpdateInteractionQuestion(ctx, interaction.ID, extracted); err != nil {
			slog.Warn("router: update interaction question", "error", err)
		}
		out <- a2a.StreamEvent{
			Type: "artifact",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf("📄 **Re-extracted** _(model: %s)_:\n\n%s\n\n---\n", modelName, extracted))},
			},
		}
		r.emitAlternativesGated(out, config.CategoryVision, modelName, interaction.ID, "extract_proceed",
			"Does this extraction look right? Proceed, or try yet another model.")

	case config.CategoryHintGenerator:
		// Regenerate the CURRENT hint (no increment) with an alternative model.
		level := interaction.HintLevel
		if level < 1 {
			level = 1
		}
		emitTransition(out, "router", "hint", task.ID,
			fmt.Sprintf("retry hint with alternate model | model: %s | hint #: %d", modelName, level))
		r.dispatchHintWithModel(ctx, task, interaction, level, modelName, out)

	case config.CategoryRouter:
		// Re-evaluate the LAST attempt with an alternative evaluator model.
		r.reEvaluateLastAttempt(ctx, task, convID, userID, interaction, modelName, out)

	default:
		// Solver categories (Level1/2/3) or empty → re-solve.
		emitTransition(out, "router", "solver", task.ID,
			fmt.Sprintf("retry with alternate model | model: %s | purpose: solver retry", modelName))
		completed := r.dispatchSolverWithModel(ctx, task, interaction, modelName, out)
		if completed {
			if err := r.store.CloseInteraction(ctx, interaction.ID, models.InteractionSolved, models.ExitNeededSolution); err != nil {
				slog.Warn("router: close interaction", "error", err)
			}
			r.clearHints(interaction.ID)
		} else {
			slog.Info("router: retry_model solver still needs model picker")
		}
	}
}

// extractImageWithModel re-runs image extraction using a different vision
// model, loading the raw bytes by image_id from the DB. Used for Vision
// retry_model flows.
func (r *Router) extractImageWithModel(ctx context.Context, imageID, modelID string, out chan<- a2a.StreamEvent) (string, error) {
	img, err := r.store.GetImage(ctx, imageID)
	if err != nil || img == nil {
		return "", fmt.Errorf("failed to load image %s: %v", imageID, err)
	}
	// Build a data URI for the vision LLM
	dataURI := fmt.Sprintf("data:%s;base64,%s", img.MimeType, base64.StdEncoding.EncodeToString(img.Data))

	// Create a one-shot extraction agent with the alternative vision model
	altClient := llm.NewClient(r.cfg.LLMAPIKey, modelID, r.cfg.LLMBaseURL, r.cfg.LLMUserID)
	altAgent := NewImageExtractionAgent(altClient, r.store)

	out <- a2a.StreamEvent{
		Type: "artifact",
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf("🔍 **Re-analysing image…** _(model: %s)_\n\n", modelID))},
		},
	}

	extractStart := time.Now()
	extractTask := &a2a.Task{
		ID:      uuid.New().String(),
		AgentID: "image_extraction",
		State:   a2a.TaskStateSubmitted,
		Input: a2a.Message{
			Role:  "user",
			Parts: []a2a.Part{a2a.ImagePart(dataURI)},
		},
		Metadata:  map[string]string{"image_id": imageID},
		CreatedAt: time.Now().UTC(),
	}
	result, err := altAgent.Handle(ctx, extractTask)
	slog.Info("router: re-extractImage done",
		"model", modelID,
		"elapsed_ms", time.Since(extractStart).Milliseconds())
	if err != nil {
		return "", fmt.Errorf("re-extraction failed: %v", err)
	}
	if result.Output != nil {
		for _, p := range result.Output.Parts {
			if p.Type == "text" {
				return p.Text, nil
			}
		}
	}
	return "", fmt.Errorf("re-extraction returned no text")
}

// reEvaluateLastAttempt re-runs the attempt evaluator with an alternative
// model on the most recent attempt for this interaction. The new result is
// saved as a new row; the "final" displayed score is the best across all
// attempts for this interaction.
func (r *Router) reEvaluateLastAttempt(ctx context.Context, task *a2a.Task, convID, userID string, interaction *models.Interaction, modelID string, out chan<- a2a.StreamEvent) {
	attempts, err := r.store.GetAttemptsByInteraction(ctx, interaction.ID)
	if err != nil || len(attempts) == 0 {
		out <- a2a.StreamEvent{Type: "error", Error: "no attempt to re-evaluate"}
		return
	}
	last := attempts[len(attempts)-1]

	altClient := llm.NewClient(r.cfg.LLMAPIKey, modelID, r.cfg.LLMBaseURL, r.cfg.LLMUserID)
	altEval := NewAttemptEvaluatorAgent(altClient, nil, r.store)

	emitTransition(out, "router", "attempt_evaluator", task.ID,
		fmt.Sprintf("re-evaluating attempt with alternate model | model: %s", modelID))

	evalStart := time.Now()
	result, evalErr := altEval.Evaluate(ctx, interaction.QuestionText, last.StudentMessage, last.HintIndex)
	slog.Info("router: re-evaluation",
		"model", modelID,
		"elapsed_ms", time.Since(evalStart).Milliseconds())
	if evalErr != nil {
		slog.Warn("router: re-evaluation failed, offering retry", "error", evalErr, "model", modelID)
		r.offerFailureRetry(out, config.CategoryRouter, modelID, interaction.ID,
			"Re-evaluation with "+modelID+" failed: "+evalErr.Error())
		return
	}

	// Save as a new attempt row so history is preserved
	retry := &models.StudentAttempt{
		InteractionID:  interaction.ID,
		UserID:         userID,
		HintIndex:      last.HintIndex,
		StudentMessage: last.StudentMessage,
		EvaluatorJSON:  *result,
	}
	if err := r.store.SaveStudentAttempt(ctx, retry); err != nil {
		slog.Warn("router: save re-evaluated attempt", "error", err)
	}

	// Compute best score across all attempts (including this new one)
	bestScore := result.Score
	bestCorrect := result.Correct
	for _, a := range attempts {
		if a.EvaluatorJSON.Score > bestScore {
			bestScore = a.EvaluatorJSON.Score
			bestCorrect = a.EvaluatorJSON.Correct
		}
	}

	r.emitAttemptFeedback(out, result, bestScore, bestCorrect, modelID)
	r.emitAlternatives(out, config.CategoryRouter, modelID, interaction.ID, true,
		fmt.Sprintf("Evaluation complete (best score so far: %.0f%%). Try another evaluator?", bestScore*100))

	// Keep the interaction in waiting_for_attempt so the student can still submit more work
	out <- a2a.StreamEvent{
		Type:  "status",
		State: a2a.TaskStateInputNeeded,
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf(`{"hint_level":%d,"attempt_evaluated":true,"score":%.2f}`, interaction.HintLevel, bestScore))},
		},
	}
}

// emitAttemptFeedback renders a student-facing feedback artifact showing the
// current evaluation plus the best score seen so far for this interaction.
func (r *Router) emitAttemptFeedback(out chan<- a2a.StreamEvent, result *models.EvaluatorResult, bestScore float64, bestCorrect bool, modelID string) {
	var fb strings.Builder
	if result.Correct {
		fb.WriteString("✅ **This evaluator marked your attempt as correct.**\n\n")
	} else {
		fb.WriteString(fmt.Sprintf("📝 **Score from %s: %.0f%%**\n\n", modelID, result.Score*100))
	}
	bestSuffix := ""
	if bestCorrect {
		bestSuffix = " — correct!"
	}
	fb.WriteString(fmt.Sprintf("🏆 **Best score across all attempts: %.0f%%**%s\n\n", bestScore*100, bestSuffix))
	if len(result.Strengths) > 0 {
		fb.WriteString("**Strengths:**\n")
		for _, s := range result.Strengths {
			fb.WriteString(fmt.Sprintf("- %s\n", s))
		}
		fb.WriteString("\n")
	}
	if len(result.Errors) > 0 {
		fb.WriteString("**Issues found:**\n")
		for _, e := range result.Errors {
			fb.WriteString(fmt.Sprintf("- %s\n", e))
		}
		fb.WriteString("\n")
	}
	if len(result.MissingSteps) > 0 {
		fb.WriteString("**Missing steps:**\n")
		for _, m := range result.MissingSteps {
			fb.WriteString(fmt.Sprintf("- %s\n", m))
		}
		fb.WriteString("\n")
	}
	if result.NextGuidance != "" {
		fb.WriteString(fmt.Sprintf("💡 %s\n", result.NextGuidance))
	}
	out <- a2a.StreamEvent{
		Type: "artifact",
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(fb.String())},
		},
	}
}

func (r *Router) extractImage(ctx context.Context, task *a2a.Task, out chan<- a2a.StreamEvent) (string, error) {
	extractStart := time.Now()
	extractor, ok := r.subAgents["image_extraction"]
	if !ok {
		return "", fmt.Errorf("image_extraction agent not registered")
	}

	extractTaskID := uuid.New().String()
	visionModel := config.DefaultVisionModel()
	emitTransition(out, "router", "image_extraction", extractTaskID,
		fmt.Sprintf("extracting text from image | model: %s", visionModel))

	// Emit a progress artifact so the user knows extraction is underway
	out <- a2a.StreamEvent{
		Type: "artifact",
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf("🔍 **Analysing image…** _(model: %s)_\nThis may take 15-30 seconds for complex diagrams.\n\n", visionModel))},
		},
	}

	slog.Info("router: extractImage dispatching", "task", extractTaskID)

	extractTask := &a2a.Task{
		ID:        extractTaskID,
		AgentID:   "image_extraction",
		State:     a2a.TaskStateSubmitted,
		Input:     task.Input,
		Metadata:  task.Metadata,
		CreatedAt: time.Now().UTC(),
	}

	result, err := extractor.Handle(ctx, extractTask)
	extractDuration := time.Since(extractStart)
	if err != nil {
		slog.Error("router: extractImage failed",
			"error", err,
			"elapsed_ms", extractDuration.Milliseconds())
		return "", fmt.Errorf("image extraction failed: %v", err)
	}

	slog.Info("router: extractImage done",
		"elapsed_ms", extractDuration.Milliseconds(),
		"elapsed_s", fmt.Sprintf("%.1f", extractDuration.Seconds()))
	emitTransition(out, "image_extraction", "router", extractTaskID,
		fmt.Sprintf("extraction complete (%.1fs) | model: %s", extractDuration.Seconds(), visionModel))

	if result.Output != nil {
		for _, p := range result.Output.Parts {
			if p.Type == "text" {
				return p.Text, nil
			}
		}
	}
	return "", fmt.Errorf("image extraction returned no text")
}

// ── Helpers ──────────────────────────────────────────────────────────

// emitTransition sends a "transition" event to the SSE stream.
func emitTransition(out chan<- a2a.StreamEvent, from, to, taskID, reason string) {
	ev := a2a.StreamEvent{
		Type:      "transition",
		FromAgent: from,
		ToAgent:   to,
		TaskID:    taskID,
		Reason:    reason,
	}
	slog.Debug("a2a transition", "from", from, "to", to, "task", taskID, "reason", reason)
	out <- ev
}

// hasImageInput checks whether the task input contains an image part.
func hasImageInput(msg a2a.Message) bool {
	for _, p := range msg.Parts {
		if p.Type == "image" && p.ImageURL != "" {
			return true
		}
	}
	return false
}

// extractText gathers all text parts from a message.
func extractText(msg a2a.Message) string {
	var parts []string
	for _, p := range msg.Parts {
		if p.Type == "text" && p.Text != "" {
			parts = append(parts, p.Text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += "\n" + parts[i]
	}
	return result
}
