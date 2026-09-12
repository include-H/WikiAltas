package llm

import "encoding/json"

// ToolDef is one OpenAI-compatible tool definition (function calling).
type ToolDef struct {
	Type     string      `json:"type"` // always "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef describes a callable function.
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// FunctionCall is the model's requested invocation.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolCall is one tool invocation returned by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// ChatResult is the assistant turn: either final text and/or tool calls.
type ChatResult struct {
	Content   string
	ToolCalls []ToolCall
	// Reasoning 是本轮模型输出的思考全文（网关的 reasoning 字段），
	// 只用于展示与回放，不会回喂给模型。
	Reasoning string
	// Usage 是本轮用量（跨 provider 归一化）。
	Usage Usage
}

// Usage 是一次模型调用的用量，跨 provider 归一化。
//
// CacheReadTokens 是判断"前缀有没有被悄悄改写"的生产信号：只要对话是追加式的、
// 头部（system + 工具 schema）不变，第二次以后的每一次调用都该有缓存命中；
// 它掉到 0 就说明前面某处被重写了（压缩、改提示、换工具集、或者是重建历史）。
type Usage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
	// CacheReadTokens 命中前缀缓存的输入 token（Responses 报在
	// input_tokens_details.cached_tokens 上）。
	CacheReadTokens int `json:"cacheReadTokens"`
	// CacheWriteTokens 写入缓存的 token（多数网关不报，恒为 0）。
	CacheWriteTokens int `json:"cacheWriteTokens"`
}

// NewFunctionTool builds a tool definition from a JSON-schema parameters object.
func NewFunctionTool(name, desc string, params map[string]any) ToolDef {
	raw, _ := json.Marshal(params)
	return ToolDef{
		Type: "function",
		Function: FunctionDef{
			Name:        name,
			Description: desc,
			Parameters:  raw,
		},
	}
}

// ObjectSchema is a small helper for JSON-schema objects.
func ObjectSchema(props map[string]any, required []string) map[string]any {
	s := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
