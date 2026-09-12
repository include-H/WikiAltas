// Package llm 是模型协议客户端层：**协议差异只允许收在这里**，
// 往上（执行器、前端）只有一种形状——Responses 的输出项与流事件。
//
// 目前只实现一种协议：OpenAI 的 Responses API（`/v1/responses`，见 responses.go）。
// `Config.Protocol` 这个字段保留着，是因为它是"以后要加新协议"的位置：
// 加协议 = 加一个常量 + 一个客户端 + NewClient 里一个 case，
// 上层与前端一行都不用动。
package llm

import (
	"context"
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

// ProtocolResponses 是当前实现（也是唯一的默认值）。
const ProtocolResponses = "responses"

// UnsupportedProtocolError 是"设置里填了个我们不认识的协议"。它故意不是
// 可重试错误，也故意不让调用方静默退回：配置错了就该在工单里炸出来。
type UnsupportedProtocolError struct{ Protocol string }

func (e UnsupportedProtocolError) Error() string {
	return fmt.Sprintf("llm: 不支持的协议 %q（当前只支持 %s）——请在设置里把协议改回 %s",
		e.Protocol, ProtocolResponses, ProtocolResponses)
}

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

// Config holds endpoint settings. Endpoint 是 API 的**基址**
// （e.g. https://api.openai.com/v1），客户端自己接 /responses。
type Config struct {
	Endpoint    string
	APIKey      string
	Model       string
	Temperature *float64
	MaxTokens   *int
	// ReasoningEffort 是思考等级（none | low | medium | high | xhigh | max；none=关闭）。
	// Responses 规范里它在
	// `reasoning.effort` 上；空 = 不发这个参数（由网关用默认值）。
	ReasoningEffort string
	// Protocol 是协议标识。留空 = 默认（responses）。这是"留后手"的位置：
	// 以后加协议只动这里和 NewClient 的分派，上层不感知。
	Protocol string
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
	// 输出上限与温度：过去没读，导致请求里根本没有 max_output_tokens，
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
	cfg.ReasoningEffort = strings.TrimSpace(os.Getenv("WIKIATLAS_LLM_REASONING_EFFORT"))
	cfg.Protocol = strings.TrimSpace(os.Getenv("WIKIATLAS_LLM_PROTOCOL"))
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

// NewClient 按协议造客户端。空协议 = 默认（responses）。
//
// 认不出的协议返回**明确错误**，不静默退回默认协议：静默退回会让人以为
// "设置生效了"，实际跑的是另一条路——那比直接报错难查得多。
func NewClient(cfg Config) (Client, error) {
	// 旧词表迁移：宿主曾用 "off" 表示关闭，线上协议只认 "none"。在这一个入口归一，
	// 存过旧值的设置 / 工单上下文照常工作——统一词表：none | low | medium | high | xhigh | max。
	if strings.EqualFold(strings.TrimSpace(cfg.ReasoningEffort), "off") {
		cfg.ReasoningEffort = "none"
	}
	p := strings.ToLower(strings.TrimSpace(cfg.Protocol))
	if p == "" {
		p = ProtocolResponses
	}
	switch p {
	case ProtocolResponses:
		return NewResponsesClient(cfg), nil
	default:
		return nil, UnsupportedProtocolError{Protocol: cfg.Protocol}
	}
}

// ErrorClient 是"配置就是错的"这个状态的载体：任何调用都返回构造时那个错误。
//
// 为什么不用 echo 兜底：echo 会静默跑起 mock 流程，界面上看起来一切正常，
// 而用户配的模型一次都没被调用过。配置错误要在工单里炸出来。
type ErrorClient struct{ Err error }

func (c ErrorClient) Model() string { return "error" }

func (c ErrorClient) Chat(context.Context, []Message, []ToolDef) (ChatResult, error) {
	if c.Err == nil {
		return ChatResult{}, errors.New("llm: 客户端未配置")
	}
	return ChatResult{}, c.Err
}

// EchoClient 是"没有配置任何模型"时的占位：执行器识别到它就跑 mock 流程
// （演示/离线可用），而不是发一个注定失败的请求。
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
	return ChatResult{Content: "(echo) " + last}, nil
}

// IsEcho 判断是不是"没有真模型"的占位客户端（nil 也算）。
func IsEcho(c Client) bool {
	if c == nil {
		return true
	}
	_, ok := c.(EchoClient)
	return ok
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
	var up UnsupportedProtocolError
	if errors.As(err, &up) {
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

func truncateForErr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
