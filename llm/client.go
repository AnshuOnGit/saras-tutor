// Package llm provides a thin abstraction over OpenAI-compatible chat completion APIs.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// MinConfidence is the global threshold. Any LLM response below this value
// causes the call to fail with ErrLowConfidence.
const MinConfidence = 0.9

// insecureHTTPClient skips TLS certificate verification (for local proxy use only).
var insecureHTTPClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
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
}

// --- public API ---

// Complete sends a non-streaming chat completion and returns the full response
// along with model name and token usage.
func (c *Client) Complete(ctx context.Context, messages []ChatMessage) (*CompletionResult, error) {
	body, _ := json.Marshal(chatRequest{
		Model:     c.Model,
		Messages:  messages,
		Stream:    false,
		User:      c.UserID,
		MaxTokens: c.MaxTokens,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := insecureHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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
	slog.Info("llm complete", "model", model, "tokens", cr.Usage.TotalTokens, "response_len", len(content))
	return &CompletionResult{
		Content: content,
		Model:   model,
		Usage:   cr.Usage,
	}, nil
}

// StreamComplete sends a streaming chat completion and delivers each token to onToken.
// Return an error from onToken to abort the stream.
func (c *Client) StreamComplete(ctx context.Context, messages []ChatMessage, onToken func(token string) error) error {
	body, _ := json.Marshal(chatRequest{
		Model:    c.Model,
		Messages: messages,
		Stream:   true,
		User:     c.UserID,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := insecureHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("llm: status %d: %s", resp.StatusCode, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	var full strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				full.WriteString(ch.Delta.Content)
				if err := onToken(ch.Delta.Content); err != nil {
					return err
				}
			}
		}
	}
	slog.Debug("llm stream complete", "model", c.Model, "response_len", full.Len())
	return scanner.Err()
}
