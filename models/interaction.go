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
	HintLevel      int             `json:"hint_level"` // 1-3: which hint level was active
	Score          float64         `json:"score"`      // 0.0–1.0 (promoted from evaluator_json)
	Correct        bool            `json:"correct"`    // promoted from evaluator_json
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
	ExamTarget      string    `json:"exam_target"` // JEE | NEET | BOTH
	TotalQuestions  int       `json:"total_questions"`
	TotalSelfSolved int       `json:"total_self_solved"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// StudentTopicMastery stores per-topic aggregate learning signal.
type StudentTopicMastery struct {
	UserID          string     `json:"user_id"`
	TopicID         int64      `json:"topic_id"`
	MasteryScore    float64    `json:"mastery_score"` // 0.0–1.0 weighted composite
	TotalAttempts   int        `json:"total_attempts"`
	CorrectAttempts int        `json:"correct_attempts"`
	AvgScore        float64    `json:"avg_score"`        // mean attempt score on this topic
	AvgHintsUsed    float64    `json:"avg_hints_used"`   // mean hints consumed before correct
	SolutionsViewed int        `json:"solutions_viewed"` // full solution requests
	LastAttemptAt   *time.Time `json:"last_attempt_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// ComputeMasteryScore calculates the weighted composite mastery score.
//
//	mastery = 0.40·avg_score + 0.30·self_solve_rate − 0.15·hint_penalty − 0.15·solution_penalty
//
// Where:
//
//	self_solve_rate = correct_attempts / total_attempts
//	hint_penalty    = avg_hints_used / 3   (normalised to 0–1)
//	solution_penalty = solutions_viewed / total_attempts
func (m *StudentTopicMastery) ComputeMasteryScore() float64 {
	if m.TotalAttempts == 0 {
		return 0.0
	}
	selfSolveRate := float64(m.CorrectAttempts) / float64(m.TotalAttempts)
	hintPenalty := m.AvgHintsUsed / 3.0
	solutionPenalty := float64(m.SolutionsViewed) / float64(m.TotalAttempts)

	score := 0.40*m.AvgScore + 0.30*selfSolveRate - 0.15*hintPenalty - 0.15*solutionPenalty

	// Clamp to [0, 1]
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score
}
