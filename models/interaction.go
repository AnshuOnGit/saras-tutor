package models

import "time"

// ── Interaction State Machine ────────────────────────────────────────────
//
//  new → hint_1 ──→ hint_2 ──→ hint_3 ──→ solved  (student asked for full solution)
//          │  ↑       │  ↑       │  ↑
//          ▼  │       ▼  │       ▼  │
//      waiting_for_attempt (submit_attempt → evaluate → back to hint_N or next)
//          │         │         │
//          ▼         ▼         ▼
//        closed    closed    closed   (student clicked "Got it, I'll try")
//
// "solved" is ALSO terminal — set automatically when the solver finishes.
// "waiting_for_attempt" is transient — the student has been invited to try.

// InteractionState represents the current position in the tutoring flow.
type InteractionState string

const (
	InteractionNew               InteractionState = "new"
	InteractionExtracting        InteractionState = "extracting" // awaiting user confirmation of extracted text
	InteractionHint1             InteractionState = "hint_1"
	InteractionHint2             InteractionState = "hint_2"
	InteractionHint3             InteractionState = "hint_3"
	InteractionWaitingForAttempt InteractionState = "waiting_for_attempt"
	InteractionSolved            InteractionState = "solved" // full solution was shown
	InteractionClosed            InteractionState = "closed" // student self-solved / dismissed
)

// IsTerminal returns true if the interaction can't advance further.
func (s InteractionState) IsTerminal() bool {
	return s == InteractionSolved || s == InteractionClosed
}

// IsWaitingForAttempt returns true when the student has been asked to try.
func (s InteractionState) IsWaitingForAttempt() bool {
	return s == InteractionWaitingForAttempt
}

// HintLevel returns the numeric hint level for this state (0 if not a hint state).
func (s InteractionState) HintLevel() int {
	switch s {
	case InteractionHint1:
		return 1
	case InteractionHint2:
		return 2
	case InteractionHint3:
		return 3
	default:
		return 0
	}
}

// NextHintState returns the next hint state, or "" if already at max.
func (s InteractionState) NextHintState() InteractionState {
	switch s {
	case InteractionNew, InteractionHint1:
		return InteractionHint2
	case InteractionHint2:
		return InteractionHint3
	default:
		return "" // hint_3 → solver, not another hint
	}
}

// ExitReason captures how the student resolved an interaction (for analytics).
type ExitReason string

const (
	ExitSatisfiedHint1 ExitReason = "satisfied_hint_1"
	ExitSatisfiedHint2 ExitReason = "satisfied_hint_2"
	ExitSatisfiedHint3 ExitReason = "satisfied_hint_3"
	ExitNeededSolution ExitReason = "needed_solution"
	ExitNewQuestion    ExitReason = "new_question" // abandoned for a new question
)

// ExitReasonForState returns the appropriate exit reason when the student
// clicks "Got it" (self-solved) from the given state.
func ExitReasonForState(s InteractionState) ExitReason {
	switch s {
	case InteractionHint1:
		return ExitSatisfiedHint1
	case InteractionHint2:
		return ExitSatisfiedHint2
	case InteractionHint3:
		return ExitSatisfiedHint3
	default:
		return ExitSatisfiedHint1
	}
}

// StudentAttempt stores a student's work submitted between hints, along with
// the evaluator's structured assessment.
type StudentAttempt struct {
	AttemptID      int64           `json:"attempt_id"`
	InteractionID  string          `json:"interaction_id"`
	UserID         string          `json:"user_id"`
	HintIndex      int             `json:"hint_index"` // 1-3
	StudentMessage string          `json:"student_message"`
	EvaluatorJSON  EvaluatorResult `json:"evaluator_json"`
	CreatedAt      time.Time       `json:"created_at"`
}

// EvaluatorResult is the structured rubric output from the attempt evaluator agent.
type EvaluatorResult struct {
	Correct      bool     `json:"correct"`       // overall correctness
	Score        float64  `json:"score"`         // 0.0 – 1.0
	Strengths    []string `json:"strengths"`     // what the student did well
	Errors       []string `json:"errors"`        // specific mistakes
	MissingSteps []string `json:"missing_steps"` // steps the student skipped
	NextGuidance string   `json:"next_guidance"` // tailored advice for the student
	HintConsumed int      `json:"hint_consumed"` // which hint level was active
}

// Interaction tracks a single question the student is working through.
// This is the "short-term memory" — it lives for one question lifecycle.
type Interaction struct {
	ID             string           `json:"id"`
	ConversationID string           `json:"conversation_id"`
	QuestionText   string           `json:"question_text"`
	ImageID        string           `json:"image_id,omitempty"`   // FK to images table (for vision retry)
	SubjectID      *int64           `json:"subject_id,omitempty"` // FK to subjects table
	TopicIDs       []int64          `json:"topic_ids,omitempty"`  // loaded from interaction_topics
	Difficulty     int              `json:"difficulty,omitempty"`
	ProblemText    string           `json:"problem_text,omitempty"` // clean parsed question text
	State          InteractionState `json:"state"`
	HintLevel      int              `json:"hint_level"` // 0-3
	ExitReason     *ExitReason      `json:"exit_reason,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

// StudentProfile stores long-term learning patterns for a student.
type StudentProfile struct {
	UserID          string    `json:"user_id"`
	DisplayName     string    `json:"display_name"`
	ExamTarget      string    `json:"exam_target"`
	TotalQuestions  int       `json:"total_questions"`
	TotalSelfSolved int       `json:"total_self_solved"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
