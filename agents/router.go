package agents

import (
	"context"
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
	store       *db.Store
	cfg         *config.Config
	llmClient   *llm.Client // lightweight client for utility calls (validation, etc.)
	subAgents   map[string]a2a.Agent
	retryModels []string // alternative models for user to pick
}

// NewRouter creates the deterministic router.
func NewRouter(store *db.Store, cfg *config.Config, llmClient *llm.Client, subAgents map[string]a2a.Agent) *Router {
	return &Router{
		store:       store,
		cfg:         cfg,
		llmClient:   llmClient,
		subAgents:   subAgents,
		retryModels: cfg.RetryModels,
	}
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
		if err := r.store.AppendProfileQuestionStat(ctx, userID, convID, chapterName, topicNames, difficulty); err != nil {
			slog.Warn("router: append aggr_stats", "error", err)
		}
	}

	// 8. Dispatch Hint Level 1
	r.dispatchHint(ctx, task, interaction, 1, out)
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

	r.dispatchHint(ctx, task, interaction, nextLevel, out)
}

func (r *Router) handleShowSolution(ctx context.Context, task *a2a.Task, convID, userID string, out chan<- a2a.StreamEvent) {
	interaction, err := r.store.GetActiveInteraction(ctx, convID)
	if err != nil || interaction == nil {
		slog.Info("router: no active interaction for show_solution, treating as new question")
		r.handleNewQuestion(ctx, task, convID, userID, out)
		return
	}

	slog.Info("router: dispatching solver", "interaction", interaction.ID)
	emitTransition(out, "router", "solver", task.ID, "student requested full solution")

	// Dispatch solver with the original question (+ image if verifier low)
	completed := r.dispatchSolver(ctx, task, interaction, out)

	if completed {
		// Mark interaction as solved only if fully completed (not awaiting model picker)
		if err := r.store.CloseInteraction(ctx, interaction.ID, models.InteractionSolved, models.ExitNeededSolution); err != nil {
			slog.Warn("router: close interaction", "error", err)
		}

		// Update long-term memory
		if userID != "" {
			if err := r.store.UpdateProfileQuestionOutcome(ctx, userID, convID, interaction.HintLevel, false); err != nil {
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

	// Update long-term memory
	if userID != "" {
		if err := r.store.UpdateProfileQuestionOutcome(ctx, userID, convID, interaction.HintLevel, true); err != nil {
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
			if err := r.store.UpdateProfileQuestionOutcome(ctx, userID, convID, interaction.HintLevel, true); err != nil {
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
	hintAgent, ok := r.subAgents["hint"]
	if !ok {
		out <- a2a.StreamEvent{Type: "error", Error: "hint agent not registered"}
		return
	}

	hintTaskID := uuid.New().String()
	emitTransition(out, "router", "hint", hintTaskID, fmt.Sprintf("giving hint level %d", level))

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
	modelLabel := r.cfg.SolverModel
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

	// Build JSON payload for model picker
	modelsJSON := `["` + strings.Join(r.retryModels, `","`) + `"]`
	out <- a2a.StreamEvent{
		Type:  "status",
		State: a2a.TaskStateInputNeeded,
		Message: &a2a.Message{
			Role: "agent",
			Parts: []a2a.Part{a2a.TextPart(fmt.Sprintf(
				`{"model_picker":true,"models":%s,"interaction_id":"%s"}`,
				modelsJSON, interaction.ID))},
		},
	}
}

// handleRetryModel is called when the user picks an alternative model after
// the verifier rejected the initial solution.
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
	emitTransition(out, "router", "solver", task.ID,
		fmt.Sprintf("student requested retry with model: %s", modelName))

	// Dispatch solver with the selected model
	completed := r.dispatchSolverWithModel(ctx, task, interaction, modelName, out)

	if completed {
		if err := r.store.CloseInteraction(ctx, interaction.ID, models.InteractionSolved, models.ExitNeededSolution); err != nil {
			slog.Warn("router: close interaction", "error", err)
		}
	} else {
		slog.Info("router: retry_model solver still needs model picker")
	}
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
