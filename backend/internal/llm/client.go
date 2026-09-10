// Package llm provides an OpenAI-compatible chat client interface.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Message is a chat message. Tool-calling fields are optional.
type Message struct {
	Role       string     `json:"role"` // system | user | assistant | tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// Client is the LLM interface used by the run executor.
type Client interface {
	// Chat sends messages (and optional tools) and returns the assistant turn.
	Chat(ctx context.Context, messages []Message, tools []ToolDef) (ChatResult, error)
	Model() string
}

// Config holds OpenAI-compatible endpoint settings.
type Config struct {
	Endpoint    string // e.g. https://api.openai.com/v1
	APIKey      string
	Model       string
	Temperature *float64
	MaxTokens   *int
}

// ConfigFromEnv builds config from WIKIATLAS_LLM_* env vars.
// Supports WIKIATLAS_LLM_BASE_URL and legacy WIKIATLAS_LLM_ENDPOINT.
// Returns ok=false when not configured.
func ConfigFromEnv() (Config, bool) {
	key := firstEnv("WIKIATLAS_LLM_API_KEY", "OPENAI_API_KEY")
	if key == "" {
		return Config{}, false
	}
	cfg := Config{
		Endpoint: firstEnv("WIKIATLAS_LLM_BASE_URL", "WIKIATLAS_LLM_ENDPOINT"),
		APIKey:   key,
		Model:    os.Getenv("WIKIATLAS_LLM_MODEL"),
	}
	// 输出上限与温度：过去没读，导致请求里根本没有 max_tokens，
	// 长章节会被服务端默认值截断（表现为写一半、乱码、反复重写）。
	if v := strings.TrimSpace(os.Getenv("WIKIATLAS_LLM_MAX_TOKENS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxTokens = &n
		}
	}
	if v := strings.TrimSpace(os.Getenv("WIKIATLAS_LLM_TEMPERATURE")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			cfg.Temperature = &f
		}
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://api.openai.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-4o-mini"
	}
	return cfg, true
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// OpenAIClient is a minimal OpenAI-compatible chat completions client with tool calling.
type OpenAIClient struct {
	cfg    Config
	client *http.Client
}

// NewOpenAIClient creates a client.
func NewOpenAIClient(cfg Config) *OpenAIClient {
	// 默认输出上限 8192：足够写完整的一章（1000–1500 汉字 ≈ 2–3k token）留足余量，
	// 又不至于让模型一次吐出整篇长文（那会被模型自身预算截断 → 残稿覆盖正文）。
	// 端点通常不校验 max_tokens 上限（实测 131072 也接受），真正的约束来自模型自身。
	// 需要时用 WIKIATLAS_LLM_MAX_TOKENS 覆盖。
	if cfg.MaxTokens == nil {
		def := 8192
		cfg.MaxTokens = &def
	}
	return &OpenAIClient{
		cfg:    cfg,
		client: &http.Client{Timeout: 180 * time.Second},
	}
}

func (c *OpenAIClient) Model() string { return c.cfg.Model }

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
	Tools       []ToolDef `json:"tools,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Role      string     `json:"role"`
			Content   *string    `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Chat implements Client.
func (c *OpenAIClient) Chat(ctx context.Context, messages []Message, tools []ToolDef) (ChatResult, error) {
	// Some providers reject null content when tool_calls are present; normalize.
	norm := make([]Message, len(messages))
	copy(norm, messages)
	for i := range norm {
		if norm[i].Role == "assistant" && len(norm[i].ToolCalls) > 0 && norm[i].Content == "" {
			norm[i].Content = ""
		}
	}

	reqBody := chatRequest{
		Model:       c.cfg.Model,
		Messages:    norm,
		Temperature: c.cfg.Temperature,
		MaxTokens:   c.cfg.MaxTokens,
		Tools:       tools,
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return ChatResult{}, err
	}
	url := strings.TrimRight(c.cfg.Endpoint, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return ChatResult{}, fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()

	var raw bytes.Buffer
	if _, err := raw.ReadFrom(resp.Body); err != nil {
		return ChatResult{}, fmt.Errorf("llm read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ChatResult{}, fmt.Errorf("llm http %d: %s", resp.StatusCode, truncateForErr(raw.String(), 400))
	}
	var cr chatResponse
	if err := json.Unmarshal(raw.Bytes(), &cr); err != nil {
		return ChatResult{}, fmt.Errorf("llm decode: %w", err)
	}
	if cr.Error != nil {
		return ChatResult{}, fmt.Errorf("llm error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return ChatResult{}, fmt.Errorf("llm: no choices returned")
	}
	msg := cr.Choices[0].Message
	content := ""
	if msg.Content != nil {
		content = *msg.Content
	}
	return ChatResult{Content: content, ToolCalls: msg.ToolCalls}, nil
}

func truncateForErr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// EchoClient is a mock used when no LLM is configured. It echoes a canned reply.
type EchoClient struct{}

func (EchoClient) Model() string { return "echo" }

func (EchoClient) Chat(_ context.Context, messages []Message, _ []ToolDef) (ChatResult, error) {
	last := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			last = messages[i].Content
			break
		}
	}
	return ChatResult{Content: "[echo] " + last}, nil
}

// Resolve returns a configured client or EchoClient.
func Resolve(cfg Config) Client {
	if cfg.APIKey != "" {
		return NewOpenAIClient(cfg)
	}
	return EchoClient{}
}

// IsEcho reports whether c is the no-op echo client.
func IsEcho(c Client) bool {
	if c == nil {
		return true
	}
	_, ok := c.(EchoClient)
	return ok
}
