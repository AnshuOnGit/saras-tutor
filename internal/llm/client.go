// Package llm provides a thin abstraction over OpenAI-compatible chat completion APIs.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"saras-tutor/internal/logger"
	"strings"
	"time"
)

// MinConfidence is the global threshold. Any LLM response below this value
// causes the call to fail with ErrLowConfidence.
const MinConfidence = 0.9

var httpClient = &http.Client{
	Transport: &http.Transport{},
}

// Usage captures token counts from an API response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// CompletionResult is the rich return value from Complete().
type CompletionResult struct {
	Content string
	Model   string // model that actually responded
	Usage   Usage
}

// ErrLowConfidence is returned when the model's self-reported confidence
// is below MinConfidence.
type ErrLowConfidence struct {
	Got       float64
	Threshold float64
	Agent     string
}

func (e *ErrLowConfidence) Error() string {
	return fmt.Sprintf("low confidence: %.2f (threshold %.2f) from %s — aborting", e.Got, e.Threshold, e.Agent)
}

// Client talks to an OpenAI-compatible chat completions endpoint.
type Client struct {
	APIKey    string
	Model     string
	BaseURL   string
	UserID    string // sent as X-User-ID header for proxy authentication
	MaxTokens int    // optional; 0 means use model default
}

// NewClient creates a client for the given model.
func NewClient(apiKey, model, baseURL, userID string) *Client {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &Client{
		APIKey:  apiKey,
		Model:   model,
		BaseURL: baseURL,
		UserID:  userID,
	}
}

// setHeaders applies auth and proxy headers to an outgoing request.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.UserID != "" {
		req.Header.Set("X-User-ID", c.UserID)
	}
}

// --- request / response types ---

// ChatMessage represents a single message in the chat history.
type ChatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []ContentPart
}

// ContentPart is used for multi-modal messages (text + image).
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL wraps the URL for a vision request.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "low", "high", or "auto" (default)
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	Stream    bool          `json:"stream"`
	User      string        `json:"user,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
}

// summarizeMessages renders chat messages into a compact, log-safe form
// (truncating long text and replacing image data URIs with their byte size).
// Used by Complete / StreamComplete so the operator can verify exactly what
// prompt was sent to the LLM.
func summarizeMessages(messages []ChatMessage) []map[string]string {
	const maxTextLen = 2000
	out := make([]map[string]string, 0, len(messages))
	for i, m := range messages {
		entry := map[string]string{
			"i":    fmt.Sprintf("%d", i),
			"role": m.Role,
		}
		switch v := m.Content.(type) {
		case string:
			entry["text"] = truncate(v, maxTextLen)
			entry["len"] = fmt.Sprintf("%d", len(v))
		case []ContentPart:
			var parts []string
			for _, p := range v {
				switch p.Type {
				case "text":
					parts = append(parts, fmt.Sprintf("text(%d): %s", len(p.Text), truncate(p.Text, maxTextLen)))
				case "image_url":
					url := ""
					if p.ImageURL != nil {
						url = p.ImageURL.URL
					}
					if strings.HasPrefix(url, "data:") {
						parts = append(parts, fmt.Sprintf("image[data-uri %d bytes]", len(url)))
					} else {
						parts = append(parts, fmt.Sprintf("image[url=%s]", truncate(url, 200)))
					}
				default:
					parts = append(parts, fmt.Sprintf("%s[?]", p.Type))
				}
			}
			entry["parts"] = strings.Join(parts, " | ")
		default:
			entry["content"] = fmt.Sprintf("%T", v)
		}
		out = append(out, entry)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("…[+%d chars]", len(s)-n)
}

// --- public API ---

// Complete sends a non-streaming chat completion and returns the full response
// along with model name and token usage.
func (c *Client) Complete(ctx context.Context, messages []ChatMessage) (*CompletionResult, error) {
	reqPayload := chatRequest{
		Model:     c.Model,
		Messages:  messages,
		Stream:    false,
		User:      c.UserID,
		MaxTokens: c.MaxTokens,
	}
	body, _ := json.Marshal(reqPayload)

	logger.Info().Str("model", c.Model).
		Str("base_url", c.BaseURL).
		Int("body_bytes", len(body)).
		Int("max_tokens", c.MaxTokens).
		Int("msg_count", len(messages)).
		Interface("messages", summarizeMessages(messages)).
		Msg("llm: request")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	httpStart := time.Now()
	resp, err := httpClient.Do(req)
	httpDuration := time.Since(httpStart)
	if err != nil {
		logger.Error().Str("model", c.Model).
			Err(err).
			Int64("elapsed_ms", httpDuration.Milliseconds()).
			Msg("llm: HTTP request failed")
		return nil, err
	}
	defer resp.Body.Close()

	logger.Info().Str("model", c.Model).
		Int("status", resp.StatusCode).
		Int64("elapsed_ms", httpDuration.Milliseconds()).
		Str("elapsed_s", fmt.Sprintf("%.1f", httpDuration.Seconds())).
		Msg("llm: HTTP response received")

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llm: status %d: %s", resp.StatusCode, string(b))
	}

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, err
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("llm: no choices returned")
	}
	content := cr.Choices[0].Message.Content
	model := cr.Model
	if model == "" {
		model = c.Model
	}
	logger.Info().Str("model", model).
		Int("prompt_tokens", cr.Usage.PromptTokens).
		Int("completion_tokens", cr.Usage.CompletionTokens).
		Int("total_tokens", cr.Usage.TotalTokens).
		Int("response_len", len(content)).
		Str("response", truncate(content, 4000)).
		Int64("total_elapsed_ms", httpDuration.Milliseconds()).
		Msg("llm: complete")
	return &CompletionResult{
		Content: content,
		Model:   model,
		Usage:   cr.Usage,
	}, nil
}

// StreamComplete sends a streaming chat completion and delivers each token to onToken.
// Return an error from onToken to abort the stream.
// StreamResult holds token usage returned from a streaming call.
type StreamResult struct {
	Usage       Usage
	TokenChunks int // number of content chunks received; 0 means empty stream, -1 means non-retryable
}

// maxStreamRetries is the number of times to retry a stream that fails with
// an immediate EOF or 0-token response before giving up.  NVIDIA NIM endpoints
// sometimes accept the connection and then hang up instantly.
const maxStreamRetries = 2

// StreamComplete sends a streaming chat completion and delivers each token to onToken.
// Return an error from onToken to abort the stream.
//
// Automatic retry: if the stream connects but immediately returns EOF or
// produces 0 tokens (a common transient failure on NVIDIA NIM), the request
// is retried up to maxStreamRetries times with a short back-off.
func (c *Client) StreamComplete(ctx context.Context, messages []ChatMessage, onToken func(token string) error) (*StreamResult, error) {
	logger.Info().Str("model", c.Model).
		Str("base_url", c.BaseURL).
		Int("max_tokens", c.MaxTokens).
		Int("msg_count", len(messages)).
		Interface("messages", summarizeMessages(messages)).
		Msg("llm: stream request prompt")

	body, _ := json.Marshal(chatRequest{
		Model:     c.Model,
		Messages:  messages,
		Stream:    true,
		User:      c.UserID,
		MaxTokens: c.MaxTokens,
	})

	var lastErr error
	for attempt := 0; attempt <= maxStreamRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 2 * time.Second
			logger.Warn().Str("model", c.Model).
				Int("attempt", attempt+1).
				Float64("backoff_s", backoff.Seconds()).
				AnErr("prev_error", lastErr).
				Msg("llm: stream retry")
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		result, err := c.doStreamRequest(ctx, body, attempt, onToken)
		if err == nil {
			return result, nil
		}

		// Abort early on context cancellation.
		if ctx.Err() != nil {
			return result, err
		}
		lastErr = err

		// Non-200 HTTP status → not retryable (doStreamRequest tags these
		// with TokenChunks == -1). Return the error immediately so the
		// caller can surface a model-picker UX without the user waiting
		// through N rounds of back-off for a permanent failure.
		if result != nil && result.TokenChunks < 0 {
			logger.Warn().Str("model", c.Model).
				Int("attempt", attempt+1).
				Err(err).
				Msg("llm: stream non-retryable failure")
			return result, err
		}

		// Heuristic: only retry if the stream produced nothing useful
		// (immediate EOF / 0 tokens).  If we already streamed tokens to
		// the user, retrying would duplicate content.
		if result != nil && result.TokenChunks > 0 {
			return result, err
		}
	}
	return nil, fmt.Errorf("llm: stream failed after %d attempts (%s): %w", maxStreamRetries+1, c.Model, lastErr)
}

// doStreamRequest executes a single streaming HTTP request and reads the SSE stream.
func (c *Client) doStreamRequest(ctx context.Context, body []byte, attempt int, onToken func(token string) error) (*StreamResult, error) {
	logger.Info().Str("model", c.Model).
		Int("body_bytes", len(body)).
		Int("max_tokens", c.MaxTokens).
		Int("msg_count", -1). // not available here, logged by caller
		Int("attempt", attempt+1).
		Msg("llm: stream request")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	httpStart := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		logger.Error().Str("model", c.Model).
			Int("attempt", attempt+1).
			Err(err).
			Int64("elapsed_ms", time.Since(httpStart).Milliseconds()).
			Msg("llm: stream HTTP failed")
		return nil, err
	}
	defer resp.Body.Close()

	logger.Info().Str("model", c.Model).
		Int("attempt", attempt+1).
		Int("status", resp.StatusCode).
		Int64("time_to_first_byte_ms", time.Since(httpStart).Milliseconds()).
		Msg("llm: stream connected")

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		// Non-200 is not retryable — return a sentinel that the caller won't retry
		return &StreamResult{TokenChunks: -1}, fmt.Errorf("llm: status %d: %s", resp.StatusCode, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	// Increase scanner buffer for very large streaming responses (1 MB)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var full strings.Builder
	tokenCount := 0
	lineCount := 0
	var usage Usage
	for scanner.Scan() {
		line := scanner.Text()
		lineCount++
		if line == "" {
			continue // SSE blank separator
		}
		if !strings.HasPrefix(line, "data: ") {
			logger.Debug().Str("model", c.Model).
				Int("line_num", lineCount).
				Str("line", line).
				Msg("llm: stream non-data line")
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			logger.Warn().Str("model", c.Model).
				Err(err).
				Int("line_num", lineCount).
				Str("data_prefix", truncateStr(data, 300)).
				Msg("llm: stream chunk parse failed")
			continue
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				full.WriteString(ch.Delta.Content)
				tokenCount++
				if err := onToken(ch.Delta.Content); err != nil {
					// onToken error — not retryable (already streamed to user)
					return &StreamResult{Usage: usage, TokenChunks: tokenCount}, err
				}
			}
		}
	}
	elapsed := time.Since(httpStart)

	// Check for scanner errors (unexpected EOF, buffer overflow, etc.)
	if scanErr := scanner.Err(); scanErr != nil {
		logger.Error().Str("model", c.Model).
			Int("attempt", attempt+1).
			Err(scanErr).
			Int("lines_read", lineCount).
			Int("token_chunks", tokenCount).
			Int64("elapsed_ms", elapsed.Milliseconds()).
			Msg("llm: stream scanner error")
		return &StreamResult{Usage: usage, TokenChunks: tokenCount},
			fmt.Errorf("llm: stream read error (%s): %w", c.Model, scanErr)
	}

	// Empty stream — retryable
	if tokenCount == 0 {
		logger.Warn().Str("model", c.Model).
			Int("attempt", attempt+1).
			Int("lines_read", lineCount).
			Int64("elapsed_ms", elapsed.Milliseconds()).
			Msg("llm: stream returned 0 tokens")
		return &StreamResult{Usage: usage, TokenChunks: 0},
			fmt.Errorf("llm: stream from %s returned 0 tokens (lines read: %d)", c.Model, lineCount)
	}

	logger.Info().Str("model", c.Model).
		Int("attempt", attempt+1).
		Int("prompt_tokens", usage.PromptTokens).
		Int("completion_tokens", usage.CompletionTokens).
		Int("total_tokens", usage.TotalTokens).
		Int("response_len", full.Len()).
		Str("response", truncate(full.String(), 4000)).
		Int("token_chunks", tokenCount).
		Int64("total_elapsed_ms", elapsed.Milliseconds()).
		Str("total_elapsed_s", fmt.Sprintf("%.1f", elapsed.Seconds())).
		Msg("llm: stream complete")
	return &StreamResult{Usage: usage, TokenChunks: tokenCount}, nil
}

// truncateStr trims s to at most n bytes for safe logging.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
