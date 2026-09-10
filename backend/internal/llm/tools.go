package llm

import "encoding/json"

// ToolDef is one OpenAI-compatible tool definition (function calling).
type ToolDef struct {
	Type     string     `json:"type"` // always "function"
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

// StrProp builds a string property schema.
func StrProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
