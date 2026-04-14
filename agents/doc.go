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

import (
	"log/slog"
	"strings"
)

// stripThinkBlocks removes <think>...</think> blocks that reasoning models
// (e.g. DeepSeek-R1) emit as internal chain-of-thought. These should never
// be shown to the student.  Used for non-streaming (sync) responses.
func stripThinkBlocks(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start == -1 {
			break
		}
		end := strings.Index(s, "</think>")
		if end == -1 {
			// Unclosed <think> — strip from <think> to end
			s = s[:start]
			break
		}
		s = s[:start] + s[end+len("</think>"):]
	}
	return strings.TrimSpace(s)
}

// thinkFilter strips <think>...</think> blocks from streaming LLM responses.
// Reasoning models (e.g. DeepSeek-R1) emit their internal chain-of-thought
// in these blocks, which must never reach the student.
//
// Design: two-phase approach that avoids byte-level slicing (which would
// corrupt multi-byte UTF-8 characters like α, β, ∫).
//
//	Phase 0 (detecting): Buffer tokens until we know if <think> is present.
//	                     DeepSeek-R1 always starts with <think> as the first token.
//	Phase 1 (inThink):   Swallow all tokens until </think> is found.
//	Phase 2 (passthrough): Emit every token directly — normal streaming.
//
// Usage:
//
//	tf := newThinkFilter(func(text string) { /* emit to SSE */ })
//	llmClient.StreamComplete(ctx, msgs, func(token string) error {
//	    tf.Write(token)
//	    return nil
//	})
//	tf.Flush()
type thinkFilter struct {
	buf   strings.Builder
	phase int // 0=detecting, 1=insideThink, 2=passthrough
	emit  func(string)
}

func newThinkFilter(emit func(string)) *thinkFilter {
	return &thinkFilter{emit: emit}
}

// Write processes a single streaming token.
func (f *thinkFilter) Write(token string) {
	switch f.phase {
	case 2: // passthrough — fast path, most tokens land here after think block ends
		f.emit(token)
		return

	case 0: // detecting — buffering to decide if <think> block exists
		f.buf.WriteString(token)
		s := f.buf.String()

		if idx := strings.Index(s, "<think>"); idx != -1 {
			// Found <think> — emit any text before it, enter think phase
			if pre := strings.TrimSpace(s[:idx]); len(pre) > 0 {
				f.emit(pre)
			}
			remainder := s[idx+len("<think>"):]
			// Check if </think> is already in the buffer (tiny think block)
			if endIdx := strings.Index(remainder, "</think>"); endIdx != -1 {
				f.phase = 2
				f.buf.Reset()
				if after := strings.TrimSpace(remainder[endIdx+len("</think>"):]); len(after) > 0 {
					f.emit(after)
				}
			} else {
				f.phase = 1
				f.buf.Reset()
				f.buf.WriteString(remainder)
			}
			return
		}

		// No <think> yet.  DeepSeek-R1 always starts with <think> immediately.
		// If we have >30 bytes with no '<' at all, switch to passthrough.
		if len(s) > 30 && !strings.Contains(s, "<") {
			f.phase = 2
			f.buf.Reset()
			f.emit(s)
		}
		// Otherwise keep buffering (might be a partial "<thi..." across tokens)

	case 1: // inside <think> block — swallow everything until </think>
		f.buf.WriteString(token)
		s := f.buf.String()
		if endIdx := strings.Index(s, "</think>"); endIdx != -1 {
			f.phase = 2
			after := s[endIdx+len("</think>"):]
			f.buf.Reset()
			if trimmed := strings.TrimSpace(after); len(trimmed) > 0 {
				f.emit(trimmed)
			}
		}
		// else keep swallowing
	}
}

// Flush emits any buffered content at the end of the stream.
func (f *thinkFilter) Flush() {
	if f.buf.Len() == 0 {
		return
	}
	s := f.buf.String()
	f.buf.Reset()

	switch f.phase {
	case 0:
		// Never found <think> — emit everything
		if trimmed := strings.TrimSpace(s); len(trimmed) > 0 {
			f.emit(trimmed)
		}
	case 1:
		// Stream ended inside <think> — discard (incomplete reasoning)
		slog.Warn("thinkFilter: stream ended inside <think>, discarding",
			"buffered_bytes", len(s))
	case 2:
		// Shouldn't normally have buffered content in passthrough
		if trimmed := strings.TrimSpace(s); len(trimmed) > 0 {
			f.emit(trimmed)
		}
	}
}
