// Package llm provides an OpenAI-compatible chat client interface.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// streamIdleTimeout：流式连接多久没有任何字节就判死（长回答不受影响，卡死才断）。
const streamIdleTimeout = 150 * time.Second

// atomicInt64 只是让看门狗和读循环共享一个时间戳。
type atomicInt64 = atomic.Int64

// activityReader 每读到一点数据就刷新最后活动时间。
type activityReader struct {
	rc   io.ReadCloser
	last *atomic.Int64
}

func (r *activityReader) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	if n > 0 {
		r.last.Store(time.Now().UnixNano())
	}
	return n, err
}

func (r *activityReader) Close() error { return r.rc.Close() }

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

// Delta 是一次流式增量（对齐 DeepSeek Harness 的 StreamChunk：文本 / 思维链 / 工具参数）。
// ToolIndex 用来把同一轮里的多个 tool_call 增量归并到一起。
type Delta struct {
	Kind      string // text | reasoning | tool
	Text      string
	ToolIndex int
	ToolID    string
	ToolName  string
}

// StreamingClient 额外支持增量输出；执行器优先用它，接口不满足时退回 Chat。
type StreamingClient interface {
	ChatStream(ctx context.Context, messages []Message, tools []ToolDef, onDelta func(Delta)) (ChatResult, error)
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
	cfg Config
	// 非流式请求给整体超时；流式请求不能有整体超时（长回答会被腰斩），
	// 靠 ChatStream 里的 idle 看门狗兜底。
	client *http.Client
	stream *http.Client
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
		client: &http.Client{Timeout: 300 * time.Second},
		stream: &http.Client{},
	}
}

// HTTPError 保留状态码，供执行器判断"该不该重试"。
type HTTPError struct {
	Status int
	Body   string
}

func (e HTTPError) Error() string { return fmt.Sprintf("llm http %d: %s", e.Status, e.Body) }

// IsRetryable 判断错误是否值得重试：限流/服务端 5xx/网络抖动 → 重试；
// 用户取消、超时（可能只是长回答）、模型返回的业务错误 → 不重试。
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var he HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusTooManyRequests || he.Status >= 500
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "llm request:") || strings.Contains(msg, "llm stream:")
}

func (c *OpenAIClient) Model() string { return c.cfg.Model }

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
	Tools       []ToolDef `json:"tools,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
}

// chatStreamChunk 是 OpenAI 兼容流式响应里的一帧（只取我们用得到的字段）。
type chatStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content          *string `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
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
		return ChatResult{}, HTTPError{Status: resp.StatusCode, Body: truncateForErr(raw.String(), 400)}
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

// ChatStream implements StreamingClient：SSE 增量 + 拼回完整 ChatResult。
// 增量先喂给 onDelta（文本/思维链/工具参数），流结束后返回可落库的完整结果；
// 与 Chat 共享同一套请求体，provider 不支持 stream 时由调用方回退到 Chat。
func (c *OpenAIClient) ChatStream(
	ctx context.Context,
	messages []Message,
	tools []ToolDef,
	onDelta func(Delta),
) (ChatResult, error) {
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
		Stream:      true,
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
	req.Header.Set("Accept", "text/event-stream")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	// idle 看门狗：流式连接不设整体超时（长回答可能跑十几分钟），
	// 但超过 streamIdleTimeout 一个字节都没来就取消，避免工单永远挂着。
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var lastRead atomicInt64
	lastRead.Store(time.Now().UnixNano())
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if time.Since(time.Unix(0, lastRead.Load())) > streamIdleTimeout {
					cancel()
					return
				}
			}
		}
	}()
	resp, err := c.stream.Do(req.WithContext(ctx))
	if err != nil {
		return ChatResult{}, fmt.Errorf("llm request: %w", err)
	}
	// 读的时候刷新"最后活动时间"
	body := &activityReader{rc: resp.Body, last: &lastRead}
	defer body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(body, 4096))
		return ChatResult{}, HTTPError{Status: resp.StatusCode, Body: truncateForErr(string(raw), 400)}
	}

	var (
		content   strings.Builder
		toolOrder []int
		toolByIdx = map[int]*ToolCall{}
	)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk chatStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // 心跳/非 JSON 帧直接跳过
		}
		if chunk.Error != nil {
			return ChatResult{}, fmt.Errorf("llm error: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.ReasoningContent != nil && *delta.ReasoningContent != "" && onDelta != nil {
			onDelta(Delta{Kind: "reasoning", Text: *delta.ReasoningContent})
		}
		if delta.Content != nil && *delta.Content != "" {
			content.WriteString(*delta.Content)
			if onDelta != nil {
				onDelta(Delta{Kind: "text", Text: *delta.Content})
			}
		}
		for _, tc := range delta.ToolCalls {
			call, ok := toolByIdx[tc.Index]
			if !ok {
				call = &ToolCall{Type: "function"}
				toolByIdx[tc.Index] = call
				toolOrder = append(toolOrder, tc.Index)
			}
			if tc.ID != "" {
				call.ID = tc.ID
			}
			if tc.Function.Name != "" {
				call.Function.Name = tc.Function.Name
			}
			call.Function.Arguments += tc.Function.Arguments
			if onDelta != nil && (tc.Function.Arguments != "" || tc.Function.Name != "") {
				onDelta(Delta{
					Kind:      "tool",
					Text:      tc.Function.Arguments,
					ToolIndex: tc.Index,
					ToolID:    call.ID,
					ToolName:  call.Function.Name,
				})
			}
		}
	}
	if err := scanner.Err(); err != nil && content.Len() == 0 && len(toolOrder) == 0 {
		return ChatResult{}, fmt.Errorf("llm stream: %w", err)
	}

	out := ChatResult{Content: content.String()}
	for _, idx := range toolOrder {
		if call := toolByIdx[idx]; call != nil {
			out.ToolCalls = append(out.ToolCalls, *call)
		}
	}
	return out, nil
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
