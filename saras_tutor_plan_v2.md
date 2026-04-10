# Saras Tutor — Project Summary, Architecture, and Roadmap

Author: Anshu Kumar\
Created: 27 Mar 2026\
Last Updated: 5 Apr 2026

------------------------------------------------------------------------

# 1. Vision

Build an **AI tutor for JEE / NEET students** that:

-   Solves doubts from **images of questions** (handwritten or printed)
-   Provides **hints first instead of direct answers** (progressive 3-level system)
-   **Evaluates student attempts** between hints to measure learning
-   Understands **student strengths and weaknesses** via per-topic tracking
-   Generates **similar practice questions** from weak areas
-   Tracks **topic mastery over time** across sessions
-   Eventually integrates with **WhatsApp and mobile app**

Goal: **Improve conceptual understanding and exam rank**, not just give
answers.

------------------------------------------------------------------------

# 2. Current System Architecture (Implemented)

```
Student → React UI (Vite) → POST /chat → Go/Gin Backend → A2A Router → Sub-Agents → LLM
                ↑                              |                                       |
                └──── SSE streaming ←──────────┘                                       |
                                               ↕                                       |
                                          PostgreSQL ←─────────────────────────────────┘
```

## Multi-Agent Pipeline

```
Image Upload → ImageExtractionAgent (OCR/Vision) → extracted text
                     ↓
           QuestionValidator (PCMB filter)
                     ↓
           QuestionParser (topic/subtopic/difficulty extraction)
                     ↓
           Router creates Interaction (state machine)
                     ↓
           HintAgent (level 1 → 2 → 3) ←→ AttemptEvaluatorAgent (scores student work)
                     ↓
           SolverAgent (full solution) → VerifierAgent (quality gate)
                     ↓ (if verifier fails twice)
           ModelPicker → retry with alternative LLM
```

## Technology Stack

| Layer | Technology |
|-------|-----------|
| Frontend | React 19 + Vite 5, react-markdown, KaTeX, Mermaid |
| Backend | Go 1.25, Gin framework |
| Database | PostgreSQL 16 (Docker) |
| LLM | OpenAI-compatible API (Claude Sonnet 4.5, GPT-5, Gemini) |
| Streaming | Server-Sent Events (SSE) |
| Agent Protocol | Custom A2A (Agent-to-Agent) orchestration |

------------------------------------------------------------------------

# 3. Product Philosophy

Instead of a simple "question solver", Saras Tutor behaves like a **teacher**.

**Implemented Learning Loop:**

1.  Student uploads question (text or image)
2.  System extracts text via OCR (with diagram parsing)
3.  System validates it is a PCMB question
4.  System parses topic, subtopics, difficulty (1–4)
5.  **Hint Level 1**: Gentle conceptual nudge
6.  Student tries → **AttemptEvaluator** scores their work
7.  **Hint Level 2**: Outlined approach + first steps
8.  Student tries again → scored
9.  **Hint Level 3**: Detailed walkthrough
10. Full solution (with quality verification) on demand
11. Student profile updated with topic competency

This builds:

-   Topic mastery tracking
-   Weakness detection per subtopic
-   Personalized hint difficulty
-   Measurable learning improvement

------------------------------------------------------------------------

# 4. Implemented Features

## 4.1 Image Question Solver ✅

-   Students upload photos of printed or handwritten problems
-   Server-side image resizing for optimal LLM consumption
-   Vision-capable LLM extracts: text, options, diagrams
-   Diagram parsing: circuits, free-body diagrams, graphs → Mermaid notation
-   Confidence scoring on extraction quality
-   Persistent image storage in PostgreSQL (BYTEA)

## 4.2 Progressive Hint System (3 Levels) ✅

-   **Level 1**: Gentle nudge — identify the concept/formula, guiding question (no calculations)
-   **Level 2**: Outline approach + first 1–2 steps, student completes the rest
-   **Level 3**: Detailed walkthrough + most steps, student finishes final calculation
-   **Level 4** (auto-escalation): Full solution via Solver agent
-   State machine: `new → hint_1 → hint_2 → hint_3 → solved/closed`
-   Original image passed as context to each hint for diagram awareness

## 4.3 Student Attempt Evaluation ✅

-   Between hints, students can submit their work (typed text or photo of handwritten work)
-   **OCR pipeline**: handwritten photos are first processed by ImageExtractionAgent to extract text
-   AttemptEvaluatorAgent scores the extracted work on a 0–1 rubric:
    -   `correct` (bool), `score` (0.0–1.0)
    -   `strengths[]`, `errors[]`, `missing_steps[]`
    -   `next_guidance` (personalized next step)
-   Results persisted in `student_attempts` table
-   Frontend shows formatted rubric feedback with score, strengths, and issues

## 4.4 Solution Quality Verification ✅

-   VerifierAgent scores every solution (0.0–1.0) before showing to student
-   Minimum threshold: 60%
-   If verification fails, Router retries solver (up to 2 attempts)
-   After 2 failures, student is offered **ModelPicker** to retry with alternative LLM
-   Verifier uses vision support for diagram-heavy questions

## 4.5 Question Validation ✅

-   Filters out non-academic questions
-   Allowed subjects: **Physics, Chemistry, Mathematics, Biology**
-   Rejects casual chat, non-PCMB queries with a friendly message
-   Safe fallback: returns valid on LLM failure

## 4.6 Structured Topic Extraction ✅

-   QuestionParser produces structured metadata from every question:
    -   `subject` (Physics, Chemistry, etc.)
    -   `main_topic` (e.g., Kinematics, Electrostatics, Thermodynamics)
    -   `subtopics[]` (~150+ granular concepts in the taxonomy)
    -   `difficulty` (1–4 scale)
    -   `variables` (extracted with units)
-   Data stored per-interaction for analytics

## 4.7 Student Knowledge Tracking ✅

-   `student_profiles` table stores per-student longitudinal data
-   `aggr_stats` (JSONB) tracks per-conversation question history:
    -   Topics + subtopics attempted
    -   Difficulty level of each question
    -   Hint level consumed before solving
    -   Whether student self-solved or needed full solution
-   Enables future: personalized difficulty, weak-topic recommendations

## 4.8 Streaming SSE Responses ✅

-   All agents support `HandleStream()` for token-by-token output
-   Frontend receives and renders tokens in real-time
-   Event types: `artifact`, `transition`, `metadata`, `status`, `error`
-   Agent-to-agent transition events visible in UI for transparency

## 4.9 Rich Content Rendering ✅

-   **Markdown** with full formatting (headings, lists, bold, code blocks)
-   **LaTeX math** via KaTeX: inline `$...$` and display `$$...$$`
-   **Mermaid diagrams**: flowcharts, graphs rendered as SVG
-   Automatic `\(...\)` → `$...$` conversion for LLM compatibility

## 4.10 Multi-Model Support ✅

-   Default model configurable via `OPENAI_MODEL_DEFAULT`
-   Separate `VISION_MODEL` for image extraction
-   `RETRY_MODELS` (comma-separated) for fallback on verification failure
-   Frontend ModelPicker UI lets student choose retry model

## 4.11 Full Conversation History ✅

-   All user messages and assistant responses persisted to `messages` table
-   Images stored in `images` table (binary + metadata)
-   Conversation-level grouping by user + session
-   Powers future: review past questions, resume sessions

------------------------------------------------------------------------

# 5. Agent Architecture (6 Agents)

| # | Agent | ID | Role |
|---|-------|----|------|
| 1 | **Router** | `router` | Deterministic dispatcher — routes by `action` field, manages state machine, no LLM |
| 2 | **ImageExtractionAgent** | `image_extraction` | Vision OCR — extracts text, options, diagrams from question photos and handwritten work |
| 3 | **HintAgent** | `hint` | Progressive pedagogy — curated prompts per level, streams hints with image context |
| 4 | **SolverAgent** | `solver` | Full step-by-step solution in Markdown+LaTeX, vision-aware |
| 5 | **VerifierAgent** | `verifier` | Quality gate — scores solutions 0–1, catches errors before student sees them |
| 6 | **AttemptEvaluatorAgent** | `attempt_evaluator` | Rubric scorer — evaluates student work against expected progress at current hint level |

**Supporting utilities** (not full agents):
-   `QuestionValidator`: PCMB subject filter
-   `QuestionParser`: Structured topic/difficulty extraction with 150+ subtopic taxonomy

**Protocol**: All agents implement `a2a.Agent` interface:
```go
type Agent interface {
    Card() AgentCard
    Handle(ctx, *Task) (*Task, error)
    HandleStream(ctx, *Task, chan<- StreamEvent)
}
```

------------------------------------------------------------------------

# 6. API Design

## POST /chat

Primary endpoint. Accepts JSON or multipart/form-data. Returns SSE stream.

**Request fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `user_id` | Yes | Student identifier |
| `session_id` | Yes | Session grouping |
| `action` | Yes | `new_question`, `more_help`, `show_solution`, `retry_model`, `submit_attempt`, `close` |
| `text` | Conditional | Question or attempt text |
| `image` | Conditional | Photo upload (multipart) |
| `model` | No | Override LLM model (for retry) |

**SSE event types:**

| Type | Purpose |
|------|---------|
| `artifact` | Streaming text tokens (solution, hint, feedback) |
| `transition` | Agent routing trace (e.g., `router → hint`) |
| `metadata` | Machine-readable data (scores, token counts, hint levels) |
| `status` | State changes (working, completed, input-needed) |
| `error` | Error messages |

**Response headers:** `X-Conversation-ID` (for session continuity)

## GET /health

Liveness probe. Returns `{"status":"ok"}`.

------------------------------------------------------------------------

# 7. Database Schema (6 Tables)

### conversations
Groups messages for a user+session pair.

| Column | Type | Purpose |
|--------|------|---------|
| id | TEXT PK | UUID |
| user_id | TEXT | Student ID |
| session_id | TEXT | Session ID |
| created_at | TIMESTAMPTZ | Timestamp |

### messages
Full chat history with role tracking.

| Column | Type | Purpose |
|--------|------|---------|
| id | TEXT PK | UUID |
| conversation_id | TEXT FK | Parent conversation |
| role | TEXT | `user` or `assistant` |
| content | TEXT | Message text |
| content_type | TEXT | `text` or `image_url` |
| agent | TEXT | Which agent generated this (hint, solver, etc.) |
| created_at | TIMESTAMPTZ | Timestamp |

### images
Binary image storage for question photos.

| Column | Type | Purpose |
|--------|------|---------|
| id | TEXT PK | UUID |
| conversation_id | TEXT FK | Parent conversation |
| message_id | TEXT FK | Associated message |
| filename | TEXT | Original filename |
| mime_type | TEXT | Image MIME type |
| data | BYTEA | Raw binary image |

### interactions
Per-question lifecycle tracking (state machine).

| Column | Type | Purpose |
|--------|------|---------|
| id | TEXT PK | UUID |
| conversation_id | TEXT FK | Parent conversation |
| question_text | TEXT | Extracted question |
| image_id | TEXT | Reference to uploaded image |
| main_topic | TEXT | e.g., "Kinematics" |
| subtopics | TEXT[] | e.g., {"relative_velocity", "projectile_motion"} |
| difficulty | INT | 1–4 scale |
| state | TEXT | `new`, `hint_1`, `hint_2`, `hint_3`, `waiting_for_attempt`, `solved`, `closed` |
| hint_level | INT | Current hint level (0–3) |
| exit_reason | TEXT | How interaction ended |
| problem_json | TEXT | Full parsed question as JSON |

### student_attempts
Student work submissions scored by evaluator.

| Column | Type | Purpose |
|--------|------|---------|
| attempt_id | BIGSERIAL PK | Auto-increment |
| interaction_id | TEXT FK | Parent interaction |
| user_id | TEXT | Student ID |
| hint_index | INT | Which hint level (1–3) |
| student_message | TEXT | What the student submitted |
| evaluator_json | JSONB | Full rubric result (score, strengths, errors, etc.) |

### student_profiles
Longitudinal student competency data.

| Column | Type | Purpose |
|--------|------|---------|
| user_id | TEXT PK | Student ID |
| name | TEXT | Student name |
| total_questions | INT | Lifetime question count |
| aggr_stats | JSONB | Per-conversation topic/difficulty/hint-level history |

------------------------------------------------------------------------

# 8. Frontend Components (7 Components)

| Component | Purpose |
|-----------|---------|
| **App.jsx** | Root — message state, SSE streaming, routing logic |
| **MessageBubble.jsx** | Renders messages by type: assistant (Markdown), user, transition, metadata, error |
| **MarkdownRenderer.jsx** | Markdown + LaTeX (KaTeX) + Mermaid diagram rendering |
| **InputBar.jsx** | Text + image input; context-aware placeholder during hint flow |
| **HintActions.jsx** | Post-hint buttons: "Got it", "Another hint", "Show solution" + hint level dots |
| **ModelPicker.jsx** | Alternative model selection when verifier fails twice |
| **ConfirmExtraction.jsx** | Confirm extracted question text before proceeding |

------------------------------------------------------------------------

# 9. Core Differentiator — Active Learning Loop

Most AI tools today:

```
Student sends question → AI solves it → Student reads → Done
```

**Saras Tutor implements an Active Learning Loop:**

```
Student question
     ↓
Image OCR + Topic parsing
     ↓
Hint Level 1 (conceptual nudge)
     ↓
Student submits attempt (text or photo) ←────────┐
     ↓                                           │
AttemptEvaluator scores work (rubric)             │
     ↓                                           │
Score < threshold? → Hint Level 2 ────────────────┘
     ↓
Score good? → "Great work!" + update competency
     ↓
Full solution available on demand (quality-verified)
     ↓
Student profile updated (topic × difficulty × hint level)
```

This creates **measurable learning** — not just answer delivery.

------------------------------------------------------------------------

# 10. Configuration

| Variable | Default | Purpose |
|----------|---------|---------|
| `PORT` | `8080` | Backend server port |
| `DATABASE_URL` | `postgres://saras:saras@localhost:5432/saras_tutor?sslmode=disable` | PostgreSQL connection |
| `LLM_BASE_URL` | `https://api.openai.com/v1` | OpenAI-compatible endpoint |
| `LLM_API_KEY` | — | API authentication |
| `LLM_USER_ID` | — | Optional proxy user header |
| `OPENAI_MODEL_DEFAULT` | `claude-sonnet-4.5` | Default solving model |
| `VISION_MODEL` | `claude-sonnet-4.5` | Image extraction model |
| `RETRY_MODELS` | `gpt-5,gpt-4,gemini` | Fallback models (comma-separated) |

**Loading order:** `.env` file → environment variables → hardcoded defaults

------------------------------------------------------------------------

# 11. Infrastructure

**Database:** PostgreSQL 16 Alpine via Docker Compose

```
docker compose up -d
```

**Backend:** `go run main.go` (auto-migrates DB on startup)

**Frontend:** `cd frontend && npm install && npm run dev` (Vite on port 5173, proxies to :8080)

------------------------------------------------------------------------

# 12. Challenges & Mitigations

| Challenge | Mitigation (Implemented) |
|-----------|-------------------------|
| Many AI tools already solve questions | Focus on **hint-first learning loop** with attempt evaluation |
| Handwritten image quality varies | Dedicated OCR agent with confidence scoring |
| Diagram questions (circuits, pulleys) | Mermaid diagram extraction + vision context in hints |
| LLM can produce wrong solutions | VerifierAgent quality gate + model retry fallback |
| Cost control for LLM usage | Hints use less tokens than full solutions; streaming avoids timeout costs |
| Tracking student competency | `student_profiles` with JSONB aggregated stats |

------------------------------------------------------------------------

# 13. Roadmap — What's Next

## Near-term (Week 1–2)

-   [ ] Test with 10 real JEE questions end-to-end
-   [ ] Tune hint prompts based on student feedback
-   [ ] Practice question generation from weak topics
-   [ ] Concept Gap Mapping (detect missing prerequisites)

## Medium-term (Month 1–2)

-   [ ] Student knowledge graph with per-subtopic skill scores
-   [ ] Skill update rules (score ±Δ on success/failure)
-   [ ] Question history review UI
-   [ ] Chemistry + Biology topic taxonomy (currently Physics-heavy)

## Long-term

-   [ ] WhatsApp Bot integration
-   [ ] Android/iOS mobile app
-   [ ] Spaced repetition for weak topics
-   [ ] Batch analytics dashboard for coaches/teachers
-   [ ] Deployment to Railway / Fly.io

------------------------------------------------------------------------

# 14. First Milestone

**10 students solving doubts daily using Saras Tutor.**

Do NOT think about scale yet. Ship → learn → iterate.

------------------------------------------------------------------------

# 15. Founder Reminder

You already have advantages:

-   Backend engineering experience
-   AI system design knowledge
-   **Working multi-agent prototype with 6 agents, 6 DB tables, and full frontend**

Most founders fail because they **never ship**.

Keep moving forward **one small step every day**.

Progress beats perfection.
