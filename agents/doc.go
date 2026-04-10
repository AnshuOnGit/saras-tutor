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
