// Package tools defines the librarian tool registry skeleton.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Tool is the interface every librarian tool implements.
type Tool interface {
	Name() string
	Description() string
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
	desc string
	fn   func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

func NewFuncTool(name, desc string, fn func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)) *FuncTool {
	return &FuncTool{name: name, desc: desc, fn: fn}
}

func (t *FuncTool) Name() string        { return t.name }
func (t *FuncTool) Description() string { return t.desc }
func (t *FuncTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if t.fn == nil {
		return nil, fmt.Errorf("tool %s has no implementation", t.name)
	}
	return t.fn(ctx, input)
}

// DefaultRegistry builds the P0 skeleton registry.
// Full LLM tool loop lands in a later phase; these stubs document the surface.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	// Read tools
	r.Register(NewFuncTool("search_works", "Search works by title/alias/FTS", stub("search_works")))
	r.Register(NewFuncTool("read_work", "Read work metadata + content_md", stub("read_work")))
	r.Register(NewFuncTool("read_doc", "Read document content", stub("read_doc")))
	r.Register(NewFuncTool("get_tree", "Get subtree", stub("get_tree")))
	r.Register(NewFuncTool("read_skill", "Read wiki-writing skill file", stub("read_skill")))
	r.Register(NewFuncTool("search_web", "Exa web search", stub("search_web")))
	r.Register(NewFuncTool("fetch_url", "Fetch an allowed search result page", stub("fetch_url")))
	r.Register(NewFuncTool("list_library", "List Emby/Komga/GameAtlas items", stub("list_library")))
	// Write tools
	r.Register(NewFuncTool("upsert_work", "Create or update a work node", stub("upsert_work")))
	r.Register(NewFuncTool("write_content", "Replace works/docs content_md", stub("write_content")))
	r.Register(NewFuncTool("patch_section", "Replace a section by ## anchor", stub("patch_section")))
	r.Register(NewFuncTool("upsert_relation", "Create typed relation", stub("upsert_relation")))
	r.Register(NewFuncTool("attach_library_link", "Attach external library link", stub("attach_library_link")))
	r.Register(NewFuncTool("create_doc", "Create a materials document", stub("create_doc")))
	r.Register(NewFuncTool("sync_library", "Sync stubs from library", stub("sync_library")))
	// Answer
	r.Register(NewFuncTool("answer", "Answer the user, optionally with work/doc ids", stub("answer")))
	return r
}

func stub(name string) func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	return func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(map[string]any{
			"ok":      false,
			"tool":    name,
			"message": "tool skeleton — full implementation in next phase",
			"echo":    json.RawMessage(append([]byte("null"), input...))[:len(input)+0],
		})
	}
}
