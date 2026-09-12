// Package tools defines the librarian tool registry skeleton.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Tool is the interface every librarian tool implements.
//
// 接口里**没有 Description()**：模型读到的那份唯一描述在 ToolSchemas()。
// 曾经注册处各带一份 desc，而没有任何读者——两处各写一份必然漂移
// （真实踩过：todo_write 的纪律写在注册参数上，模型读的是 ToolSchemas，等于没写）。
type Tool interface {
	Name() string
	// Execute runs the tool with JSON input and returns JSON output.
	Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

// Registry holds named tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register adds a tool (overwrites same name).
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Names lists registered tool names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	return out
}

// FuncTool adapts a function into a Tool.
type FuncTool struct {
	name string
	fn   func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

func NewFuncTool(name string, fn func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)) *FuncTool {
	return &FuncTool{name: name, fn: fn}
}

func (t *FuncTool) Name() string { return t.name }
func (t *FuncTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if t.fn == nil {
		return nil, fmt.Errorf("tool %s has no implementation", t.name)
	}
	return t.fn(ctx, input)
}
