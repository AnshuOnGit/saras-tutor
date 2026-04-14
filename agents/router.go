package agents

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
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
}

// NewRouter creates the deterministic router.
func NewRouter(store *db.Store, cfg *config.Config, llmClient *llm.Client, subAgents map[string]a2a.Agent) *Router {
	return &Router{
		store:     store,
		cfg:       cfg,
		llmClient: llmClient,
		subAgents: subAgents,
	}
}

// resolveTargetModel determines which LLM to use for a given interaction.
// If the task metadata contains an explicit "model" (from retry_model), use that.
// Otherwise, call SelectModelForTask based on the interaction's difficulty and subject.
func (r *Router) resolveTargetModel(ctx context.Context, task *a2a.Task, interaction *models.Interaction) string {
	// Explicit model from frontend (retry_model action)
	if task.Metadata != nil && task.Metadata["model"] != "" {
		return task.Metadata["model"]
	}

	// Auto-select based on difficulty + subject
	subjectName := ""
	if interaction != nil {
		subjectName = r.store.LookupSubjectName(ctx, interaction.SubjectID)
	}
	difficulty := 0
	if interaction != nil {
		difficulty = interaction.Difficulty
	}

	selected := config.SelectModelForTask(difficulty, subjectName, false)
	slog.Info("router: auto-selected model", "model", selected, "difficulty", difficulty, "subject", subjectName)
	return selected
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
	case "confirm_extraction":
		r.handleConfirmExtraction(ctx, task, convID, userID, out)
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

	// 2. Image extraction if needed
	if hasImageInput(task.Input) {
		extracted, err := r.extractImage(ctx, task, out)
		if err != nil {
			out <- a2a.StreamEvent{Type: "error", Error: err.Error()}
			return
		}
		questionText = extracted

		// Show extracted content as artifact
		out <- a2a.StreamEvent{
			Type: "artifact",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.TextPart("**Extracted from image:**\n\n" + questionText)},
			},
		}

		// ── Pause for student confirmation ──
		// Create a preliminary interaction to hold the image_id so
		// re-extraction via retry_model has access to the image.
		imageID := ""
		if task.Metadata != nil {
			imageID = task.Metadata["image_id"]
		}
		interaction := &models.Interaction{
			ID:             uuid.New().String(),
			ConversationID: convID,
			QuestionText:   questionText,
			ImageID:        imageID,
			State:          models.InteractionNew,
			CreatedAt:      time.Now().UTC(),
		}
		if err := r.store.CreateInteraction(ctx, interaction); err != nil {
			slog.Warn("router: create preliminary interaction", "error", err)
		}

		extractedJSON, _ := json.Marshal(questionText)
		out <- a2a.StreamEvent{
			Type:  "status",
			State: a2a.TaskStateInputNeeded,
			Message: &a2a.Message{
				Role: "agent",
				Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf(
					`{"extraction_confirm":true,"extracted_text":%s}`,
					extractedJSON))},
			},
		}
		return // wait for confirm_extraction or retry_model from frontend
	}

	if questionText == "" {
		out <- a2a.StreamEvent{Type: "error", Error: "no question text provided"}
		return
	}

	// 3. Validate the question is about an allowed subject (math/physics/chemistry/biology)
	vr, _ := ValidateQuestion(ctx, r.llmClient, questionText)
	if vr != nil && !vr.Valid {
		slog.Info("router: question rejected", "reason", vr.Reason)
		out <- a2a.StreamEvent{
			Type: "artifact",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.TextPart(InvalidQuestionMessage(vr))},
			},
		}
		out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
		return
	}

	// 4. Parse the question into structured format (topics, difficulty, variables)
	parsedQ, _ := ParseQuestion(ctx, r.llmClient, questionText)
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

	// 5. Resolve taxonomy IDs
	subjectID := r.store.LookupSubjectID(ctx, subjectName)
	topicIDs := r.store.LookupTopicIDs(ctx, topicNames)

	// 6. Create new interaction (store image_id if image was uploaded)
	imageID := ""
	if task.Metadata != nil {
		imageID = task.Metadata["image_id"]
	}
	interaction := &models.Interaction{
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
		// Continue anyway — hints still work without DB tracking
	}

	// 7. Update student profile
	if userID != "" {
		if _, err := r.store.GetOrCreateProfile(ctx, userID); err != nil {
			slog.Warn("router: get/create profile", "error", err)
		}
		if err := r.store.IncrementProfileQuestions(ctx, userID); err != nil {
			slog.Warn("router: increment questions", "error", err)
		}
	}

	// 8. Auto-select the best model for this question and store in metadata
	targetModel := r.resolveTargetModel(ctx, task, interaction)
	if task.Metadata == nil {
		task.Metadata = map[string]string{}
	}
	task.Metadata["target_model"] = targetModel

	// 9. Dispatch Hint Level 1 (using the auto-selected model)
	r.dispatchHintWithModel(ctx, task, interaction, 1, targetModel, out)
}

// handleConfirmExtraction is called when the student confirms that the image
// extraction looks correct. It continues the flow: validate → parse → hint.
func (r *Router) handleConfirmExtraction(ctx context.Context, task *a2a.Task, convID, userID string, out chan<- a2a.StreamEvent) {
	interaction, err := r.store.GetActiveInteraction(ctx, convID)
	if err != nil || interaction == nil {
		slog.Info("router: no active interaction for confirm_extraction, treating as new question")
		r.handleNewQuestion(ctx, task, convID, userID, out)
		return
	}

	questionText := interaction.QuestionText
	if questionText == "" {
		out <- a2a.StreamEvent{Type: "error", Error: "no extracted text found for confirmation"}
		return
	}

	slog.Info("router: extraction confirmed", "interaction", interaction.ID, "text_len", len(questionText))

	// 1. Validate the question
	vr, _ := ValidateQuestion(ctx, r.llmClient, questionText)
	if vr != nil && !vr.Valid {
		slog.Info("router: question rejected", "reason", vr.Reason)
		out <- a2a.StreamEvent{
			Type: "artifact",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.TextPart(InvalidQuestionMessage(vr))},
			},
		}
		out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
		return
	}

	// 2. Parse the question
	parsedQ, _ := ParseQuestion(ctx, r.llmClient, questionText)
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

	// 3. Resolve taxonomy IDs and update the interaction
	subjectID := r.store.LookupSubjectID(ctx, subjectName)
	topicIDs := r.store.LookupTopicIDs(ctx, topicNames)

	interaction.SubjectID = subjectID
	interaction.TopicIDs = topicIDs
	interaction.Difficulty = difficulty
	interaction.ProblemText = problemText
	if err := r.store.UpdateInteractionMetadata(ctx, interaction); err != nil {
		slog.Warn("router: update interaction metadata", "error", err)
	}

	// 4. Update student profile
	if userID != "" {
		if _, err := r.store.GetOrCreateProfile(ctx, userID); err != nil {
			slog.Warn("router: get/create profile", "error", err)
		}
		if err := r.store.IncrementProfileQuestions(ctx, userID); err != nil {
			slog.Warn("router: increment questions", "error", err)
		}
	}

	// 5. Auto-select the best model and dispatch hint
	targetModel := r.resolveTargetModel(ctx, task, interaction)
	if task.Metadata == nil {
		task.Metadata = map[string]string{}
	}
	task.Metadata["target_model"] = targetModel

	r.dispatchHintWithModel(ctx, task, interaction, 1, targetModel, out)
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
	if nextLevel > 3 {
		// Already at hint_3 — give full solution
		slog.Info("router: escalating to full solution", "hint_level", interaction.HintLevel)
		r.handleShowSolution(ctx, task, convID, userID, out)
		return
	}

	targetModel := r.resolveTargetModel(ctx, task, interaction)
	r.dispatchHintWithModel(ctx, task, interaction, nextLevel, targetModel, out)
}

func (r *Router) handleShowSolution(ctx context.Context, task *a2a.Task, convID, userID string, out chan<- a2a.StreamEvent) {
	interaction, err := r.store.GetActiveInteraction(ctx, convID)
	if err != nil || interaction == nil {
		slog.Info("router: no active interaction for show_solution, treating as new question")
		r.handleNewQuestion(ctx, task, convID, userID, out)
		return
	}

	// Auto-select the best model for this interaction
	targetModel := r.resolveTargetModel(ctx, task, interaction)

	slog.Info("router: dispatching solver", "interaction", interaction.ID, "model", targetModel)
	emitTransition(out, "router", "solver", task.ID, "student requested full solution")

	// Dispatch solver with the auto-selected model
	completed := r.dispatchSolverWithModel(ctx, task, interaction, targetModel, out)

	if completed {
		// Mark interaction as solved only if fully completed (not awaiting model picker)
		if err := r.store.CloseInteraction(ctx, interaction.ID, models.InteractionSolved, models.ExitNeededSolution); err != nil {
			slog.Warn("router: close interaction", "error", err)
		}

		// Update long-term memory + topic mastery (selfSolved=false — needed full solution)
		if userID != "" {
			if err := r.store.RecordInteractionOutcome(ctx, userID, interaction, false); err != nil {
				slog.Warn("router: record outcome", "error", err)
			}
		}
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

	// Update long-term memory + topic mastery (selfSolved=true — student closed without needing solution)
	if userID != "" {
		if err := r.store.RecordInteractionOutcome(ctx, userID, interaction, true); err != nil {
			slog.Warn("router: record outcome", "error", err)
		}
	}

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
			out <- a2a.StreamEvent{Type: "error", Error: "could not read your image: " + err.Error()}
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

	emitTransition(out, "router", "attempt_evaluator", task.ID,
		fmt.Sprintf("evaluating student attempt at hint level %d", hintLevel))

	result, evalErr := evalAgent.Evaluate(ctx, interaction.QuestionText, studentText, hintLevel)
	if evalErr != nil {
		slog.Error("router: evaluator error", "error", evalErr)
		out <- a2a.StreamEvent{Type: "error", Error: "failed to evaluate attempt"}
		return
	}

	emitTransition(out, "attempt_evaluator", "router", task.ID, fmt.Sprintf("evaluation complete — score %.0f%%", result.Score*100))

	// Persist the attempt — for image-only, record a placeholder in student_message
	attemptMessage := studentText
	if attemptMessage == "" {
		attemptMessage = "[image submission]"
	}
	attempt := &models.StudentAttempt{
		InteractionID:  interaction.ID,
		UserID:         userID,
		HintLevel:      hintLevel,
		Score:          result.Score,
		Correct:        result.Correct,
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

	// Build student-facing feedback
	var fb strings.Builder
	if result.Correct {
		fb.WriteString("✅ **Great work!** Your attempt is correct.\n\n")
	} else {
		fb.WriteString(fmt.Sprintf("📝 **Score: %.0f%%**\n\n", result.Score*100))
	}
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

	// Decide next state based on result
	if result.Correct && result.Score >= 0.8 {
		// Student nailed it — close the interaction
		exitReason := models.ExitReasonForState(interaction.State)
		if err := r.store.CloseInteraction(ctx, interaction.ID, models.InteractionClosed, exitReason); err != nil {
			slog.Warn("router: close interaction after correct attempt", "error", err)
		}
		if userID != "" {
			if err := r.store.RecordInteractionOutcome(ctx, userID, interaction, true); err != nil {
				slog.Warn("router: record outcome", "error", err)
			}
		}
		out <- a2a.StreamEvent{
			Type: "artifact",
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.TextPart("\n---\n🎯 **Well done!** You solved it. Ask another question whenever you're ready.")},
			},
		}
		out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
		slog.Info("router: student attempt correct", "interaction", interaction.ID)
	} else {
		// Not fully correct — stay in waiting_for_attempt, offer options
		if err := r.store.UpdateInteraction(ctx, interaction.ID, models.InteractionWaitingForAttempt, hintLevel); err != nil {
			slog.Warn("router: update interaction to waiting_for_attempt", "error", err)
		}
		out <- a2a.StreamEvent{
			Type:  "status",
			State: a2a.TaskStateInputNeeded,
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf(`{"hint_level":%d,"attempt_evaluated":true,"score":%.2f}`, hintLevel, result.Score))},
			},
		}
		slog.Info("router: attempt scored, waiting", "score", result.Score, "interaction", interaction.ID)
	}
}

// ── Dispatchers ──────────────────────────────────────────────────────

func (r *Router) dispatchHint(ctx context.Context, task *a2a.Task, interaction *models.Interaction, level int, out chan<- a2a.StreamEvent) {
	r.dispatchHintWithModel(ctx, task, interaction, level, "", out)
}

// dispatchHintWithModel dispatches a hint, optionally overriding the model.
func (r *Router) dispatchHintWithModel(ctx context.Context, task *a2a.Task, interaction *models.Interaction, level int, modelOverride string, out chan<- a2a.StreamEvent) {
	hintAgent, ok := r.subAgents["hint"]
	if !ok {
		out <- a2a.StreamEvent{Type: "error", Error: "hint agent not registered"}
		return
	}

	// If modelOverride is set, create a temporary hint agent with that model
	if modelOverride != "" {
		altClient := llm.NewClient(r.cfg.LLMAPIKey, modelOverride, r.cfg.LLMBaseURL, r.cfg.LLMUserID)
		hintAgent = NewHintAgent(altClient, r.store)
		slog.Info("router: hint using override model", "model", modelOverride)
	}

	hintTaskID := uuid.New().String()
	modelLabel := config.DefaultSolverModel()
	if modelOverride != "" {
		modelLabel = modelOverride
	}
	emitTransition(out, "router", "hint", hintTaskID, fmt.Sprintf("giving hint level %d (model: %s)", level, modelLabel))

	// All hints use extracted text only — images are consumed solely by
	// the image_extraction agent to avoid redundant vision-model costs.
	inputParts := []a2a.Part{a2a.TextPart(interaction.QuestionText)}
	slog.Info("router: dispatching hint", "level", level)

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

	// Stream hint to client
	hintAgent.HandleStream(ctx, hintTask, out)
	emitTransition(out, "hint", "router", hintTaskID, fmt.Sprintf("hint level %d delivered", level))

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
		fmt.Sprintf("sending question to solver (model: %s)", modelLabel))

	// All downstream agents (solver, verifier) work with extracted text only.
	// Images are consumed solely by the image_extraction agent.

	// ── Pass 1: text-only solve ──
	inputParts := []a2a.Part{a2a.TextPart(interaction.QuestionText)}
	solveTask := &a2a.Task{
		ID:        solveTaskID,
		AgentID:   "solver",
		State:     a2a.TaskStateSubmitted,
		Input:     a2a.Message{Role: "user", Parts: inputParts},
		Metadata:  task.Metadata,
		CreatedAt: time.Now().UTC(),
	}

	fullSolution := r.streamAndCapture(ctx, solver, solveTask, out)
	emitTransition(out, "solver", "router", solveTaskID, "solver pass 1 finished")

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

	emitTransition(out, "router", "verifier", solveTaskID, "verifying solution quality")
	vResult, vComp, vErr := va.Verify(ctx, interaction.QuestionText, fullSolution, "")
	if vErr != nil {
		slog.Warn("router: verifier error, accepting solution", "error", vErr)
		out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
		return true
	}
	emitVerifierMetadata(out, vResult, vComp, 1)

	if vResult.Score >= MinVerifierScore {
		slog.Info("router: verifier pass 1 accepted", "score", vResult.Score, "threshold", MinVerifierScore)
		out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
		return true
	}

	slog.Info("router: verifier pass 1 low, offering model picker", "score", vResult.Score, "threshold", MinVerifierScore)

	// No image retry — images are only used during extraction.
	// Offer the user a model picker to try a different LLM.
	r.emitModelPicker(out, vResult, interaction)
	return false
}

// streamAndCapture streams a solver task to the output channel while also
// capturing all artifact text into a single string for verification.
func (r *Router) streamAndCapture(ctx context.Context, solver a2a.Agent, task *a2a.Task, out chan<- a2a.StreamEvent) string {
	// Use an intermediate channel to intercept events
	intermediate := make(chan a2a.StreamEvent, 128)
	go func() {
		solver.HandleStream(ctx, task, intermediate)
		close(intermediate)
	}()

	var captured strings.Builder
	for ev := range intermediate {
		// Forward everything to the real output
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
	return captured.String()
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

// emitModelPicker sends an input-needed event with the available model options.
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

	// Build JSON payload for model picker (include difficulty & subject for /experts)
	difficulty := interaction.Difficulty
	subject := ""
	if interaction.SubjectID != nil {
		subject = r.store.LookupSubjectName(context.Background(), interaction.SubjectID)
	}
	out <- a2a.StreamEvent{
		Type:  "status",
		State: a2a.TaskStateInputNeeded,
		Message: &a2a.Message{
			Role: "agent",
			Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf(
				`{"model_picker":true,"difficulty":%d,"subject":"%s","interaction_id":"%s"}`,
				difficulty, subject, interaction.ID))},
		},
	}
}

// handleRetryModel is called when the user picks an alternative model after
// the verifier rejected the initial solution.
//
// Special case: if the selected model is a Vision model, we re-run image
// extraction with the original image, update the interaction's question_text,
// and then solve with the freshly-extracted text.
func (r *Router) handleRetryModel(ctx context.Context, task *a2a.Task, convID, userID string, out chan<- a2a.StreamEvent) {
	interaction, err := r.store.GetActiveInteraction(ctx, convID)
	if err != nil || interaction == nil {
		slog.Info("router: no active interaction for retry_model, treating as new question")
		r.handleNewQuestion(ctx, task, convID, userID, out)
		return
	}

	modelName := ""
	if task.Metadata != nil {
		modelName = task.Metadata["model"]
	}
	if modelName == "" {
		out <- a2a.StreamEvent{Type: "error", Error: "retry_model requires a 'model' in metadata"}
		return
	}

	slog.Info("router: retry_model", "model", modelName, "interaction", interaction.ID)

	// ── Vision model branch: re-extract then solve ──
	if isVisionModel(modelName) {
		r.handleRetryVision(ctx, task, interaction, modelName, out)
		return
	}

	// ── Standard branch: re-solve with a different text model ──
	emitTransition(out, "router", "solver", task.ID,
		fmt.Sprintf("student requested retry with model: %s", modelName))

	completed := r.dispatchSolverWithModel(ctx, task, interaction, modelName, out)

	if completed {
		if err := r.store.CloseInteraction(ctx, interaction.ID, models.InteractionSolved, models.ExitNeededSolution); err != nil {
			slog.Warn("router: close interaction", "error", err)
		}
	} else {
		slog.Info("router: retry_model solver still needs model picker")
	}
}

// handleRetryVision re-runs image extraction with a vision model, updates
// the interaction's question_text, then dispatches the solver.
func (r *Router) handleRetryVision(ctx context.Context, task *a2a.Task, interaction *models.Interaction, visionModel string, out chan<- a2a.StreamEvent) {
	// 1. Load the original image from the DB
	if interaction.ImageID == "" {
		out <- a2a.StreamEvent{Type: "error", Error: "no image stored for this question — cannot re-extract"}
		return
	}

	img, err := r.store.GetImage(ctx, interaction.ImageID)
	if err != nil {
		slog.Error("router: failed to load image for re-extraction", "image_id", interaction.ImageID, "error", err)
		out <- a2a.StreamEvent{Type: "error", Error: "failed to load original image"}
		return
	}

	// 2. Build a data URI from the stored image bytes
	dataURI := fmt.Sprintf("data:%s;base64,%s",
		img.MimeType,
		base64Encode(img.Data),
	)

	emitTransition(out, "router", "image_extraction", task.ID,
		fmt.Sprintf("re-extracting image with vision model: %s", visionModel))

	// 3. Build a temporary extraction task with the image
	extractor, ok := r.subAgents["image_extraction"]
	if !ok {
		out <- a2a.StreamEvent{Type: "error", Error: "image_extraction agent not registered"}
		return
	}

	// Create a temporary vision-model extractor if different from default
	altVisionClient := llm.NewClient(r.cfg.LLMAPIKey, visionModel, r.cfg.LLMBaseURL, r.cfg.LLMUserID)
	altVisionClient.MaxTokens = 4096
	extractor = NewImageExtractionAgent(altVisionClient, r.store)

	extractTask := &a2a.Task{
		ID:      uuid.New().String(),
		AgentID: "image_extraction",
		State:   a2a.TaskStateSubmitted,
		Input: a2a.Message{
			Role:  "user",
			Parts: []a2a.Part{a2a.ImagePart(dataURI)},
		},
		Metadata:  task.Metadata,
		CreatedAt: time.Now().UTC(),
	}

	result, err := extractor.Handle(ctx, extractTask)
	if err != nil {
		slog.Error("router: vision re-extraction failed", "error", err)
		out <- a2a.StreamEvent{Type: "error", Error: "image re-extraction failed: " + err.Error()}
		return
	}

	// 4. Extract the new text
	newText := ""
	if result.Output != nil {
		for _, p := range result.Output.Parts {
			if p.Type == "text" {
				newText = p.Text
				break
			}
		}
	}
	if newText == "" {
		out <- a2a.StreamEvent{Type: "error", Error: "vision re-extraction returned no text"}
		return
	}

	emitTransition(out, "image_extraction", "router", task.ID, "re-extraction complete")

	// 5. Show the new extraction to the student
	out <- a2a.StreamEvent{
		Type: "artifact",
		Message: &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart("**Re-extracted from image (" + visionModel + "):**\n\n" + newText)},
		},
	}

	// 6. Update the interaction's question_text in the DB
	interaction.QuestionText = newText
	if err := r.store.UpdateInteractionQuestionText(ctx, interaction.ID, newText); err != nil {
		slog.Warn("router: failed to update question_text", "error", err)
	}
	slog.Info("router: question_text updated from vision re-extraction",
		"interaction", interaction.ID, "model", visionModel, "text_len", len(newText))

	// 7. Pause for student confirmation again (they may want to re-extract once more)
	reExtractedJSON, _ := json.Marshal(newText)
	out <- a2a.StreamEvent{
		Type:  "status",
		State: a2a.TaskStateInputNeeded,
		Message: &a2a.Message{
			Role: "agent",
			Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf(
				`{"extraction_confirm":true,"extracted_text":%s}`,
				reExtractedJSON))},
		},
	}
	// Student will send confirm_extraction to continue or retry_model to re-extract again.
}

// isVisionModel returns true if the model ID is a vision-capable VLM
// from the model registry.
func isVisionModel(modelID string) bool {
	m := config.GetModelByID(modelID)
	return m != nil && m.Category == config.CategoryVision
}

// base64Encode is a small helper wrapping encoding/base64.
func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// ── Image extraction ─────────────────────────────────────────────────

func (r *Router) extractImage(ctx context.Context, task *a2a.Task, out chan<- a2a.StreamEvent) (string, error) {
	extractor, ok := r.subAgents["image_extraction"]
	if !ok {
		return "", fmt.Errorf("image_extraction agent not registered")
	}

	extractTaskID := uuid.New().String()
	emitTransition(out, "router", "image_extraction", extractTaskID, "input contains image — extracting text")

	extractTask := &a2a.Task{
		ID:        extractTaskID,
		AgentID:   "image_extraction",
		State:     a2a.TaskStateSubmitted,
		Input:     task.Input,
		Metadata:  task.Metadata,
		CreatedAt: time.Now().UTC(),
	}

	result, err := extractor.Handle(ctx, extractTask)
	if err != nil {
		return "", fmt.Errorf("image extraction failed: %v", err)
	}

	emitTransition(out, "image_extraction", "router", extractTaskID, "extraction complete")

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
