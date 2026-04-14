// Package agents contains all agent implementations for the saras-tutor system.
//
// Architecture: Orchestration Pattern (A2A)
//
//	The SupervisorAgent is the single orchestrator that:
//	  1. Receives every user request
//	  2. Classifies intent via LLM
//	  3. Decides the execution plan (which agents, in what order)
//	  4. Dispatches sub-tasks and collects results
//	  5. Logs & emits every A2A transition to the SSE stream
//
//	Sub-agents NEVER call each other. All routing is supervisor-controlled.
//
//	+--------+     +------------------+     +-----------+
//	| /chat  | --> |   Supervisor     | --> | Image     |
//	|        | <-- |   (orchestrator) | <-- | Extraction|
//	+--------+     +--------+---------+     +-----------+
//	                        |
//	           +------------+------------+
//	           |            |            |
//	     +-----v----+ +----v-----+ +----v------+
//	     |  Solver   | | Verifier | |   Hint    |
//	     +----------+ +----------+ +-----------+
package agents

// mathPrecisionDirective is a shared system-prompt fragment injected into every
// agent that produces or evaluates mathematical content.  It enforces strict
// IIT-JEE-level precision so that no constant, coefficient, or exponent is
// ever silently dropped.
const mathPrecisionDirective = `
MATH PRECISION (MANDATORY — apply to every expression you write):
- You are a precise IIT-JEE tutor. You must NEVER skip constants or coefficients.
- If an equation has a fraction like 1/2, it MUST be explicitly included as \frac{1}{2}. Write $\frac{1}{2}mv^2$, NOT $mv^2/2$ or $½mv²$.
- Always double-check OCR / extracted text against your mathematical intuition.
  For example, if the OCR says "1-t2" but the context is an integral, verify whether it should be "1-t^2" and correct it.
- ALL math must be in LaTeX dollar-sign delimiters:
  • Inline: $...$   (e.g. $F = ma$)
  • Display: $$...$$ on its own line (e.g. $$\int_0^\pi \sin\theta\,d\theta$$)
- NEVER use \( \) or \[ \] delimiters.
- Every exponent must use ^{}: $x^{2}$, $e^{-x}$, $r^{-1}$.
- Every subscript must use _{}: $v_{0}$, $S_{1}$.
- Fractions must use \frac{num}{den}, not a/b inside display math.
- Vectors: $\vec{F}$ or $\overrightarrow{AB}$ — never bare italic F for a vector.
`
